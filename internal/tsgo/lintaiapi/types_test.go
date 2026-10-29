package lintaiapi

import (
	"context"
	"path/filepath"
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
		"src/tree.ts":         "export type Tree = { kids: Tree[] };\nexport type Deep<T> = { next(): Deep<{ v: T }> };\n",
		"src/cond.ts":         "export type Unwrap<T> = T extends Promise<infer U> ? U : T;\n",
		"src/ab.ts":           "export type A = { a: number };\nexport type B = { b: string };\n",
		"src/actions.ts": `import type { ActionResult } from "./core/result";
import type { ActionResult as LegacyResult } from "./legacy/types";
import { Box, Color } from "./box";
import type { Tree, Deep } from "./tree";
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
export function tree(x: Tree): void {}
export function deep(x: Deep<string>): void {}
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
// depend on the group or on file order.
func TestTypeFactsAcrossConfigGroupsAndFileOrder(t *testing.T) {
	files := map[string]string{
		"shared/model.ts":     "export type Model = { id: string; tags: string[] };\n",
		"app/tsconfig.json":   `{"compilerOptions": {"strict": true}, "include": ["**/*.ts", "../shared/**/*.ts"]}`,
		"app/use.ts":          "import type { Model } from \"../shared/model\";\nexport function load(x: Model): Model | null { return x; }\n",
		"admin/tsconfig.json": `{"compilerOptions": {"strict": true}, "include": ["**/*.ts", "../shared/**/*.ts"]}`,
		"admin/use.ts":        "import type { Model } from \"../shared/model\";\nexport function save(x: Model | null): void {}\n",
		"tsconfig.json":       `{"files": [], "references": [{"path": "./app"}, {"path": "./admin"}]}`,
	}
	root := t.TempDir()
	paths := writeWorkspace(t, root, files)
	build := func(order []string) *Snapshot {
		snap, err := BuildSnapshot(context.Background(), BuildSnapshotRequest{WorkspaceRoot: root, Files: order})
		if err != nil {
			t.Fatal(err)
		}
		return snap
	}
	first := build(paths)
	reversed := slices.Clone(paths)
	slices.Reverse(reversed)
	second := build(reversed)

	x := newTypeIndex(t, first)
	load := x.fn("load")
	save := x.fn("save")
	if x.get(load.ParameterTypes[0]).Name != "Model" || load.ReturnType != save.ParameterTypes[0] {
		t.Fatalf("load %+v save %+v", load, save)
	}
	if !strings.HasPrefix(filepath.ToSlash(x.get(load.ParameterTypes[0]).Origin), "shared/") {
		t.Fatalf("origin %q", x.get(load.ParameterTypes[0]).Origin)
	}
	if !slices.EqualFunc(first.Types, second.Types, func(a, b TypeFact) bool {
		return a.ID == b.ID && a.Display == b.Display && a.Kind == b.Kind
	}) {
		t.Fatalf("file order changed the type facts:\n%+v\n%+v", first.Types, second.Types)
	}
}
