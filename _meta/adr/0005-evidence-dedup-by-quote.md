# 0005. Evidence dedup by quote text

Status: Accepted
Date: 2026-05-21

## Context

Claude Code creates a new session JSONL file on every `/resume` or branch, copying the prior turns verbatim into the new file (same UUIDs, same content). Naive evidence collection treats every appearance of the same turn as independent reinforcement.

We observed this in practice: `obs_0001` spiked to `count=9` because the same turn appeared in seven resumption-chunks of one conversation. The real number of independent reinforcements was three. Inflated counts mislead synthesize, which uses count as a judgment input.

## Decision

Within an observation, evidence entries are deduplicated by their `quote` text. The first occurrence wins; subsequent identical quotes are dropped. `evidence_count` is maintained as `len(evidence)`, always.

Dedup runs at write time (every `applyDeltas` call) and as a separate `distill compact` command for one-shot cleanup of historical inflation.

## Consequences

- **+** Counts reflect actual independent reinforcement, which is what synthesize needs.
- **+** Compact is idempotent — running it twice is a no-op after the first.
- **−** We lose the "this quote surfaced in N session files" metric, but it was misleading anyway.
- **−** Evidence entries without a `quote` (rare; only present if the extractor's prompt was modified to skip quotes) bypass dedup. Acceptable for v1.
- **−** Alternative: dedup by turn UUID would be more semantically correct but requires plumbing UUIDs through the extractor output. Quote-based dedup is good enough for now.
