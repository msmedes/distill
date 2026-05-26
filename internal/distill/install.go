package distill

import (
	"bufio"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

const launchdLabel = "com.msmedes.distill.watch"
const installBootstrapRecentLimit = 15

type installPlan struct {
	preferences    preferences
	bootstrapCount int
}

var launchctl = func(args ...string) error {
	return exec.Command("launchctl", args...).Run()
}

var brewPrefix = func(formula string) (string, error) {
	out, err := exec.Command("brew", "--prefix", formula).Output()
	return strings.TrimSpace(string(out)), err
}

var brewServices = func(args ...string) error {
	return exec.Command("brew", append([]string{"services"}, args...)...).Run()
}

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
	plan := installPlan{preferences: prefs, bootstrapCount: installBootstrapRecentLimit}
	if !*nonInteractive {
		plan, err = promptInstallPlan(os.Stdin, os.Stdout, defaults)
		if err != nil {
			return err
		}
		prefs = plan.preferences
	} else {
		prefs = plan.preferences
	}
	if err := ensureInstallTargets(prefs); err != nil {
		return err
	}
	if err := writePreferences(p, prefs); err != nil {
		return err
	}
	if err := bootstrapInstallSessions(p, prefs, plan.bootstrapCount, os.Stdout); err != nil {
		return err
	}
	if err := configureLaunchdWatcher(prefs); err != nil {
		return err
	}
	printInstallSummary(os.Stdout, prefs)
	return nil
}

func promptInstallPreferences(in io.Reader, out io.Writer, defaults preferences) (preferences, error) {
	plan, err := promptInstallPlan(in, out, defaults)
	return plan.preferences, err
}

func promptInstallPlan(in io.Reader, out io.Writer, defaults preferences) (installPlan, error) {
	reader := bufio.NewReader(in)
	prefs := defaults
	fmt.Fprintln(out, "distill setup")
	printInstructionFileState(out, defaults)

	watchClaude, err := promptBool(reader, out, "Watch Claude Code?", true)
	if err != nil {
		return installPlan{}, err
	}
	watchCodex, err := promptBool(reader, out, "Watch Codex?", true)
	if err != nil {
		return installPlan{}, err
	}
	prefs.WatchClaude = watchClaude
	prefs.WatchCodex = watchCodex
	if !prefs.WatchClaude && !prefs.WatchCodex {
		return installPlan{}, fmt.Errorf("choose at least one product to watch")
	}

	unified, err := promptBool(reader, out, "Use one user-scoped instructions file at ~/.agents/AGENTS.md?", true)
	if err != nil {
		return installPlan{}, err
	}
	if unified {
		prefs.PromotionMode = promotionModeUnified
	} else {
		prefs.PromotionMode = promotionModeSeparate
	}
	bootstrapCount, err := promptInt(reader, out, "Process recent quiet sessions now? (0 to skip)", installBootstrapRecentLimit)
	if err != nil {
		return installPlan{}, err
	}
	autoWatch, err := promptBool(reader, out, "Watch future sessions automatically at login?", true)
	if err != nil {
		return installPlan{}, err
	}
	prefs.AutomaticWatch = autoWatch
	prefs, err = normalizePreferences(prefs)
	if err != nil {
		return installPlan{}, err
	}
	return installPlan{preferences: prefs, bootstrapCount: bootstrapCount}, nil
}

