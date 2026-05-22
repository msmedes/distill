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
		ObsID     string `json:"obs_id"`
		Kind      string `json:"kind"`
		Reasoning string `json:"reasoning"`
	} `json:"proposals"`
}

func runSynthesize(args []string) error {
	fs := flag.NewFlagSet("synthesize", flag.ExitOnError)
	model := fs.String("model", "sonnet", "model to use: haiku | sonnet | opus")
	if err := fs.Parse(args); err != nil {
		return err
	}

	p, err := resolvePaths()
	if err != nil {
		return err
	}
	if err := p.ensure(); err != nil {
		return err
	}

	resolved, err := resolveModel(*model)
	if err != nil {
		return err
	}

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()

	added, err := synthesizeProposals(ctx, p, resolved)
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
func synthesizeProposals(ctx context.Context, p paths, model modelID) (int, error) {
	st := newStore(p)
	obs, err := st.readObservations()
	if err != nil {
		return 0, err
	}

	active := activeObservations(obs)
	if len(active) == 0 {
		return 0, nil
	}

	prompt, err := buildSynthesizePrompt(active)
	if err != nil {
		return 0, err
	}

	raw, err := callClaude(ctx, model, prompt)
	if err != nil {
		return 0, fmt.Errorf("synthesize call: %w", err)
	}

	var resp proposalsResponse
	if err := json.Unmarshal([]byte(extractJSONBlock(raw)), &resp); err != nil {
		return 0, fmt.Errorf("parsing synthesis output: %w\nraw: %s", err, truncate(raw, 1500))
	}

	now := time.Now().UTC().Format(time.RFC3339)
	added := 0
	_, err = st.updateObservationsIfChanged(func(obs []observation) ([]observation, bool, error) {
		for _, prop := range resp.Proposals {
			if prop.Kind != proposalSkill && prop.Kind != proposalClaudeMD {
				continue
			}
			i, ok := findObservation(obs, prop.ObsID)
			if !ok {
				continue
			}
			if obs[i].Status != statusActive {
				continue
			}
			if hasProposalOfKind(obs[i].Proposals, prop.Kind) {
				continue
			}
			obs[i].Proposals = append(obs[i].Proposals, proposal{
				Kind:      prop.Kind,
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

func activeObservations(obs []observation) []observation {
	var out []observation
	for _, o := range obs {
		if o.Status == statusActive {
			out = append(out, o)
		}
	}
	return out
}

func hasProposalOfKind(props []proposal, kind string) bool {
	for _, p := range props {
		if p.Kind == kind {
			return true
		}
	}
	return false
}

func buildSynthesizePrompt(obs []observation) (string, error) {
	raw, err := promptsFS.ReadFile("prompts/synthesize.md")
	if err != nil {
		return "", err
	}
	var b strings.Builder
	for _, o := range obs {
		fmt.Fprintf(&b, "## %s [%s] count=%d\n", o.ID, o.Type, o.EvidenceCount)
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
