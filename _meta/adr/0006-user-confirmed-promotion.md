# 0006. User-confirmed promotion

Status: Accepted
Date: 2026-05-21

## Context

We considered a fully-autonomous loop: LLM extracts → LLM synthesizes → LLM writes skills/CLAUDE.md entries → distill quietly updates the user's `~/.claude/` state in the background. The user would open distill periodically to see what had been done.

Counterargument: a single bad skill pollutes *every subsequent agent session* until it's manually removed. Similarly, a bad CLAUDE.md entry shapes every agent response, everywhere. The blast radius of a wrong autonomous write is large; the cost of asking the user to click is small.

## Decision

Promotion to a skill or always-on instruction file is **always** user-confirmed. The LLM proposes via Synthesize; the user accepts or dismisses via the UI. Accepting a promotion opens a preview diff first, and the user must explicitly commit that preview before distill writes. Ignored observations stay in the store but disappear from the default view; the user can unignore them later. Distill never autonomously writes to agent artifacts.

## Consequences

- **+** Bad proposals are recoverable at zero cost — click *dismiss*.
- **+** The signal-to-noise ratio of CLAUDE.md and the skill set is preserved, which is the load-bearing property that makes Claude Code useful at all.
- **+** The user remains the curator of their own taste, which is the whole point of the project.
- **−** distill requires periodic user attention; nothing happens autonomously.
- **−** If the user stops opening distill, observations accumulate without being acted on. This is a feature, not a bug — but it does mean the system has zero value to a user who never engages with it.
