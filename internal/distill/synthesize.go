package distill

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"strings"
	"time"
)

type proposalsResponse struct {
	Proposals []struct {
		ObsID     string           `json:"obs_id"`
		Kind      string           `json:"kind,omitempty"`
		Artifact  string           `json:"artifact,omitempty"`
		Scope     observationScope `json:"scope,omitempty"`
		Reasoning string           `json:"reasoning"`
	} `json:"proposals"`
}

type synthesizedProposal struct {
	ObsID     string           `json:"obs_id"`
	Artifact  string           `json:"artifact"`
	Scope     observationScope `json:"scope"`
	Reasoning string           `json:"reasoning"`
}

func runSynthesize(args []string) error {
	p, err := resolvePaths()
	if err != nil {
		return err
	}
	if err := p.ensure(); err != nil {
		return err
	}
	prefs, err := readPreferences(p)
	if err != nil {
		return err
	}

	fs := flag.NewFlagSet("synthesize", flag.ExitOnError)
	backend := fs.String("backend", prefs.GenerationBackend, "backend to use: claude | codex")
	model := fs.String("model", prefs.GenerationModel, "model to use")
	if err := fs.Parse(args); err != nil {
		return err
	}
	prefs.GenerationBackend = *backend
	prefs.GenerationModel = *model
	prefs, err = normalizePreferences(prefs)
	if err != nil {
		return err
	}

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()

	added, err := synthesizeProposals(ctx, p, prefs)
	if err != nil {
		return err
	}

	if added == 0 {
		fmt.Println("no new proposals")
		return nil
	}
	fmt.Printf("added %d proposal(s)\n", added)
	return nil
}

// synthesizeProposals runs one synthesis pass over active observations and
// attaches LLM-proposed promotions. Returns the number of newly-attached
// proposals (proposals already present on an observation are skipped).
func synthesizeProposals(ctx context.Context, p paths, prefs preferences) (int, error) {
	resp, err := generateSynthesizedProposals(ctx, p, prefs)
	if err != nil {
		return 0, err
	}
	st := newStore(p)
	now := time.Now().UTC().Format(time.RFC3339)
	added := 0
	_, err = st.updateObservationsIfChanged(func(obs []observation) ([]observation, bool, error) {
		for _, prop := range resp {
			i, ok := findObservation(obs, prop.ObsID)
			if !ok {
				continue
			}
			if obs[i].Status != statusActive {
				continue
			}
			if hasProposal(obs[i].Proposals, prop.Artifact, prop.Scope) {
				continue
			}
			obs[i].Proposals = append(obs[i].Proposals, proposal{
				Kind:      prop.Artifact,
				Artifact:  prop.Artifact,
				Scope:     prop.Scope,
				At:        now,
				Reasoning: strings.TrimSpace(prop.Reasoning),
			})
			added++
		}
		return obs, added > 0, nil
	})
	if err != nil {
		return added, err
	}
	return added, nil
}

func generateSynthesizedProposals(ctx context.Context, p paths, prefs preferences) ([]synthesizedProposal, error) {
	st := newStore(p)
	obs, err := st.readObservations()
	if err != nil {
		return nil, err
	}

	active := activeObservations(obs)
	if len(active) == 0 {
		return nil, nil
	}

	prompt, err := buildSynthesizePrompt(active)
	if err != nil {
		return nil, err
	}

	raw, err := callGeneration(ctx, prefs, prompt)
	if err != nil {
		return nil, fmt.Errorf("synthesize call: %w", err)
	}

	var resp proposalsResponse
	if err := json.Unmarshal([]byte(extractJSONBlock(raw)), &resp); err != nil {
		return nil, fmt.Errorf("parsing synthesis output: %w\nraw: %s", err, truncate(raw, 1500))
	}

	byID := make(map[string]observation, len(obs))
	for _, o := range obs {
		byID[o.ID] = o
	}
	var out []synthesizedProposal
	for _, prop := range resp.Proposals {
		artifact := normalizeProposalArtifact(prop.Artifact, prop.Kind)
		if artifact != artifactSkill && artifact != artifactAgentsMD {
			continue
		}
		o, ok := byID[prop.ObsID]
		if !ok || o.Status != statusActive {
			continue
		}
		scope := prop.Scope
		if !validScope(scope) {
			scope = o.Scope
		}
		if !validProposalScope(o, scope) || hasProposal(o.Proposals, artifact, scope) {
			continue
		}
		out = append(out, synthesizedProposal{
			ObsID:     prop.ObsID,
			Artifact:  artifact,
			Scope:     scope,
			Reasoning: strings.TrimSpace(prop.Reasoning),
		})
	}
	return out, nil
}

func activeObservations(obs []observation) []observation {
	var out []observation
	for _, o := range obs {
		if o.Status == statusActive {
			out = append(out, o)
		}
	}
	return out
}

func hasProposal(props []proposal, artifact string, scope observationScope) bool {
	for _, p := range props {
		normalizeProposal(&p, scope)
		if p.Artifact == artifact && p.Scope == scope {
			return true
		}
	}
	return false
}

func normalizeProposalArtifact(artifact, kind string) string {
	if artifact != "" {
		return artifact
	}
	switch kind {
	case proposalClaudeMD:
		return artifactAgentsMD
	case proposalSkill:
		return artifactSkill
	default:
		return kind
	}
}

func validProposalScope(o observation, scope observationScope) bool {
	if !validScope(scope) {
		return false
	}
	if scope == scopeProject {
		return strings.TrimSpace(o.ProjectCWD) != ""
	}
	return true
}

func buildSynthesizePrompt(obs []observation) (string, error) {
	raw, err := promptsFS.ReadFile("prompts/synthesize.md")
	if err != nil {
		return "", err
	}
	var b strings.Builder
	for _, o := range obs {
		scope := string(o.Scope)
		if o.Scope == scopeProject && o.ProjectCWD != "" {
			scope += ":" + o.ProjectCWD
		}
		fmt.Fprintf(&b, "## %s [%s] scope=%s count=%d\n", o.ID, o.Type, scope, o.EvidenceCount)
		fmt.Fprintf(&b, "claim: %s\n", o.Claim)
		if len(o.ContradictedBy) > 0 {
			fmt.Fprintf(&b, "contradicted by %d session(s)\n", len(o.ContradictedBy))
		}
		if len(o.Notes) > 0 {
			b.WriteString("notes:\n")
			for _, n := range o.Notes {
				fmt.Fprintf(&b, "  - %s\n", n.Text)
			}
		}
		if len(o.Evidence) > 0 {
			b.WriteString("evidence:\n")
			for _, e := range o.Evidence {
				q := strings.ReplaceAll(strings.TrimSpace(e.Quote), "\n", " ")
				if len(q) > 200 {
					q = q[:200] + "…"
				}
				if q != "" {
					fmt.Fprintf(&b, "  - %q\n", q)
				}
			}
		}
		b.WriteString("\n")
	}
	return strings.ReplaceAll(string(raw), "{{OBSERVATIONS}}", strings.TrimRight(b.String(), "\n")), nil
}
