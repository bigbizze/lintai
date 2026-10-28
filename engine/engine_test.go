package engine

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/bigbizze/lintai/backend/typescript"
	"github.com/bigbizze/lintai/diagnostics"
)

func TestRuleErrorPhases(t *testing.T) {
	repoRoot, err := filepath.Abs("..")
	if err != nil {
		t.Fatal(err)
	}
	ruleRoot := t.TempDir()
	cases := []struct {
		name    string
		phase   string
		message string
		source  string
	}{
		{
			name:    "program-throws",
			phase:   "program",
			message: "cannot interpolate",
			source: `export default rule("t.program-throws").version(1)
	.program(() => dl` + "`" + `Decl v(F). v(F) :- module(F), glob(F, ${[1] as never}).` + "`" + `)
	.report("v").message(() => "x");`,
		},
		{
			name:    "parse",
			phase:   "program_parse",
			message: "",
			source: `export default rule("t.parse").version(1)
	.program(dl` + "`" + `Decl v(F). v(F) :- module(F)` + "`" + `)
	.report("v").message(() => "x");`,
		},
		{
			name:    "redefine-prelude",
			phase:   "program_analysis",
			message: "calls_reach",
			source: `export default rule("t.redefine").version(1)
	.program(dl` + "`" + `Decl v(F). v(F) :- module(F). calls_reach(A, B) :- fn(A, _, _), fn(B, _, _).` + "`" + `)
	.report("v").message(() => "x");`,
		},
		{
			name:    "fact-limit",
			phase:   "evaluation",
			message: "fact size limit",
			source: `export default rule("t.limit").version(1)
	.program(dl` + "`" + `Decl v(A, B). v(A, B) :- module(A), module(B).` + "`" + `)
	.report("v").message(() => "x");`,
		},
		{
			name:    "premise",
			phase:   "premise",
			message: "nothing",
			source: `export default rule("t.premise").version(1)
	.program(dl` + "`" + `Decl nothing(F). nothing(F) :- module(F), glob(F, "no/such/dir/**"). Decl v(F). v(F) :- module(F).` + "`" + `)
	.report("v").expectNonEmpty("nothing").message(() => "x");`,
		},
		{
			name:    "anchor-without-location",
			phase:   "report",
			message: "no location",
			source: `export default rule("t.report").version(1)
	.program(dl` + "`" + `Decl v(F). v("not-an-entity") :- module(_).` + "`" + `)
	.report("v").message(() => "x");`,
		},
		{
			name:    "message-throws",
			phase:   "message",
			message: "message exploded",
			source: `export default rule("t.message").version(1)
	.program(dl` + "`" + `Decl v(F). v(F) :- module(F).` + "`" + `)
	.report("v").message(() => { throw new Error("message exploded"); });`,
		},
	}
	rulePaths := make([]string, 0, len(cases)+1)
	for _, item := range cases {
		rulePaths = append(rulePaths, writeTestFile(t, ruleRoot, item.name+".ts", `import { dl, rule } from "@lintai/sdk";
`+item.source+"\n"))
	}
	rulePaths = append(rulePaths, filepath.Join(repoRoot, "testdata/fixtures/rules/pure-no-effects.ts"))

	items, err := New(typescript.New()).Run(context.Background(), Options{
		RepoRoot:      repoRoot,
		AssetRoot:     filepath.Join(repoRoot, "packages", "eslint-plugin"),
		WorkspaceRoot: filepath.Join(repoRoot, "testdata/fixtures/workspace"),
		RuleGlobs:     rulePaths,
		Env:           map[string]any{},
		Severity:      diagnostics.SeverityError,
		FactLimit:     50,
	})
	if err != nil {
		t.Fatal(err)
	}

	byRule := make(map[string][]diagnostics.Diagnostic)
	for _, item := range items {
		byRule[item.RuleID] = append(byRule[item.RuleID], item)
	}
	ruleIDs := map[string]string{
		"program-throws":          "t.program-throws",
		"parse":                   "t.parse",
		"redefine-prelude":        "t.redefine",
		"fact-limit":              "t.limit",
		"premise":                 "t.premise",
		"anchor-without-location": "t.report",
		"message-throws":          "t.message",
	}
	for _, item := range cases {
		got := byRule[ruleIDs[item.name]]
		if len(got) != 1 {
			t.Fatalf("%s: expected only one error diagnostic, got %+v", item.name, got)
		}
		if got[0].DiagnosticKind != diagnostics.KindRuleExecutionError || got[0].Phase != item.phase {
			t.Fatalf("%s: expected phase %s, got %+v", item.name, item.phase, got[0])
		}
		if !strings.Contains(got[0].Message, item.message) {
			t.Fatalf("%s: expected message to contain %q, got %q", item.name, item.message, got[0].Message)
		}
	}
	if len(byRule["arch.pure-no-effects"]) != 2 {
		t.Fatalf("expected the unaffected rule to still report its violations, got %+v", byRule["arch.pure-no-effects"])
	}
}

