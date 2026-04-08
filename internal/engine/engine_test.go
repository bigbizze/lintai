package engine

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/bigbizze/lintai/internal/backend/typescript"
	"github.com/bigbizze/lintai/internal/diagnostics"
)

func TestEngineRunsFixtureRules(t *testing.T) {
	repoRoot, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	runner := New(typescript.New())
	items, err := runner.Run(context.Background(), Options{
		RepoRoot:      repoRoot,
		WorkspaceRoot: filepath.Join(repoRoot, "testdata/fixtures/workspace"),
		RuleGlobs:     []string{"testdata/fixtures/rules/*.ts"},
		Env:           map[string]any{},
		Severity:      diagnostics.SeverityError,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(items) < 3 {
		t.Fatalf("expected at least 3 diagnostics, got %d", len(items))
	}
}
