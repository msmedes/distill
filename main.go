package main

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

Examples:
  distill extract                    # process the most recent session
  distill extract --recent 5         # process the 5 most recent sessions
  distill extract --new              # process every unprocessed session
  distill extract --session 085573a7 # process one session by id (prefix ok)
  distill extract --dry-run          # show what would be extracted, don't write
  distill list                       # show accumulated observations
  distill serve                      # browse observations in your browser
`

func main() {
	if len(os.Args) < 2 {
		fmt.Print(usage)
		return
	}

	switch os.Args[1] {
	case "extract":
		if err := runExtract(os.Args[2:]); err != nil {
			fmt.Fprintf(os.Stderr, "extract: %v\n", err)
			os.Exit(1)
		}
	case "list":
		if err := runList(os.Args[2:]); err != nil {
			fmt.Fprintf(os.Stderr, "list: %v\n", err)
			os.Exit(1)
		}
	case "serve":
		if err := runServe(os.Args[2:]); err != nil {
			fmt.Fprintf(os.Stderr, "serve: %v\n", err)
			os.Exit(1)
		}
	case "compact":
		if err := runCompact(os.Args[2:]); err != nil {
			fmt.Fprintf(os.Stderr, "compact: %v\n", err)
			os.Exit(1)
		}
	case "synthesize":
		if err := runSynthesize(os.Args[2:]); err != nil {
			fmt.Fprintf(os.Stderr, "synthesize: %v\n", err)
			os.Exit(1)
		}
	case "help", "--help", "-h":
		fmt.Print(usage)
	default:
		fmt.Fprintf(os.Stderr, "unknown command: %s\n\n", os.Args[1])
		fmt.Fprint(os.Stderr, usage)
		os.Exit(1)
	}
}