func TestRelativeRuleGlobsResolveFromWorkspaceRoot(t *testing.T) {
	repoRoot, err := filepath.Abs("..")
	if err != nil {
		t.Fatal(err)
	}
	workspaceRoot := t.TempDir()
	writeTestFile(t, workspaceRoot, "src/bad.ts", "export async function bad() {\n\treturn 1;\n}\n")
	writeTestFile(t, workspaceRoot, "lintai-rules/no-async.ts", `import { dl, rule } from "@lintai/sdk";
export default rule("arch.no-async").version(1)
	.program(dl`+"`"+`Decl v(Fn). v(Fn) :- fn_async(Fn).`+"`"+`)
	.report("v").message(() => "no async");
`)
	items, err := New(typescript.New()).Run(context.Background(), Options{
		RepoRoot:      repoRoot,
		AssetRoot:     filepath.Join(repoRoot, "packages", "eslint-plugin"),
		WorkspaceRoot: workspaceRoot,
		RuleGlobs:     []string{"lintai-rules/**/*.ts"},
		Env:           map[string]any{},
		Severity:      diagnostics.SeverityError,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 1 || items[0].RuleID != "arch.no-async" || items[0].SourceLocation.File != "src/bad.ts" {
		t.Fatalf("expected one arch.no-async violation in src/bad.ts, got %+v", items)
	}
}

func writeTestFile(t *testing.T, root, relativePath, contents string) string {
	t.Helper()

	path := filepath.Join(root, filepath.FromSlash(relativePath))
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(contents), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestDlStringsRoundTripThroughMangle(t *testing.T) {
	repoRoot, err := filepath.Abs("..")
	if err != nil {
		t.Fatal(err)
	}
	tricky, err := json.Marshal("q\"s'b\\n\nt\tc\x01d\x7fé😀`${x}")
	if err != nil {
		t.Fatal(err)
	}
	rulePath := writeTestFile(t, t.TempDir(), "round-trip.ts", `import { dl, rule } from "@lintai/sdk";
const tricky = `+string(tricky)+`;
export default rule("t.round-trip").version(1)
	.program(dl`+"`"+`Decl v(F, S, N, K). v(F, ${tricky}, ${-12}, ${dl.name("a/b_c")}) :- module(F), F = "src/pure/math.ts".`+"`"+`)
	.report("v")
	.message((row) => JSON.stringify([row.S === tricky, row.N, row.K]));
`)
	items, err := New(typescript.New()).Run(context.Background(), Options{
		RepoRoot:      repoRoot,
		AssetRoot:     filepath.Join(repoRoot, "packages", "eslint-plugin"),
		WorkspaceRoot: filepath.Join(repoRoot, "testdata/fixtures/workspace"),
		RuleGlobs:     []string{rulePath},
		Env:           map[string]any{},
		Severity:      diagnostics.SeverityError,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 1 || items[0].Message != `[true,-12,"/a/b_c"]` {
		t.Fatalf("expected the values to round-trip, got %+v", items)
	}
}
