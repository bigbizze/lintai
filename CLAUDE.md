# LintAI — Project Instructions

## What this is

LintAI is a deterministic architectural policy enforcement system. It catches repository-structure and engineering-philosophy violations that AI coding agents and hurried humans routinely introduce. Read the specs in `specs/` before making architectural decisions.

## Repository layout

- **Go host** at repo root: `go.mod`, `cmd/`, `internal/`
- **JS/TS packages** under `packages/`, managed by pnpm workspaces
- **Specs** in `specs/` — the authoritative architecture documents

## Toolchain

- **Go 1.24**: binary at `/usr/local/go/bin/go` (not on default PATH)
- **Node 22** + **pnpm 10**: for JS/TS packages
- **Biome**: JS/TS formatting and linting (`pnpm check`, `pnpm format`)
- **TypeScript 5.7+**: rule authoring language

## Go packages

- `cmd/lintai/` — CLI entry point
- `internal/engine/` — orchestrator, rule execution, diagnostic collection
- `internal/snapshot/` — immutable semantic snapshot construction
- `internal/gojaruntime/` — Goja JS runtime bridge for pure assertion phase

## JS/TS packages

- `@lintai/sdk` (`packages/sdk/`) — query constructors, rule builder, type definitions
- `@lintai/eslint-plugin` (`packages/eslint-plugin/`) — ESLint bridge rule + authoring lint rules
- `@lintai/authoring-rules` (`packages/authoring-rules/`) — rule-authoring purity checks

## Key architecture rules

- The **pure assertion phase** (assert/message) runs in Goja inside the Go process. It must NOT have access to filesystem, network, process, wall clock, randomness, or any ambient host capabilities.
- The **setup phase** runs in a separate Node-compatible worker/process over the bundled rule module. It is capability-restricted to read-only local workspace inspection. No network, writes, child processes, time, randomness, or arbitrary process.env reads.
- **Environment-dependent values** must come through the explicit config/env channel (Zod schema), not ambient reads.
- **File paths** in rule-visible data are canonical normalized workspace-relative paths, anchored to workspace root.
- **Rules are trusted repository code** in v1. No hostile third-party plugin isolation.
- **Queries are immutable values** with host-owned execution. Terminal assertions lower to host-owned assertion handles.
- **Rule execution is atomic per rule**. If any phase throws, all provisional findings for that rule are discarded.

## Build commands

```sh
pnpm install                           # install JS/TS dependencies
pnpm check                             # run Biome linter/formatter check
pnpm format                            # auto-format JS/TS with Biome
/usr/local/go/bin/go build ./cmd/lintai  # build Go CLI
/usr/local/go/bin/go test ./...          # run Go tests
```
