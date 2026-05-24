package distill

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestObservationTemplateParsesAfterBlockExtraction(t *testing.T) {
	s := &server{}
	if err := s.loadTemplates(); err != nil {
		t.Fatal(err)
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
	s := &server{
		paths: paths{
			stateDir:        dir,
			observationFile: filepath.Join(dir, "observations.jsonl"),
			stateFile:       filepath.Join(dir, "state.json"),
			preferencesFile: filepath.Join(dir, "preferences.json"),
			candidatesDir:   filepath.Join(dir, "candidates"),
		},
	}
	if err := s.loadTemplates(); err != nil {
		t.Fatal(err)
	}
	return s
}
