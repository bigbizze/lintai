package lintaiapi

import (
	"context"
	"path/filepath"
	"slices"
	"testing"
)

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
	if len(declaration.TypeObservations) != 2 ||
		!slices.ContainsFunc(declaration.TypeObservations, func(observation []DeclarationType) bool {
			return len(observation) == 1 && observation[0].Space == SpaceType
		}) ||
		!slices.ContainsFunc(declaration.TypeObservations, func(observation []DeclarationType) bool { return len(observation) == 2 }) ||
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
