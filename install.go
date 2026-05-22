package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

const sessionEndEvent = "SessionEnd"

func runInstall(args []string) error {
	fs := flag.NewFlagSet("install", flag.ExitOnError)
	uninstall := fs.Bool("uninstall", false, "remove the distill SessionEnd hook")
	if err := fs.Parse(args); err != nil {
		return err
	}

	binaryPath, err := os.Executable()
	if err != nil {
		return fmt.Errorf("resolving distill binary path: %w", err)
	}

	home, err := os.UserHomeDir()
	if err != nil {
		return err
	}
	settingsPath := filepath.Join(home, ".claude", "settings.json")

	settings, err := readSettings(settingsPath)
	if err != nil {
		return err
	}

	command := fmt.Sprintf("%s hook", binaryPath)

	if *uninstall {
		removed := removeDistillHook(settings)
		if !removed {
			fmt.Println("no distill hook found in", settingsPath)
			return nil
		}
		if err := writeSettings(settingsPath, settings); err != nil {
			return err
		}
		fmt.Println("removed distill SessionEnd hook from", settingsPath)
		return nil
	}

	changed := upsertDistillHook(settings, command)
	if !changed {
		fmt.Println("distill SessionEnd hook already installed; nothing to do")
		fmt.Println("  settings:", settingsPath)
		fmt.Println("  command: ", command)
		return nil
	}
	if err := writeSettings(settingsPath, settings); err != nil {
		return err
	}
	fmt.Println("installed distill SessionEnd hook")
	fmt.Println("  settings:", settingsPath)
	fmt.Println("  command: ", command)
	fmt.Println()
	fmt.Println("New sessions will extract automatically when they end.")
	fmt.Println("Logs: ~/.distill/hook.log")
	return nil
}

// runHook is the entry point Claude Code invokes via the SessionEnd hook.
// It reads the hook JSON payload from stdin and spawns a detached
// `distill extract --session <id>` so Claude Code doesn't wait on the LLM call.
func runHook(_ []string) error {
	payloadBytes, err := io.ReadAll(os.Stdin)
	if err != nil {
		return fmt.Errorf("reading hook payload: %w", err)
	}
	var payload struct {
		SessionID      string `json:"session_id"`
		TranscriptPath string `json:"transcript_path"`
		HookEventName  string `json:"hook_event_name"`
		Cwd            string `json:"cwd"`
		Reason         string `json:"reason"`
	}
	if err := json.Unmarshal(payloadBytes, &payload); err != nil {
		return fmt.Errorf("parsing hook payload: %w", err)
	}
	if payload.SessionID == "" {
		return errors.New("hook payload missing session_id")
	}
	p, err := resolvePaths()
	if err != nil {
		return err
	}
	if err := p.ensure(); err != nil {
		return err
	}

	logPath := filepath.Join(p.stateDir, "hook.log")
	logFile, err := os.OpenFile(logPath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return fmt.Errorf("opening hook log: %w", err)
	}
	defer logFile.Close()

	if os.Getenv("DISTILL_INTERNAL") == "1" {
		fmt.Fprintf(logFile, "skipping internal distill claude session %s\n", payload.SessionID)
		return nil
	}

	binaryPath, err := os.Executable()
	if err != nil {
		return err
	}

	cmd := exec.Command(binaryPath, "extract", "--session", payload.SessionID)
	cmd.Stdout = logFile
	cmd.Stderr = logFile
	cmd.Stdin = nil
	detachProcess(cmd)
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("spawning extract: %w", err)
	}
	// Don't wait — the child runs detached; the parent (hook) returns immediately.
	if err := cmd.Process.Release(); err != nil {
		// Non-fatal — the child has already been started.
		fmt.Fprintln(os.Stderr, "warn:", err)
	}
	return nil
}

// settings is a minimal model of ~/.claude/settings.json that preserves
// unknown fields by carrying them in extra. We only touch hooks.SessionEnd.
type claudeSettings struct {
	raw map[string]any
}

