package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/bigbizze/lintai/diagnostics"
)

// The fixture workspace contains planted violations of every fixture rule.
// A change to extraction, the prelude or a rule that adds or loses a
// violation changes this exact set.
func TestFixtureRulesReportPlantedViolations(t *testing.T) {
	repoRoot, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	args := []string{
		"--json", "--explain",
		"--repo-root", repoRoot,
		"--workspace-root", filepath.Join(repoRoot, "testdata/fixtures/workspace"),
		"--rules", filepath.Join(repoRoot, "testdata/fixtures/rules/*.ts"),
	}
	first := runJSON(t, args)
	second := runJSON(t, args)
	if !bytes.Equal(first, second) {
		t.Fatalf("two runs produced different output:\n%s\n---\n%s", first, second)
	}

	var items []diagnostics.Diagnostic
	if err := json.Unmarshal(first, &items); err != nil {
		t.Fatal(err)
	}
	got := make([]string, 0, len(items))
	for _, item := range items {
		if item.DiagnosticKind != diagnostics.KindArchitectureViolation {
			t.Fatalf("unexpected rule error: %+v", item)
		}
		got = append(got, fmt.Sprintf("%s %s %s:%d", item.RuleID, item.AssertionID, item.SourceLocation.File, item.SourceLocation.StartLine))
		if len(item.Explanation) == 0 {
			t.Fatalf("expected an explanation for %s", got[len(got)-1])
		}
	}
	want := []string{
		"arch.core-no-service-reach violation src/core/entry.ts:1",
		"arch.core-no-service-reach violation src/core/leaf.ts:1",
		"arch.core-no-service-reach violation src/core/mid.ts:1",
		"arch.layers violation src/pure/helper.ts:1",
		"arch.pure-no-service-imports violation src/pure/helper.ts:1",
		"arch.pure-no-effects violation src/pure/helper.ts:3",
		"arch.pure-no-effects violation src/pure/math.ts:3",
		"arch.ambient clock_in_pure src/pure/time.ts:1",
		"arch.server-only missing_marker src/server/config.ts:1",
		"arch.ambient env_outside_config src/server/handler.ts:5",
		"arch.ambient env_dynamic src/server/handler.ts:5",
	}
	if !slices.Equal(got, want) {
		t.Fatalf("planted violations differ:\ngot:\n%s\nwant:\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}

	for _, item := range items {
		if item.RuleID != "arch.pure-no-effects" || item.SourceLocation.File != "src/pure/math.ts" {
			continue
		}
		last := item.Explanation[len(item.Explanation)-1]
		if !strings.HasPrefix(last, "fn_awaits(") {
			t.Fatalf("expected the explanation to end with a base fact, got %q in %q", last, item.Explanation)
		}
	}
}

func runJSON(t *testing.T, args []string) []byte {
	t.Helper()
	var stdout bytes.Buffer
	err := run(context.Background(), args, &stdout)
	if code := exitCode(err, io.Discard); code != exitViolations {
		t.Fatalf("exit code %d, want %d: %v", code, exitViolations, err)
	}
	return stdout.Bytes()
}

// CI must be able to tell "the rules found violations" apart from "lintai
// could not check the workspace".
func TestExitCodes(t *testing.T) {
	repoRoot, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	workspace := filepath.Join(repoRoot, "testdata/fixtures/workspace")
	rule := func(name string) string { return filepath.Join(repoRoot, "testdata/fixtures/rules", name) }
	cases := []struct {
		name string
		args []string
		want int
	}{
		{"violations", []string{"--workspace-root", workspace, "--repo-root", repoRoot, "--rules", rule("layers.ts")}, exitViolations},
		{"clean", []string{"--workspace-root", workspace, "--repo-root", repoRoot, "--rules", rule("layers.ts"), "--exclude", "src/pure/**"}, exitClean},
		{"unknown flag", []string{"--no-such-flag"}, exitUsage},
		{"bad file scope", []string{"--workspace-root", workspace, "--repo-root", repoRoot, "--rules", rule("layers.ts"), "--files", "all"}, exitUsage},
		{"glob matches nothing", []string{"--workspace-root", workspace, "--repo-root", repoRoot, "--rules", "missing/*.ts"}, exitUsage},
		{"unknown relation", []string{"facts", "--workspace-root", workspace, "--relation", "no_such_relation"}, exitUsage},
		{"facts accepts --asset-root", []string{"facts", "--workspace-root", workspace, "--relation", "module", "--asset-root", repoRoot}, exitClean},
	}
	for _, item := range cases {
		t.Run(item.name, func(t *testing.T) {
			err := run(context.Background(), item.args, io.Discard)
			if code := exitCode(err, io.Discard); code != item.want {
				t.Fatalf("exit code %d, want %d: %v", code, item.want, err)
			}
		})
	}
}

// Authoring mistakes give an error that names the cause. A helper module
// under the rule glob is not a rule and is skipped.
func TestAuthoringErrors(t *testing.T) {
	repoRoot, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	rulesDir := t.TempDir()
	rules := map[string]string{
		"helper.ts": `export const shared = "src/**";`,
		"no-version.ts": `import { dl, rule } from "@lintai/sdk";
export default rule("t.no-version").program(dl` + "`Decl v(F). v(F) :- module(F).`" + `).report("v").message((row) => String(row.F));`,
		"negated-wildcard.ts": `import { dl, rule } from "@lintai/sdk";
export default rule("t.negated-wildcard").version(1).program(dl` + "`Decl v(F). v(F) :- module(F), !import_edge(_, F, _, \"server-only\").`" + `).report("v").message((row) => String(row.F));`,
		"unknown-column.ts": `import { dl, rule } from "@lintai/sdk";
export default rule("t.unknown-column").version(1).program(dl` + "`Decl v(F). v(F) :- module(F).`" + `).report("v").message((row) => String(row.Nope));`,
		"expect-all.ts": `import { dl, rule } from "@lintai/sdk";
export default rule("t.expect-all").version(1).program(dl` + "`Decl a(F). a(F) :- module(F), glob(F, \"none/**\"). Decl b(F). b(F) :- module(F), glob(F, \"nada/**\").`" + `).report("a").expectNonEmpty("a", "b").message((row) => String(row.F));`,
	}
	for name, text := range rules {
		if err := os.WriteFile(filepath.Join(rulesDir, name), []byte(text), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	var stdout bytes.Buffer
	err = run(context.Background(), []string{
		"--json", "--repo-root", repoRoot,
		"--workspace-root", filepath.Join(repoRoot, "testdata/fixtures/workspace"),
		"--rules", filepath.Join(rulesDir, "*.ts"),
	}, &stdout)
	if code := exitCode(err, io.Discard); code != exitRuleError {
		t.Fatalf("exit code %d, want %d: %v", code, exitRuleError, err)
	}
	var items []diagnostics.Diagnostic
	if err := json.Unmarshal(stdout.Bytes(), &items); err != nil {
		t.Fatal(err)
	}
	got := make(map[string]string)
	for _, item := range items {
		if item.DiagnosticKind != diagnostics.KindRuleExecutionError {
			t.Fatalf("unexpected violation: %+v", item)
		}
		got[item.RuleID] = item.Phase + ": " + item.Message
	}
	want := map[string]string{
		"t.no-version":       "setup: rule t.no-version must call .version(n)",
		"t.negated-wildcard": "program_analysis: _ cannot be used in the negated atom",
		"t.unknown-column":   "message() read row.Nope, but the reported predicate has no argument Nope. Its arguments are: F",
		"t.expect-all":       "premise: expectNonEmpty: these predicates have no facts: a, b",
	}
	if len(got) != len(want) {
		t.Fatalf("got errors for %v, want %v", got, want)
	}
	for ruleID, prefix := range want {
		if !strings.Contains(got[ruleID], prefix) {
			t.Errorf("%s: got %q, want it to contain %q", ruleID, got[ruleID], prefix)
		}
	}
}
