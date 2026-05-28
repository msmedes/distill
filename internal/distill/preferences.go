package distill

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

type preferences struct {
	WatchClaude             bool   `json:"watch_claude"`
	WatchCodex              bool   `json:"watch_codex"`
	AutomaticWatch          bool   `json:"automatic_watch"`
	ExtractionBackend       string `json:"extraction_backend"`
	ExtractionModel         string `json:"extraction_model"`
	GenerationBackend       string `json:"generation_backend"`
	GenerationModel         string `json:"generation_model"`
	ClaudeCommandPath       string `json:"claude_command_path"`
	CodexCommandPath        string `json:"codex_command_path"`
	WatchInterval           string `json:"watch_interval"`
	QuietFor                string `json:"quiet_for"`
	WebRunBatchLimit        int    `json:"web_run_batch_limit"`
	MinUserTurns            int    `json:"min_user_turns"`
	MinUserChars            int    `json:"min_user_chars"`
	MaxTranscriptChars      int    `json:"max_transcript_chars"`
	MaxObservations         int    `json:"max_observations"`
	ZoomContextChars        int    `json:"zoom_context_chars"`
	NoSkip                  bool   `json:"no_skip"`
	PromotionMode           string `json:"promotion_mode"`
	AlwaysOnPath            string `json:"always_on_path"`
	ClaudeMDPath            string `json:"claude_md_path"`
	CodexAgentsPath         string `json:"codex_agents_path"`
	SkillsDir               string `json:"skills_dir"`
	ProjectInstructionsFile string `json:"project_instructions_file"`
	ProjectSkillsDir        string `json:"project_skills_dir"`
}

type modelOption struct {
	Value string
	Label string
	Hint  string
}

const (
	promotionModeUnified           = "unified"
	promotionModeSeparate          = "separate"
	extractionBackendClaude        = "claude"
	extractionBackendCodex         = "codex"
	defaultClaudeExtractionModel   = "haiku"
	defaultCodexExtractionModel    = "gpt-5.5"
	defaultGenerationBackend       = extractionBackendClaude
	defaultClaudeGenerationModel   = "opus"
	defaultCodexGenerationModel    = "gpt-5.5"
	defaultWatchInterval           = "1h"
	defaultQuietFor                = "10m"
	defaultWebRunBatchLimit        = 5
	defaultMinUserTurns            = 2
	defaultMinUserChars            = 200
	defaultMaxTranscriptChars      = 60_000
	defaultMaxObservations         = 80
	defaultZoomContextChars        = 2500
	defaultProjectInstructionsFile = "AGENTS.md"
	defaultProjectSkillsDir        = ".agents/skills"
)

var claudeExtractionModelOptions = []modelOption{
	{Value: "haiku", Label: "fastest", Hint: "Haiku"},
	{Value: "sonnet", Label: "balanced", Hint: "Sonnet"},
	{Value: "opus", Label: "smartest", Hint: "Opus"},
}

var codexExtractionModelOptions = []modelOption{
	{Value: "gpt-5.4-mini", Label: "fastest", Hint: "GPT-5.4 Mini"},
	{Value: "gpt-5.4", Label: "balanced", Hint: "GPT-5.4"},
	{Value: "gpt-5.5", Label: "smartest", Hint: "GPT-5.5"},
}

func defaultPreferences() (preferences, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return preferences{}, err
	}
	return preferences{
		WatchClaude:             true,
		WatchCodex:              true,
		AutomaticWatch:          true,
		ExtractionBackend:       extractionBackendClaude,
		ExtractionModel:         defaultClaudeExtractionModel,
		GenerationBackend:       defaultGenerationBackend,
		GenerationModel:         defaultClaudeGenerationModel,
		WatchInterval:           defaultWatchInterval,
		QuietFor:                defaultQuietFor,
		WebRunBatchLimit:        defaultWebRunBatchLimit,
		MinUserTurns:            defaultMinUserTurns,
		MinUserChars:            defaultMinUserChars,
		MaxTranscriptChars:      defaultMaxTranscriptChars,
		MaxObservations:         defaultMaxObservations,
		ZoomContextChars:        defaultZoomContextChars,
		PromotionMode:           promotionModeUnified,
		AlwaysOnPath:            filepath.Join(home, ".agents", "AGENTS.md"),
		ClaudeMDPath:            filepath.Join(home, ".claude", "CLAUDE.md"),
		CodexAgentsPath:         filepath.Join(home, ".codex", "AGENTS.md"),
		SkillsDir:               filepath.Join(home, ".agents", "skills"),
		ProjectInstructionsFile: defaultProjectInstructionsFile,
		ProjectSkillsDir:        defaultProjectSkillsDir,
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
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(b, &fields); err != nil {
		return preferences{}, fmt.Errorf("parsing %s: %w", p.preferencesFile, err)
	}
	if err := json.Unmarshal(b, &prefs); err != nil {
		return preferences{}, fmt.Errorf("parsing %s: %w", p.preferencesFile, err)
	}
	if _, ok := fields["extraction_model"]; !ok {
		prefs.ExtractionModel = ""
	}
	if _, ok := fields["generation_model"]; !ok {
		prefs.GenerationModel = ""
	}
	return normalizePreferences(prefs)
}

