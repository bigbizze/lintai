package engine

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
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
		AssetRoot:     filepath.Join(repoRoot, "packages", "client"),
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
		AssetRoot:     filepath.Join(repoRoot, "packages", "client"),
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
		AssetRoot:     filepath.Join(repoRoot, "packages", "client"),
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

// !glob(P, X) holds when P does not match X, a clause can end with a name
// constant (Layer != /core.), and with verbatimModuleSyntax an
// import { type X } is a runtime import. Before this, the first was an
// error, the second did not parse, and the third was not a runtime import.
func TestNegatedGlobAndTrailingNameConstant(t *testing.T) {
	repoRoot, err := filepath.Abs("..")
	if err != nil {
		t.Fatal(err)
	}
	workspaceRoot := t.TempDir()
	writeTestFile(t, workspaceRoot, "tsconfig.json", `{"compilerOptions": {"module": "preserve", "moduleResolution": "bundler", "verbatimModuleSyntax": true}}`)
	writeTestFile(t, workspaceRoot, "src/core/a.ts", "export type A = number;\n")
	writeTestFile(t, workspaceRoot, "src/ui/b.ts", "import { type A } from \"../core/a\";\nexport const b: A = 1;\n")
	writeTestFile(t, workspaceRoot, "lintai-rules/runtime.ts", `import { dl, rule } from "@lintai/sdk";
export default rule("t.runtime").version(1)
	.program(dl`+"`"+`Decl r(Imp). r(Imp) :- import_edge(Imp, F, _, _), glob(F, "src/**"), import_runtime(Imp).`+"`"+`)
	.report("r").message(() => "runtime");
`)
	writeTestFile(t, workspaceRoot, "lintai-rules/outside.ts", `import { dl, rule } from "@lintai/sdk";
export default rule("t.outside").version(1)
	.program(dl`+"`"+`Decl layer(F, L). layer(F, /ui) :- module(F), glob(F, "src/ui/**"). layer(F, /core) :- module(F), glob(F, "src/core/**").
Decl v(F). v(F) :- module(F), !glob(F, "src/core/**"), layer(F, L), L != /core.
# a comment with /core. in it
Decl w(F). w(F) :- module(F), F = "src/a/b.c", layer(F, /ui.x).`+"`"+`)
	.report("v").message(() => "outside");
`)
	items, err := New(typescript.New()).Run(context.Background(), Options{
		RepoRoot:      repoRoot,
		AssetRoot:     filepath.Join(repoRoot, "packages", "client"),
		WorkspaceRoot: workspaceRoot,
		RuleGlobs:     []string{"lintai-rules/**/*.ts"},
		Env:           map[string]any{},
		Severity:      diagnostics.SeverityError,
	})
	if err != nil {
		t.Fatal(err)
	}
	byRule := map[string][]string{}
	for _, item := range items {
		byRule[item.RuleID] = append(byRule[item.RuleID], item.SourceLocation.File)
	}
	if got := byRule["t.outside"]; len(got) != 1 || got[0] != "src/ui/b.ts" {
		t.Fatalf("expected one t.outside violation in src/ui/b.ts, got %+v", items)
	}
	if got := byRule["t.runtime"]; len(got) != 1 || got[0] != "src/ui/b.ts" {
		t.Fatalf("expected the retained type import to be a runtime import, got %+v", items)
	}
}

// A rule with severity off is not evaluated, so it reports nothing even when
// its program fails. A warning rule's violations are warnings, but its
// execution errors are errors.
func TestRuleSeverity(t *testing.T) {
	repoRoot, err := filepath.Abs("..")
	if err != nil {
		t.Fatal(err)
	}
	workspaceRoot := t.TempDir()
	writeTestFile(t, workspaceRoot, "src/a.ts", "export async function a() {\n\treturn 1;\n}\n")
	writeTestFile(t, workspaceRoot, "lintai-rules/rules.ts", `import { dl, rule } from "@lintai/sdk";
export const soft = rule("t.soft").version(1)
	.program(dl`+"`"+`Decl v(Fn). v(Fn) :- fn_async(Fn).`+"`"+`)
	.report("v").message(() => "soft");
`)
	writeTestFile(t, workspaceRoot, "lintai-rules/broken-off.ts", `import { dl, rule } from "@lintai/sdk";
export default rule("t.broken-off").version(1)
	.program(dl`+"`"+`Decl v(F). v(F) :- module(F)`+"`"+`)
	.report("v").message(() => "x");
`)
	writeTestFile(t, workspaceRoot, "lintai-rules/broken-warning.ts", `import { dl, rule } from "@lintai/sdk";
export default rule("t.broken-warning").version(1)
	.program(dl`+"`"+`Decl v(F). v(F) :- module(F)`+"`"+`)
	.report("v").message(() => "x");
`)
	writeTestFile(t, workspaceRoot, "lintai-rules/soft.ts", `export { soft as default } from "./rules";
`)
	items, err := New(typescript.New()).Run(context.Background(), Options{
		RepoRoot:      repoRoot,
		AssetRoot:     filepath.Join(repoRoot, "packages", "client"),
		WorkspaceRoot: workspaceRoot,
		RuleGlobs:     []string{"lintai-rules/broken-*.ts", "lintai-rules/soft.ts"},
		Env:           map[string]any{},
		RuleSeverity: map[string]diagnostics.Severity{
			"t.soft":           diagnostics.SeverityWarning,
			"t.broken-off":     diagnostics.SeverityOff,
			"t.broken-warning": diagnostics.SeverityWarning,
			"t.missing":        diagnostics.SeverityOff,
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	got := make([]string, 0, len(items))
	for _, item := range items {
		got = append(got, item.RuleID+" "+string(item.DiagnosticKind)+" "+string(item.Severity)+" "+item.Phase)
	}
	sort.Strings(got)
	want := []string{
		"t.broken-warning rule_execution_error error program_parse",
		"t.missing rule_execution_error error config",
		"t.soft architecture_violation warning ",
	}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("got\n%s\nwant\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
}
