package distill

import (
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
	for _, want := range []string{"promotion destinations", "What does always-on mean?", "What is a skill?"} {
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
