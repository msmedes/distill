# Generate a Claude Code skill

You are converting an observation about a developer into a Claude Code **skill** — a reusable instruction file that future agent sessions will load when relevant.

## Input

- **Observation ID:** `{{OBS_ID}}`
- **Type:** `{{OBS_TYPE}}`
- **Claim:** {{OBS_CLAIM}}
- **Evidence (real moments where this pattern showed up):**
{{OBS_EVIDENCE}}
- **User notes (refinements the user provided):**
{{OBS_NOTES}}

## Skill format

A skill is a markdown file. Frontmatter has `name` (kebab-case slug, ≤40 chars) and `description` (the trigger — must include "Use when …" so the matcher knows when to fire). Body is markdown explaining the rule + how to apply it.

A good description is **specific**: it names the situations that should trigger the skill. Bad: "for code quality". Good: "Use when writing or reviewing TypeScript/full-stack code. Covers …"

## What you are writing

Convert the observation into a skill that, when loaded into a future session's context, would make the model behave the way the user actually wants. Keep it terse — skills are loaded into context so every word costs tokens. Pull from the evidence quotes — they show how the user actually phrases things.

If the observation is too thin or too situational to make a useful skill (a single one-off correction, or a pattern that only applies to one project), say so by setting the body to `INSUFFICIENT` and skip — caller will surface that.

## Output

Output exactly one JSON object. No preamble, no fences, no closing remarks.

```json
{
  "name": "<kebab-case-slug>",
  "description": "<one-line trigger description that includes 'Use when …'>",
  "body": "<markdown body of the skill — terse, actionable, ready to be loaded into context>"
}
```
