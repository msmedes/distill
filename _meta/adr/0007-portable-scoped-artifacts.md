# 0007. Portable scoped artifacts

## Context

distill originally treated promoted guidance as user-level output: always-on instructions went to the configured user instruction file, and skills went to the configured user skills directory. That is too broad for observations learned from one repository. If repeated corrections happen because a specific project has specific conventions, promoting them globally pollutes future unrelated sessions.

Modern coding agents increasingly compose instruction files and skills by scope: user-level guidance, project-level guidance, and sometimes nested workspace guidance. Distill should preserve that distinction without becoming an installer for every agent runtime.

## Decision

Observations carry scope:

- `user`: the claim applies broadly across the developer's work.
- `project`: the claim applies to the project cwd where the evidence was observed.

Promotion proposals choose an artifact and scope:

- `artifact=agents-md`, `scope=user` writes the configured user instructions file.
- `artifact=skill`, `scope=user` writes under the configured user skills directory.
- `artifact=agents-md`, `scope=project` writes `<project>/AGENTS.md`.
- `artifact=skill`, `scope=project` writes `<project>/.agents/skills/<name>/SKILL.md`.

Distill emits portable artifacts. It does not try to keep runtime-specific Claude, Codex, OpenCode, Pi, or OpenRouter paths synchronized. Users and other tools are responsible for symlinking, copying, or configuring agent runtimes that do not read the portable paths directly.

The LLM may recommend artifact and scope, but Go code resolves concrete paths deterministically.

## Consequences

- Project-specific correction loops can become project-local instructions or skills instead of global user preferences.
- The data layer can later support workspace scope without changing the promotion concept.
- Distill avoids maintaining a compatibility matrix of every agent runtime's evolving loader paths.
- Users may need to configure or symlink `.agents/skills` for runtimes that do not load that portable path by default.
- Existing observations without scope remain valid and normalize to `user`.
