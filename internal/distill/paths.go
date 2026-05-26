package distill

import (
	"os"
	"path/filepath"
)

type paths struct {
	claudeProjects   string
	codexSessions    string
	stateDir         string
	observationFile  string
	stateFile        string
	preferencesFile  string
	sessionIndexFile string
	candidatesDir    string
	internalCallsDir string
}

func resolvePaths() (paths, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return paths{}, err
	}
	stateDir := filepath.Join(home, ".distill")
	return paths{
		claudeProjects:   filepath.Join(home, ".claude", "projects"),
		codexSessions:    filepath.Join(home, ".codex", "sessions"),
		stateDir:         stateDir,
		observationFile:  filepath.Join(stateDir, "observations.jsonl"),
		stateFile:        filepath.Join(stateDir, "state.json"),
		preferencesFile:  filepath.Join(stateDir, "preferences.json"),
		sessionIndexFile: filepath.Join(stateDir, "sessions.db"),
		candidatesDir:    filepath.Join(stateDir, "candidates"),
		internalCallsDir: filepath.Join(stateDir, "internal-model-calls"),
	}, nil
}

func (p paths) ensure() error {
	if err := os.MkdirAll(p.stateDir, 0o755); err != nil {
		return err
	}
	return os.MkdirAll(p.candidatesDir, 0o755)
}
