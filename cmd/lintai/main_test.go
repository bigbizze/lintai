package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/bigbizze/lintai/internal/diagnostics"
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
	if err != nil && !strings.HasPrefix(err.Error(), "lintai found") {
		t.Fatal(err)
	}
	return stdout.Bytes()
}
