package distill

import (
	"path/filepath"
	"testing"
)

func TestPromotableObservationReturnsAlreadyPromotedObservation(t *testing.T) {
	dir := t.TempDir()
	st := newStore(paths{
		stateDir:        dir,
		observationFile: filepath.Join(dir, "observations.jsonl"),
		stateFile:       filepath.Join(dir, "state.json"),
	})
	if err := writeObservations(st.paths.observationFile, []observation{{
		ID:         "obs_0001",
		Claim:      "Prefer direct answers",
		Status:     statusPromotedToSkill,
		PromotedTo: "/tmp/skill/SKILL.md",
	}}); err != nil {
		t.Fatal(err)
	}

	obs, err := promotableObservation(st, "obs_0001")
	if err != nil {
		t.Fatal(err)
	}
	if obs.PromotedTo != "/tmp/skill/SKILL.md" {
		t.Fatalf("expected promoted path, got %#v", obs)
	}
}

func TestFinalizeSkillPromotionRejectsInactiveObservation(t *testing.T) {
	dir := t.TempDir()
	st := newStore(paths{
		stateDir:        dir,
		observationFile: filepath.Join(dir, "observations.jsonl"),
		stateFile:       filepath.Join(dir, "state.json"),
	})
	if err := writeObservations(st.paths.observationFile, []observation{{
		ID:     "obs_0001",
		Claim:  "Prefer direct answers",
		Status: statusIgnored,
	}}); err != nil {
		t.Fatal(err)
	}

	if err := finalizeSkillPromotion(st, "obs_0001", "/tmp/skill/SKILL.md"); err == nil {
		t.Fatal("expected finalizing inactive observation to fail")
	}
}
