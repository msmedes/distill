package distill

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestObservationTemplateParsesAfterBlockExtraction(t *testing.T) {
	s := &server{}
	if err := s.loadTemplates(); err != nil {
		t.Fatal(err)
	}
}

func TestRunProcessesNewestUnrunSessions(t *testing.T) {
	s := testServer(t)
	if err := writePreferences(s.paths, preferences{WatchClaude: false, WatchCodex: true}); err != nil {
		t.Fatal(err)
	}
	now := time.Now().Add(-time.Hour)
	for i := 1; i <= 6; i++ {
		writeCodexSession(t, s.paths.codexSessions, "session-"+strconv.Itoa(i), now.Add(time.Duration(i)*time.Minute))
	}

	req := httptest.NewRequest(http.MethodPost, "/run", nil)
	rr := httptest.NewRecorder()
	s.handleRun(rr, req)

	if rr.Code != http.StatusSeeOther {
		t.Fatalf("unexpected run status: %d\n%s", rr.Code, rr.Body.String())
	}
	state, err := readState(s.paths.stateFile)
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"session-6", "session-5", "session-4", "session-3", "session-2"} {
		if !processedSession(state, sessionMeta{product: productCodex, sessionID: id}) {
			t.Fatalf("expected %s to be processed, state=%#v", id, state.ProcessedSessions)
		}
	}
	if processedSession(state, sessionMeta{product: productCodex, sessionID: "session-1"}) {
		t.Fatalf("oldest session should remain unprocessed after capped run: %#v", state.ProcessedSessions)
	}
}

func TestIndexShowsRunControlWhenEmpty(t *testing.T) {
	s := testServer(t)
	if err := writePreferences(s.paths, preferences{WatchClaude: false, WatchCodex: true}); err != nil {
		t.Fatal(err)
	}
	writeCodexSession(t, s.paths.codexSessions, "session-1", time.Now().Add(-time.Hour))
	if err := writeState(s.paths.stateFile, &stateFile{ProcessedSessions: map[string]string{
		sessionStateKey(productCodex, "session-1"): "2026-05-24T12:30:00Z",
	}}); err != nil {
		t.Fatal(err)
	}

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	rr := httptest.NewRecorder()
	s.handleIndex(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("unexpected status: %d\n%s", rr.Code, rr.Body.String())
	}
	body := rr.Body.String()
	for _, want := range []string{`action="/run"`, `href="/sessions"`, "1 session processed", "up to 5", "no observations yet"} {
		if !strings.Contains(body, want) {
			t.Fatalf("index missing %q:\n%s", want, body)
		}
	}
}

func TestIndexDoesNotScanSessionsForRunControl(t *testing.T) {
	s := testServer(t)
	if err := writePreferences(s.paths, preferences{WatchClaude: false, WatchCodex: true}); err != nil {
		t.Fatal(err)
	}
	if err := os.RemoveAll(s.paths.codexSessions); err != nil {
		t.Fatal(err)
	}

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	rr := httptest.NewRecorder()
	s.handleIndex(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("unexpected status: %d\n%s", rr.Code, rr.Body.String())
	}
	body := rr.Body.String()
	if !strings.Contains(body, "up to 5") {
		t.Fatalf("index did not render run control without session directory:\n%s", body)
	}
	if strings.Contains(body, "promotion destinations") {
		t.Fatalf("index rendered duplicate promotion destination settings:\n%s", body)
	}
}

func TestHelpPageRenders(t *testing.T) {
	s := &server{}
	if err := s.loadTemplates(); err != nil {
		t.Fatal(err)
	}

	req := httptest.NewRequest(http.MethodGet, "/help", nil)
	rr := httptest.NewRecorder()
	s.handleHelp(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("unexpected status: %d", rr.Code)
	}
	body := rr.Body.String()
	for _, want := range []string{"promotion destinations", "What are instructions?", "What is a skill?"} {
		if !strings.Contains(body, want) {
			t.Fatalf("help page missing %q", want)
		}
	}
}

