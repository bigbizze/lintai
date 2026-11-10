package lintaiapi

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"testing"
)

const strictConfig = `{
  "compilerOptions": {
    "target": "es2022",
    "module": "preserve",
    "moduleResolution": "bundler",
    "strict": true,
    "noEmit": true,
    "baseUrl": ".",
    "paths": { "@/*": ["src/*"] }
  },
  "include": ["src/**/*.ts"]
}
`

func buildTestSnapshot(t *testing.T, files map[string]string) *Snapshot {
	t.Helper()
	root := t.TempDir()
	if _, ok := files["tsconfig.json"]; !ok {
		files["tsconfig.json"] = strictConfig
	}
	paths := writeWorkspace(t, root, files)
	snapshot, err := BuildSnapshot(context.Background(), BuildSnapshotRequest{WorkspaceRoot: root, Files: paths})
	if err != nil {
		t.Fatal(err)
	}
	return snapshot
}

func describeImport(edge ImportEdge) string {
	text := fmt.Sprintf("%s %s -> %s %s", edge.FromPath, edge.Specifier, edge.ToPath, edge.Form)
	if edge.Internal {
		text += " internal"
	}
	if edge.Unresolved {
		text += " unresolved"
	}
	if edge.Package != "" {
		text += " package=" + edge.Package
	}
	if edge.IsTypeOnly {
		text += " type"
	}
	return text
}

