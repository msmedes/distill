package distill

import (
	"embed"
	"fmt"
	"os"
	"runtime/debug"
)

// Version is overridden at release time via -ldflags
// "-X github.com/msmedes/distill/internal/distill.Version=<tag>".
var Version = ""

func versionString() string {
	if Version != "" {
		return Version
	}
	if info, ok := debug.ReadBuildInfo(); ok && info.Main.Version != "" && info.Main.Version != "(devel)" {
		return info.Main.Version
	}
	return "dev"
}

//go:embed prompts/*.md
var promptsFS embed.FS

const usage = `distill — extract observations about you from Claude Code and Codex sessions

Usage:
  distill agents                                                       # operating guide for coding agents — start here
  distill extract    [--codex|--claude|--product claude|codex|all] [--session <id>] [--recent <n>] [--new] [--dry-run] [--model <model>]
  distill watch      [--product claude|codex|all] [--interval 1h] [--quiet-for 10m]
  distill synthesize [--backend claude|codex] [--model <model>]
  distill list
  distill serve      [--port <n>] [--host <addr>]
  distill compact
  distill install    [--yes]
  distill version

Examples:
  distill agents                     # operating guide for coding agents — start here
  distill extract                    # process the most recent session
  distill extract --codex            # process the most recent Codex session
  distill extract --product codex    # process the most recent Codex session
  distill extract --recent 5         # process the 5 most recent sessions
  distill extract --new              # process every unprocessed session
  distill extract --session 085573a7 # process one session by id (prefix ok)
  distill extract --dry-run          # show what would be extracted, don't write
  distill watch                      # poll for quiet unprocessed sessions
  distill install                    # configure watcher and promotion destinations
  distill list                       # show accumulated observations
  distill serve                      # browse observations in your browser
`

func Main(args []string) int {
	if len(args) < 2 {
		fmt.Print(usage)
		return 0
	}

	switch args[1] {
	case "extract":
		if err := runExtract(args[2:]); err != nil {
			fmt.Fprintf(os.Stderr, "extract: %v\n", err)
			return 1
		}
	case "watch":
		if err := runWatch(args[2:]); err != nil {
			fmt.Fprintf(os.Stderr, "watch: %v\n", err)
			return 1
		}
	case "list":
		if err := runList(args[2:]); err != nil {
			fmt.Fprintf(os.Stderr, "list: %v\n", err)
			return 1
		}
	case "serve":
		if err := runServe(args[2:]); err != nil {
			fmt.Fprintf(os.Stderr, "serve: %v\n", err)
			return 1
		}
	case "compact":
		if err := runCompact(args[2:]); err != nil {
			fmt.Fprintf(os.Stderr, "compact: %v\n", err)
			return 1
		}
	case "synthesize":
		if err := runSynthesize(args[2:]); err != nil {
			fmt.Fprintf(os.Stderr, "synthesize: %v\n", err)
			return 1
		}
	case "install":
		if err := runInstall(args[2:]); err != nil {
			fmt.Fprintf(os.Stderr, "install: %v\n", err)
			return 1
		}
	case "agents", "--agents":
		printAgentsGuide(os.Stdout)
	case "version", "--version", "-v":
		fmt.Println(versionString())
	case "help", "--help", "-h":
		fmt.Print(usage)
	default:
		fmt.Fprintf(os.Stderr, "unknown command: %s\n\n", args[1])
		fmt.Fprint(os.Stderr, usage)
		return 1
	}
	return 0
}
