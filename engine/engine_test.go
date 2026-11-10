package engine

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"testing"

	"github.com/bigbizze/lintai/backend"
	"github.com/bigbizze/lintai/backend/typescript"
	"github.com/bigbizze/lintai/diagnostics"
	"github.com/bigbizze/lintai/facts"
	"github.com/bigbizze/lintai/runtime"
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

func TestDeclaredWriterCapabilityPolicy(t *testing.T) {
	repoRoot, err := filepath.Abs("..")
	if err != nil {
		t.Fatal(err)
	}
	fixture := filepath.Join(repoRoot, "testdata", "call-provenance")
	for _, test := range []struct {
		name          string
		missingExport bool
		noWrites      bool
		wrappedIndex  bool
	}{
		{name: "selected export alias"},
		{name: "wrapped static index", wrappedIndex: true},
		{name: "missing export alias", missingExport: true},
		{name: "no forbidden writes", noWrites: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			root := t.TempDir()
			for _, file := range []string{"contracts.ts", "calls.ts", "tsconfig.json"} {
				contents, err := os.ReadFile(filepath.Join(fixture, file))
				if err != nil {
					t.Fatal(err)
				}
				source := string(contents)
				if test.wrappedIndex && file == "calls.ts" {
					source = strings.Replace(source, "shared.write()", `shared[(("write" as const) satisfies string)]()`, 1)
				}
				if test.missingExport && file == "contracts.ts" {
					source = strings.Replace(source, "export type { Writer as PublicWriter };", "", 1)
				}
				if test.noWrites && file == "calls.ts" {
					source = strings.ReplaceAll(source, "store.write()", "store.read()")
				}
				writeTestFile(t, root, file, source)
			}
			items, err := New(typescript.New()).Run(context.Background(), Options{
				RepoRoot: repoRoot, AssetRoot: filepath.Join(repoRoot, "packages", "client"),
				WorkspaceRoot: root, RuleGlobs: []string{filepath.Join(fixture, "writer-policy.ts")},
				Files: backend.FileScopeWalk, Severity: diagnostics.SeverityError,
			})
			if err != nil {
				t.Fatal(err)
			}
			var got []string
			for _, item := range items {
				if item.RuleID != "arch.no-writer-write" || item.DiagnosticKind != diagnostics.KindArchitectureViolation ||
					item.Severity != diagnostics.SeverityError || item.SourceLocation == nil || item.SourceLocation.File != "calls.ts" {
					t.Fatalf("unexpected diagnostic: %+v", item)
				}
				got = append(got, fmt.Sprintf("%s:%d:%s", item.AssertionID, item.SourceLocation.StartLine, item.Message))
			}
			want := []string{
				"indeterminate:14:Cannot establish Writer.write policy: unsupported",
				"indeterminate:15:Cannot establish Writer.write policy: synthetic",
				"indeterminate:16:Cannot establish Writer.write policy: unresolved",
			}
			if test.missingExport {
				want = append(want, "indeterminate:1:Cannot establish Writer.write policy: missing export")
			} else if !test.noWrites {
				want = append(want,
					"forbidden:7:Forbidden member: Writer.write",
					"forbidden:9:Forbidden member: Writer.write")
			}
			slices.Sort(got)
			slices.Sort(want)
			if !slices.Equal(got, want) {
				t.Fatalf("policy diagnostics = %q, want %q", got, want)
			}
		})
	}
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

