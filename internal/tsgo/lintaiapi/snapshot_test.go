package lintaiapi

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// V32, V33, V40: keys come from source syntax and no-change collection reuses them.
func TestTrackingKeysAndReuse(t *testing.T) {
	root := t.TempDir()
	source := `import { value } from "./dep";
export function named() { return 1; }
class Sample {
  static run() {} run() {} #run() {}
  get size() { return 1; } set size(v: number) {}
}
export const result = [1].map((x) => x);
`
	files := writeWorkspace(t, root, map[string]string{"src/main.ts": source, "src/dep.ts": "export const value = 1;"})
	request := BuildSnapshotRequest{WorkspaceRoot: root, Files: files}
	first, err := BuildSnapshotReusingTracking(context.Background(), request, nil)
	if err != nil {
		t.Fatal(err)
	}
	second, err := BuildSnapshotReusingTracking(context.Background(), request, first)
	if err != nil {
		t.Fatal(err)
	}
	if first.trackingKeyBuilds == 0 || second.trackingKeyBuilds != 0 {
		t.Fatalf("key builds cold=%d unchanged=%d", first.trackingKeyBuilds, second.trackingKeyBuilds)
	}
	t.Logf("tracking key constructions: cold=%d, no-change=%d", first.trackingKeyBuilds, second.trackingKeyBuilds)
	data := func(s *Snapshot, id string) string {
		value := s.Tracking[id].Data
		if value == nil {
			return ""
		}
		encoded, err := json.Marshal(value)
		if err != nil {
			t.Fatal(err)
		}
		return string(encoded)
	}
	named := func(s *Snapshot, name string) string {
		for _, fn := range s.Functions {
			if fn.Name == name {
				return data(s, fn.SemanticKey)
			}
		}
		t.Fatalf("missing function %q", name)
		return ""
	}
	importKey := func(s *Snapshot, specifier string) string {
		for _, edge := range s.ImportEdges {
			if edge.Specifier == specifier {
				return data(s, edge.SemanticKey)
			}
		}
		t.Fatalf("missing import %q", specifier)
		return ""
	}
	declarationKey := func(s *Snapshot, name string) string {
		for _, declaration := range s.Declarations {
			if declaration.Name == name && declaration.Kind == "variable" {
				return data(s, declaration.ID)
			}
		}
		t.Fatalf("missing variable declaration %q", name)
		return ""
	}
	if named(first, "named") == "" || importKey(first, "./dep") == "" {
		t.Fatal("missing named or module-reference key")
	}
	var distinct = make(map[string]bool)
	anonymous := false
	for _, fn := range first.Functions {
		if strings.HasPrefix(fn.Name, "Sample.") {
			distinct[data(first, fn.SemanticKey)] = true
			if !strings.Contains(first.Tracking[fn.SemanticKey].Description, "Sample.") {
				t.Errorf("member description omits its readable scope: %q", first.Tracking[fn.SemanticKey].Description)
			}
		}
		if strings.HasPrefix(fn.Name, "<anonymous@") {
			anonymous = true
			if data(first, fn.SemanticKey) != "" {
				t.Fatal("anonymous function received a key")
			}
		}
		for _, signature := range fn.SignatureObservations {
			if data(first, SignatureID(fn.SemanticKey, signature)) != "" {
				t.Fatal("signature received a key")
			}
		}
	}
	if !anonymous || len(distinct) != 5 || distinct[""] {
		t.Fatalf("member placement, privacy or accessor keys collided: %v", distinct)
	}
	for _, typ := range first.Types {
		if data(first, typ.ID) != "" {
			t.Fatal("type received a key")
		}
	}
	for _, site := range first.Sites {
		if strings.HasPrefix(first.Tracking[site.ID].Description, "site") && site.ID != first.ImportEdges[0].SemanticKey && data(first, site.ID) != "" {
			t.Fatal("ordinary site received a key")
		}
	}
	changed := strings.Replace(source, "{ value }", "{ type value as renamed }", 1)
	changed = strings.Replace(changed, "return 1;", "return 2;", 1)
	changed = strings.Replace(changed, "[1].map", "[2].map", 1)
	changed = "// unrelated insertion\nimport \"./unrelated\";\n" + changed
	if err := os.WriteFile(filepath.Join(root, "src/main.ts"), []byte(changed), 0o644); err != nil {
		t.Fatal(err)
	}
	third, err := BuildSnapshotReusingTracking(context.Background(), request, second)
	if err != nil {
		t.Fatal(err)
	}
	if named(first, "named") != named(third, "named") || declarationKey(first, "result") == "" || declarationKey(first, "result") != declarationKey(third, "result") || importKey(first, "./dep") != importKey(third, "./dep") {
		t.Fatal("movement, body/initializer edit, or import binding/type-only change altered its tracking key")
	}
	changed = strings.Replace(changed, "function named()", "function renamed()", 1)
	changed = strings.Replace(changed, "\"./dep\"", "\"./new-dep\"", 1)
	if err := os.WriteFile(filepath.Join(root, "src/main.ts"), []byte(changed), 0o644); err != nil {
		t.Fatal(err)
	}
	fourth, err := BuildSnapshotReusingTracking(context.Background(), request, third)
	if err != nil {
		t.Fatal(err)
	}
	if named(first, "named") == named(fourth, "renamed") || importKey(first, "./dep") == importKey(fourth, "./new-dep") {
		t.Fatal("rename or module specifier change kept its old tracking key")
	}
	t.Run("parse error", func(t *testing.T) {
		root := t.TempDir()
		files := writeWorkspace(t, root, map[string]string{"src/bad.ts": "export const = ;\n"})
		snapshot, err := BuildSnapshotReusingTracking(context.Background(), BuildSnapshotRequest{WorkspaceRoot: root, Files: files}, nil)
		if err != nil {
			t.Fatal(err)
		}
		if len(snapshot.ParseErrors) == 0 {
			t.Fatal("missing parse-error site")
		}
		support := snapshot.Tracking[snapshot.ParseErrors[0].SemanticKey]
		if support.Data != nil || !strings.HasPrefix(support.Description, "parse error:") {
			t.Fatalf("parse-error tracking support = %+v", support)
		}
	})
	t.Run("auxiliary source declaration", func(t *testing.T) {
		root := t.TempDir()
		writeWorkspace(t, root, map[string]string{
			"app/main.ts":       `import { parse } from "../dist/index"; export const value = parse("x");`,
			"dist/index.d.ts":   `export declare function parse(value: string): string;`,
			"source/index.ts":   `export function parse(value: string): string { return value; }`,
			"source-links.json": `{"version":1,"entries":[{"checkedEntry":"dist/index.d.ts","sourceEntry":"source/index.ts"}]}`,
		})
		request := BuildSnapshotRequest{WorkspaceRoot: root, Files: []string{filepath.Join(root, "app/main.ts")}, SourceLinks: "source-links.json"}
		cold, err := BuildSnapshotReusingTracking(context.Background(), request, nil)
		if err != nil {
			t.Fatal(err)
		}
		warm, err := BuildSnapshotReusingTracking(context.Background(), request, cold)
		if err != nil {
			t.Fatal(err)
		}
		if len(cold.SourceBindings) != 1 || len(cold.SourceBindings[0].SourceDeclarations) != 1 {
			t.Fatalf("missing auxiliary declaration: %+v", cold.SourceBindings)
		}
		id := cold.SourceBindings[0].SourceDeclarations[0]
		if data(cold, id) == "" || data(cold, id) != data(warm, id) || warm.trackingKeyBuilds != 0 {
			t.Fatalf("auxiliary tracking was not reused: cold=%q warm=%q builds=%d", data(cold, id), data(warm, id), warm.trackingKeyBuilds)
		}
		t.Logf("auxiliary tracking key constructions: cold=%d, no-change=%d", cold.trackingKeyBuilds, warm.trackingKeyBuilds)
	})
}

