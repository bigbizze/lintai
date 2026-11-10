package lintaiapi

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"testing"
)

func TestConflictSourceBindingsWithMaps(t *testing.T) {
	bindings := []SourceBindingFact{
		{CheckedDeclarations: []string{"a", "b"}, SourceDeclarations: []string{"one", "two"}},
		{CheckedDeclarations: []string{"a"}, SourceDeclarations: []string{"other"}, Incomplete: []string{"unresolved"}},
		{CheckedDeclarations: []string{"a"}},
		{CheckedDeclarations: []string{"unlinked"}, SourceDeclarations: []string{"other"}},
	}
	links := []DeclarationSourceFact{
		{CheckedDecl: "a", SourceDecl: "one"},
		{CheckedDecl: "b", SourceDecl: "two"},
		{CheckedDecl: "a", SourceDecl: "two"},
		{CheckedDecl: "unrelated", SourceDecl: "other"},
	}
	got := conflictSourceBindingsWithMaps(bindings, links)
	if len(got[0].Incomplete) != 0 || !slices.Equal(got[1].Incomplete, []string{AssociationConflict, "unresolved"}) || len(got[2].Incomplete) != 0 || len(got[3].Incomplete) != 0 {
		t.Fatalf("unexpected binding conflicts: %+v", got)
	}
}

func TestSemanticExportsKeepCheckerBindingsAndContributionProvenance(t *testing.T) {
	snapshot := buildTestSnapshot(t, map[string]string{
		"src/base.ts": `export interface Token { value: string }
export class Both {}
export const value = 1;
export const shared = 2;
export const fnValue = () => 1;
export default class DefaultThing {}
export function parse(input: string): string;
export function parse(input: number): number;
export function parse(input: string | number): string | number { return input; }
export interface Merged { left: string }
export interface Merged { right: number }
`,
		"src/pair.ts": `export { value as left, Both as right } from "./base";`,
		"src/index.ts": `export { left as first, right as second } from "./pair";
export { type Both as TypeOnlyBoth } from "./base";
export { MissingTypeOrValue as MissingNamed } from "./missing-named";
export const shared = "local";
export * from "./base";
export type Primitive = string;
`,
		"src/defaults.ts": `export { default as NamedDefault } from "./base";
export default class {}
`,
		"src/namespace.ts":            `export * as all from "./base";`,
		"src/namespace-incomplete.ts": `export * as child from "./star-a";`,
		"src/star-a.ts":               `export * from "./star-b"; export const known = 1;`,
		"src/star-b.ts":               `export * from "./star-c";`,
		"src/star-c.ts":               `export * from "./star-a"; export * from "./missing"; export const leaf = 1;`,
		"src/star-root.ts":            `export * from "./star-a";`,
		"src/cjs.ts":                  `const api = { run() {} }; export = api;`,
	})

	first := semanticExport(t, snapshot, "src/index.ts", "first", SpaceValue)
	second := semanticExport(t, snapshot, "src/index.ts", "second", SpaceValue)
	if len(first.Reexports) != 1 || first.Reexports[0].Module != "src/pair.ts" || first.Reexports[0].SourceName != "left" {
		t.Fatalf("first renamed binding provenance: %+v", first)
	}
	if len(second.Reexports) != 1 || second.Reexports[0].Module != "src/pair.ts" || second.Reexports[0].SourceName != "right" {
		t.Fatalf("second renamed binding provenance: %+v", second)
	}

	if _, found := findSemanticExport(snapshot, "src/index.ts", "TypeOnlyBoth", SpaceValue); found {
		t.Fatal("type-only export created a value-space binding")
	}
	semanticExport(t, snapshot, "src/index.ts", "TypeOnlyBoth", SpaceType)
	semanticExport(t, snapshot, "src/index.ts", "Both", SpaceType)
	semanticExport(t, snapshot, "src/index.ts", "Both", SpaceValue)
	for _, space := range []string{SpaceType, SpaceValue} {
		missing := semanticExport(t, snapshot, "src/index.ts", "MissingNamed", space)
		if len(missing.Targets) != 0 || !slices.Equal(missing.Incomplete, []string{AssociationUnresolved}) {
			t.Fatalf("unresolved named re-export guessed a target or public space: %+v", missing)
		}
	}
	if _, found := findSemanticExport(snapshot, "src/index.ts", "default", SpaceValue); found {
		t.Fatal("star export included the source default binding")
	}

	shared := semanticExport(t, snapshot, "src/index.ts", "shared", SpaceValue)
	if len(shared.Reexports) != 0 || len(shared.Targets) != 1 {
		t.Fatalf("explicit export override did not own the binding: %+v", shared)
	}
	if len(shared.Via) != 1 {
		t.Fatalf("explicit override retained unrelated star provenance: %v", shared.Via)
	}

	defaultClass := semanticExport(t, snapshot, "src/defaults.ts", "default", SpaceType)
	if len(defaultClass.Targets) != 1 {
		t.Fatalf("anonymous default class target: %+v", defaultClass)
	}
	semanticExport(t, snapshot, "src/defaults.ts", "default", SpaceValue)
	semanticExport(t, snapshot, "src/defaults.ts", "NamedDefault", SpaceValue)
	namespace := semanticExport(t, snapshot, "src/namespace.ts", "all", SpaceValue)
	if len(namespace.Targets) != 1 || len(namespace.Incomplete) != 0 || semanticDeclaration(t, snapshot, namespace.Targets[0]).Kind != "namespace" {
		t.Fatalf("namespace export did not retain a complete namespace binding: %+v", namespace)
	}
	semanticExport(t, snapshot, "src/cjs.ts", "export=", SpaceValue)
	if _, found := findSemanticExport(snapshot, "src/cjs.ts", "run", SpaceValue); found {
		t.Fatal("export = recursively invented a property export")
	}

	functionValue := semanticExport(t, snapshot, "src/index.ts", "fnValue", SpaceValue)
	if len(functionValue.Targets) != 1 || semanticDeclaration(t, snapshot, functionValue.Targets[0]).Kind != "variable" {
		t.Fatalf("function-valued export target was not its variable declaration: %+v", functionValue)
	}
	parse := semanticExport(t, snapshot, "src/base.ts", "parse", SpaceValue)
	if len(parse.Targets) != 3 {
		t.Fatalf("overload signatures and implementation were not separate targets: %+v", parse)
	}
	merged := semanticExport(t, snapshot, "src/base.ts", "Merged", SpaceType)
	if len(merged.Targets) != 2 {
		t.Fatalf("merged interface declarations were not separate targets: %+v", merged)
	}
	primitive := semanticExport(t, snapshot, "src/index.ts", "Primitive", SpaceType)
	if len(primitive.Targets) != 1 || len(semanticDeclaration(t, snapshot, primitive.Targets[0]).Types) == 0 {
		t.Fatalf("unused primitive type alias was not rooted in export facts: %+v", primitive)
	}

	for _, file := range []string{"src/star-a.ts", "src/star-b.ts", "src/star-c.ts", "src/star-root.ts"} {
		if !slices.ContainsFunc(snapshot.ModuleExportsIncomplete, func(item ModuleExportsIncompleteFact) bool {
			return item.File == file && item.Reason == AssociationUnresolved
		}) {
			t.Fatalf("unresolved star inventory was not propagated to %s: %+v", file, snapshot.ModuleExportsIncomplete)
		}
	}
	semanticExport(t, snapshot, "src/star-root.ts", "leaf", SpaceValue)
	semanticExport(t, snapshot, "src/namespace-incomplete.ts", "child", SpaceValue)
	if slices.ContainsFunc(snapshot.ModuleExportsIncomplete, func(item ModuleExportsIncompleteFact) bool {
		return item.File == "src/namespace-incomplete.ts"
	}) {
		t.Fatalf("namespace source's incomplete inventory propagated to its single binding: %+v", snapshot.ModuleExportsIncomplete)
	}
	if !slices.ContainsFunc(snapshot.ImportEdges, func(edge ImportEdge) bool {
		return edge.FromPath == "src/star-c.ts" && edge.Specifier == "./missing" && edge.Unresolved
	}) {
		t.Fatal("unresolved star import fact was lost")
	}
}

