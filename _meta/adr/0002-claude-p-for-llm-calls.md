# 0002. Reuse `claude -p` for LLM calls

Status: Accepted; superseded in part by [ADR 0008](./0008-selectable-extraction-backend.md) for configurable backend selection
Date: 2026-05-21

## Context

distill makes LLM calls to extract observations from transcripts, synthesize proposals, and generate skill content. The obvious path is to depend on the Anthropic SDK and require the user to set `ANTHROPIC_API_KEY`. This adds credential-setup friction — most Claude Code users are on a Max subscription, not API; their Max billing doesn't help an API-based tool. Forcing them to provision and pay separately for API access would lose half the audience before the tool became useful.

## Decision

distill shells out to the local `claude` CLI in print mode: `claude -p --model <id> --output-format text`. It pipes the prompt to stdin and reads the response from stdout. The Go process never touches Anthropic credentials directly.

Observation extraction and generation can now use either `claude -p` or `codex exec`; see ADR 0008. This ADR still explains why local CLI backends are preferred over direct API credentials.

## Consequences

- **+** Zero credential setup. If the user has Claude Code installed and signed in, distill works.
- **+** Costs roll up under the user's existing Claude Code plan.
- **+** Model upgrades are decoupled from any SDK upgrade path — when Anthropic ships a new Sonnet, change the model id string.
- **−** Inherits `claude -p`'s startup overhead (~1s) per call. Batch operations feel slower than a direct API call would.
- **−** Inherits auto-memory injection: `claude -p` loads the user's memory system into context. Early extractor runs hallucinated memory file names as `obs_id` reinforcements because of this. The prompt now defends against it but the leak exists.
- **−** No token streaming. The output is fetched all-at-once. Acceptable for the response sizes distill works with (mostly small JSON).
