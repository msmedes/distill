package distill

import (
	"os"
	"path/filepath"
	"syscall"
)

type store struct {
	paths paths
}

func newStore(p paths) store {
	return store{paths: p}
}

func (s store) readObservations() ([]observation, error) {
	var obs []observation
	err := s.withLock(func() error {
		var err error
		obs, err = readObservations(s.paths.observationFile)
		return err
	})
	return obs, err
}

func (s store) updateObservations(update func([]observation) ([]observation, error)) error {
	_, err := s.updateObservationsIfChanged(func(obs []observation) ([]observation, bool, error) {
		updated, err := update(obs)
		return updated, true, err
	})
	return err
}

func (s store) updateObservationsIfChanged(update func([]observation) ([]observation, bool, error)) (bool, error) {
	changed := false
	err := s.withLock(func() error {
		obs, err := readObservations(s.paths.observationFile)
		if err != nil {
			return err
		}
		updated, ok, err := update(obs)
		if err != nil {
			return err
		}
		if !ok {
			return nil
		}
		changed = true
		return writeObservations(s.paths.observationFile, updated)
	})
	return changed, err
}

func (s store) readState() (*stateFile, error) {
	var state *stateFile
	err := s.withLock(func() error {
		var err error
		state, err = readState(s.paths.stateFile)
		return err
	})
	return state, err
}

func (s store) markProcessed(sessionID string) error {
	return s.withLock(func() error {
		state, err := readState(s.paths.stateFile)
		if err != nil {
			return err
		}
		state.markProcessed(sessionID)
		return writeState(s.paths.stateFile, state)
	})
}

func (s store) applySessionDeltas(sessionID string, apply func([]observation) []observation) (bool, error) {
	applied := false
	err := s.withLock(func() error {
		state, err := readState(s.paths.stateFile)
		if err != nil {
			return err
		}
		if _, seen := state.ProcessedSessions[sessionID]; seen {
			return nil
		}
		obs, err := readObservations(s.paths.observationFile)
		if err != nil {
			return err
		}
		if err := writeObservations(s.paths.observationFile, apply(obs)); err != nil {
			return err
		}
		state.markProcessed(sessionID)
		if err := writeState(s.paths.stateFile, state); err != nil {
			return err
		}
		applied = true
		return nil
	})
	return applied, err
}

func (s store) withLock(fn func() error) error {
	if err := os.MkdirAll(s.paths.stateDir, 0o755); err != nil {
		return err
	}
	lockPath := filepath.Join(s.paths.stateDir, "store.lock")
	f, err := os.OpenFile(lockPath, os.O_CREATE|os.O_RDWR, 0o644)
	if err != nil {
		return err
	}
	defer f.Close()
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX); err != nil {
		return err
	}
	defer syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
	return fn()
}
