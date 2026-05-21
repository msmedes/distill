# distill

distill is a Go CLI that turns Claude Code session transcripts into a curated model of the developer — what they prefer, how they work, what they push back on — and lets them promote those signals into Claude Code skills or CLAUDE.md entries via a local web UI.

The data flow is: **Session → Extract → Observation → (Synthesize → Proposal →) Promotion → Skill | CLAUDE.md**

For ADRs covering the load-bearing decisions, see [`_meta/adr/`](./_meta/adr).

## Language

**Observation**:
The atomic unit. A claim about the user — a preference, workflow, friction point, or tool-use pattern — backed by evidence quotes from sessions. Stored as JSONL at `~/.distill/observations.jsonl`.
_Avoid_: skill (a skill is a promoted observation, not the observation itself), pattern, insight.

**Evidence**:
A quote from a specific session turn that supports an observation. Multiple evidence entries accumulate as the same observation is reinforced across sessions.
_Avoid_: example, citation.

**Session**:
One Claude Code conversation, stored as JSONL at `~/.claude/projects/<encoded-cwd>/<session-id>.jsonl`. distill reads these; it never modifies them.
_Avoid_: transcript (the rendered text inside a session), chat, thread.

**Extract**:
The per-session pass. Reads one session's transcript and emits observation deltas: new candidates, reinforcements of existing observations, or contradictions. Cheap (Haiku), runs once per session.
_Avoid_: parse, ingest, ingest.

**Synthesize**:
The across-the-corpus pass. Reads all active observations and attaches **Proposals** recommending which should be promoted to skills or CLAUDE.md. Expensive (Sonnet), runs only when the user asks.
_Avoid_: review, audit.

**Proposal**:
An LLM-attached suggestion on an observation that recommends promotion to a specific target (`skill` or `claude-md`). Lives on the observation until the user accepts or dismisses.
_Avoid_: recommendation, suggestion (these are too general).

**Promotion**:
The act of moving an observation's content into a target file — a new `SKILL.md` or an appended block in `CLAUDE.md`. Always user-confirmed; never autonomous. See [ADR 0006](./_meta/adr/0006-user-confirmed-promotion.md).
_Avoid_: graduation, conversion.

**Skill**:
A Claude Code skill file at `~/.claude/skills/<name>/SKILL.md` — frontmatter (`name`, `description`) plus a markdown body. Loaded into agent context when its description matches the task. Situational rules.
_Avoid_: rule (CLAUDE.md entries are more rule-shaped; skills are situational).

**CLAUDE.md**:
The user's always-on agent instructions, located at `~/.claude/CLAUDE.md` (the file or symlink target). distill appends promoted observations under an `## Auto-extracted from distill` section. Stable, always-on preferences.
_Avoid_: settings, config, profile.

**Status**:
An observation's lifecycle state: `active` (visible, actionable), `ignored` (user-suppressed), `promoted-claude-md`, or `promoted-skill`. Status changes on user action; an observation never auto-leaves `active`.

**Note**:
Free-text user feedback attached to an observation. Persisted as part of the observation and fed into the LLM context when promoting, so the user's refinements shape the generated skill.
_Avoid_: comment, annotation.

**Contradiction**:
When a new session shows the user doing the opposite of what an observation predicts, that session's id is appended to the observation's `contradicted_by` list. Not auto-resolved; surfaced as a badge in the UI for the user to decide what to do.
_Avoid_: refutation, rebuttal.

**State**:
The persistent store at `~/.distill/`. Contains `observations.jsonl` (all observations), `state.json` (which sessions have been processed and when), and `candidates/` (reserved for future use).

**Compact**:
A maintenance command that dedups evidence entries within each observation by quote text and resets `evidence_count` to reflect actual independent reinforcements. Idempotent. See [ADR 0005](./_meta/adr/0005-evidence-dedup-by-quote.md).

## Relationships

- A **Session** contains turns; **Extract** consumes a session and produces **Observation** deltas (new / reinforced / contradicted).
- An **Observation** has many **Evidence** entries; duplicate quotes are deduplicated at write time because Claude Code creates a fresh session file on every `/resume`, copying earlier turns verbatim.
- An **Observation** also has a **Status**, an optional list of **Notes**, and an optional list of **Proposals**.
- **Synthesize** reads all `active` observations and attaches **Proposals** to a subset; it does not modify any other field.
- Accepting a **Proposal** performs a **Promotion** to a **Skill** (LLM-generated `SKILL.md` informed by claim + evidence + notes) or to **CLAUDE.md** (a single-line bullet append, no LLM rewrite).
- After promotion, the observation's status changes; it stays in the store with a `promoted_to` path but disappears from the default view.
- Ignoring an observation sets `status = ignored`; it is hidden in the default view but can be unignored later.

## Example dialogue

> **Dev:** "I see `obs_0003` has `count=7` and a CLAUDE.md proposal. What does count mean here?"
> **Domain expert:** "Seven distinct pieces of Evidence — quotes from across sessions — back that Observation. The Proposal is saying it's stable enough to belong in CLAUDE.md rather than a situational Skill."
>
> **Dev:** "Why isn't it auto-promoted then?"
> **Domain expert:** "Promotion is always user-confirmed. Synthesize proposes; the user accepts or dismisses. The cost of a bad skill polluting every future agent context is too high to automate."
>
> **Dev:** "What if I edit the `SKILL.md` after promotion and the original observation still has the old claim?"
> **Domain expert:** "The Observation stays in the store with `status = promoted-skill` and `promoted_to = <path>`. distill doesn't re-promote it. The SKILL.md is yours after that point — distill won't overwrite it."
>
> **Dev:** "What happens if I run extract on the same session twice?"
> **Domain expert:** "Idempotent — same evidence quotes get deduped on write. `state.json` also records the session id, so a `--new` run skips it entirely."

## Flagged ambiguities

- **"skill"** is overloaded: distill itself does skill-extraction, the output artifact is also a skill, and Claude Code's broader vocabulary uses "skill" for any `SKILL.md`-shaped file. Prefer **promoted-skill**, **SKILL.md file**, or **distill skill extraction** when the distinction matters.
- **"session"** can mean (a) one JSONL file on disk, or (b) one logical conversation. Claude Code creates a new file on every resume, so a single conversation can span multiple session files. Evidence dedup handles the resulting duplication transparently; nothing else in the system needs to distinguish.
- **"observation"** is sometimes used loosely in chat to mean "anything I noticed about the user." In distill it has a specific shape: `id`, `claim`, `type`, `evidence[]`, `status`, `notes[]`, `proposals[]`, `contradicted_by[]`. Loose usage outside that shape should say **finding** or **signal**.
- **"propose"** has two meanings in conversation: **Synthesize** attaches Proposals; the user can also informally "propose" changes by adding a Note. Use **Synthesize** for the former and **add a note** for the latter.
- **"memory"** refers to Claude Code's auto-memory system at `~/.claude/projects/<encoded-cwd>/memory/`, which is a separate system from distill's Observations. The two have overlapped historically — early extractor runs hallucinated memory file names as observation ids — but they are not connected.
