package distill

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestShouldSkipExtractionRequiresEnoughUserSignal(t *testing.T) {
	opts := extractOpts{minUserTurns: 2, minUserChars: 200}

	if !shouldSkipExtraction(transcriptSignal{userTurns: 1, userChars: 500}, opts) {
		t.Fatal("expected one low-signal user turn to skip")
	}
	if !shouldSkipExtraction(transcriptSignal{userTurns: 2, userChars: 120}, opts) {
		t.Fatal("expected short low-signal session to skip")
	}
	if shouldSkipExtraction(transcriptSignal{userTurns: 1, userChars: 20, markerCount: 1}, opts) {
		t.Fatal("expected marker-bearing session to run extraction")
	}
	if shouldSkipExtraction(transcriptSignal{userTurns: 3, userChars: 250}, opts) {
		t.Fatal("expected sufficiently large low-signal session to run extraction")
	}
}

func TestInternalExtractionSessionSkipsExtractorAndMarksProcessed(t *testing.T) {
	dir := t.TempDir()
	p := paths{
		stateDir:        filepath.Join(dir, "state"),
		stateFile:       filepath.Join(dir, "state", "state.json"),
		observationFile: filepath.Join(dir, "state", "observations.jsonl"),
	}
	st := newStore(p)
	state := &stateFile{ProcessedSessions: map[string]string{}}
	sessionPath := filepath.Join(dir, "internal.jsonl")
	body := strings.Join([]string{
		`{"type":"user","uuid":"user-1","message":{"content":[{"type":"text","text":"# Observation extraction\n\nYou are reading user-authored turns from a single coding-agent session between a developer and Claude Code or Codex.\n\nOutput the JSON object now."}]}}`,
		`{"type":"assistant","uuid":"assistant-1","message":{"content":[{"type":"text","text":"{\"reasoning\":\"nothing notable\",\"new_observations\":[],\"reinforced\":[],\"contradicted\":[]}"}]}}`,
	}, "\n")
	if err := os.WriteFile(sessionPath, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}

	err := processOne(st, state, sessionMeta{
		product:   productClaude,
		sessionID: "internal-session",
		filePath:  sessionPath,
		cwd:       "/tmp/distill",
	}, extractOpts{minUserTurns: 2, minUserChars: 200})
	if err != nil {
		t.Fatal(err)
	}

	persisted, err := readState(p.stateFile)
	if err != nil {
		t.Fatal(err)
	}
	if !processedSession(persisted, sessionMeta{product: productClaude, sessionID: "internal-session"}) {
		t.Fatalf("expected internal extraction session to be marked processed: %#v", persisted)
	}
	if _, err := os.Stat(p.observationFile); !os.IsNotExist(err) {
		t.Fatalf("expected no observations file write, got err=%v", err)
	}
}

func TestInternalModelCallCWDMarksSessionProcessedBeforeParsing(t *testing.T) {
	dir := t.TempDir()
	internalDir := filepath.Join(dir, "state", "internal-model-calls")
	p := paths{
		stateDir:         filepath.Join(dir, "state"),
		stateFile:        filepath.Join(dir, "state", "state.json"),
		observationFile:  filepath.Join(dir, "state", "observations.jsonl"),
		internalCallsDir: internalDir,
	}
	st := newStore(p)
	state := &stateFile{ProcessedSessions: map[string]string{}}
	missingSessionPath := filepath.Join(dir, "does-not-exist.jsonl")

	err := processOne(st, state, sessionMeta{
		product:   productClaude,
		sessionID: "internal-cwd-session",
		filePath:  missingSessionPath,
		cwd:       internalDir,
	}, extractOpts{minUserTurns: 2, minUserChars: 200})
	if err != nil {
		t.Fatal(err)
	}

	persisted, err := readState(p.stateFile)
	if err != nil {
		t.Fatal(err)
	}
	if !processedSession(persisted, sessionMeta{product: productClaude, sessionID: "internal-cwd-session"}) {
		t.Fatalf("expected internal cwd session to be marked processed: %#v", persisted)
	}
}

func TestInternalModelCallCWDMarksCodexSessionProcessedBeforeParsing(t *testing.T) {
	dir := t.TempDir()
	internalDir := filepath.Join(dir, "state", "internal-model-calls")
	p := paths{
		stateDir:         filepath.Join(dir, "state"),
		stateFile:        filepath.Join(dir, "state", "state.json"),
		observationFile:  filepath.Join(dir, "state", "observations.jsonl"),
		internalCallsDir: internalDir,
	}
	st := newStore(p)
	state := &stateFile{ProcessedSessions: map[string]string{}}
	missingSessionPath := filepath.Join(dir, "does-not-exist.jsonl")

	err := processOne(st, state, sessionMeta{
		product:   productCodex,
		sessionID: "codex-internal-cwd-session",
		filePath:  missingSessionPath,
		cwd:       filepath.Join(internalDir, "nested"),
	}, extractOpts{minUserTurns: 2, minUserChars: 200})
	if err != nil {
		t.Fatal(err)
	}

	persisted, err := readState(p.stateFile)
	if err != nil {
		t.Fatal(err)
	}
	if !processedSession(persisted, sessionMeta{product: productCodex, sessionID: "codex-internal-cwd-session"}) {
		t.Fatalf("expected internal Codex cwd session to be marked processed: %#v", persisted)
	}
}