func TestDeclarationTypeObservationsCompareOnlyObservedSpaces(t *testing.T) {
	root := t.TempDir()
	config := `{"compilerOptions":{"strict":true,"module":"preserve","moduleResolution":"bundler"},"files":["index.ts"]}`
	writeWorkspace(t, root, map[string]string{
		"type/tsconfig.json":               config,
		"type/index.ts":                    `export type { Widget } from "widget";`,
		"both/tsconfig.json":               config,
		"both/index.ts":                    `export { Widget } from "widget";`,
		"node_modules/widget/package.json": `{"name":"widget","types":"index.d.ts"}`,
		"node_modules/widget/index.d.ts":   `export declare class Widget {}`,
	})
	snapshot, err := BuildSnapshot(context.Background(), BuildSnapshotRequest{
		WorkspaceRoot: root,
		Files:         []string{filepath.Join(root, "type/index.ts"), filepath.Join(root, "both/index.ts")},
	})
	if err != nil {
		t.Fatal(err)
	}
	typeOnly := semanticExport(t, snapshot, "type/index.ts", "Widget", SpaceType)
	both := semanticExport(t, snapshot, "both/index.ts", "Widget", SpaceValue)
	if len(typeOnly.Targets) != 1 || !slices.Equal(typeOnly.Targets, both.Targets) {
		t.Fatalf("re-exports did not resolve to the same class: type=%+v value=%+v", typeOnly, both)
	}
	declaration := semanticDeclaration(t, snapshot, typeOnly.Targets[0])
	if !slices.ContainsFunc(declaration.Types, func(item DeclarationType) bool { return item.Space == SpaceType }) ||
		!slices.ContainsFunc(declaration.Types, func(item DeclarationType) bool { return item.Space == SpaceValue }) ||
		slices.Contains(declaration.Incomplete, AssociationConflict) {
		t.Fatalf("matching type-space observations conflicted with an extra value-space observation: %+v", declaration)
	}
}

