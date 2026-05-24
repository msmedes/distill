# Rewrite agent instructions

You are updating an AGENTS.md-style instruction file. It may be user-scoped or project-scoped. This file is loaded directly into future agent sessions, so the result must read like intentional user-authored guidance, not like a tool export.

## Current file

```markdown
{{CURRENT_ALWAYS_ON}}
```

## Observation to integrate

- **Observation ID:** `{{OBS_ID}}`
- **Type:** `{{OBS_TYPE}}`
- **Scope:** `{{OBS_SCOPE}}`
- **Claim:** {{OBS_CLAIM}}
- **Evidence:**
{{OBS_EVIDENCE}}
- **User notes:**
{{OBS_NOTES}}

## Rewrite rules

- Return the complete updated markdown file, not a patch and not commentary.
- Preserve the user's existing hierarchy, voice, priorities, and constraints.
- Integrate the observation where it naturally belongs in the current structure.
- Do not create a special distill section.
- Do not include provenance markers, observation IDs, timestamps, or "distill" labels in the markdown.
- Do not duplicate an instruction that is already present; sharpen or merge the existing wording instead.
- Keep the change as small as possible while making the instruction useful to future agents.
- If the observation is too weak, redundant, or inappropriate for this scoped instruction file, return the current file unchanged.

## Output

Output only the complete updated markdown file. No preamble, no fences, no closing remarks.