func TestInternalExtractionPromptDetectionIsNarrow(t *testing.T) {
	if !isInternalExtractionPrompt("# Observation extraction\n\nYou are reading a transcript of a single coding session.\n\nOutput the JSON object now.") {
		t.Fatal("expected bundled extraction prompt to be recognized")
	}
	if isInternalExtractionPrompt("# Observation extraction\n\nCan you help me design an observation extraction feature?") {
		t.Fatal("did not expect a user discussion of observation extraction to be treated as internal")
	}
}

func TestExtractProductSelectionAcceptsErgonomicProductFlags(t *testing.T) {
	tests := []struct {
		name        string
		productName string
		productSet  bool
		codexOnly   bool
		claudeOnly  bool
		want        product
		wantErr     string
	}{
		{name: "default all", productName: "all", want: productAll},
		{name: "product flag", productName: "codex", productSet: true, want: productCodex},
		{name: "codex alias", productName: "all", codexOnly: true, want: productCodex},
		{name: "claude alias", productName: "all", claudeOnly: true, want: productClaude},
		{name: "conflicting aliases", productName: "all", codexOnly: true, claudeOnly: true, wantErr: "choose only one product selector"},
		{name: "conflicting product and alias", productName: "codex", productSet: true, codexOnly: true, wantErr: "choose only one product selector"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := extractProductSelection(tt.productName, tt.productSet, tt.codexOnly, tt.claudeOnly)
			if tt.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("expected error containing %q, got %v", tt.wantErr, err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if got != tt.want {
				t.Fatalf("expected %s, got %s", tt.want, got)
			}
		})
	}
}

func TestRenderExtractionTranscriptDropsAssistantTurnsByDefault(t *testing.T) {
	rendered := renderExtractionTranscript([]transcriptTurn{
		{role: "user", uuid: "1234567890", text: "first"},
		{role: "assistant", uuid: "aaaaaaaaaa", text: "assistant text"},
		{role: "user", uuid: "abcdefghi", text: "second"},
	}, 2500)

	want := "[turn 1 | user | 12345678]\nfirst\n\n---\n\n[turn 3 | user | abcdefgh]\nsecond"
	if rendered != want {
		t.Fatalf("unexpected rendered transcript:\nwant: %q\n got: %q", want, rendered)
	}
}

func TestRenderExtractionTranscriptZoomsIntoPrecedingAssistantTurn(t *testing.T) {
	rendered := renderExtractionTranscript([]transcriptTurn{
		{role: "user", uuid: "1111111111", text: "start"},
		{role: "assistant", uuid: "2222222222", text: "assistant behavior being corrected"},
		{role: "user", uuid: "3333333333", text: "no, don't do that"},
	}, 12)

	want := "[turn 1 | user | 11111111]\nstart\n\n---\n\n[turn 3 | user | 33333333]\nno, don't do that\n\n[local context: preceding assistant turn]\n[turn 2 | assistant | 22222222]\n[...assistant context truncated...]\nng corrected"
	if rendered != want {
		t.Fatalf("unexpected rendered transcript:\nwant: %q\n got: %q", want, rendered)
	}
}

func TestRelevantObservationsUsesQueryOverlap(t *testing.T) {
	obs := []observation{
		{ID: "obs_1", Claim: "User prefers concise final answers", EvidenceCount: 1},
		{ID: "obs_2", Claim: "User wants browser screenshots for frontend polish", EvidenceCount: 3},
		{ID: "obs_3", Claim: "User asks for git hygiene before commits", EvidenceCount: 2},
	}

	got := relevantObservations(obs, "please use browser screenshots to verify the frontend", 1, "")
	if len(got) != 1 || got[0].ID != "obs_2" {
		t.Fatalf("expected obs_2, got %#v", got)
	}
}

func TestRelevantObservationsExcludesOtherProjectScopes(t *testing.T) {
	obs := []observation{
		{ID: "obs_user", Claim: "User prefers concise final answers", Scope: scopeUser},
		{ID: "obs_here", Claim: "Project guidance belongs in local artifacts", Scope: scopeProject, ProjectCWD: "/work/distill"},
		{ID: "obs_elsewhere", Claim: "Project guidance belongs in local artifacts", Scope: scopeProject, ProjectCWD: "/work/other"},
	}

	got := relevantObservations(obs, "project guidance local artifacts", 10, "/work/distill")

	if len(got) != 2 {
		t.Fatalf("expected user and matching project observations, got %#v", got)
	}
	for _, o := range got {
		if o.ID == "obs_elsewhere" {
			t.Fatalf("included observation from a different project: %#v", got)
		}
	}
}

