# internal/tsgo

This directory holds the TypeScript 7 compiler code that lintai uses to extract facts.

- `internal/` is a copy of the Go packages of the TypeScript 7 compiler (`microsoft/TypeScript`, directory `tsc/internal/`) that `lintaiapi` imports, directly or indirectly. `scripts/sync-tsgo.sh` writes it. Nobody edits it by hand. Each file starts with a `DO NOT EDIT` line that names its source path and commit.
- `lintaiapi/` is lintai's extractor. It calls the compiler's parser and checker in the same process and returns the snapshot that `backend/typescript` converts into facts. lintai owns and edits this package. It started as the `lintaiapi` package of the `bigbizze/typescript-go` fork, which lintai no longer uses.
- `VERSION` names the `microsoft/TypeScript` commit, its date, and the npm `typescript` version built from it.
- `LICENSE.txt` and `NOTICE.txt` are the license and third-party notices of `microsoft/TypeScript` at that commit. The copied code is under the Apache License 2.0.
- `patches/` would hold changes to the copied code that `lintaiapi` cannot avoid. The sync script applies them after it copies. There are none now.

Go's rules for `internal/` directories mean that only `lintaiapi` can import the compiler packages, and only lintai can import `lintaiapi`. Go consumers use `backend/typescript`.

## Upgrade TypeScript

The same procedure is in `docs/upgrading-typescript.md`.

1. Pick a `microsoft/TypeScript` commit. Normally use the `gitHead` of a published `typescript` npm release: `npm view typescript@<version> gitHead`.
2. Run `scripts/sync-tsgo.sh <commit>`. It recomputes the package list, replaces `internal/`, rewrites import paths, updates `VERSION`, the license files and `go.mod`, and builds.
3. Fix compile errors in `lintaiapi`.
4. Run `go test ./cmd/lintai -run TestGoldenParity`. Explain every fact difference in the commit message. Regenerate the goldens with `LINTAI_UPDATE_GOLDEN=1` in a separate commit.
5. Measure with `LINTAI_EXTRACT_TIMING=1` and update the numbers in `docs/install.md` if they changed noticeably.

CI runs the sync script for the commit in `VERSION` and fails if the result differs from the committed files.