func TestExportedTypeRootsSurviveTsconfigFiltering(t *testing.T) {
	root := t.TempDir()
	writeWorkspace(t, root, map[string]string{
		"tsconfig.json": `{"compilerOptions":{"strict":true},"include":["src/**/*.ts"]}`,
		"src/index.ts":  `export type PublicMode = "safe"; export { Internal } from "./internal";`,
		"src/internal.ts": `export class Internal {}
export type Unused = { hidden: bigint };`,
	})
	snapshot, err := BuildSnapshot(context.Background(), BuildSnapshotRequest{
		WorkspaceRoot: root, Files: []string{filepath.Join(root, "src/index.ts")}, TsconfigFiles: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	public := semanticExport(t, snapshot, "src/index.ts", "PublicMode", SpaceType)
	if len(public.Targets) != 1 {
		t.Fatalf("public primitive type export: %+v", public)
	}
	declaration := semanticDeclaration(t, snapshot, public.Targets[0])
	if len(declaration.Types) != 1 || !slices.ContainsFunc(snapshot.Types, func(item TypeFact) bool {
		return item.ID == declaration.Types[0].Type && item.Kind == TypeKindLiteral
	}) {
		t.Fatalf("export-only type root was filtered out: declaration=%+v types=%+v", declaration, snapshot.Types)
	}
	if !slices.ContainsFunc(snapshot.Modules, func(item Module) bool { return item.Path == "src/index.ts" }) {
		t.Fatalf("requested root module was lost: %+v", snapshot.Modules)
	}
	if _, found := findSemanticExport(snapshot, "src/internal.ts", "Unused", SpaceType); found {
		t.Fatal("an imported but unselected module became a public export root")
	}
}

func TestExportTargetConflictsRetainAllProgramObservations(t *testing.T) {
	build := func(redGroup, blueGroup string, tsconfigFiles bool) ExportFact {
		root := t.TempDir()
		config := func(target string) string {
			return `{"compilerOptions":{"strict":true,"module":"preserve","moduleResolution":"bundler","baseUrl":".","paths":{"contract":["../targets/` + target + `"]}},"include":["**/*.ts","../shared/**/*.ts"]}`
		}
		writeWorkspace(t, root, map[string]string{
			"targets/red.ts":             `export interface Api { red: string }`,
			"targets/blue.ts":            `export interface Api { blue: number }`,
			"shared/index.ts":            `export { Api } from "contract";`,
			redGroup + "/tsconfig.json":  config("red"),
			redGroup + "/use.ts":         `import type { Api } from "../shared"; export const red: Api = { red: "ok" };`,
			blueGroup + "/tsconfig.json": config("blue"),
			blueGroup + "/use.ts":        `import type { Api } from "../shared"; export const blue: Api = { blue: 1 };`,
			"tsconfig.json":              `{"compilerOptions":{"module":"preserve","moduleResolution":"bundler","baseUrl":".","paths":{"contract":["./targets/red"]}},"files":["shared/index.ts"],"references":[{"path":"./` + redGroup + `"},{"path":"./` + blueGroup + `"}]}`,
		})
		files := []string{
			filepath.Join(root, redGroup+"/use.ts"), filepath.Join(root, blueGroup+"/use.ts"),
			filepath.Join(root, "shared/index.ts"), filepath.Join(root, "targets/red.ts"), filepath.Join(root, "targets/blue.ts"),
		}
		snapshot, err := BuildSnapshot(context.Background(), BuildSnapshotRequest{WorkspaceRoot: root, Files: files, TsconfigFiles: tsconfigFiles})
		if err != nil {
			t.Fatal(err)
		}
		item := semanticExport(t, snapshot, "shared/index.ts", "Api", SpaceType)
		for _, target := range item.Targets {
			if len(semanticDeclaration(t, snapshot, target).Types) == 0 {
				t.Fatalf("retention dropped a conflicting declaration endpoint or type: %+v", item)
			}
		}
		return item
	}
	first := build("a-red", "z-blue", false)
	second := build("z-red", "a-blue", false)
	if len(first.Targets) != 2 || !slices.Equal(first.Incomplete, []string{AssociationConflict}) {
		t.Fatalf("differing complete export targets were not retained as a conflict: %+v", first)
	}
	if !slices.Equal(first.Targets, second.Targets) || !slices.Equal(first.Incomplete, second.Incomplete) {
		t.Fatalf("export observations depend on config group order: first=%+v second=%+v", first, second)
	}
	filtered := build("a-red", "z-blue", true)
	if !slices.Equal(first.Targets, filtered.Targets) || !slices.Equal(first.Incomplete, filtered.Incomplete) {
		t.Fatalf("tsconfig retention lost conflicting observations: unfiltered=%+v filtered=%+v", first, filtered)
	}
}

func semanticExport(t *testing.T, snapshot *Snapshot, file, name, space string) ExportFact {
	t.Helper()
	item, found := findSemanticExport(snapshot, file, name, space)
	if !found {
		t.Fatalf("missing module export %s %s in %s: %+v", name, space, file, snapshot.Exports)
	}
	return item
}

func findSemanticExport(snapshot *Snapshot, file, name, space string) (ExportFact, bool) {
	for _, item := range snapshot.Exports {
		if item.File == file && item.Name == name && item.Space == space {
			return item, true
		}
	}
	return ExportFact{}, false
}

func semanticDeclaration(t *testing.T, snapshot *Snapshot, id string) DeclarationFact {
	t.Helper()
	for _, item := range snapshot.Declarations {
		if item.ID == id {
			return item
		}
	}
	t.Fatalf("missing declaration endpoint %q", id)
	return DeclarationFact{}
}

func TestMergedDeclarationsContributeOnlyTheirOwnSpaces(t *testing.T) {
	snapshot := buildTestSnapshot(t, map[string]string{
		"src/index.ts": `export interface Thing { field: string }
export const Thing = 123;
export class Joined {}
export namespace Joined { export const member = 1; }
export interface Merged { a: string }
export interface Merged { b: number }`,
	})
	for _, item := range []struct{ space, kind string }{{SpaceType, "interface"}, {SpaceValue, "variable"}} {
		binding := semanticExport(t, snapshot, "src/index.ts", "Thing", item.space)
		if len(binding.Targets) != 1 || len(binding.Incomplete) != 0 || len(binding.Via) != 1 {
			t.Fatalf("merged %s binding: %+v", item.space, binding)
		}
		declaration := semanticDeclaration(t, snapshot, binding.Targets[0])
		if declaration.Kind != item.kind || len(declaration.Types) != 1 || declaration.Types[0].Space != item.space {
			t.Fatalf("declaration occupies the wrong spaces: %+v", declaration)
		}
	}
	joined := semanticExport(t, snapshot, "src/index.ts", "Joined", SpaceValue)
	if len(joined.Targets) != 2 || len(semanticExport(t, snapshot, "src/index.ts", "Joined", SpaceType).Targets) != 1 {
		t.Fatalf("class and namespace merge lost a legitimate contribution: %+v", joined)
	}
	if got := semanticExport(t, snapshot, "src/index.ts", "Merged", SpaceType); len(got.Targets) != 2 {
		t.Fatalf("interface merge lost a declaration: %+v", got)
	}
}

func TestCompetingStarExportsAreIncompleteUnlessExplicitlyOverridden(t *testing.T) {
	snapshot := buildTestSnapshot(t, map[string]string{
		"src/a.ts":            `export const shared = 1; export const same = true; export interface Mixed { x: string }`,
		"src/b.ts":            `export const shared = 2; export { same } from "./a"; export const Mixed = 1;`,
		"src/ambiguous.ts":    `export * from "./a"; export * from "./b";`,
		"src/forward.ts":      `export * from "./ambiguous";`,
		"src/named.ts":        `export { shared as renamed } from "./ambiguous";`,
		"src/override.ts":     `export * from "./a"; export * from "./b"; export const shared = 3;`,
		"src/override-ref.ts": `export { same } from "./a"; export * from "./a"; export * from "./b";`,
	})
	ambiguous := semanticExport(t, snapshot, "src/ambiguous.ts", "shared", SpaceValue)
	if len(ambiguous.Targets) != 0 || !slices.Contains(ambiguous.Incomplete, AssociationConflict) || len(ambiguous.Reexports) != 2 {
		t.Fatalf("ambiguous stars asserted a coherent target: %+v", ambiguous)
	}
	forward := semanticExport(t, snapshot, "src/forward.ts", "shared", SpaceValue)
	if len(forward.Targets) != 0 || !slices.Contains(forward.Incomplete, AssociationConflict) {
		t.Fatalf("star chain hid an ambiguous binding: %+v", forward)
	}
	named := semanticExport(t, snapshot, "src/named.ts", "renamed", SpaceValue)
	if len(named.Targets) != 0 || !slices.Contains(named.Incomplete, AssociationConflict) {
		t.Fatalf("named re-export hid an ambiguous binding: %+v", named)
	}
	mixed := semanticExport(t, snapshot, "src/ambiguous.ts", "Mixed", SpaceType)
	if len(mixed.Targets) != 0 || !slices.Contains(mixed.Incomplete, AssociationConflict) {
		t.Fatalf("competing type and value symbols hid a star collision: %+v", mixed)
	}
	same := semanticExport(t, snapshot, "src/ambiguous.ts", "same", SpaceValue)
	if len(same.Targets) != 1 || len(same.Incomplete) != 0 {
		t.Fatalf("two paths to one target are not ambiguous: %+v", same)
	}
	if explicit := semanticExport(t, snapshot, "src/override-ref.ts", "same", SpaceValue); len(explicit.Via) != 1 || len(explicit.Reexports) != 1 || explicit.Reexports[0].SourceName != "same" {
		t.Fatalf("explicit re-export did not override matching star paths: %+v", explicit)
	}
	override := semanticExport(t, snapshot, "src/override.ts", "shared", SpaceValue)
	if len(override.Targets) != 1 || len(override.Incomplete) != 0 || semanticDeclaration(t, snapshot, override.Targets[0]).Origin != "src/override.ts" {
		t.Fatalf("explicit export did not override the stars: %+v", override)
	}
	for _, test := range []struct {
		name, checked, source     string
		checkedDecls, sourceDecls int
	}{
		{"ambiguous checked binding", `export * from "./a"; export * from "./b";`, `export const shared = 1;`, 0, 1},
		{"ambiguous source binding", `export declare const shared: number;`, `export * from "./a"; export * from "./b";`, 1, 0},
	} {
		t.Run(test.name, func(t *testing.T) {
			root := t.TempDir()
			writeWorkspace(t, root, map[string]string{
				"tsconfig.json":      `{"compilerOptions":{"module":"preserve","moduleResolution":"bundler"},"include":["src/**/*.ts"]}`,
				"checked/index.d.ts": test.checked,
				"checked/a.d.ts":     `export declare const shared: number;`,
				"checked/b.d.ts":     `export declare const shared: string;`,
				"src/index.ts":       test.source,
				"src/a.ts":           `export const shared = 1;`,
				"src/b.ts":           `export const shared = 2;`,
				"src/app.ts":         `import { shared } from "../checked"; export const result = shared;`,
				"source-links.json":  `{"version":1,"entries":[{"checkedEntry":"checked/index.d.ts","sourceEntry":"src/index.ts"}]}`,
			})
			result, err := BuildSnapshot(context.Background(), BuildSnapshotRequest{WorkspaceRoot: root, Files: []string{filepath.Join(root, "src/app.ts")}, SourceLinks: "source-links.json"})
			if err != nil {
				t.Fatal(err)
			}
			if len(result.SourceBindings) != 1 {
				t.Fatalf("binding attempts: %+v", result.SourceBindings)
			}
			binding := result.SourceBindings[0]
			if len(binding.CheckedDeclarations) != test.checkedDecls || len(binding.SourceDeclarations) != test.sourceDecls || binding.SourceFile != "" || !slices.Contains(binding.Incomplete, AssociationConflict) {
				t.Fatalf("ambiguous binding asserted a source association: %+v", binding)
			}
		})
	}
}

func TestOnlyRetainedModulesRootExportsAndTypes(t *testing.T) {
	root := t.TempDir()
	writeWorkspace(t, root, map[string]string{
		"app/tsconfig.json":    `{"compilerOptions":{"module":"preserve","moduleResolution":"bundler"},"files":["main.ts"]}`,
		"app/main.ts":          `import type { Secret } from "../hidden/leak"; export type Public = Secret;`,
		"hidden/tsconfig.json": `{"files":["keep.ts"]}`,
		"hidden/keep.ts":       `export const visible = 1;`,
		"hidden/leak.ts":       `export type Secret = { password: string }; export type Other = { forbidden: bigint };`,
	})
	files := []string{filepath.Join(root, "app/main.ts"), filepath.Join(root, "hidden/keep.ts"), filepath.Join(root, "hidden/leak.ts")}
	snapshot, err := BuildSnapshot(context.Background(), BuildSnapshotRequest{WorkspaceRoot: root, Files: files, TsconfigFiles: true})
	if err != nil {
		t.Fatal(err)
	}
	if slices.ContainsFunc(snapshot.Modules, func(item Module) bool { return item.Path == "hidden/leak.ts" }) ||
		slices.ContainsFunc(snapshot.Exports, func(item ExportFact) bool { return item.File == "hidden/leak.ts" }) ||
		slices.ContainsFunc(snapshot.Declarations, func(item DeclarationFact) bool { return item.Name == "Other" }) ||
		slices.ContainsFunc(snapshot.Types, func(item TypeFact) bool { return item.Name == "Other" }) {
		t.Fatalf("excluded module became a root: modules=%+v exports=%+v declarations=%+v types=%+v", snapshot.Modules, snapshot.Exports, snapshot.Declarations, snapshot.Types)
	}
	public := semanticExport(t, snapshot, "app/main.ts", "Public", SpaceType)
	if len(public.Targets) != 1 || len(semanticDeclaration(t, snapshot, public.Targets[0]).Types) == 0 {
		t.Fatalf("retained export lost its declaration/type endpoint: %+v", public)
	}
	if !slices.ContainsFunc(snapshot.Types, func(item TypeFact) bool { return item.Name == "Secret" }) {
		t.Fatal("the retained public type lost its imported endpoint")
	}
}

func TestExportedDestructuringRetainsBindingsAndStatementSites(t *testing.T) {
	root := t.TempDir()
	writeWorkspace(t, root, map[string]string{
		"tsconfig.json":      `{"compilerOptions":{"strict":true,"module":"preserve","moduleResolution":"bundler"},"include":["src/**/*.ts"]}`,
		"checked/index.d.ts": `export declare const foo: number, nested: string, inner: boolean, first: number, rest: number[];`,
		"src/index.ts": `export const { foo, child: { nested }, tuple: [inner] } = { foo: 1, child: { nested: "x" }, tuple: [true] };
export const [first, , ...rest] = [1, 2, 3];`,
		"src/app.ts":        `import { foo } from "../checked"; export const use = foo;`,
		"source-links.json": `{"version":1,"entries":[{"checkedEntry":"checked/index.d.ts","sourceEntry":"src/index.ts"}]}`,
	})
	snapshot, err := BuildSnapshot(context.Background(), BuildSnapshotRequest{WorkspaceRoot: root,
		Files: []string{filepath.Join(root, "src/index.ts"), filepath.Join(root, "src/app.ts")}, SourceLinks: "source-links.json"})
	if err != nil {
		t.Fatal(err)
	}
	var objectSite, arraySite string
	for _, name := range []string{"foo", "nested", "inner", "first", "rest"} {
		item := semanticExport(t, snapshot, "src/index.ts", name, SpaceValue)
		if len(item.Targets) != 1 || len(item.Incomplete) != 0 || len(item.Via) != 1 {
			t.Fatalf("%s lost its binding or statement site: %+v", name, item)
		}
		declaration := semanticDeclaration(t, snapshot, item.Targets[0])
		if declaration.Kind != "variable" || len(declaration.Types) != 1 || declaration.Types[0].Space != SpaceValue {
			t.Fatalf("%s lost its declared value type: %+v", name, declaration)
		}
		site := &objectSite
		if name == "first" || name == "rest" {
			site = &arraySite
		}
		if *site != "" && *site != item.Via[0] {
			t.Fatalf("%s was not attributed to its containing statement: %+v", name, item)
		}
		*site = item.Via[0]
		if !slices.ContainsFunc(snapshot.SourceBindings, func(binding SourceBindingFact) bool {
			return binding.Name == name && len(binding.CheckedDeclarations) == 1 &&
				slices.Equal(binding.SourceDeclarations, item.Targets) && len(binding.Incomplete) == 0
		}) {
			t.Fatalf("%s did not match the checked and source binding: %+v", name, snapshot.SourceBindings)
		}
	}
	if objectSite == arraySite {
		t.Fatal("two export statements shared one syntax site")
	}
}

func TestArbitraryExportNamesRemainDistinctFromStarSyntax(t *testing.T) {
	root := t.TempDir()
	writeWorkspace(t, root, map[string]string{
		"tsconfig.json":      `{"compilerOptions":{"module":"preserve","moduleResolution":"bundler"},"include":["src/**/*.ts"]}`,
		"checked/index.d.ts": `declare const v: number; export { v as "", v as "*", v as "\u0000" };`,
		"src/base.ts":        `const v = 1; export { v as "", v as "*", v as "\u0000" };`,
		"src/other.ts":       `const other = 2; export { other as "*" }; export const ordinary = 3;`,
		"src/index.ts":       `export { "" as "", "*" as "*", "\u0000" as "\u0000" } from "./base"; export * from "./other";`,
		"src/through.ts":     `export * from "./base";`,
		"src/app.ts":         `import { "" as empty, "*" as star, "\u0000" as nul } from "../checked"; export const use = empty + star + nul;`,
		"source-links.json":  `{"version":1,"entries":[{"checkedEntry":"checked/index.d.ts","sourceEntry":"src/base.ts"}]}`,
	})
	snapshot, err := BuildSnapshot(context.Background(), BuildSnapshotRequest{WorkspaceRoot: root,
		Files: []string{filepath.Join(root, "src/index.ts"), filepath.Join(root, "src/base.ts"), filepath.Join(root, "src/through.ts"), filepath.Join(root, "src/app.ts")}, SourceLinks: "source-links.json"})
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"", "*", "\x00"} {
		base := semanticExport(t, snapshot, "src/base.ts", name, SpaceValue)
		item := semanticExport(t, snapshot, "src/index.ts", name, SpaceValue)
		if len(base.Targets) != 1 || !slices.Equal(item.Targets, base.Targets) || len(item.Incomplete) != 0 ||
			len(item.Reexports) != 1 || item.Reexports[0].Module != "src/base.ts" || item.Reexports[0].SourceName != name {
			t.Fatalf("literal name %q collided with a star contribution: %+v, base=%+v", name, item, base)
		}
		through := semanticExport(t, snapshot, "src/through.ts", name, SpaceValue)
		if !slices.Equal(through.Targets, base.Targets) || len(through.Incomplete) != 0 || len(through.Reexports) != 1 || through.Reexports[0].Module != "src/base.ts" {
			t.Fatalf("star export lost literal name %q: %+v", name, through)
		}
		if !slices.ContainsFunc(snapshot.SourceBindings, func(binding SourceBindingFact) bool {
			return binding.Name == name && slices.Equal(binding.SourceDeclarations, base.Targets) && len(binding.Incomplete) == 0
		}) {
			t.Fatalf("literal name %q was not matched by source association: %+v", name, snapshot.SourceBindings)
		}
	}
	ordinary := semanticExport(t, snapshot, "src/index.ts", "ordinary", SpaceValue)
	if len(ordinary.Reexports) != 1 || ordinary.Reexports[0].Module != "src/other.ts" || ordinary.Reexports[0].SourceName != "*" {
		t.Fatalf("star contribution lost its provenance: %+v", ordinary)
	}
}

func TestCommonJSStaticBindingsAndDynamicInventory(t *testing.T) {
	snapshot := buildTestSnapshot(t, map[string]string{
		"tsconfig.json":  `{"compilerOptions":{"allowJs":true,"checkJs":true,"module":"commonjs"},"include":["src/**/*"]}`,
		"src/index.js":   `exports.answer = 42; const key = Math.random() ? "first" : "second"; exports[key] = 7;`,
		"src/dynamic.js": `const key = Math.random() ? "first" : "second"; exports[key] = 7; Object.assign(exports, { extra: 1 });`,
		"src/barrel.ts":  `export * from "./dynamic";`,
	})
	answer := semanticExport(t, snapshot, "src/index.js", "answer", SpaceValue)
	if len(answer.Targets) != 1 || len(answer.Via) != 1 || len(answer.Incomplete) != 0 {
		t.Fatalf("checker-visible static CommonJS binding lost its declaration: %+v", answer)
	}
	declaration := semanticDeclaration(t, snapshot, answer.Targets[0])
	if declaration.Kind != "property" || len(declaration.Types) != 1 || declaration.Types[0].Space != SpaceValue {
		t.Fatalf("static CommonJS assignment lost its declared value type: %+v", declaration)
	}
	for _, file := range []string{"src/index.js", "src/dynamic.js", "src/barrel.ts"} {
		if !slices.ContainsFunc(snapshot.ModuleExportsIncomplete, func(item ModuleExportsIncompleteFact) bool {
			return item.File == file && item.Reason == AssociationUnsupported
		}) {
			t.Fatalf("dynamic CommonJS inventory for %s was reported as complete: %+v", file, snapshot.ModuleExportsIncomplete)
		}
	}
	for _, name := range []string{"first", "second"} {
		if _, found := findSemanticExport(snapshot, "src/index.js", name, SpaceValue); found {
			t.Fatalf("dynamic write invented the binding %q", name)
		}
	}
}

func TestSourceAssociationKeepsStaticCommonJSBinding(t *testing.T) {
	root := t.TempDir()
	writeWorkspace(t, root, map[string]string{
		"tsconfig.json":     `{"compilerOptions":{"allowJs":true,"checkJs":true,"module":"commonjs"},"include":["src/**/*.js"]}`,
		"checked/index.js":  `exports.answer = 42;`,
		"src/index.js":      `exports.answer = 43;`,
		"src/app.js":        `const checked = require("../checked"); exports.use = checked.answer;`,
		"source-links.json": `{"version":1,"entries":[{"checkedEntry":"checked/index.js","sourceEntry":"src/index.js"}]}`,
	})
	snapshot, err := BuildSnapshot(context.Background(), BuildSnapshotRequest{WorkspaceRoot: root,
		Files: []string{filepath.Join(root, "src/app.js"), filepath.Join(root, "src/index.js")}, SourceLinks: "source-links.json"})
	if err != nil {
		t.Fatal(err)
	}
	public := semanticExport(t, snapshot, "src/index.js", "answer", SpaceValue)
	if len(snapshot.SourceBindings) != 1 {
		t.Fatalf("static CommonJS association lost its binding: %+v", snapshot.SourceBindings)
	}
	binding := snapshot.SourceBindings[0]
	if binding.Name != "answer" || len(binding.CheckedDeclarations) != 1 ||
		!slices.Equal(binding.SourceDeclarations, public.Targets) || len(binding.Incomplete) != 0 {
		t.Fatalf("static CommonJS association lost checked/source evidence: %+v", binding)
	}
	if len(snapshot.SourceEntries) != 1 || !slices.Contains(snapshot.SourceEntries[0].Incomplete, AssociationUnsupported) {
		t.Fatalf("CommonJS source inventory was reported as complete: %+v", snapshot.SourceEntries)
	}
}

func TestTypeOnlyValueBindingsKeepTheirValueDeclarations(t *testing.T) {
	root := t.TempDir()
	writeWorkspace(t, root, map[string]string{
		"tsconfig.json":      `{"compilerOptions":{"module":"preserve","moduleResolution":"bundler"},"include":["src/**/*.ts"]}`,
		"checked/base.d.ts":  `export declare const count: number; export declare function parse(value: string): number;`,
		"checked/index.d.ts": `export type { count, parse } from "./base";`,
		"src/base.ts":        `export const count = 1; export function parse(value: string): number { return value.length; }`,
		"src/index.ts":       `export type { count, parse } from "./base";`,
		"src/top.ts":         `export * from "./index";`,
		"src/app.ts":         `import type { count, parse } from "../checked"; export type Values = [typeof count, typeof parse];`,
		"source-links.json":  `{"version":1,"entries":[{"checkedEntry":"checked/index.d.ts","sourceEntry":"src/index.ts"}]}`,
	})
	snapshot, err := BuildSnapshot(context.Background(), BuildSnapshotRequest{WorkspaceRoot: root,
		Files: []string{filepath.Join(root, "src/app.ts"), filepath.Join(root, "src/index.ts"), filepath.Join(root, "src/top.ts")}, SourceLinks: "source-links.json"})
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"count", "parse"} {
		item := semanticExport(t, snapshot, "src/index.ts", name, SpaceType)
		top := semanticExport(t, snapshot, "src/top.ts", name, SpaceType)
		if len(item.Targets) != 1 || len(item.Incomplete) != 0 || !slices.Equal(top.Targets, item.Targets) || len(top.Incomplete) != 0 {
			t.Fatalf("type-only %s lost its resolved declaration: item=%+v top=%+v", name, item, top)
		}
		declaration := semanticDeclaration(t, snapshot, item.Targets[0])
		if len(declaration.Types) != 1 || declaration.Types[0].Space != SpaceValue {
			t.Fatalf("type-only %s changed the declaration's actual space: %+v", name, declaration)
		}
		if _, found := findSemanticExport(snapshot, "src/index.ts", name, SpaceValue); found {
			t.Fatalf("type-only %s became a value-accessible export", name)
		}
		if !slices.ContainsFunc(snapshot.SourceBindings, func(binding SourceBindingFact) bool {
			return binding.Name == name && binding.Space == SpaceType &&
				len(binding.CheckedDeclarations) == 1 && slices.Equal(binding.SourceDeclarations, item.Targets) && len(binding.Incomplete) == 0
		}) {
			t.Fatalf("type-only %s lost checked/source membership: %+v", name, snapshot.SourceBindings)
		}
	}
}

