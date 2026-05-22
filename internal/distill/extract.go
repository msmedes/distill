package distill

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"sort"
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
	minUserTurns      int
	minUserChars      int
	maxObservations   int
	zoomContextChars  int
	noSkip            bool
}

type stateFile struct {
	ProcessedSessions map[string]string `json:"processed_sessions"`
}

type extractionResult struct {
	Reasoning       string                 `json:"reasoning"`
	NewObservations []extractedObservation `json:"new_observations"`
	Reinforced      []extractedReinforce   `json:"reinforced"`
	Contradicted    []extractedContradict  `json:"contradicted"`
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
		maxChars  = fs.Int("max-transcript-chars", 60_000, "truncate rendered user-message excerpts longer than this")
		minTurns  = fs.Int("min-user-turns", 2, "skip sessions with fewer user turns unless high-signal language appears")
		minChars  = fs.Int("min-user-chars", 200, "skip sessions with fewer user-message chars unless high-signal language appears")
		maxObs    = fs.Int("max-observations", 80, "maximum relevant existing observations to include in extractor prompt")
		zoomChars = fs.Int("zoom-context-chars", 2500, "max chars from the preceding assistant turn to include around high-signal user turns")
		noSkip    = fs.Bool("no-skip", false, "disable cheap local skipping for short low-signal sessions")
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
		minUserTurns:      *minTurns,
		minUserChars:      *minChars,
		maxObservations:   *maxObs,
		zoomContextChars:  *zoomChars,
		noSkip:            *noSkip,
	}

	p, err := resolvePaths()
	if err != nil {
		return err
	}
	if err := p.ensure(); err != nil {
		return err
	}

	st := newStore(p)
	state, err := st.readState()
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
		if err := processOne(st, state, s, opts); err != nil {
			fmt.Fprintf(os.Stderr, "  error: %v\n\n", err)
			continue
		}
	}

	return nil
}

func processOne(st store, state *stateFile, s sessionMeta, opts extractOpts) error {
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
		markProcessed(st, state, s.sessionID, opts.dryRun)
		fmt.Println()
		return nil
	}

	signal := summarizeTranscriptSignal(turns)
	if !opts.noSkip && shouldSkipExtraction(signal, opts) {
		fmt.Printf("  low-signal session: %d user turn(s), %d user chars, no correction/preference markers — skipping claude call\n",
			signal.userTurns, signal.userChars)
		markProcessed(st, state, s.sessionID, opts.dryRun)
		fmt.Println()
		return nil
	}

	rendered := renderExtractionTranscript(turns, opts.zoomContextChars)
	if len(rendered) > opts.maxTranscriptChar {
		fmt.Printf("  user-message excerpt truncated: %d → %d chars\n", len(rendered), opts.maxTranscriptChar)
		rendered = rendered[:opts.maxTranscriptChar] + "\n\n[...transcript truncated...]"
	}

	existing, err := st.readObservations()
	if err != nil {
		return fmt.Errorf("reading observations: %w", err)
	}

	relevantExisting := relevantObservations(existing, rendered, opts.maxObservations)
	if len(relevantExisting) < len(existing) {
		fmt.Printf("  prompt context: %d/%d observations selected\n", len(relevantExisting), len(existing))
	}

	prompt, err := buildExtractPrompt(s, rendered, relevantExisting)
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

	applied, err := st.applySessionDeltas(s.sessionID, func(current []observation) []observation {
		return applyDeltas(current, result, s)
	})
	if err != nil {
		return fmt.Errorf("writing observations: %w", err)
	}
	if applied {
		state.markProcessed(s.sessionID)
	} else {
		fmt.Println("  session was already processed by another distill process — skipping write")
	}
	fmt.Println()
	return nil
}