func TestSettingsPageRendersMockConfiguration(t *testing.T) {
	s := testServer(t)
	if err := writePreferences(s.paths, preferences{
		WatchClaude:     true,
		WatchCodex:      true,
		AutomaticWatch:  true,
		PromotionMode:   promotionModeUnified,
		AlwaysOnPath:    filepath.Join(t.TempDir(), "AGENTS.md"),
		ClaudeMDPath:    filepath.Join(t.TempDir(), "CLAUDE.md"),
		CodexAgentsPath: filepath.Join(t.TempDir(), "CODEX_AGENTS.md"),
		SkillsDir:       filepath.Join(t.TempDir(), "skills"),
	}); err != nil {
		t.Fatal(err)
	}

	req := httptest.NewRequest(http.MethodGet, "/settings", nil)
	rr := httptest.NewRecorder()
	s.handleSettings(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("unexpected status: %d\n%s", rr.Code, rr.Body.String())
	}
	body := rr.Body.String()
	for _, want := range []string{
		"distill<span class=\"dot\">.</span> settings",
		"extraction",
		"claude -p",
		"codex exec",
		"watched products",
		"target policy",
		"diagnostics",
		"save settings",
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("settings page missing %q:\n%s", want, body)
		}
	}
}

func TestSettingsPostPersistsConfiguration(t *testing.T) {
	s := testServer(t)
	form := url.Values{
		"extraction_backend":        {"codex"},
		"extraction_model":          {"gpt-5.1-codex-max"},
		"claude_command_path":       {"/usr/local/bin/claude"},
		"codex_command_path":        {"/usr/local/bin/codex"},
		"watch_claude":              {"on"},
		"watch_interval":            {"30m"},
		"quiet_for":                 {"15m"},
		"web_run_batch_limit":       {"12"},
		"min_user_turns":            {"3"},
		"min_user_chars":            {"250"},
		"max_transcript_chars":      {"70000"},
		"max_observations":          {"90"},
		"zoom_context_chars":        {"3000"},
		"skip_low_signal":           {"on"},
		"promotion_mode":            {promotionModeSeparate},
		"always_on_path":            {filepath.Join(t.TempDir(), "AGENTS.md")},
		"claude_md_path":            {filepath.Join(t.TempDir(), "CLAUDE.md")},
		"codex_agents_path":         {filepath.Join(t.TempDir(), "CODEX_AGENTS.md")},
		"skills_dir":                {filepath.Join(t.TempDir(), "skills")},
		"project_instructions_file": {"PROJECT_AGENTS.md"},
		"project_skills_dir":        {".distill/skills"},
	}
	req := httptest.NewRequest(http.MethodPost, "/settings", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rr := httptest.NewRecorder()

	s.handleSettings(rr, req)

	if rr.Code != http.StatusSeeOther {
		t.Fatalf("unexpected status: %d\n%s", rr.Code, rr.Body.String())
	}
	prefs, err := readPreferences(s.paths)
	if err != nil {
		t.Fatal(err)
	}
	if prefs.ExtractionBackend != extractionBackendCodex || prefs.ExtractionModel != "gpt-5.1-codex-max" {
		t.Fatalf("unexpected backend prefs: %#v", prefs)
	}
	if !prefs.WatchClaude || prefs.WatchCodex {
		t.Fatalf("unexpected watched products: %#v", prefs)
	}
	if prefs.WatchInterval != "30m" || prefs.QuietFor != "15m" || prefs.WebRunBatchLimit != 12 {
		t.Fatalf("unexpected cadence prefs: %#v", prefs)
	}
	if prefs.NoSkip {
		t.Fatal("expected checked skip_low_signal to keep local skip enabled")
	}
	if prefs.PromotionMode != promotionModeSeparate || prefs.ProjectInstructionsFile != "PROJECT_AGENTS.md" || prefs.ProjectSkillsDir != ".distill/skills" {
		t.Fatalf("unexpected target prefs: %#v", prefs)
	}
}

func TestSessionsPageRendersProcessedSessions(t *testing.T) {
	s := testServer(t)
	if err := writePreferences(s.paths, preferences{WatchClaude: false, WatchCodex: true}); err != nil {
		t.Fatal(err)
	}
	path := writeCodexSessionWithTurns(t, s.paths.codexSessions, "session-abc123", []string{
		`{"type":"session_meta","payload":{"id":"session-abc123","cwd":"/tmp/distill"}}`,
		`{"timestamp":"2026-05-24T12:00:00Z","type":"response_item","payload":{"type":"message","role":"user","content":[{"type":"input_text","text":"Please wire the processed sessions page into the header."}]}}`,
		`{"timestamp":"2026-05-24T12:42:00Z","type":"response_item","payload":{"type":"message","role":"assistant","content":[{"type":"output_text","text":"Done."}]}}`,
	}, time.Date(2026, 5, 24, 12, 45, 0, 0, time.UTC))
	writeCodexSessionWithTurns(t, s.paths.codexSessions, "session-newer", []string{
		`{"type":"session_meta","payload":{"id":"session-newer","cwd":"/tmp/distill"}}`,
		`{"timestamp":"2026-05-25T09:00:00Z","type":"response_item","payload":{"type":"message","role":"user","content":[{"type":"input_text","text":"Newer session should sort first by session time."}]}}`,
	}, time.Date(2026, 5, 25, 9, 0, 0, 0, time.UTC))
	if path == "" {
		t.Fatal("empty session path")
	}
	if err := refreshSessionIndex(context.Background(), s.paths, productCodex); err != nil {
		t.Fatal(err)
	}
	if err := writeState(s.paths.stateFile, &stateFile{ProcessedSessions: map[string]string{
		sessionStateKey(productCodex, "session-abc123"): "2026-05-24T13:00:00Z",
		sessionStateKey(productCodex, "session-newer"):  "2026-05-24T11:00:00Z",
		"legacy-claude-session":                         "2026-05-23T13:00:00Z",
	}}); err != nil {
		t.Fatal(err)
	}

	req := httptest.NewRequest(http.MethodGet, "/sessions?filter=all", nil)
	rr := httptest.NewRecorder()
	s.handleSessions(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("unexpected status: %d\n%s", rr.Code, rr.Body.String())
	}
	body := rr.Body.String()
	for _, want := range []string{
		"distill<span class=\"dot\">.</span> sessions",
		"3 processed",
		"codex",
		"Newer session should sort first by session time.",
		"Please wire the processed sessions page into the header.",
		"distill",
		"42m",
		"cd &#39;/tmp/distill&#39; &amp;&amp; codex resume &#39;session-newer&#39;",
		"copy cmd",
		"source missing",
		"legacy-",
		"untitled session",
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("sessions page missing %q:\n%s", want, body)
		}
	}
	if strings.Index(body, "Newer session should sort first by session time.") > strings.Index(body, "Please wire the processed sessions page into the header.") {
		t.Fatalf("newer session sorted after older session:\n%s", body)
	}
}

