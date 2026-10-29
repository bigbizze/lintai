# lintai 0.3.0: copy the TypeScript 7 compiler into lintai, drop the fork

You are working in a clone of `bigbizze/lintai` at `main` (0.2.2 plus the public Go engine API and the plugin failure fixes, head `dba24be`). This document is the full specification for release 0.3.0. Implement all of it. Before writing code, copy this document into the repository as `specs/extraction-vendored-tsgo.md`.

The goal: lintai stops depending on the `bigbizze/typescript-go` fork, and keeps everything the fork gives it. Today `go.mod` replaces `github.com/microsoft/TypeScript/tsc` with the fork, and the extractor lives in the fork's `lintaiapi` package, which calls the compiler's parser and checker in the same process. After this release:

- The Go packages of the TypeScript 7 compiler that `lintaiapi` needs are copied, unmodified except for import paths, into `internal/tsgo/internal/` by a script, from one exact `microsoft/TypeScript` commit.
- `lintaiapi` moves into lintai as `internal/tsgo/lintaiapi/`, owned and edited by lintai.
- Extraction stays in-process and works exactly as it does now. There is no second process, no pipe, no binary syntax-tree decoder and no `tsc` executable. Node is not involved in extraction.
- Upgrading TypeScript means running the script for a new commit and running the golden parity test. No fork to rebase.

Rule authors see the same base relations and the same facts, byte for byte, except for one new additive relation (section 7).

Backwards compatibility with the fork is not a goal. Delete the `replace` directive and the `github.com/microsoft/TypeScript/tsc` requirement from `go.mod`.

## 1. Facts this design rests on

These were checked on 2026-10-29. Re-check each one; where the code disagrees with this document, the code wins.

