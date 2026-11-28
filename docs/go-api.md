# Go API

The `engine` package provides a retained analysis session. Construct producer instances with their own settings, open a session with its configuration, and close it when finished. The configuration is bound once; every operation uses it:

```go
import (
    "context"

    "github.com/bigbizze/lintai/backend/typescript"
    "github.com/bigbizze/lintai/engine"
)

func check(ctx context.Context, workspaceRoot string) error {
    producer := typescript.New(typescript.Options{Files: typescript.FileScopeTsconfig})
    session, err := engine.Open(engine.Config{
        WorkspaceRoot: workspaceRoot,
        RuleWorkers:   2,
    }, producer)
    if err != nil {
        _ = producer.Close()
        return err
    }
    defer session.Close()

    result, err := session.Check(ctx)
    if err != nil {
        return err
    }
    _ = result.DiagnosticsFor("src/example.ts")
    return nil
}
```

Opening validates and binds each producer description once. The caller owns all supplied producers until construction succeeds. A successful session closes each bound producer once.

Migrating from the previous API: `engine.Options` with per-call options becomes one `engine.Config` passed to `Open`. `RepoRoot` is gone. `Check(ctx, options)` becomes `Check(ctx)`. A suppression write becomes `UpdateSuppressions(ctx, engine.SuppressionUpdate{All: true})`. `Refresh(ctx, options, file)` becomes `RefreshFiles(ctx, []string{file})`, and `RefreshAll(ctx, options)` becomes `RefreshAll(ctx)`.

`RefreshFiles(ctx, files)` and `RefreshAll(ctx)` return a `FilesResult` holding ordered `FileGroup` values, each with `File`, `SourceDigest` and `Diagnostics`, plus workspace problems, the snapshot version, the physical root resolved for that analysis and an alias map. Explicit requests preserve filename spelling, order and duplicates. `RefreshAll` returns each canonical file once, in normalized order, with accepted alternative names in `Aliases`. An empty digest means source identity is unavailable. A producer can supply `GenerationMetadata.SourceDigests` keyed by filename, using lowercase hexadecimal SHA-256 of the UTF-8 encoding of the analyzed logical text with one leading BOM removed. File projections use that captured identity even after a disk edit.

`Check` reads the published pack and validates producer inputs in Full mode. Each actual analysis initializes a fresh VM per inspected rule. Eligible rules parse configuration, generate their program and facts, and render messages in that VM. The session can retain a complete immutable compiled plan for a matching key, the current fact index and one completed publication.

The refresh methods are read-only. Each call validates once, and all its projections come from the same completed result. Returned results are detached from the retained publication. An empty list still validates and can return workspace problems. A later call validates again. Both validate the published result's observations in Quick mode and, when all eligible inputs remain unchanged, return the requested groups and unlocated workspace problems without starting rule or producer work. Otherwise they perform analysis with Quick reuse checks. Results with rule execution errors are displayed but retried on the next refresh. A failed or canceled request makes `Current` unavailable; `Current` and `Result.DiagnosticsFor` read completed data without refreshing it. `Close` is idempotent, cancels and joins active work, and prevents further requests.

A publication records the inputs used during analysis. Concurrent edits can yield different versions of different inputs within a single publication. The next validating request checks the captured observations. Edit application sources directly. After editing executable rules or their static data, run `lintai build-rules` to publish the change.

`RuleWorkers` bounds independent rule evaluations: zero uses `GOMAXPROCS`, one runs sequentially, and a larger value permits that many active jobs. Negative values fail. Changing only this setting does not invalidate reusable work.

A producer implements `backend.Producer`: `Describe` supplies its stable namespace, schema, implementation identity and immutable effective configuration. `Prepare` receives the exact requested base predicates and an observation mode, then returns a frozen generation. `Metadata` provides a nonempty producer-owned revision, JSON-shaped scope, analyzed files and captured `observe.Set`. `Emit` resolves relation handles on a `facts.Builder`, adds rows using producer-local entity keys, and supplies reporting anchors. Resolving a requested relation records successful production even when it has no rows. The builder validates and qualifies keys, rows and support before publishing store-ready facts. `Close` releases producer resources. See `testdata/consumer-module` for an external implementation.

