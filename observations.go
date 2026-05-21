package main

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"sort"
	"strconv"
	"strings"
)

type observationType string

const (
	typePreference observationType = "preference"
	typeWorkflow   observationType = "workflow"
	typeFriction   observationType = "friction"
	typeToolUse    observationType = "tool-use"
)

func validType(t observationType) bool {
	switch t {
	case typePreference, typeWorkflow, typeFriction, typeToolUse:
		return true
	}
	return false
}

type evidence struct {
	SessionID  string   `json:"session_id"`
	TurnRefs   []string `json:"turn_refs"`
	Quote      string   `json:"quote,omitempty"`
	RecordedAt string   `json:"recorded_at"`
}

type note struct {
	At   string `json:"at"`
	Text string `json:"text"`
}

// proposal is an LLM-generated suggestion that an observation should be
// promoted somewhere. Lives on the observation until the user accepts or
// dismisses it.
type proposal struct {
	Kind      string `json:"kind"` // "skill" | "claude-md"
	At        string `json:"at"`
	Reasoning string `json:"reasoning"`
}

const (
	proposalSkill    = "skill"
	proposalClaudeMD = "claude-md"
)

// Status values: an observation is "active" by default and exits that state
// via user action (ignore, or promote to CLAUDE.md / a skill).
const (
	statusActive            = "active"
	statusIgnored           = "ignored"
	statusPromotedClaudeMD  = "promoted-claude-md"
	statusPromotedToSkill   = "promoted-skill"
)

type observation struct {
	ID             string          `json:"id"`
	Claim          string          `json:"claim"`
	Type           observationType `json:"type"`
	FirstSeen      string          `json:"first_seen"`
	LastSeen       string          `json:"last_seen"`
	Evidence       []evidence      `json:"evidence"`
	EvidenceCount  int             `json:"evidence_count"`
	ContradictedBy []string        `json:"contradicted_by"`

	// User-curation state. Defaulted in readObservations so older records
	// still load cleanly.
	Status     string     `json:"status"`
	PromotedTo string     `json:"promoted_to,omitempty"`
	Notes      []note     `json:"notes"`
	Proposals  []proposal `json:"proposals"`
}

func readObservations(path string) ([]observation, error) {
	f, err := os.Open(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 1<<20), 16<<20)

	var out []observation
	for scanner.Scan() {
		line := scanner.Bytes()
		if len(line) == 0 {
			continue
		}
		var o observation
		if err := json.Unmarshal(line, &o); err != nil {
			fmt.Fprintf(os.Stderr, "warn: skipping malformed observation: %v\n", err)
			continue
		}
		if o.ContradictedBy == nil {
			o.ContradictedBy = []string{}
		}
		if o.Evidence == nil {
			o.Evidence = []evidence{}
		}
		if o.Notes == nil {
			o.Notes = []note{}
		}
		if o.Proposals == nil {
			o.Proposals = []proposal{}
		}
		if o.Status == "" {
			o.Status = statusActive
		}
		out = append(out, o)
	}
	if err := scanner.Err(); err != nil && err != io.EOF {
		return nil, err
	}
	return out, nil
}

func writeObservations(path string, obs []observation) error {
	tmp := path + ".tmp"
	f, err := os.Create(tmp)
	if err != nil {
		return err
	}
	w := bufio.NewWriter(f)
	enc := json.NewEncoder(w)
	for _, o := range obs {
		if err := enc.Encode(o); err != nil {
			f.Close()
			return err
		}
	}
	if err := w.Flush(); err != nil {
		f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

func nextObservationID(existing []observation) string {
	max := 0
	for _, o := range existing {
		n, err := strconv.Atoi(strings.TrimPrefix(o.ID, "obs_"))
		if err == nil && n > max {
			max = n
		}
	}
	return fmt.Sprintf("obs_%04d", max+1)
}

// dedupEvidence collapses evidence entries that share the same quote text.
// Same quote = same turn, regardless of which session file it surfaced in
// (Claude Code creates a fresh session file on every resume/branch, copying
// prior turns verbatim). Keeps the first entry per quote and resets the count
// to the deduped length.
func dedupEvidence(o *observation) (removed int) {
	if len(o.Evidence) <= 1 {
		o.EvidenceCount = len(o.Evidence)
		return 0
	}
	seen := map[string]bool{}
	kept := make([]evidence, 0, len(o.Evidence))
	for _, e := range o.Evidence {
		key := strings.TrimSpace(e.Quote)
		if key == "" {
			kept = append(kept, e)
			continue
		}
		if seen[key] {
			removed++
			continue
		}
		seen[key] = true
		kept = append(kept, e)
	}
	o.Evidence = kept
	o.EvidenceCount = len(kept)
	return removed
}

// renderObservationsForPrompt produces a compact view the extractor can scan
// before deciding whether to add a new observation. Claim + type + count only;
// evidence arrays are too heavy for context.
func renderObservationsForPrompt(obs []observation) string {
	if len(obs) == 0 {
		return "(no prior observations)"
	}
	sorted := make([]observation, len(obs))
	copy(sorted, obs)
	sort.Slice(sorted, func(i, j int) bool {
		return sorted[i].ID < sorted[j].ID
	})
	var b strings.Builder
	for _, o := range sorted {
		fmt.Fprintf(&b, "- %s [%s | count=%d]: %s\n", o.ID, o.Type, o.EvidenceCount, o.Claim)
	}
	return strings.TrimRight(b.String(), "\n")
}
