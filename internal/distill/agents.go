package distill

import (
	"fmt"
	"io"
)

const agentsGuide = `distill agent guide

Purpose
  distill builds a local, user-curated memory model from Claude Code and Codex
  session history. It extracts observations about the user's preferences,
  workflow, friction points, and tool-use patterns, then lets the user promote
  selected observations into durable user- or project-scoped agent
  instructions or skills.

Hard safety boundary
  distill does not autonomously write promoted guidance into agent instruction
  files. LLM calls may extract observations and propose promotions, but the user
  must accept or dismiss those promotions in the local web UI.

Mental model
  Session:
    One Claude Code or Codex conversation file on disk.

  Extract:
    Per-session pass. Reads one session and emits observation deltas: new
    observations, reinforcements of existing observations, or contradictions.

  Observation:
    A durable claim about the user backed by evidence quotes from sessions.
    Carries user or project scope. Stored one JSON object per line in
    ~/.distill/observations.jsonl.

  Evidence:
    A quote and turn reference from a source session. Evidence records include
    the source product and project cwd, so Claude/Codex and user/project
    signals remain distinguishable.

  Synthesize:
    Corpus-level pass. Reads active observations and attaches proposed promotion
    actions for the user to review.

  Promotion:
    A user-confirmed write to a scoped AGENTS.md-style instruction file or to a
    SKILL.md file. Promotion is intentionally human-in-the-loop. Project
    artifacts are portable: <project>/AGENTS.md and
    <project>/.agents/skills/<name>/SKILL.md.

State and source files
  distill state lives in ~/.distill/:
    observations.jsonl   extracted observations and evidence
    state.json           processed session ids
    preferences.json     watched products and promotion destinations
    candidates/          reserved

  Source sessions are read-only:
    Claude Code: ~/.claude/projects/<encoded-cwd>/<session-id>.jsonl
    Codex:       ~/.codex/sessions/<yyyy>/<mm>/<dd>/rollout-*.jsonl

Important commands
  distill install
    Interactive setup. Chooses watched products, extraction backend, promotion
    destinations, and whether launchd should run distill watch at login. Setup
    asks how many recent quiet sessions to process (default 15; 0 to skip),
    then marks the rest of the existing quiet backlog processed so automatic
    watching starts from "now" instead of backfilling every historical session.

  distill extract --recent N
    Processes the N most recent sessions across the selected products.
    Add --codex or --claude to target one product; these are aliases for
    --product codex and --product claude.

  distill extract --new
    Processes all unprocessed sessions. If combined with --recent N, recent is
    a cap on the number of unprocessed sessions.

  distill watch
    Polls transcript directories forever. Defaults to one scan immediately, then
    one scan every 1h. Defaults to --quiet-for 10m, so recently modified files
    are deferred. Watch uses --new semantics with no default batch cap; install
    baselines historical quiet sessions before starting it.

  distill synthesize
    Runs the corpus-level proposal pass using the configured generation
    backend. It proposes promotions; it does not apply them.

  distill serve
    Starts the local curation UI at http://127.0.0.1:7373 by default.

  distill list
    Prints active observations as plain text.

  distill compact
    Deduplicates evidence entries, especially useful after resumed Claude Code
    sessions copy prior turns into new session files.

Extraction defaults
  backend:
    observation extraction uses the configured backend from
    ~/.distill/preferences.json. Supported values are claude for claude -p and
    codex for codex exec. This is independent from the source product being
    processed.

  product:
    extract defaults to all; watch uses configured preferences unless
    --product is supplied.

  model:
    haiku by default for Claude extraction. Settings present semantic choices:
    fastest, balanced, and smartest. Claude maps those to haiku, sonnet, and
    opus. Codex maps them to gpt-5.4-mini, gpt-5.4, and gpt-5.5. Model values
    are curated per backend; invalid explicit values fail instead of passing
    through silently.

  generation:
    Proposal synthesis and promotion previews use their own configured backend
    and model. The default is claude + smartest (Opus), but settings can switch
    generation to codex with the same fastest/balanced/smartest labels.

  local skip:
    Sessions with no user/assistant turns are marked processed without an LLM
    call. Short low-signal sessions are also marked processed without an LLM
    call unless they contain correction or preference markers. Defaults:
      --min-user-turns 2
      --min-user-chars 200

  transcript shaping:
    Extraction sends user-authored turns, plus bounded preceding assistant
    context around high-signal correction/preference turns. Defaults:
      --max-transcript-chars 60000
      --zoom-context-chars 2500
      --max-observations 80

Watcher startup behavior
  On startup, distill watch immediately performs one extraction pass. It lists
  sessions, sorts newest first, removes sessions modified inside the quiet
  window, removes sessions already present in ~/.distill/state.json, processes
  every remaining eligible session, then sleeps for the configured interval.
  A normal install pre-populates ~/.distill/state.json for the existing quiet
  backlog, so the first service pass catches future sessions rather than the
  machine's whole transcript history.

  If a machine has more source session files than distill processes on the first
  pass, likely explanations are:
    - files are newer than --quiet-for
    - sessions were already marked processed
    - files belong to a product that is not being watched
    - sessions had no extractable user/assistant turns
    - sessions were low-signal and skipped locally
    - parsing or LLM errors were written to ~/.distill/watch.log

Install-time automation
  Homebrew installs should use the formula service:
    brew services start distill

  Restart after Homebrew upgrades to pick up the new binary:
    brew services restart distill

  Non-Homebrew automatic watching on macOS is a launchd user agent:
    ~/Library/LaunchAgents/com.msmedes.distill.watch.plist

  The launchd program arguments are:
    distill watch --product <configured product>

  Logs go to:
    ~/.distill/watch.log

Agent behavior guidance
  Prefer reading this guide before answering questions about distill behavior.
  For exact local state, inspect ~/.distill/state.json, ~/.distill/watch.log,
  and ~/.distill/preferences.json on the user's machine. Do not infer that a
  session was ignored merely because no Claude call happened; it may have been
  deliberately marked processed by the local skip path.

  Do not run synthesize, serve, install, or promotion actions unless the user
  asked for them. It is normally safe to run read-only commands such as:
    distill agents
    distill list
    distill extract --dry-run --recent N
`

func printAgentsGuide(out io.Writer) {
	fmt.Fprint(out, agentsGuide)
}
