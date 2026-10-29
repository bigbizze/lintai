package lintaiapi

import (
	"context"
	"fmt"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"
)

type typeIndex struct {
	t     *testing.T
	snap  *Snapshot
	types map[string]TypeFact
}

func newTypeIndex(t *testing.T, snap *Snapshot) typeIndex {
	types := make(map[string]TypeFact, len(snap.Types))
	for _, item := range snap.Types {
		if _, ok := types[item.ID]; ok {
			t.Fatalf("duplicate type entity %s", item.ID)
		}
		types[item.ID] = item
	}
	return typeIndex{t: t, snap: snap, types: types}
}

func (x typeIndex) get(id string) TypeFact {
	x.t.Helper()
	item, ok := x.types[id]
	if !ok {
		x.t.Fatalf("no type entity %q", id)
	}
	return item
}

func (x typeIndex) fn(name string) Function {
	x.t.Helper()
	for _, item := range x.snap.Functions {
		if item.Name == name {
			return item
		}
	}
	x.t.Fatalf("no function %s", name)
	return Function{}
}

func (x typeIndex) returns(name string) TypeFact {
	x.t.Helper()
	return x.get(x.fn(name).ReturnType)
}

func (x typeIndex) param(name string, index int) TypeFact {
	x.t.Helper()
	return x.get(x.fn(name).ParameterTypes[index])
}

func hasStructure(item TypeFact) bool {
	return len(item.Args) > 0 || len(item.Members) > 0 || len(item.Props) > 0 || len(item.SigParams) > 0 || item.SigReturns != "" || item.Literal != nil
}

