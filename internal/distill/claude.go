package distill

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

type modelID string

const (
	modelHaiku  modelID = "claude-haiku-4-5-20251001"
	modelSonnet modelID = "claude-sonnet-4-6"
	modelOpus   modelID = "claude-opus-4-7"
)

func resolveModel(name string) (modelID, error) {
	switch name {
	case "", "haiku":
		return modelHaiku, nil
	case "sonnet":
		return modelSonnet, nil
	case "opus":
		return modelOpus, nil
	default:
		return "", fmt.Errorf("unknown model: %s", name)
	}
}

// callClaude shells out to the local `claude` CLI in print mode. Reuses the
// user's existing Claude Code auth — distill never touches an API key.
func callClaude(ctx context.Context, model modelID, prompt string) (string, error) {
	return callClaudeCommand(ctx, "", model, prompt)
}

func callClaudeCommand(ctx context.Context, override string, model modelID, prompt string) (string, error) {
	claudePath, env := resolveClaudeCommand(override)
	args := []string{
		"-p",
		"--model", string(model),
		"--output-format", "text",
		"--no-session-persistence",
		"--disable-slash-commands",
	}
	cmd, err := internalHarnessCommand(ctx, claudePath, args...)
	if err != nil {
		return "", err
	}
	cmd.Stdin = strings.NewReader(prompt)
	cmd.Env = internalHarnessEnv(env)

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("claude exited: %w\nstderr: %s", err, truncate(stderr.String(), 2000))
	}
	return strings.TrimSpace(stdout.String()), nil
}

func callExtractor(ctx context.Context, prefs preferences, model string, prompt string) (string, error) {
	return callConfiguredBackend(ctx, prefs, prefs.ExtractionBackend, model, prompt)
}

func callGeneration(ctx context.Context, prefs preferences, prompt string) (string, error) {
	return callConfiguredBackend(ctx, prefs, prefs.GenerationBackend, prefs.GenerationModel, prompt)
}

func callConfiguredBackend(ctx context.Context, prefs preferences, backend, model, prompt string) (string, error) {
	switch backend {
	case extractionBackendClaude:
		resolved, err := resolveModel(model)
		if err != nil {
			return "", err
		}
		return callClaudeCommand(ctx, prefs.ClaudeCommandPath, resolved, prompt)
	case extractionBackendCodex:
		return callCodexExec(ctx, prefs, model, prompt)
	default:
		return "", fmt.Errorf("unknown model backend: %s", backend)
	}
}

func callCodexExec(ctx context.Context, prefs preferences, model string, prompt string) (string, error) {
	codexPath, env := resolveCommand("codex", prefs.CodexCommandPath)
	out, err := os.CreateTemp("", "distill-codex-output-*.txt")
	if err != nil {
		return "", err
	}
	outPath := out.Name()
	out.Close()
	defer os.Remove(outPath)

	args := []string{"exec", "--skip-git-repo-check", "--ephemeral", "--sandbox", "read-only", "--ignore-rules", "--output-last-message", outPath}
	if strings.TrimSpace(model) != "" && model != "haiku" && model != "sonnet" && model != "opus" {
		args = append(args, "--model", model)
	}
	args = append(args, "-")
	cmd, err := internalHarnessCommand(ctx, codexPath, args...)
	if err != nil {
		return "", err
	}
	cmd.Stdin = strings.NewReader(prompt)
	cmd.Env = internalHarnessEnv(env)

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("codex exec exited: %w\nstderr: %s", err, truncate(stderr.String(), 2000))
	}
	b, err := os.ReadFile(outPath)
	if err == nil && strings.TrimSpace(string(b)) != "" {
		return strings.TrimSpace(string(b)), nil
	}
	return strings.TrimSpace(stdout.String()), nil
}

func internalHarnessCommand(ctx context.Context, path string, args ...string) (*exec.Cmd, error) {
	dir, err := internalHarnessDir()
	if err != nil {
		return nil, err
	}
	cmd := exec.CommandContext(ctx, path, args...)
	cmd.Dir = dir
	return cmd, nil
}

func internalHarnessDir() (string, error) {
	p, err := resolvePaths()
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll(p.internalCallsDir, 0o755); err != nil {
		return "", err
	}
	return p.internalCallsDir, nil
}

func internalHarnessEnv(env []string) []string {
	return append(env, "DISTILL_INTERNAL=1")
}

func resolveClaudeCommand(override string) (string, []string) {
	return resolveCommand("claude", override)
}

func resolveCommand(name, override string) (string, []string) {
	env := os.Environ()
	path := extendedExecutablePath()
	env = setEnv(env, "PATH", path)
	if strings.TrimSpace(override) != "" {
		if strings.Contains(override, string(filepath.Separator)) {
			return override, env
		}
		if found := findExecutable(override, filepath.SplitList(path)); found != "" {
			return found, env
		}
		return override, env
	}
	if found := findExecutable(name, filepath.SplitList(path)); found != "" {
		return found, env
	}
	return name, env
}

func extendedExecutablePath() string {
	parts := filepath.SplitList(os.Getenv("PATH"))
	home, err := os.UserHomeDir()
	if err == nil && home != "" {
		parts = append(parts, filepath.Join(home, ".local", "bin"))
	}
	parts = append(parts, "/opt/homebrew/bin", "/usr/local/bin")
	return strings.Join(dedupeStrings(parts), string(os.PathListSeparator))
}

func findExecutable(name string, dirs []string) string {
	for _, dir := range dirs {
		if dir == "" {
			continue
		}
		path := filepath.Join(dir, name)
		info, err := os.Stat(path)
		if err == nil && !info.IsDir() && info.Mode()&0o111 != 0 {
			return path
		}
	}
	return ""
}

func setEnv(env []string, key, value string) []string {
	prefix := key + "="
	for i, entry := range env {
		if strings.HasPrefix(entry, prefix) {
			env[i] = prefix + value
			return env
		}
	}
	return append(env, prefix+value)
}

func dedupeStrings(values []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, value := range values {
		if value == "" || seen[value] {
			continue
		}
		seen[value] = true
		out = append(out, value)
	}
	return out
}

// extractJSONBlock pulls the first JSON object out of a response that may
// include a preamble or code fences.
func extractJSONBlock(text string) string {
	// Fenced ```json block.
	if i := strings.Index(text, "```"); i >= 0 {
		rest := text[i+3:]
		if strings.HasPrefix(rest, "json\n") {
			rest = rest[5:]
		} else if strings.HasPrefix(rest, "\n") {
			rest = rest[1:]
		}
		if end := strings.Index(rest, "```"); end >= 0 {
			return strings.TrimSpace(rest[:end])
		}
	}

	// First brace or bracket.
	brace := strings.Index(text, "{")
	bracket := strings.Index(text, "[")
	start := brace
	switch {
	case brace == -1 && bracket == -1:
		return strings.TrimSpace(text)
	case brace == -1:
		start = bracket
	case bracket != -1 && bracket < brace:
		start = bracket
	}
	return strings.TrimSpace(text[start:])
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}

// withTimeout is a small ergonomic helper for callers that want a flat
// timeout without managing the context themselves.
func withTimeout(d time.Duration) (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.Background(), d)
}
