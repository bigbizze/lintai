# LintAI

Deterministic architectural policy enforcement for codebases. Catches repository-structure and engineering-philosophy violations that AI coding agents and hurried humans routinely introduce.

## Structure

- `specs/` — v1 and v2 architecture specifications
- `cmd/lintai/` — Go CLI entry point
- `internal/` — Go engine, snapshot, and Goja runtime packages
- `packages/sdk/` — `@lintai/sdk` TypeScript rule authoring SDK
- `packages/eslint-plugin/` — `@lintai/eslint-plugin` ESLint integration
- `packages/authoring-rules/` — `@lintai/authoring-rules` rule-authoring lint checks

## Setup

```sh
pnpm install
go build ./cmd/lintai
```

The Go toolchain must be on your `PATH`. No sibling checkout of `typescript-go` is required once the forked module pin is in place.

## npm Packaging

Linux and macOS distribution is packaged as:

- `lintai` — npm CLI launcher
- `@lintai/eslint-plugin` — ESLint integration plus bundling assets
- `@lintai/sdk` — rule authoring SDK
- `@lintai/lintai-{darwin,linux}-{x64,arm64}` — native binaries

Build the publishable artifacts locally with:

```sh
pnpm build:dist
pnpm smoke:npm
```

If the consuming repository uses pnpm 10+, allow the `esbuild` install step:

```json
{
  "pnpm": {
    "onlyBuiltDependencies": ["esbuild"]
  }
}
```