// Types are entities keyed by where each named type is declared, not by
// the checker's text. Before this, fn_param and fn_returns held
// TypeToString output, so rules matched on text that changes between
// compiler versions and does not tell two types with the same name apart.
func TestTypeFacts(t *testing.T) {
	snap := buildTestSnapshot(t, map[string]string{
		"src/core/result.ts":  "export type ActionResult<T> = { ok: true; value: T } | { ok: false; error: string };\n",
		"src/legacy/types.ts": "export type ActionResult<T> = { done: T };\n",
		"src/box.ts":          "export class Box<T> { constructor(public value: T) {} }\nexport enum Color { Red, Green }\n",
		"src/tree.ts":         "export type Tree = { kids: Tree[] };\nexport type Deep<T> = { next(): Deep<{ v: T }> };\nexport type Wide<T> = { next(): Wide<{ a: T; b: T; c: T }> };\nexport type Pair = [string, number];\n",
		"src/cond.ts":         "export type Unwrap<T> = T extends Promise<infer U> ? U : T;\n",
		"src/ab.ts":           "export type A = { a: number };\nexport type B = { b: string };\n",
		"src/actions.ts": `import type { ActionResult } from "./core/result";
import type { ActionResult as LegacyResult } from "./legacy/types";
import { Box, Color } from "./box";
import type { Tree, Deep, Wide, Pair } from "./tree";
import type { Unwrap } from "./cond";
import type { A, B } from "./ab";
export async function save(): Promise<ActionResult<number>> { return { ok: true, value: 1 }; }
export function legacy(): LegacyResult<number> { return { done: 1 }; }
export function ab(x: A | B): void {}
export function ba(x: B | A): void {}
export function flag(x: boolean): void {}
export function box(x: Box<string>): void {}
export function list(x: string[]): void {}
export function tuple(x: [string, number?]): void {}
export function callback(x: (n: number) => void): void {}
export function options(x: { name: string; size?: number }): void {}
export function letter(x: "a" | "b"): void {}
export function color(x: Color): void {}
export function identity<T>(x: T): T { return x; }
export const first = <T>(x: T): T => x;
export const second = <T>(x: T): T => x;
export function half(x: 0.5 | 2): void {}
export function tree(x: Tree): void {}
export function deep(x: Deep<string>): void {}
export function wide(x: Wide<string>): void {}
export function pair(x: Pair): void {}
export function quoted(x: { "a?": string }, y: { a?: string }): void {}
export function localA() { type R = { a: number }; return null as unknown as R; }
export function localB() { type R = { b: string }; return null as unknown as R; }
export function unwrap<T>(x: Unwrap<T>): T extends string ? 1 : 2 { return 1 as never; }
`,
	})
	x := newTypeIndex(t, snap)

	// 1. Promise<ActionResult<number>>.
	promise := x.returns("save")
	if !promise.Named || promise.Origin != "lib" || promise.Name != "Promise" || len(promise.Args) != 1 {
		t.Fatalf("save returns %+v", promise)
	}
	result := x.get(promise.Args[0])
	if !result.Named || result.Origin != "src/core/result.ts" || result.Name != "ActionResult" || result.Kind != TypeKindUnion {
		t.Fatalf("Promise argument %+v", result)
	}
	if len(result.Args) != 1 || x.get(result.Args[0]).Kind != TypeKindNumber || len(result.Members) != 2 {
		t.Fatalf("ActionResult<number> args %v members %v", result.Args, result.Members)
	}
	if result.Location == nil || result.Location.File != "src/core/result.ts" || result.Location.StartLine != 1 || result.Location.StartColumn != 13 {
		t.Fatalf("ActionResult location %+v", result.Location)
	}
	if promise.Location != nil {
		t.Fatalf("a lib type has no location, got %+v", promise.Location)
	}

	// 2. The same name in two files is two entities.
	legacy := x.returns("legacy")
	if legacy.Name != "ActionResult" || legacy.Origin != "src/legacy/types.ts" || legacy.ID == result.ID {
		t.Fatalf("legacy ActionResult %+v", legacy)
	}

	// 3. Member order does not change the ID.
	if x.fn("ab").ParameterTypes[0] != x.fn("ba").ParameterTypes[0] {
		t.Fatalf("A | B and B | A differ: %+v %+v", x.param("ab", 0), x.param("ba", 0))
	}

	// 4. boolean is not the union true | false.
	if flag := x.param("flag", 0); flag.Kind != TypeKindBoolean || hasStructure(flag) {
		t.Fatalf("boolean %+v", flag)
	}

	// 5. The this-type argument of a class reference is dropped.
	if box := x.param("box", 0); box.Kind != TypeKindObject || box.Name != "Box" || box.Origin != "src/box.ts" || len(box.Args) != 1 || x.get(box.Args[0]).Kind != TypeKindString {
		t.Fatalf("Box<string> %+v", box)
	}

	// 6. Arrays are the lib interface Array.
	if list := x.param("list", 0); list.Origin != "lib" || list.Name != "Array" || len(list.Args) != 1 || x.get(list.Args[0]).Kind != TypeKindString {
		t.Fatalf("string[] %+v", list)
	}

	// 7. A tuple and a function type.
	tuple := x.param("tuple", 0)
	if tuple.Kind != TypeKindTuple || tuple.Named || len(tuple.Args) != 2 || x.get(tuple.Args[0]).Kind != TypeKindString || x.get(tuple.Args[1]).Kind != TypeKindNumber {
		t.Fatalf("[string, number?] %+v", tuple)
	}
	callback := x.param("callback", 0)
	if callback.Kind != TypeKindFunction || len(callback.SigParams) != 1 || x.get(callback.SigParams[0]).Kind != TypeKindNumber || x.get(callback.SigReturns).Kind != TypeKindVoid {
		t.Fatalf("(n: number) => void %+v", callback)
	}

	// 8. An anonymous object type with an optional property.
	options := x.param("options", 0)
	if options.Kind != TypeKindObject || options.Named || len(options.Props) != 2 {
		t.Fatalf("object type %+v", options)
	}
	for _, prop := range options.Props {
		want := map[string]string{"name": TypeKindString, "size": TypeKindNumber}[prop.Name]
		if x.get(prop.Type).Kind != want || prop.Optional != (prop.Name == "size") {
			t.Fatalf("property %+v", prop)
		}
	}

	// 9. A literal union.
	letter := x.param("letter", 0)
	values := make([]string, 0, 2)
	for _, member := range letter.Members {
		item := x.get(member)
		if item.Kind != TypeKindLiteral || item.Literal == nil || item.Literal.Kind != LiteralString {
			t.Fatalf("literal member %+v", item)
		}
		values = append(values, item.Literal.Text)
	}
	slices.Sort(values)
	if letter.Kind != TypeKindUnion || !slices.Equal(values, []string{"a", "b"}) {
		t.Fatalf(`"a" | "b" %+v %v`, letter, values)
	}

	// 10. An enum and a type parameter.
	if color := x.param("color", 0); color.Kind != TypeKindObject || color.Name != "Color" || color.Origin != "src/box.ts" || hasStructure(color) {
		t.Fatalf("enum %+v", color)
	}
	param := x.param("identity", 0)
	if param.Kind != TypeKindTypeParam || param.ParamName != "T" || x.fn("identity").ReturnType != param.ID {
		t.Fatalf("type parameter %+v", param)
	}

	// Anonymous generic functions do not share type parameter entities.
	if x.fn("first").ParameterTypes[0] == x.fn("second").ParameterTypes[0] {
		t.Fatalf("the T of two arrow functions is one entity: %+v", x.param("first", 0))
	}

	// A number literal can be a float.
	numbers := make([]float64, 0, 2)
	for _, member := range x.param("half", 0).Members {
		numbers = append(numbers, x.get(member).Literal.Number)
	}
	slices.Sort(numbers)
	if !slices.Equal(numbers, []float64{0.5, 2}) {
		t.Fatalf("0.5 | 2 %v", numbers)
	}

	// 11. A recursive alias gives a finite fact set.
	tree := x.param("tree", 0)
	if tree.Name != "Tree" || tree.Kind != TypeKindObject || len(tree.Props) != 1 {
		t.Fatalf("Tree %+v", tree)
	}
	kids := x.get(tree.Props[0].Type)
	if kids.Name != "Array" || len(kids.Args) != 1 || kids.Args[0] != tree.ID {
		t.Fatalf("Tree.kids %+v", kids)
	}

	// A type that makes a new type at each level stops at the depth limit.
	if len(snap.Types) > 200 {
		t.Fatalf("expected a bounded fact set, got %d types", len(snap.Types))
	}
	if deep := x.param("deep", 0); deep.Name != "Deep" || deep.Kind != TypeKindObject {
		t.Fatalf("Deep<string> %+v", deep)
	}

	// A type that uses its argument several times at each level has a
	// display text and a key that grow exponentially with depth. Before the
	// limits, this one type took seconds and megabytes of text.
	if wide := x.param("wide", 0); wide.Name != "Wide" || len(wide.Display) > 4*maxTypeDisplayLength {
		t.Fatalf("Wide<string> %s %d", wide.Name, len(wide.Display))
	}
	for _, item := range snap.Types {
		if len(item.Display) > 4*maxTypeDisplayLength {
			t.Fatalf("display text of %d bytes", len(item.Display))
		}
	}

	// A tuple alias without type parameters has its elements.
	if pair := x.param("pair", 0); pair.Kind != TypeKindTuple || pair.Name != "Pair" || len(pair.Args) != 2 {
		t.Fatalf("Pair %+v", pair)
	}

	// A quoted property name that looks like an optional name is a
	// different type.
	if x.fn("quoted").ParameterTypes[0] == x.fn("quoted").ParameterTypes[1] {
		t.Fatalf(`{ "a?": string } and { a?: string } are one entity`)
	}

	// Two local types with the same name in one file are two entities.
	localA, localB := x.returns("localA"), x.returns("localB")
	if localA.Name != "R" || localB.Name != "R" || localA.ID == localB.ID {
		t.Fatalf("local types %+v %+v", localA, localB)
	}

	// 12. A conditional type is /other with display text and no structure
	// facts. An alias of one is named, and its kind is /other too.
	conditional := x.returns("unwrap")
	if conditional.Kind != TypeKindOther || conditional.Named || hasStructure(conditional) || conditional.Display != "T extends string ? 1 : 2" {
		t.Fatalf("conditional %+v", conditional)
	}
	if aliased := x.param("unwrap", 0); aliased.Kind != TypeKindOther || aliased.Name != "Unwrap" || len(aliased.Args) != 1 || x.get(aliased.Args[0]).Kind != TypeKindTypeParam {
		t.Fatalf("aliased conditional %+v", aliased)
	}
}

