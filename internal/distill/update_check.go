package distill

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

var latestReleaseURL = "https://api.github.com/repos/msmedes/distill/releases/latest"

type updateStatus struct {
	Available      bool      `json:"available"`
	CurrentVersion string    `json:"current_version"`
	LatestVersion  string    `json:"latest_version"`
	ReleaseURL     string    `json:"release_url"`
	UpgradeCommand string    `json:"upgrade_command"`
	CheckedAt      time.Time `json:"checked_at"`
	ExpiresAt      time.Time `json:"expires_at"`
}

type latestReleaseResponse struct {
	TagName string `json:"tag_name"`
	HTMLURL string `json:"html_url"`
}

func checkForUpdates(ctx context.Context, p paths, client *http.Client, now time.Time) (updateStatus, error) {
	current := versionString()
	status := updateStatus{
		CurrentVersion: current,
		CheckedAt:      now,
		ExpiresAt:      nextLocalMidnight(now),
	}
	if _, ok := parseReleaseVersion(current); !ok {
		return status, nil
	}
	if cached, ok := readCachedUpdateStatus(p, current, now); ok {
		return cached, nil
	}
	if client == nil {
		client = http.DefaultClient
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, latestReleaseURL, nil)
	if err != nil {
		return status, err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("User-Agent", "distill/"+current)

	resp, err := client.Do(req)
	if err != nil {
		return status, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return status, fmt.Errorf("latest release check returned %s", resp.Status)
	}
	var latest latestReleaseResponse
	if err := json.NewDecoder(resp.Body).Decode(&latest); err != nil {
		return status, err
	}
	if _, ok := parseReleaseVersion(latest.TagName); !ok {
		return status, fmt.Errorf("latest release has invalid tag: %s", latest.TagName)
	}
	status.LatestVersion = latest.TagName
	status.ReleaseURL = latest.HTMLURL
	if compareReleaseVersions(latest.TagName, current) > 0 {
		status.Available = true
		status.UpgradeCommand = "brew upgrade distill"
		if !runningFromHomebrew() {
			status.UpgradeCommand = ""
		}
	}
	if err := writeCachedUpdateStatus(p, status); err != nil {
		return status, err
	}
	return status, nil
}

func readCachedUpdateStatus(p paths, current string, now time.Time) (updateStatus, bool) {
	path := updateCheckCachePath(p)
	if path == "" {
		return updateStatus{}, false
	}
	b, err := os.ReadFile(path)
	if err != nil {
		return updateStatus{}, false
	}
	var cached updateStatus
	if err := json.Unmarshal(b, &cached); err != nil {
		return updateStatus{}, false
	}
	if cached.CurrentVersion != current || !now.Before(cached.ExpiresAt) {
		return updateStatus{}, false
	}
	return cached, true
}

func writeCachedUpdateStatus(p paths, status updateStatus) error {
	path := updateCheckCachePath(p)
	if path == "" {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	b, err := json.MarshalIndent(status, "", "  ")
	if err != nil {
		return err
	}
	b = append(b, '\n')
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, b, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

func updateCheckCachePath(p paths) string {
	if p.stateDir != "" {
		return filepath.Join(p.stateDir, "update-check.json")
	}
	if p.preferencesFile != "" {
		return filepath.Join(filepath.Dir(p.preferencesFile), "update-check.json")
	}
	return ""
}

func nextLocalMidnight(t time.Time) time.Time {
	local := t.Local()
	y, m, d := local.Date()
	return time.Date(y, m, d+1, 0, 0, 0, 0, local.Location())
}

func compareReleaseVersions(a, b string) int {
	av, aok := parseReleaseVersion(a)
	bv, bok := parseReleaseVersion(b)
	if !aok || !bok {
		return 0
	}
	for i := range av {
		if av[i] > bv[i] {
			return 1
		}
		if av[i] < bv[i] {
			return -1
		}
	}
	return 0
}

func parseReleaseVersion(v string) ([3]int, bool) {
	var out [3]int
	parts := strings.Split(strings.TrimPrefix(strings.TrimSpace(v), "v"), ".")
	if len(parts) != 3 {
		return out, false
	}
	for i, part := range parts {
		n, err := strconv.Atoi(part)
		if err != nil || n < 0 {
			return out, false
		}
		out[i] = n
	}
	return out, true
}
