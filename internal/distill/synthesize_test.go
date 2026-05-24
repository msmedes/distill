package distill

import (
	"strings"
	"testing"
)

func TestBuildSynthesizePromptIncludesObservationScope(t *testing.T) {
	prompt, err := buildSynthesizePrompt([]observation{{
		ID:            "obs_0001",
		Type:          typePreference,
		Claim:         "Project-specific corrections should stay local.",
		Scope:         scopeProject,
		ProjectCWD:    "/work/distill",
		EvidenceCount: 2,
	}})
	if err != nil {
		t.Fatal(err)
	}

	if !strings.Contains(prompt, "scope=project:/work/distill") {
		t.Fatalf("prompt missing project scope:\n%s", prompt)
	}
	if !strings.Contains(prompt, `"artifact": "skill" | "agents-md"`) {
		t.Fatalf("prompt missing artifact output contract:\n%s", prompt)
	}
}

func TestHasProposalDistinguishesScope(t *testing.T) {
	props := []proposal{{
		Artifact: artifactSkill,
		Scope:    scopeProject,
	}}

	if !hasProposal(props, artifactSkill, scopeProject) {
		t.Fatal("expected project skill proposal to exist")
	}
	if hasProposal(props, artifactSkill, scopeUser) {
		t.Fatal("did not expect user skill proposal to match project skill")
	}
}

func TestNormalizeLegacyProposalKind(t *testing.T) {
	p := proposal{Kind: proposalClaudeMD}
	normalizeProposal(&p, scopeProject)

	if p.Artifact != artifactAgentsMD || p.Scope != scopeProject {
		t.Fatalf("unexpected normalized legacy proposal: %#v", p)
	}
}
