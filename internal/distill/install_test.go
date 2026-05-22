package distill

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestPromptInstallPreferencesDefaultsToWatchingBothUnifiedAndAutomatic(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	defaults, err := defaultPreferences()
	if err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer

	prefs, err := promptInstallPreferences(strings.NewReader("\n\n\n\n"), &out, defaults)
	if err != nil {
		t.Fatal(err)
	}

	if !prefs.WatchClaude || !prefs.WatchCodex {
		t.Fatalf("expected both products watched, got %#v", prefs)
	}
	if prefs.PromotionMode != promotionModeUnified {
		t.Fatalf("expected unified promotion mode, got %s", prefs.PromotionMode)
	}
	if !prefs.AutomaticWatch {
		t.Fatal("expected automatic watcher by default")
	}
	if prefs.AlwaysOnPath != filepath.Join(home, ".agents", "AGENTS.md") {
		t.Fatalf("unexpected always-on path: %s", prefs.AlwaysOnPath)
	}
	if !strings.Contains(out.String(), "Watch Claude Code? [Y/n]") {
		t.Fatalf("expected Claude prompt, got %q", out.String())
	}
}

func TestPromptInstallPreferencesCanKeepSeparateDestinations(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	defaults, err := defaultPreferences()
	if err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer

	prefs, err := promptInstallPreferences(strings.NewReader("y\ny\nn\nn\n"), &out, defaults)
	if err != nil {
		t.Fatal(err)
	}

	if prefs.PromotionMode != promotionModeSeparate {
		t.Fatalf("expected separate promotion mode, got %s", prefs.PromotionMode)
	}
	if prefs.AutomaticWatch {
		t.Fatal("expected automatic watcher disabled")
	}
}

func TestPromptInstallPreferencesRequiresAWatchedProduct(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	defaults, err := defaultPreferences()
	if err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer

	if _, err := promptInstallPreferences(strings.NewReader("n\nn\n"), &out, defaults); err == nil {
		t.Fatal("expected no watched products to fail")
	}
}

func TestConfigureLaunchdWatcherWritesAndLoadsPlist(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	oldLaunchctl := launchctl
	t.Cleanup(func() { launchctl = oldLaunchctl })
	var calls [][]string
	launchctl = func(args ...string) error {
		calls = append(calls, append([]string(nil), args...))
		return nil
	}

	prefs, err := defaultPreferences()
	if err != nil {
		t.Fatal(err)
	}
	prefs.WatchClaude = false
	prefs.WatchCodex = true
	prefs.AutomaticWatch = true

	if err := configureLaunchdWatcher(prefs); err != nil {
		t.Fatal(err)
	}

	plistPath := filepath.Join(home, "Library", "LaunchAgents", launchdLabel+".plist")
	body := mustReadFile(t, plistPath)
	if !strings.Contains(body, "<string>watch</string>") || !strings.Contains(body, "<string>codex</string>") {
		t.Fatalf("unexpected plist:\n%s", body)
	}
	if len(calls) != 2 || calls[0][0] != "unload" || calls[1][0] != "load" {
		t.Fatalf("unexpected launchctl calls: %#v", calls)
	}
}

func TestConfigureLaunchdWatcherManualRemovesPlist(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	oldLaunchctl := launchctl
	t.Cleanup(func() { launchctl = oldLaunchctl })
	launchctl = func(args ...string) error { return nil }

	plistPath := filepath.Join(home, "Library", "LaunchAgents", launchdLabel+".plist")
	if err := writeLaunchdPlist(plistPath, "/tmp/distill", productAll); err != nil {
		t.Fatal(err)
	}
	prefs, err := defaultPreferences()
	if err != nil {
		t.Fatal(err)
	}
	prefs.AutomaticWatch = false

	if err := configureLaunchdWatcher(prefs); err != nil {
		t.Fatal(err)
	}
	if _, err := mustStatMissing(plistPath); err != nil {
		t.Fatal(err)
	}
}

func mustReadFile(t *testing.T, path string) string {
	t.Helper()
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(body)
}

func mustStatMissing(path string) (bool, error) {
	_, err := os.Stat(path)
	if err == nil {
		return false, fmt.Errorf("expected %s to be removed", path)
	}
	if !os.IsNotExist(err) {
		return false, err
	}
	return true, nil
}
