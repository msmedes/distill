package distill

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestPromptInstallPreferencesDefaultsToWatchingBothUnifiedAndAutomatic(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	defaults, err := defaultPreferences()
	if err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer

	plan, err := promptInstallPlan(strings.NewReader("\n\n\n\n\n\n"), &out, defaults)
	if err != nil {
		t.Fatal(err)
	}
	prefs := plan.preferences

	if !prefs.WatchClaude || !prefs.WatchCodex {
		t.Fatalf("expected both products watched, got %#v", prefs)
	}
	if prefs.PromotionMode != promotionModeUnified {
		t.Fatalf("expected unified promotion mode, got %s", prefs.PromotionMode)
	}
	if prefs.ExtractionBackend != extractionBackendClaude {
		t.Fatalf("expected claude extraction backend, got %s", prefs.ExtractionBackend)
	}
	if !prefs.AutomaticWatch {
		t.Fatal("expected automatic watcher by default")
	}
	if plan.bootstrapCount != installBootstrapRecentLimit {
		t.Fatalf("expected default bootstrap count %d, got %d", installBootstrapRecentLimit, plan.bootstrapCount)
	}
	if prefs.AlwaysOnPath != filepath.Join(home, ".agents", "AGENTS.md") {
		t.Fatalf("unexpected always-on path: %s", prefs.AlwaysOnPath)
	}
	if !strings.Contains(out.String(), "Watch Claude Code? [Y/n]") {
		t.Fatalf("expected Claude prompt, got %q", out.String())
	}
	if !strings.Contains(out.String(), "Extraction backend for observations (claude/codex) [claude]") {
		t.Fatalf("expected extraction backend prompt, got %q", out.String())
	}
	if !strings.Contains(out.String(), "Process recent quiet sessions now? (0 to skip) [15]") {
		t.Fatalf("expected bootstrap prompt, got %q", out.String())
	}
}