func TestBuildSnapshotUsesNearestTsConfigForAliasImports(t *testing.T) {
	root := t.TempDir()
	files := writeWorkspace(t, root, map[string]string{
		"packages/app/tsconfig.json": `{
  "compilerOptions": {
    "baseUrl": ".",
    "paths": {
      "@lib/*": ["src/lib/*"]
    },
    "target": "es2022",
    "module": "nodenext",
    "moduleResolution": "nodenext",
    "noEmit": true
  },
  "include": ["src/**/*.ts"]
}
`,
		"packages/app/src/lib/effect.ts": `export async function effect() {
	await Promise.resolve(1);
	return 1;
}
`,
		"packages/app/src/pure/helper.ts": `import { effect } from "@lib/effect";

export function helper() {
	return effect();
}
`,
	})

	snapshot, err := BuildSnapshot(context.Background(), BuildSnapshotRequest{
		WorkspaceRoot: root,
		Files:         files,
	})
	if err != nil {
		t.Fatal(err)
	}

	expectedImportTarget := "packages/app/src/lib/effect.ts"
	foundImport := false
	for _, edge := range snapshot.ImportEdges {
		if edge.FromPath == "packages/app/src/pure/helper.ts" && edge.ToPath == expectedImportTarget {
			foundImport = true
			break
		}
	}
	if !foundImport {
		t.Fatalf("expected alias import to resolve to %s, got %+v", expectedImportTarget, snapshot.ImportEdges)
	}

	foundCall := false
	for _, edge := range snapshot.CallEdges {
		if edge.FromName == "helper" && edge.ToName == "effect" {
			foundCall = true
			break
		}
	}
	if !foundCall {
		t.Fatalf("expected helper -> effect call edge, got %+v", snapshot.CallEdges)
	}
}

