package typescript

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/bigbizze/lintai/backend"
	"github.com/bigbizze/lintai/facts"
	"github.com/bigbizze/lintai/internal/tsgo/lintaiapi"
	"github.com/bigbizze/lintai/observe"
)

func TestGenerationInvalidatesCapturedContentAndScope(t *testing.T) {
	root := t.TempDir()
	file := writeWorkspaceFile(t, root, "src/main.ts", "export const value = 1;")
	producer := New(Options{Files: FileScopeWalk})
	first, err := producer.buildTestGeneration(context.Background(), root, root)
	if err != nil {
		t.Fatal(err)
	}
	rootPath, err := filepath.Abs(root)
	if err != nil {
		t.Fatal(err)
	}
	digest, err := first.observations.Digest(filepath.Clean(rootPath))
	if err != nil || first.inputRevision != digest {
		t.Fatalf("input revision must equal captured observation digest: revision=%q digest=%q err=%v", first.inputRevision, digest, err)
	}
	again, err := producer.buildTestGeneration(context.Background(), root, root)
	if err != nil || again != first {
		t.Fatalf("unchanged generation not reused: %v", err)
	}
	info, err := os.Stat(file)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(file, []byte("export const value = 2;"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(file, info.ModTime(), info.ModTime()); err != nil {
		t.Fatal(err)
	}
	if changed, err := first.observations.Changed(context.Background(), observe.Full); err != nil || changed == nil {
		t.Fatalf("content change: %v %v", changed, err)
	}
	second, err := producer.buildTestGeneration(context.Background(), root, root)
	if err != nil || second == first || second.inputRevision == first.inputRevision {
		t.Fatalf("changed generation: %v", err)
	}
	late := writeWorkspaceFile(t, root, "src/late.ts", "export const late = 1;")
	if changed, err := second.observations.Changed(context.Background(), observe.Full); err != nil || changed == nil {
		t.Fatalf("new source: %v %v", changed, err)
	}
	third, err := producer.buildTestGeneration(context.Background(), root, root)
	if err != nil || third == second {
		t.Fatalf("new source did not invalidate generation: %v", err)
	}
	if err := os.Remove(late); err != nil {
		t.Fatal(err)
	}
	if changed, err := third.observations.Changed(context.Background(), observe.Full); err != nil || changed == nil {
		t.Fatalf("deleted source: %v %v", changed, err)
	}
}

func TestCompilerObservationValidatesRawBytes(t *testing.T) {
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(root, "source.ts")
	raw := []byte{0xef, 0xbb, 0xbf, 'e', 'x', 'p', 'o', 'r', 't', ' ', '{', '}', ';'}
	if err := os.WriteFile(file, raw, 0o600); err != nil {
		t.Fatal(err)
	}
	snapshot, err := lintaiapi.BuildSnapshot(context.Background(), lintaiapi.BuildSnapshotRequest{WorkspaceRoot: root, Files: []string{file}})
	if err != nil {
		t.Fatal(err)
	}
	set, err := compilerObservations(snapshot.CompilerInputs)
	if err != nil {
		t.Fatal(err)
	}
	if changed, err := set.Changed(context.Background(), observe.Full); err != nil || changed != nil {
		t.Fatalf("unchanged raw bytes: %v %v", changed, err)
	}
	if err := os.WriteFile(file, raw[3:], 0o600); err != nil {
		t.Fatal(err)
	}
	if changed, err := set.Changed(context.Background(), observe.Full); err != nil || changed == nil {
		t.Fatalf("changed raw encoding was not detected: %v %v", changed, err)
	}
}

func TestAbsentTsconfigIncludeRootIsObservedAndInvalidated(t *testing.T) {
	parent := t.TempDir()
	root := filepath.Join(parent, "workspace")
	writeWorkspaceFile(t, root, "tsconfig.json", `{"compilerOptions":{"module":"preserve","moduleResolution":"bundler"},"include":["src/**/*.ts","../generated/**/*.ts"]}`)
	source := writeWorkspaceFile(t, root, "src/app.ts", `export const value = 1;`)
	generatedRoot := filepath.Join(parent, "generated")
	snapshot, err := lintaiapi.BuildSnapshot(context.Background(), lintaiapi.BuildSnapshotRequest{
		WorkspaceRoot: root, Files: []string{source}, TsconfigFiles: true,
	})
	if err != nil {
		t.Fatalf("collect with absent include root: %v", err)
	}
	if !slices.ContainsFunc(snapshot.Modules, func(module lintaiapi.Module) bool { return module.Path == "src/app.ts" }) {
		t.Fatalf("source module was lost with absent include root: %+v", snapshot.Modules)
	}
	observations, err := compilerObservations(snapshot.CompilerInputs)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.ContainsFunc(observations.Records(), func(record observe.Record) bool {
		return record.Kind == observe.State && record.Path == generatedRoot && record.Op == observe.ReadDirectory && record.Value == observe.Missing
	}) {
		t.Fatalf("missing include root was not captured: %+v", observations.Records())
	}
	if changed, err := observations.Changed(context.Background(), observe.Full); err != nil || changed != nil {
		t.Fatalf("unchanged observations: %v %v", changed, err)
	}
	if err := os.Mkdir(generatedRoot, 0o755); err != nil {
		t.Fatal(err)
	}
	if changed, err := observations.Changed(context.Background(), observe.Full); err != nil || changed == nil {
		t.Fatalf("created include root did not invalidate observations: %v %v", changed, err)
	}
}

// Demand changes require extraction, and every returned handle keeps its own snapshot.
func TestGenerationDemandAndFrozenEmission(t *testing.T) {
	root := t.TempDir()
	file := writeWorkspaceFile(t, root, "src/main.ts", "export function before() {}")
	producer := New(Options{Files: FileScopeWalk})
	description, err := producer.Describe()
	if err != nil {
		t.Fatal(err)
	}
	descriptor, err := facts.ValidateDescription(description)
	if err != nil {
		t.Fatal(err)
	}
	module := facts.Predicate{Name: "ts.module", Arity: 1}
	fn := facts.Predicate{Name: "ts.fn", Arity: 3}
	// The exported description is detached. Direct preparations still
	// validate mode, predicate name and arity, and duplicate demand.
	description.BaseRelations[0].Predicate = facts.Predicate{Name: "mutated", Arity: 1}
	for _, invalid := range []struct {
		mode   observe.Mode
		demand []facts.Predicate
	}{
		{observe.Mode(255), []facts.Predicate{module}},
		{observe.Full, []facts.Predicate{{Name: "ts.module", Arity: 2}}},
		{observe.Full, []facts.Predicate{module, module}},
		{observe.Full, []facts.Predicate{description.BaseRelations[0].Predicate}},
	} {
		if _, err := producer.Prepare(context.Background(), backend.CollectionRequest{Predicates: invalid.demand}, invalid.mode); err == nil {
			t.Fatalf("accepted invalid request %+v", invalid)
		}
	}
	request := backend.CollectionRequest{RepoRoot: root, WorkspaceRoot: root, Predicates: []facts.Predicate{module}}
	first, err := producer.Prepare(context.Background(), request, observe.Full)
	if err != nil {
		t.Fatal(err)
	}
	request.Predicates = []facts.Predicate{module, fn}
	second, err := producer.Prepare(context.Background(), request, observe.Full)
	if err != nil {
		t.Fatal(err)
	}
	if first == second {
		t.Fatal("changed demand reused the prior extraction")
	}
	request.Predicates = []facts.Predicate{fn, module}
	same, err := producer.Prepare(context.Background(), request, observe.Full)
	if err != nil || same != second {
		t.Fatalf("order changed generation: %v", err)
	}
	emit := func(g backend.Generation) string {
		t.Helper()
		builder, err := facts.NewBuilder(descriptor, []facts.Predicate{fn, module})
		if err != nil {
			t.Fatal(err)
		}
		if err := g.Emit(context.Background(), builder); err != nil {
			t.Fatal(err)
		}
		output, err := builder.Finish()
		if err != nil {
			t.Fatal(err)
		}
		atoms := output.Atoms(fn)
		if len(atoms) != 1 {
			t.Fatalf("expected one function: %v", atoms)
		}
		return atoms[0].String()
	}
	before := emit(second)
	if err := os.WriteFile(file, []byte("export function after() {}"), 0o644); err != nil {
		t.Fatal(err)
	}
	third, err := producer.Prepare(context.Background(), request, observe.Full)
	if err != nil || third == second {
		t.Fatalf("changed input did not produce a generation: %v", err)
	}
	if got := emit(second); got != before {
		t.Fatalf("previous generation changed: %s != %s", got, before)
	}
	if got := emit(third); got == before || !strings.Contains(got, "after") {
		t.Fatalf("new generation did not expose changed function: %s", got)
	}
}
