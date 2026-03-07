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
	ShowFooter *bool  `json:"show_footer,omitempty"`
	Apps       []App  `json:"apps"`
}

type App struct {
	Name string `json:"name"`
	Type string `json:"type"` // "appstore" or "playstore"
	URL  string `json:"url"`
}

type State map[string]string // app name -> last version

type VersionInfo struct {
	Version      string
	ReleaseNotes string
	IconURL      string
}

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

// parseAppStoreURL extracts the country code and app ID from an App Store URL.
func parseAppStoreURL(url string) (country string, appID string, err error) {
	re := regexp.MustCompile(`apple\.com/(\w+)/app/.+/id(\d+)`)
	match := re.FindStringSubmatch(url)
	if len(match) < 3 {
		return "", "", fmt.Errorf("could not parse App Store URL: %s", url)
	}
	return match[1], match[2], nil
}

// fetchAppStoreVersion uses the iTunes Lookup API to get version and release notes.
func fetchAppStoreVersion(url string) (*VersionInfo, int, error) {
	country, appID, err := parseAppStoreURL(url)
	if err != nil {
		return nil, 0, err
	}

	apiURL := fmt.Sprintf("https://itunes.apple.com/lookup?id=%s&country=%s", appID, country)
	body, status, err := fetch(apiURL)
	if err != nil {
		return nil, status, err
	}

	var result struct {
		Results []struct {
			Version       string `json:"version"`
			ReleaseNotes  string `json:"releaseNotes"`
			ArtworkURL512 string `json:"artworkUrl512"`
		} `json:"results"`
	}
	if err := json.Unmarshal([]byte(body), &result); err != nil {
		return nil, status, fmt.Errorf("failed to parse iTunes API response: %w", err)
	}
	if len(result.Results) == 0 {
		return nil, status, fmt.Errorf("no results from iTunes API for app %s", appID)
	}

	return &VersionInfo{
		Version:      result.Results[0].Version,
		ReleaseNotes: result.Results[0].ReleaseNotes,
		IconURL:      result.Results[0].ArtworkURL512,
	}, status, nil
}

// fetchPlayStoreVersion scrapes the Play Store page for the "Updated on" date.
// Release notes are not available server-side from the Play Store.
func fetchPlayStoreVersion(url string) (*VersionInfo, int, error) {
	body, status, err := fetch(url)
	if err != nil {
		return nil, status, err
	}

	re := regexp.MustCompile(`Updated on</div><div class="xg1aie">([^<]+)</div>`)
	match := re.FindStringSubmatch(body)
	if len(match) < 2 {
		return nil, status, fmt.Errorf("version not found")
	}

	return &VersionInfo{
		Version: strings.TrimSpace(match[1]),
	}, status, nil
}

func sendDiscordWebhook(cfg *AppConfig, appName string, info *VersionInfo, oldVersion, url string) error {
	if oldVersion == "" {
		oldVersion = "N/A"
	}

	fields := []map[string]string{
		{
			"name":   "New Version",
			"value":  "`" + info.Version + "`",
			"inline": "true",
		},
		{
			"name":   "Old Version",
			"value":  "`" + oldVersion + "`",
			"inline": "true",
		},
	}

	if info.ReleaseNotes != "" {
		notes := info.ReleaseNotes
		if len(notes) > 1016 {
			notes = notes[:1013] + "..."
		}
		notes = "```\n" + notes + "\n```"
		fields = append(fields, map[string]string{
			"name":  "Release Notes",
			"value": notes,
		})
	}

	embed := map[string]interface{}{
		"author": map[string]string{
			"name": "New Update",
		},
		"title":  appName,
		"url":    url,
		"fields": fields,
		"color":  0x43b581,
	}

	showFooter := cfg.ShowFooter == nil || *cfg.ShowFooter
	if showFooter {
		embed["footer"] = map[string]string{
			"text": "github.com/b_nnett/appstore-monitor",
		}
	}

	if info.IconURL != "" {
		embed["thumbnail"] = map[string]string{
			"url": info.IconURL,
		}
	}
	payload := map[string]interface{}{
		"embeds": []interface{}{embed},
	}
	data, _ := json.Marshal(payload)
	resp, err := http.Post(cfg.WebhookURL, "application/json", strings.NewReader(string(data)))
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
		var info *VersionInfo
		var status int

		switch app.Type {
		case "appstore":
			info, status, err = fetchAppStoreVersion(app.URL)
		case "playstore":
			info, status, err = fetchPlayStoreVersion(app.URL)
		default:
			continue
		}

		if err != nil {
			fmt.Printf("Error fetching %s: %v\n", app.Name, err)
			continue
		}

		if state[app.Name] != info.Version {
			fmt.Printf("New version for %s: %s\n", app.Name, info.Version)
			if info.ReleaseNotes != "" {
				fmt.Printf("  Notes: %s\n", info.ReleaseNotes)
			}
			err = sendDiscordWebhook(cfg, app.Name, info, state[app.Name], app.URL)
			if err != nil {
				fmt.Printf("Webhook error: %v\n", err)
			}
			state[app.Name] = info.Version
			changed = true
		}

		logRequest(app.URL, status, info.Version)
	}

	if changed {
		saveState(state)
	}
}
