package distill

import (
	"os"
	"path/filepath"
	"strings"
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

func (s store) applySessionDeltas(session sessionMeta, apply func([]observation) []observation) (bool, error) {
	applied := false
	err := s.withLock(func() error {
		state, err := readState(s.paths.stateFile)
		if err != nil {
			return err
		}
		if processedSession(state, session) {
			return nil
		}
		obs, err := readObservations(s.paths.observationFile)
		if err != nil {
			return err
		}
		if observationsContainSession(obs, session) {
			state.markProcessed(sessionStateKey(session.product, session.sessionID))
			return writeState(s.paths.stateFile, state)
		}
		if err := writeObservations(s.paths.observationFile, apply(obs)); err != nil {
			return err
		}
		state.markProcessed(sessionStateKey(session.product, session.sessionID))
		if err := writeState(s.paths.stateFile, state); err != nil {
			return err
		}
		applied = true
		return nil
	})
	return applied, err
}

func observationsContainSession(obs []observation, session sessionMeta) bool {
	key := sessionStateKey(session.product, session.sessionID)
	for _, o := range obs {
		for _, e := range o.Evidence {
			if sessionMatches(e.Product, e.SessionID, session) {
				return true
			}
		}
		if contains(o.ContradictedBy, key) {
			return true
		}
		if session.product == productClaude && contains(o.ContradictedBy, session.sessionID) {
			return true
		}
	}
	return false
}

func sessionMatches(evidenceProduct product, sessionID string, session sessionMeta) bool {
	if sessionID != session.sessionID {
		return false
	}
	if evidenceProduct == "" {
		evidenceProduct = productClaude
	}
	return evidenceProduct == session.product
}

func (s store) withLock(fn func() error) error {
	if err := os.MkdirAll(s.paths.stateDir, 0o755); err != nil {
		return err
	}
	lockPath := filepath.Join(s.paths.stateDir, "store.lock")
	return withFileLock(lockPath, fn)
}

func (s store) withNamedLock(name string, fn func() error) error {
	if err := os.MkdirAll(s.paths.stateDir, 0o755); err != nil {
		return err
	}
	return withFileLock(filepath.Join(s.paths.stateDir, lockFileName(name)+".lock"), fn)
}

func withFileLock(lockPath string, fn func() error) error {
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

func lockFileName(name string) string {
	var b strings.Builder
	for _, r := range name {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '-' || r == '_' {
			b.WriteRune(r)
			continue
		}
		b.WriteByte('_')
	}
	if b.Len() == 0 {
		return "lock"
	}
	return b.String()
}
