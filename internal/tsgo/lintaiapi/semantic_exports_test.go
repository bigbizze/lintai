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
		"src/namespace.ts": `export * as all from "./base";`,
		"src/star-a.ts":    `export * from "./star-b"; export const known = 1;`,
		"src/star-b.ts":    `export * from "./star-c";`,
		"src/star-c.ts":    `export * from "./star-a"; export * from "./missing"; export const leaf = 1;`,
		"src/star-root.ts": `export * from "./star-a";`,
		"src/cjs.ts":       `const api = { run() {} }; export = api;`,
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
	if !slices.ContainsFunc(snapshot.ImportEdges, func(edge ImportEdge) bool {
		return edge.FromPath == "src/star-c.ts" && edge.Specifier == "./missing" && edge.Unresolved
	}) {
		t.Fatal("unresolved star import fact was lost")
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
	build := func(redGroup, blueGroup string) ExportFact {
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
			"tsconfig.json":              `{"files":[],"references":[{"path":"./` + redGroup + `"},{"path":"./` + blueGroup + `"}]}`,
		})
		files := []string{
			filepath.Join(root, redGroup+"/use.ts"), filepath.Join(root, blueGroup+"/use.ts"),
			filepath.Join(root, "shared/index.ts"), filepath.Join(root, "targets/red.ts"), filepath.Join(root, "targets/blue.ts"),
		}
		snapshot, err := BuildSnapshot(context.Background(), BuildSnapshotRequest{WorkspaceRoot: root, Files: files})
		if err != nil {
			t.Fatal(err)
		}
		return semanticExport(t, snapshot, "shared/index.ts", "Api", SpaceType)
	}
	first := build("a-red", "z-blue")
	second := build("z-red", "a-blue")
	if len(first.Targets) != 2 || !slices.Equal(first.Incomplete, []string{AssociationConflict}) {
		t.Fatalf("differing complete export targets were not retained as a conflict: %+v", first)
	}
	if !slices.Equal(first.Targets, second.Targets) || !slices.Equal(first.Incomplete, second.Incomplete) {
		t.Fatalf("export observations depend on config group order: first=%+v second=%+v", first, second)
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