// A type from packages/core/dist/*.d.ts and the same type from
// packages/core/src/*.ts are one entity, as calls through dist/ resolve to
// the source function.
func TestTypeFactsMapDistDeclarationsToSource(t *testing.T) {
	snap := buildTestSnapshot(t, map[string]string{
		"packages/core/src/result.ts":    "export type Result = { ok: boolean };\n",
		"packages/core/dist/result.d.ts": "export type Result = { ok: boolean };\n",
		"src/a.ts": `import type { Result } from "../packages/core/src/result";
import type { Result as Built } from "../packages/core/dist/result";
export function fromSource(x: Result): void {}
export function fromDist(x: Built): void {}
`,
	})
	x := newTypeIndex(t, snap)
	source := x.param("fromSource", 0)
	if source.Origin != "packages/core/src/result.ts" || source.ID != x.fn("fromDist").ParameterTypes[0] {
		t.Fatalf("source %+v dist %+v", source, x.param("fromDist", 0))
	}
}

// Each config group has its own checker, so the same type is a different
// checker object in each group. Its ID, facts and display text must not
// depend on the group. Files and groups are sorted by path, so the test
// renames a group to change the order.
func TestTypeFactsAcrossConfigGroups(t *testing.T) {
	build := func(first, second string) *Snapshot {
		files := map[string]string{
			"shared/model.ts":         "export type Model = { id: string; tags: string[] };\n",
			first + "/tsconfig.json":  `{"compilerOptions": {"strict": true}, "include": ["**/*.ts", "../shared/**/*.ts"]}`,
			first + "/use.ts":         "import type { Model } from \"../shared/model\";\nexport function load(x: Model): Model | null { return x; }\n",
			second + "/tsconfig.json": `{"compilerOptions": {"strict": true}, "include": ["**/*.ts", "../shared/**/*.ts"]}`,
			second + "/use.ts":        "import type { Model } from \"../shared/model\";\nexport function save(x: Model | null): void {}\n",
			"tsconfig.json":           `{"files": [], "references": [{"path": "./` + first + `"}, {"path": "./` + second + `"}]}`,
		}
		root := t.TempDir()
		snap, err := BuildSnapshot(context.Background(), BuildSnapshotRequest{WorkspaceRoot: root, Files: writeWorkspace(t, root, files)})
		if err != nil {
			t.Fatal(err)
		}
		return snap
	}
	before := build("app", "zadmin")
	after := build("zapp", "admin")
	x := newTypeIndex(t, before)
	load := x.fn("load")
	save := x.fn("save")
	if x.get(load.ParameterTypes[0]).Name != "Model" || load.ReturnType != save.ParameterTypes[0] {
		t.Fatalf("load %+v save %+v", load, save)
	}
	if !strings.HasPrefix(filepath.ToSlash(x.get(load.ParameterTypes[0]).Origin), "shared/") {
		t.Fatalf("origin %q", x.get(load.ParameterTypes[0]).Origin)
	}
	if !reflect.DeepEqual(before.Types, after.Types) {
		t.Fatalf("the group order changed the type facts:\n%+v\n%+v", before.Types, after.Types)
	}
}