func TestApplyDeltasPersistsProjectScopeAndEvidenceCWD(t *testing.T) {
	got := applyDeltas(nil, extractionResult{
		NewObservations: []extractedObservation{{
			Claim:            "User wants distill guidance scoped to the project when corrections come from project work.",
			Type:             typePreference,
			Scope:            scopeProject,
			EvidenceTurnRefs: []string{"turn 4"},
			EvidenceQuote:    "scope it to the project",
		}},
	}, sessionMeta{product: productCodex, sessionID: "session-1", cwd: "/tmp/distill"})

	if len(got) != 1 {
		t.Fatalf("expected one observation, got %#v", got)
	}
	if got[0].Scope != scopeProject || got[0].ProjectCWD != "/tmp/distill" {
		t.Fatalf("unexpected scope: %#v", got[0])
	}
	if got[0].Evidence[0].ProjectCWD != "/tmp/distill" {
		t.Fatalf("expected evidence cwd, got %#v", got[0].Evidence[0])
	}
}

func TestApplyDeltasDoesNotReinforceProjectObservationFromDifferentProject(t *testing.T) {
	existing := []observation{{
		ID:            "obs_0001",
		Claim:         "Project-specific guidance belongs in project artifacts.",
		Type:          typePreference,
		Scope:         scopeProject,
		ProjectCWD:    "/tmp/distill",
		EvidenceCount: 1,
		Evidence:      []evidence{{Quote: "original"}},
		Status:        statusActive,
	}}

	got := applyDeltas(existing, extractionResult{
		Reinforced: []extractedReinforce{{
			ObsID:            "obs_0001",
			Scope:            scopeProject,
			EvidenceTurnRefs: []string{"turn 7"},
			EvidenceQuote:    "same idea elsewhere",
		}},
	}, sessionMeta{product: productCodex, sessionID: "session-2", cwd: "/tmp/other"})

	if got[0].EvidenceCount != 1 || len(got[0].Evidence) != 1 {
		t.Fatalf("different project reinforced observation: %#v", got[0])
	}
}

func TestApplyDeltasDoesNotContradictProjectObservationFromDifferentProject(t *testing.T) {
	existing := []observation{{
		ID:            "obs_0001",
		Claim:         "Project-specific guidance belongs in project artifacts.",
		Type:          typePreference,
		Scope:         scopeProject,
		ProjectCWD:    "/tmp/distill",
		EvidenceCount: 1,
		Evidence:      []evidence{{Quote: "original"}},
		Status:        statusActive,
	}}

	got := applyDeltas(existing, extractionResult{
		Contradicted: []extractedContradict{{
			ObsID:       "obs_0001",
			Explanation: "same idea contradicted elsewhere",
		}},
	}, sessionMeta{product: productCodex, sessionID: "session-2", cwd: "/tmp/other"})

	if len(got[0].ContradictedBy) != 0 {
		t.Fatalf("different project contradicted observation: %#v", got[0])
	}
}

func TestParseCodexTranscriptUsesUserAndAssistantMessages(t *testing.T) {
	path := filepath.Join(t.TempDir(), "codex.jsonl")
	body := strings.Join([]string{
		`{"type":"session_meta","payload":{"id":"session-1","cwd":"/tmp/project"}}`,
		`{"timestamp":"2026-05-22T20:40:00Z","type":"response_item","payload":{"type":"message","role":"developer","content":[{"type":"input_text","text":"ignore"}]}}`,
		`{"timestamp":"2026-05-22T20:41:00Z","type":"response_item","payload":{"type":"message","role":"user","content":[{"type":"input_text","text":"do the thing"}]}}`,
		`{"timestamp":"2026-05-22T20:42:00Z","type":"response_item","payload":{"type":"message","role":"assistant","content":[{"type":"output_text","text":"done"}]}}`,
	}, "\n")
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}

	turns, err := parseCodexTranscript(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(turns) != 2 {
		t.Fatalf("expected 2 user/assistant turns, got %#v", turns)
	}
	if turns[0].role != "user" || turns[0].text != "do the thing" {
		t.Fatalf("unexpected first turn: %#v", turns[0])
	}
	if turns[1].role != "assistant" || turns[1].text != "done" {
		t.Fatalf("unexpected second turn: %#v", turns[1])
	}
}

func TestProcessedSessionKeepsLegacyClaudeStateCompatible(t *testing.T) {
	state := &stateFile{ProcessedSessions: map[string]string{"legacy-session": "2026-05-22T20:00:00Z"}}

	if !processedSession(state, sessionMeta{product: productClaude, sessionID: "legacy-session"}) {
		t.Fatal("expected legacy Claude session key to remain processed")
	}
	if processedSession(state, sessionMeta{product: productCodex, sessionID: "legacy-session"}) {
		t.Fatal("did not expect legacy key to mark Codex session processed")
	}
}

func TestQuietSessionsFiltersRecentlyModifiedTranscripts(t *testing.T) {
	now := time.Date(2026, 5, 22, 12, 0, 0, 0, time.UTC)
	sessions := []sessionMeta{
		{sessionID: "old", mtime: now.Add(-2 * time.Hour)},
		{sessionID: "recent", mtime: now.Add(-5 * time.Minute)},
	}

	got := quietSessions(sessions, 10*time.Minute, now)
	if len(got) != 1 || got[0].sessionID != "old" {
		t.Fatalf("expected only old session, got %#v", got)
	}
}
