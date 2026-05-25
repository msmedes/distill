package distill

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

type sessionMeta struct {
	product    product
	sessionID  string
	projectDir string
	filePath   string
	mtime      time.Time
	cwd        string
}

type sessionCandidate struct {
	product    product
	sessionID  string
	projectDir string
	filePath   string
	mtime      time.Time
}

type transcriptTurn struct {
	role      string
	text      string
	timestamp string
	uuid      string
}

func listSessions(p paths, target product) ([]sessionMeta, error) {
	var out []sessionMeta
	for _, source := range productSources(target) {
		sessions, err := source.listSessions(p)
		if err != nil && !errors.Is(err, os.ErrNotExist) {
			return nil, err
		}
		out = append(out, sessions...)
	}
	sort.Slice(out, func(i, j int) bool {
		return out[i].mtime.After(out[j].mtime)
	})
	return out, nil
}

func listSessionCandidates(p paths, target product) ([]sessionCandidate, error) {
	var out []sessionCandidate
	for _, source := range productSources(target) {
		candidates, err := source.listCandidates(p)
		if err != nil && !errors.Is(err, os.ErrNotExist) {
			return nil, err
		}
		out = append(out, candidates...)
	}
	sort.Slice(out, func(i, j int) bool {
		return out[i].mtime.After(out[j].mtime)
	})
	return out, nil
}

// listClaudeSessions walks every Claude Code project dir and returns all sessions.
// Errors on individual project dirs are skipped silently.
func listClaudeSessions(claudeProjects string) ([]sessionMeta, error) {
	candidates, err := listClaudeSessionCandidates(claudeProjects)
	if err != nil {
		return nil, err
	}
	out := make([]sessionMeta, 0, len(candidates))
	for _, c := range candidates {
		out = append(out, c.hydrate())
	}
	return out, nil
}

func listClaudeSessionCandidates(claudeProjects string) ([]sessionCandidate, error) {
	entries, err := os.ReadDir(claudeProjects)
	if err != nil {
		return nil, fmt.Errorf("reading %s: %w", claudeProjects, err)
	}

	var out []sessionCandidate
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
			out = append(out, sessionCandidate{
				product:    productClaude,
				sessionID:  strings.TrimSuffix(f.Name(), ".jsonl"),
				projectDir: projectDir,
				filePath:   filePath,
				mtime:      info.ModTime(),
			})
		}
	}

	return out, nil
}

func listCodexSessions(codexSessions string) ([]sessionMeta, error) {
	candidates, err := listCodexSessionCandidates(codexSessions)
	if err != nil {
		return nil, err
	}
	out := make([]sessionMeta, 0, len(candidates))
	for _, c := range candidates {
		out = append(out, c.hydrate())
	}
	return out, nil
}

func listCodexSessionCandidates(codexSessions string) ([]sessionCandidate, error) {
	var out []sessionCandidate
	err := filepath.WalkDir(codexSessions, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			if d != nil && d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if d.IsDir() || !strings.HasSuffix(d.Name(), ".jsonl") {
			return nil
		}
		info, err := d.Info()
		if err != nil {
			return nil
		}
		out = append(out, sessionCandidate{
			product:   productCodex,
			sessionID: strings.TrimSuffix(strings.TrimPrefix(d.Name(), "rollout-"), ".jsonl"),
			filePath:  path,
			mtime:     info.ModTime(),
		})
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("reading %s: %w", codexSessions, err)
	}
	return out, nil
}

func (c sessionCandidate) hydrate() sessionMeta {
	s := sessionMeta{
		product:    c.product,
		sessionID:  c.sessionID,
		projectDir: c.projectDir,
		filePath:   c.filePath,
		mtime:      c.mtime,
	}
	switch c.product {
	case productClaude:
		s.cwd = peekCwd(c.filePath)
		if s.cwd == "" {
			s.cwd = decodeClaudeProjectCWD(c.projectDir)
		}
	case productCodex:
		meta := peekCodexMeta(c.filePath)
		if meta.sessionID != "" {
			s.sessionID = meta.sessionID
		}
		s.cwd = meta.cwd
	}
	return s
}

func decodeClaudeProjectCWD(projectDir string) string {
	name := filepath.Base(projectDir)
	if !strings.HasPrefix(name, "-") {
		return ""
	}
	candidate := string(filepath.Separator) + strings.ReplaceAll(strings.TrimPrefix(name, "-"), "-", string(filepath.Separator))
	if info, err := os.Stat(candidate); err == nil && info.IsDir() {
		return candidate
	}
	return ""
}

// parseTranscript reads a session JSONL and returns just the user/assistant
// turns. Drops metadata, file-history snapshots, tool_use args, tool_result
// chatter — none of it carries taste signal.
func parseTranscript(filePath string) ([]transcriptTurn, error) {
	return parseTranscriptLines(filePath, decodeClaudeTurn)
}

func parseCodexTranscript(filePath string) ([]transcriptTurn, error) {
	return parseTranscriptLines(filePath, decodeCodexTurn)
}

func parseTranscriptLines(filePath string, decode func([]byte) (transcriptTurn, bool)) ([]transcriptTurn, error) {
	f, err := os.Open(filePath)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 1<<20), 64<<20)

	var turns []transcriptTurn
	for scanner.Scan() {
		line := scanner.Bytes()
		if len(line) == 0 {
			continue
		}
		if turn, ok := decode(line); ok {
			turns = append(turns, turn)
		}
	}
	if err := scanner.Err(); err != nil && err != io.EOF {
		return nil, err
	}
	return turns, nil
}