func TestBuildSnapshotTracksMissingNearestTsconfigForCreation(t *testing.T) {
	for _, existingConfig := range []bool{true, false} {
		name := "synthetic"
		if existingConfig {
			name = "parent config"
		}
		t.Run(name, func(t *testing.T) {
			root := t.TempDir()
			files := map[string]string{
				"src/core/app.ts": `import { marker } from "choice"; export const selected = marker;`,
				"targets/old.ts":  `export const marker = "old";`,
				"targets/new.ts":  `export const marker = "new";`,
			}
			if existingConfig {
				files["tsconfig.json"] = `{"compilerOptions":{"module":"preserve","moduleResolution":"bundler","baseUrl":".","paths":{"choice":["./targets/old"]}},"files":["src/core/app.ts"]}`
			}
			writeWorkspace(t, root, files)
			request := BuildSnapshotRequest{WorkspaceRoot: root, Files: []string{filepath.Join(root, "src/core/app.ts")}}
			build := func() *Snapshot {
				t.Helper()
				snapshot, err := BuildSnapshot(context.Background(), request)
				if err != nil {
					t.Fatal(err)
				}
				return snapshot
			}
			before := build()
			for _, path := range []string{"src/core/tsconfig.json", "src/tsconfig.json"} {
				candidate := filepath.Join(root, path)
				if !slices.ContainsFunc(before.Inputs, func(input SnapshotInput) bool { return input.Path == candidate && !input.Exists }) {
					t.Fatalf("missing nearest-config candidate %s was not tracked: %+v", candidate, before.Inputs)
				}
			}
			if !existingConfig && !slices.ContainsFunc(before.Inputs, func(input SnapshotInput) bool {
				return input.Path == filepath.Join(root, "tsconfig.json") && !input.Exists
			}) {
				t.Fatalf("missing root config was not tracked for the synthetic program: %+v", before.Inputs)
			}
			if !slices.ContainsFunc(before.ImportEdges, func(edge ImportEdge) bool {
				return edge.Specifier == "choice" && (existingConfig && edge.ToPath == "targets/old.ts" || !existingConfig && edge.Unresolved)
			}) {
				t.Fatalf("unexpected resolution before creating the config: %+v", before.ImportEdges)
			}
			candidate := filepath.Join(root, "src/core/tsconfig.json")
			config := `{"compilerOptions":{"module":"preserve","moduleResolution":"bundler","baseUrl":"../..","paths":{"choice":["./targets/new"]}},"files":["app.ts"]}`
			if err := os.WriteFile(candidate, []byte(config), 0o644); err != nil {
				t.Fatal(err)
			}
			after := build()
			if !slices.ContainsFunc(after.Inputs, func(input SnapshotInput) bool { return input.Path == candidate && input.Exists }) ||
				!slices.ContainsFunc(after.ImportEdges, func(edge ImportEdge) bool { return edge.Specifier == "choice" && edge.ToPath == "targets/new.ts" }) {
				t.Fatalf("created nearer config did not change the program resolution: inputs=%+v imports=%+v", after.Inputs, after.ImportEdges)
			}
		})
	}
}

