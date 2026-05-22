package distill

import (
	"path/filepath"
	"testing"
)

func TestApplySessionDeltasSkipsAlreadyProcessedSession(t *testing.T) {
	dir := t.TempDir()
	st := newStore(paths{
		stateDir:        dir,
		observationFile: filepath.Join(dir, "observations.jsonl"),
		stateFile:       filepath.Join(dir, "state.json"),
	})

	applied, err := st.applySessionDeltas("session-1", func(obs []observation) []observation {
		return append(obs, observation{ID: "obs_0001", Claim: "one"})
	})
	if err != nil {
		t.Fatal(err)
	}
	if !applied {
		t.Fatal("expected first application to write")
	}

	applied, err = st.applySessionDeltas("session-1", func(obs []observation) []observation {
		return append(obs, observation{ID: "obs_0002", Claim: "two"})
	})
	if err != nil {
		t.Fatal(err)
	}
	if applied {
		t.Fatal("expected duplicate session application to skip")
	}

	obs, err := st.readObservations()
	if err != nil {
		t.Fatal(err)
	}
	if len(obs) != 1 || obs[0].ID != "obs_0001" {
		t.Fatalf("expected only the first observation, got %#v", obs)
	}
}
