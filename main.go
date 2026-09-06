package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"regexp"
	"time"

	playapp "github.com/n0madic/google-play-scraper/pkg/app"
)

const (
	defaultConfigPath = "config.json"
	defaultStatePath  = "state.json"
	defaultLogPath    = "app.log"
)

type AppConfig struct {
	WebhookURL      string `json:"webhook_url"`
	ShowFooter      *bool  `json:"show_footer,omitempty"`
	NotifyFirstSeen bool   `json:"notify_on_first_seen,omitempty"`
	Apps            []App  `json:"apps"`
}

type App struct {
	Name      string `json:"name"`
	Type      string `json:"type"` // "appstore" or "playstore"
	URL       string `json:"url,omitempty"`
	PackageID string `json:"package_id,omitempty"`
	Country   string `json:"country,omitempty"`
	Language  string `json:"language,omitempty"`
}

type StateEntry struct {
	Version string `json:"version,omitempty"`
	Updated string `json:"updated,omitempty"`
}

type State map[string]StateEntry

type VersionInfo struct {
	Version      string
	Updated      string
	ReleaseNotes string
	IconURL      string
}

type FetchVersionFunc func(App) (*VersionInfo, int, error)
type NotifyFunc func(*AppConfig, App, *VersionInfo, StateEntry) error

var logger *log.Logger

func envOrDefault(name, fallback string) string {
	if value := os.Getenv(name); value != "" {
		return value
	}
	return fallback
}

func initLogger() error {
	f, err := os.OpenFile(envOrDefault("APPSTORE_MONITOR_LOG", defaultLogPath), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0600)
	if err != nil {
		return err
	}
	logger = log.New(f, "", log.LstdFlags)
	return nil
}

func logRequest(url string, status int, value string) {
	if logger != nil {
		logger.Printf("%s - %d - %s", url, status, value)
	}
}

func loadConfig() (*AppConfig, error) {
	data, err := os.ReadFile(envOrDefault("APPSTORE_MONITOR_CONFIG", defaultConfigPath))
	if err != nil {
		return nil, err
	}
	var cfg AppConfig
	if err := json.Unmarshal(data, &cfg); err != nil {
		return nil, err
	}
	if webhook := os.Getenv("DISCORD_WEBHOOK_URL"); webhook != "" {
		cfg.WebhookURL = webhook
	}
	if len(cfg.Apps) == 0 {
		return nil, errors.New("config contains no apps")
	}
	return &cfg, nil
}

func loadState() (State, error) {
	data, err := os.ReadFile(envOrDefault("APPSTORE_MONITOR_STATE", defaultStatePath))
	if os.IsNotExist(err) {
		return State{}, nil
	}
	if err != nil {
		return nil, err
	}
	var state State
	if err := json.Unmarshal(data, &state); err != nil {
		return nil, err
	}
	return state, nil
}

func saveState(state State) error {
	data, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(envOrDefault("APPSTORE_MONITOR_STATE", defaultStatePath), data, 0600)
}

func fetch(url string) (string, int, error) {
	client := &http.Client{Timeout: 20 * time.Second}
	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		return "", 0, err
	}
	req.Header.Set("User-Agent", "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 Chrome/120 Safari/537.36")
	resp, err := client.Do(req)
	if err != nil {
		return "", 0, err
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if err != nil {
		return "", resp.StatusCode, err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", resp.StatusCode, fmt.Errorf("request failed: %s", resp.Status)
	}
	return string(body), resp.StatusCode, nil
}

func parseAppStoreURL(url string) (country string, appID string, err error) {
	re := regexp.MustCompile(`apple\.com/([[:alnum:]-]+)/app/.+/id([0-9]+)`)
	match := re.FindStringSubmatch(url)
	if len(match) < 3 {
		return "", "", fmt.Errorf("could not parse App Store URL: %s", url)
	}
	return match[1], match[2], nil
}

