# Go API

## Bulk suppressions

`Engine.Analyze` automatically applies workspace-root `lintai-suppressions.json`. Set `Options.SuppressionsLocation` to override it, `SuppressAll` or `SuppressRules []string` to record violations, `PruneSuppressions` to lower stale counts, and `PassOnUnprunedSuppressions` to omit unused-entry diagnostics. Missing explicit locations fail except when recording. Writing modes are mutually exclusive.

`Result.Diagnostics` contains reported findings; `Suppressed` contains suppressed findings. `UnusedSuppressions` lists stale allowances even when their diagnostics are omitted. `SuppressionsPath` and `SuppressionsWritten` describe the file operation. Only error-severity architecture violations with locations can be suppressed. Provide `location` facts for each analyzed file so unused-entry scope is accurate, including files with no violations.

The public `suppressions` package exposes `Load`, `Parse`, `Write`, `Apply`, `Record`, `Prune`, and `Limit`. `Apply(File, []diagnostics.Diagnostic, Scope)` returns reported/suppressed diagnostics, unused entries and exceeded allowances. `Record` takes a rule list (nil means all). `Prune` additionally accepts an optional file-existence callback; nil preserves unanalyzed files. `Scope` identifies analyzed files, error-severity rules that completed, known rule IDs and whether unknown rules can be classified as unused. See [the specification](../specs/bulk-suppressions.md).

The reusable engine is exported from the `github.com/bigbizze/lintai` module.
Backends implement `backend.Backend` and return `facts.Snapshot`, which carries
a snapshot version, relation declarations, prelude, and typed base facts.

```go
import (
	"context"

	"github.com/bigbizze/lintai/backend"
	"github.com/bigbizze/lintai/engine"
	"github.com/bigbizze/lintai/facts"
)

type CatalogBackend struct{}

func (CatalogBackend) ID() string { return "catalog" }

func (CatalogBackend) BuildSnapshot(
	ctx context.Context,
	repoRoot, workspaceRoot string,
	options backend.SnapshotOptions,
) (facts.Snapshot, error) {
	return facts.Snapshot{
		Version: "catalog-snapshot-42",
		Schema: facts.Schema{
			Declarations: `Decl resource(Id)
  descr [extensional(), arg(Id, "Stable resource id.")].
Decl location(Entity, File, StartLine, StartColumn, EndLine, EndColumn)
  descr [extensional(), arg(Entity, "Stable entity id."), arg(File, "Source file."), arg(StartLine, "Start line."), arg(StartColumn, "Start column."), arg(EndLine, "End line."), arg(EndColumn, "End column.")].`,
			Prelude: "",
		},
		Facts: []facts.Fact{{
			Relation: "resource",
			Values:   []facts.Value{facts.String("catalog:orders")},
		}},
	}, nil
}

func Analyze(ctx context.Context, repoRoot, assetRoot, workspaceRoot string) error {
	runner := engine.New(CatalogBackend{})
	_, err := runner.Analyze(ctx, engine.Options{
		RepoRoot:      repoRoot,
		AssetRoot:     assetRoot,
		WorkspaceRoot: workspaceRoot,
		RuleGlobs:     []string{"lintai-rules/**/*.ts"},
	})
	return err
}
```

Use `facts.String`, `facts.Number`, and `facts.Name` to distinguish strings,
numbers, and Mangle name constants in tuples. The engine validates the schema,
prelude, snapshot version, and fact arities before evaluating rules.
Every backend schema must declare `location(Entity, File, StartLine,
StartColumn, EndLine, EndColumn)`; report anchors use its string entity id to
create the diagnostic source location.

`AssetRoot` is the directory that contains `scripts/bundle-rule.mjs` and
`scripts/prepare-rule.mjs`, which bundle and run the rules in Node. In an npm
install it is `node_modules/@lintai/client` (with pnpm,
`node_modules/.pnpm/node_modules/@lintai/client`); in this repository it is
`packages/client`.

`Version` identifies the snapshot in diagnostic provenance and in the server's
analysis result. The engine does not cache facts between runs: it builds the
fact store from `Facts` on every `Analyze` call.

The TypeScript implementation is available at `backend/typescript`. It keeps
the TypeScript compiler extraction and conversion from its compiler model to
facts inside that backend. A composite backend can wrap it, append declarations
and facts from another provider, and return the resulting snapshot; the
`testdata/fixtures/external-backend/mixed-facts` fixture exercises that path
with synthetic provider data.

`backend/typescript` contains a copy of the TypeScript 7 compiler, at the
`microsoft/TypeScript` commit in `internal/tsgo/VERSION`. A consuming module
needs only a `require` of `github.com/bigbizze/lintai`; there is no `replace`
directive to copy. Importing `backend/typescript` adds about one minute to a
clean build and about 60 MB to the binary (see "Build and extraction cost" in
`docs/install.md`).