func readPreferencesFromDefaultPaths() (preferences, error) {
	p, err := resolvePaths()
	if err != nil {
		return preferences{}, err
	}
	return readPreferences(p)
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
	if prefs.ExtractionBackend == "" {
		prefs.ExtractionBackend = defaults.ExtractionBackend
	}
	if prefs.GenerationBackend == "" {
		prefs.GenerationBackend = defaults.GenerationBackend
	}
	if strings.TrimSpace(prefs.WatchInterval) == "" {
		prefs.WatchInterval = defaults.WatchInterval
	}
	if strings.TrimSpace(prefs.QuietFor) == "" {
		prefs.QuietFor = defaults.QuietFor
	}
	if prefs.WebRunBatchLimit <= 0 {
		prefs.WebRunBatchLimit = defaults.WebRunBatchLimit
	}
	if prefs.MinUserTurns <= 0 {
		prefs.MinUserTurns = defaults.MinUserTurns
	}
	if prefs.MinUserChars <= 0 {
		prefs.MinUserChars = defaults.MinUserChars
	}
	if prefs.MaxTranscriptChars <= 0 {
		prefs.MaxTranscriptChars = defaults.MaxTranscriptChars
	}
	if prefs.MaxObservations <= 0 {
		prefs.MaxObservations = defaults.MaxObservations
	}
	if prefs.ZoomContextChars <= 0 {
		prefs.ZoomContextChars = defaults.ZoomContextChars
	}
	if strings.TrimSpace(prefs.ProjectInstructionsFile) == "" {
		prefs.ProjectInstructionsFile = defaults.ProjectInstructionsFile
	}
	if strings.TrimSpace(prefs.ProjectSkillsDir) == "" {
		prefs.ProjectSkillsDir = defaults.ProjectSkillsDir
	}
	if prefs.PromotionMode != promotionModeUnified && prefs.PromotionMode != promotionModeSeparate {
		return preferences{}, fmt.Errorf("promotion mode must be %q or %q: %s", promotionModeUnified, promotionModeSeparate, prefs.PromotionMode)
	}
	if prefs.ExtractionBackend != extractionBackendClaude && prefs.ExtractionBackend != extractionBackendCodex {
		return preferences{}, fmt.Errorf("extraction backend must be %q or %q: %s", extractionBackendClaude, extractionBackendCodex, prefs.ExtractionBackend)
	}
	if prefs.GenerationBackend != extractionBackendClaude && prefs.GenerationBackend != extractionBackendCodex {
		return preferences{}, fmt.Errorf("generation backend must be %q or %q: %s", extractionBackendClaude, extractionBackendCodex, prefs.GenerationBackend)
	}
	prefs.ExtractionModel, err = normalizeModelForBackend("extraction model", prefs.ExtractionBackend, prefs.ExtractionModel, defaultExtractionModel(prefs.ExtractionBackend))
	if err != nil {
		return preferences{}, err
	}
	prefs.GenerationModel, err = normalizeModelForBackend("generation model", prefs.GenerationBackend, prefs.GenerationModel, defaultGenerationModel(prefs.GenerationBackend))
	if err != nil {
		return preferences{}, err
	}
	if _, err := time.ParseDuration(prefs.WatchInterval); err != nil {
		return preferences{}, fmt.Errorf("watch interval must be a Go duration: %w", err)
	}
	if _, err := time.ParseDuration(prefs.QuietFor); err != nil {
		return preferences{}, fmt.Errorf("session idle time must be a Go duration: %w", err)
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
	claudeCommand, err := expandOptionalCommandPath(strings.TrimSpace(prefs.ClaudeCommandPath))
	if err != nil {
		return preferences{}, err
	}
	codexCommand, err := expandOptionalCommandPath(strings.TrimSpace(prefs.CodexCommandPath))
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
		return preferences{}, fmt.Errorf("codex AGENTS.md path must be absolute: %s", prefs.CodexAgentsPath)
	}
	if !filepath.IsAbs(skillsDir) {
		return preferences{}, fmt.Errorf("skills directory must be absolute: %s", prefs.SkillsDir)
	}
	if filepath.IsAbs(prefs.ProjectInstructionsFile) || strings.Contains(prefs.ProjectInstructionsFile, string(filepath.Separator)) {
		return preferences{}, fmt.Errorf("project instructions file must be a filename, not a path: %s", prefs.ProjectInstructionsFile)
	}
	if filepath.IsAbs(prefs.ProjectSkillsDir) {
		return preferences{}, fmt.Errorf("project skills directory must be relative: %s", prefs.ProjectSkillsDir)
	}
	prefs.AlwaysOnPath = filepath.Clean(alwaysOnPath)
	prefs.ClaudeMDPath = filepath.Clean(claudePath)
	prefs.CodexAgentsPath = filepath.Clean(codexPath)
	prefs.SkillsDir = filepath.Clean(skillsDir)
	prefs.ClaudeCommandPath = claudeCommand
	prefs.CodexCommandPath = codexCommand
	prefs.ProjectInstructionsFile = filepath.Clean(prefs.ProjectInstructionsFile)
	prefs.ProjectSkillsDir = filepath.Clean(prefs.ProjectSkillsDir)
	return prefs, nil
}