// One declaration compiled under strict and non-strict options has
// different structure in each group. 0.6.0 kept the structure of the group
// that sorted first. The merged fact now has no structure, is marked as a
// conflict, and does not depend on the group order.
func TestTypeFactsConflictAcrossConfigGroups(t *testing.T) {
	build := func(loose, strict string) *Snapshot {
		files := map[string]string{
			"shared/r.ts":             "export type R = { a: string | undefined };\n",
			loose + "/tsconfig.json":  `{"compilerOptions": {"strict": false}, "include": ["**/*.ts", "../shared/**/*.ts"]}`,
			loose + "/use.ts":         "import type { R } from \"../shared/r\";\nexport function fa(x: R): void {}\n",
			strict + "/tsconfig.json": `{"compilerOptions": {"strict": true}, "include": ["**/*.ts", "../shared/**/*.ts"]}`,
			strict + "/use.ts":        "import type { R } from \"../shared/r\";\nexport function fb(x: R): void {}\n",
			"tsconfig.json":           `{"files": [], "references": [{"path": "./` + loose + `"}, {"path": "./` + strict + `"}]}`,
		}
		root := t.TempDir()
		snap, err := BuildSnapshot(context.Background(), BuildSnapshotRequest{WorkspaceRoot: root, Files: writeWorkspace(t, root, files)})
		if err != nil {
			t.Fatal(err)
		}
		return snap
	}
	looseFirst := build("a", "b")
	strictFirst := build("c", "b")
	x := newTypeIndex(t, looseFirst)
	r := x.param("fa", 0)
	if r.ID != x.fn("fb").ParameterTypes[0] || r.Name != "R" || len(r.Props) != 0 || !slices.Equal(r.Incomplete, []string{IncompleteConflict}) {
		t.Fatalf("R %+v", r)
	}
	y := newTypeIndex(t, strictFirst)
	if !reflect.DeepEqual(r, y.param("fa", 0)) {
		t.Fatalf("the group order changed R:\n%+v\n%+v", r, y.param("fa", 0))
	}
}

