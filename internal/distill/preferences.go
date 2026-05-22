package distill

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

type preferences struct {
	WatchClaude     bool   `json:"watch_claude"`
	WatchCodex      bool   `json:"watch_codex"`
	PromotionMode   string `json:"promotion_mode"`
	AlwaysOnPath    string `json:"always_on_path"`
	ClaudeMDPath    string `json:"claude_md_path"`
	CodexAgentsPath string `json:"codex_agents_path"`
	SkillsDir       string `json:"skills_dir"`
}

const (
	promotionModeUnified  = "unified"
	promotionModeSeparate = "separate"
)

func defaultPreferences() (preferences, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return preferences{}, err
	}
	return preferences{
		WatchClaude:     true,
		WatchCodex:      true,
		PromotionMode:   promotionModeUnified,
		AlwaysOnPath:    filepath.Join(home, ".agents", "AGENTS.md"),
		ClaudeMDPath:    filepath.Join(home, ".claude", "CLAUDE.md"),
		CodexAgentsPath: filepath.Join(home, ".codex", "AGENTS.md"),
		SkillsDir:       filepath.Join(home, ".agents", "skills"),
	}, nil
}

func readPreferences(p paths) (preferences, error) {
	prefs, err := defaultPreferences()
	if err != nil {
		return preferences{}, err
	}
	b, err := os.ReadFile(p.preferencesFile)
	if errors.Is(err, os.ErrNotExist) {
		return prefs, nil
	}
	if err != nil {
		return preferences{}, err
	}
	if len(strings.TrimSpace(string(b))) == 0 {
		return prefs, nil
	}
	if err := json.Unmarshal(b, &prefs); err != nil {
		return preferences{}, fmt.Errorf("parsing %s: %w", p.preferencesFile, err)
	}
	return normalizePreferences(prefs)
}

func writePreferences(p paths, prefs preferences) error {
	prefs, err := normalizePreferences(prefs)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(p.preferencesFile), 0o755); err != nil {
		return err
	}
	b, err := json.MarshalIndent(prefs, "", "  ")
	if err != nil {
		return err
	}
	b = append(b, '\n')
	tmp := p.preferencesFile + ".tmp"
	if err := os.WriteFile(tmp, b, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, p.preferencesFile)
}

func normalizePreferences(prefs preferences) (preferences, error) {
	defaults, err := defaultPreferences()
	if err != nil {
		return preferences{}, err
	}
	if strings.TrimSpace(prefs.ClaudeMDPath) == "" {
		prefs.ClaudeMDPath = defaults.ClaudeMDPath
	}
	if strings.TrimSpace(prefs.AlwaysOnPath) == "" {
		prefs.AlwaysOnPath = defaults.AlwaysOnPath
	}
	if strings.TrimSpace(prefs.CodexAgentsPath) == "" {
		prefs.CodexAgentsPath = defaults.CodexAgentsPath
	}
	if strings.TrimSpace(prefs.SkillsDir) == "" {
		prefs.SkillsDir = defaults.SkillsDir
	}
	if prefs.PromotionMode == "" {
		prefs.PromotionMode = defaults.PromotionMode
	}
	if prefs.PromotionMode != promotionModeUnified && prefs.PromotionMode != promotionModeSeparate {
		return preferences{}, fmt.Errorf("promotion mode must be %q or %q: %s", promotionModeUnified, promotionModeSeparate, prefs.PromotionMode)
	}
	if !prefs.WatchClaude && !prefs.WatchCodex {
		prefs.WatchClaude = defaults.WatchClaude
		prefs.WatchCodex = defaults.WatchCodex
	}
	alwaysOnPath, err := expandHomePath(strings.TrimSpace(prefs.AlwaysOnPath))
	if err != nil {
		return preferences{}, err
	}
	claudePath, err := expandHomePath(strings.TrimSpace(prefs.ClaudeMDPath))
	if err != nil {
		return preferences{}, err
	}
	codexPath, err := expandHomePath(strings.TrimSpace(prefs.CodexAgentsPath))
	if err != nil {
		return preferences{}, err
	}
	skillsDir, err := expandHomePath(strings.TrimSpace(prefs.SkillsDir))
	if err != nil {
		return preferences{}, err
	}
	if !filepath.IsAbs(alwaysOnPath) {
		return preferences{}, fmt.Errorf("always-on path must be absolute: %s", prefs.AlwaysOnPath)
	}
	if !filepath.IsAbs(claudePath) {
		return preferences{}, fmt.Errorf("CLAUDE.md path must be absolute: %s", prefs.ClaudeMDPath)
	}
	if !filepath.IsAbs(codexPath) {
		return preferences{}, fmt.Errorf("Codex AGENTS.md path must be absolute: %s", prefs.CodexAgentsPath)
	}
	if !filepath.IsAbs(skillsDir) {
		return preferences{}, fmt.Errorf("skills directory must be absolute: %s", prefs.SkillsDir)
	}
	prefs.AlwaysOnPath = filepath.Clean(alwaysOnPath)
	prefs.ClaudeMDPath = filepath.Clean(claudePath)
	prefs.CodexAgentsPath = filepath.Clean(codexPath)
	prefs.SkillsDir = filepath.Clean(skillsDir)
	return prefs, nil
}

func (p preferences) watchProduct() product {
	switch {
	case p.WatchClaude && p.WatchCodex:
		return productAll
	case p.WatchClaude:
		return productClaude
	case p.WatchCodex:
		return productCodex
	default:
		return productAll
	}
}

func (p preferences) alwaysOnDestination(o observation) string {
	if p.PromotionMode == promotionModeUnified {
		return p.AlwaysOnPath
	}
	switch primaryObservationProduct(o) {
	case productClaude:
		return p.ClaudeMDPath
	case productCodex:
		return p.CodexAgentsPath
	default:
		return p.AlwaysOnPath
	}
}

func primaryObservationProduct(o observation) product {
	var found product
	for _, e := range o.Evidence {
		p := e.Product
		if p == "" {
			p = productClaude
		}
		if found == "" {
			found = p
			continue
		}
		if found != p {
			return productAll
		}
	}
	return found
}

func expandHomePath(path string) (string, error) {
	if path == "~" || strings.HasPrefix(path, "~/") {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		if path == "~" {
			return home, nil
		}
		return filepath.Join(home, path[2:]), nil
	}
	return path, nil
}
