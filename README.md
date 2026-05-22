<img width="968" height="593" alt="image" src="https://github.com/user-attachments/assets/3ef40a75-2ebd-41a8-b677-a783e47ea211" />

# distill

Build a model of yourself — your preferences, workflows, friction points — from your Claude Code and Codex session history. Curate it through a local web UI. Promote the signal into skills or always-on instruction entries.

distill never autonomously writes to your agent artifacts. The LLM extracts and proposes; you accept or dismiss. Promotion destinations are configurable in the web UI.

For the conceptual model and vocabulary, see [`CONTEXT.md`](./CONTEXT.md).
For the load-bearing design decisions, see [`_meta/adr/`](./_meta/adr).

## Install

distill calls the `claude` CLI for all LLM work, so it rides your existing Claude Code login — no Anthropic API key needed. See [ADR 0002](./_meta/adr/0002-claude-p-for-llm-calls.md). Install Claude Code first: <https://docs.claude.com/en/docs/claude-code>.

### Homebrew (macOS / Linux)

```sh
brew install msmedes/distill/distill
```

### From source

Requires Go 1.24+.

```sh
git clone https://github.com/msmedes/distill.git
cd distill
go build -o distill ./cmd/distill
```

A single static binary lands at `./distill`. No runtime dependencies beyond `claude`.

## Quickstart

```sh
# 0. Configure watched products, promotion destinations, and automatic watching
./distill install

# 1. Walk recent Claude Code and Codex sessions, emit observations
./distill extract --recent 20

# 2. (Later) catch up on everything new since last time
./distill extract --new

# 3. Ask the model to propose which observations should become skills / always-on entries
./distill synthesize

# 4. Open the curation UI
./distill serve
# → http://127.0.0.1:7373
```

In the UI, each observation has actions hidden behind a pencil toggle — **ignore**, **→ always-on**, **→ skill**, plus a free-text note input. Proposals appear as accent-colored banners with **accept** / **dismiss**. The `propose promotions` button at the right of the filter bar re-runs synthesis on demand.

## Commands

| Command | What it does |
| --- | --- |
| `distill extract`    | Process Claude Code and/or Codex sessions, emit observation deltas. |
| `distill watch`      | Poll for quiet, unprocessed sessions and run extraction continuously. |
| `distill synthesize` | Read all active observations, attach LLM-proposed promotions. |
| `distill serve`      | Run the curation web UI (default `http://127.0.0.1:7373`). |
| `distill list`       | Plain-text dump of accumulated observations. |
| `distill compact`    | Dedup evidence entries (e.g. after `/resume` duplicated turns). |
| `distill install`    | Interactive setup for watched products, promotion destinations, and automatic watching. |
| `distill help`       | Usage. |

### `extract` flags

| Flag | Default | Description |
| --- | --- | --- |
| `--product <claude\|codex\|all>` | configured by `install`, default `all` | Which product's sessions to process. |
| `--session <id>`         | —      | Process one specific session (prefix matches). |
| `--recent <n>`           | `1`    | Process the N most recent sessions across all projects. |
| `--new`                  | off    | Process every unprocessed session (combine with `--recent` to cap). |
| `--dry-run`              | off    | Show what would be extracted without writing. |
| `--model <haiku\|sonnet>` | `haiku` | Model for the per-session pass. |
| `--max-transcript-chars` | `60000` | Truncate rendered user-message excerpts longer than this. |
| `--min-user-turns <n>` | `2` | Skip sessions with fewer user turns unless correction/preference language appears. |
| `--min-user-chars <n>` | `200` | Skip sessions with fewer user-message chars unless correction/preference language appears. |
| `--max-observations <n>` | `80` | Include at most this many relevant existing observations in the extractor prompt. |
| `--zoom-context-chars <n>` | `2500` | Include up to this many chars from the preceding assistant turn around high-signal user turns. |
| `--no-skip` | off | Disable cheap local skipping for short low-signal sessions. |

### `watch` flags

| Flag | Default | Description |
| --- | --- | --- |
| `--product <claude\|codex\|all>` | `all` | Which product's sessions to process. |
| `--interval <duration>` | `1h` | Time between scans. Go duration syntax, e.g. `30m`, `2h`. |
| `--quiet-for <duration>` | `10m` | Ignore transcripts modified more recently than this. |
| `--model <haiku\|sonnet>` | `haiku` | Model for the per-session pass. |
| `--no-skip` | off | Disable cheap local skipping for short low-signal sessions. |

### `serve` flags

