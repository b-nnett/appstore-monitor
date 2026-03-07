package main

import (
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"regexp"
	"strings"
	"time"
)

type AppConfig struct {
	WebhookURL string `json:"webhook_url"`
	Apps       []App  `json:"apps"`
}

type App struct {
	Name string `json:"name"`
	Type string `json:"type"` // "appstore" or "playstore"
	URL  string `json:"url"`
}

type State map[string]string // app name -> last version

var logger *log.Logger

func initLogger() {
	f, err := os.OpenFile("app.log", os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		panic(err)
	}
	logger = log.New(f, "", log.LstdFlags)
}

func logRequest(url string, status int, version string) {
	logger.Printf("%s - %d - %s\n", url, status, version)
}

func loadConfig() (*AppConfig, error) {
	data, err := os.ReadFile("config.json")
	if err != nil {
		return nil, err
	}
	var cfg AppConfig
	err = json.Unmarshal(data, &cfg)
	return &cfg, err
}

func loadState() (State, error) {
	data, err := os.ReadFile("state.json")
	if os.IsNotExist(err) {
		return State{}, nil
	}
	if err != nil {
		return nil, err
	}
	var state State
	err = json.Unmarshal(data, &state)
	return state, err
}

func saveState(state State) error {
	data, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile("state.json", data, 0644)
}

func fetch(url string) (string, int, error) {
	client := &http.Client{Timeout: 10 * time.Second}
	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return "", 0, err
	}
	req.Header.Set("User-Agent", "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/120.0.0.0 Safari/537.36")
	resp, err := client.Do(req)
	if err != nil {
		return "", 0, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	return string(body), resp.StatusCode, err
}

func parseAppStoreVersion(html string) (string, error) {
	re := regexp.MustCompile(`<h4[^>]*>Version ([^<]+)</h4>`)
	match := re.FindStringSubmatch(html)
	if len(match) < 2 {
		return "", fmt.Errorf("version not found")
	}
	return strings.TrimSpace(match[1]), nil
}

func parsePlayStoreVersion(html string) (string, error) {
	// Play Store version is in a div with class "xg1aie" after "Updated on"
	re := regexp.MustCompile(`Updated on</div><div class="xg1aie">([^<]+)</div>`)
	match := re.FindStringSubmatch(html)
	if len(match) < 2 {
		return "", fmt.Errorf("version not found")
	}
	return strings.TrimSpace(match[1]), nil
}

func sendDiscordWebhook(webhookURL, appName, version, oldVersion, url string) error {
	if oldVersion == "" {
		oldVersion = "N/A"
	}

	embed := map[string]interface{}{
		"title": fmt.Sprintf("Updated: %s", appName),
		"url":   url,
		"fields": []map[string]string{
			{
				"name":   "New Version",
				"value":  "`" + version + "`",
				"inline": "true",
			},
			{
				"name":   "Old Version",
				"value":  "`" + oldVersion + "`",
				"inline": "true",
			},
		},
		"color": 0x43b581,
		"footer": map[string]string{
			"text": "github.com/b_nnett/appstore-monitor",
		},
	}
	payload := map[string]interface{}{
		"embeds": []interface{}{embed},
	}
	data, _ := json.Marshal(payload)
	resp, err := http.Post(webhookURL, "application/json", strings.NewReader(string(data)))
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		return fmt.Errorf("webhook failed: %s", resp.Status)
	}
	return nil
}

func main() {
	cfg, err := loadConfig()
	if err != nil {
		panic(err)
	}
	state, err := loadState()
	if err != nil {
		panic(err)
	}
	changed := false

	initLogger()
	for _, app := range cfg.Apps {
		html, status, err := fetch(app.URL)

		if err != nil {
			fmt.Printf("Error fetching %s: %v\n", app.Name, err)
			continue
		}

		var version string

		switch app.Type {
		case "appstore":
			version, err = parseAppStoreVersion(html)
		case "playstore":
			version, err = parsePlayStoreVersion(html)
		default:
			continue
		}

		if err != nil {
			fmt.Printf("Error parsing %s: %v\n", app.Name, err) // should probably send a webhook here too, but alas
			continue
		}

		if state[app.Name] != version {
			fmt.Printf("New version for %s: %s\n", app.Name, version)
			err = sendDiscordWebhook(cfg.WebhookURL, app.Name, version, state[app.Name], app.URL)
			if err != nil {
				fmt.Printf("Webhook error: %v\n", err)
			}
			state[app.Name] = version
			changed = true
		}

		logRequest(app.URL, status, version)
	}

	if changed {
		saveState(state)
	}
}