func TestSessionsPageDefaultsToNewestProcessedSessions(t *testing.T) {
	s := testServer(t)
	state := &stateFile{ProcessedSessions: map[string]string{}}
	var evidenceRecords []evidence
	base := time.Date(2026, 5, 24, 12, 0, 0, 0, time.UTC)
	for i := range 105 {
		id := fmt.Sprintf("session-%03d", i)
		writeCodexSessionWithTurns(t, s.paths.codexSessions, id, []string{
			`{"type":"session_meta","payload":{"id":"` + id + `","cwd":"/tmp/distill"}}`,
			`{"timestamp":"2026-05-24T12:00:00Z","type":"response_item","payload":{"type":"message","role":"user","content":[{"type":"input_text","text":"Session ` + id + ` title."}]}}`,
		}, base.Add(time.Duration(i)*time.Minute))
		state.ProcessedSessions[sessionStateKey(productCodex, id)] = base.Add(time.Duration(i) * time.Minute).Format(time.RFC3339)
		evidenceRecords = append(evidenceRecords, evidence{
			SessionID:  id,
			Product:    productCodex,
			TurnRefs:   []string{"turn 1"},
			Quote:      "Session " + id + " title.",
			RecordedAt: base.Add(time.Duration(i) * time.Minute).Format(time.RFC3339),
		})
	}
	if err := writeObservations(s.paths.observationFile, []observation{{
		ID:        "obs_0001",
		Claim:     "test",
		Type:      typeWorkflow,
		Scope:     scopeUser,
		FirstSeen: base.Format(time.RFC3339),
		LastSeen:  base.Format(time.RFC3339),
		Evidence:  evidenceRecords,
		Status:    statusActive,
	}}); err != nil {
		t.Fatal(err)
	}
	if err := refreshSessionIndex(context.Background(), s.paths, productCodex); err != nil {
		t.Fatal(err)
	}
	if err := writeState(s.paths.stateFile, state); err != nil {
		t.Fatal(err)
	}

	req := httptest.NewRequest(http.MethodGet, "/sessions", nil)
	rr := httptest.NewRecorder()
	s.handleSessions(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("unexpected status: %d\n%s", rr.Code, rr.Body.String())
	}
	body := rr.Body.String()
	for _, want := range []string{"105 with observations", "105 processed", "showing 100 newest", "show 500", "Session session-104 title.", "1 evidence"} {
		if !strings.Contains(body, want) {
			t.Fatalf("sessions page missing %q:\n%s", want, body)
		}
	}
	if strings.Contains(body, "Session session-004 title.") {
		t.Fatalf("default sessions page rendered beyond newest 100:\n%s", body)
	}
}