// markProcessed updates the in-memory state and flushes to disk immediately
// so the server (and the next `--new` invocation) sees progress mid-batch.
// In dry-run mode, the in-memory state still gets updated for the rest of
// the batch but is never persisted.
func markProcessed(st store, state *stateFile, sessionID string, dryRun bool) {
	state.markProcessed(sessionID)
	if dryRun {
		return
	}
	if err := st.markProcessed(sessionID); err != nil {
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

type transcriptSignal struct {
	userTurns   int
	userChars   int
	markerCount int
}

var extractionSignalMarkers = []string{
	"actually",
	"always",
	"don't",
	"do not",
	"i don't want",
	"i prefer",
	"i want",
	"instead",
	"make sure",
	"never",
	"no,",
	"remember",
	"shouldn't",
	"stop",
	"that's wrong",
	"wait",
	"wrong",
	"you keep",
}

func summarizeTranscriptSignal(turns []transcriptTurn) transcriptSignal {
	var s transcriptSignal
	for _, t := range turns {
		if t.role != "user" {
			continue
		}
		s.userTurns++
		s.userChars += len(t.text)
		if hasExtractionSignalMarker(t.text) {
			s.markerCount++
		}
	}
	return s
}

func shouldSkipExtraction(s transcriptSignal, opts extractOpts) bool {
	if s.markerCount > 0 {
		return false
	}
	return s.userTurns < opts.minUserTurns || s.userChars < opts.minUserChars
}

func hasExtractionSignalMarker(text string) bool {
	lower := strings.ToLower(text)
	for _, marker := range extractionSignalMarkers {
		if strings.Contains(lower, marker) {
			return true
		}
	}
	return false
}

func relevantObservations(obs []observation, query string, max int) []observation {
	if max <= 0 || len(obs) <= max {
		return obs
	}
	queryTokens := tokenSet(query)
	type scoredObservation struct {
		observation observation
		score       int
	}
	scored := make([]scoredObservation, 0, len(obs))
	for _, o := range obs {
		score := 0
		for token := range tokenSet(o.Claim) {
			if queryTokens[token] {
				score++
			}
		}
		if score > 0 {
			scored = append(scored, scoredObservation{observation: o, score: score})
		}
	}
	sort.Slice(scored, func(i, j int) bool {
		if scored[i].score != scored[j].score {
			return scored[i].score > scored[j].score
		}
		if scored[i].observation.EvidenceCount != scored[j].observation.EvidenceCount {
			return scored[i].observation.EvidenceCount > scored[j].observation.EvidenceCount
		}
		return scored[i].observation.ID < scored[j].observation.ID
	})
	if len(scored) > max {
		scored = scored[:max]
	}
	out := make([]observation, len(scored))
	for i, o := range scored {
		out[i] = o.observation
	}
	return out
}

func tokenSet(text string) map[string]bool {
	tokens := strings.FieldsFunc(strings.ToLower(text), func(r rune) bool {
		return !(r >= 'a' && r <= 'z') && !(r >= '0' && r <= '9')
	})
	out := make(map[string]bool, len(tokens))
	for _, token := range tokens {
		if len(token) < 4 {
			continue
		}
		out[token] = true
	}
	return out
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
		return nil, fmt.Errorf("parsing state file: %w", err)
	}
	if s.ProcessedSessions == nil {
		s.ProcessedSessions = map[string]string{}
	}
	return &s, nil
}

func writeState(path string, s *stateFile) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	f, err := os.CreateTemp(filepath.Dir(path), filepath.Base(path)+".*.tmp")
	if err != nil {
		return err
	}
	tmp := f.Name()
	defer os.Remove(tmp)
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

func (s *stateFile) markProcessed(sessionID string) {
	if s.ProcessedSessions == nil {
		s.ProcessedSessions = map[string]string{}
	}
	s.ProcessedSessions[sessionID] = time.Now().UTC().Format(time.RFC3339)
}

func contains(xs []string, x string) bool {
	for _, v := range xs {
		if v == x {
			return true
		}
	}
	return false
}
