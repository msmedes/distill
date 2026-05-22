package distill

import (
	"bytes"
	"path/filepath"
	"strings"
	"testing"
)

func TestPromptInstallPreferencesDefaultsToWatchingBothAndUnified(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	defaults, err := defaultPreferences()
	if err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer

	prefs, err := promptInstallPreferences(strings.NewReader("\n\n\n"), &out, defaults)
	if err != nil {
		t.Fatal(err)
	}

	if !prefs.WatchClaude || !prefs.WatchCodex {
		t.Fatalf("expected both products watched, got %#v", prefs)
	}
	if prefs.PromotionMode != promotionModeUnified {
		t.Fatalf("expected unified promotion mode, got %s", prefs.PromotionMode)
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

	prefs, err := promptInstallPreferences(strings.NewReader("y\ny\nn\n"), &out, defaults)
	if err != nil {
		t.Fatal(err)
	}

	if prefs.PromotionMode != promotionModeSeparate {
		t.Fatalf("expected separate promotion mode, got %s", prefs.PromotionMode)
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
