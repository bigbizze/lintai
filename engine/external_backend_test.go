package engine_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/bigbizze/lintai/backend"
	"github.com/bigbizze/lintai/backend/typescript"
	"github.com/bigbizze/lintai/diagnostics"
	"github.com/bigbizze/lintai/engine"
	"github.com/bigbizze/lintai/facts"
)

type mixedFactsBackend struct {
	typescript backend.Backend
}

func (b mixedFactsBackend) ID() string { return "mixed-fixture" }

func (b mixedFactsBackend) BuildSnapshot(ctx context.Context, repoRoot, workspaceRoot string, options backend.SnapshotOptions) (facts.Snapshot, error) {
	snapshot, err := b.typescript.BuildSnapshot(ctx, repoRoot, workspaceRoot, options)
	if err != nil {
		return facts.Snapshot{}, err
	}
	snapshot.Version += "+external-fixture-v1"
	snapshot.Schema.Declarations += `

Decl external_resource(Resource, Specifier)
  descr [
	 extensional(),
	 doc("A resource reported by an external provider."),
	 arg(Resource, "Stable external resource id."),
	 arg(Specifier, "Name used by source modules.")
  ].
Decl external_boundary(Resource)
  descr [
	 extensional(),
	 doc("An external resource that is outside the allowed boundary."),
	 arg(Resource, "Stable external resource id.")
  ].
`
	snapshot.Facts = append(snapshot.Facts,
		facts.Fact{Relation: "external_resource", Values: []facts.Value{facts.String("provider:catalog"), facts.String("catalog-api")}},
		facts.Fact{Relation: "external_boundary", Values: []facts.Value{facts.String("provider:catalog")}},
	)
	return snapshot, nil
}

