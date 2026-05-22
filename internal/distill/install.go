package distill

import (
	"bufio"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

func runInstall(args []string) error {
	fs := flag.NewFlagSet("install", flag.ExitOnError)
	nonInteractive := fs.Bool("yes", false, "accept recommended defaults")
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
	defaults, err := defaultPreferences()
	if err != nil {
		return err
	}
	prefs := defaults
	if !*nonInteractive {
		prefs, err = promptInstallPreferences(os.Stdin, os.Stdout, defaults)
		if err != nil {
			return err
		}
	}
	if err := ensureInstallTargets(prefs); err != nil {
		return err
	}
	if err := writePreferences(p, prefs); err != nil {
		return err
	}
	printInstallSummary(os.Stdout, prefs)
	return nil
}

func promptInstallPreferences(in io.Reader, out io.Writer, defaults preferences) (preferences, error) {
	reader := bufio.NewReader(in)
	prefs := defaults
	fmt.Fprintln(out, "distill setup")
	printInstructionFileState(out, defaults)

	watchClaude, err := promptBool(reader, out, "Watch Claude Code?", true)
	if err != nil {
		return preferences{}, err
	}
	watchCodex, err := promptBool(reader, out, "Watch Codex?", true)
	if err != nil {
		return preferences{}, err
	}
	prefs.WatchClaude = watchClaude
	prefs.WatchCodex = watchCodex
	if !prefs.WatchClaude && !prefs.WatchCodex {
		return preferences{}, fmt.Errorf("choose at least one product to watch")
	}

	unified, err := promptBool(reader, out, "Use one always-on instructions file at ~/.agents/AGENTS.md?", true)
	if err != nil {
		return preferences{}, err
	}
	if unified {
		prefs.PromotionMode = promotionModeUnified
	} else {
		prefs.PromotionMode = promotionModeSeparate
	}
	return normalizePreferences(prefs)
}

func promptBool(reader *bufio.Reader, out io.Writer, question string, defaultYes bool) (bool, error) {
	suffix := " [Y/n] "
	if !defaultYes {
		suffix = " [y/N] "
	}
	for {
		fmt.Fprint(out, question, suffix)
		line, err := reader.ReadString('\n')
		if err != nil && !errors.Is(err, io.EOF) {
			return false, err
		}
		answer := strings.ToLower(strings.TrimSpace(line))
		if answer == "" {
			return defaultYes, nil
		}
		switch answer {
		case "y", "yes":
			return true, nil
		case "n", "no":
			return false, nil
		default:
			fmt.Fprintln(out, "please answer y or n")
		}
		if errors.Is(err, io.EOF) {
			return defaultYes, nil
		}
	}
}

func printInstructionFileState(out io.Writer, prefs preferences) {
	fmt.Fprintln(out, "Instruction files:")
	printPathState(out, "agents", prefs.AlwaysOnPath)
	printPathState(out, "claude", prefs.ClaudeMDPath)
	printPathState(out, "codex", prefs.CodexAgentsPath)
}

func printPathState(out io.Writer, label, path string) {
	info, err := os.Lstat(path)
	switch {
	case errors.Is(err, os.ErrNotExist):
		fmt.Fprintf(out, "  %s: %s (missing)\n", label, path)
	case err != nil:
		fmt.Fprintf(out, "  %s: %s (%v)\n", label, path, err)
	case info.Mode()&os.ModeSymlink != 0:
		target, err := os.Readlink(path)
		if err != nil {
			fmt.Fprintf(out, "  %s: %s (symlink)\n", label, path)
			return
		}
		fmt.Fprintf(out, "  %s: %s -> %s\n", label, path, target)
	default:
		fmt.Fprintf(out, "  %s: %s (exists)\n", label, path)
	}
}

func ensureInstallTargets(prefs preferences) error {
	if err := os.MkdirAll(filepath.Dir(prefs.AlwaysOnPath), 0o755); err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(prefs.ClaudeMDPath), 0o755); err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(prefs.CodexAgentsPath), 0o755); err != nil {
		return err
	}
	return os.MkdirAll(prefs.SkillsDir, 0o755)
}

func printInstallSummary(out io.Writer, prefs preferences) {
	fmt.Fprintln(out, "saved distill preferences")
	fmt.Fprintf(out, "  watch: %s\n", prefs.watchProduct())
	if prefs.PromotionMode == promotionModeUnified {
		fmt.Fprintf(out, "  always-on: %s\n", prefs.AlwaysOnPath)
	} else {
		fmt.Fprintf(out, "  claude always-on: %s\n", prefs.ClaudeMDPath)
		fmt.Fprintf(out, "  codex always-on:  %s\n", prefs.CodexAgentsPath)
	}
	fmt.Fprintf(out, "  skills: %s\n", prefs.SkillsDir)
	fmt.Fprintln(out)
	fmt.Fprintf(out, "run: distill watch --product %s\n", prefs.watchProduct())
}
