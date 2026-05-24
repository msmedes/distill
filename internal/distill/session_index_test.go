package distill

import (
	"context"
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"
)

func TestSessionIndexRefreshIndexesSessions(t *testing.T) {
	p := testPaths(t)
	old := time.Now().Add(-time.Hour)
	writeCodexSession(t, p.codexSessions, "session-1", old)

	if err := refreshSessionIndex(context.Background(), p, productCodex); err != nil {
		t.Fatal(err)
	}

	targets, ok, err := indexedRecentUnprocessedTargets(p, &stateFile{ProcessedSessions: map[string]string{}}, productCodex, 10*time.Minute, 5)
	if err != nil {
		t.Fatal(err)
	}
	if !ok {
		t.Fatal("expected warm index")
	}
	if len(targets) != 1 || targets[0].sessionID != "session-1" {
		t.Fatalf("unexpected indexed targets: %#v", targets)
	}
}

func TestSessionIndexSkipsUnchangedFilesOnRefresh(t *testing.T) {
	p := testPaths(t)
	old := time.Now().Add(-time.Hour)
	writeCodexSession(t, p.codexSessions, "session-1", old)

	if err := refreshSessionIndex(context.Background(), p, productCodex); err != nil {
		t.Fatal(err)
	}
	idx, err := openSessionIndex(p)
	if err != nil {
		t.Fatal(err)
	}
	var firstIndexedAt int64
	if err := idx.db.QueryRow(`SELECT indexed_at_ns FROM sessions WHERE session_id = 'session-1'`).Scan(&firstIndexedAt); err != nil {
		t.Fatal(err)
	}
	idx.close()

	time.Sleep(time.Nanosecond)
	if err := refreshSessionIndex(context.Background(), p, productCodex); err != nil {
		t.Fatal(err)
	}
	idx, err = openSessionIndex(p)
	if err != nil {
		t.Fatal(err)
	}
	defer idx.close()
	var secondIndexedAt int64
	if err := idx.db.QueryRow(`SELECT indexed_at_ns FROM sessions WHERE session_id = 'session-1'`).Scan(&secondIndexedAt); err != nil {
		t.Fatal(err)
	}
	if secondIndexedAt <= firstIndexedAt {
		t.Fatalf("expected unchanged row heartbeat to update indexed_at_ns: first=%d second=%d", firstIndexedAt, secondIndexedAt)
	}
}

func TestSessionIndexMarksDeletedSessions(t *testing.T) {
	p := testPaths(t)
	path := filepath.Join(p.codexSessions, "session-1.jsonl")
	writeCodexSession(t, p.codexSessions, "session-1", time.Now().Add(-time.Hour))
	if err := refreshSessionIndex(context.Background(), p, productCodex); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if err := refreshSessionIndex(context.Background(), p, productCodex); err != nil {
		t.Fatal(err)
	}

	targets, ok, err := indexedRecentUnprocessedTargets(p, &stateFile{ProcessedSessions: map[string]string{}}, productCodex, 10*time.Minute, 5)
	if err != nil {
		t.Fatal(err)
	}
	if !ok {
		t.Fatal("expected warm index")
	}
	if len(targets) != 0 {
		t.Fatalf("deleted session remained selectable: %#v", targets)
	}
}

func TestIndexedSelectionExcludesProcessedAndCapsNewest(t *testing.T) {
	p := testPaths(t)
	now := time.Now().Add(-time.Hour)
	for i := 1; i <= 6; i++ {
		writeCodexSession(t, p.codexSessions, "session-"+strconv.Itoa(i), now.Add(time.Duration(i)*time.Minute))
	}
	if err := refreshSessionIndex(context.Background(), p, productCodex); err != nil {
		t.Fatal(err)
	}
	state := &stateFile{ProcessedSessions: map[string]string{
		sessionStateKey(productCodex, "session-6"): time.Now().Format(time.RFC3339),
	}}

	targets, ok, err := indexedRecentUnprocessedTargets(p, state, productCodex, 10*time.Minute, 5)
	if err != nil {
		t.Fatal(err)
	}
	if !ok {
		t.Fatal("expected warm index")
	}
	if len(targets) != 5 {
		t.Fatalf("expected 5 targets, got %#v", targets)
	}
	if targets[0].sessionID != "session-5" || targets[4].sessionID != "session-1" {
		t.Fatalf("unexpected target order: %#v", targets)
	}
}

func testPaths(t *testing.T) paths {
	t.Helper()
	dir := t.TempDir()
	claudeProjects := filepath.Join(dir, "claude-projects")
	codexSessions := filepath.Join(dir, "codex-sessions")
	for _, path := range []string{claudeProjects, codexSessions} {
		if err := os.MkdirAll(path, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	return paths{
		claudeProjects:   claudeProjects,
		codexSessions:    codexSessions,
		stateDir:         dir,
		observationFile:  filepath.Join(dir, "observations.jsonl"),
		stateFile:        filepath.Join(dir, "state.json"),
		preferencesFile:  filepath.Join(dir, "preferences.json"),
		sessionIndexFile: filepath.Join(dir, "sessions.db"),
		candidatesDir:    filepath.Join(dir, "candidates"),
	}
}
