package bundle

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestBuildAllAndPrepareAllFixtureRules(t *testing.T) {
	t.Parallel()

	repoRoot := repoRootForBundleTest(t)
	workspaceRoot := filepath.Join(repoRoot, "testdata/fixtures/workspace")
	rulePaths := []string{
		filepath.Join(repoRoot, "testdata/fixtures/rules/pure-no-effects.ts"),
		filepath.Join(repoRoot, "testdata/fixtures/rules/pure-no-service-imports.ts"),
	}

	artifacts, buildFailures, err := BuildAll(context.Background(), repoRoot, rulePaths)
	if err != nil {
		t.Fatal(err)
	}
	if len(buildFailures) != 0 {
		t.Fatalf("expected no build failures, got %#v", buildFailures)
	}
	if len(artifacts) != len(rulePaths) {
		t.Fatalf("expected %d build results, got %d", len(rulePaths), len(artifacts))
	}

	prepared, prepareFailures, err := PrepareAll(context.Background(), repoRoot, workspaceRoot, artifacts, map[string]any{})
	if err != nil {
		t.Fatal(err)
	}
	if len(prepareFailures) != 0 {
		t.Fatalf("expected no prepare failures, got %#v", prepareFailures)
	}
	if len(prepared) != len(rulePaths) {
		t.Fatalf("expected %d prepare results, got %d", len(rulePaths), len(prepared))
	}

	effects := prepared[rulePaths[0]]
	if effects.RuleID != "arch.pure-no-effects" {
		t.Fatalf("unexpected rule id %q", effects.RuleID)
	}
	effectsEnv, ok := effects.Env.(map[string]any)
	if !ok {
		t.Fatalf("expected env object, got %#v", effects.Env)
	}
	if effectsEnv["pureDir"] != "src/pure" {
		t.Fatalf("expected pureDir default, got %#v", effectsEnv["pureDir"])
	}

	imports := prepared[rulePaths[1]]
	if imports.RuleID != "arch.pure-no-service-imports" {
		t.Fatalf("unexpected rule id %q", imports.RuleID)
	}
	importsEnv, ok := imports.Env.(map[string]any)
	if !ok {
		t.Fatalf("expected env object, got %#v", imports.Env)
	}
	if importsEnv["serviceDir"] != "src/services" {
		t.Fatalf("expected serviceDir default, got %#v", importsEnv["serviceDir"])
	}
}

func TestBuildAllReportsPerRuleErrors(t *testing.T) {
	t.Parallel()

	repoRoot := repoRootForBundleTest(t)
	validRule := filepath.Join(repoRoot, "testdata/fixtures/rules/pure-no-effects.ts")
	brokenRule := writeRuleFile(t, "broken-build.ts", "export default ;\n")

	artifacts, buildFailures, err := BuildAll(context.Background(), repoRoot, []string{validRule, brokenRule})
	if err != nil {
		t.Fatal(err)
	}
	if len(artifacts) != 1 {
		t.Fatalf("expected 1 successful artifact, got %d", len(artifacts))
	}
	if _, ok := artifacts[validRule]; !ok {
		t.Fatalf("expected valid rule artifact for %q", validRule)
	}
	buildErr, ok := buildFailures[brokenRule]
	if !ok {
		t.Fatalf("expected build failure for %q", brokenRule)
	}
	if !strings.Contains(buildErr.Error(), "Unexpected") {
		t.Fatalf("expected parse failure, got %q", buildErr.Error())
	}
}

func TestPrepareAllReportsPerRuleErrors(t *testing.T) {
	t.Parallel()

	repoRoot := repoRootForBundleTest(t)
	workspaceRoot := filepath.Join(repoRoot, "testdata/fixtures/workspace")
	validRule := filepath.Join(repoRoot, "testdata/fixtures/rules/pure-no-effects.ts")
	brokenRule := writeRuleFile(t, "broken-prepare.ts", `
import { functions, rule } from "@lintai/sdk";

export default rule("arch.broken-setup")
	.version(7)
	.setup(() => {
		throw new Error("setup boom");
	})
	.assert(() => functions().in("src/**").isEmpty())
	.message(() => "broken");
`)

	artifacts, buildFailures, err := BuildAll(context.Background(), repoRoot, []string{validRule, brokenRule})
	if err != nil {
		t.Fatal(err)
	}
	if len(buildFailures) != 0 {
		t.Fatalf("expected no build failures, got %#v", buildFailures)
	}

	prepared, prepareFailures, err := PrepareAll(context.Background(), repoRoot, workspaceRoot, artifacts, map[string]any{})
	if err != nil {
		t.Fatal(err)
	}
	if len(prepared) != 1 {
		t.Fatalf("expected 1 prepared rule, got %d", len(prepared))
	}
	prepareErr, ok := prepareFailures[brokenRule]
	if !ok {
		t.Fatalf("expected prepare failure for %q", brokenRule)
	}
	if prepareErr.RuleID != "arch.broken-setup" {
		t.Fatalf("unexpected prepare rule id %q", prepareErr.RuleID)
	}
	if prepareErr.RuleVersion != 7 {
		t.Fatalf("unexpected prepare rule version %d", prepareErr.RuleVersion)
	}
	if !strings.Contains(prepareErr.Message, "setup boom") {
		t.Fatalf("expected setup error, got %q", prepareErr.Message)
	}
}

func repoRootForBundleTest(t *testing.T) string {
	t.Helper()

	repoRoot, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	return repoRoot
}

func writeRuleFile(t *testing.T, name, contents string) string {
	t.Helper()

	path := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(path, []byte(contents), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}
