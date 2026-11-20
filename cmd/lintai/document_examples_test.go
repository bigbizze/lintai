package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/bigbizze/lintai/diagnostics"
)

// Run the exact quickstart and optional helper against a real workspace.
func TestFirstRunDocumentation(t *testing.T) {
	repo, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	readFence := func(path, marker string) string {
		t.Helper()
		data, err := os.ReadFile(filepath.Join(repo, path))
		if err != nil {
			t.Fatal(err)
		}
		if strings.Count(string(data), marker) != 1 {
			t.Fatalf("%s: expected exactly one %q", path, marker)
		}
		_, rest, _ := strings.Cut(string(data), marker)
		body, _, ok := strings.Cut(rest, "\n```\n")
		if !ok {
			t.Fatalf("%s: missing closing fence", path)
		}
		return body
	}
	quickstart := readFence("docs/install.md", "Add a rule:\n\n```ts\n")
	helper := "Decl permitted_opaque_leaf(Type).\n" + readFence("docs/examples.md", "```mangle\nDecl permitted_opaque_leaf(Type).\n")
	workspace := t.TempDir()
	write := func(path, content string) {
		t.Helper()
		name := filepath.Join(workspace, path)
		if err := os.MkdirAll(filepath.Dir(name), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(name, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("src/example.ts", "interface Secret { value: string }\nexport async function f(value: Promise<Secret>) { return value; }\nexport function g(value: Promise<string>) { return value; }\n")
	write("rules/quickstart.ts", quickstart+"\n")
	write("rules/optional-helper.ts", "import { dl, rule } from \"@lintai/sdk\";\n"+
		"export default rule(\"docs.optional-helper\").version(1).program(dl`\n"+helper+"\n"+
		"Decl violation(Fn).\nviolation(Fn) :- ts.fn_signature(Fn, Sig), ts.signature_param(Sig, 0, T), reaches_disallowed_incomplete(T).\n"+
		"`).report(\"violation\").message(() => \"incomplete type\");\n")
	output := runJSON(t, []string{"--json", "--repo-root", repo, "--workspace-root", workspace, "--rules", filepath.Join(workspace, "rules/*.ts")})
	var items []diagnostics.Diagnostic
	if err := json.Unmarshal(output, &items); err != nil {
		t.Fatal(err)
	}
	var ids []string
	for _, item := range items {
		if item.DiagnosticKind != diagnostics.KindArchitectureViolation {
			t.Fatalf("documented rule failed: %+v", item)
		}
		ids = append(ids, item.RuleID)
	}
	slices.Sort(ids)
	if !slices.Equal(ids, []string{"arch.no-async", "docs.optional-helper"}) {
		t.Fatalf("documented example diagnostics = %v", ids)
	}
}
