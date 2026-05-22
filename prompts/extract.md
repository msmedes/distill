# Observation extraction

You are reading user-authored turns from a single coding session between a developer and Claude Code. Some high-signal user turns include a bounded preceding assistant turn as local context. Your job is to extract **observations** about the developer — claims about their preferences, workflows, friction points, or tool-use patterns — that would help future agents work with them better.

## What you are looking for

**Corrections are the highest-signal source.** When the user pushes back ("no, don't…", "stop doing X", "actually…", "wait,", "you keep doing Y") — that's the user *explicitly stating* a preference. Probably 70-80% of useful observations come from corrections. Look for these first.

**Inferred patterns are lower-signal.** "The user seems to prefer X because they did X twice" is weak. Only emit inferences if the pattern is striking and you can cite specific evidence.

## Observation types

- `preference`: a stated or strongly-inferred taste — "prefers terse responses", "doesn't want speculative error handling"
- `workflow`: a recurring sequence — "asks for a cleanup pass after large refactors", "uses Plan agent before implementation"
- `friction`: a recurring annoyance or correction — "consistently corrects when I add comments explaining what code does"
- `tool-use`: a way of using tools / harness — "prefers running tests via the watch script, not one-shot"

## What to suppress (do not emit observations about these)

- **Task-specific content.** "User is working on a Tauri IPC bug" is not an observation; it's project context.
- **Things already in CLAUDE.md.** If the user has already stated a preference there, don't re-extract it.
- **Generic-to-all-users claims.** "User wants working code" — useless. The observation must differentiate this user from a generic user.
- **Single weak inferences.** If the only evidence is "they did X once and didn't object," that's not enough. Let it sit until it reinforces.

## Existing observations

Before adding a new observation, check the relevant existing observations below. If your finding matches an existing one, output a `reinforced` entry instead of a new observation. If your finding *contradicts* an existing one (the user did the opposite of what the observation predicts), output a `contradicted` entry.

```
{{EXISTING_OBSERVATIONS}}
```

## Evidence requirements

Every observation, reinforcement, or contradiction must cite **specific turn numbers** and include a **short quote** from the transcript. No hand-waving. If you can't cite a turn, you can't emit the observation.

When local assistant context is present, use it only to understand what the user was correcting. Evidence quotes should come from the user's words unless the assistant quote is necessary to identify the corrected behavior.

## Output

Output a single JSON object. **Default toward emitting nothing.** Most sessions should produce zero or one observation. Sessions full of corrections and explicit preferences might produce three or four. If a session is all task content with no taste signal, emit empty arrays — that is the correct output.

```json
{
  "reasoning": "<one or two sentences on what stood out in this session, or 'nothing notable'>",
  "new_observations": [
    {
      "claim": "<one sentence stating the pattern, written in third person about the user>",
      "type": "<preference | workflow | friction | tool-use>",
      "evidence_turn_refs": ["turn 12", "turn 19"],
      "evidence_quote": "<short verbatim quote from the cited turns>"
    }
  ],
  "reinforced": [
    {
      "obs_id": "<id from the existing list above>",
      "evidence_turn_refs": ["turn 47"],
      "evidence_quote": "<short verbatim quote>"
    }
  ],
  "contradicted": [
    {
      "obs_id": "<id from the existing list above>",
      "evidence_turn_refs": ["turn 31"],
      "explanation": "<what the user did that contradicts the observation>"
    }
  ]
}
```

## User-message excerpt

Session ID: `{{SESSION_ID}}`
Project cwd: `{{SESSION_CWD}}`

```
{{TRANSCRIPT}}
```

Output the JSON object now. No preamble, no closing remarks, no markdown fences — just the JSON.