func TestReexportSiteIsStableAcrossCompilerGroups(t *testing.T) {
	root := t.TempDir()
	writeWorkspace(t, root, map[string]string{
		"shared/tsconfig.json": `{"compilerOptions":{"module":"preserve","moduleResolution":"bundler"},"files":["index.ts","base.ts"]}`,
		"shared/base.ts":       `export const value = 1;`,
		"shared/index.ts":      `export { value } from "./base";`,
		"app/tsconfig.json":    `{"compilerOptions":{"module":"preserve","moduleResolution":"bundler"},"files":["main.ts","../shared/index.ts","../shared/base.ts"]}`,
		"app/main.ts":          `import { value } from "../shared"; export const result = value;`,
	})
	snapshot, err := BuildSnapshot(context.Background(), BuildSnapshotRequest{WorkspaceRoot: root,
		Files: []string{filepath.Join(root, "app/main.ts"), filepath.Join(root, "shared/index.ts")}})
	if err != nil {
		t.Fatal(err)
	}
	item := semanticExport(t, snapshot, "shared/index.ts", "value", SpaceValue)
	if len(item.Via) != 1 || len(item.Reexports) != 1 || len(item.Incomplete) != 0 {
		t.Fatalf("one syntax occurrence acquired multiple sites: %+v", item)
	}
	if !slices.ContainsFunc(snapshot.ImportEdges, func(edge ImportEdge) bool {
		return edge.FromPath == "shared/index.ts" && edge.Specifier == "./base" && edge.SemanticKey == item.Via[0] && edge.Range == item.Location
	}) {
		t.Fatalf("export site did not reuse the import site and location: export=%+v imports=%+v", item, snapshot.ImportEdges)
	}
}

