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

func TestNormalizeExtractionModelForBackend(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	prefs, err := normalizePreferences(preferences{
		ExtractionBackend: extractionBackendCodex,
		ExtractionModel:   "haiku",
		AlwaysOnPath:      filepath.Join(home, "AGENTS.md"),
		ClaudeMDPath:      filepath.Join(home, "CLAUDE.md"),
		CodexAgentsPath:   filepath.Join(home, "CODEX_AGENTS.md"),
		SkillsDir:         filepath.Join(home, "skills"),
	})
	if err != nil {
		t.Fatal(err)
	}
	if prefs.ExtractionModel != defaultCodexExtractionModel {
		t.Fatalf("expected codex default model, got %s", prefs.ExtractionModel)
	}

	prefs, err = normalizePreferences(preferences{
		ExtractionBackend: extractionBackendClaude,
		ExtractionModel:   "gpt-5.5",
		AlwaysOnPath:      filepath.Join(home, "AGENTS.md"),
		ClaudeMDPath:      filepath.Join(home, "CLAUDE.md"),
		CodexAgentsPath:   filepath.Join(home, "CODEX_AGENTS.md"),
		SkillsDir:         filepath.Join(home, "skills"),
	})
	if err != nil {
		t.Fatal(err)
	}
	if prefs.ExtractionModel != defaultClaudeExtractionModel {
		t.Fatalf("expected claude default model, got %s", prefs.ExtractionModel)
	}
}

func TestNormalizeGenerationModelForBackend(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	prefs, err := normalizePreferences(preferences{
		GenerationBackend: extractionBackendCodex,
		GenerationModel:   "opus",
		AlwaysOnPath:      filepath.Join(home, "AGENTS.md"),
		ClaudeMDPath:      filepath.Join(home, "CLAUDE.md"),
		CodexAgentsPath:   filepath.Join(home, "CODEX_AGENTS.md"),
		SkillsDir:         filepath.Join(home, "skills"),
	})
	if err != nil {
		t.Fatal(err)
	}
	if prefs.GenerationModel != defaultCodexGenerationModel {
		t.Fatalf("expected codex generation default, got %s", prefs.GenerationModel)
	}

	prefs, err = normalizePreferences(preferences{
		GenerationBackend: extractionBackendClaude,
		GenerationModel:   "gpt-5.5",
		AlwaysOnPath:      filepath.Join(home, "AGENTS.md"),
		ClaudeMDPath:      filepath.Join(home, "CLAUDE.md"),
		CodexAgentsPath:   filepath.Join(home, "CODEX_AGENTS.md"),
		SkillsDir:         filepath.Join(home, "skills"),
	})
	if err != nil {
		t.Fatal(err)
	}
	if prefs.GenerationModel != defaultClaudeGenerationModel {
		t.Fatalf("expected claude generation default, got %s", prefs.GenerationModel)
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

func TestPortableProjectDestinations(t *testing.T) {
	prefs := preferences{
		PromotionMode: promotionModeUnified,
		AlwaysOnPath:  "/home/me/.agents/AGENTS.md",
		SkillsDir:     "/home/me/.agents/skills",
	}
	obs := observation{
		Scope:      scopeProject,
		ProjectCWD: "/work/distill",
	}

	if got := prefs.agentsDestination(obs, ""); got != filepath.Join("/work/distill", "AGENTS.md") {
		t.Fatalf("unexpected project AGENTS.md path: %s", got)
	}
	skillsDir, err := prefs.skillsDestination(obs, "")
	if err != nil {
		t.Fatal(err)
	}
	if want := filepath.Join("/work/distill", ".agents", "skills"); skillsDir != want {
		t.Fatalf("unexpected project skills dir: %s", skillsDir)
	}

	userSkillsDir, err := prefs.skillsDestination(obs, scopeUser)
	if err != nil {
		t.Fatal(err)
	}
	if userSkillsDir != prefs.SkillsDir {
		t.Fatalf("expected requested user scope to use user skills dir, got %s", userSkillsDir)
	}
}
