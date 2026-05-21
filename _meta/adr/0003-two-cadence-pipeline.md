# 0003. Two-cadence pipeline

Status: Accepted
Date: 2026-05-21

## Context

A single-pass design would have one LLM call do everything — read each session, evaluate it against the corpus, propose skills, write them. That fails two ways: a powerful model running on every session is expensive (most sessions produce nothing), and a cheap model doing corpus-wide judgment misses patterns that only emerge across sessions.

We also need to control *when* expensive work happens. Per-session work should be cheap enough to forget about; corpus-wide work should be opt-in.

## Decision

Two passes with different cadences and different models:

- **Extract** runs once per session. Uses Haiku. Reads one transcript and emits **observation deltas** (new candidates, reinforcements, contradictions). Bias: aggressively conservative — most sessions emit zero observations.
- **Synthesize** runs on demand, triggered by the user clicking *propose promotions*. Uses Sonnet. Reads all `active` observations and attaches **Proposals** recommending skill or CLAUDE.md promotion to a small subset.

The two passes never overlap in scope. Extract never proposes; Synthesize never reads transcripts.

## Consequences

- **+** Per-session cost stays bounded. The user can extract 50+ sessions without thinking about it.
- **+** Synthesis is rare enough that Sonnet is acceptable for the better judgment.
- **+** The two prompts can evolve independently. Prompt changes in one don't invalidate state produced by the other.
- **−** Two prompts to maintain, two failure modes to debug.
- **−** Synthesized proposals can go stale if many new observations land after the last synthesize run. The UI offers no warning about staleness yet — open question.
