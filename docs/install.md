# Install and Caveats

This page covers both consumer installation and source builds.

## Requirements and caveats

For npm consumers:

- Linux or macOS
- `x64` or `arm64`
- stable Node `>=26.13.1 <27`
- a TypeScript repository with a `tsconfig.json` somewhere under the workspace you want to analyze

For source builds:

- Go on your `PATH`
- stable Node `>=26.13.1 <27`
- `pnpm`

Current caveats:

- Windows packaging is out of scope right now. Use WSL.
- the Linux binaries are static, so they run on glibc and musl (Alpine) systems
- only the TypeScript backend ships today
- packaged binaries are distributed through `@lintai/cli` plus platform-specific optional dependencies

## Install from npm and caveats

### CLI + SDK

```sh
pnpm add -D @lintai/cli @lintai/sdk
# or
npm install -D @lintai/cli @lintai/sdk
```

This gives you:

- `pnpm exec lintai`
- the TypeScript rule authoring SDK

### CLI + SDK + ESLint integration

```sh
pnpm add -D @lintai/cli @lintai/sdk @lintai/eslint-plugin eslint@^10.15.0 @typescript-eslint/parser@^8.73.0
# or
npm install -D @lintai/cli @lintai/sdk @lintai/eslint-plugin eslint@^10.15.0 @typescript-eslint/parser@^8.73.0
```

`@lintai/sdk` does not depend on Zod. If your rules export `config` schemas using Zod, add it yourself:

```sh
pnpm add -D zod@3
```

A named `config` export supplies a synchronous `parse(value)` method. The parser runs in the pure runtime. See the [SDK example](../packages/sdk/README.md).

### CLI + SDK + oxlint integration

```sh
pnpm add -D @lintai/cli @lintai/sdk @lintai/oxlint-plugin oxlint
# or
npm install -D @lintai/cli @lintai/sdk @lintai/oxlint-plugin oxlint
```

