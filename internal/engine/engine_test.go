package engine

import (
	"context"
	"os"
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

func TestEngineContinuesWhenOneRuleFailsDuringSetup(t *testing.T) {
	repoRoot, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	goodRule := filepath.Join(repoRoot, "testdata/fixtures/rules/pure-no-effects.ts")
	brokenRule := filepath.Join(t.TempDir(), "broken-setup.ts")
	if err := os.WriteFile(brokenRule, []byte(`
import { functions, rule } from "@lintai/sdk";

export default rule("arch.broken-setup")
	.version(3)
	.setup(() => {
		throw new Error("setup exploded");
	})
	.assert(() => functions().in("src/**").isEmpty())
	.message(() => "broken");
`), 0o644); err != nil {
		t.Fatal(err)
	}

	runner := New(typescript.New())
	items, err := runner.Run(context.Background(), Options{
		RepoRoot:      repoRoot,
		WorkspaceRoot: filepath.Join(repoRoot, "testdata/fixtures/workspace"),
		RuleGlobs:     []string{goodRule, brokenRule},
		Env:           map[string]any{},
		Severity:      diagnostics.SeverityError,
	})
	if err != nil {
		t.Fatal(err)
	}

	foundArchitectureViolation := false
	foundSetupFailure := false
	for _, item := range items {
		if item.DiagnosticKind == diagnostics.KindArchitectureViolation {
			foundArchitectureViolation = true
		}
		if item.DiagnosticKind == diagnostics.KindRuleExecutionError && item.RuleID == "arch.broken-setup" && item.Phase == "setup" {
			foundSetupFailure = true
		}
	}
	if !foundArchitectureViolation {
		t.Fatalf("expected unaffected rules to still report violations, got %+v", items)
	}
	if !foundSetupFailure {
		t.Fatalf("expected setup failure diagnostic, got %+v", items)
	}
}