`Prepare` may reuse a generation when its captured inputs and exact demand are unchanged. A generation must emit its own frozen snapshot even after a later preparation. For a fixed description, request, revision and scope, its rows, reporting support and analyzed files must be identical. Revisions may be opaque non-file tokens. `ReusableFromObservations` is false by default: set it only when observations cover every mutable input; an explicitly reusable empty set promises immutable inputs. The engine captures metadata once and composes the validated producer catalog, exact requested relations, revision and scope into the fact identity; an unchanged composed identity skips emission. An observation digest alone does not establish that these other inputs match.

The TypeScript producer accepts `typescript.Options`, including `Files`, `Excludes`, and `SourceLinks`, at construction. Its input revision is the workspace-normalized digest of the combined compiler and source-selection observations, including compiler configuration, content, resolution probes and directory membership. Effective settings belong to the producer descriptor, while selected candidates, exact and primary files, dependencies, contexts and mode belong to the generation scope; both participate separately in the composed fact identity. The producer validates its observations on a fresh check, including dependencies that are not primary files. `Result.AnalyzedFiles` is coverage. A caller that resolves producer configuration must replace affected producer instances when effective settings change. The CLI server owns configuration observations and does this before file requests and forced checks.

The public `observe` package represents captured content, filesystem operation results, directory listings, link targets, resolved paths and environment values. `observe.New` validates and detaches records; `Records` returns copies, `Union` retains conflicting expectations, and `Digest` creates an I/O-free, workspace-relative identity. `Changed(ctx, observe.Full)` reads and hashes every content record it accepts. `Quick` can reuse a certified size and modification-time hint outside a two-second racy window; a same-size edit with a deliberately restored modification time can therefore be missed until a Full check. The pack observation records the digest of the exact bytes read by the operation. A successful live hash comparison can renew a private hint without changing the observation identity.

`Session.Facts` prepares and emits selected base relations without running rules; the `facts` command uses this path. The separate module in `testdata/consumer-module` demonstrates importing the TypeScript producer and session from another Go module.

## Suppressions and rule integrity

`Check` applies the workspace suppressions file to complete raw outcomes. `Config.SuppressionsLocation` selects the file. `UpdateSuppressions` records (`All`, `Rules`) or prunes (`Prune`) allowances once. `Result.Diagnostics` contains presented findings and `Suppressed` contains suppressed findings. `UnusedSuppressions`, `SuppressionsPath`, and `SuppressionsWritten` describe the policy result. Reading a result never replays writes.

Set `Config.RulesLock` to enforce the approved pack bytes, effective policy and suppression ceiling before any rule is initialized. Go callers approve deliberately with `session.BuildRulesLock(ctx, target)` and `engine.WriteRulesLock(ctx, path, lock)`. The native build, approval, inspection and analysis paths run without Node. See the [trust model](trust.md).

## Serve protocol

`lintai serve` processes Content-Length framed JSON-RPC requests sequentially. Initialize with `{"protocolVersion":4,"workspaceRoot":"/project"}`. Both peers require version 4. Initialization supplies the session settings once, and each subsequent projection validates the workspace configuration and publication once.

Call `diagnostics/files` with `{"files":["src/example.ts"]}` or `{"files":"all"}`. Arrays preserve the requested spelling, order, duplicates and empty groups. An empty array returns workspace problems without groups. The all-files selector returns each canonical file once, with accepted alternative names in `aliases`. A result has this shape:

```ts
type Finding = {
  ruleId: string; assertionId: string; severity: "error" | "warning";
  message: string; range?: [number, number, number, number];
};
type HostResult = {
  snapshotVersion: string; problems: string[];
  physicalRoot: string; aliases: Record<string, string>;
  files: Array<{ file: string; sourceDigest: string; findings: Finding[] }>;
};
```

A range is start line, start column, end line, end column, one-based in UTF-16 units. `physicalRoot` is the workspace root the producers resolved for that analysis; an empty value authorizes no physical alias lookup. An empty digest means source identity is unavailable. The digest is lowercase hex SHA-256 over the analyzed logical text encoded in UTF-8 after removing one leading BOM. Native and Go diagnostics keep snapshot hashes, producers, anchors and explanations; the host payload omits them.

A custom `server.Analyzer` implements `RefreshFiles`, `RefreshAll` and `Close`, returning an `engine.FilesResult`. A recoverable analysis failure returns JSON-RPC error `-32010`. `shutdown` responds with `{}` and closes the session.
