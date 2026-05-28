package distill

import (
	"fmt"
	"log"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

type settingsData struct {
	Preferences        preferences
	WatchProduct       product
	ExtractionBackend  string
	ClaudeModels       []modelOption
	CodexModels        []modelOption
	ClaudeCommand      string
	CodexCommand       string
	IndexedClaude      int
	IndexedCodex       int
	ProcessedClaude    int
	ProcessedCodex     int
	UnprocessedClaude  int
	UnprocessedCodex   int
	SessionIndexStatus string
	LastWatcherError   watcherErrorInfo
}

type watcherErrorInfo struct {
	HasError  bool
	Message   string
	TimeLabel string
}

func (s *server) handleSettings(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		s.renderSettings(w)
		return
	case http.MethodPost:
	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if err := r.ParseForm(); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	prefs, err := readPreferences(s.paths)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if _, fullSettings := r.Form["extraction_backend"]; fullSettings {
		prefs.WatchClaude = r.FormValue("watch_claude") == "on"
		prefs.WatchCodex = r.FormValue("watch_codex") == "on"
		prefs.AutomaticWatch = r.FormValue("automatic_watch") == "on"
		prefs.ExtractionBackend = r.FormValue("extraction_backend")
		prefs.ExtractionModel = extractionModelFromForm(r, prefs.ExtractionBackend)
		prefs.GenerationBackend = r.FormValue("generation_backend")
		prefs.GenerationModel = generationModelFromForm(r, prefs.GenerationBackend)
		prefs.ClaudeCommandPath = r.FormValue("claude_command_path")
		prefs.CodexCommandPath = r.FormValue("codex_command_path")
		prefs.WatchInterval = r.FormValue("watch_interval")
		prefs.QuietFor = r.FormValue("quiet_for")
		prefs.WebRunBatchLimit = parsePositiveIntForm(r, "web_run_batch_limit", prefs.WebRunBatchLimit)
		prefs.MinUserTurns = parsePositiveIntForm(r, "min_user_turns", prefs.MinUserTurns)
		prefs.MinUserChars = parsePositiveIntForm(r, "min_user_chars", prefs.MinUserChars)
		prefs.MaxTranscriptChars = parsePositiveIntForm(r, "max_transcript_chars", prefs.MaxTranscriptChars)
		prefs.MaxObservations = parsePositiveIntForm(r, "max_observations", prefs.MaxObservations)
		prefs.ZoomContextChars = parsePositiveIntForm(r, "zoom_context_chars", prefs.ZoomContextChars)
		prefs.NoSkip = r.FormValue("skip_low_signal") != "on"
		prefs.PromotionMode = r.FormValue("promotion_mode")
		prefs.ClaudeMDPath = r.FormValue("claude_md_path")
		prefs.CodexAgentsPath = r.FormValue("codex_agents_path")
		prefs.ProjectInstructionsFile = r.FormValue("project_instructions_file")
		prefs.ProjectSkillsDir = r.FormValue("project_skills_dir")
	}
	prefs.AlwaysOnPath = r.FormValue("always_on_path")
	prefs.SkillsDir = r.FormValue("skills_dir")
	if err := writePreferences(s.paths, prefs); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	target := "/settings"
	if ref := r.Header.Get("Referer"); strings.HasPrefix(ref, "http://"+r.Host) || strings.HasPrefix(ref, "https://"+r.Host) {
		target = ref
	}
	http.Redirect(w, r, target, http.StatusSeeOther)
}

func extractionModelFromForm(r *http.Request, backend string) string {
	return backendModelFromForm(r, backend, "extraction_model", "claude_extraction_model", "codex_extraction_model")
}

func generationModelFromForm(r *http.Request, backend string) string {
	return backendModelFromForm(r, backend, "generation_model", "claude_generation_model", "codex_generation_model")
}

func backendModelFromForm(r *http.Request, backend, fallbackName, claudeName, codexName string) string {
	switch backend {
	case extractionBackendCodex:
		if model := r.FormValue(codexName); strings.TrimSpace(model) != "" {
			return model
		}
	case extractionBackendClaude:
		if model := r.FormValue(claudeName); strings.TrimSpace(model) != "" {
			return model
		}
	}
	return r.FormValue(fallbackName)
}

func parsePositiveIntForm(r *http.Request, name string, fallback int) int {
	n, err := strconv.Atoi(strings.TrimSpace(r.FormValue(name)))
	if err != nil || n <= 0 {
		return fallback
	}
	return n
}

func (s *server) renderSettings(w http.ResponseWriter) {
	prefs, err := readPreferences(s.paths)
	if err != nil {
		http.Error(w, fmt.Sprintf("reading preferences: %v", err), http.StatusInternalServerError)
		return
	}
	data := settingsData{
		Preferences:        prefs,
		WatchProduct:       prefs.watchProduct(),
		ExtractionBackend:  prefs.ExtractionBackend,
		ClaudeModels:       modelOptionsForBackend(extractionBackendClaude),
		CodexModels:        modelOptionsForBackend(extractionBackendCodex),
		ClaudeCommand:      resolvedCommandLabel("claude", prefs.ClaudeCommandPath),
		CodexCommand:       resolvedCommandLabel("codex", prefs.CodexCommandPath),
		SessionIndexStatus: "index unavailable",
		LastWatcherError:   latestWatcherError(s.paths),
	}
	data.IndexedClaude, data.IndexedCodex, data.ProcessedClaude, data.ProcessedCodex, data.UnprocessedClaude, data.UnprocessedCodex = settingsSessionCounts(s.paths)
	if status, err := explainSessionIndex(s.paths); err == nil {
		data.SessionIndexStatus = status
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := s.settingsTmpl.Execute(w, data); err != nil {
		log.Printf("settings template execute: %v", err)
	}
}

func resolvedCommandLabel(name, override string) string {
	if name == "claude" {
		path, _ := resolveClaudeCommand(override)
		return path
	}
	if strings.TrimSpace(override) != "" {
		path, _ := resolveCommand(name, override)
		return path
	}
	path, err := exec.LookPath(name)
	if err == nil {
		return path
	}
	if found := findExecutable(name, filepath.SplitList(extendedExecutablePath())); found != "" {
		return found
	}
	return "not found"
}

func settingsSessionCounts(p paths) (indexedClaude, indexedCodex, processedClaude, processedCodex, unprocessedClaude, unprocessedCodex int) {
	st := newStore(p)
	state, err := st.readState()
	if err == nil {
		for key := range state.ProcessedSessions {
			productName, _ := splitSessionStateKey(key)
			switch productName {
			case productClaude:
				processedClaude++
			case productCodex:
				processedCodex++
			}
		}
	}
	indexed, err := indexedSessionsByStateKey(p)
	if err != nil {
		return
	}
	for key, session := range indexed {
		processed := false
		if state != nil {
			processed = processedSession(state, session)
			if !processed && session.product == productClaude {
				_, processed = state.ProcessedSessions[key]
			}
		}
		switch session.product {
		case productClaude:
			indexedClaude++
			if !processed {
				unprocessedClaude++
			}
		case productCodex:
			indexedCodex++
			if !processed {
				unprocessedCodex++
			}
		}
	}
	return
}

func latestWatcherError(p paths) watcherErrorInfo {
	path := filepath.Join(p.stateDir, "watch.log")
	b, err := os.ReadFile(path)
	if err != nil {
		return watcherErrorInfo{Message: "none recorded"}
	}
	info, _ := os.Stat(path)
	lines := strings.Split(string(b), "\n")
	for i := len(lines) - 1; i >= 0; i-- {
		line := strings.TrimSpace(lines[i])
		if strings.Contains(line, "error:") || strings.Contains(line, "failed") {
			return watcherErrorInfo{
				HasError:  true,
				Message:   line,
				TimeLabel: watcherErrorTimeLabel(line, info),
			}
		}
	}
	return watcherErrorInfo{Message: "none recorded"}
}

func watcherErrorTimeLabel(line string, info os.FileInfo) string {
	if t, ok := parseWatcherLogTimestamp(line); ok {
		return formatSettingsTime(t)
	}
	if info != nil {
		return "log updated " + formatSettingsTime(info.ModTime())
	}
	return "time unknown"
}

func parseWatcherLogTimestamp(line string) (time.Time, bool) {
	if fields := strings.Fields(line); len(fields) > 0 {
		if t, err := time.Parse(time.RFC3339, fields[0]); err == nil {
			return t, true
		}
	}
	for _, layout := range []string{"2006/01/02 15:04:05", "2006-01-02 15:04:05"} {
		if len(line) >= len(layout) {
			if t, err := time.ParseInLocation(layout, line[:len(layout)], time.Local); err == nil {
				return t, true
			}
		}
	}
	return time.Time{}, false
}

func formatSettingsTime(t time.Time) string {
	return t.Local().Format("2006-01-02 15:04:05 MST")
}