| Flag | Default | Description |
| --- | --- | --- |
| `--port <n>`    | `7373`      | Port to listen on. |
| `--host <addr>` | `127.0.0.1` | Bind address. Keep on loopback unless you know what you're doing. |

## State

Persistent data is local-only, at `~/.distill/`:

```
~/.distill/
├── observations.jsonl   # one observation per line
├── state.json           # which sessions have been processed
├── preferences.json     # watched products and promotion destinations
└── candidates/          # reserved for future use
```

Source sessions are read from:

- Claude Code: `~/.claude/projects/<encoded-cwd>/<session-id>.jsonl`
- Codex: `~/.codex/sessions/<yyyy>/<mm>/<dd>/rollout-*.jsonl`

distill never modifies source sessions.

Promoted observations default to:

- **Skills:** `~/.agents/skills/<name>/SKILL.md` — LLM-generated from claim + evidence + notes.
- **Always-on instructions:** appended to `~/.agents/AGENTS.md` under an `## Auto-extracted from distill` section. Raw append, no LLM rewrite — edit by hand to reshape.

`distill install` asks whether to watch Claude Code, Codex, or both. It also inspects `~/.agents/AGENTS.md`, `~/.claude/CLAUDE.md`, and `~/.codex/AGENTS.md`, then asks whether always-on promotions should go to one shared `~/.agents/AGENTS.md` file or stay product-specific (`~/.claude/CLAUDE.md` for Claude, `~/.codex/AGENTS.md` for Codex). Finally, it asks whether to start `distill watch` automatically at login. It creates destination directories and writes the choice to `~/.distill/preferences.json`; it does not move or symlink existing instruction files.

## Architecture at a glance

Two passes, two cadences (see [ADR 0003](./_meta/adr/0003-two-cadence-pipeline.md)):

```
                          ┌──────────────────────────┐
                          │ Claude/Codex session logs │
                          └────────────┬─────────────┘
                                       │
                                       ▼
   distill extract  ──── Haiku ───►  observation deltas  ──►  ~/.distill/observations.jsonl
   (per session, cheap)                  (new / reinforce
                                          / contradict)
                                       │
                                       ▼
   distill synthesize  ─── Sonnet ──►  attach proposals  ──►  same store
   (across the corpus,                   to active obs
    on demand)
                                       │
                                       ▼
                            distill serve  ───►  user accepts / dismisses
                                       │
                                       ▼
                          ~/.agents/skills/<name>/SKILL.md
                          configured always-on file (append)
```

## Status

Active. v1 ships per-session extraction, on-demand synthesis, full review/curation UI, and single-binary distribution.

Known follow-ups:
- **No observation decay.** The store accumulates indefinitely. Old observations stay weighted as if still relevant.
- **No automatic dedup of near-duplicate observations.** Extract reinforces existing ones if the model recognizes them, but it sometimes creates new observations that are obvious duplicates. `compact` only handles evidence-level dedup, not observation-level.
- **No retrieval layer.** Observations could feed in-context retrieval at query time (the "show me how Mike handled situations like this" layer). Not built yet.

## Auto-extract by watching transcripts

`distill watch` polls Claude Code and Codex transcript directories and processes sessions that have been quiet long enough. This keeps automatic ingestion consistent across both products instead of relying on product-specific hook lifecycle events.

The extractor does a cheap local pass before calling Claude. Short sessions with no correction/preference markers are marked processed without an LLM call, and non-skipped sessions send user-authored turns plus bounded local assistant context around high-signal corrections. Existing observations are reduced to a relevant capped subset.

```sh
./distill install                       # choose watched products and destinations
./distill watch                         # scan every hour, require 10m quiet
./distill watch --interval 2h           # scan every two hours
./distill watch --product codex         # watch only Codex transcripts
./distill watch --quiet-for 30m         # wait longer before processing active files
```

If you choose automatic watching during `install`, distill writes and loads a user launchd agent at `~/Library/LaunchAgents/com.msmedes.distill.watch.plist`. Logs go to `~/.distill/watch.log`. If you choose manual watching, no launchd agent is installed and the summary prints the exact `distill watch --product ...` command to run.

## Smoke tests

```sh
scripts/smoke-install.sh
```

The script runs `distill install` against temporary `HOME` directories, feeds representative interactive answers, and asserts the resulting `~/.distill/preferences.json` and launchd plist behavior. It does not start `distill watch` and does not touch your real home directory.

## License

MIT — see [`LICENSE`](./LICENSE).