func TestStarCollisionsPropagateThroughCycles(t *testing.T) {
	snapshot := buildTestSnapshot(t, map[string]string{
		"src/one.ts":      `export const shared = 1;`,
		"src/two.ts":      `export const shared = 2;`,
		"src/a.ts":        `export * from "./b"; export * from "./one"; export * from "./two";`,
		"src/b.ts":        `export * from "./a";`,
		"src/forward.ts":  `export { shared as renamed } from "./b";`,
		"src/override.ts": `export * from "./b"; export const shared = 3;`,
	})
	for _, item := range []struct{ file, name string }{{"src/a.ts", "shared"}, {"src/b.ts", "shared"}, {"src/forward.ts", "renamed"}} {
		binding := semanticExport(t, snapshot, item.file, item.name, SpaceValue)
		if len(binding.Targets) != 0 || !slices.Contains(binding.Incomplete, AssociationConflict) {
			t.Fatalf("cycle hid a star collision at %s: %+v", item.file, binding)
		}
	}
	override := semanticExport(t, snapshot, "src/override.ts", "shared", SpaceValue)
	if len(override.Targets) != 1 || len(override.Incomplete) != 0 {
		t.Fatalf("explicit declaration did not override the cycle: %+v", override)
	}
}

func TestTypeOnlyStarsPropagateThroughCycles(t *testing.T) {
	snapshot := buildTestSnapshot(t, map[string]string{
		"src/leaf.ts":  `export class Widget {}`,
		"src/a.ts":     `export * from "./b"; export type * from "./leaf";`,
		"src/b.ts":     `export * from "./a";`,
		"src/mixed.ts": `export * from "./b"; export * from "./leaf";`,
	})
	leaf := semanticExport(t, snapshot, "src/leaf.ts", "Widget", SpaceType)
	for _, file := range []string{"src/a.ts", "src/b.ts", "src/mixed.ts"} {
		item := semanticExport(t, snapshot, file, "Widget", SpaceType)
		if len(leaf.Targets) != 1 || !slices.Equal(item.Targets, leaf.Targets) || len(item.Incomplete) != 0 {
			t.Fatalf("type-only cycle lost its checked target at %s: %+v", file, item)
		}
		_, valueFound := findSemanticExport(snapshot, file, "Widget", SpaceValue)
		if valueFound != (file == "src/mixed.ts") {
			t.Fatalf("wrong value accessibility at %s: %t", file, valueFound)
		}
	}
}