// A ring of function types that recurse through each other is keyed once
// from inside the ring and once from a deeper entry function. In 0.6.0 a
// key that a deep walk truncated was cached and reused for a shallower
// walk, so the IDs of the ring depended on which file sorted first.
func TestTypeFactsRecursiveKeysDoNotDependOnFileNames(t *testing.T) {
	var ring strings.Builder
	for index := range 6 {
		fmt.Fprintf(&ring, "export function a%d() { return { n: a%d }; }\n", index, (index+1)%6)
	}
	build := func(entryFile string, depth int) []string {
		entry := "import { a0 } from \"./r\";\nexport function entry() { return " + strings.Repeat("{ n: ", depth) + "a0" + strings.Repeat(" }", depth) + "; }\n"
		snap := buildTestSnapshot(t, map[string]string{"src/r.ts": ring.String(), "src/" + entryFile: entry})
		x := newTypeIndex(t, snap)
		ids := make([]string, 0, 6)
		for index := range 6 {
			ids = append(ids, x.fn(fmt.Sprintf("a%d", index)).ReturnType)
		}
		return ids
	}
	for depth := 5; depth <= 12; depth++ {
		before, after := build("a.ts", depth), build("z.ts", depth)
		if !slices.Equal(before, after) {
			t.Fatalf("depth %d: the ring IDs depend on the entry file name:\n%v\n%v", depth, before, after)
		}
	}
}

// Keys are JSON arrays with an origin kind. In 0.6.0 a declare module
// "lib" block had the origin of the real lib, the T of the generic methods
// of two object literals had one ID, and readonly was not in the key.
func TestTypeKeyFraming(t *testing.T) {
	snap := buildTestSnapshot(t, map[string]string{
		"src/fake.d.ts":      "declare module \"lib\" { export interface Promise<T> { fake: T } }\ndeclare module \"src/core/result.ts\" { export type ActionResult<T> = { fake: T } }\n",
		"src/core/result.ts": "export type ActionResult<T> = { ok: true; value: T };\n",
		"src/use.ts": `import type { Promise as FakePromise } from "lib";
import type { ActionResult as FakeResult } from "src/core/result.ts";
import type { ActionResult } from "./core/result";
export async function real(): Promise<number> { return 1; }
export function fake(): FakePromise<number> { return null as never; }
export function realResult(): ActionResult<number> { return null as never; }
export function fakeResult(): FakeResult<number> { return null as never; }
export const a = { m<T>(x: T): T { return x; } };
export const b = { m<T>(x: T): T { return x; } };
export function readonlyTuple(x: readonly [string, number]): void {}
export function mutableTuple(x: [string, number]): void {}
export function readonlyProp(x: { readonly a: string }): void {}
export function mutableProp(x: { a: string }): void {}
`,
	})
	x := newTypeIndex(t, snap)
	real, fake := x.returns("real"), x.returns("fake")
	if real.ID == fake.ID || real.OriginKind != OriginKindLib || fake.OriginKind != OriginKindAmbientModule {
		t.Fatalf("lib Promise %+v and declare module Promise %+v", real, fake)
	}
	realResult, fakeResult := x.returns("realResult"), x.returns("fakeResult")
	if realResult.ID == fakeResult.ID || realResult.OriginKind != OriginKindSourceFile || fakeResult.OriginKind != OriginKindAmbientModule {
		t.Fatalf("workspace ActionResult %+v and declare module ActionResult %+v", realResult, fakeResult)
	}
	var methods []Function
	for _, item := range snap.Functions {
		if item.Name == "m" || strings.HasSuffix(item.Name, ".m") {
			methods = append(methods, item)
		}
	}
	if len(methods) != 2 || methods[0].ParameterTypes[0] == methods[1].ParameterTypes[0] {
		t.Fatalf("the T of two object literal methods is one entity: %+v", methods)
	}
	readonlyTuple, mutableTuple := x.param("readonlyTuple", 0), x.param("mutableTuple", 0)
	if readonlyTuple.ID == mutableTuple.ID || !readonlyTuple.Readonly || mutableTuple.Readonly {
		t.Fatalf("readonly tuple %+v mutable tuple %+v", readonlyTuple, mutableTuple)
	}
	readonlyProp, mutableProp := x.param("readonlyProp", 0), x.param("mutableProp", 0)
	if readonlyProp.ID == mutableProp.ID || !readonlyProp.Props[0].Readonly || mutableProp.Props[0].Readonly {
		t.Fatalf("readonly property %+v mutable property %+v", readonlyProp, mutableProp)
	}
}

