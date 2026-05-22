# Propose promotions

You are reviewing a batch of observations about a developer. For each one, decide whether it should be:

- **Promoted to a skill** — the observation describes a recurring, actionable pattern that would help future agent sessions if loaded as context. Skills should be specific enough to differentiate this user from a generic user, broad enough that the situation will recur.
- **Promoted to always-on instructions** — the observation describes a stated, always-on preference that doesn't need situation-specific triggering. Short rules, not workflows.
- **Neither** — most observations should fall here. The bar is high. Don't propose unless you'd defend the proposal to a skeptic.

**Default to silence.** A batch of 20 observations might produce 0–3 proposals. If nothing is strong enough, output an empty proposals list. Producing noise here costs the user's attention and erodes trust.

## Rules

- Do **not** propose for observations with `status` other than `active`.
- Do **not** propose for observations with `evidence_count` < 2 unless the evidence is exceptionally strong (e.g., an explicit "always do X" / "never do Y" statement).
- Do **not** propose duplicates of what's already in always-on instructions or existing skills (you don't have visibility into those — be conservative).
- Each observation can have at most one proposal. Pick the better fit of skill vs. always-on instructions.
- The `reasoning` field is one sentence. Be honest about why — "user explicitly stated this multiple times" beats "this seems important".

## Observations to review

```
{{OBSERVATIONS}}
```

## Output

A single JSON object. No preamble, no fences.

```json
{
  "proposals": [
    {
      "obs_id": "obs_NNNN",
      "kind": "skill" | "claude-md",
      "reasoning": "<one sentence>"
    }
  ]
}
```