func TestPromptInstallPreferencesCanKeepSeparateDestinations(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	defaults, err := defaultPreferences()
	if err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer

	plan, err := promptInstallPlan(strings.NewReader("y\ny\ncodex\nn\n0\nn\n"), &out, defaults)
	if err != nil {
		t.Fatal(err)
	}
	prefs := plan.preferences

	if prefs.PromotionMode != promotionModeSeparate {
		t.Fatalf("expected separate promotion mode, got %s", prefs.PromotionMode)
	}
	if prefs.ExtractionBackend != extractionBackendCodex {
		t.Fatalf("expected codex extraction backend, got %s", prefs.ExtractionBackend)
	}
	if plan.bootstrapCount != 0 {
		t.Fatalf("expected bootstrap count 0, got %d", plan.bootstrapCount)
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

func TestMarkExistingQuietSessionsProcessedBaselinesOnlyQuietSessions(t *testing.T) {
	dir := t.TempDir()
	now := time.Date(2026, 5, 26, 12, 0, 0, 0, time.UTC)
	claudeProjects := filepath.Join(dir, "claude-projects")
	codexSessions := filepath.Join(dir, "codex-sessions")
	for _, path := range []string{claudeProjects, codexSessions} {
		if err := os.MkdirAll(path, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	p := paths{
		claudeProjects:  claudeProjects,
		codexSessions:   codexSessions,
		stateDir:        filepath.Join(dir, "state"),
		stateFile:       filepath.Join(dir, "state", "state.json"),
		observationFile: filepath.Join(dir, "state", "observations.jsonl"),
	}
	writeInstallClaudeSession(t, p.claudeProjects, "old-claude", now.Add(-time.Hour))
	writeInstallClaudeSession(t, p.claudeProjects, "recent-claude", now.Add(-time.Minute))
	writeCodexSession(t, p.codexSessions, "old-codex", now.Add(-time.Hour))
	writeCodexSession(t, p.codexSessions, "recent-codex", now.Add(-time.Minute))

	count, err := markExistingQuietSessionsProcessed(p, productAll, 10*time.Minute, now)
	if err != nil {
		t.Fatal(err)
	}
	if count != 2 {
		t.Fatalf("expected 2 quiet sessions marked, got %d", count)
	}
	state, err := readState(p.stateFile)
	if err != nil {
		t.Fatal(err)
	}
	if !processedSession(state, sessionMeta{product: productClaude, sessionID: "old-claude"}) {
		t.Fatalf("old Claude session not marked: %#v", state)
	}
	if !processedSession(state, sessionMeta{product: productCodex, sessionID: "old-codex"}) {
		t.Fatalf("old Codex session not marked: %#v", state)
	}
	if processedSession(state, sessionMeta{product: productClaude, sessionID: "recent-claude"}) {
		t.Fatalf("recent Claude session should remain eligible: %#v", state)
	}
	if processedSession(state, sessionMeta{product: productCodex, sessionID: "recent-codex"}) {
		t.Fatalf("recent Codex session should remain eligible: %#v", state)
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

func TestConfigureLaunchdWatcherUsesHomebrewServiceForHomebrewBinary(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("DISTILL_TEST_HOMEBREW_SERVICE", "1")
	oldLaunchctl := launchctl
	oldBrewServices := brewServices
	t.Cleanup(func() {
		launchctl = oldLaunchctl
		brewServices = oldBrewServices
	})
	launchctl = func(args ...string) error { return nil }
	var brewCalls [][]string
	brewServices = func(args ...string) error {
		brewCalls = append(brewCalls, append([]string(nil), args...))
		return nil
	}

	prefs, err := defaultPreferences()
	if err != nil {
		t.Fatal(err)
	}
	prefs.AutomaticWatch = true

	if err := configureLaunchdWatcher(prefs); err != nil {
		t.Fatal(err)
	}

	plistPath := filepath.Join(home, "Library", "LaunchAgents", launchdLabel+".plist")
	if _, err := mustStatMissing(plistPath); err != nil {
		t.Fatal(err)
	}
	if len(brewCalls) != 1 || brewCalls[0][0] != "restart" || brewCalls[0][1] != "distill" {
		t.Fatalf("expected brew services restart distill, got %#v", brewCalls)
	}
}

func TestConfigureLaunchdWatcherStopsHomebrewServiceWhenAutomaticWatchDisabled(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("DISTILL_TEST_HOMEBREW_SERVICE", "1")
	oldLaunchctl := launchctl
	oldBrewServices := brewServices
	t.Cleanup(func() {
		launchctl = oldLaunchctl
		brewServices = oldBrewServices
	})
	launchctl = func(args ...string) error { return nil }
	var brewCalls [][]string
	brewServices = func(args ...string) error {
		brewCalls = append(brewCalls, append([]string(nil), args...))
		return nil
	}

	prefs, err := defaultPreferences()
	if err != nil {
		t.Fatal(err)
	}
	prefs.AutomaticWatch = false

	if err := configureLaunchdWatcher(prefs); err != nil {
		t.Fatal(err)
	}

	if len(brewCalls) != 1 || brewCalls[0][0] != "stop" || brewCalls[0][1] != "distill" {
		t.Fatalf("expected brew services stop distill, got %#v", brewCalls)
	}
}

func TestPrintInstallSummaryShowsWebUINextStep(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	prefs, err := defaultPreferences()
	if err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer

	printInstallSummary(&out, prefs)
	got := out.String()
	if !strings.Contains(got, "open the web UI: distill serve") {
		t.Fatalf("summary missing serve command:\n%s", got)
	}
	if !strings.Contains(got, "extraction backend: claude") {
		t.Fatalf("summary missing extraction backend:\n%s", got)
	}
	if !strings.Contains(got, "generation backend: claude") {
		t.Fatalf("summary missing generation backend:\n%s", got)
	}
	if !strings.Contains(got, "then visit: http://127.0.0.1:7373") {
		t.Fatalf("summary missing web UI URL:\n%s", got)
	}
	if !strings.Contains(got, "agent guide: distill agents") {
		t.Fatalf("summary missing agent guide command:\n%s", got)
	}
}

func TestPrintInstallSummaryShowsHomebrewServiceCommand(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("DISTILL_TEST_HOMEBREW_SERVICE", "1")
	prefs, err := defaultPreferences()
	if err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer

	printInstallSummary(&out, prefs)
	got := out.String()
	if !strings.Contains(got, "automatic watch: Homebrew service") {
		t.Fatalf("summary missing Homebrew service:\n%s", got)
	}
	if !strings.Contains(got, "service command: brew services restart distill") {
		t.Fatalf("summary missing brew services command:\n%s", got)
	}
}

func TestPrintInstallSummaryKeepsManualWatchCommand(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	prefs, err := defaultPreferences()
	if err != nil {
		t.Fatal(err)
	}
	prefs.AutomaticWatch = false
	var out bytes.Buffer

	printInstallSummary(&out, prefs)
	got := out.String()
	if !strings.Contains(got, "run manually: distill watch --product all") {
		t.Fatalf("summary missing manual watch command:\n%s", got)
	}
	if !strings.Contains(got, "open the web UI: distill serve") {
		t.Fatalf("summary missing serve command:\n%s", got)
	}
}

func writeInstallClaudeSession(t *testing.T, root, id string, mtime time.Time) {
	t.Helper()
	projectDir := filepath.Join(root, "-tmp-project")
	if err := os.MkdirAll(projectDir, 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(projectDir, id+".jsonl")
	body := `{"type":"user","uuid":"user-1","message":{"content":[{"type":"text","text":"hello"}]}}` + "\n"
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(path, mtime, mtime); err != nil {
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