// Types whose structure lintai does not emit are marked, and types that
// 0.6.0 collapsed into one ID get their own IDs.
func TestTypeFactsIncomplete(t *testing.T) {
	nest := func(leaf string) string {
		text := leaf
		for range 18 {
			text = "{ a: " + text + " }"
		}
		return text
	}
	snap := buildTestSnapshot(t, map[string]string{
		"src/a.ts":         "export const tag: unique symbol = Symbol();\n",
		"src/b.ts":         "export const tag: unique symbol = Symbol();\n",
		"src/infra/db.ts":  "export type Db = { users: string; posts: number };\n",
		"src/legacy/db.ts": "export type Db = { users: string; posts: number };\n",
		"src/use.ts": `import { tag as tagA } from "./a";
import { tag as tagB } from "./b";
import type { Db } from "./infra/db";
import type { Db as LegacyDb } from "./legacy/db";
export function deepString(x: ` + nest("string") + `): void {}
export function deepNumber(x: ` + nest("number") + `): void {}
export function symbolA(x: typeof tagA): void {}
export function symbolB(x: typeof tagB): void {}
export function get<K extends keyof Db>(k: K): Db[K] { return null as never; }
export function legacyGet<K extends keyof LegacyDb>(k: K): LegacyDb[K] { return null as never; }
export function constrained<T extends Db>(x: T): void {}
export function plain<T>(x: T): void {}
export function indexed(x: { [key: string]: Db }): void {}
`,
	})
	x := newTypeIndex(t, snap)
	incomplete := func(item TypeFact, reason string) bool { return slices.Contains(item.Incomplete, reason) }

	deepString, deepNumber := x.param("deepString", 0), x.param("deepNumber", 0)
	if deepString.ID == deepNumber.ID || !incomplete(deepString, IncompleteTruncated) || !incomplete(deepNumber, IncompleteTruncated) {
		t.Fatalf("truncated types %+v %+v", deepString, deepNumber)
	}
	symbolA, symbolB := x.param("symbolA", 0), x.param("symbolB", 0)
	if symbolA.ID == symbolB.ID || !incomplete(symbolA, IncompleteUnsupported) {
		t.Fatalf("unique symbol types %+v %+v", symbolA, symbolB)
	}
	get, legacyGet := x.returns("get"), x.returns("legacyGet")
	if get.ID == legacyGet.ID || !incomplete(get, IncompleteUnsupported) {
		t.Fatalf("indexed access types %+v %+v", get, legacyGet)
	}
	if constrained := x.param("constrained", 0); !incomplete(constrained, IncompleteConstraint) {
		t.Fatalf("constrained type parameter %+v", constrained)
	}
	if plain := x.param("plain", 0); len(plain.Incomplete) != 0 {
		t.Fatalf("type parameter without a constraint %+v", plain)
	}
	if indexed := x.param("indexed", 0); !incomplete(indexed, IncompleteSignatures) {
		t.Fatalf("object with an index signature %+v", indexed)
	}
}

// Optional and rest tuple elements and signature parameters were in the
// key only in 0.6.0, with no relation that a rule could read.
func TestTypeFactsMarks(t *testing.T) {
	snap := buildTestSnapshot(t, map[string]string{
		"src/use.ts": `export function optionalElement(x: [string, number?]): void {}
export function restElement(x: [string, ...number[]]): void {}
export function restParam(x: (a: string, ...b: number[]) => void): void {}
export function optionalParam(x: (a: string, b?: number) => void): void {}
`,
	})
	x := newTypeIndex(t, snap)
	if item := x.param("optionalElement", 0); !slices.Equal(item.ArgMarks, []string{"", MarkOptional}) {
		t.Fatalf("[string, number?] %+v", item)
	}
	if item := x.param("restElement", 0); !slices.Equal(item.ArgMarks, []string{"", MarkRest}) {
		t.Fatalf("[string, ...number[]] %+v", item)
	}
	if item := x.param("restParam", 0); !slices.Equal(item.SigMarks, []string{"", MarkRest}) {
		t.Fatalf("(a, ...b) => void %+v", item)
	}
	if item := x.param("optionalParam", 0); !slices.Equal(item.SigMarks, []string{"", MarkOptional}) {
		t.Fatalf("(a, b?) => void %+v", item)
	}
}
