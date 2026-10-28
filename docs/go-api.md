# Go API

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
	RepoRoot: repoRoot, AssetRoot: assetRoot, WorkspaceRoot: workspaceRoot,
	RuleGlobs: []string{"lintai-rules/**/*.ts"},
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

The TypeScript implementation is available at `backend/typescript`. It keeps
the TypeScript compiler extraction and conversion from its compiler model to
facts inside that backend. A composite backend can wrap it, append declarations
and facts from another provider, and return the resulting snapshot; the
`testdata/fixtures/external-backend/mixed-facts` fixture exercises that path
with synthetic provider data.

Because Go applies `replace` directives only from the main module, a consuming
module that imports `backend/typescript` must include the same compiler fork
replacement used here:

```go
replace github.com/microsoft/TypeScript/tsc => github.com/bigbizze/typescript-go v0.0.0-20261028214434-1b2c2fa7c3bb
```

The separate module in `testdata/consumer-module` demonstrates this
requirement. From that directory, run `go run .`; its `go.mod` replaces
`github.com/bigbizze/lintai` with this checkout and pins the compiler fork
above, then imports both `backend/typescript` and `engine` to analyze the
TypeScript fixture workspace.

Other reusable packages are:

- `diagnostics` for the stable diagnostic and source-location JSON types.
- `runtime` for loading and evaluating a bundled rule's pure phase.
- `bundle` for building rule bundles and running authoring setup.
- `server` for the JSON-RPC analysis server; `jsonrpc` exposes its framing
  types and helpers.

`SnapshotOptions.Excludes` contains doublestar globs relative to the workspace
root. `SnapshotOptions.Files` selects `backend.FileScopeTsconfig` (the default)
or `backend.FileScopeWalk`.

The CLI remains in `cmd/lintai`; its flags and exit codes are documented in
`docs/api.md`.