// Run with -run '^$' -bench '^BenchmarkStarDiamonds$' -benchtime=1x.
func BenchmarkStarDiamonds(b *testing.B) {
	for _, depth := range []int{14, 16, 18, 20} {
		b.Run(fmt.Sprintf("depth%d", depth), func(b *testing.B) {
			root := b.TempDir()
			write := func(name, contents string) {
				b.Helper()
				if err := os.WriteFile(filepath.Join(root, name), []byte(contents), 0o644); err != nil {
					b.Fatal(err)
				}
			}
			write("tsconfig.json", `{"compilerOptions":{"module":"preserve","moduleResolution":"bundler"},"include":["*.ts"]}`)
			write("a0.ts", `export const leaf = 1;`)
			write("b0.ts", `export { leaf } from "./a0";`)
			for level := 1; level <= depth; level++ {
				body := fmt.Sprintf(`export * from "./a%d"; export * from "./b%d";`, level-1, level-1)
				write(fmt.Sprintf("a%d.ts", level), body)
				write(fmt.Sprintf("b%d.ts", level), body)
			}
			request := BuildSnapshotRequest{WorkspaceRoot: root, Files: []string{filepath.Join(root, fmt.Sprintf("a%d.ts", depth))}}
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				snapshot, err := BuildSnapshot(context.Background(), request)
				if err != nil {
					b.Fatal(err)
				}
				item, found := findSemanticExport(snapshot, fmt.Sprintf("a%d.ts", depth), "leaf", SpaceValue)
				if !found || len(item.Targets) != 1 || len(item.Incomplete) != 0 {
					b.Fatalf("diamond lost its one target: %+v", item)
				}
			}
		})
	}
}
