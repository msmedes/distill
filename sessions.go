package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

type sessionMeta struct {
	sessionID  string
	projectDir string
	filePath   string
	mtime      time.Time
	cwd        string
}

type transcriptTurn struct {
	role      string
	text      string
	timestamp string
	uuid      string
}

// listSessions walks every Claude Code project dir and returns all sessions
// newest-first. Errors on individual project dirs are skipped silently.
func listSessions(claudeProjects string) ([]sessionMeta, error) {
	entries, err := os.ReadDir(claudeProjects)
	if err != nil {
		return nil, fmt.Errorf("reading %s: %w", claudeProjects, err)
	}

	var out []sessionMeta
	for _, e := range entries {
		if !e.IsDir() || strings.HasPrefix(e.Name(), ".") {
			continue
		}
		projectDir := filepath.Join(claudeProjects, e.Name())
		files, err := os.ReadDir(projectDir)
		if err != nil {
			continue
		}
		for _, f := range files {
			if f.IsDir() || !strings.HasSuffix(f.Name(), ".jsonl") {
				continue
			}
			filePath := filepath.Join(projectDir, f.Name())
			info, err := f.Info()
			if err != nil {
				continue
			}
			cwd := peekCwd(filePath)
			out = append(out, sessionMeta{
				sessionID:  strings.TrimSuffix(f.Name(), ".jsonl"),
				projectDir: projectDir,
				filePath:   filePath,
				mtime:      info.ModTime(),
				cwd:        cwd,
			})
		}
	}

	sort.Slice(out, func(i, j int) bool {
		return out[i].mtime.After(out[j].mtime)
	})
	return out, nil
}

// parseTranscript reads a session JSONL and returns just the user/assistant
// turns. Drops metadata, file-history snapshots, tool_use args, tool_result
// chatter — none of it carries taste signal.
func parseTranscript(filePath string) ([]transcriptTurn, error) {
	f, err := os.Open(filePath)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	// JSONL lines can be huge when a tool result was embedded; bump the buffer.
	scanner.Buffer(make([]byte, 1<<20), 64<<20)

	var turns []transcriptTurn
	for scanner.Scan() {
		line := scanner.Bytes()
		if len(line) == 0 {
			continue
		}
		var entry struct {
			Type      string          `json:"type"`
			Timestamp string          `json:"timestamp"`
			UUID      string          `json:"uuid"`
			Message   json.RawMessage `json:"message"`
			Content   json.RawMessage `json:"content"`
		}
		if err := json.Unmarshal(line, &entry); err != nil {
			continue
		}
		if entry.Type != "user" && entry.Type != "assistant" {
			continue
		}

		// message.content takes precedence (newer schema); fall back to top-level content.
		var content json.RawMessage
		if len(entry.Message) > 0 {
			var m struct {
				Content json.RawMessage `json:"content"`
			}
			if err := json.Unmarshal(entry.Message, &m); err == nil {
				content = m.Content
			}
		}
		if len(content) == 0 {
			content = entry.Content
		}

		text := extractText(content)
		if text == "" {
			continue
		}
		turns = append(turns, transcriptTurn{
			role:      entry.Type,
			text:      text,
			timestamp: entry.Timestamp,
			uuid:      entry.UUID,
		})
	}
	if err := scanner.Err(); err != nil && err != io.EOF {
		return nil, err
	}
	return turns, nil
}

// renderTranscript formats turns for prompt injection with [turn N | role | uuid8] headers
// so the extractor can cite specific turns.
func renderTranscript(turns []transcriptTurn) string {
	var b strings.Builder
	for i, t := range turns {
		if i > 0 {
			b.WriteString("\n\n---\n\n")
		}
		uuidShort := t.uuid
		if len(uuidShort) > 8 {
			uuidShort = uuidShort[:8]
		}
		fmt.Fprintf(&b, "[turn %d | %s | %s]\n%s", i+1, t.role, uuidShort, t.text)
	}
	return b.String()
}

func extractText(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	// Try string first.
	var s string
	if err := json.Unmarshal(raw, &s); err == nil {
		return strings.TrimSpace(s)
	}
	// Otherwise expect an array of content blocks.
	var blocks []map[string]any
	if err := json.Unmarshal(raw, &blocks); err != nil {
		return ""
	}
	var parts []string
	for _, b := range blocks {
		typ, _ := b["type"].(string)
		switch typ {
		case "text":
			if t, ok := b["text"].(string); ok {
				parts = append(parts, t)
			}
		case "tool_use":
			// Drop tool inputs — too noisy, not taste-bearing.
			parts = append(parts, "[tool call]")
		case "tool_result":
			// Skip entirely.
		}
	}
	return strings.TrimSpace(strings.Join(parts, "\n"))
}

// peekCwd does a quick scan of the first few lines of a session file to find
// the cwd field. Used only for human-friendly labels; failures return "".
func peekCwd(filePath string) string {
	f, err := os.Open(filePath)
	if err != nil {
		return ""
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 1<<20), 8<<20)
	for i := 0; i < 20 && scanner.Scan(); i++ {
		var entry struct {
			Cwd string `json:"cwd"`
		}
		if err := json.Unmarshal(scanner.Bytes(), &entry); err == nil && entry.Cwd != "" {
			return entry.Cwd
		}
	}
	return ""
}
