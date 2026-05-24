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
	"strconv"
	"strings"
)

type observationType string
type observationScope string

const (
	typePreference observationType = "preference"
	typeWorkflow   observationType = "workflow"
	typeFriction   observationType = "friction"
	typeToolUse    observationType = "tool-use"
)

const (
	scopeUser    observationScope = "user"
	scopeProject observationScope = "project"
)

func validType(t observationType) bool {
	switch t {
	case typePreference, typeWorkflow, typeFriction, typeToolUse:
		return true
	}
	return false
}

func validScope(s observationScope) bool {
	switch s {
	case scopeUser, scopeProject:
		return true
	}
	return false
}

type evidence struct {
	SessionID  string   `json:"session_id"`
	Product    product  `json:"product,omitempty"`
	ProjectCWD string   `json:"project_cwd,omitempty"`
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
	// Kind is the legacy JSON field. New records use Artifact, but Kind stays
	// readable so older observations do not lose pending proposals.
	Kind      string           `json:"kind,omitempty"`
	Artifact  string           `json:"artifact,omitempty"` // "skill" | "agents-md"
	Scope     observationScope `json:"scope,omitempty"`
	At        string           `json:"at"`
	Reasoning string           `json:"reasoning"`
}

const (
	artifactSkill    = "skill"
	artifactAgentsMD = "agents-md"

	proposalSkill    = artifactSkill
	proposalClaudeMD = "claude-md"
)

// Status values: an observation is "active" by default and exits that state
// via user action (ignore, or promote to always-on instructions / a skill).
const (
	statusActive           = "active"
	statusIgnored          = "ignored"
	statusPromotedClaudeMD = "promoted-claude-md"
	statusPromotedToSkill  = "promoted-skill"
)

type observation struct {
	ID             string           `json:"id"`
	Claim          string           `json:"claim"`
	Type           observationType  `json:"type"`
	Scope          observationScope `json:"scope"`
	ProjectCWD     string           `json:"project_cwd,omitempty"`
	FirstSeen      string           `json:"first_seen"`
	LastSeen       string           `json:"last_seen"`
	Evidence       []evidence       `json:"evidence"`
	EvidenceCount  int              `json:"evidence_count"`
	ContradictedBy []string         `json:"contradicted_by"`

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
			return nil, fmt.Errorf("malformed observation record: %w", err)
		}
		normalizeObservation(&o)
		out = append(out, o)
	}
	if err := scanner.Err(); err != nil && err != io.EOF {
		return nil, err
	}
	return out, nil
}

func writeObservations(path string, obs []observation) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	f, err := os.CreateTemp(filepath.Dir(path), filepath.Base(path)+".*.tmp")
	if err != nil {
		return err
	}
	tmp := f.Name()
	defer os.Remove(tmp)
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

func normalizeObservation(o *observation) {
	if o.ContradictedBy == nil {
		o.ContradictedBy = []string{}
	}
	if o.Evidence == nil {
		o.Evidence = []evidence{}
	}
	for i := range o.Evidence {
		if o.Evidence[i].Product == "" {
			o.Evidence[i].Product = productClaude
		}
	}
	if o.Notes == nil {
		o.Notes = []note{}
	}
	if o.Proposals == nil {
		o.Proposals = []proposal{}
	}
	if !validScope(o.Scope) {
		o.Scope = scopeUser
	}
	if o.Scope != scopeProject {
		o.ProjectCWD = ""
	}
	for i := range o.Proposals {
		normalizeProposal(&o.Proposals[i], o.Scope)
	}
	if o.Status == "" {
		o.Status = statusActive
	}
}

func normalizeProposal(p *proposal, fallbackScope observationScope) {
	if p.Artifact == "" {
		switch p.Kind {
		case proposalClaudeMD:
			p.Artifact = artifactAgentsMD
		case proposalSkill:
			p.Artifact = artifactSkill
		}
	}
	if p.Kind == "" {
		p.Kind = p.Artifact
	}
	if !validScope(p.Scope) {
		if validScope(fallbackScope) {
			p.Scope = fallbackScope
		} else {
			p.Scope = scopeUser
		}
	}
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
		scope := string(o.Scope)
		if o.Scope == scopeProject && o.ProjectCWD != "" {
			scope += ":" + o.ProjectCWD
		}
		fmt.Fprintf(&b, "- %s [%s | scope=%s | count=%d]: %s\n", o.ID, o.Type, scope, o.EvidenceCount, o.Claim)
	}
	return strings.TrimRight(b.String(), "\n")
}
