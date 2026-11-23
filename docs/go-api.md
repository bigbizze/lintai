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

`Check` validates captured inputs in Full mode before reusing a generation. It retains eligible rule bundle texts and compiled plan templates per rule, the current fact index, and one completed result. Each check reruns rule preparation and program generation in fresh runtimes, even when its bundle and plan are reused. `Check(ctx, options)` supplies a complete request that overrides the defaults for that call.

`Refresh(ctx, options, file)` requires a complete explicit request and is read-only: it rejects suppression recording and pruning. It validates the published result's observations in Quick mode and, when the request and all eligible inputs remain unchanged, returns the file's diagnostics and unlocated workspace problems without starting rule or producer work. Otherwise it performs analysis with Quick reuse checks. Results with rule execution errors are displayed but retried on the next refresh. A failed or canceled request makes `Current` unavailable; `Current` and `Result.DiagnosticsFor` read completed data without refreshing it. `Close` is idempotent, cancels and joins active work, and prevents further requests.

Publication describes the evidence actually captured; it does not reread all inputs after analysis. Concurrent edits can yield a result containing different versions of different inputs, and a subsequent validating request normally detects the change. Setup, config transforms and runtime-loaded code may read ambient files, environment values, clocks or other state that is not observed. An unchanged `Refresh` can retain the prior result when only those inputs change; use `Check` to force preparation again. The current editor server still uses `Check` in this delivery unit.

`RuleWorkers` bounds independent rule evaluations: zero uses `GOMAXPROCS`, one runs sequentially, and a larger value permits that many active jobs. Negative values fail. Changing only this setting does not invalidate reusable work. `AssetRoot` contains `scripts/bundle-rule.mjs` and `scripts/prepare-rule.mjs`; in this repository it is `packages/client`.

A producer implements `backend.Producer`: `Describe` supplies its stable namespace, schema, implementation identity and immutable effective configuration. `Prepare` receives the exact requested base predicates and an observation mode, then returns a frozen generation. `Metadata` provides a nonempty producer-owned revision, JSON-shaped scope, analyzed files and captured `observe.Set`. `Emit` resolves relation handles on a `facts.Builder`, adds rows using producer-local entity keys, and supplies reporting anchors. Resolving a requested relation records successful production even when it has no rows. The builder validates and qualifies keys, rows and support before publishing store-ready facts. `Close` releases producer resources. See `testdata/consumer-module` for an external implementation.

`Prepare` may reuse a generation when its captured inputs and exact demand are unchanged. A generation must emit its own frozen snapshot even after a later preparation. For a fixed description, request, revision and scope, its rows, reporting support and analyzed files must be identical. Revisions may be opaque non-file tokens. `ReusableFromObservations` is false by default: set it only when observations cover every mutable input; an explicitly reusable empty set promises immutable inputs. The engine captures metadata once, and an unchanged composed identity skips emission.

The TypeScript producer accepts `typescript.Options`, including `Files`, `Excludes`, and `SourceLinks`, at construction. Its captured inputs include compiler configuration, content, resolution probes and directory membership. It validates those observations on a fresh check, including dependencies that are not primary files. `Result.AnalyzedFiles` is coverage, while `Result.WatchHints` and the server's `watchHints` are suggestions for requesting another check; a watcher is not a validity proof. The caller that resolves producer configuration should reopen affected producer instances when that configuration changes. The CLI server does this before its checks.

The public `observe` package represents captured content, filesystem operation results, directory listings, link targets, resolved paths and environment values. `observe.New` validates and detaches records; `Records` returns copies, `Union` retains conflicting expectations, and `Digest` creates an I/O-free, workspace-relative identity. `Changed(ctx, observe.Full)` reads and hashes every content record it accepts. `Quick` can reuse a certified size and modification-time hint outside a two-second racy window; a same-size edit with a deliberately restored modification time can therefore be missed until a Full check. Initial bundler observations contain no stamps, because the bundle may have consumed staged bytes. A successful live hash comparison can renew a private hint without changing the observation identity. `observe.Decode` accepts the strict `lintai.observations/1` envelope.

`Session.Facts` prepares and emits selected base relations without running rules; the `facts` command uses this path. The separate module in `testdata/consumer-module` demonstrates importing the TypeScript producer and session from another Go module.

## Suppressions and rule integrity

`Check` applies the workspace suppressions file to complete raw outcomes. `Options.SuppressionsLocation`, `SuppressAll`, `SuppressRules`, and `PruneSuppressions` control the current policy. `Result.Diagnostics` contains presented findings and `Suppressed` contains suppressed findings. `UnusedSuppressions`, `SuppressionsPath`, and `SuppressionsWritten` describe the policy result. Reading a result never replays writes.

Rule bundling, listing, locking and checking require a stable Node executable `>=26.13.1 <27` on PATH; facts, schema and scope operations do not. `Options.RulesLock` selects a rules lock checked before setup and evaluation. `SetupSandbox` selects the Node permission policy for setup: on by default, or explicitly off. `engine.BuildRulesLock` and `engine.WriteRulesLock` create the lock. The `diagnostics`, `runtime`, `bundle`, and `server` packages provide the diagnostic types, rule runtime, bundle operations, and JSON-RPC server respectively.
