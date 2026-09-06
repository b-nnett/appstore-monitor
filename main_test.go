package main

import (
	"errors"
	"testing"
)

func testApp() App {
	return App{Name: "Depop Android", Type: "playstore", PackageID: "com.depop"}
}

func TestPackageID(t *testing.T) {
	tests := []struct {
		name string
		app  App
		want string
	}{
		{name: "explicit", app: App{PackageID: "com.depop"}, want: "com.depop"},
		{name: "URL", app: App{URL: "https://play.google.com/store/apps/details?hl=en&id=com.ebay.mobile&gl=GB"}, want: "com.ebay.mobile"},
		{name: "missing", app: App{}, want: ""},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := packageID(test.app); got != test.want {
				t.Fatalf("packageID() = %q, want %q", got, test.want)
			}
		})
	}
}

func TestCheckAppsSeedsWithoutNotification(t *testing.T) {
	app := testApp()
	cfg := &AppConfig{Apps: []App{app}}
	state := State{}
	notifications := 0
	fetcher := func(App) (*VersionInfo, int, error) {
		return &VersionInfo{Version: "2.406", Updated: "2026-09-03"}, 200, nil
	}
	notify := func(*AppConfig, App, *VersionInfo, StateEntry) error {
		notifications++
		return nil
	}

	changed, err := checkApps(cfg, state, fetcher, notify)
	if err != nil {
		t.Fatal(err)
	}
	if !changed {
		t.Fatal("expected state to change")
	}
	if notifications != 0 {
		t.Fatalf("sent %d notifications, want 0", notifications)
	}
	if got := state[app.key()]; got.Version != "2.406" || got.Updated != "2026-09-03" {
		t.Fatalf("unexpected state: %+v", got)
	}
}

func TestCheckAppsNotifiesOnChange(t *testing.T) {
	app := testApp()
	cfg := &AppConfig{Apps: []App{app}}
	state := State{app.key(): {Version: "2.405", Updated: "2026-08-27"}}
	notifications := 0
	fetcher := func(App) (*VersionInfo, int, error) {
		return &VersionInfo{Version: "2.406", Updated: "2026-09-03"}, 200, nil
	}
	notify := func(*AppConfig, App, *VersionInfo, StateEntry) error {
		notifications++
		return nil
	}

	changed, err := checkApps(cfg, state, fetcher, notify)
	if err != nil {
		t.Fatal(err)
	}
	if !changed || notifications != 1 {
		t.Fatalf("changed = %v, notifications = %d", changed, notifications)
	}
	if got := state[app.key()].Version; got != "2.406" {
		t.Fatalf("stored version = %q", got)
	}
}

func TestCheckAppsRetriesAfterNotificationFailure(t *testing.T) {
	app := testApp()
	cfg := &AppConfig{Apps: []App{app}}
	previous := StateEntry{Version: "2.405", Updated: "2026-08-27"}
	state := State{app.key(): previous}
	fetcher := func(App) (*VersionInfo, int, error) {
		return &VersionInfo{Version: "2.406", Updated: "2026-09-03"}, 200, nil
	}
	notify := func(*AppConfig, App, *VersionInfo, StateEntry) error {
		return errors.New("Discord unavailable")
	}

	changed, err := checkApps(cfg, state, fetcher, notify)
	if err == nil {
		t.Fatal("expected notification error")
	}
	if changed {
		t.Fatal("state should not be marked changed")
	}
	if got := state[app.key()]; got != previous {
		t.Fatalf("state advanced after failed notification: %+v", got)
	}
}
