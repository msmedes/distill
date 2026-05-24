package distill

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestReadObservationsRejectsMalformedRecords(t *testing.T) {
	path := filepath.Join(t.TempDir(), "observations.jsonl")
	if err := os.WriteFile(path, []byte(`{"id":"obs_0001"}`+"\nnot-json\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	if _, err := readObservations(path); err == nil {
		t.Fatal("expected malformed observation record to return an error")
	}
}

func stubAlwaysOnGenerator(fn func(string, observation) string) func() {
	original := generateAlwaysOnInstructions
	generateAlwaysOnInstructions = func(_ context.Context, current string, o observation) (string, error) {
		return fn(current, o), nil
	}
	return func() {
		generateAlwaysOnInstructions = original
	}
}

func TestBuildAlwaysOnPromptBansDistillManagedSection(t *testing.T) {
	prompt, err := buildAlwaysOnPrompt("# Instructions\n", observation{
		ID:    "obs_0007",
		Type:  typePreference,
		Claim: "Prefer direct answers",
		Evidence: []evidence{{
			SessionID: "session-1",
			TurnRefs:  []string{"turn-1"},
			Quote:     "No preamble.",
		}},
	})
	if err != nil {
		t.Fatal(err)
	}

	for _, want := range []string{"Do not create a special distill section", "Do not include provenance markers", "Output only the complete updated markdown file"} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("always-on prompt missing %q:\n%s", want, prompt)
		}
	}
}

func TestRewriteAlwaysOnInstructionsWritesModelOutput(t *testing.T) {
	path := filepath.Join(t.TempDir(), "CLAUDE.md")
	if err := os.WriteFile(path, []byte("# Instructions\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	restore := stubAlwaysOnGenerator(func(_ string, o observation) string {
		return "# Instructions\n\n- " + o.Claim + "\n"
	})
	defer restore()

	if err := rewriteAlwaysOnInstructions(context.TODO(), path, observation{ID: "obs_0007", Claim: "Prefer direct answers"}); err != nil {
		t.Fatal(err)
	}
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := string(body), "# Instructions\n\n- Prefer direct answers\n"; got != want {
		t.Fatalf("unexpected rewrite:\n%s", got)
	}
}