func TestAlwaysOnPromotionPreviewsThenCommitsExactOutput(t *testing.T) {
	s := testServer(t)
	restore := stubAlwaysOnGenerator(func(current string, o observation) string {
		return current + "\n- " + o.Claim + "\n"
	})
	defer restore()
	alwaysOn := filepath.Join(t.TempDir(), "AGENTS.md")
	original := "# Instructions\n"
	if err := os.WriteFile(alwaysOn, []byte(original), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := writePreferences(s.paths, preferences{
		PromotionMode: promotionModeUnified,
		AlwaysOnPath:  alwaysOn,
		SkillsDir:     filepath.Join(t.TempDir(), "skills"),
	}); err != nil {
		t.Fatal(err)
	}
	if err := writeObservations(s.paths.observationFile, []observation{{
		ID:     "obs_0003",
		Claim:  "Prefer direct answers",
		Type:   typePreference,
		Status: statusActive,
	}}); err != nil {
		t.Fatal(err)
	}

	req := httptest.NewRequest(http.MethodPost, "/obs/obs_0003/promote-claude-md", nil)
	rr := httptest.NewRecorder()
	s.handleObsAction(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("unexpected status: %d\n%s", rr.Code, rr.Body.String())
	}
	body, err := os.ReadFile(alwaysOn)
	if err != nil {
		t.Fatal(err)
	}
	if string(body) != original {
		t.Fatalf("preview wrote always-on file:\n%s", body)
	}
	preview := rr.Body.String()
	for _, want := range []string{"review before commit", "write diff", "commit this", "Prefer direct answers"} {
		if !strings.Contains(preview, want) {
			t.Fatalf("preview missing %q:\n%s", want, preview)
		}
	}

	output := original + "\n- Prefer direct answers\n"
	form := url.Values{
		"path":      {alwaysOn},
		"base_hash": {sha256Hex([]byte(original))},
		"output":    {output},
	}
	req = httptest.NewRequest(http.MethodPost, "/obs/obs_0003/commit-promote-claude-md", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rr = httptest.NewRecorder()
	s.handleObsAction(rr, req)

	if rr.Code != http.StatusSeeOther {
		t.Fatalf("unexpected commit status: %d\n%s", rr.Code, rr.Body.String())
	}
	body, err = os.ReadFile(alwaysOn)
	if err != nil {
		t.Fatal(err)
	}
	if string(body) != output {
		t.Fatalf("commit did not write exact preview output:\n%s", body)
	}
	obs, err := readObservations(s.paths.observationFile)
	if err != nil {
		t.Fatal(err)
	}
	if obs[0].Status != statusPromotedClaudeMD || obs[0].PromotedTo != alwaysOn {
		t.Fatalf("commit did not finalize promotion: %#v", obs[0])
	}
}

func TestAlwaysOnCommitRejectsChangedDestination(t *testing.T) {
	s := testServer(t)
	alwaysOn := filepath.Join(t.TempDir(), "AGENTS.md")
	if err := os.WriteFile(alwaysOn, []byte("# Changed\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := writePreferences(s.paths, preferences{
		PromotionMode: promotionModeUnified,
		AlwaysOnPath:  alwaysOn,
		SkillsDir:     filepath.Join(t.TempDir(), "skills"),
	}); err != nil {
		t.Fatal(err)
	}
	if err := writeObservations(s.paths.observationFile, []observation{{
		ID:     "obs_0004",
		Claim:  "Prefer direct answers",
		Type:   typePreference,
		Status: statusActive,
	}}); err != nil {
		t.Fatal(err)
	}
	form := url.Values{
		"path":      {alwaysOn},
		"base_hash": {sha256Hex([]byte("# Instructions\n"))},
		"output":    {"# Instructions\n\n- Prefer direct answers\n"},
	}
	req := httptest.NewRequest(http.MethodPost, "/obs/obs_0004/commit-promote-claude-md", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rr := httptest.NewRecorder()
	s.handleObsAction(rr, req)

	if rr.Code != http.StatusInternalServerError {
		t.Fatalf("expected stale commit to fail, got %d", rr.Code)
	}
	obs, err := readObservations(s.paths.observationFile)
	if err != nil {
		t.Fatal(err)
	}
	if obs[0].Status != statusActive {
		t.Fatalf("stale commit mutated observation: %#v", obs[0])
	}
}

func TestInstructionPreviewUsesRequestedTargetScopeInPrompt(t *testing.T) {
	s := testServer(t)
	alwaysOn := filepath.Join(t.TempDir(), "AGENTS.md")
	projectDir := t.TempDir()
	var promptObservation observation
	restore := stubAlwaysOnGenerator(func(current string, o observation) string {
		promptObservation = o
		return current + "\n- " + o.Claim + "\n"
	})
	defer restore()
	if err := writePreferences(s.paths, preferences{
		PromotionMode: promotionModeUnified,
		AlwaysOnPath:  alwaysOn,
		SkillsDir:     filepath.Join(t.TempDir(), "skills"),
	}); err != nil {
		t.Fatal(err)
	}
	if err := writeObservations(s.paths.observationFile, []observation{{
		ID:         "obs_0009",
		Claim:      "Project-specific corrections can be promoted globally only when requested.",
		Type:       typePreference,
		Scope:      scopeProject,
		ProjectCWD: projectDir,
		Status:     statusActive,
	}}); err != nil {
		t.Fatal(err)
	}

	preview, err := previewPromoteToAgentsMDWithScope(context.TODO(), s.paths, "obs_0009", scopeUser, true)
	if err != nil {
		t.Fatal(err)
	}

	if preview.CommitPath != alwaysOn || preview.CommitScope != scopeUser {
		t.Fatalf("preview did not resolve user target: %#v", preview)
	}
	if promptObservation.Scope != scopeUser || promptObservation.ProjectCWD != "" {
		t.Fatalf("prompt used observation scope instead of target scope: %#v", promptObservation)
	}
}

func TestSkillPreviewCommitWritesExactOutput(t *testing.T) {
	s := testServer(t)
	skillsDir := filepath.Join(t.TempDir(), "skills")
	skillPath := filepath.Join(skillsDir, "direct-answers", "SKILL.md")
	output := "---\nname: direct-answers\ndescription: Prefer direct answers\n---\n\nAnswer directly.\n"
	if err := writePreferences(s.paths, preferences{
		PromotionMode: promotionModeUnified,
		AlwaysOnPath:  filepath.Join(t.TempDir(), "AGENTS.md"),
		SkillsDir:     skillsDir,
	}); err != nil {
		t.Fatal(err)
	}
	if err := writeObservations(s.paths.observationFile, []observation{{
		ID:     "obs_0005",
		Claim:  "Prefer direct answers",
		Type:   typePreference,
		Status: statusActive,
	}}); err != nil {
		t.Fatal(err)
	}

	form := url.Values{
		"path":   {skillPath},
		"output": {output},
	}
	req := httptest.NewRequest(http.MethodPost, "/obs/obs_0005/commit-promote-skill", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rr := httptest.NewRecorder()
	s.handleObsAction(rr, req)

	if rr.Code != http.StatusSeeOther {
		t.Fatalf("unexpected commit status: %d\n%s", rr.Code, rr.Body.String())
	}
	body, err := os.ReadFile(skillPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(body) != output {
		t.Fatalf("commit did not write exact skill output:\n%s", body)
	}
	obs, err := readObservations(s.paths.observationFile)
	if err != nil {
		t.Fatal(err)
	}
	if obs[0].Status != statusPromotedToSkill || obs[0].PromotedTo != skillPath {
		t.Fatalf("commit did not finalize skill promotion: %#v", obs[0])
	}
}

func TestProjectAlwaysOnPromotionCreatesProjectAgentsFile(t *testing.T) {
	s := testServer(t)
	projectDir := t.TempDir()
	output := "# Project Instructions\n\n- Keep guidance local.\n"
	if err := writePreferences(s.paths, preferences{
		PromotionMode: promotionModeUnified,
		AlwaysOnPath:  filepath.Join(t.TempDir(), "AGENTS.md"),
		SkillsDir:     filepath.Join(t.TempDir(), "skills"),
	}); err != nil {
		t.Fatal(err)
	}
	if err := writeObservations(s.paths.observationFile, []observation{{
		ID:         "obs_0006",
		Claim:      "Project-specific corrections should stay local.",
		Type:       typePreference,
		Scope:      scopeProject,
		ProjectCWD: projectDir,
		Status:     statusActive,
	}}); err != nil {
		t.Fatal(err)
	}

	form := url.Values{
		"path":      {filepath.Join(projectDir, "AGENTS.md")},
		"base_hash": {sha256Hex(nil)},
		"scope":     {string(scopeProject)},
		"output":    {output},
	}
	req := httptest.NewRequest(http.MethodPost, "/obs/obs_0006/commit-promote-claude-md", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rr := httptest.NewRecorder()
	s.handleObsAction(rr, req)

	if rr.Code != http.StatusSeeOther {
		t.Fatalf("unexpected commit status: %d\n%s", rr.Code, rr.Body.String())
	}
	body, err := os.ReadFile(filepath.Join(projectDir, "AGENTS.md"))
	if err != nil {
		t.Fatal(err)
	}
	if string(body) != output {
		t.Fatalf("unexpected project AGENTS.md:\n%s", body)
	}
}

func TestProjectSkillCommitWritesPortableProjectSkill(t *testing.T) {
	s := testServer(t)
	projectDir := t.TempDir()
	skillPath := filepath.Join(projectDir, ".agents", "skills", "local-workflow", "SKILL.md")
	output := "---\nname: local-workflow\ndescription: Use when working in this project\n---\n\nKeep it local.\n"
	if err := writePreferences(s.paths, preferences{
		PromotionMode: promotionModeUnified,
		AlwaysOnPath:  filepath.Join(t.TempDir(), "AGENTS.md"),
		SkillsDir:     filepath.Join(t.TempDir(), "skills"),
	}); err != nil {
		t.Fatal(err)
	}
	if err := writeObservations(s.paths.observationFile, []observation{{
		ID:         "obs_0007",
		Claim:      "Project-specific corrections should become project skills.",
		Type:       typeWorkflow,
		Scope:      scopeProject,
		ProjectCWD: projectDir,
		Status:     statusActive,
	}}); err != nil {
		t.Fatal(err)
	}

	form := url.Values{
		"path":   {skillPath},
		"scope":  {string(scopeProject)},
		"output": {output},
	}
	req := httptest.NewRequest(http.MethodPost, "/obs/obs_0007/commit-promote-skill", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rr := httptest.NewRecorder()
	s.handleObsAction(rr, req)

	if rr.Code != http.StatusSeeOther {
		t.Fatalf("unexpected commit status: %d\n%s", rr.Code, rr.Body.String())
	}
	body, err := os.ReadFile(skillPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(body) != output {
		t.Fatalf("unexpected project skill:\n%s", body)
	}
}

func TestProjectSkillCommitRejectsPathOutsidePortableProjectSkillsDir(t *testing.T) {
	s := testServer(t)
	projectDir := t.TempDir()
	if err := writePreferences(s.paths, preferences{
		PromotionMode: promotionModeUnified,
		AlwaysOnPath:  filepath.Join(t.TempDir(), "AGENTS.md"),
		SkillsDir:     filepath.Join(t.TempDir(), "skills"),
	}); err != nil {
		t.Fatal(err)
	}
	if err := writeObservations(s.paths.observationFile, []observation{{
		ID:         "obs_0008",
		Claim:      "Project-specific corrections should become project skills.",
		Type:       typeWorkflow,
		Scope:      scopeProject,
		ProjectCWD: projectDir,
		Status:     statusActive,
	}}); err != nil {
		t.Fatal(err)
	}

	form := url.Values{
		"path":   {filepath.Join(projectDir, ".opencode", "skills", "bad", "SKILL.md")},
		"scope":  {string(scopeProject)},
		"output": {"---\nname: bad\ndescription: bad\n---\n\nbad\n"},
	}
	req := httptest.NewRequest(http.MethodPost, "/obs/obs_0008/commit-promote-skill", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rr := httptest.NewRecorder()
	s.handleObsAction(rr, req)

	if rr.Code != http.StatusInternalServerError {
		t.Fatalf("expected invalid path to fail, got %d", rr.Code)
	}
}

func testServer(t *testing.T) *server {
	t.Helper()
	dir := t.TempDir()
	claudeProjects := filepath.Join(dir, "claude-projects")
	codexSessions := filepath.Join(dir, "codex-sessions")
	for _, path := range []string{claudeProjects, codexSessions} {
		if err := os.MkdirAll(path, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	s := &server{
		paths: paths{
			claudeProjects:   claudeProjects,
			codexSessions:    codexSessions,
			stateDir:         dir,
			observationFile:  filepath.Join(dir, "observations.jsonl"),
			stateFile:        filepath.Join(dir, "state.json"),
			preferencesFile:  filepath.Join(dir, "preferences.json"),
			sessionIndexFile: filepath.Join(dir, "sessions.db"),
			candidatesDir:    filepath.Join(dir, "candidates"),
		},
	}
	if err := s.loadTemplates(); err != nil {
		t.Fatal(err)
	}
	return s
}

func writeCodexSession(t *testing.T, root, id string, mtime time.Time) {
	t.Helper()
	writeCodexSessionWithTurns(t, root, id, []string{
		`{"type":"session_meta","payload":{"id":"` + id + `","cwd":"/tmp/distill"}}`,
		`{"timestamp":"2026-05-24T12:00:00Z","type":"response_item","payload":{"type":"message","role":"user","content":[{"type":"input_text","text":"short low signal"}]}}`,
	}, mtime)
}

func writeCodexSessionWithTurns(t *testing.T, root, id string, lines []string, mtime time.Time) string {
	t.Helper()
	body := strings.Join(lines, "\n")
	path := filepath.Join(root, id+".jsonl")
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(path, mtime, mtime); err != nil {
		t.Fatal(err)
	}
	return path
}