func TestBuildSnapshotExtractsExpandedMetadata(t *testing.T) {
	root := t.TempDir()
	files := writeWorkspace(t, root, map[string]string{
		"tsconfig.json": `{
  "compilerOptions": {
    "target": "es2022",
    "module": "nodenext",
    "moduleResolution": "nodenext",
    "noEmit": true
  },
  "include": ["src/**/*.ts"]
}
`,
		"src/data/db.ts": `export default async function db(query: string): Promise<number> {
	return query.length;
}

export type DbConfig = {
	dsn: string;
};

export function helper(config: DbConfig): number {
	return config.dsn.length;
}
`,
		"src/service/repository.ts": `import db, { helper as runHelper, type DbConfig } from "../data/db";
import * as dbModule from "../data/db";

type LocalConfig = DbConfig;

export class Repository {
	async save(config: DbConfig): Promise<number> {
		await db("select 1");
		return runHelper(config) + (await dbModule.default(config.dsn));
	}
}

export function mirror(config: LocalConfig): DbConfig {
	return config;
}
`,
	})

	snapshot, err := BuildSnapshot(context.Background(), BuildSnapshotRequest{
		WorkspaceRoot: root,
		Files:         files,
	})
	if err != nil {
		t.Fatal(err)
	}

	var dbFunction *Function
	for i := range snapshot.Functions {
		if snapshot.Functions[i].Name == "db" {
			dbFunction = &snapshot.Functions[i]
			break
		}
	}
	if dbFunction == nil {
		t.Fatalf("expected db function in snapshot, got %+v", snapshot.Functions)
	}
	if !dbFunction.IsExported || !dbFunction.IsAsync {
		t.Fatalf("expected db function to be exported and async, got %+v", dbFunction)
	}
	if dbFunction.ParameterCount != 1 {
		t.Fatalf("expected db parameter count 1, got %+v", dbFunction)
	}
	if display := typeDisplay(snapshot, dbFunction.ReturnType); display != "Promise<number>" {
		t.Fatalf("expected db return type Promise<number>, got %q", display)
	}
	if len(dbFunction.ParameterTypes) != 1 || typeDisplay(snapshot, dbFunction.ParameterTypes[0]) != "string" {
		t.Fatalf("unexpected db parameter types %+v", dbFunction.ParameterTypes)
	}

	var mixedImport *ImportEdge
	var namespaceImport *ImportEdge
	for i := range snapshot.ImportEdges {
		edge := &snapshot.ImportEdges[i]
		if edge.FromPath != "src/service/repository.ts" {
			continue
		}
		if len(edge.ImportedSymbols) == 3 {
			mixedImport = edge
		}
		if len(edge.ImportedSymbols) == 1 && edge.ImportedSymbols[0].Kind == "namespace" {
			namespaceImport = edge
		}
	}
	if mixedImport == nil || namespaceImport == nil {
		t.Fatalf("expected mixed and namespace imports, got %+v", snapshot.ImportEdges)
	}
	if !mixedImport.HasDefaultImport || !mixedImport.HasNamedImports || mixedImport.HasNamespaceImport || mixedImport.IsTypeOnly {
		t.Fatalf("unexpected mixed import flags %+v", mixedImport)
	}
	if mixedImport.ImportedSymbols[0] != (ImportedSymbol{Name: "db", Kind: "default", IsTypeOnly: false}) {
		t.Fatalf("unexpected default import symbol %+v", mixedImport.ImportedSymbols[0])
	}
	if mixedImport.ImportedSymbols[1] != (ImportedSymbol{Name: "runHelper", Kind: "named", IsTypeOnly: false}) {
		t.Fatalf("unexpected named import symbol %+v", mixedImport.ImportedSymbols[1])
	}
	if mixedImport.ImportedSymbols[2] != (ImportedSymbol{Name: "DbConfig", Kind: "named", IsTypeOnly: true}) {
		t.Fatalf("unexpected type-only import symbol %+v", mixedImport.ImportedSymbols[2])
	}
	if !namespaceImport.HasNamespaceImport || namespaceImport.HasDefaultImport || namespaceImport.HasNamedImports || namespaceImport.IsTypeOnly {
		t.Fatalf("unexpected namespace import flags %+v", namespaceImport)
	}

	foundTarget := false
	for _, ref := range snapshot.TypeRefs {
		if ref.FilePath == "src/service/repository.ts" && ref.Name == "DbConfig" && ref.TargetPath == "src/data/db.ts" {
			foundTarget = true
			break
		}
	}
	if !foundTarget {
		t.Fatalf("expected DbConfig type ref target path, got %+v", snapshot.TypeRefs)
	}
}