// Rules check types through structural facts. The example rule from
// docs/examples.md reports an exported async action that does not return
// Promise<ActionResult<T>>, and type_contains finds a named type nested in
// a signature.
func TestStructuralTypeRules(t *testing.T) {
	repoRoot, err := filepath.Abs("..")
	if err != nil {
		t.Fatal(err)
	}
	workspaceRoot := t.TempDir()
	writeTestFile(t, workspaceRoot, "tsconfig.json", `{"compilerOptions": {"strict": true, "module": "preserve", "moduleResolution": "bundler"}}`)
	writeTestFile(t, workspaceRoot, "src/core/result.ts", "export type ActionResult<T> = { ok: true; value: T } | { ok: false; error: string };\n")
	writeTestFile(t, workspaceRoot, "src/legacy/types.ts", "export type ActionResult<T> = { done: T };\n")
	writeTestFile(t, workspaceRoot, "src/infra/db.ts", "export class Db { query(): number { return 1; } }\n")
	const actionSource = `import type { ActionResult } from "../core/result";
import type { ActionResult as Legacy } from "../legacy/types";
export async function save(): Promise<ActionResult<number>> { return { ok: true, value: 1 }; }
export async function legacy(): Promise<Legacy<number>> { return { done: 1 }; }
export const wrongDirect = async () => 1;
export const wrongWrapped = (async () => 2);
export const wrongChecked = (async () => 3) satisfies () => Promise<number>;
export default (async () => 4);
`
	writeTestFile(t, workspaceRoot, "src/actions/save.ts", actionSource)
	writeTestFile(t, workspaceRoot, "src/domain/user.ts", `import type { Db } from "../infra/db";
export interface Hidden { forbidden: Db }
export function hidden(value: Hidden): void {}
export function load(input: { db: Db[] }): number { return input.db.length; }
export function sync(): number { return 1; }
export const plain = (): number => 1;
export function maybe(): Promise<number> | undefined { return undefined; }
export function constrained<T extends Db>(x: T): void {}
export function picked<K extends keyof Db>(k: K): Db[K] { return null as never; }
`)
	actionRule, err := os.ReadFile(filepath.Join(repoRoot, "testdata/npm-smoke/lintai-rules/actions-return-action-result.ts"))
	if err != nil {
		t.Fatal(err)
	}
	writeTestFile(t, workspaceRoot, "lintai-rules/actions.ts", string(actionRule))
	writeTestFile(t, workspaceRoot, "lintai-rules/domain.ts", `import { dl, rule } from "@lintai/sdk";
export default rule("arch.domain-signatures").version(1)
	.program(dl`+"`"+`
		Decl violation(Fn, Name).
		Decl signature_type(Fn, T).
		Decl mentions_infra(Fn).
		signature_type(Fn, T) :- fn_param(Fn, _, T).
		signature_type(Fn, T) :- fn_returns(Fn, T).
		mentions_infra(Fn) :- signature_type(Fn, T), type_named(T, Origin, _), glob(Origin, "src/infra/**").
		mentions_infra(Fn) :- signature_type(Fn, T), type_contains(T, Inner), type_named(Inner, Origin, _), glob(Origin, "src/infra/**").
		violation(Fn, Name) :- fn(Fn, File, Name), fn_exported(Fn), glob(File, "src/domain/**"), mentions_infra(Fn).
	`+"`"+`)
	.report("violation")
	.message((row) => row.Name);
`)
	writeTestFile(t, workspaceRoot, "lintai-rules/domain-strict.ts", `import { dl, rule } from "@lintai/sdk";
export default rule("arch.domain-signatures-strict").version(1)
	.program(dl`+"`"+`
		Decl violation(Fn, Name).
		Decl signature_type(Fn, T).
		signature_type(Fn, T) :- fn_param(Fn, _, T).
		signature_type(Fn, T) :- fn_returns(Fn, T).
		violation(Fn, Name) :- fn(Fn, File, Name), fn_exported(Fn), glob(File, "src/domain/**"), signature_type(Fn, T), type_reaches_incomplete(T).
	`+"`"+`)
	.report("violation")
	.message((row) => row.Name);
`)
	writeTestFile(t, workspaceRoot, "lintai-rules/awaited.ts", `import { dl, rule } from "@lintai/sdk";
export default rule("t.awaited").version(1)
	.program(dl`+"`"+`
		Decl v(Fn, Name, Text).
		v(Fn, Name, Text) :- fn(Fn, _, Name), fn_returns_awaited(Fn, T), type_display(T, Text).
	`+"`"+`)
	.report("v")
	.message((row) => `+"`${row.Name}:${row.Text}`"+`);
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
		byRule[item.RuleID] = append(byRule[item.RuleID], item.Message)
	}
	for _, messages := range byRule {
		sort.Strings(messages)
	}
	wrappedName := fmt.Sprintf("<anonymous@%d>", strings.Index(actionSource, "async () => 2"))
	checkedName := fmt.Sprintf("<anonymous@%d>", strings.Index(actionSource, "async () => 3"))
	defaultName := fmt.Sprintf("<anonymous@%d>", strings.Index(actionSource, "async () => 4"))
	wantActions := []string{
		"legacy must return Promise<ActionResult<T>>, but returns Promise<Legacy<number>>", "wrongDirect must return Promise<ActionResult<T>>, but returns Promise<number>",
		wrappedName + " must return Promise<ActionResult<T>>, but returns Promise<number>", checkedName + " must return Promise<ActionResult<T>>, but returns Promise<number>", defaultName + " must return Promise<ActionResult<T>>, but returns Promise<number>",
	}
	sort.Strings(wantActions)
	if got := byRule["arch.actions-return-action-result"]; !slices.Equal(got, wantActions) {
		t.Fatalf("actions rule: got %v, want %v", got, wantActions)
	}
	// type_contains does not see the Db in a type parameter constraint,
	// indexed access type, or omitted interface members, so the plain rule
	// reports only load. The strict rule catches the interface omission and
	// ordinary Promise/Array library interfaces as well as the two existing
	// incomplete type forms.
	if got := byRule["arch.domain-signatures"]; len(got) != 1 || got[0] != "load" {
		t.Fatalf("domain rule: %+v", items)
	}
	if got := byRule["arch.domain-signatures-strict"]; strings.Join(got, ",") != "constrained,hidden,load,maybe,picked" {
		t.Fatalf("strict domain rule: %v", got)
	}
	// fn_returns_awaited unwraps one lib Promise, not a union that has one.
	want := []string{"Db.query:number", "constrained:void", "hidden:void", "legacy:Legacy<number>", "load:number", "maybe:Promise<number> | undefined", "picked:Db[K]", "plain:number", "save:ActionResult<number>", "sync:number", "wrongDirect:number", wrappedName + ":number", checkedName + ":number", defaultName + ":number"}
	sort.Strings(want)
	if got := byRule["t.awaited"]; strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("fn_returns_awaited: got %v, want %v", got, want)
	}
}

func TestActionReturnRuleReportsConflictingCheckedSignatures(t *testing.T) {
	repoRoot, err := filepath.Abs("..")
	if err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name       string
		rootConfig string
		groups     []string
		want       []string
	}{
		{
			name:       "loose",
			rootConfig: `{"compilerOptions":{"strict":false},"files":["src/actions/save.ts","src/core/result.ts"],"references":[{"path":"./loose"}]}`,
			groups:     []string{"loose"},
			want:       []string{"wrong must return Promise<ActionResult<T>>, but returns Promise<number>"},
		},
		{
			name:       "strict",
			rootConfig: `{"compilerOptions":{"strict":true},"files":["src/actions/save.ts","src/core/result.ts"],"references":[{"path":"./strict"}]}`,
			groups:     []string{"strict"},
			want: []string{
				"mixed must return Promise<ActionResult<T>>, but returns Promise<ActionResult<number> | undefined>",
				"wrong must return Promise<ActionResult<T>>, but returns Promise<number>",
			},
		},
		{
			name:       "both",
			rootConfig: `{"compilerOptions":{"strict":false},"files":["src/actions/save.ts","src/core/result.ts"],"references":[{"path":"./loose"},{"path":"./strict"}]}`,
			groups:     []string{"loose", "strict"},
			want: []string{
				"ambiguous has conflicting checked signatures; cannot determine whether its return type is allowed",
				"mixed has conflicting checked signatures; cannot determine whether its return type is allowed",
				"wrong must return Promise<ActionResult<T>>, but returns Promise<number>",
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			root := t.TempDir()
			writeTestFile(t, root, "tsconfig.json", test.rootConfig)
			writeTestFile(t, root, "src/core/result.ts", `export type ActionResult<T> = { ok: true; value: T } | { ok: false; error: string };`)
			writeTestFile(t, root, "src/actions/save.ts", `import type { ActionResult } from "../core/result";
export async function approved(): Promise<ActionResult<number>> { return { ok: true, value: 1 }; }
export async function wrong(): Promise<number> { return 1; }
export async function mixed(): Promise<ActionResult<number> | undefined> { return undefined; }
export async function ambiguous(value: string | undefined): Promise<ActionResult<number>> { return { ok: true, value: value?.length ?? 0 }; }
`)
			for _, group := range test.groups {
				strict := "false"
				if group == "strict" {
					strict = "true"
				}
				writeTestFile(t, root, group+"/tsconfig.json", `{"compilerOptions":{"strict":`+strict+`},"include":["**/*.ts","../src/**/*.ts"]}`)
				writeTestFile(t, root, group+"/use.ts", `export { mixed } from "../src/actions/save";`)
			}
			rulePath := filepath.Join(repoRoot, "testdata/npm-smoke/lintai-rules/actions-return-action-result.ts")
			items, err := New(typescript.New()).Run(context.Background(), Options{
				RepoRoot:      repoRoot,
				AssetRoot:     filepath.Join(repoRoot, "packages", "client"),
				WorkspaceRoot: root,
				RuleGlobs:     []string{rulePath},
				Env:           map[string]any{},
				Severity:      diagnostics.SeverityError,
			})
			if err != nil {
				t.Fatal(err)
			}
			got := make([]string, 0, len(items))
			for _, item := range items {
				if item.RuleID != "arch.actions-return-action-result" || item.SourceLocation == nil || item.SourceLocation.File != "src/actions/save.ts" {
					t.Fatalf("unexpected diagnostic: %+v", item)
				}
				got = append(got, item.Message)
			}
			sort.Strings(got)
			if strings.Join(got, "\n") != strings.Join(test.want, "\n") {
				t.Fatalf("diagnostics got %q, want %q", got, test.want)
			}
		})
	}
}

func TestStrictRulesTreatUncertainCalleesAsIndeterminate(t *testing.T) {
	repoRoot, err := filepath.Abs("..")
	if err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	writeTestFile(t, root, "src/pure/values.ts", `let callback = (() => 0);
export function replace() { callback = () => Date.now(); }
export function invoke() { return callback(); }
export function escapeMutable() { return [1].map(callback); }
export function applyMutable() { return callback.call(undefined); }
export function inferred(callback = Date.now) { return callback(); }
export function named(flag: boolean) { return (flag ? function first() { return 0; } : function second() { return 1; })(); }
export function unnamed(flag: boolean) { return (flag ? () => 0 : () => 1)(); }
export function literal() { return ((() => 0))(); }
const stable = (() => 0);
export function bound() { return (stable as () => number)(); }
`)
	writeTestFile(t, root, "lintai-rules/strict.ts", `import { dl, rule } from "@lintai/sdk";
export default rule("test.strict").version(1)
  .program(dl`+"`"+`
    Decl selected(Fn, Name).
    selected(Fn, Name) :- fn(Fn, _, Name), fn_exported(Fn), Name != "replace".
    Decl ambient(Fn, Name).
    ambient(Fn, Name) :- selected(Fn, Name), fn_reaches_ambient_strict(Fn, _).
    Decl indeterminate(Fn, Name).
    indeterminate(Fn, Name) :- selected(Fn, Name), fn_reaches_unresolved(Fn).
  `+"`"+`)
  .report("ambient", "indeterminate").message((row, ctx) => `+"`${row.Name}:${ctx.assertion_id}`"+`);
`)
	items, err := New(typescript.New()).Run(context.Background(), Options{
		RepoRoot: repoRoot, AssetRoot: filepath.Join(repoRoot, "packages/client"), WorkspaceRoot: root,
		RuleGlobs: []string{"lintai-rules/*.ts"}, Files: backend.FileScopeWalk,
	})
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, item := range items {
		if item.RuleID != "test.strict" {
			t.Fatalf("unexpected diagnostic: %+v", item)
		}
		got = append(got, item.Message)
	}
	sort.Strings(got)
	want := []string{"applyMutable:indeterminate", "escapeMutable:indeterminate", "inferred:ambient", "inferred:indeterminate", "invoke:indeterminate", "named:indeterminate", "unnamed:indeterminate"}
	if !slices.Equal(got, want) {
		t.Fatalf("strict rule outcomes = %v, want %v", got, want)
	}
}

func TestExportedInfrastructureTypePolicyFindsKnexWithoutFlaggingContracts(t *testing.T) {
	repoRoot, err := filepath.Abs("..")
	if err != nil {
		t.Fatal(err)
	}
	workspaceRoot := t.TempDir()
	writeTestFile(t, workspaceRoot, "tsconfig.json", `{"compilerOptions":{"strict":true,"module":"preserve","moduleResolution":"bundler"},"include":["src/**/*.ts"]}`)
	writeTestFile(t, workspaceRoot, "node_modules/knex/package.json", `{"name":"knex","types":"index.d.ts"}`)
	writeTestFile(t, workspaceRoot, "node_modules/knex/index.d.ts", `export interface Knex { query(sql: string): Promise<unknown> }`)
	writeTestFile(t, workspaceRoot, "node_modules/@echo/grpc/package.json", `{"name":"@echo/grpc","types":"index.d.ts"}`)
	writeTestFile(t, workspaceRoot, "node_modules/@echo/grpc/index.d.ts", `export type EchoMessage = { eventVersionId: number; traceId: string }`)
	writeTestFile(t, workspaceRoot, "src/common/validation.ts", `import type { Knex } from "knex";
export type ValidationContext = { db: Knex; actorId: string };
`)
	writeTestFile(t, workspaceRoot, "src/common/left.ts", `export type { ValidationContext } from "./validation";`)
	writeTestFile(t, workspaceRoot, "src/common/right.ts", `export type { ValidationContext } from "./validation";`)
	writeTestFile(t, workspaceRoot, "src/common/event.ts", `export type EventDTO = { eventVersionId: number };`)
	writeTestFile(t, workspaceRoot, "src/common/rpc.ts", `import type { EchoMessage } from "@echo/grpc";
export type EventMessage = { payload: EchoMessage };
`)
	writeTestFile(t, workspaceRoot, "src/common/index.ts", `export type { ValidationContext } from "./left";
export type { ValidationContext as ContextThroughRight } from "./right";
export type { EventDTO } from "./event";
export type { EventMessage } from "./rpc";
`)
	rule := `import { dl, rule } from "@lintai/sdk";
export default rule("arch.public-api-infrastructure-types").version(1)
	.program(dl` + "`" + `
		Decl api_root(Export, File, Name, Type).
		api_root(E, F, N, T) :- module_export(E, F, N, /type), glob(F, "src/common/index.ts"), export_target(E, D), declaration_type(D, /type, T).
		Decl reaches_knex(Type).
		reaches_knex(T) :- type_named(T, "knex", _), type_origin_kind(T, /package).
		reaches_knex(T) :- type_child(T, Child), reaches_knex(Child).
		Decl violation(Export, Name).
		violation(Export, Name) :- api_root(Export, _, Name, Type), reaches_knex(Type).
	` + "`" + `)
	.report("violation")
	.message((row) => row.Name);
`
	writeTestFile(t, workspaceRoot, "lintai-rules/public-api.ts", rule)
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
	var got []string
	for _, item := range items {
		if item.RuleID != "arch.public-api-infrastructure-types" {
			t.Fatalf("unexpected diagnostic: %+v", item)
		}
		got = append(got, item.Message)
	}
	sort.Strings(got)
	want := []string{"ContextThroughRight", "ValidationContext"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("public API policy findings: got %v, want %v", got, want)
	}
}

// Exercise the published facts and the prelude on the same ownership boundaries.
func TestSuspensionOwnershipAndReferencePolicy(t *testing.T) {
	const source = `function known() {}
export function total(input = (known(), import.meta.env)) {
  function nestedAlone() {} function nested() {} const later = nested;
  [1].map(async () => {
    known();
    unknown();
    const saved = known;
    Date.now();
    import.meta.env;
    await unknown();
  });
}
class Absent {
  call = known();
  missing = unknown();
  clock = Date.now();
  meta = import.meta.env;
  value = () => known();
}
class Present {
  field = known();
  constructor(x: number); constructor(x?: number) {}
}
async function make() {
  class Missing { field = await unknown(); }
}
Date.now();
export function values() { const saved = () => known(); return function returned() { known(); }; }
export function choose(flag: boolean) {
  (flag ? () => 0 : () => Date.now())();
  (((() => known()) as () => void)!)();
  const bound = ((() => known()) satisfies () => void);
  (bound as () => void)();
}
`
	root := t.TempDir()
	writeTestFile(t, root, "src/scorecard/total.ts", source)
	writeTestFile(t, root, "src/scorecard/only-unsupported.ts", `class Only { value = await unknown(); }`)
	snapshot, err := typescript.New().BuildSnapshot(context.Background(), root, root, backend.SnapshotOptions{Files: backend.FileScopeWalk})
	if err != nil {
		t.Fatal(err)
	}
	if err := facts.Validate(snapshot); err != nil {
		t.Fatal(err)
	}
	lines := map[string]int{}
	for _, fact := range snapshot.Facts {
		if fact.Relation == "location" && fact.Values[1].String == "src/scorecard/total.ts" {
			lines[fact.Values[0].String] = int(fact.Values[2].Number)
		}
	}
	find := func(relation string, line int) []facts.Fact {
		var result []facts.Fact
		for _, fact := range snapshot.Facts {
			if fact.Relation == relation && lines[fact.Values[0].String] == line {
				result = append(result, fact)
			}
		}
		return result
	}
	owner := func(site string) string {
		for _, fact := range snapshot.Facts {
			if fact.Relation == "site_in_fn" && fact.Values[0].String == site {
				return fact.Values[1].String
			}
		}
		return ""
	}
	unsupported := func(site string) string {
		for _, fact := range snapshot.Facts {
			if fact.Relation == "site_attribution_unsupported" && fact.Values[0].String == site {
				return fact.Values[1].String
			}
		}
		return ""
	}
	callback := fmt.Sprintf("src/scorecard/total.ts::arrow_function::<anonymous@%d>", strings.Index(source, "async () =>"))
	scorecard := "src/scorecard/total.ts::function::total"
	known := "src/scorecard/total.ts::function::known"
	constructor := "src/scorecard/total.ts::constructor::Present.constructor"
	for _, check := range []struct {
		relation   string
		line       int
		fn, reason string
	}{
		{"call_edge", 2, scorecard, ""}, {"meta_access", 2, scorecard, ""}, {"fn_ref", 3, scorecard, ""}, {"fn_ref", 4, scorecard, ""},
		{"call_edge", 5, callback, ""}, {"call_unresolved", 6, callback, ""},
		{"fn_ref", 7, callback, ""}, {"external_call", 8, callback, ""},
		{"ambient_use", 8, callback, ""}, {"global_ref", 8, callback, ""},
		{"meta_access", 9, callback, ""}, {"call_unresolved", 10, callback, ""},
		{"call_edge", 14, "", "instance_construction"}, {"call_unresolved", 15, "", "instance_construction"},
		{"external_call", 16, "", "instance_construction"}, {"ambient_use", 16, "", "instance_construction"},
		{"global_ref", 16, "", "instance_construction"}, {"meta_access", 17, "", "instance_construction"},
		{"fn_ref", 18, "", "instance_construction"}, {"call_edge", 21, constructor, ""},
	} {
		rows := find(check.relation, check.line)
		if len(rows) != 1 {
			t.Errorf("%s line %d: got %v", check.relation, check.line, rows)
			continue
		}
		site := rows[0].Values[0].String
		if got := owner(site); got != check.fn {
			t.Errorf("%s line %d owner = %q, want %q", check.relation, check.line, got, check.fn)
		}
		if got := unsupported(site); got != check.reason {
			t.Errorf("%s line %d unsupported = %q, want %q", check.relation, check.line, got, check.reason)
		}
		if check.relation == "call_edge" || check.relation == "call_unresolved" || check.relation == "external_call" || check.relation == "fn_ref" {
			if got := rows[0].Values[1].String; got != check.fn {
				t.Errorf("%s line %d caller = %q, want %q", check.relation, check.line, got, check.fn)
			}
		}
	}
	if rows := find("call_edge", 5); len(rows) == 1 && rows[0].Values[2].String != known {
		t.Errorf("wrong callback target: %v", rows)
	}
	if rows := find("fn_ref", 7); len(rows) == 1 && rows[0].Values[2].String != known {
		t.Errorf("wrong referenced function: %v", rows)
	}
	if rows := find("fn_ref", 3); len(rows) == 1 && rows[0].Values[2].String != "src/scorecard/total.ts::function::total.nested" {
		t.Errorf("wrong nested function reference: %v", rows)
	}
	var valueTargets []string
	for _, row := range find("fn_ref", 28) {
		if got := owner(row.Values[0].String); got != "src/scorecard/total.ts::function::values" {
			t.Errorf("function value owner = %q: %v", got, row)
		}
		valueTargets = append(valueTargets, row.Values[2].String)
	}
	slices.Sort(valueTargets)
	if want := []string{"src/scorecard/total.ts::arrow_function::values.saved", "src/scorecard/total.ts::function_expression::values.returned"}; !slices.Equal(valueTargets, want) {
		t.Errorf("assigned and returned references = %v, want %v", valueTargets, want)
	}
	if rows := find("suspension_site", 10); len(rows) != 1 {
		t.Errorf("callback suspension: %v", rows)
	} else {
		site := rows[0].Values[0].String
		found := false
		for _, fact := range snapshot.Facts {
			if fact.Relation == "suspension_in_fn" && fact.Values[0].String == site && fact.Values[1].String == callback {
				found = true
			}
		}
		if !found || !strings.HasPrefix(site, "suspension:") {
			t.Errorf("callback suspension owner or ID: %v", rows)
		}
	}
	if rows := find("suspension_site", 25); len(rows) != 1 {
		t.Errorf("unsupported suspension: %v", rows)
	} else if reason := unsupported(rows[0].Values[0].String); reason != "instance_construction" {
		t.Errorf("unsupported suspension reason = %q", reason)
	}
	if rows := find("ambient_use", 27); len(rows) != 1 || owner(rows[0].Values[0].String) != "" || unsupported(rows[0].Values[0].String) != "" {
		t.Errorf("module ambient use: %v", rows)
	}
	choose := "src/scorecard/total.ts::function::choose"
	for _, check := range []struct {
		line             int
		relation, target string
	}{
		{30, "call_unresolved", ""},
		{31, "call_edge", fmt.Sprintf("src/scorecard/total.ts::arrow_function::<anonymous@%d>", strings.Index(source, "() => known()) as"))},
		{33, "call_edge", fmt.Sprintf("src/scorecard/total.ts::arrow_function::<anonymous@%d>", strings.Index(source, "() => known()) satisfies"))},
	} {
		var outcomes []facts.Fact
		for _, relation := range []string{"call_edge", "external_call", "call_unresolved"} {
			for _, row := range find(relation, check.line) {
				if row.Values[1].String == choose {
					outcomes = append(outcomes, row)
				}
			}
		}
		if len(outcomes) != 1 || outcomes[0].Relation != check.relation || check.target != "" && outcomes[0].Values[2].String != check.target {
			t.Errorf("choose line %d outcomes: %v", check.line, outcomes)
		}
	}
	if rows := find("fn_ref", 30); len(rows) != 2 {
		t.Errorf("conditional function values: %v", rows)
	}
	if rows := find("fn_ref", 31); len(rows) != 0 {
		t.Errorf("direct IIFE was also treated as a function value: %v", rows)
	}
	for _, fact := range snapshot.Facts {
		if fact.Relation == "fn_ref" && strings.HasSuffix(fact.Values[2].String, "::function::total.nestedAlone") {
			t.Errorf("declaration created a reference: %v", fact)
		}
		if fact.Relation == "call_edge" && fact.Values[1].String == scorecard && fact.Values[2].String == callback {
			t.Errorf("containment created a call: %v", fact)
		}
		if fact.Relation == "fn_awaits" && fact.Values[0].String != callback {
			t.Errorf("await attributed to wrong function: %v", fact)
		}
		if fact.Relation == "suspension_at_module" {
			t.Errorf("callback suspension attributed to module: %v", fact)
		}
	}
	base, err := facts.Store(snapshot.Facts)
	if err != nil {
		t.Fatal(err)
	}
	decls, prelude, err := facts.ParseSchema(snapshot.Schema)
	if err != nil {
		t.Fatal(err)
	}
	output := runtime.ProgramOutput{Text: `
Decl direct(Fn).
direct(Fn) :- fn_suspends(Fn).
direct(Fn) :- call_edge(Site, Fn, To), !site_attribution_unknown(Site), direct(To).
Decl with_refs(Fn).
with_refs(Fn) :- fn_suspends(Fn).
with_refs(Fn) :- call_edge(Site, Fn, To), !site_attribution_unknown(Site), with_refs(To).
with_refs(Fn) :- fn_ref(Site, Fn, To), !site_attribution_unknown(Site), with_refs(To).
Decl selected(Fn).
selected(Fn) :- fn(Fn, File, _), fn_exported(Fn), glob(File, "src/scorecard/**").
Decl call_only(Fn).
call_only(Fn) :- selected(Fn), direct(Fn).
Decl follow_refs(Fn).
follow_refs(Fn) :- selected(Fn), with_refs(Fn).
Decl module_seen(File).
module_seen(File) :- module_has_suspension(File).
Decl bad_owner(Site).
bad_owner(Site) :- site_attribution_unsupported(Site, _), call_edge(Site, "", _), calls_reach("", _).
Decl uncertain(Fn).
uncertain(Fn) :- fn(Fn, _, "choose"), fn_has_unresolved_call(Fn).
`}
	violations, perr := evaluateProgram(base, prelude, decls, output, []string{"call_only", "follow_refs", "module_seen", "bad_owner", "uncertain"}, nil, evalOptions{})
	if perr != nil {
		t.Fatal(perr)
	}
	var got []string
	for _, item := range violations {
		got = append(got, item.predicate+":"+item.anchor)
	}
	slices.Sort(got)
	want := []string{"follow_refs:" + scorecard, "module_seen:src/scorecard/only-unsupported.ts", "module_seen:src/scorecard/total.ts", "uncertain:" + choose}
	if !slices.Equal(got, want) {
		t.Fatalf("policy rows = %v, want %v", got, want)
	}
}
