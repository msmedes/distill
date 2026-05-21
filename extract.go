package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"strings"
	"time"
)

type extractOpts struct {
	sessionID         string
	recent            int
	onlyNew           bool
	dryRun            bool
	model             string
	maxTranscriptChar int
}

type stateFile struct {
	ProcessedSessions map[string]string `json:"processed_sessions"`
}

type extractionResult struct {
	Reasoning       string                  `json:"reasoning"`
	NewObservations []extractedObservation  `json:"new_observations"`
	Reinforced      []extractedReinforce    `json:"reinforced"`
	Contradicted    []extractedContradict   `json:"contradicted"`
}

type extractedObservation struct {
	Claim            string          `json:"claim"`
	Type             observationType `json:"type"`
	EvidenceTurnRefs []string        `json:"evidence_turn_refs"`
	EvidenceQuote    string          `json:"evidence_quote"`
}

type extractedReinforce struct {
	ObsID            string   `json:"obs_id"`
	EvidenceTurnRefs []string `json:"evidence_turn_refs"`
	EvidenceQuote    string   `json:"evidence_quote"`
}

type extractedContradict struct {
	ObsID            string   `json:"obs_id"`
	EvidenceTurnRefs []string `json:"evidence_turn_refs"`
	Explanation      string   `json:"explanation"`
}

func runExtract(args []string) error {
	fs := flag.NewFlagSet("extract", flag.ExitOnError)
	var (
		sessionID = fs.String("session", "", "process a specific session by id (prefix ok)")
		recent    = fs.Int("recent", 1, "number of most-recent sessions to process")
		onlyNew   = fs.Bool("new", false, "process all unprocessed sessions (overrides --recent)")
		dryRun    = fs.Bool("dry-run", false, "show what would be extracted, don't write")
		model     = fs.String("model", "haiku", "model to use: haiku | sonnet")
		maxChars  = fs.Int("max-transcript-chars", 200_000, "truncate transcripts longer than this")
	)
	if err := fs.Parse(args); err != nil {
		return err
	}

	opts := extractOpts{
		sessionID:         *sessionID,
		recent:            *recent,
		onlyNew:           *onlyNew,
		dryRun:            *dryRun,
		model:             *model,
		maxTranscriptChar: *maxChars,
	}

	p, err := resolvePaths()
	if err != nil {
		return err
	}
	if err := p.ensure(); err != nil {
		return err
	}

	state, err := readState(p.stateFile)
	if err != nil {
		return err
	}

	all, err := listSessions(p.claudeProjects)
	if err != nil {
		return err
	}

	if _, err := resolveModel(opts.model); err != nil {
		return err
	}

	targets := pickTargets(all, state, opts)
	if len(targets) == 0 {
		fmt.Println("no sessions to process")
		return nil
	}

	fmt.Printf("processing %d session(s)…\n\n", len(targets))

	for _, s := range targets {
		if err := processOne(p, state, s, opts); err != nil {
			fmt.Fprintf(os.Stderr, "  error: %v\n\n", err)
			continue
		}
	}

	return nil
}

func processOne(p paths, state *stateFile, s sessionMeta, opts extractOpts) error {
	cwdLabel := s.cwd
	if cwdLabel == "" {
		cwdLabel = "?"
	}
	fmt.Printf("── %s (%s)\n", s.sessionID[:8], cwdLabel)

	turns, err := parseTranscript(s.filePath)
	if err != nil {
		return fmt.Errorf("parsing transcript: %w", err)
	}
	if len(turns) == 0 {
		fmt.Println("  (no user/assistant turns — skipping)")
		markProcessed(p, state, s.sessionID, opts.dryRun)
		fmt.Println()
		return nil
	}

	rendered := renderTranscript(turns)
	if len(rendered) > opts.maxTranscriptChar {
		fmt.Printf("  transcript truncated: %d → %d chars\n", len(rendered), opts.maxTranscriptChar)
		rendered = rendered[:opts.maxTranscriptChar] + "\n\n[...transcript truncated...]"
	}

	existing, err := readObservations(p.observationFile)
	if err != nil {
		return fmt.Errorf("reading observations: %w", err)
	}

	prompt, err := buildExtractPrompt(s, rendered, existing)
	if err != nil {
		return err
	}

	model, err := resolveModel(opts.model)
	if err != nil {
		return err
	}

	fmt.Printf("  calling claude -p (model=%s)…\n", opts.model)
	ctx, cancel := withTimeout(3 * time.Minute)
	defer cancel()

	start := time.Now()
	raw, err := callClaude(ctx, model, prompt)
	if err != nil {
		return fmt.Errorf("claude call failed: %w", err)
	}
	fmt.Printf("  response in %.1fs\n", time.Since(start).Seconds())

	jsonText := extractJSONBlock(raw)
	var result extractionResult
	if err := json.Unmarshal([]byte(jsonText), &result); err != nil {
		fmt.Fprintf(os.Stderr, "  failed to parse extractor output: %v\n", err)
		fmt.Fprintf(os.Stderr, "  raw output:\n%s\n", truncate(raw, 2000))
		return nil
	}

	printExtractSummary(result)

	if opts.dryRun {
		fmt.Println("  (dry run — not writing)")
		fmt.Println()
		return nil
	}

	updated := applyDeltas(existing, result, s)
	if err := writeObservations(p.observationFile, updated); err != nil {
		return fmt.Errorf("writing observations: %w", err)
	}
	markProcessed(p, state, s.sessionID, false)
	fmt.Println()
	return nil
}