func promptInt(reader *bufio.Reader, out io.Writer, question string, defaultValue int) (int, error) {
	suffix := fmt.Sprintf(" [%d] ", defaultValue)
	for {
		fmt.Fprint(out, question, suffix)
		line, err := reader.ReadString('\n')
		if err != nil && !errors.Is(err, io.EOF) {
			return 0, err
		}
		answer := strings.TrimSpace(line)
		if answer == "" {
			return defaultValue, nil
		}
		if n, parseErr := strconv.Atoi(answer); parseErr == nil && n >= 0 {
			return n, nil
		}
		fmt.Fprintln(out, "please enter a non-negative integer")
		if errors.Is(err, io.EOF) {
			return defaultValue, nil
		}
	}
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

func bootstrapInstallSessions(p paths, prefs preferences, bootstrapCount int, out io.Writer) error {
	quietFor, err := time.ParseDuration(prefs.QuietFor)
	if err != nil {
		return err
	}
	if bootstrapCount > 0 {
		fmt.Fprintf(out, "bootstrap: processing up to %d recent quiet session(s)\n", bootstrapCount)
		opts := extractOpts{
			product:           prefs.watchProduct(),
			recent:            bootstrapCount,
			onlyNew:           true,
			model:             prefs.ExtractionModel,
			maxTranscriptChar: prefs.MaxTranscriptChars,
			minUserTurns:      prefs.MinUserTurns,
			minUserChars:      prefs.MinUserChars,
			maxObservations:   prefs.MaxObservations,
			zoomContextChars:  prefs.ZoomContextChars,
			noSkip:            prefs.NoSkip,
		}
		if err := runExtractWithPaths(p, opts, quietFor); err != nil {
			return err
		}
	}
	count, err := markExistingQuietSessionsProcessed(p, prefs.watchProduct(), quietFor, time.Now())
	if err != nil {
		return err
	}
	fmt.Fprintf(out, "bootstrap: baselined %d existing quiet session(s); watcher will process future sessions\n", count)
	return nil
}

func markExistingQuietSessionsProcessed(p paths, target product, quietFor time.Duration, now time.Time) (int, error) {
	sessions, err := listSessions(p, target)
	if err != nil {
		return 0, err
	}
	sessions = quietSessions(sessions, quietFor, now)
	st := newStore(p)
	count := 0
	err = st.withLock(func() error {
		state, err := readState(p.stateFile)
		if err != nil {
			return err
		}
		for _, session := range sessions {
			if processedSession(state, session) {
				continue
			}
			state.markProcessed(sessionStateKey(session.product, session.sessionID))
			count++
		}
		if count == 0 {
			return nil
		}
		return writeState(p.stateFile, state)
	})
	return count, err
}

func configureLaunchdWatcher(prefs preferences) error {
	plistPath, err := launchdPlistPath()
	if err != nil {
		return err
	}
	executable, err := os.Executable()
	if err != nil {
		return fmt.Errorf("resolving distill executable: %w", err)
	}
	homebrew := isHomebrewDistillExecutable(executable)
	if !prefs.AutomaticWatch {
		if err := unloadLaunchdWatcher(plistPath); err != nil {
			return err
		}
		if homebrew && os.Getenv("DISTILL_TEST_LAUNCHD") != "1" {
			_ = brewServices("stop", "distill")
		}
		return nil
	}
	if homebrew {
		if err := unloadLaunchdWatcher(plistPath); err != nil {
			return err
		}
		if os.Getenv("DISTILL_TEST_LAUNCHD") == "1" {
			return nil
		}
		if err := brewServices("restart", "distill"); err != nil {
			return fmt.Errorf("restarting Homebrew service: %w", err)
		}
		return nil
	}
	if err := writeLaunchdPlist(plistPath, executable, prefs.watchProduct()); err != nil {
		return err
	}
	if os.Getenv("DISTILL_TEST_LAUNCHD") == "1" {
		return nil
	}
	_ = launchctl("unload", plistPath)
	if err := launchctl("load", plistPath); err != nil {
		return fmt.Errorf("loading launchd watcher: %w", err)
	}
	return nil
}

func isHomebrewDistillExecutable(executable string) bool {
	if os.Getenv("DISTILL_TEST_HOMEBREW_SERVICE") == "1" {
		return true
	}
	prefix, err := brewPrefix("distill")
	if err != nil || strings.TrimSpace(prefix) == "" {
		return false
	}
	if pathWithinDir(executable, prefix) {
		return true
	}
	realExecutable, err := filepath.EvalSymlinks(executable)
	if err != nil {
		return false
	}
	realPrefix, err := filepath.EvalSymlinks(prefix)
	if err != nil {
		return false
	}
	return pathWithinDir(realExecutable, realPrefix)
}

func unloadLaunchdWatcher(plistPath string) error {
	_ = launchctl("unload", plistPath)
	if err := os.Remove(plistPath); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return nil
}

func launchdPlistPath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, "Library", "LaunchAgents", launchdLabel+".plist"), nil
}

func writeLaunchdPlist(path, executable string, watchProduct product) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	logPath, err := launchdLogPath()
	if err != nil {
		return err
	}
	body := fmt.Sprintf(`<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
  <key>Label</key>
  <string>%s</string>
  <key>ProgramArguments</key>
  <array>
    <string>%s</string>
    <string>watch</string>
    <string>--product</string>
    <string>%s</string>
  </array>
  <key>RunAtLoad</key>
  <true/>
  <key>KeepAlive</key>
  <true/>
  <key>StandardOutPath</key>
  <string>%s</string>
  <key>StandardErrorPath</key>
  <string>%s</string>
</dict>
</plist>
`, launchdLabel, xmlEscape(executable), xmlEscape(string(watchProduct)), xmlEscape(logPath), xmlEscape(logPath))
	return os.WriteFile(path, []byte(body), 0o644)
}

func launchdLogPath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	dir := filepath.Join(home, ".distill")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	return filepath.Join(dir, "watch.log"), nil
}

func xmlEscape(s string) string {
	replacer := strings.NewReplacer(
		"&", "&amp;",
		"<", "&lt;",
		">", "&gt;",
		`"`, "&quot;",
		"'", "&apos;",
	)
	return replacer.Replace(s)
}

func printInstallSummary(out io.Writer, prefs preferences) {
	fmt.Fprintln(out, "saved distill preferences")
	fmt.Fprintf(out, "  watch: %s\n", prefs.watchProduct())
	if prefs.PromotionMode == promotionModeUnified {
		fmt.Fprintf(out, "  user instructions: %s\n", prefs.AlwaysOnPath)
	} else {
		fmt.Fprintf(out, "  claude user instructions: %s\n", prefs.ClaudeMDPath)
		fmt.Fprintf(out, "  codex user instructions:  %s\n", prefs.CodexAgentsPath)
	}
	fmt.Fprintf(out, "  user skills: %s\n", prefs.SkillsDir)
	if prefs.AutomaticWatch {
		if runningFromHomebrew() {
			fmt.Fprintln(out, "  automatic watch: Homebrew service")
			fmt.Fprintln(out, "  service command: brew services restart distill")
		} else if plistPath, err := launchdPlistPath(); err == nil {
			fmt.Fprintf(out, "  automatic watch: %s\n", plistPath)
		} else {
			fmt.Fprintln(out, "  automatic watch: enabled")
		}
	} else {
		fmt.Fprintln(out, "  automatic watch: disabled")
	}
	fmt.Fprintln(out)
	if !prefs.AutomaticWatch {
		fmt.Fprintf(out, "run manually: distill watch --product %s\n", prefs.watchProduct())
	}
	fmt.Fprintln(out, "agent guide: distill agents")
	fmt.Fprintln(out, "open the web UI: distill serve")
	fmt.Fprintln(out, "then visit: http://127.0.0.1:7373")
}

func runningFromHomebrew() bool {
	executable, err := os.Executable()
	if err != nil {
		return false
	}
	return isHomebrewDistillExecutable(executable)
}
