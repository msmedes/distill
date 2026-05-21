# 0004. Observations are primary; skills and CLAUDE.md are derived

Status: Accepted
Date: 2026-05-21

## Context

The original framing was "extract skills from sessions." That collapses several distinct things — the *finding* (the user has preference X), the *artifact* (a SKILL.md file with frontmatter and a description), and the *destination* (either a skill, a CLAUDE.md entry, or nothing). Treating "skill" as the primary unit makes most session content unrepresentable: most sessions don't produce a skill, but they do produce *signal* worth tracking.

We also identified a longer-term ambition: the same source data could feed multiple "taste-projection" layers (skills, CLAUDE.md, retrieval over past trajectories). Tying the data layer to the skill format would foreclose the other consumers.

## Decision

The durable atom is an **Observation** — a small structured record (`claim`, `type`, `evidence[]`, `status`, `notes[]`, `proposals[]`, `contradicted_by[]`). Skills and CLAUDE.md entries are downstream views, generated on promotion.

An observation can be promoted multiple ways (to a skill, to CLAUDE.md, to neither). The observation persists either way.

## Consequences

- **+** Most observations never become anything. That's fine. The cost of an observation is low; the cost of a skill polluting agent context is high.
- **+** Future consumers — in-context retrieval, auto-updated CLAUDE.md baselines — can be added without restructuring the pipeline.
- **+** The "duplicate detection / merge" problem becomes tractable: dedup happens at the observation layer, not in the downstream artifacts.
- **−** Slightly more storage. Most observations sit forever as low-count noise. **Decay is not implemented yet**; the store will accumulate indefinitely. This is a known follow-up.
