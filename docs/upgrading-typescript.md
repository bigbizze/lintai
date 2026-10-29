# Upgrading TypeScript

lintai extracts facts with a copy of the TypeScript 7 compiler. The copy is in `internal/tsgo/internal/`, and `internal/tsgo/VERSION` names the `microsoft/TypeScript` commit it came from. Nobody edits the copy by hand. `scripts/sync-tsgo.sh` writes it.

To move lintai to a new TypeScript version:

1. Pick a `microsoft/TypeScript` commit. Normally this is the `gitHead` of a published `typescript` npm release:

   ```sh
   npm view typescript@<version> gitHead
   ```

2. Run the sync script. It fetches the commit, computes which compiler packages `internal/tsgo/lintaiapi` needs, replaces `internal/tsgo/internal/`, rewrites import paths, updates `internal/tsgo/VERSION`, the license files and `go.mod`, and builds:

   ```sh
   scripts/sync-tsgo.sh <commit>
   ```

3. Fix compile errors in `internal/tsgo/lintaiapi/`. Keep the changes small.

4. Run the golden parity test:

   ```sh
   go test ./cmd/lintai -run TestGoldenParity
   ```

   Explain every fact difference in the commit message, for example a type string that the new checker prints differently. Then regenerate the goldens in a separate commit:

   ```sh
   LINTAI_UPDATE_GOLDEN=1 go test ./cmd/lintai -run TestGoldenParity
   ```

5. Measure extraction with `LINTAI_EXTRACT_TIMING=1 lintai facts --workspace-root <repo>`, and update "Build and extraction cost" in `docs/install.md` if the numbers changed noticeably.

If `lintaiapi` needs a change to the copied compiler that it cannot make through exported APIs, add a patch file under `internal/tsgo/patches/` with a comment that says why it exists. The sync script applies the patches after it copies. Prefer a change in `lintaiapi`.

CI runs the sync script for the commit in `internal/tsgo/VERSION` and fails if the result differs from the committed files.
