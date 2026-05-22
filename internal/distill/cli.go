package distill

import (
	"embed"
	"fmt"
	"os"
)

//go:embed prompts/*.md
var promptsFS embed.FS

const usage = `distill — extract observations about you from Claude Code sessions

Usage:
  distill extract    [--session <id>] [--recent <n>] [--new] [--dry-run] [--model haiku|sonnet]
  distill synthesize [--model haiku|sonnet|opus]
  distill list
  distill serve      [--port <n>] [--host <addr>]
  distill compact
  distill install    [--uninstall]   # install/remove the Claude Code SessionEnd hook
  distill hook                       # internal: invoked by the SessionEnd hook

Examples:
  distill extract                    # process the most recent session
  distill extract --recent 5         # process the 5 most recent sessions
  distill extract --new              # process every unprocessed session
  distill extract --session 085573a7 # process one session by id (prefix ok)
  distill extract --dry-run          # show what would be extracted, don't write
  distill list                       # show accumulated observations
  distill serve                      # browse observations in your browser
  distill install                    # auto-extract whenever a Claude Code session ends
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
	case "hook":
		if err := runHook(args[2:]); err != nil {
			fmt.Fprintf(os.Stderr, "hook: %v\n", err)
			return 1
		}
	case "help", "--help", "-h":
		fmt.Print(usage)
	default:
		fmt.Fprintf(os.Stderr, "unknown command: %s\n\n", args[1])
		fmt.Fprint(os.Stderr, usage)
		return 1
	}
	return 0
}
