package engine_test

import (
	"context"
	"path/filepath"
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

func (b mixedFactsBackend) BuildSnapshot(ctx context.Context, repoRoot, workspaceRoot string, excludes []string) (facts.Snapshot, error) {
	snapshot, err := b.typescript.BuildSnapshot(ctx, repoRoot, workspaceRoot, excludes)
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
