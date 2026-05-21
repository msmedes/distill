<img width="968" height="593" alt="image" src="https://github.com/user-attachments/assets/3ef40a75-2ebd-41a8-b677-a783e47ea211" />

# distill

Build a model of yourself — your preferences, workflows, friction points — from your Claude Code session history. Curate it through a local web UI. Promote the signal into Claude Code skills or CLAUDE.md entries.

distill never autonomously writes to your `~/.claude/` artifacts. The LLM extracts and proposes; you accept or dismiss.

For the conceptual model and vocabulary, see [`CONTEXT.md`](./CONTEXT.md).
For the load-bearing design decisions, see [`_meta/adr/`](./_meta/adr).

## Install

Requires Go 1.22+ and an authenticated `claude` CLI on `PATH`. distill calls `claude -p` for all LLM work, so it rides your existing Claude Code login — no Anthropic API key needed. See [ADR 0002](./_meta/adr/0002-claude-p-for-llm-calls.md).

```sh
git clone https://github.com/msmedes/distill.git
cd distill
go build -o distill .
```

A single static binary lands at `./distill`. No runtime dependencies.

## Quickstart

```sh
# 0. (One-time) install the SessionEnd hook so future sessions extract automatically
./distill install

# 1. Walk recent sessions, emit observations
./distill extract --recent 20

# 2. (Later) catch up on everything new since last time
./distill extract --new

# 3. Ask the model to propose which observations should become skills / CLAUDE.md entries
./distill synthesize

# 4. Open the curation UI
./distill serve
# → http://127.0.0.1:7373
```

In the UI, each observation has actions hidden behind a pencil toggle — **ignore**, **→ CLAUDE.md**, **→ skill**, plus a free-text note input. Proposals appear as accent-colored banners with **accept** / **dismiss**. The `propose promotions` button at the right of the filter bar re-runs synthesis on demand.

## Commands

| Command | What it does |
| --- | --- |
| `distill extract`    | Process Claude Code sessions, emit observation deltas. |
| `distill synthesize` | Read all active observations, attach LLM-proposed promotions. |
| `distill serve`      | Run the curation web UI (default `http://127.0.0.1:7373`). |
| `distill list`       | Plain-text dump of accumulated observations. |
| `distill compact`    | Dedup evidence entries (e.g. after `/resume` duplicated turns). |
| `distill install`    | Install the Claude Code `SessionEnd` hook so extract runs automatically. |
| `distill hook`       | Internal — invoked by the installed hook on session end. |
| `distill help`       | Usage. |

### `extract` flags

| Flag | Default | Description |
| --- | --- | --- |
| `--session <id>`         | —      | Process one specific session (prefix matches). |
| `--recent <n>`           | `1`    | Process the N most recent sessions across all projects. |
| `--new`                  | off    | Process every unprocessed session (combine with `--recent` to cap). |
| `--dry-run`              | off    | Show what would be extracted without writing. |
| `--model <haiku\|sonnet>` | `haiku` | Model for the per-session pass. |
| `--max-transcript-chars` | `200000` | Truncate transcripts longer than this. |

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
└── candidates/          # reserved for future use
```

Source sessions are read from `~/.claude/projects/<encoded-cwd>/<session-id>.jsonl`. distill never modifies them.

Promoted observations are written to:

- **Skills:** `~/.claude/skills/<name>/SKILL.md` — LLM-generated from claim + evidence + notes.
- **CLAUDE.md:** appended to `~/.claude/CLAUDE.md` (follows symlinks) under an `## Auto-extracted from distill` section. Raw append, no LLM rewrite — edit by hand to reshape.

## Architecture at a glance

Two passes, two cadences (see [ADR 0003](./_meta/adr/0003-two-cadence-pipeline.md)):

```
                          ┌──────────────────────────┐
                          │ ~/.claude/projects/*.jsonl│  (Claude Code session logs)
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
                          ~/.claude/skills/<name>/SKILL.md
                          ~/.claude/CLAUDE.md (append)
```

## Status

Active. v1 ships per-session extraction, on-demand synthesis, full review/curation UI, and single-binary distribution.

Known follow-ups:
- **No observation decay.** The store accumulates indefinitely. Old observations stay weighted as if still relevant.
- **No automatic dedup of near-duplicate observations.** Extract reinforces existing ones if the model recognizes them, but it sometimes creates new observations that are obvious duplicates. `compact` only handles evidence-level dedup, not observation-level.
- **No retrieval layer.** Observations could feed in-context retrieval at query time (the "show me how Mike handled situations like this" layer). Not built yet.

## Auto-extract on session end

`distill install` writes a `SessionEnd` hook into `~/.claude/settings.json`. When a Claude Code session ends, the hook receives the session id on stdin and spawns `distill extract --session <id>` detached — Claude Code returns immediately, the extract runs in the background. Output goes to `~/.distill/hook.log`.

```sh
./distill install              # install the hook
./distill install --uninstall  # remove it
tail -f ~/.distill/hook.log    # watch extraction as sessions end
```

Re-running `install` is idempotent: if the hook is already wired to the same binary path, nothing changes; if the path moved, it rewrites in place. Other hooks in `settings.json` are preserved.

### Developing distill with the hook live

The hook stores the **absolute path** of whichever binary you ran `install` from — typically your in-repo `./distill`. So the dev loop is:

1. Run `./distill install` once from your checkout.
2. Iterate on source. Each `go build -o distill .` overwrites the binary at the same path.
3. The next session-end fires the hook → uses your freshly built code.

You only need to reinstall if you move the binary or rename the `hook` subcommand. `air` (see `.air.toml`) gives you save-to-rebuild for a tight loop.

## License

MIT — see [`LICENSE`](./LICENSE).