This does not install ESLint. The plugin needs oxlint 1.90.0 or later. See [Oxlint plugin reference](api.md#oxlint-plugin-reference-and-caveats).

## Quick consumer setup and caveats

Create a `lintai-rules/` directory:

```sh
mkdir -p lintai-rules
```

Add a rule:

```ts
// lintai-rules/no-async.ts
import { dl, rule } from "@lintai/sdk";

export default rule("arch.no-async")
	.version(1)
	.program(dl`
		Decl violation(Fn, File, Name).
		violation(Fn, File, Name) :- ts.fn(Fn, File, Name), ts.fn_async(Fn), lintai.glob(File, "src/**/*.ts").
	`)
	.report("violation")
	.message((row) => `${row.Name} in ${row.File} must not be async`);
```

Create `lintai.toml` in the workspace root.

```toml
[rules]
entries = ["lintai-rules/no-async.ts"]
```

```sh
pnpm exec lintai build-rules && pnpm exec lintai check
```

The native builder resolves installed rule dependencies and publishes `.lintai/rules.json`. Checks keep using the last successful build until you rebuild. The native executable can check the pack with Node absent from `PATH`.

With npm, use `npm exec -- lintai` in place of `pnpm exec lintai`.

The CLI exits with 0 when there are no violations, 1 when a rule found violations, 2 for a usage or configuration error, and 3 when a rule could not run. By default, the snapshot contains selected files that their `tsconfig.json` compiles, plus selected files without a `tsconfig.json`. Use `--files walk` to include all selected source files, or repeat `--exclude 'src/generated/**'` to exclude more paths. The default walk skips `node_modules`, `dist` and dot-directories such as `.next`.

Each selected file has one compiler owner. Starting in its directory, lintai finds the nearest ancestor named exactly `tsconfig.json` within the workspace. It selects that config when its original parsed roots contain the file; otherwise it searches existing project references depth first in resolver order, with cycle protection, for the first matching root, then falls back to the nearest config. Missing references are skipped; a malformed reached config is an error. A file with no nearest config uses the initial synthetic program. Exact `--source` roots are added after ownership selection. `tsconfig` mode excludes files absent from the chosen program; `walk` mode prepares a synthetic fallback for them. `lintai scope --explain --json` shows the selected config, effective owner and known root memberships for every selected file.

For eligible modules, root membership is drawn from the bounded set of original config roots built from all selected files' nearest configs and their references. It is separate from the one effective owner and does not include files that are only visible through imports. Calls use the caller's owner; derived types and declarations keep the context of the root that requested them. Public config paths use one normalized representation, which supports joins from `module_owner_config` to `config_adjusted` and from `source_entry_tsconfig` to `source_config_adjusted`.

For shared scope settings, put `exclude`, `include` and exact `sources` under `[workspace]` in `lintai.toml` at the workspace root. Repeat `--source <file>` to add exact files, including files otherwise excluded. Use `--config <file>` to choose a different config, relative to the invocation directory. See [Snapshot scope](api.md#snapshot-scope) for precedence, `--files`, and how to inspect the selection.

## ESLint and Oxlint setup

Build the rule pack, enable the fixed error and warning adapters, and use a single invocation-scoped plugin for bulk linting. The same configuration defaults to live requests in the editor. See the [ESLint and Oxlint configurations and commands](api.md#linter-rules).

## Build from source and caveats

If you are developing LintAI itself:

```sh
pnpm install
go build ./cmd/lintai
pnpm build
go test ./...
```

Useful source-build caveats:

- `@lintai/cli` is the npm launcher package, but the repo-root Go binary is still built from `./cmd/lintai`
- source builds need stock Go 1.27.3 or later and pnpm only. There is no compiler fork, no `tsc` binary and no `typescript` npm dependency for extraction: the TypeScript 7 compiler is copied into `internal/tsgo/internal/` and compiled into the `lintai` binary. Its version is in `internal/tsgo/VERSION`.
- removing the fork also removes the main obstacle to Windows builds, but this release does not ship them
- npm packaging smoke tests use:
  - `pnpm build:dist`
  - `pnpm smoke:npm` (ESLint)
  - `pnpm smoke:oxlint` (oxlint CLI and language server, with no ESLint installed)

## Cost

These numbers come from `pnpm bench:g3`. It runs lintai on the zod repository (511 files, 10,214 findings) with the benchmark rules. They were measured on Linux x64 with Node 26.13.1, Go 1.27.3, ESLint 10.15.0 and Oxlint 1.90.0.

| Run | Time |
|---|---|
| `lintai check`, cold | 10 s |
| ESLint CLI, 477 files, cold | 14 s |
| Oxlint CLI, 477 files, cold | 10.5 s |
| Capture of the whole workspace, warm | 3.4 s |
| One file in the editor, warm | 84 ms |

Most of the cold time is the TypeScript type checker. A warm run reuses the facts when no input changed. Run `pnpm bench:g3` to measure your own machine.

Set `LINTAI_EXTRACT_TIMING=1` to print one line to stderr with the number of files and programs and the milliseconds spent in config loading, program creation, the fact walk and in total.

## Troubleshooting and caveats

### `lintai could not locate the native binary package`

Reinstall `@lintai/cli` on a supported platform and architecture. The launcher resolves one of:

- `@lintai/lintai-darwin-arm64`
- `@lintai/lintai-darwin-x64`
- `@lintai/lintai-linux-arm64`
- `@lintai/lintai-linux-x64`

### Missing rule pack

Run `pnpm exec lintai build-rules` from the workspace root. Each path in `[rules].entries` must name a rule entry. Install the SDK and any packages the rule imports. A build error names the failing entry and leaves the previous pack readable.

### ESLint does not pick up a rule change immediately

Rebuild with `lintai build-rules` and lint the source file again. In locked mode, approve the replacement pack with `lintai lock` after review. The editor can pick up new IDs in the next live operation.
