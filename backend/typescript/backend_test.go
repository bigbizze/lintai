package typescript

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/bigbizze/lintai/backend"
	"github.com/bigbizze/lintai/engine"
	"github.com/bigbizze/lintai/facts"
	"github.com/bigbizze/lintai/internal/analysis"
)

func TestBuildSnapshotExposesStableFunctionKeysAndCalls(t *testing.T) {
	repoRoot, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	workspaceRoot := filepath.Join(repoRoot, "testdata/fixtures/workspace")

	snapshot, err := New().buildAnalysisSnapshot(context.Background(), repoRoot, workspaceRoot, backend.SnapshotOptions{})
	if err != nil {
		t.Fatal(err)
	}

	foundHelperEffect := false
	foundComputeHelper := false
	for _, edge := range snapshot.CallEdges {
		switch {
		case edge.FromName == "loadValue" && edge.ToName == "fetchData":
			foundHelperEffect = true
		case edge.FromName == "compute" && edge.ToName == "loadValue":
			foundComputeHelper = true
		}
	}
	if !foundHelperEffect || !foundComputeHelper {
		t.Fatalf("expected fixture call edges, got %+v", snapshot.CallEdges)
	}
}

func TestFilteredCallTargetsRemainUnresolved(t *testing.T) {
	root := t.TempDir()
	writeWorkspaceFile(t, root, "tsconfig.json", `{"compilerOptions":{"module":"preserve","moduleResolution":"bundler"},"files":["src/main.ts"]}`)
	writeWorkspaceFile(t, root, "src/excluded/tsconfig.json", `{"files":["other.ts"]}`)
	writeWorkspaceFile(t, root, "src/excluded/other.ts", `export const other = 1;`)
	writeWorkspaceFile(t, root, "src/excluded/effect.ts", `export function effect() { return helper(); } function helper() { return 1; }`)
	const mainSource = `import { effect } from "./excluded/effect";
effect();
export function run() {
  effect();
  effect();
  return effect;
}
class C {
  value = effect();
  ref = effect;
}
function kept() { return 1; }
export function use() { return kept(); }
export function escape() { return [1].map(effect); }
class Escaping { value = [1].map(effect); }
`
	writeWorkspaceFile(t, root, "src/main.ts", mainSource)
	build := func(scope backend.FileScope) facts.Snapshot {
		t.Helper()
		snapshot, err := New().BuildSnapshot(context.Background(), root, root, backend.SnapshotOptions{Files: scope})
		if err != nil {
			t.Fatal(err)
		}
		if err := facts.Validate(snapshot); err != nil {
			t.Fatal(err)
		}
		return snapshot
	}
	walk, filtered := build(backend.FileScopeWalk), build(backend.FileScopeTsconfig)
	index := func(snapshot facts.Snapshot) map[string]map[string]facts.Fact {
		result := make(map[string]map[string]facts.Fact)
		for _, fact := range snapshot.Facts {
			if result[fact.Relation] == nil {
				result[fact.Relation] = make(map[string]facts.Fact)
			}
			result[fact.Relation][fact.Values[0].String] = fact
		}
		return result
	}
	before, after := index(walk), index(filtered)
	const target = "src/excluded/effect.ts::function::effect"
	want := map[int]string{2: "src/main.ts", 4: "src/main.ts::function::run", 5: "src/main.ts::function::run", 9: ""}
	for site, edge := range before["call_edge"] {
		if edge.Values[2].String != target {
			continue
		}
		location := before["location"][site]
		line := int(location.Values[2].Number)
		owner, ok := want[line]
		if !ok || edge.Values[1].String != owner {
			t.Fatalf("unexpected original call: %v at %v", edge, location)
		}
		delete(want, line)
		unresolved, ok := after["call_unresolved"][site]
		if !ok || unresolved.Values[1].String != owner || !slices.Equal(location.Values, after["location"][site].Values) {
			t.Errorf("call site changed or disappeared: %v at %v; unresolved %v at %v", edge, location, unresolved, after["location"][site])
		}
		if owner == "src/main.ts::function::run" {
			if fact, ok := after["site_in_fn"][site]; !ok || fact.Values[1].String != owner {
				t.Errorf("call lost its lexical owner: %v", unresolved)
			}
		}
		if line == 9 {
			if fact, ok := after["site_attribution_unsupported"][site]; !ok || fact.Values[1].String != "instance_construction" {
				t.Errorf("call lost its unsupported attribution: %v", unresolved)
			}
		}
	}
	if len(want) != 0 {
		t.Errorf("missing original calls at lines %v", want)
	}
	if len(after["call_unresolved"]) != 6 || len(after["call_edge"]) != 1 || len(after["external_call"]) != 2 {
		t.Errorf("unexpected call outcomes: unresolved %v, edges %v, external %v", after["call_unresolved"], after["call_edge"], after["external_call"])
	}
	for _, check := range []struct {
		line  int
		owner string
	}{
		{14, "src/main.ts::function::escape"},
		{15, ""},
	} {
		var matched bool
		for site, call := range after["call_unresolved"] {
			location := after["location"][site]
			if int(location.Values[2].Number) != check.line {
				continue
			}
			matched = true
			column := strings.Index(strings.Split(mainSource, "\n")[check.line-1], "effect") + 1
			if call.Values[1].String != check.owner || int(location.Values[3].Number) != column || int(location.Values[4].Number) != check.line || int(location.Values[5].Number) != column+len("effect") {
				t.Errorf("escaping argument did not keep its owner and location: %v at %v", call, location)
			}
			if check.owner == "" {
				if marker, ok := after["site_attribution_unsupported"][site]; !ok || marker.Values[1].String != "instance_construction" {
					t.Errorf("escaping argument lost unsupported attribution: %v", call)
				}
			} else if owner, ok := after["site_in_fn"][site]; !ok || owner.Values[1].String != check.owner {
				t.Errorf("escaping argument lost its function owner: %v", call)
			}
		}
		if !matched {
			t.Errorf("missing uncertainty at escaping argument on line %d", check.line)
		}
	}
	for _, edge := range after["call_edge"] {
		if edge.Values[1].String != "src/main.ts::function::use" || edge.Values[2].String != "src/main.ts::function::kept" {
			t.Errorf("unexpected retained call: %v", edge)
		}
	}
	removedRefs := 0
	unsupportedRef := false
	for site, ref := range before["fn_ref"] {
		if ref.Values[2].String == target {
			removedRefs++
			if marker, ok := before["site_attribution_unsupported"][site]; ok && marker.Values[1].String == "instance_construction" {
				unsupportedRef = true
			}
			if _, ok := after["site_attribution_unsupported"][site]; ok {
				t.Errorf("removed reference left an attribution marker: %v", ref)
			}
		}
	}
	if removedRefs != 4 || !unsupportedRef {
		t.Errorf("expected ordinary and escaping references before filtering, got %d, unsupported = %t", removedRefs, unsupportedRef)
	}
	if len(after["fn_ref"]) != 0 {
		t.Errorf("references to filtered functions survived: %v", after["fn_ref"])
	}
	if _, ok := after["module"]["src/excluded/effect.ts"]; ok {
		t.Error("filtered target was added as a module")
	}
	repoRoot, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	writeWorkspaceFile(t, root, "lintai-rules/strict.ts", `import { dl, rule } from "@lintai/sdk";
export default rule("test.filtered-callback").version(1)
  .program(dl`+"`"+`Decl indeterminate(Fn, Name).
    indeterminate(Fn, Name) :- fn(Fn, _, Name), Name = "escape", fn_reaches_unresolved(Fn).`+"`"+`)
  .report("indeterminate").message((row) => row.Name);
`)
	items, err := engine.New(New()).Run(context.Background(), engine.Options{
		RepoRoot: repoRoot, AssetRoot: filepath.Join(repoRoot, "packages/client"), WorkspaceRoot: root,
		RuleGlobs: []string{"lintai-rules/*.ts"}, Files: backend.FileScopeTsconfig,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 1 || items[0].RuleID != "test.filtered-callback" || items[0].Message != "escape" {
		t.Fatalf("filtered callback strict rule = %+v, want escape indeterminate", items)
	}
}

func TestSnapshotVersionIgnoresFileOrderAndAbsoluteRoot(t *testing.T) {
	t.Parallel()

	rootOne := t.TempDir()
	rootTwo := t.TempDir()

	firstOne := writeWorkspaceFile(t, rootOne, "src/a.ts", "export const alpha = 1;\n")
	secondOne := writeWorkspaceFile(t, rootOne, "src/b.ts", "export const beta = 2;\n")
	firstTwo := writeWorkspaceFile(t, rootTwo, "src/a.ts", "export const alpha = 1;\n")
	secondTwo := writeWorkspaceFile(t, rootTwo, "src/b.ts", "export const beta = 2;\n")

	left, err := snapshotVersion(rootOne, []string{secondOne, firstOne})
	if err != nil {
		t.Fatal(err)
	}
	right, err := snapshotVersion(rootTwo, []string{firstTwo, secondTwo})
	if err != nil {
		t.Fatal(err)
	}
	if left != right {
		t.Fatalf("expected stable hash across order and roots, got %q != %q", left, right)
	}
}

// A tsconfig with options that TypeScript 7 removed still loads, its path
// aliases still resolve, and the changes are visible as config_adjusted
// facts. Neither mode writes a file into the workspace.
func TestLegacyTsconfigIsAdjusted(t *testing.T) {
	repoRoot, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	workspaceRoot := filepath.Join(repoRoot, "testdata/fixtures/legacy-tsconfig")
	before := listTree(t, workspaceRoot)
	for _, files := range []backend.FileScope{backend.FileScopeTsconfig, backend.FileScopeWalk} {
		snapshot, err := New().buildAnalysisSnapshot(context.Background(), repoRoot, workspaceRoot, backend.SnapshotOptions{Files: files})
		if err != nil {
			t.Fatalf("%s: %v", files, err)
		}
		var alias *analysis.ImportEdge
		for index := range snapshot.ImportEdges {
			if snapshot.ImportEdges[index].Specifier == "@/lib/helper" {
				alias = &snapshot.ImportEdges[index]
			}
		}
		if alias == nil || !alias.Internal || alias.ToPath != "src/lib/helper.ts" {
			t.Fatalf("%s: expected @/lib/helper to resolve to src/lib/helper.ts, got %+v", files, alias)
		}
		got := make([]string, 0, len(snapshot.Adjustments))
		for _, item := range snapshot.Adjustments {
			got = append(got, item.Config+" "+item.Option+" "+item.Action)
		}
		want := []string{
			"tsconfig.json baseUrl rewritten",
			"tsconfig.json moduleResolution replaced",
			"tsconfig.json paths rewritten",
			"tsconfig.json target removed",
		}
		if !slices.Equal(got, want) {
			t.Fatalf("%s: adjustments\ngot  %q\nwant %q", files, got, want)
		}
	}
	if after := listTree(t, workspaceRoot); !slices.Equal(before, after) {
		t.Fatalf("extraction changed the workspace:\nbefore %q\nafter  %q", before, after)
	}
}

// Walk mode analyzes files outside any tsconfig with a synthetic config,
// which must not be written into the workspace.
func TestWalkModeWritesNothingIntoTheWorkspace(t *testing.T) {
	root := t.TempDir()
	writeWorkspaceFile(t, root, "src/a.ts", "export const a = 1;\n")
	snapshot, err := New().buildAnalysisSnapshot(context.Background(), root, root, backend.SnapshotOptions{Files: backend.FileScopeWalk})
	if err != nil {
		t.Fatal(err)
	}
	if len(snapshot.Modules) != 1 {
		t.Fatalf("expected one module, got %+v", snapshot.Modules)
	}
	if got := listTree(t, root); !slices.Equal(got, []string{"src", "src/a.ts"}) {
		t.Fatalf("extraction changed the workspace: %q", got)
	}
}

func listTree(t *testing.T, root string) []string {
	t.Helper()
	var result []string
	err := filepath.WalkDir(root, func(path string, entry os.DirEntry, err error) error {
		if err != nil || path == root {
			return err
		}
		relative, err := filepath.Rel(root, path)
		result = append(result, filepath.ToSlash(relative))
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	return result
}

func TestSnapshotVersionUsesDelimitersBetweenPathsAndContents(t *testing.T) {
	t.Parallel()

	rootOne := t.TempDir()
	rootTwo := t.TempDir()

	ab := writeWorkspaceFile(t, rootOne, "ab", "cd")
	abc := writeWorkspaceFile(t, rootTwo, "abc", "d")

	left, err := snapshotVersion(rootOne, []string{ab})
	if err != nil {
		t.Fatal(err)
	}
	right, err := snapshotVersion(rootTwo, []string{abc})
	if err != nil {
		t.Fatal(err)
	}
	if left == right {
		t.Fatalf("expected different hashes for path/content boundary cases, got %q", left)
	}
}

func TestSnapshotVersionTracksAssociationConfigAndMissingInputs(t *testing.T) {
	root := t.TempDir()
	writeWorkspaceFile(t, root, "tsconfig.json", `{"compilerOptions":{"strict":true,"module":"preserve","moduleResolution":"bundler"},"include":["src/**/*.ts"]}`)
	writeWorkspaceFile(t, root, "dist/index.d.ts", `export declare const load: number;`)
	writeWorkspaceFile(t, root, "src/index.ts", `export const load = 1;`)
	writeWorkspaceFile(t, root, "src/app.ts", `import { load } from "../dist"; export const result = load;`)
	linksPath := writeWorkspaceFile(t, root, "source-links.json", `{"version":1,"entries":[{"checkedEntry":"dist/index.d.ts","sourceEntry":"src/index.ts"}]}`)
	options := backend.SnapshotOptions{Files: backend.FileScopeTsconfig, SourceLinks: "source-links.json"}
	build := func() *analysis.Snapshot {
		t.Helper()
		snapshot, err := New().buildAnalysisSnapshot(context.Background(), root, root, options)
		if err != nil {
			t.Fatal(err)
		}
		return snapshot
	}
	initial := build()
	mapPath := filepath.Join(root, "dist/index.d.ts.map")
	if !slices.Contains(initial.InputPaths, mapPath) {
		t.Fatalf("missing declaration map path was not included in snapshot inputs: %v", initial.InputPaths)
	}
	if err := os.WriteFile(mapPath, []byte(`{"version":3,"file":"index.d.ts","sources":[],"mappings":""}`), 0o644); err != nil {
		t.Fatal(err)
	}
	mapCreated := build()
	if initial.Version == mapCreated.Version {
		t.Fatal("creating a previously missing declaration map did not invalidate the snapshot")
	}
	missingSource := filepath.Join(root, "src/missing.ts")
	if err := os.WriteFile(linksPath, []byte(`{"version":1,"entries":[{"checkedEntry":"dist/index.d.ts","sourceEntry":"src/missing.ts"}]}`), 0o644); err != nil {
		t.Fatal(err)
	}
	missing := build()
	if mapCreated.Version == missing.Version || !slices.Contains(missing.InputPaths, missingSource) {
		t.Fatalf("source-links changes or missing source were not retained: version=%s inputs=%v", missing.Version, missing.InputPaths)
	}
	writeWorkspaceFile(t, root, "src/missing.ts", `export const load = 2;`)
	createdSource := build()
	if missing.Version == createdSource.Version {
		t.Fatal("creating a previously missing source entry did not invalidate the snapshot")
	}
}

func TestSourceLinksFactsKeepUnappliedEntriesVisible(t *testing.T) {
	root := t.TempDir()
	writeWorkspaceFile(t, root, "tsconfig.json", `{"compilerOptions":{"strict":true,"module":"preserve","moduleResolution":"bundler"},"include":["src/**/*.ts"]}`)
	writeWorkspaceFile(t, root, "dist/index.d.ts", `export declare const load: number;`)
	writeWorkspaceFile(t, root, "src/source.ts", `export const load = 1;`)
	writeWorkspaceFile(t, root, "src/app.ts", `import { load } from "../dist"; export const result = load;`)
	writeWorkspaceFile(t, root, "source-links.json", `{"version":1,"entries":[
		{"checkedEntry":"dist/index.d.ts","sourceEntry":"src/source.ts"},
		{"checkedEntry":"unused/dist/index.d.ts","sourceEntry":"unused/src/index.ts"}
	]}`)
	snapshot, err := New().BuildSnapshot(context.Background(), root, root, backend.SnapshotOptions{Files: backend.FileScopeTsconfig, SourceLinks: "source-links.json"})
	if err != nil {
		t.Fatal(err)
	}
	entries := make(map[string]string)
	statuses := make(map[string][]string)
	attempts := make(map[string]int)
	for _, fact := range snapshot.Facts {
		switch fact.Relation {
		case "source_entry":
			entries[fact.Values[1].String] = fact.Values[0].String
		case "source_entry_applied", "source_entry_unapplied", "source_entry_incomplete":
			statuses[fact.Values[0].String] = append(statuses[fact.Values[0].String], fact.Relation)
		case "source_binding_attempt":
			attempts[fact.Values[1].String]++
		}
	}
	applied, unapplied := entries["dist/index.d.ts"], entries["unused/dist/index.d.ts"]
	if applied == "" || unapplied == "" {
		t.Fatalf("configured entries were not published: %v", entries)
	}
	if !slices.Equal(statuses[applied], []string{"source_entry_applied"}) || attempts[applied] == 0 {
		t.Fatalf("applied entry facts: statuses=%v attempts=%d", statuses[applied], attempts[applied])
	}
	if !slices.Equal(statuses[unapplied], []string{"source_entry_unapplied"}) || attempts[unapplied] != 0 {
		t.Fatalf("unapplied entry facts: statuses=%v attempts=%d", statuses[unapplied], attempts[unapplied])
	}
}

func TestSnapshotTracksMissingPackageDirectoryAndDeclaration(t *testing.T) {
	root := t.TempDir()
	writeWorkspaceFile(t, root, "tsconfig.json", `{"compilerOptions":{"strict":true,"module":"preserve","moduleResolution":"bundler"},"include":["src/**/*.ts"]}`)
	writeWorkspaceFile(t, root, "src/app.ts", `import { value } from "latepkg"; export const result = value;`)
	options := backend.SnapshotOptions{Files: backend.FileScopeTsconfig}
	build := func() *analysis.Snapshot {
		t.Helper()
		snapshot, err := New().buildAnalysisSnapshot(context.Background(), root, root, options)
		if err != nil {
			t.Fatal(err)
		}
		return snapshot
	}
	// Without node_modules, resolution probes no file under it.
	if before := build(); !slices.Contains(before.InputPaths, filepath.Join(root, "node_modules")) {
		t.Fatalf("missing node_modules directory was not retained as an input: %v", before.InputPaths)
	}
	writeWorkspaceFile(t, root, "node_modules/latepkg/package.json", `{"types":"index.d.ts"}`)
	declarationPath := filepath.Join(root, "node_modules/latepkg/index.d.ts")
	withoutDeclaration := build()
	if !slices.Contains(withoutDeclaration.InputPaths, declarationPath) {
		t.Fatalf("missing package declaration candidate was not retained as an input: %v", withoutDeclaration.InputPaths)
	}
	writeWorkspaceFile(t, root, "node_modules/latepkg/index.d.ts", `export declare const value: string;`)
	if after := build(); withoutDeclaration.Version == after.Version {
		t.Fatal("creating the resolved package declaration did not change the snapshot version")
	}
}

func TestBuildSnapshotExposesExpandedMetadataAndCapabilities(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	writeWorkspaceFile(t, root, "tsconfig.json", `{
  "compilerOptions": {
    "target": "es2022",
    "module": "nodenext",
    "moduleResolution": "nodenext",
    "noEmit": true
  },
  "include": ["src/**/*.ts"]
}
`)
	writeWorkspaceFile(t, root, "src/data/db.ts", `export default async function db(input: string): Promise<number> {
	return input.length;
}

export type DbConfig = {
	endpoint: string;
};

export function helper(config: DbConfig): number {
	return config.endpoint.length;
}
`)
	writeWorkspaceFile(t, root, "src/service/repository.ts", `import db, { helper as runHelper, type DbConfig } from "../data/db";
import * as dbModule from "../data/db";

type LocalConfig = DbConfig;

export class Repository {
	async save(config: DbConfig): Promise<number> {
		await db("sample input");
		return runHelper(config) + (await dbModule.default(config.endpoint));
	}
}

export function mirror(config: LocalConfig): DbConfig {
	return config;
}
`)

	snapshot, err := New().buildAnalysisSnapshot(context.Background(), root, root, backend.SnapshotOptions{})
	if err != nil {
		t.Fatal(err)
	}

	var dbFnFound bool
	for _, fn := range snapshot.Functions {
		if fn.Name != "db" {
			continue
		}
		dbFnFound = true
		if !fn.IsExported || !fn.IsAsync {
			t.Fatalf("expected db to be exported async, got %+v", fn)
		}
		if fn.ParameterCount != 1 {
			t.Fatalf("expected db parameter count 1, got %+v", fn)
		}
	}
	if !dbFnFound {
		t.Fatalf("expected to find db function in snapshot, got %+v", snapshot.Functions)
	}

	var mixedImportFound bool
	var namespaceImportFound bool
	for _, edge := range snapshot.ImportEdges {
		if edge.FromPath != "src/service/repository.ts" {
			continue
		}
		switch len(edge.ImportedSymbols) {
		case 3:
			mixedImportFound = true
			if !edge.HasDefaultImport || !edge.HasNamedImports || edge.HasNamespaceImport || edge.IsTypeOnly {
				t.Fatalf("expected mixed import flags, got %+v", edge)
			}
			if edge.ImportedSymbols[0].Name != "db" || edge.ImportedSymbols[0].Kind != "default" || edge.ImportedSymbols[0].IsTypeOnly {
				t.Fatalf("unexpected default import symbol %+v", edge.ImportedSymbols[0])
			}
			if edge.ImportedSymbols[1].Name != "runHelper" || edge.ImportedSymbols[1].Kind != "named" || edge.ImportedSymbols[1].IsTypeOnly {
				t.Fatalf("unexpected named import symbol %+v", edge.ImportedSymbols[1])
			}
			if edge.ImportedSymbols[2].Name != "DbConfig" || edge.ImportedSymbols[2].Kind != "named" || !edge.ImportedSymbols[2].IsTypeOnly {
				t.Fatalf("unexpected type-only import symbol %+v", edge.ImportedSymbols[2])
			}
		case 1:
			if edge.ImportedSymbols[0].Kind == "namespace" {
				namespaceImportFound = true
				if !edge.HasNamespaceImport || edge.HasDefaultImport || edge.HasNamedImports || edge.IsTypeOnly {
					t.Fatalf("expected namespace import flags, got %+v", edge)
				}
			}
		}
	}
	if !mixedImportFound || !namespaceImportFound {
		t.Fatalf("expected repository imports to expose structured symbols, got %+v", snapshot.ImportEdges)
	}

	var targetFound bool
	for _, ref := range snapshot.TypeRefs {
		if ref.FilePath == "src/service/repository.ts" && ref.Name == "DbConfig" && ref.TargetPath == "src/data/db.ts" {
			targetFound = true
			break
		}
	}
	if !targetFound {
		t.Fatalf("expected DbConfig type ref to resolve to src/data/db.ts, got %+v", snapshot.TypeRefs)
	}
}

func TestBuildSnapshotExposesImportMetaAccessesAndIgnoresStrings(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	writeWorkspaceFile(t, root, "tsconfig.json", `{
  "compilerOptions": {
    "target": "es2022",
    "module": "nodenext",
    "moduleResolution": "nodenext",
    "noEmit": true
  },
  "include": ["src/**/*.ts"]
}
`)
	writeWorkspaceFile(t, root, "src/env.ts", `export function readEnv() {
	return import.meta.env.API_URL;
}
`)
	writeWorkspaceFile(t, root, "src/strings.ts", `export function literalEnv() {
	return "import.meta.env.API_URL";
}
`)

	snapshot, err := New().buildAnalysisSnapshot(context.Background(), root, root, backend.SnapshotOptions{})
	if err != nil {
		t.Fatal(err)
	}

	var importMetaFound bool
	for _, access := range snapshot.Accesses {
		switch {
		case access.FilePath == "src/env.ts" && access.Root == "import.meta" && access.AccessPath == "import.meta.env" && access.Origin == "special_form":
			importMetaFound = true
			if access.Range.File != "src/env.ts" || access.Range.StartLine != 2 {
				t.Fatalf("unexpected import.meta access range %+v", access.Range)
			}
		case access.FilePath == "src/strings.ts":
			t.Fatalf("expected string literal to be ignored, got %+v", access)
		}
	}
	if !importMetaFound {
		t.Fatalf("expected import.meta access in snapshot, got %+v", snapshot.Accesses)
	}
}

func TestBuiltinAmbientSources(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	writeWorkspaceFile(t, root, "tsconfig.json", `{"compilerOptions":{"strict":true,"module":"esnext","moduleResolution":"bundler","target":"es2022","lib":["es2022","dom"]}}`)
	writeWorkspaceFile(t, root, "src/a.ts", `import { randomUUID } from "node:crypto";
import * as nodeCrypto from "node:crypto";
import { setTimeout as later } from "node:timers";
import { cwd } from "node:process";
export function a() {
	Math.random();
	crypto.randomUUID();
	randomUUID();
	nodeCrypto.randomBytes(4);
	crypto.getRandomValues(new Uint8Array(2));
	fetch("x");
	globalThis.fetch("y");
	setTimeout(() => {}, 1);
	later(() => {}, 1);
	setInterval(() => {}, 2);
	process.cwd();
	cwd();
	process.argv;
	const record = { fetch: 1, random: 2 };
	return [record.fetch, Math.max(1)];
}
`)

	snapshot, err := New().buildAnalysisSnapshot(context.Background(), root, root, backend.SnapshotOptions{})
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, use := range snapshot.AmbientUses {
		got = append(got, use.Source+"."+use.Capability)
	}
	slices.Sort(got)
	want := []string{
		"network.fetch", "network.fetch",
		"process.argv", "process.cwd", "process.cwd",
		"random.bytes", "random.bytes", "random.number", "random.uuid", "random.uuid",
		"timer.schedule", "timer.schedule", "timer.schedule",
	}
	if !slices.Equal(got, want) {
		t.Fatalf("got %q, want %q", got, want)
	}
}

func writeWorkspaceFile(t *testing.T, root, relativePath, contents string) string {
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

func TestUnreadableOptionalMapKeepsCheckedFactsAndChangesVersionWhenReadable(t *testing.T) {
	root := t.TempDir()
	writeWorkspaceFile(t, root, "tsconfig.json", `{"compilerOptions":{"module":"preserve","moduleResolution":"bundler"},"include":["src/**/*.ts"]}`)
	writeWorkspaceFile(t, root, "dist/index.d.ts", "export declare const value: number;\n//# sourceMappingURL=index.d.ts.map\n")
	writeWorkspaceFile(t, root, "src/app.ts", `export { value } from "../dist";`)
	mapPath := writeWorkspaceFile(t, root, "dist/index.d.ts.map", `{"version":3,"file":"index.d.ts","sources":[],"mappings":""}`)
	if err := os.Chmod(mapPath, 0); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(mapPath, 0o600) })
	if _, err := os.ReadFile(mapPath); err == nil {
		t.Skip("current user can read mode-000 files")
	}
	options := backend.SnapshotOptions{Files: backend.FileScopeTsconfig}
	unreadable, err := New().buildAnalysisSnapshot(context.Background(), root, root, options)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.ContainsFunc(unreadable.DeclarationSourceOutcomes, func(item analysis.DeclarationSourceOutcome) bool { return item.Outcome == "unavailable_map" }) ||
		!slices.ContainsFunc(unreadable.Exports, func(item analysis.Export) bool { return item.Name == "value" && len(item.Targets) == 1 }) || !slices.Contains(unreadable.InputPaths, mapPath) {
		t.Fatalf("unreadable optional map lost checked facts, outcome or input: %+v", unreadable)
	}
	if err := os.Chmod(mapPath, 0o600); err != nil {
		t.Fatal(err)
	}
	readable, err := New().buildAnalysisSnapshot(context.Background(), root, root, options)
	if err != nil {
		t.Fatal(err)
	}
	if unreadable.Version == readable.Version {
		t.Fatal("making the map readable did not change the snapshot version")
	}
	linksPath := writeWorkspaceFile(t, root, "source-links.json", `{}`)
	if err := os.Chmod(linksPath, 0); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(linksPath, 0o600) })
	options.SourceLinks = "source-links.json"
	if _, err := New().buildAnalysisSnapshot(context.Background(), root, root, options); err == nil || !strings.Contains(err.Error(), "invalid --source-links") {
		t.Fatalf("unreadable requested configuration was not an error: %v", err)
	}
}