// markProcessed updates the in-memory state and flushes to disk immediately
// so the server (and the next `--new` invocation) sees progress mid-batch.
// In dry-run mode, the in-memory state still gets updated for the rest of
// the batch but is never persisted.
func markProcessed(p paths, state *stateFile, sessionID string, dryRun bool) {
	state.ProcessedSessions[sessionID] = time.Now().UTC().Format(time.RFC3339)
	if dryRun {
		return
	}
	if err := writeState(p.stateFile, state); err != nil {
		fmt.Fprintf(os.Stderr, "  warn: state flush failed: %v\n", err)
	}
}

func pickTargets(all []sessionMeta, state *stateFile, opts extractOpts) []sessionMeta {
	if opts.sessionID != "" {
		for _, s := range all {
			if s.sessionID == opts.sessionID || strings.HasPrefix(s.sessionID, opts.sessionID) {
				return []sessionMeta{s}
			}
		}
		return nil
	}
	if opts.onlyNew {
		var out []sessionMeta
		for _, s := range all {
			if _, seen := state.ProcessedSessions[s.sessionID]; !seen {
				out = append(out, s)
			}
		}
		// When --recent is also set, treat it as a cap on --new.
		if opts.recent > 0 && opts.recent < len(out) {
			out = out[:opts.recent]
		}
		return out
	}
	n := opts.recent
	if n > len(all) {
		n = len(all)
	}
	return all[:n]
}

func applyDeltas(existing []observation, result extractionResult, s sessionMeta) []observation {
	now := time.Now().UTC().Format(time.RFC3339)

	byID := make(map[string]*observation, len(existing))
	out := make([]observation, len(existing))
	copy(out, existing)
	for i := range out {
		byID[out[i].ID] = &out[i]
	}

	for _, r := range result.Reinforced {
		o, ok := byID[r.ObsID]
		if !ok {
			continue
		}
		o.Evidence = append(o.Evidence, evidence{
			SessionID:  s.sessionID,
			TurnRefs:   r.EvidenceTurnRefs,
			Quote:      r.EvidenceQuote,
			RecordedAt: now,
		})
		o.EvidenceCount++
		o.LastSeen = now
	}

	for _, c := range result.Contradicted {
		o, ok := byID[c.ObsID]
		if !ok {
			continue
		}
		if !contains(o.ContradictedBy, s.sessionID) {
			o.ContradictedBy = append(o.ContradictedBy, s.sessionID)
		}
		o.LastSeen = now
	}

	for _, n := range result.NewObservations {
		if !validType(n.Type) || len(n.Claim) < 8 {
			continue
		}
		id := nextObservationID(out)
		out = append(out, observation{
			ID:        id,
			Claim:     n.Claim,
			Type:      n.Type,
			FirstSeen: now,
			LastSeen:  now,
			Evidence: []evidence{{
				SessionID:  s.sessionID,
				TurnRefs:   n.EvidenceTurnRefs,
				Quote:      n.EvidenceQuote,
				RecordedAt: now,
			}},
			EvidenceCount:  1,
			ContradictedBy: []string{},
		})
	}

	for i := range out {
		dedupEvidence(&out[i])
	}
	return out
}

func printExtractSummary(r extractionResult) {
	if r.Reasoning != "" {
		fmt.Printf("  reasoning: %s\n", r.Reasoning)
	}
	fmt.Printf("  new=%d reinforced=%d contradicted=%d\n",
		len(r.NewObservations), len(r.Reinforced), len(r.Contradicted))
	for _, n := range r.NewObservations {
		fmt.Printf("    + [%s] %s\n", n.Type, n.Claim)
	}
	for _, r := range r.Reinforced {
		fmt.Printf("    ^ %s (%s)\n", r.ObsID, strings.Join(r.EvidenceTurnRefs, ", "))
	}
	for _, c := range r.Contradicted {
		fmt.Printf("    ! %s: %s\n", c.ObsID, c.Explanation)
	}
}

func buildExtractPrompt(s sessionMeta, transcript string, existing []observation) (string, error) {
	raw, err := promptsFS.ReadFile("prompts/extract.md")
	if err != nil {
		return "", err
	}
	cwd := s.cwd
	if cwd == "" {
		cwd = "(unknown)"
	}
	out := string(raw)
	out = strings.ReplaceAll(out, "{{SESSION_ID}}", s.sessionID)
	out = strings.ReplaceAll(out, "{{SESSION_CWD}}", cwd)
	out = strings.ReplaceAll(out, "{{TRANSCRIPT}}", transcript)
	out = strings.ReplaceAll(out, "{{EXISTING_OBSERVATIONS}}", renderObservationsForPrompt(existing))
	return out, nil
}

func readState(path string) (*stateFile, error) {
	f, err := os.Open(path)
	if errors.Is(err, os.ErrNotExist) {
		return &stateFile{ProcessedSessions: map[string]string{}}, nil
	}
	if err != nil {
		return nil, err
	}
	defer f.Close()

	var s stateFile
	if err := json.NewDecoder(f).Decode(&s); err != nil {
		return &stateFile{ProcessedSessions: map[string]string{}}, nil
	}
	if s.ProcessedSessions == nil {
		s.ProcessedSessions = map[string]string{}
	}
	return &s, nil
}

func writeState(path string, s *stateFile) error {
	tmp := path + ".tmp"
	f, err := os.Create(tmp)
	if err != nil {
		return err
	}
	enc := json.NewEncoder(f)
	enc.SetIndent("", "  ")
	if err := enc.Encode(s); err != nil {
		f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

func contains(xs []string, x string) bool {
	for _, v := range xs {
		if v == x {
			return true
		}
	}
	return false
}