// Every import form makes an edge. An edge records its form, whether it
// resolves to a workspace source file, whether it resolves at all, and
// the package name for a bare specifier. Before this, only import
// declarations made edges, and an import that resolved into node_modules
// was marked internal.
func TestImportEdgesForEveryForm(t *testing.T) {
	snapshot := buildTestSnapshot(t, map[string]string{
		"node_modules/pkg/package.json": `{ "name": "pkg", "version": "1.0.0", "types": "index.d.ts" }`,
		"node_modules/pkg/index.d.ts":   "export declare function run(): void;\n",
		"node_modules/pkg/sub.d.ts":     "export declare const sub: number;\n",
		"src/types.d.ts":                "declare module \"virtual:env\" { export const mode: string; }\n",
		"src/b.ts":                      "export const b = 1;\nexport type B = number;\n",
		"src/a.ts": `import { b } from "./b";
import type { B } from "./b";
import { run } from "pkg";
import { sub } from "pkg/sub";
import { mode } from "virtual:env";
import { missing } from "./missing";
import { alias } from "@/typo";
export * from "./b";
export { b as renamed } from "./b";
export type { B as BType } from "./b";
import eq = require("./b");
const required = require("./b");
export async function lazy() { return import("./b"); }
export type Lazy = typeof import("./b");
export const used = [b, run, sub, mode, missing, alias, eq, required] as unknown as B;
`,
	})
	got := make([]string, 0, len(snapshot.ImportEdges))
	for _, edge := range snapshot.ImportEdges {
		got = append(got, describeImport(edge))
	}
	want := []string{
		"src/a.ts ./b -> src/b.ts static internal",
		"src/a.ts ./b -> src/b.ts static internal type",
		"src/a.ts ./b -> src/b.ts reexport internal",
		"src/a.ts ./b -> src/b.ts reexport internal",
		"src/a.ts ./b -> src/b.ts reexport internal type",
		"src/a.ts ./b -> src/b.ts import_equals internal",
		"src/a.ts ./b -> src/b.ts require internal",
		"src/a.ts ./b -> src/b.ts dynamic internal",
		"src/a.ts ./b -> src/b.ts import_type internal type",
		"src/a.ts ./missing -> ./missing static unresolved",
		"src/a.ts @/typo -> @/typo static unresolved",
		"src/a.ts pkg -> pkg static package=pkg",
		"src/a.ts pkg/sub -> pkg/sub static package=pkg",
		"src/a.ts virtual:env -> virtual:env static package=virtual:env",
	}
	slices.Sort(got)
	slices.Sort(want)
	if !slices.Equal(got, want) {
		t.Fatalf("import edges differ:\ngot:\n%s\nwant:\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
}

// A method call links only to the declaration that the checker resolves.
// seen.add() on a Set and ids.text() on a parameter must not link to the
// local functions add and text. Module-scope calls and calls in anonymous
// default exports have a caller. Calls to declarations outside the
// workspace sources are external calls.
func TestCallEdgesFollowTheResolvedSignature(t *testing.T) {
	snapshot := buildTestSnapshot(t, map[string]string{
		"src/a.ts": `function add(value: number): number {
  return Date.now() + value;
}
const text = (value: number): string => String(value);
export function collect(values: number[]): Set<number> {
  const seen = new Set<number>();
  for (const value of values) seen.add(value);
  return seen;
}
export function show(ids: { text(id: string): string }, id: string): string {
  return ids.text(id);
}
export function direct(): string {
  return text(add(1));
}
export function parse(raw: string): unknown {
  return JSON.parse(raw);
}
class Box {
  open(): number { return add(2); }
  handler = () => this.open();
  get size(): number { return add(5); }
  [Symbol.iterator] = () => add(6);
}
export const box = new Box();
add(3);
export default () => add(4);
`,
	})
	calls := make([]string, 0)
	for _, edge := range snapshot.CallEdges {
		from := edge.FromName
		if edge.FromModule {
			from = "<module>"
		}
		calls = append(calls, fmt.Sprintf("%s -> %s", from, edge.ToName))
	}
	slices.Sort(calls)
	wantCalls := []string{
		"<module> -> add",
		"Box.[Symbol.iterator] -> add",
		"Box.handler -> Box.open",
		"Box.open -> add",
		"Box.size -> add",
		"default -> add",
		"direct -> add",
		"direct -> text",
	}
	if !slices.Equal(calls, wantCalls) {
		t.Fatalf("call edges differ:\ngot:\n%s\nwant:\n%s", strings.Join(calls, "\n"), strings.Join(wantCalls, "\n"))
	}

	external := make([]string, 0)
	for _, call := range snapshot.ExternalCalls {
		external = append(external, fmt.Sprintf("%s %s %s", call.FromSemanticKey, call.Module, call.Name))
	}
	slices.Sort(external)
	wantExternal := []string{
		"src/a.ts::arrow_function::text lib String",
		"src/a.ts::function::add lib DateConstructor.now",
		"src/a.ts::function::collect lib Set",
		"src/a.ts::function::collect lib Set.add",
		"src/a.ts::function::parse lib JSON.parse",
	}
	if !slices.Equal(external, wantExternal) {
		t.Fatalf("external calls differ:\ngot:\n%s\nwant:\n%s", strings.Join(external, "\n"), strings.Join(wantExternal, "\n"))
	}

	unresolved := make([]string, 0)
	for _, call := range snapshot.CallsUnresolved {
		unresolved = append(unresolved, call.FromSemanticKey)
	}
	if want := []string{"src/a.ts", "src/a.ts::function::show"}; !slices.Equal(unresolved, want) {
		t.Fatalf("unresolved calls differ:\ngot  %q\nwant %q", unresolved, want)
	}
}

func TestGetterReturnedCallablesRemainUnresolved(t *testing.T) {
	snapshot := buildTestSnapshot(t, map[string]string{
		"node_modules/pkg/package.json": `{ "name": "pkg", "types": "index.d.ts" }`,
		"node_modules/pkg/index.d.ts":   "export declare class External { constructor(); get run(): () => void; method(): void; }\n",
		"src/a.ts": `import { External } from "pkg";
async function suspends() { await 0; }
const box = {
  get run() { return suspends; },
  get callback() { return async () => { await 0; }; },
  get maker() { return Worker; },
  method() {}
};
class Worker {
  constructor() {}
  get run() { return suspends; }
  method() {}
}
const worker = new Worker();
const external = new External();
export function invoke(mixed: Worker | { run(): void }) {
  box.run();
  box.callback();
  new box.maker();
  worker.run();
  box.method();
  worker.method();
  external.run();
  external.method();
  mixed.run();
}
`,
	})
	var calls []string
	for _, edge := range snapshot.CallEdges {
		calls = append(calls, fmt.Sprintf("%d: call %s", edge.Range.StartLine, edge.ToName))
	}
	for _, call := range snapshot.ExternalCalls {
		calls = append(calls, fmt.Sprintf("%d: external %s %s", call.Range.StartLine, call.Module, call.Name))
	}
	for _, call := range snapshot.CallsUnresolved {
		calls = append(calls, fmt.Sprintf("%d: unresolved", call.Range.StartLine))
	}
	slices.Sort(calls)
	want := []string{
		"14: call Worker.constructor",
		"15: external pkg External",
		"17: unresolved",
		"18: unresolved",
		"19: unresolved",
		"20: unresolved",
		"21: call box.method",
		"22: call Worker.method",
		"23: unresolved",
		"24: external pkg External.method",
		"25: unresolved",
	}
	if !slices.Equal(calls, want) {
		t.Fatalf("call outcomes differ:\ngot:\n%s\nwant:\n%s", strings.Join(calls, "\n"), strings.Join(want, "\n"))
	}
}

// Every call site has exactly one of call_edge, external_call and
// call_unresolved, so a strict rule can fail closed on calls that the
// checker does not resolve. A function used as a value is a fn_ref, and an
// import() or require() with a computed specifier is an import_opaque.
func TestStrictReachabilityFacts(t *testing.T) {
	snapshot := buildTestSnapshot(t, map[string]string{
		"node_modules/pkg/package.json": `{ "name": "pkg", "version": "1.0.0", "types": "index.d.ts" }`,
		"node_modules/pkg/index.d.ts":   "export declare function run(): void;\nexport declare const handlers: { go(): void };\n",
		"src/effect.ts":                 "export function effect(): number { return Date.now(); }\n",
		"src/a.ts": `import { effect } from "./effect";
import { run, handlers } from "pkg";
type Deps = { load(): number };
export function inject(deps: Deps, cb: () => number, table: Record<string, () => void>, loose: any): number {
  const f = deps.load;
  f();
  table["k"]();
  cb?.();
  loose();
  run();
  handlers.go();
  return deps.load();
}
export function wire(): number {
  return inject({ load: effect }, effect, {}, null);
}
export const registry = [effect];
export async function load(name: string) {
  await import(name);
  await import(` + "`./${name}`" + `);
  await import(` + "`./effect`" + `);
  return require("./" + name);
}
class Plain {}
export const plain = new Plain();
`,
		"src/b.ts": `export function hidden(read: () => number, tag: (s: TemplateStringsArray) => string): void {
  read.call(undefined);
  read.apply(null, []);
  [1].map(read);
  tag` + "`x`" + `;
  [1].map(() => 1);
  [1].filter(Boolean);
}
class Stamped {
  stamp = Date.now();
}
class Base {
  constructor() {}
}
class Quiet extends Base {
  handler = () => 1;
}
export const made = [new Stamped(), new Quiet()];
`,
	})
	callers := func(items []CallUnresolved) []string {
		result := make([]string, 0, len(items))
		for _, item := range items {
			result = append(result, fmt.Sprintf("%s:%d", item.FromSemanticKey, item.Range.StartLine))
		}
		return result
	}
	if got, want := callers(snapshot.CallsUnresolved), []string{
		"src/a.ts:25",
		"src/a.ts::function::inject:6",
		"src/a.ts::function::inject:7",
		"src/a.ts::function::inject:8",
		"src/a.ts::function::inject:9",
		"src/a.ts::function::inject:12",
		"src/b.ts:18",
		"src/b.ts::function::hidden:2",
		"src/b.ts::function::hidden:3",
		"src/b.ts::function::hidden:4",
		"src/b.ts::function::hidden:5",
	}; !slices.Equal(got, want) {
		t.Fatalf("unresolved calls differ:\ngot  %q\nwant %q", got, want)
	}

	external := make([]string, 0)
	for _, call := range snapshot.ExternalCalls {
		if call.FromPath == "src/a.ts" {
			external = append(external, call.Module+" "+call.Name)
		}
	}
	slices.Sort(external)
	if want := []string{"pkg go", "pkg run"}; !slices.Equal(external, want) {
		t.Fatalf("external calls differ:\ngot  %q\nwant %q", external, want)
	}

	refs := make([]string, 0)
	for _, ref := range snapshot.FnRefs {
		refs = append(refs, fmt.Sprintf("%s:%d", ref.SemanticKey, ref.Range.StartLine))
	}
	slices.Sort(refs)
	if want := []string{
		"src/a.ts::function::wire::refs::src/effect.ts::function::effect#2:15",
		"src/a.ts::function::wire::refs::src/effect.ts::function::effect:15",
		"src/a.ts::refs::src/effect.ts::function::effect:17",
		"src/b.ts::function::hidden::refs::src/b.ts::arrow_function::<anonymous@178>:6",
		"src/b.ts::unsupported::refs::src/b.ts::arrow_function::Quiet.handler:16",
	}; !slices.Equal(refs, want) {
		t.Fatalf("function references differ:\ngot  %q\nwant %q", refs, want)
	}

	opaque := make([]string, 0)
	for _, item := range snapshot.ImportsOpaque {
		opaque = append(opaque, fmt.Sprintf("%s:%d", item.FromPath, item.Range.StartLine))
	}
	if want := []string{"src/a.ts:19", "src/a.ts:20", "src/a.ts:22"}; !slices.Equal(opaque, want) {
		t.Fatalf("opaque imports differ:\ngot  %q\nwant %q", opaque, want)
	}
	dynamic := 0
	for _, edge := range snapshot.ImportEdges {
		if edge.Form == ImportFormDynamic {
			dynamic++
		}
	}
	if dynamic != 1 {
		t.Fatalf("want one dynamic import edge for the template literal without substitutions, got %d", dynamic)
	}
}

func TestWrappedFunctionBindings(t *testing.T) {
	const source = `interface OwnershipOnly { secret: string }
const callback = (async (value: OwnershipOnly) => { await value; });
export const exported = ((async () => { await 1; })!);
const object = { handler: (((function () { return 1; }) as () => number)!) };
class Box { handler = (<() => number>(() => 1)); }
const alias = (function original() { return 1; });
const direct = () => 1;
export const directAsync = async () => 1;
export const wrappedAsync = (async () => 1);
export const checkedAsync = (async () => 1) satisfies () => Promise<number>;
export function total(box: Box): void { void callback; void object.handler; void box.handler; void alias; void direct; }
export default ((() => 1) satisfies () => number);
`
	snapshot := buildTestSnapshot(t, map[string]string{"src/main.ts": source})
	functions := make(map[string]Function)
	for _, fn := range snapshot.Functions {
		functions[fn.SemanticKey] = fn
	}
	var anonymous []string
	for _, item := range []struct {
		fragment, token, kind string
		exported              bool
		parameters            int
	}{
		{"const callback = (async (value:", "async", "arrow_function", false, 1},
		{"export const exported = ((async", "async", "arrow_function", true, 0},
		{"handler: (((function () { return 1; })", "function", "function_expression", false, 0},
		{"handler = (<() => number>(() => 1)", "() => 1", "arrow_function", false, 0},
		{"export default ((() => 1)", "() => 1", "arrow_function", true, 0},
		{"export const wrappedAsync = (async", "async", "arrow_function", true, 0},
		{"export const checkedAsync = (async", "async", "arrow_function", true, 0},
	} {
		offset := strings.Index(source, item.fragment) + strings.Index(item.fragment, item.token)
		key := fmt.Sprintf("src/main.ts::%s::<anonymous@%d>", item.kind, offset)
		fn, ok := functions[key]
		if !ok || fn.IsExported != item.exported || fn.SignatureOmitted || fn.SignatureUnresolved || len(fn.ParameterTypes) != item.parameters || fn.ReturnType == "" || len(fn.SignatureObservations) != 1 || !fn.SignatureObservations[0].Complete {
			t.Errorf("wrapped function %q: %+v", item.fragment, fn)
		}
		anonymous = append(anonymous, key)
	}
	if direct := functions["src/main.ts::arrow_function::directAsync"]; direct.ReturnType == "" || direct.ReturnType != functions[anonymous[5]].ReturnType || direct.ReturnType != functions[anonymous[6]].ReturnType {
		t.Errorf("direct, wrapped and satisfies bindings have different return types: %q, %q, %q", direct.ReturnType, functions[anonymous[5]].ReturnType, functions[anonymous[6]].ReturnType)
	}
	for _, key := range []string{"src/main.ts::function_expression::original", "src/main.ts::arrow_function::direct"} {
		if fn, ok := functions[key]; !ok || fn.SignatureOmitted || fn.ReturnType == "" {
			t.Errorf("named function identity or signature changed: %+v", fn)
		}
	}
	wantRefs := []string{anonymous[0], anonymous[2], anonymous[3], "src/main.ts::function_expression::original", "src/main.ts::arrow_function::direct"}
	var refs []string
	for _, ref := range snapshot.FnRefs {
		if ref.FromSemanticKey == "src/main.ts::function::total" {
			refs = append(refs, ref.ToSemanticKey)
		}
	}
	slices.Sort(refs)
	slices.Sort(wantRefs)
	if !slices.Equal(refs, wantRefs) {
		t.Errorf("binding references = %v, want %v", refs, wantRefs)
	}
	var suspensionOwners []string
	for _, site := range snapshot.SuspensionSites {
		suspensionOwners = append(suspensionOwners, site.FunctionSemanticKey)
	}
	slices.Sort(suspensionOwners)
	wantOwners := []string{anonymous[0], anonymous[1]}
	slices.Sort(wantOwners)
	if !slices.Equal(suspensionOwners, wantOwners) {
		t.Errorf("suspension owners = %v, want %v", suspensionOwners, wantOwners)
	}
	if fn := functions[anonymous[0]]; len(fn.ParameterTypes) == 1 {
		var parameter *TypeFact
		for i := range snapshot.Types {
			if snapshot.Types[i].ID == fn.ParameterTypes[0] {
				parameter = &snapshot.Types[i]
				break
			}
		}
		if parameter == nil || parameter.Name != "OwnershipOnly" {
			t.Errorf("wrapped binding parameter type = %+v, want OwnershipOnly", parameter)
		}
	}
}

// Locations start at the first token. Before this, they started at the
// leading trivia, so import columns were one too small and a call after a
// comment started on an earlier line.
func TestLocationsStartAtTheFirstToken(t *testing.T) {
	snapshot := buildTestSnapshot(t, map[string]string{
		"src/b.ts": "export function b(): number { return 1; }\n",
		"src/a.ts": `import { b } from "./b";
export function a(): number {
  // comment

  return b();
}
`,
	})
	if len(snapshot.ImportEdges) != 1 || len(snapshot.CallEdges) != 1 {
		t.Fatalf("expected one import and one call, got %+v and %+v", snapshot.ImportEdges, snapshot.CallEdges)
	}
	importRange := snapshot.ImportEdges[0].Range
	if importRange.StartLine != 1 || importRange.StartColumn != 19 {
		t.Fatalf("import starts at %d:%d, want 1:19", importRange.StartLine, importRange.StartColumn)
	}
	callRange := snapshot.CallEdges[0].Range
	if callRange.StartLine != 5 || callRange.StartColumn != 10 {
		t.Fatalf("call starts at %d:%d, want 5:10", callRange.StartLine, callRange.StartColumn)
	}
}

func TestDirectivesAndParseErrors(t *testing.T) {
	snapshot := buildTestSnapshot(t, map[string]string{
		"src/client.ts": "'use client';\n\"use strict\";\nexport const x = 'not a directive';\n",
		"src/broken.ts": "export function f( {\n",
	})
	directives := make([]string, 0)
	for _, directive := range snapshot.Directives {
		directives = append(directives, fmt.Sprintf("%s %s %d:%d", directive.FilePath, directive.Text, directive.Range.StartLine, directive.Range.StartColumn))
	}
	slices.Sort(directives)
	want := []string{"src/client.ts use client 1:1", "src/client.ts use strict 2:1"}
	if !slices.Equal(directives, want) {
		t.Fatalf("directives differ: got %v, want %v", directives, want)
	}
	if len(snapshot.ParseErrors) != 1 || snapshot.ParseErrors[0].FilePath != "src/broken.ts" || snapshot.ParseErrors[0].Message == "" {
		t.Fatalf("expected one parse error in src/broken.ts, got %+v", snapshot.ParseErrors)
	}
}

// A global reference is a use of a name that resolves to the global binding.
// A local binding with the same name is not one.
func TestGlobalRefs(t *testing.T) {
	snapshot := buildTestSnapshot(t, map[string]string{
		"src/a.ts": `export function a() {
	const c = crypto;
	const w = globalThis.fetch;
	return [c, w, { Date }];
}
export function b(crypto: number) {
	const Date = 1;
	return crypto + Date;
}
`,
	})
	got := make([]string, 0)
	for _, ref := range snapshot.GlobalRefs {
		fn := ref.FunctionSemanticKey[strings.LastIndex(ref.FunctionSemanticKey, "::")+2:]
		got = append(got, fmt.Sprintf("%s %s:%d", ref.Name, fn, ref.Range.StartLine))
	}
	slices.Sort(got)
	want := []string{"Date a:4", "crypto a:2", "fetch a:3", "globalThis a:3"}
	if !slices.Equal(got, want) {
		t.Fatalf("got %q, want %q", got, want)
	}
}

// With verbatimModuleSyntax, import { type X } from "m" is emitted as
// import {} from "m", so the module loads at runtime. Without the option,
// tsc removes the import.
func TestRetainedTypeImports(t *testing.T) {
	for _, verbatim := range []bool{true, false} {
		config := strings.Replace(strictConfig, `"strict": true,`, fmt.Sprintf(`"strict": true, "verbatimModuleSyntax": %t,`, verbatim), 1)
		snapshot := buildTestSnapshot(t, map[string]string{
			"tsconfig.json": config,
			"src/m.ts":      "export type X = number;\nexport const y = 1;\n",
			"src/a.ts":      "import { type X } from \"./m\";\nimport type { X as Z } from \"./m\";\nimport { type X as W, y } from \"./m\";\nexport const v: X | Z | W = y;\n",
			"src/b.ts":      "export { type X } from \"./m\";\nexport type { X as Z } from \"./m\";\n",
		})
		retained := 0
		for _, edge := range snapshot.ImportEdges {
			if edge.Retained {
				retained++
				if edge.Range.StartLine != 1 {
					t.Fatalf("verbatim=%t: unexpected retained import %+v", verbatim, edge)
				}
			}
		}
		if want := map[bool]int{true: 2, false: 0}[verbatim]; retained != want {
			t.Fatalf("verbatim=%t: got %d retained imports, want %d", verbatim, retained, want)
		}
	}
}

// Suspension sites describe each syntax node and the context evaluating it.
func TestSuspensionSitesAndFunctionIdentity(t *testing.T) {
	const source = `interface OwnershipOnly { secret: string }
await top();
function zero(): void {}
async function noSite() {}
function promiseOnly() { return Promise.resolve(); }
async function main(arg = await defaultValue()) {
  const value = await acquire();
  await using first = await acquire(), second = acquire();
  for await (await using x of stream()) { await consume(x); }
  return [1].map(async (unused: OwnershipOnly) => { await work(); });
}
async function factory() {
  const object = { [await key()] (arg = await methodDefault()) { return 1; } };
  class WithCtor extends base(await heritage()) {
    [await fieldName()] = await field();
    @decorate(await decoratorArg()) method(@decorate(await parameterDecorator()) arg = await parameterDefault()) {}
    static field = await staticValue();
    static { await blockValue(); }
    constructor() {}
  }
  class NoCtor { field = await unsupported(); }
  class Ambiguous { field = await ambiguous(); constructor() {} constructor(x: number) {} }
  return object;
}
`
	root := t.TempDir()
	paths := writeWorkspace(t, root, map[string]string{
		"tsconfig.json":       `{"compilerOptions":{"target":"esnext","module":"preserve","noEmit":true},"files":["src/main.ts"]}`,
		"src/main.ts":         source,
		"src/excluded.ts":     `async function excluded() { await nope(); }`,
		"other/tsconfig.json": `{"compilerOptions":{"target":"esnext","module":"preserve","noEmit":true},"files":["main.ts"]}`,
		"other/main.ts":       `import "../src/main";`,
	})
	snapshot, err := BuildSnapshot(context.Background(), BuildSnapshotRequest{WorkspaceRoot: root, Files: paths, TsconfigFiles: true})
	if err != nil {
		t.Fatal(err)
	}

	callbackOffset := strings.Index(source, "async (unused:")
	callback := fmt.Sprintf("src/main.ts::arrow_function::<anonymous@%d>", callbackOffset)
	// Each snippet starts at the expected first token of the site.
	want := []struct{ text, kind, owner string }{
		{"await top()", "await", "module"},
		{"await defaultValue()", "await", "src/main.ts::function::main"},
		{"await acquire();", "await", "src/main.ts::function::main"},
		{"await using first", "await_using", "src/main.ts::function::main"},
		{"await acquire(),", "await", "src/main.ts::function::main"},
		{"for await (", "for_await", "src/main.ts::function::main"},
		{"await using x", "await_using", "src/main.ts::function::main"},
		{"await consume(x)", "await", "src/main.ts::function::main"},
		{"await work()", "await", callback},
		{"await key()", "await", "src/main.ts::function::factory"},
		{"await methodDefault()", "await", "src/main.ts::method::factory.object.[await key()]"},
		{"await heritage()", "await", "src/main.ts::function::factory"},
		{"await fieldName()", "await", "src/main.ts::function::factory"},
		{"await field()", "await", "src/main.ts::constructor::factory.WithCtor.constructor"},
		{"await decoratorArg()", "await", "src/main.ts::function::factory"},
		{"await parameterDecorator()", "await", "src/main.ts::function::factory"},
		{"await parameterDefault()", "await", "src/main.ts::method::factory.WithCtor.method"},
		{"await staticValue()", "await", "src/main.ts::function::factory"},
		{"await blockValue()", "await", "src/main.ts::function::factory"},
		{"await unsupported()", "await", "unsupported:instance_construction"},
		{"await ambiguous()", "await", "unsupported:unsupported_context"},
	}
	byKey := make(map[string]SuspensionSite)
	for _, site := range snapshot.SuspensionSites {
		byKey[site.SemanticKey] = site
	}
	if len(byKey) != len(want) || len(snapshot.SuspensionSites) != len(want) {
		t.Fatalf("unexpected suspension sites: %+v", snapshot.SuspensionSites)
	}
	for _, expected := range want {
		offset := strings.Index(source, expected.text)
		key := fmt.Sprintf("src/main.ts::suspension::%s::%d", expected.kind, offset)
		site, ok := byKey[key]
		if !ok {
			t.Errorf("missing %s at %q", key, expected.text)
			continue
		}
		classifications := 0
		if site.FunctionSemanticKey != "" {
			classifications++
		}
		if site.AtModule {
			classifications++
		}
		if site.UnsupportedReason != "" {
			classifications++
		}
		if classifications != 1 {
			t.Errorf("site %q has %d owner classifications: %+v", expected.text, classifications, site)
		}
		owner := site.FunctionSemanticKey
		if site.AtModule {
			owner = "module"
		}
		if site.UnsupportedReason != "" {
			owner = "unsupported:" + site.UnsupportedReason
		}
		line := strings.Count(source[:offset], "\n") + 1
		column := offset - strings.LastIndex(source[:offset], "\n")
		if owner != expected.owner || site.Range.File != "src/main.ts" || site.Range.StartLine != line || site.Range.StartColumn != column || site.Range.EndLine < line || site.EntityID != "suspension:"+key {
			t.Errorf("site %q: got %+v, owner %q; want owner %q at %d:%d", expected.text, site, owner, expected.owner, line, column)
		}
	}
	for _, full := range []struct{ kind, text string }{
		{"await", "await top()"},
		{"await_using", "await using first = await acquire(), second = acquire()"},
		{"for_await", "for await (await using x of stream()) { await consume(x); }"},
	} {
		start := strings.Index(source, full.text)
		end := start + len(full.text)
		site := byKey[fmt.Sprintf("src/main.ts::suspension::%s::%d", full.kind, start)]
		line := strings.Count(source[:end], "\n") + 1
		column := end - strings.LastIndex(source[:end], "\n")
		if site.Range.EndLine != line || site.Range.EndColumn != column {
			t.Errorf("end of %q: got %+v, want %d:%d", full.text, site.Range, line, column)
		}
	}
	var anonymous, zero *Function
	for i := range snapshot.Functions {
		fn := &snapshot.Functions[i]
		if fn.SemanticKey == callback {
			anonymous = fn
		}
		if fn.SemanticKey == "src/main.ts::function::zero" {
			zero = fn
		}
	}
	if anonymous == nil || !anonymous.SignatureOmitted || anonymous.SignatureUnresolved || len(anonymous.ParameterTypes) != 0 || anonymous.ReturnType != "" {
		t.Fatalf("anonymous signature was not omitted: %+v", anonymous)
	}
	if zero == nil || zero.SignatureOmitted || zero.SignatureUnresolved || len(zero.ParameterTypes) != 0 || zero.ReturnType == "" {
		t.Fatalf("zero-parameter signature was not collected: %+v", zero)
	}
	var void bool
	for _, typ := range snapshot.Types {
		if typ.ID == zero.ReturnType && typ.Kind == "void" {
			void = true
		}
		if typ.Name == "OwnershipOnly" {
			t.Fatalf("anonymous signature added a type root: %+v", typ)
		}
	}
	if !void {
		t.Fatalf("zero return type is not void: %+v", zero)
	}
}
