package distill

import "testing"

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
