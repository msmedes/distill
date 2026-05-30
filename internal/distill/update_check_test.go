package distill

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestCheckForUpdatesFetchesAndCachesUntilEndOfDay(t *testing.T) {
	originalVersion := Version
	originalURL := latestReleaseURL
	Version = "v0.2.13"
	t.Setenv("DISTILL_TEST_HOMEBREW_SERVICE", "1")
	t.Cleanup(func() {
		Version = originalVersion
		latestReleaseURL = originalURL
	})

	hits := 0
	latest := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		hits++
		fmt.Fprint(w, `{"tag_name":"v0.2.14","html_url":"https://github.com/msmedes/distill/releases/tag/v0.2.14"}`)
	}))
	defer latest.Close()
	latestReleaseURL = latest.URL

	p := paths{stateDir: t.TempDir()}
	now := time.Date(2026, 5, 28, 15, 30, 0, 0, time.Local)
	got, err := checkForUpdates(context.Background(), p, latest.Client(), now)
	if err != nil {
		t.Fatal(err)
	}
	if !got.Available {
		t.Fatalf("expected update to be available: %#v", got)
	}
	if got.LatestVersion != "v0.2.14" || got.CurrentVersion != "v0.2.13" {
		t.Fatalf("unexpected versions: %#v", got)
	}
	if got.UpgradeCommand != "brew upgrade distill" {
		t.Fatalf("unexpected upgrade command: %q", got.UpgradeCommand)
	}
	if !got.ExpiresAt.Equal(nextLocalMidnight(now)) {
		t.Fatalf("expected end-of-day expiry, got %s", got.ExpiresAt)
	}

	latest.Close()
	cached, err := checkForUpdates(context.Background(), p, latest.Client(), now.Add(time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if hits != 1 {
		t.Fatalf("expected cached second check, got %d HTTP calls", hits)
	}
	if cached.LatestVersion != got.LatestVersion || !cached.Available {
		t.Fatalf("unexpected cached status: %#v", cached)
	}
}

func TestCheckForUpdatesRefreshesAfterCacheExpiry(t *testing.T) {
	originalVersion := Version
	originalURL := latestReleaseURL
	Version = "v0.2.13"
	t.Cleanup(func() {
		Version = originalVersion
		latestReleaseURL = originalURL
	})

	hits := 0
	latest := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		hits++
		fmt.Fprintf(w, `{"tag_name":"v0.2.%d","html_url":"https://example.test/releases/%d"}`, 13+hits, hits)
	}))
	defer latest.Close()
	latestReleaseURL = latest.URL

	p := paths{stateDir: t.TempDir()}
	now := time.Date(2026, 5, 28, 23, 30, 0, 0, time.Local)
	if _, err := checkForUpdates(context.Background(), p, latest.Client(), now); err != nil {
		t.Fatal(err)
	}
	refreshed, err := checkForUpdates(context.Background(), p, latest.Client(), nextLocalMidnight(now).Add(time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	if hits != 2 {
		t.Fatalf("expected cache refresh after expiry, got %d HTTP calls", hits)
	}
	if refreshed.LatestVersion != "v0.2.15" {
		t.Fatalf("expected refreshed latest version, got %#v", refreshed)
	}
}

func TestCheckForUpdatesSkipsDevVersion(t *testing.T) {
	originalVersion := Version
	originalURL := latestReleaseURL
	Version = ""
	t.Cleanup(func() {
		Version = originalVersion
		latestReleaseURL = originalURL
	})

	hits := 0
	latest := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		hits++
		fmt.Fprint(w, `{"tag_name":"v0.2.14"}`)
	}))
	defer latest.Close()
	latestReleaseURL = latest.URL

	got, err := checkForUpdates(context.Background(), paths{stateDir: t.TempDir()}, latest.Client(), time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if got.Available || hits != 0 {
		t.Fatalf("expected no check for dev version, status=%#v hits=%d", got, hits)
	}
}
