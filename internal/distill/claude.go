package distill

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
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
	args := []string{
		"-p",
		"--model", string(model),
		"--output-format", "text",
		"--no-session-persistence",
		"--disable-slash-commands",
	}
	cmd := exec.CommandContext(ctx, "claude", args...)
	cmd.Stdin = strings.NewReader(prompt)
	cmd.Env = append(os.Environ(), "DISTILL_INTERNAL=1")

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("claude exited: %w\nstderr: %s", err, truncate(stderr.String(), 2000))
	}
	return strings.TrimSpace(stdout.String()), nil
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