func TestBuildSnapshotResolvesQualifiedTypeReferenceTargets(t *testing.T) {
	root := t.TempDir()
	files := writeWorkspace(t, root, map[string]string{
		"tsconfig.json": `{
  "compilerOptions": {
    "target": "es2022",
    "module": "nodenext",
    "moduleResolution": "nodenext",
    "noEmit": true
  },
  "include": ["src/**/*.ts"]
}
`,
		"src/models/types.ts": `export namespace Models {
	export type User = {
		id: string;
	};
}
`,
		"src/service/repository.ts": `import { Models } from "../models/types";

export function loadUser(user: Models.User): Models.User {
	return user;
}
`,
	})

	snapshot, err := BuildSnapshot(context.Background(), BuildSnapshotRequest{
		WorkspaceRoot: root,
		Files:         files,
	})
	if err != nil {
		t.Fatal(err)
	}

	foundTarget := false
	for _, ref := range snapshot.TypeRefs {
		if ref.FilePath == "src/service/repository.ts" && ref.Name == "Models.User" && ref.TargetPath == "src/models/types.ts" {
			foundTarget = true
			break
		}
	}
	if !foundTarget {
		t.Fatalf("expected qualified type ref target path, got %+v", snapshot.TypeRefs)
	}
}

func writeWorkspace(t *testing.T, root string, files map[string]string) []string {
	t.Helper()
	paths := make([]string, 0, len(files))
	for relative, content := range files {
		path := filepath.Join(root, filepath.FromSlash(relative))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
		if filepath.Ext(path) == ".ts" || filepath.Ext(path) == ".tsx" || filepath.Ext(path) == ".js" || filepath.Ext(path) == ".jsx" {
			paths = append(paths, path)
		}
	}
	slices.Sort(paths)
	return paths
}

func assertEqualKeys(t *testing.T, left []string, right []string) {
	t.Helper()
	if !slices.Equal(left, right) {
		t.Fatalf("expected keys to match\nleft:  %v\nright: %v", left, right)
	}
}

