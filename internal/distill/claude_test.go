package distill

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestResolveClaudeCommandFindsUserLocalBin(t *testing.T) {
	home := t.TempDir()
	bin := filepath.Join(home, ".local", "bin")
	if err := os.MkdirAll(bin, 0o755); err != nil {
		t.Fatal(err)
	}
	claude := filepath.Join(bin, "claude")
	if err := os.WriteFile(claude, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOME", home)
	t.Setenv("PATH", "/usr/bin:/bin")

	got, env := resolveClaudeCommand("")
	if got != claude {
		t.Fatalf("expected %s, got %s", claude, got)
	}
	var path string
	for _, entry := range env {
		if strings.HasPrefix(entry, "PATH=") {
			path = strings.TrimPrefix(entry, "PATH=")
			break
		}
	}
	if !strings.Contains(path, bin) {
		t.Fatalf("resolved env PATH did not include %s: %s", bin, path)
	}
}

func TestCallCodexExecUsesOutputLastMessage(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("HOME", dir)
	codex := filepath.Join(dir, "codex")
	body := `#!/bin/sh
out=""
while [ "$#" -gt 0 ]; do
  if [ "$1" = "--output-last-message" ]; then
    shift
    out="$1"
  fi
  shift
done
printf '{"ok":true}' > "$out"
`
	if err := os.WriteFile(codex, []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}

	got, err := callCodexExec(context.Background(), preferences{
		ExtractionBackend: extractionBackendCodex,
		CodexCommandPath:  codex,
	}, "gpt-test", "prompt")
	if err != nil {
		t.Fatal(err)
	}
	if got != `{"ok":true}` {
		t.Fatalf("unexpected codex output: %s", got)
	}
}

func TestCallCodexExecRunsFromInternalCommandDir(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("HOME", dir)
	codex := filepath.Join(dir, "codex")
	body := `#!/bin/sh
out=""
while [ "$#" -gt 0 ]; do
  if [ "$1" = "--output-last-message" ]; then
    shift
    out="$1"
  fi
  shift
done
pwd > "$out"
`
	if err := os.WriteFile(codex, []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}

	got, err := callCodexExec(context.Background(), preferences{
		ExtractionBackend: extractionBackendCodex,
		CodexCommandPath:  codex,
	}, "gpt-test", "prompt")
	if err != nil {
		t.Fatal(err)
	}
	want := filepath.Join(dir, ".distill", "internal-model-calls")
	gotReal, err := filepath.EvalSymlinks(got)
	if err != nil {
		t.Fatal(err)
	}
	wantReal, err := filepath.EvalSymlinks(want)
	if err != nil {
		t.Fatal(err)
	}
	if gotReal != wantReal {
		t.Fatalf("expected command dir %s, got %s", want, got)
	}
}
