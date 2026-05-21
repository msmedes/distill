# Architecture Decision Records

Load-bearing decisions for distill. Each record is short — context, decision, consequences.

- [0001 — Go for distribution-grade binary](./0001-go-for-distribution.md)
- [0002 — Reuse `claude -p` for LLM calls, not the Anthropic API](./0002-claude-p-for-llm-calls.md)
- [0003 — Two-cadence pipeline: cheap extract per session, expensive synthesize on demand](./0003-two-cadence-pipeline.md)
- [0004 — Observations are primary; skills and CLAUDE.md are derived views](./0004-observations-are-primary.md)
- [0005 — Evidence dedup by quote text, not by session id](./0005-evidence-dedup-by-quote.md)
- [0006 — User-confirmed promotion, not autonomous skill creation](./0006-user-confirmed-promotion.md)
