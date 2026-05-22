package distill

import (
	"path/filepath"
	"testing"
)

func TestPreferencesExpandHomeAndRequireAbsolutePaths(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	prefs, err := normalizePreferences(preferences{
		AlwaysOnPath:    "~/agents/AGENTS.md",
		ClaudeMDPath:    "~/claude/CLAUDE.md",
		CodexAgentsPath: "~/codex/AGENTS.md",
		SkillsDir:       "~/agents/skills",
	})
	if err != nil {
		t.Fatal(err)
	}

	if prefs.AlwaysOnPath != filepath.Join(home, "agents", "AGENTS.md") {
		t.Fatalf("unexpected always-on path: %s", prefs.AlwaysOnPath)
	}
	if prefs.ClaudeMDPath != filepath.Join(home, "claude", "CLAUDE.md") {
		t.Fatalf("unexpected CLAUDE.md path: %s", prefs.ClaudeMDPath)
	}
	if prefs.CodexAgentsPath != filepath.Join(home, "codex", "AGENTS.md") {
		t.Fatalf("unexpected Codex AGENTS.md path: %s", prefs.CodexAgentsPath)
	}
	if prefs.SkillsDir != filepath.Join(home, "agents", "skills") {
		t.Fatalf("unexpected skills dir: %s", prefs.SkillsDir)
	}

	if _, err := normalizePreferences(preferences{AlwaysOnPath: "AGENTS.md", SkillsDir: "~/skills"}); err == nil {
		t.Fatal("expected relative always-on path to fail")
	}
}

func TestAlwaysOnDestinationRoutesByPromotionMode(t *testing.T) {
	prefs := preferences{
		PromotionMode:   promotionModeSeparate,
		AlwaysOnPath:    "/home/me/.agents/AGENTS.md",
		ClaudeMDPath:    "/home/me/.claude/CLAUDE.md",
		CodexAgentsPath: "/home/me/.codex/AGENTS.md",
	}
	claudeObs := observation{Evidence: []evidence{{Product: productClaude, SessionID: "claude-session"}}}
	codexObs := observation{Evidence: []evidence{{Product: productCodex, SessionID: "codex-session"}}}
	mixedObs := observation{Evidence: []evidence{{Product: productClaude}, {Product: productCodex}}}

	if got := prefs.alwaysOnDestination(claudeObs); got != prefs.ClaudeMDPath {
		t.Fatalf("expected claude path, got %s", got)
	}
	if got := prefs.alwaysOnDestination(codexObs); got != prefs.CodexAgentsPath {
		t.Fatalf("expected codex path, got %s", got)
	}
	if got := prefs.alwaysOnDestination(mixedObs); got != prefs.AlwaysOnPath {
		t.Fatalf("expected shared path for mixed evidence, got %s", got)
	}

	prefs.PromotionMode = promotionModeUnified
	if got := prefs.alwaysOnDestination(codexObs); got != prefs.AlwaysOnPath {
		t.Fatalf("expected unified path, got %s", got)
	}
}
