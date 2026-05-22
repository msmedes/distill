package distill

import (
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

func TestAppendToClaudeMDIsIdempotentByObservationID(t *testing.T) {
	path := filepath.Join(t.TempDir(), "CLAUDE.md")
	if err := os.WriteFile(path, []byte("# Instructions\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	obs := observation{ID: "obs_0007", Claim: "Prefer direct answers"}

	if err := appendToClaudeMD(path, obs); err != nil {
		t.Fatal(err)
	}
	if err := appendToClaudeMD(path, obs); err != nil {
		t.Fatal(err)
	}

	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.Count(string(body), "distill obs_0007"); got != 1 {
		t.Fatalf("expected one promoted line, got %d:\n%s", got, body)
	}
}
