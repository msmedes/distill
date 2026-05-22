package distill

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRunHookSkipsInternalDistillSessions(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("DISTILL_INTERNAL", "1")

	stdin := os.Stdin
	t.Cleanup(func() {
		os.Stdin = stdin
	})

	payload := `{"session_id":"internal-session","hook_event_name":"SessionEnd"}`
	f, err := os.CreateTemp(t.TempDir(), "hook-payload-*.json")
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if _, err := f.WriteString(payload); err != nil {
		t.Fatal(err)
	}
	if _, err := f.Seek(0, 0); err != nil {
		t.Fatal(err)
	}
	os.Stdin = f

	if err := runHook(nil); err != nil {
		t.Fatalf("runHook returned error: %v", err)
	}

	logBytes, err := os.ReadFile(filepath.Join(home, ".distill", "hook.log"))
	if err != nil {
		t.Fatalf("expected hook log: %v", err)
	}
	log := string(logBytes)
	if !strings.Contains(log, "skipping internal distill claude session internal-session") {
		t.Fatalf("expected internal-session skip log, got %q", log)
	}
}
