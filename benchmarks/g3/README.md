# G3 benchmark reproduction

This reproduces the F6 zod workload, not an ordinary ESLint CLI run. It uses two active rules, `files=tsconfig`, an empty environment, one rule worker, default severity/fact limit/sandbox, no lock or suppression recording, and no `lintai.toml` or suppression file. The exact 511 analyzed paths are in `analyzed-files.json`; 477 end in `.ts`. The first path is the single-file workload. The host uses ESLint from this repository and the locked parser 8.58.0 and TypeScript 5.9.3 from `host-tools`.

Use Node 26.13.1 first on `PATH`, Go 1.27.3 and pnpm 10.9.0. Build the repository with `pnpm install --frozen-lockfile && pnpm build`. Run the commands below in an otherwise idle environment. `PNPM_JS` is the absolute path to pnpm 10.9.0's `bin/pnpm.cjs`; executing it with Node bypasses a package-manager shim that otherwise refuses this zod checkout's `nub` declaration. Pick an empty directory outside the checkout for `BENCH`. The clone can use a local zod repository instead of the URL.

```sh
export LINTAI_BENCH_REPO="$(pwd)"
export BENCH="/absolute/path/to/empty-benchmark-directory"
export PNPM_JS="/absolute/path/to/pnpm/10.9.0/bin/pnpm.cjs"
mkdir -p "$BENCH"
git clone --no-checkout https://github.com/colinhacks/zod.git "$BENCH/zod"
git -C "$BENCH/zod" checkout --detach b1f6ba06c5de0e600640917615e5df70fa73f060
cp benchmarks/g3/zod-resolved-lock.yaml "$BENCH/zod/pnpm-lock.yaml"
printf 'packages:\n  - "packages/*"\n' > "$BENCH/zod/pnpm-workspace.yaml"
shasum -a 256 "$BENCH/zod/pnpm-lock.yaml"
(cd "$BENCH/zod" && node "$PNPM_JS" install --config.package-manager-strict=false --link-workspace-packages --ignore-scripts --frozen-lockfile)
mkdir -p "$BENCH/rules/node_modules/@lintai" "$BENCH/host-tools"
cp benchmarks/g3/rules/*.ts "$BENCH/rules/"
ln -s "$LINTAI_BENCH_REPO/packages/sdk" "$BENCH/rules/node_modules/@lintai/sdk"
cp benchmarks/g3/host-tools/package*.json "$BENCH/host-tools/"
npm ci --prefix "$BENCH/host-tools" --ignore-scripts
export LINTAI_BENCH_WORKSPACE="$BENCH/zod"
export LINTAI_BENCH_RULES="$BENCH/rules"
export LINTAI_BENCH_TOOLS="$BENCH/host-tools"
go run ./benchmarks/g3 | tee "$BENCH/engine.jsonl"
python3 benchmarks/g3/rpc.py | tee "$BENCH/rpc.jsonl"
node benchmarks/g3/host.mjs | tee "$BENCH/host.jsonl"
python3 benchmarks/g3/count-build.py "$BENCH/count-build"
LINTAI_BENCH_BINARY="$BENCH/count-build/lintai-counted" LINTAI_BENCH_TRACE="$BENCH/validations.log" node benchmarks/g3/host.mjs | tee "$BENCH/host-counted.jsonl"
```

The archived substitute lock SHA-256 is `508845cb8833310c3b723754ff82455c114e145881601bcc54e8f69cb89f6ad3`. The upstream tracked lock is incomplete for frozen pnpm installation; this archive is the F6 resolved replacement, so the installed dependency graph is tied to it. The lock and temporary workspace declaration deliberately remain in the isolated clone. No benchmark rule is inside the analyzed workspace. Use `--offline` on both installs if the exact packages are already cached.

The engine harness checks all 511 paths, 10,214 ordered diagnostics and every batch projection against the whole result, then measures 20 unchanged single-file refreshes and five forced Full checks. The RPC harness measures the same single file and five forced checks in a separate server process. The host harness runs the historical `Linter.verify` loop, including its per-file microtask, for a cold batch, a warm batch and a warm pass of ordinary callbacks. Each pass must have 477 files, 10,074 findings and exactly the same ordered messages. It counts actual client methods and uses the normal process-exit client lifecycle. Cold means a new lintai process, not a cleared operating-system cache.

The final command builds a temporary Go overlay with a trace at the existing workspace-result and configuration validation calls. It checks that the unchanged warm batch executes each site once and that ordinary callbacks execute each 477 times; its separately instrumented timing is not the reported host timing. A new analysis can have further producer or bundle checks internally; the counter describes the publication and configuration validation entry points. The first cold batch has no prior publication to validate. This probe modifies neither the repository nor the fixture.