func readSettings(path string) (*claudeSettings, error) {
	b, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return &claudeSettings{raw: map[string]any{}}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("reading %s: %w", path, err)
	}
	var raw map[string]any
	if len(b) == 0 {
		return &claudeSettings{raw: map[string]any{}}, nil
	}
	if err := json.Unmarshal(b, &raw); err != nil {
		return nil, fmt.Errorf("parsing %s: %w", path, err)
	}
	if raw == nil {
		raw = map[string]any{}
	}
	return &claudeSettings{raw: raw}, nil
}

func writeSettings(path string, s *claudeSettings) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	b, err := json.MarshalIndent(s.raw, "", "  ")
	if err != nil {
		return err
	}
	b = append(b, '\n')
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, b, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// upsertDistillHook returns true if it modified settings.
//
// settings.json hooks shape:
//
//	"hooks": {
//	  "SessionEnd": [
//	    { "hooks": [ {"type": "command", "command": "..."} ] }
//	  ]
//	}
func upsertDistillHook(s *claudeSettings, command string) bool {
	hooks := mapKey(s.raw, "hooks")
	sessionEnd := sliceKey(hooks, sessionEndEvent)

	// Look for an existing distill entry; update in place if the command path changed.
	for _, group := range sessionEnd {
		gm, ok := group.(map[string]any)
		if !ok {
			continue
		}
		entries, _ := gm["hooks"].([]any)
		for i, e := range entries {
			em, ok := e.(map[string]any)
			if !ok {
				continue
			}
			cmd, _ := em["command"].(string)
			if isDistillHookCommand(cmd) {
				if cmd == command {
					return false
				}
				em["command"] = command
				em["type"] = "command"
				entries[i] = em
				gm["hooks"] = entries
				return true
			}
		}
	}

	// No existing distill entry — append a new group.
	newGroup := map[string]any{
		"hooks": []any{
			map[string]any{"type": "command", "command": command},
		},
	}
	sessionEnd = append(sessionEnd, newGroup)
	hooks[sessionEndEvent] = sessionEnd
	s.raw["hooks"] = hooks
	return true
}

func removeDistillHook(s *claudeSettings) bool {
	hooks, ok := s.raw["hooks"].(map[string]any)
	if !ok {
		return false
	}
	rawList, ok := hooks[sessionEndEvent].([]any)
	if !ok {
		return false
	}

	changed := false
	keptGroups := rawList[:0]
	for _, group := range rawList {
		gm, ok := group.(map[string]any)
		if !ok {
			keptGroups = append(keptGroups, group)
			continue
		}
		entries, _ := gm["hooks"].([]any)
		filtered := entries[:0]
		for _, e := range entries {
			em, ok := e.(map[string]any)
			if !ok {
				filtered = append(filtered, e)
				continue
			}
			cmd, _ := em["command"].(string)
			if isDistillHookCommand(cmd) {
				changed = true
				continue
			}
			filtered = append(filtered, e)
		}
		if len(filtered) == 0 {
			changed = true
			continue
		}
		gm["hooks"] = filtered
		keptGroups = append(keptGroups, gm)
	}

	if !changed {
		return false
	}
	if len(keptGroups) == 0 {
		delete(hooks, sessionEndEvent)
	} else {
		hooks[sessionEndEvent] = keptGroups
	}
	if len(hooks) == 0 {
		delete(s.raw, "hooks")
	}
	return true
}

// isDistillHookCommand recognizes any command that invokes `distill hook`,
// so reinstalls and renames work cleanly.
func isDistillHookCommand(cmd string) bool {
	cmd = strings.TrimSpace(cmd)
	if cmd == "" {
		return false
	}
	return cmd == "distill hook" ||
		strings.HasSuffix(cmd, "/distill hook") ||
		strings.HasSuffix(cmd, `\distill hook`)
}

func mapKey(parent map[string]any, key string) map[string]any {
	if v, ok := parent[key].(map[string]any); ok {
		return v
	}
	m := map[string]any{}
	parent[key] = m
	return m
}

func sliceKey(parent map[string]any, key string) []any {
	if v, ok := parent[key].([]any); ok {
		return v
	}
	return nil
}