// With TsconfigFiles, a module is a file that tsc compiles: a root file of
// its tsconfig or of a referenced tsconfig, or a file that a root file
// imports. A file with no tsconfig is always a module.
func TestBuildSnapshotTsconfigFiles(t *testing.T) {
	root := t.TempDir()
	files := writeWorkspace(t, root, map[string]string{
		"app/tsconfig.json": `{
  "compilerOptions": { "module": "preserve", "moduleResolution": "bundler", "noEmit": true },
  "include": ["src/**/*.ts"],
  "exclude": ["src/legacy/**"]
}
`,
		"app/src/main.ts": `import { shared } from "./legacy/shared";
export function main() { return shared(); }
`,
		"app/src/legacy/shared.ts": `export function shared() { return 1; }
`,
		"app/src/legacy/old.ts": `export function old() { return 2; }
`,
		"app/scripts/build.ts": `export function build() { return 3; }
`,
		"lib/tsconfig.json": `{ "files": [], "references": [{ "path": "./tsconfig.src.json" }] }
`,
		"lib/tsconfig.src.json": `{
  "compilerOptions": { "composite": true, "module": "preserve", "moduleResolution": "bundler" },
  "include": ["src/**/*.ts"]
}
`,
		"lib/src/index.ts": `export function lib() { return 4; }
`,
		"tools/run.ts": `export function run() { return 5; }
`,
	})

	modulePaths := func(tsconfigFiles bool) []string {
		t.Helper()
		snapshot, err := BuildSnapshot(context.Background(), BuildSnapshotRequest{
			WorkspaceRoot: root,
			Files:         files,
			TsconfigFiles: tsconfigFiles,
		})
		if err != nil {
			t.Fatal(err)
		}
		paths := make([]string, 0, len(snapshot.Modules))
		for _, item := range snapshot.Modules {
			paths = append(paths, item.Path)
		}
		for _, item := range snapshot.Functions {
			if !slices.Contains(paths, item.FilePath) {
				t.Fatalf("function %s is in %s, which is not a module", item.Name, item.FilePath)
			}
		}
		return paths
	}

	assertEqualKeys(t, modulePaths(true), []string{
		"app/src/legacy/shared.ts",
		"app/src/main.ts",
		"lib/src/index.ts",
		"tools/run.ts",
	})
	assertEqualKeys(t, modulePaths(false), []string{
		"app/scripts/build.ts",
		"app/src/legacy/old.ts",
		"app/src/legacy/shared.ts",
		"app/src/main.ts",
		"lib/src/index.ts",
		"tools/run.ts",
	})

	if err := os.WriteFile(filepath.Join(root, "lib/tsconfig.src.json"), []byte(`{ "include": [`), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := BuildSnapshot(context.Background(), BuildSnapshotRequest{WorkspaceRoot: root, Files: files, TsconfigFiles: true}); err == nil {
		t.Fatal("expected an error for a referenced tsconfig that does not parse")
	}
}

func moduleKeys(snapshot *Snapshot) []string {
	keys := make([]string, 0, len(snapshot.Modules))
	for _, item := range snapshot.Modules {
		keys = append(keys, item.SemanticKey)
	}
	slices.Sort(keys)
	return keys
}

func functionKeys(snapshot *Snapshot) []string {
	keys := make([]string, 0, len(snapshot.Functions))
	for _, item := range snapshot.Functions {
		keys = append(keys, item.SemanticKey)
	}
	slices.Sort(keys)
	return keys
}

func importKeys(snapshot *Snapshot) []string {
	keys := make([]string, 0, len(snapshot.ImportEdges))
	for _, item := range snapshot.ImportEdges {
		keys = append(keys, item.SemanticKey)
	}
	slices.Sort(keys)
	return keys
}

func callKeys(snapshot *Snapshot) []string {
	keys := make([]string, 0, len(snapshot.CallEdges))
	for _, item := range snapshot.CallEdges {
		keys = append(keys, item.SemanticKey)
	}
	slices.Sort(keys)
	return keys
}

func typeRefKeys(snapshot *Snapshot) []string {
	keys := make([]string, 0, len(snapshot.TypeRefs))
	for _, item := range snapshot.TypeRefs {
		keys = append(keys, item.SemanticKey)
	}
	slices.Sort(keys)
	return keys
}

func typeDisplay(snapshot *Snapshot, id string) string {
	for _, item := range snapshot.Types {
		if item.ID == id {
			return item.Display
		}
	}
	return ""
}