func TestExternalBackendComposesMixedFacts(t *testing.T) {
	repoRoot, err := filepath.Abs("..")
	if err != nil {
		t.Fatal(err)
	}
	assertTypeScriptSnapshotFacts(t, repoRoot, filepath.Join(repoRoot, "testdata/fixtures/workspace"))

	workspaceRoot := filepath.Join(repoRoot, "testdata/fixtures/external-backend/mixed-facts")
	result, err := engine.New(mixedFactsBackend{typescript: typescript.New()}).Analyze(context.Background(), engine.Options{
		RepoRoot:      repoRoot,
		AssetRoot:     filepath.Join(repoRoot, "packages/eslint-plugin"),
		WorkspaceRoot: workspaceRoot,
		RuleGlobs:     []string{"lintai-rules/**/*.ts"},
		Env:           map[string]any{},
		Severity:      diagnostics.SeverityError,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Diagnostics) != 1 {
		t.Fatalf("expected one mixed-facts diagnostic, got %+v", result.Diagnostics)
	}
	item := result.Diagnostics[0]
	if item.RuleID != "fixture.external-boundary" || item.SourceLocation == nil || item.SourceLocation.File != "src/handler.ts" {
		t.Fatalf("unexpected diagnostic from mixed facts: %+v", item)
	}
	if item.Provenance.BackendID != "mixed-fixture" || item.Provenance.SnapshotVersion == "" {
		t.Fatalf("expected mixed backend provenance, got %+v", item.Provenance)
	}
}

// Keep the TypeScript backend's generic snapshot contract covered at its
// public boundary, including location rows and deterministic formatting.
func assertTypeScriptSnapshotFacts(t *testing.T, repoRoot, workspaceRoot string) {
	t.Helper()
	build := func() string {
		snapshot, err := typescript.New().BuildSnapshot(context.Background(), repoRoot, workspaceRoot, backend.SnapshotOptions{})
		if err != nil {
			t.Fatal(err)
		}
		if err := facts.Validate(snapshot); err != nil {
			t.Fatal(err)
		}
		locations := make(map[string]struct{})
		for _, item := range snapshot.Facts {
			if item.Relation == facts.LocationRelation && len(item.Values) == 6 && item.Values[0].Kind == facts.StringKind {
				locations[item.Values[0].String] = struct{}{}
			}
		}
		entityArgs := map[string][]int{
			"module":             {0},
			"fn":                 {0},
			"import_edge":        {0},
			"call_edge":          {0, 1, 2},
			"external_call":      {0, 1},
			"type_ref":           {0},
			"meta_access":        {0},
			"ambient_use":        {0},
			"module_directive":   {0},
			"module_parse_error": {0},
			"site_in_fn":         {0, 1},
		}
		rows := make(map[string]int, len(entityArgs))
		for _, item := range snapshot.Facts {
			rows[item.Relation]++
			for _, position := range entityArgs[item.Relation] {
				if position >= len(item.Values) || item.Values[position].Kind != facts.StringKind {
					t.Errorf("%s has an invalid entity id at argument %d", item.Relation, position)
					continue
				}
				if _, ok := locations[item.Values[position].String]; !ok {
					t.Errorf("%s entity %q has no location row", item.Relation, item.Values[position].String)
				}
			}
		}
		for relation := range entityArgs {
			if rows[relation] == 0 {
				t.Errorf("fixture workspace has no %s facts", relation)
			}
		}
		text, err := facts.Format(snapshot, nil)
		if err != nil {
			t.Fatal(err)
		}
		for _, want := range []string{
			`import_symbol_type_only("src/server/handler.ts::import::./config","Config").`,
			`type_ref_target("src/server/handler.ts::type_ref::Config","src/server/config.ts").`,
			`site_in_fn("src/server/config.ts::access::import.meta.env","src/server/config.ts::function::mode").`,
			`import_edge("src/server/handler.ts::import::server-only","src/server/handler.ts","server-only","server-only").`,
		} {
			if !strings.Contains(text, want) {
				t.Errorf("snapshot is missing fact %s", want)
			}
		}
		return text
	}

	first := build()
	if second := build(); first != second {
		t.Fatal("two public backend builds produced different facts")
	}
}

type genericFactsBackend struct{}

func (genericFactsBackend) ID() string { return "generic-fixture" }

func (genericFactsBackend) BuildSnapshot(context.Context, string, string, backend.SnapshotOptions) (facts.Snapshot, error) {
	return facts.Snapshot{
		Version: "generic-fixture-v1",
		Schema: facts.Schema{Declarations: `
Decl location(Entity, File, StartLine, StartColumn, EndLine, EndColumn)
  descr [extensional()].
Decl generic_resource(Resource)
  descr [extensional()].
`},
		Facts: []facts.Fact{
			{Relation: facts.LocationRelation, Values: []facts.Value{facts.String("generic:item"), facts.String("src/item.go"), facts.Number(1), facts.Number(1), facts.Number(1), facts.Number(2)}},
			{Relation: "generic_resource", Values: []facts.Value{facts.String("generic:item")}},
		},
	}, nil
}

func TestGenericBackendEvaluatesRulesWithoutGlobDeclaration(t *testing.T) {
	repoRoot, err := filepath.Abs("..")
	if err != nil {
		t.Fatal(err)
	}
	workspaceRoot := t.TempDir()
	rulePath := filepath.Join(workspaceRoot, "lintai-rules", "generic.ts")
	if err := os.MkdirAll(filepath.Dir(rulePath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(rulePath, []byte(`import { dl, rule } from "@lintai/sdk";
export default rule("fixture.generic-backend").version(1)
	.program(dl`+"`"+`Decl violation(Resource). violation(Resource) :- generic_resource(Resource).`+"`"+`)
	.report("violation").message((row) => String(row.Resource));
`), 0o644); err != nil {
		t.Fatal(err)
	}

	result, err := engine.New(genericFactsBackend{}).Analyze(context.Background(), engine.Options{
		RepoRoot:      repoRoot,
		AssetRoot:     filepath.Join(repoRoot, "packages/eslint-plugin"),
		WorkspaceRoot: workspaceRoot,
		RuleGlobs:     []string{"lintai-rules/**/*.ts"},
		Env:           map[string]any{},
		Severity:      diagnostics.SeverityError,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Diagnostics) != 1 {
		t.Fatalf("expected one generic-backend diagnostic, got %+v", result.Diagnostics)
	}
	item := result.Diagnostics[0]
	if item.DiagnosticKind != diagnostics.KindArchitectureViolation || item.RuleID != "fixture.generic-backend" {
		t.Fatalf("expected the generic rule to evaluate successfully, got %+v", item)
	}
	if item.SourceLocation == nil || item.SourceLocation.File != "src/item.go" {
		t.Fatalf("expected the generic backend location, got %+v", item.SourceLocation)
	}
}
