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

	got := relevantObservations(obs, "please use browser screenshots to verify the frontend", 1)
	if len(got) != 1 || got[0].ID != "obs_2" {
		t.Fatalf("expected obs_2, got %#v", got)
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
