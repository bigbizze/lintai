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
	writeTestFile(t, workspaceRoot, "src/actions/save.ts", `import type { ActionResult } from "../core/result";
import type { ActionResult as Legacy } from "../legacy/types";
export async function save(): Promise<ActionResult<number>> { return { ok: true, value: 1 }; }
export async function legacy(): Promise<Legacy<number>> { return { done: 1 }; }
`)
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
	if got := byRule["arch.actions-return-action-result"]; len(got) != 1 || got[0] != "legacy must return Promise<ActionResult<T>>, but returns Promise<Legacy<number>>" {
		t.Fatalf("actions rule: %+v", items)
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
	want := []string{"Db.query:number", "constrained:void", "hidden:void", "legacy:Legacy<number>", "load:number", "maybe:Promise<number> | undefined", "picked:Db[K]", "plain:number", "save:ActionResult<number>", "sync:number"}
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
