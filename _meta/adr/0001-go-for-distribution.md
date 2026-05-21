# 0001. Go for distribution-grade binary

Status: Accepted
Date: 2026-05-21

## Context

distill is built to be installed by other people — not just used in the development environment that created it. The deployment story is "download a binary and run it." The initial scaffold was TypeScript + Bun because the surrounding ecosystem (Cairn) uses Bun. That choice was rejected on the spot: `Bun build --compile` produces a binary, but the binary is heavy, the build story is more complex, and runtime concerns leak in.

## Decision

distill is written in Go, standard library only where possible. `go build` produces a single static binary that runs anywhere on the target architecture with no other artifacts.

## Consequences

- **+** Zero runtime dependencies. The binary ships as one file.
- **+** Cross-compilation is trivial (`GOOS=darwin GOARCH=arm64 go build`).
- **+** Idiomatic concurrency (goroutines for the HTTP server, mutex for the store) is cheap and well-supported.
- **−** Type sharing with Cairn (TypeScript) is now structural, not nominal. Schemas duplicated on both sides.
- **−** JSON marshaling is more verbose than Zod. Acceptable for the size of this project.
