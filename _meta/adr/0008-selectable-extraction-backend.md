# 0008. Selectable model backends

Status: Accepted
Date: 2026-05-28

## Context

distill originally sent observation extraction through `claude -p`, and synthesis/promotion generation also hardcoded Claude models. That kept credentials simple, but it made model work dependent on one agent runtime even when the source transcripts already include both Claude Code and Codex sessions. Users may want Codex to do either extraction or generation work while still reading either product's transcripts.

This is separate from source selection. Watching Codex sessions does not imply using Codex as the extractor, and watching Claude sessions does not imply using Claude as the extractor.

## Decision

distill stores two model backend settings in `~/.distill/preferences.json`: `extraction_backend` for session-to-observation extraction, and `generation_backend` for proposal synthesis plus promotion preview generation. Each backend can be `claude` for `claude -p` or `codex` for `codex exec`.

`distill install` asks for the extraction backend during setup, and the settings page can change extraction and generation later. Extraction uses the configured extraction backend for every selected source product. Synthesis and promotion previews use the configured generation backend.

The default remains `claude` to preserve existing installs and non-interactive setup behavior. The settings UI presents semantic model choices, then maps them to backend-specific model names: fastest / balanced / smartest map to `haiku` / `sonnet` / `opus` for Claude and `gpt-5.4-mini` / `gpt-5.4` / `gpt-5.5` for Codex.

## Consequences

- **+** Users can compare `claude -p` and `codex exec` for observation and generated-artifact quality without changing transcript sources.
- **+** Background `watch` and manual `extract` share the same configured backend.
- **+** Existing preferences remain valid because missing backend fields normalize to `claude`.
- **−** Codex model names drift with the Codex CLI; the settings constants need periodic review.
- **−** This is still a local-CLI backend abstraction, not a direct provider/API abstraction.