- The TypeScript 7 compiler's Go source lives in `microsoft/TypeScript` under `tsc/`, as the Go module `github.com/microsoft/TypeScript/tsc` (Go 1.27). The old `microsoft/typescript-go` repository is not the source any more. Paths below are relative to `tsc/` unless stated.
- The npm package `typescript@7.1.0-beta` was built from commit `3e9236361fa97a9e6c685f4833ac457ae2d8c50a` (the `gitHead` in the platform package's `package.json`). This is the upgrade target for phase 2 (section 5).
- The fork, at pseudo-version `v0.0.0-20261028214434-1b2c2fa7c3bb`, is `tsc/` plus a top-level `lintaiapi/` directory (`snapshot.go`, about 1,800 lines, `ambient.go`, and tests). Its `internal/api/encoder/decoder.go` is byte-identical to the one at the 7.1.0-beta commit, so the fork is close to 7.1.0-beta.
- `lintaiapi` imports 49 compiler packages, about 224,000 non-test lines. The whole `tsc` module is about 332,000. The list, from `go list -deps`: `internal/` `json`, `collections`, `debug`, `stringutil`, `tspath`, `core`, `locale`, `diagnostics`, `spanmap`, `ast`, `vfs`, `bundled`, `jsnum`, `scanner`, `astnav`, `binder`, `evaluator`, `semver`, `packagejson`, `vfs/vfsmatch`, `module`, `outputpaths`, `symlinks`, `jsonrpc`, `ipc`, `parser`, `contentmapper`, `glob`, `nativepath`, `tsoptions`, `modulespecifiers`, `nodebuilder`, `sourcemap`, `printer`, `pseudochecker`, `tracing`, `checker`, `transformers` and its subpackages (`declarations`, `estransforms`, `inliners`, `jsxtransforms`, `moduletransforms`, `tstransforms`), `vfs/cachedvfs`, `compiler`, `osutil`, `vfs/internal`, `vfs/osvfs`. Recompute it with the script; do not rely on this list.
- Two of these packages embed files with `go:embed`: `internal/bundled` (the `lib.*.d.ts` files) and `internal/diagnostics`. The script must copy the embedded files too. `lintaiapi` uses `bundled.WrapFS(osvfs.FS())` and `bundled.LibPath()`, so the standard library declarations come from the binary itself, as today.
- The fork's `go.mod` requires `github.com/Microsoft/go-winio`, `github.com/google/go-cmp`, `github.com/klauspost/compress`, `github.com/mackerelio/go-osstat`, `github.com/peter-evans/patience`, `github.com/zeebo/xxh3`, `golang.org/x/sync`, `golang.org/x/sys`, `golang.org/x/term`, `golang.org/x/text` and `gotest.tools/v3`, with `github.com/klauspost/cpuid/v2` indirect. Only the ones the copied closure imports belong in lintai's `go.mod`.
- Go's rule for `internal/` directories is based on import paths. Code may import `a/b/internal/c` only if the code is inside `a/b/`. So with the compiler copied to `github.com/bigbizze/lintai/internal/tsgo/internal/...`:
  - `internal/tsgo/lintaiapi` may import the compiler packages, because it is inside `internal/tsgo/`.
  - `backend/typescript` may import `internal/tsgo/lintaiapi`, because it is inside the lintai module.
  - Nothing outside lintai can import either. Go consumers keep using `backend/typescript`, as today.
- `lintaiapi`'s synthetic config for walk mode, `tsconfig.lintai.synthetic.json`, is written into the workspace root (`snapshot.go`, around line 565). It moves to the OS temp directory in this release.
- The code is Apache 2.0. The copy must carry the license and notices.

## 2. What changes

1. **`scripts/sync-tsgo.sh <commit>`** copies the compiler closure into `internal/tsgo/internal/` (section 4).
2. **`internal/tsgo/lintaiapi/`**: the fork's `lintaiapi` moved into lintai. Only import paths change, plus the temp-directory move and the tsconfig fallback (section 7).
3. `backend/typescript/backend.go` imports `github.com/bigbizze/lintai/internal/tsgo/lintaiapi` instead of the fork's package. The rest of `backend/typescript`, `internal/analysis/model.go`, `facts/`, `engine/`, `runtime/`, `server/` and `diagnostics/` stay as they are, apart from the new relation in section 7.
4. `go.mod` loses the fork and gains the compiler's own external dependencies.
5. Two phases: phase 1 copies the commit the fork is based on and must reproduce today's facts byte for byte; phase 2 moves to the 7.1.0-beta commit (section 5).
6. A tsconfig that TypeScript 7 refuses to load is rewritten into a temporary, equivalent tsconfig instead of failing the run (section 7).

## 3. Read first

- `backend/typescript/backend.go` and `access_extract.go`: how the snapshot from `lintaiapi` is converted, and the `import.meta` scanner, which stays as it is.
- `internal/analysis/model.go`: the snapshot structs.
- `internal/ambient/` and `internal/workspace/files.go`: unchanged, but you need to know what they hand to `lintaiapi`.
- `docs/go-api.md` and `testdata/consumer-module/`: Go consumers currently have to copy the fork `replace`. After this release they need nothing extra.
- **The fork.** `go mod download github.com/microsoft/TypeScript/tsc` puts it in the module cache (`go env GOMODCACHE`, directory `github.com/bigbizze/typescript-go@v0.0.0-20261028214434-1b2c2fa7c3bb`). Read `lintaiapi/` in full. Then find out exactly what the fork changed relative to `microsoft/TypeScript`:
  1. Find the upstream commit the fork is based on. Check the fork's git history if you can clone `bigbizze/typescript-go`; otherwise compare its `tsc/` tree against nearby `microsoft/TypeScript` commits from 2026-10-27 and 2026-10-28 and pick the one with the smallest difference.
  2. Diff every file outside `lintaiapi/` against that commit.
  3. Report the result before going further. The expected answer is "no changes outside `lintaiapi/`". If there are changes, prefer moving them into `lintaiapi` (using only exported or same-module APIs). Only if that is impossible, keep each one as a patch file under `internal/tsgo/patches/`, applied by the sync script after copying, with a comment saying why it exists.
- **`microsoft/TypeScript` at both commits** (the fork's base and `3e92363`), under `tsc/`: the packages `lintaiapi` calls, especially `compiler`, `checker`, `tsoptions` and `bundled`.

## 4. The sync script and `internal/tsgo/`

`scripts/sync-tsgo.sh <commit>` does the following, and nothing else writes to `internal/tsgo/internal/`:

1. Fetch `microsoft/TypeScript` at `<commit>` into a temporary directory (a shallow fetch of that one commit is enough).
2. Compute the closure: place lintai's `internal/tsgo/lintaiapi/` (with its imports pointing at `github.com/microsoft/TypeScript/tsc/internal/...`, rewritten on the fly) into the fetched `tsc/` module as a temporary package, and run `go list -deps -f '{{.ImportPath}} {{.Dir}}'` on it. Keep every package whose path starts with `github.com/microsoft/TypeScript/tsc/internal/`. Fail if `lintaiapi` imports a compiler package outside `internal/`.
3. Delete `internal/tsgo/internal/` and copy each package in the closure to the matching path under it. Copy the non-test `.go` files and every file referenced by a `go:embed` directive in them (use `go list -f '{{.EmbedFiles}}'`). Drop `_test.go` files and `testdata/`.
4. Apply `internal/tsgo/patches/*.patch` in name order, if any exist. Fail if one does not apply.
5. Rewrite import paths in the copied files and in `internal/tsgo/lintaiapi/` from `github.com/microsoft/TypeScript/tsc/` to `github.com/bigbizze/lintai/internal/tsgo/`. Use a small Go program that edits import specs (`go/parser` and `go/printer`, or `golang.org/x/tools/go/ast/astutil`), not a text replacement over whole files.
6. Add a first-line comment to each copied `.go` file: `// Code copied from microsoft/TypeScript tsc/<path> at <commit>. DO NOT EDIT; run scripts/sync-tsgo.sh.` The `DO NOT EDIT` wording makes Go tools treat the file as generated.
7. Copy the repository's license file and third-party notices file (whatever they are called at that commit) into `internal/tsgo/`.
8. Write `internal/tsgo/VERSION`: the commit, its date, and the npm version built from it if there is one (for example `7.1.0-beta`).
9. Run `go mod tidy` and `go build ./...`.

Also:

- Add a CI step that runs the script for the commit in `VERSION` into a clean checkout and fails if the result differs from what is committed.
- Exclude `internal/tsgo/internal/` from lintai's own vet and lint configuration, and from coverage reports. Keep `go vet` on `internal/tsgo/lintaiapi/`.
- Add `internal/tsgo/README.md`: what the directory is, that nobody edits `internal/` by hand, where `lintaiapi` came from, and the upgrade procedure (section 9).
- Mark `internal/tsgo/internal/**` as `linguist-generated` in `.gitattributes` so GitHub collapses it in diffs and leaves it out of language statistics.

## 5. Order of work

### Phase 0: golden parity corpus, before changing anything

With the fork still in place, build the current binary and capture goldens, committed to the repository:

1. `go build ./cmd/lintai && pnpm -r build`.
2. `./lintai facts --workspace-root testdata/fixtures/workspace > testdata/golden/workspace-facts.txt` (all relations).
3. The same for `testdata/npm-smoke`, in both `--files walk` mode and the default `tsconfig` mode (two files).
4. `./lintai --workspace-root testdata/fixtures/workspace --rules 'testdata/fixtures/rules/*.ts' --json`, using the rule globs that `cmd/lintai/main_test.go` uses, saved as `testdata/golden/workspace-diagnostics.json`. Strip the absolute-path and snapshot-version fields with a small script and keep everything else.

Add `cmd/lintai/golden_parity_test.go`, which runs the same commands and asserts that facts match byte for byte and diagnostics match field for field. It runs in CI from now on, and on every TypeScript upgrade.

Also record, with the fork build: the time of `go build ./cmd/lintai` from a clean build cache, the size of the `lintai` binary, and extraction time (see section 8) on the fixture workspace and on one public repository with at least 500 modules whose tsconfig loads under TypeScript 7. Name the repository and commit.

### Phase 1: same compiler, no fork

1. Copy `lintaiapi/` from the fork into `internal/tsgo/lintaiapi/`, tests included.
2. Run `scripts/sync-tsgo.sh <fork base commit>`.
3. Point `backend/typescript/backend.go` at the new package, update `go.mod` (section 6), and build.
4. The golden parity test must pass byte for byte. Here the compiler code is the same as the fork's, so any difference is a bug in the copy. Fix it; do not regenerate goldens.
5. Commit this phase on its own.

### Phase 2: move to TypeScript 7.1.0-beta

1. Run `scripts/sync-tsgo.sh 3e9236361fa97a9e6c685f4833ac457ae2d8c50a`.
2. Fix compile errors in `lintaiapi` caused by compiler API changes. Keep the changes small, and list each one in the final report.
3. Run the golden parity test. If facts differ, explain each difference in the commit message (for example, a changed type string from the checker). Regenerate the goldens only after every difference is explained, in a separate commit from the code change.
4. Make the section 7 changes and the walk-mode temp-directory change in this phase, after parity is established.

## 6. `go.mod`, the build, and Go consumers

- Remove the `github.com/microsoft/TypeScript/tsc` requirement and the `replace`. Add whatever the copied closure imports, then `go mod tidy`. Do not add modules the closure does not import (for example, `gotest.tools/v3` is test-only in the compiler and should not appear).
- Set the `go` directive to the highest version the copied code requires (1.27 at the time of writing), and set CI's `go-version` to match. Stock Go must build lintai with no extra steps.
- `testdata/consumer-module/go.mod`: drop the fork `replace`. `go run .` must work with only a `require` of lintai (and the local `replace` of lintai itself that the test setup already uses, if any).
- `docs/go-api.md`: remove the instructions about the fork `replace`. Say that `backend/typescript` contains a copy of the TypeScript 7 compiler at the commit in `internal/tsgo/VERSION`, so importing it adds roughly the measured build time and binary size.
- `scripts/build-npm-binaries.mjs` and `smoke-npm-install.mjs`: remove any fork fetch. The npm packages need no `typescript` dependency for extraction. The root workspace keeps its own `typescript` devDependency for building the JS packages.

## 7. Tsconfigs that TypeScript 7 rejects

TypeScript 7 removed compiler options that TypeScript 5 projects still use. For example, the 7.1 beta rejects this config, which is close to what many Next.js projects use:

```json
{ "compilerOptions": { "baseUrl": ".", "moduleResolution": "node", "target": "es5", "paths": { "@/*": ["src/*"] } }, "include": ["src"] }
```

with:

```
error TS5102: Option 'baseUrl' has been removed. Please remove it from your configuration.
  Use '"paths": {"*": ["./*"]}' instead.
error TS5108: Option 'moduleResolution=node10' has been removed. Please remove it from your configuration.
error TS5108: Option 'target=ES5' has been removed. Please remove it from your configuration.
error TS5090: Non-relative paths are not allowed. Did you forget a leading './'?
```

lintai's users keep building with their own compiler, so failing here would make lintai unusable on the repositories it is for. In `lintaiapi`'s tsconfig loading:

1. Parse the config with the compiler's `tsoptions` package, as `lintaiapi` already does. If the parse produced no option diagnostics, load it as is.
2. Otherwise use the parsed raw config and options (which follow `extends`) and the diagnostics themselves to decide what to change. Do not keep a hand-written list of removed options; drive it from the diagnostic codes and the option they name.
3. Write a flattened temporary tsconfig into the OS temp directory, with every path made absolute:
   - `baseUrl` together with `paths`: rewrite each `paths` target as an absolute path under the old `baseUrl`, then drop `baseUrl`.
   - `baseUrl` alone: replace it with `"paths": { "*": ["<abs baseUrl>/*"] }`, as the compiler's own hint says.
   - `moduleResolution` set to a removed value: use `"bundler"`.
   - Any other removed option: drop it.
   - `files`: the exact file list the parse produced for the original config, so the rewrite cannot change which files are modules.
4. Load the temporary config in place of the original and delete it after extraction. Keep reporting the original path in `ConfigFiles`, which the Go side hashes into the snapshot version.
5. Record each change in the snapshot and add a new base relation, in `backend/typescript/schema.mg` and `facts.go`:

   ```
   Decl config_adjusted(Config, Option, Action)
     descr [
       extensional(),
       doc("lintai changed a compiler option that TypeScript 7 rejects before loading this tsconfig. The analysis uses the adjusted configuration, which can differ from the project's own build."),
       arg(Config, "Workspace-relative path of the original tsconfig."),
       arg(Option, "Compiler option name."),
       arg(Action, "/removed, /rewritten or /replaced.")
     ].
   ```

   Also print one stderr line per adjusted config, for example `lintai: adjusted tsconfig.json for TypeScript 7 (baseUrl rewritten, moduleResolution replaced, target removed)`.
6. If the config still fails after adjustment, the snapshot build fails with a message naming the config and the remaining diagnostics.

Add a fixture workspace `testdata/fixtures/legacy-tsconfig/` with the config above and a `@/` import, and a test asserting that the snapshot builds, the `@/` import resolves to an internal `toPath`, and the expected `config_adjusted` rows exist. The golden fixtures need no adjustment, so their facts stay unchanged by this step.

Walk mode: move `tsconfig.lintai.synthetic.json` from the workspace root to the OS temp directory (with a unique name per build, and absolute paths inside it), and delete it after extraction. A lint run must never write into the user's workspace.

## 8. Measurements

- Add a timing line to stderr behind `LINTAI_EXTRACT_TIMING=1`: files, programs loaded, and milliseconds spent in config loading, program creation, the walk, and in total.
- After each phase, measure the same things as in phase 0: clean `go build` time, binary size, and extraction time on the fixture workspace and the chosen public repository. Phase 1 should match the fork within noise, since it is the same code. If it does not, find out why before phase 2.
- Record the final numbers, next to the 0.2.2 numbers, in `docs/install.md`.

## 9. Docs

- `README.md`, "How and caveats": the snapshot is built by a copy of the TypeScript 7 compiler compiled into lintai, at the version in `internal/tsgo/VERSION`. Say plainly that the analyzed types are that compiler's view and can differ from the host repo's own TypeScript, and that a tsconfig using options TypeScript 7 removed is adjusted, with the changes visible as `config_adjusted` facts.
- `docs/install.md`: no fork, no `tsc` binary, no TypeScript npm dependency for extraction. Source builds need stock Go and pnpm only. Give the measured sizes. Note that removing the fork clears the way for Windows builds, which this release does not ship.
- `internal/tsgo/README.md` (section 4) and a section in `CONTRIBUTING` (or `docs/upgrading-typescript.md`) with the upgrade procedure:
  1. Pick a `microsoft/TypeScript` commit, normally the `gitHead` of a published `typescript` npm release.
  2. Run `scripts/sync-tsgo.sh <commit>`.
  3. Fix `lintaiapi` compile errors.
  4. Run the golden parity test. Explain every fact difference in the commit; regenerate goldens in a separate commit.
  5. Run the timing measurement and update `docs/install.md` if it moved noticeably.

## 10. Not goals

- No incremental or watch-mode extraction. Serve mode keeps rebuilding full snapshots.
- No Windows release.
- No virtual file system for unsaved editor buffers. (The compiler's `vfs` package makes this possible later.)
- No change to the rule API, the prelude, diagnostics, exit codes or the server protocol. The only schema change is the additive `config_adjusted` relation.
- No use of the host repo's TypeScript.
- No edits to copied compiler files, other than the patch mechanism in section 3, used only if unavoidable.

## 11. Acceptance checklist

1. `go.mod` contains no `microsoft/TypeScript` and no `bigbizze/typescript-go` reference, and `go build ./cmd/lintai` works with stock Go from a clean clone.
2. `internal/tsgo/internal/` matches the output of `scripts/sync-tsgo.sh` for the commit in `VERSION`, and that commit is `3e9236361fa97a9e6c685f4833ac457ae2d8c50a` (or a later published release's `gitHead`, if you chose one and say why). The CI sync check passes.
3. Phase 1 is its own commit, and the golden parity test passed byte for byte at that commit.
4. At the final commit, `cmd/lintai/golden_parity_test.go` passes, and every golden change since phase 1 is explained in a commit message.
5. The legacy-tsconfig test passes, and a walk-mode run leaves no file in the workspace.
6. All existing Go tests, SDK tests, ESLint plugin tests and `pnpm smoke:npm` pass on Linux and macOS.
7. `lintai facts`, `lintai schema`, one-shot runs, and `lintai serve` under the ESLint plugin all work in a consumer-style install, including `--files walk` and `--exclude`.
8. `testdata/consumer-module` runs with `go run .` and no fork `replace`.
9. Two consecutive runs over an unchanged workspace produce identical snapshot versions and identical diagnostics.
10. Build time, binary size and extraction time for 0.2.2, phase 1 and the final commit are in `docs/install.md`.

## 12. Final report

Report: the commits; the fork's upstream base commit and what the fork changed outside `lintaiapi` (expected: nothing); the number of packages and lines in `internal/tsgo/internal/` at the final commit; every change to `lintaiapi` beyond import paths, and why; every golden difference in phase 2 and its cause; the measurements; and anything you could not implement as written. Once this release is merged, the `bigbizze/typescript-go` repository is no longer used and can be archived; say so in the report rather than doing it.