func normalizeModelForBackend(field, backend, model, defaultModel string) (string, error) {
	model = strings.TrimSpace(model)
	if model == "" {
		return defaultModel, nil
	}
	options := modelOptionsForBackend(backend)
	if containsModelOption(options, model) {
		return model, nil
	}
	return "", fmt.Errorf("%s must be one of %s for %s backend: %s", field, strings.Join(modelOptionValues(options), ", "), backend, model)
}

func defaultExtractionModel(backend string) string {
	switch backend {
	case extractionBackendCodex:
		return defaultCodexExtractionModel
	default:
		return defaultClaudeExtractionModel
	}
}

func defaultGenerationModel(backend string) string {
	switch backend {
	case extractionBackendCodex:
		return defaultCodexGenerationModel
	default:
		return defaultClaudeGenerationModel
	}
}

func modelOptionsForBackend(backend string) []modelOption {
	switch backend {
	case extractionBackendCodex:
		return codexExtractionModelOptions
	default:
		return claudeExtractionModelOptions
	}
}

func modelOptionValues(options []modelOption) []string {
	values := make([]string, 0, len(options))
	for _, option := range options {
		values = append(values, option.Value)
	}
	return values
}

func containsModelOption(options []modelOption, value string) bool {
	for _, option := range options {
		if option.Value == value {
			return true
		}
	}
	return false
}

func expandOptionalCommandPath(path string) (string, error) {
	if path == "" {
		return "", nil
	}
	expanded, err := expandHomePath(path)
	if err != nil {
		return "", err
	}
	if strings.Contains(expanded, string(filepath.Separator)) && !filepath.IsAbs(expanded) {
		return "", fmt.Errorf("command path must be absolute or a command name: %s", path)
	}
	if filepath.IsAbs(expanded) {
		return filepath.Clean(expanded), nil
	}
	return expanded, nil
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
	target, err := p.promotionTarget(o, artifactAgentsMD, "")
	if err != nil {
		return p.AlwaysOnPath
	}
	return target.Path
}

func (p preferences) agentsDestination(o observation, requested observationScope) string {
	target, err := p.promotionTarget(o, artifactAgentsMD, requested)
	if err != nil {
		return p.AlwaysOnPath
	}
	return target.Path
}

func (p preferences) skillsDestination(o observation, requested observationScope) (string, error) {
	target, err := p.promotionTarget(o, artifactSkill, requested)
	if err != nil {
		return "", err
	}
	return target.Path, nil
}

type promotionTarget struct {
	Artifact string
	Scope    observationScope
	Path     string
}

func (p preferences) promotionTarget(o observation, artifact string, requested observationScope) (promotionTarget, error) {
	scope := resolvedPromotionScope(o, requested)
	switch artifact {
	case artifactAgentsMD:
		if scope == scopeProject && strings.TrimSpace(o.ProjectCWD) == "" {
			return promotionTarget{}, fmt.Errorf("project-scoped instructions require project cwd")
		}
		return promotionTarget{
			Artifact: artifact,
			Scope:    scope,
			Path:     p.agentsPathForScope(o, scope),
		}, nil
	case artifactSkill:
		if scope == scopeProject {
			if strings.TrimSpace(o.ProjectCWD) == "" {
				return promotionTarget{}, fmt.Errorf("project-scoped skill requires project cwd")
			}
			return promotionTarget{
				Artifact: artifact,
				Scope:    scope,
				Path:     filepath.Join(o.ProjectCWD, p.projectSkillsDir()),
			}, nil
		}
		return promotionTarget{
			Artifact: artifact,
			Scope:    scope,
			Path:     p.SkillsDir,
		}, nil
	}
	return promotionTarget{}, fmt.Errorf("unknown promotion artifact: %s", artifact)
}

func (p preferences) agentsPathForScope(o observation, scope observationScope) string {
	if scope == scopeProject {
		return filepath.Join(o.ProjectCWD, p.projectInstructionsFile())
	}
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

func (p preferences) projectInstructionsFile() string {
	if strings.TrimSpace(p.ProjectInstructionsFile) == "" {
		return defaultProjectInstructionsFile
	}
	return p.ProjectInstructionsFile
}

func (p preferences) projectSkillsDir() string {
	if strings.TrimSpace(p.ProjectSkillsDir) == "" {
		return defaultProjectSkillsDir
	}
	return p.ProjectSkillsDir
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