func fetchAppStoreVersion(app App) (*VersionInfo, int, error) {
	country, appID, err := parseAppStoreURL(app.URL)
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

func packageID(app App) string {
	if app.PackageID != "" {
		return app.PackageID
	}
	re := regexp.MustCompile(`[?&]id=([^&]+)`)
	match := re.FindStringSubmatch(app.URL)
	if len(match) == 2 {
		return match[1]
	}
	return ""
}

func fetchPlayStoreVersion(app App) (*VersionInfo, int, error) {
	id := packageID(app)
	if id == "" {
		return nil, 0, fmt.Errorf("missing Play Store package ID for %s", app.Name)
	}
	country := app.Country
	if country == "" {
		country = "gb"
	}
	language := app.Language
	if language == "" {
		language = "en"
	}

	details := playapp.New(id, playapp.Options{Country: country, Language: language})
	if err := details.LoadDetails(); err != nil {
		return nil, 0, fmt.Errorf("load Google Play details for %s: %w", id, err)
	}
	if details.Title == "" || details.Updated.IsZero() {
		return nil, 200, fmt.Errorf("incomplete Google Play details for %s", id)
	}

	return &VersionInfo{
		Version:      details.Version,
		Updated:      details.Updated.UTC().Format("2006-01-02"),
		ReleaseNotes: details.RecentChanges,
		IconURL:      details.Icon,
	}, 200, nil
}

func fetchVersion(app App) (*VersionInfo, int, error) {
	switch app.Type {
	case "appstore":
		return fetchAppStoreVersion(app)
	case "playstore":
		return fetchPlayStoreVersion(app)
	default:
		return nil, 0, fmt.Errorf("unsupported store type %q", app.Type)
	}
}

func (app App) key() string {
	if app.Type == "playstore" {
		return "playstore:" + packageID(app)
	}
	return app.Type + ":" + app.URL
}

func (app App) storeURL() string {
	if app.URL != "" {
		return app.URL
	}
	if app.Type == "playstore" {
		return "https://play.google.com/store/apps/details?id=" + packageID(app)
	}
	return ""
}

func (info VersionInfo) state() StateEntry {
	return StateEntry{Version: info.Version, Updated: info.Updated}
}

func (entry StateEntry) display() string {
	if entry.Version != "" {
		return entry.Version
	}
	if entry.Updated != "" {
		return "Updated " + entry.Updated
	}
	return "unknown"
}

func truncateRunes(value string, max int) string {
	runes := []rune(value)
	if len(runes) <= max {
		return value
	}
	return string(runes[:max-3]) + "..."
}

func sendDiscordWebhook(cfg *AppConfig, app App, info *VersionInfo, previous StateEntry) error {
	if cfg.WebhookURL == "" {
		return errors.New("Discord webhook is not configured")
	}

	fields := []map[string]any{
		{"name": "New Version", "value": "`" + info.state().display() + "`", "inline": true},
		{"name": "Old Version", "value": "`" + previous.display() + "`", "inline": true},
	}
	if info.Updated != "" {
		fields = append(fields, map[string]any{"name": "Google Play Updated", "value": info.Updated, "inline": true})
	}
	if info.ReleaseNotes != "" {
		fields = append(fields, map[string]any{"name": "Release Notes", "value": truncateRunes(info.ReleaseNotes, 1000)})
	}

	embed := map[string]any{
		"author": map[string]string{"name": "Android marketplace update detected"},
		"title":  app.Name,
		"url":    app.storeURL(),
		"fields": fields,
		"color":  0x43b581,
	}
	if cfg.ShowFooter == nil || *cfg.ShowFooter {
		embed["footer"] = map[string]string{"text": "github.com/b-nnett/appstore-monitor"}
	}
	if info.IconURL != "" {
		embed["thumbnail"] = map[string]string{"url": info.IconURL}
	}

	data, err := json.Marshal(map[string]any{"embeds": []any{embed}})
	if err != nil {
		return err
	}
	req, err := http.NewRequest(http.MethodPost, cfg.WebhookURL, bytes.NewReader(data))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := (&http.Client{Timeout: 20 * time.Second}).Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("webhook failed: %s", resp.Status)
	}
	return nil
}

func checkApps(cfg *AppConfig, state State, fetcher FetchVersionFunc, notify NotifyFunc) (bool, error) {
	changed := false
	var failures []error

	for _, app := range cfg.Apps {
		info, status, err := fetcher(app)
		if err != nil {
			failures = append(failures, fmt.Errorf("%s: %w", app.Name, err))
			continue
		}
		current := info.state()
		previous, seen := state[app.key()]
		logRequest(app.storeURL(), status, current.display())

		if !seen {
			if cfg.NotifyFirstSeen {
				if err := notify(cfg, app, info, previous); err != nil {
					failures = append(failures, fmt.Errorf("notify %s: %w", app.Name, err))
					continue
				}
			}
			fmt.Printf("Seeded %s: %s\n", app.Name, current.display())
			state[app.key()] = current
			changed = true
			continue
		}

		if previous == current {
			continue
		}
		fmt.Printf("New version for %s: %s (previously %s)\n", app.Name, current.display(), previous.display())
		if err := notify(cfg, app, info, previous); err != nil {
			failures = append(failures, fmt.Errorf("notify %s: %w", app.Name, err))
			continue
		}
		state[app.key()] = current
		changed = true
	}

	return changed, errors.Join(failures...)
}

func main() {
	if err := initLogger(); err != nil {
		log.Fatal(err)
	}
	cfg, err := loadConfig()
	if err != nil {
		log.Fatal(err)
	}
	state, err := loadState()
	if err != nil {
		log.Fatal(err)
	}

	changed, checkErr := checkApps(cfg, state, fetchVersion, sendDiscordWebhook)
	if changed {
		if err := saveState(state); err != nil {
			log.Fatal(err)
		}
	}
	if checkErr != nil {
		log.Fatal(checkErr)
	}
}