The separate module in `testdata/consumer-module` demonstrates this. From that
directory, run `go run .`; its `go.mod` replaces `github.com/bigbizze/lintai`
with this checkout only, then imports both `backend/typescript` and `engine` to
analyze the TypeScript fixture workspace.

Other reusable packages are:

- `diagnostics` for the stable diagnostic and source-location JSON types.
- `runtime` for loading and evaluating a bundled rule's pure phase.
- `bundle` for building rule bundles and running authoring setup.
- `server` for the JSON-RPC analysis server; `jsonrpc` exposes its framing
  types and helpers.

`SnapshotOptions.Excludes` contains doublestar globs relative to the workspace
root. `SnapshotOptions.Files` selects `backend.FileScopeTsconfig` (the default)
or `backend.FileScopeWalk`. `SnapshotOptions.SourceLinks` is the path of an
optional source-links JSON file; see "Source links and input paths".

`engine.Options` has two fields for rule integrity (see `docs/trust.md`):

- `RulesLock` is the path of a lock file that `lintai lock` wrote, relative to
  the workspace root. When it is set, the engine checks each bundled rule
  against the lock before `setup()`. A mismatch is a rule error with phase
  `integrity`, and that rule does not run.
- `SetupSandbox` is `bundle.SetupSandboxAuto` (the default),
  `bundle.SetupSandboxOn` or `bundle.SetupSandboxOff`. It selects whether
  `setup()` runs under the Node permission model. With `auto` on an old Node,
  `Result.Warnings` contains a warning; print it to the user.

`engine.BuildRulesLock(ctx, options)` bundles the rules of `options` and
returns the lock; `engine.WriteRulesLock` writes it. `bundle.BuildAll` takes
the workspace root, so that the input paths that each rule hash covers are
workspace-relative.

The CLI remains in `cmd/lintai`; its flags and exit codes are documented in
`docs/api.md`.

## Source links and input paths

When a workspace package is imported through generated `.d.ts` files, a source-links file can tell lintai which source entry belongs to each checked entry. The option has the same meaning on every surface:

| Surface | Field |
| --- | --- |
| CLI | `--source-links` |
| Go backend snapshot | `backend.SnapshotOptions.SourceLinks` |
| Go engine | `engine.Options.SourceLinks` |
| Server initialize JSON | `sourceLinks` |
| TypeScript client and linter plugins | `LintaiOptions.sourceLinks` |

```go
result, err := engine.New(typescript.New()).Analyze(ctx, engine.Options{
	RepoRoot:      repoRoot,
	AssetRoot:     assetRoot,
	WorkspaceRoot: workspaceRoot,
	RuleGlobs:     []string{"lintai-rules/**/*.ts"},
	SourceLinks:   "lintai-source-links.json",
})
```

A relative path is resolved from the workspace root, and an empty string means that there is no file. The TypeScript backend reads and checks the file each time it builds a snapshot. If the file is broken, `BuildSnapshot` and `Engine.Analyze` return an error whose message starts with `invalid --source-links:`, and the server turns that error into an invalid-params response. The file format is under "Source links" in the [CLI reference](api.md#cli-reference-and-caveats). The facts are in [Source associations](api.md#source-associations), and the order in which lintai looks for source is in [Source resolution order](api.md#source-resolution-order).

Source links create a new problem for long-running clients. The facts now depend on files that are not analyzed modules, such as a declaration map, and on files that do not exist yet. If someone later builds the package and a `.d.ts.map` file appears, the facts can change even though no source file changed. To handle this, the snapshot reports these extra files. `facts.Snapshot.InputPaths` lists the source-links file, the declaration maps, the exact map paths that lintai tried and did not find, the checked and source entry files, the auxiliary tsconfig files and the files they extend or reference, and the resolution candidates, including missing directories such as `node_modules` or a package folder. It also includes configs and dependencies consulted before an optional source association failed, so correcting one can refresh the result. The TypeScript backend also hashes the path, presence and file content of these inputs into `Snapshot.Version`, so the version changes when one of them changes. `engine.Result.InputPaths` and the server's `AnalysisResult.inputPaths` pass the list on. In `serve` mode the TypeScript client watches each input’s parent directory, or its nearest existing parent when the path does not exist yet, without watching subfolders, and drops its diagnostics when one of them changes. It also watches the parent directories for replacement, so it follows a rebuilt output directory instead of continuing to watch the old one.

A custom backend can leave `InputPaths` empty. If its facts depend on files other than the analyzed modules, list those files there and include their state in `Version`, so that clients know when to refresh.