func parseSessionTranscript(s sessionMeta) ([]transcriptTurn, error) {
	source, ok := productSourceFor(s.product)
	if !ok {
		return nil, fmt.Errorf("unsupported product: %s", s.product)
	}
	return source.parseTranscript(s.filePath)
}

func decodeClaudeTurn(line []byte) (transcriptTurn, bool) {
	var entry struct {
		Type      string          `json:"type"`
		Timestamp string          `json:"timestamp"`
		UUID      string          `json:"uuid"`
		Message   json.RawMessage `json:"message"`
		Content   json.RawMessage `json:"content"`
	}
	if err := json.Unmarshal(line, &entry); err != nil {
		return transcriptTurn{}, false
	}
	if entry.Type != "user" && entry.Type != "assistant" {
		return transcriptTurn{}, false
	}

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
		return transcriptTurn{}, false
	}
	return transcriptTurn{
		role:      entry.Type,
		text:      text,
		timestamp: entry.Timestamp,
		uuid:      entry.UUID,
	}, true
}

func decodeCodexTurn(line []byte) (transcriptTurn, bool) {
	var entry struct {
		Timestamp string          `json:"timestamp"`
		Type      string          `json:"type"`
		Payload   json.RawMessage `json:"payload"`
	}
	if err := json.Unmarshal(line, &entry); err != nil || entry.Type != "response_item" {
		return transcriptTurn{}, false
	}
	var item struct {
		Type    string          `json:"type"`
		Role    string          `json:"role"`
		Content json.RawMessage `json:"content"`
	}
	if err := json.Unmarshal(entry.Payload, &item); err != nil || item.Type != "message" {
		return transcriptTurn{}, false
	}
	if item.Role != "user" && item.Role != "assistant" {
		return transcriptTurn{}, false
	}
	text := extractCodexMessageText(item.Content)
	if text == "" {
		return transcriptTurn{}, false
	}
	return transcriptTurn{
		role:      item.Role,
		text:      text,
		timestamp: entry.Timestamp,
	}, true
}

// renderExtractionTranscript formats user-authored turns for extraction and
// includes a bounded preceding assistant turn when the user turn contains
// correction/preference language. That gives the model enough local context to
// understand what the user was correcting without loading the whole session.
func renderExtractionTranscript(turns []transcriptTurn, zoomContextChars int) string {
	var b strings.Builder
	wrote := false
	for i, t := range turns {
		if t.role != "user" {
			continue
		}
		if wrote {
			b.WriteString("\n\n---\n\n")
		}
		wrote = true
		uuidShort := t.uuid
		if len(uuidShort) > 8 {
			uuidShort = uuidShort[:8]
		}
		fmt.Fprintf(&b, "[turn %d | user | %s]\n%s", i+1, uuidShort, t.text)
		if zoomContextChars > 0 && hasExtractionSignalMarker(t.text) {
			if prior, ok := previousAssistantTurn(turns, i); ok {
				b.WriteString("\n\n[local context: preceding assistant turn]\n")
				fmt.Fprintf(&b, "[turn %d | assistant | %s]\n%s", prior.index+1, shortUUID(prior.turn.uuid), tailString(prior.turn.text, zoomContextChars))
			}
		}
	}
	return b.String()
}

type indexedTranscriptTurn struct {
	index int
	turn  transcriptTurn
}

func previousAssistantTurn(turns []transcriptTurn, before int) (indexedTranscriptTurn, bool) {
	for i := before - 1; i >= 0; i-- {
		if turns[i].role == "assistant" {
			return indexedTranscriptTurn{index: i, turn: turns[i]}, true
		}
	}
	return indexedTranscriptTurn{}, false
}

func shortUUID(uuid string) string {
	if len(uuid) > 8 {
		return uuid[:8]
	}
	return uuid
}

func tailString(s string, max int) string {
	if max <= 0 || len(s) <= max {
		return s
	}
	return "[...assistant context truncated...]\n" + s[len(s)-max:]
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

func extractCodexMessageText(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	var s string
	if err := json.Unmarshal(raw, &s); err == nil {
		return strings.TrimSpace(s)
	}
	var blocks []map[string]any
	if err := json.Unmarshal(raw, &blocks); err != nil {
		return ""
	}
	var parts []string
	for _, b := range blocks {
		typ, _ := b["type"].(string)
		switch typ {
		case "input_text", "output_text", "text":
			if t, ok := b["text"].(string); ok {
				parts = append(parts, t)
			}
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
	if err := scanner.Err(); err != nil {
		return ""
	}
	return ""
}

type codexMeta struct {
	sessionID string
	cwd       string
}

func peekCodexMeta(filePath string) codexMeta {
	f, err := os.Open(filePath)
	if err != nil {
		return codexMeta{}
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 1<<20), 8<<20)
	for i := 0; i < 20 && scanner.Scan(); i++ {
		var entry struct {
			Type    string `json:"type"`
			Payload struct {
				ID  string `json:"id"`
				Cwd string `json:"cwd"`
			} `json:"payload"`
		}
		if err := json.Unmarshal(scanner.Bytes(), &entry); err == nil && entry.Type == "session_meta" {
			return codexMeta{sessionID: entry.Payload.ID, cwd: entry.Payload.Cwd}
		}
	}
	if err := scanner.Err(); err != nil {
		return codexMeta{}
	}
	return codexMeta{}
}
