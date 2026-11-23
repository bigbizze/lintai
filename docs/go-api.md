# Go API

The `engine` package provides a retained analysis session. Construct producer instances with their own settings, open a session with the default request, and close it when finished:

```go
import (
    "context"

    "github.com/bigbizze/lintai/backend/typescript"
    "github.com/bigbizze/lintai/engine"
)

func check(ctx context.Context, repoRoot, assetRoot, workspaceRoot string) error {
    producer := typescript.New(typescript.Options{Files: typescript.FileScopeTsconfig})
    session := engine.Open(engine.Options{
        RepoRoot:      repoRoot,
        AssetRoot:     assetRoot,
        WorkspaceRoot: workspaceRoot,
        RuleGlobs:     []string{"lintai-rules/**/*.ts"},
        RuleWorkers:   2,
    }, producer)
    defer session.Close()

    result, err := session.Check(ctx)
    if err != nil {
        return err
    }
    _ = result.DiagnosticsFor("src/example.ts")
    return nil
}
```

`Check` validates the inputs used by each producer before reusing a generation. It retains eligible compiled code and plans, the current fact index, and complete raw results. A changed input replaces the affected producer generation. `Check(ctx, options)` supplies a complete request that overrides the defaults for that call. A returned result describes its completed generation; `DiagnosticsFor` and `Session.Current` only read it. A failed check makes the session's current result unavailable, and calls after `Close` fail. `Close` is idempotent and joins active rule jobs.

`RuleWorkers` bounds independent rule evaluations: zero uses `GOMAXPROCS`, one runs sequentially, and a larger value permits that many active jobs. Negative values fail. Changing only this setting does not invalidate reusable work. `AssetRoot` contains `scripts/bundle-rule.mjs` and `scripts/prepare-rule.mjs`; in this repository it is `packages/client`.

A producer implements `backend.Producer`: `Describe` supplies its namespace and relation catalog; `Collect` returns the requested relations, producer-owned reporting anchors, analyzed-file coverage, an input revision, scope and optional watch hints; `Close` releases its resources. Each collection describes the inputs it actually consumed. The engine validates and freezes the batch before evaluation. Producers own their input validity and may use non-file revision tokens. See `engine/external_backend_test.go` for a small independent producer.

The TypeScript producer accepts `typescript.Options`, including `Files`, `Excludes`, and `SourceLinks`, at construction. Its captured inputs include compiler configuration, content, resolution probes and directory membership. It validates those observations on a fresh check, including dependencies that are not primary files. `Result.AnalyzedFiles` is coverage, while `Result.WatchHints` and the server's `watchHints` are suggestions for requesting another check; a watcher is not a validity proof. The caller that resolves producer configuration should reopen affected producer instances when that configuration changes. The CLI server does this before its checks.

The public `observe` package represents captured content, filesystem operation results, directory listings, link targets, resolved paths and environment values. `observe.New` validates and detaches records; `Records` returns copies, `Union` retains conflicting expectations, and `Digest` creates an I/O-free, workspace-relative identity. `Changed(ctx, observe.Full)` reads and hashes every content record it accepts. `Quick` can reuse a certified size and modification-time hint outside a two-second racy window; a same-size edit with a deliberately restored modification time can therefore be missed until a Full check. Initial bundler observations contain no stamps, because the bundle may have consumed staged bytes. A successful live hash comparison can renew a private hint without changing the observation identity. `observe.Decode` accepts the strict `lintai.observations/1` envelope.

`Session.Facts` explicitly collects selected base relations without running rules; the `facts` command uses this path. The separate module in `testdata/consumer-module` demonstrates importing the TypeScript producer and session from another Go module.

## Suppressions and rule integrity

`Check` applies the workspace suppressions file to complete raw outcomes. `Options.SuppressionsLocation`, `SuppressAll`, `SuppressRules`, and `PruneSuppressions` control the current policy. `Result.Diagnostics` contains presented findings and `Suppressed` contains suppressed findings. `UnusedSuppressions`, `SuppressionsPath`, and `SuppressionsWritten` describe the policy result. Reading a result never replays writes.

Rule bundling, listing, locking and checking require a stable Node executable `>=26.13.1 <27` on PATH; facts, schema and scope operations do not. `Options.RulesLock` selects a rules lock checked before setup and evaluation. `SetupSandbox` selects the Node permission policy for setup: on by default, or explicitly off. `engine.BuildRulesLock` and `engine.WriteRulesLock` create the lock. The `diagnostics`, `runtime`, `bundle`, and `server` packages provide the diagnostic types, rule runtime, bundle operations, and JSON-RPC server respectively.
