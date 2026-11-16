package typescript

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/bigbizze/lintai/backend"
	"github.com/bigbizze/lintai/facts"
	"github.com/bigbizze/lintai/internal/tsgo/lintaiapi"
	"github.com/bigbizze/lintai/internal/workspace"
)

func hasSnapshotInput(snapshot *lintaiapi.Snapshot, path string) bool {
	return slices.ContainsFunc(snapshot.Inputs, func(input lintaiapi.SnapshotInput) bool { return input.Path == path })
}

// V04, V09, V10: relation demand must preserve rows and eligible reporting
// support from a full collection, including metadata-only collection.
func TestRequestedRelationsMatchFullCollection(t *testing.T) {
	root := t.TempDir()
	writeWorkspaceFile(t, root, "src/main.ts", `
import { helper } from "./other";
export { external } from "../dist/generated";
export type Result = { value: string };
export function calculate(value: Result): Result { return value; }
calculate({ value: helper("ok") });
`)
	writeWorkspaceFile(t, root, "src/other.ts", `export function helper(value: string): string { return value; }`)
	writeWorkspaceFile(t, root, "dist/generated.d.ts", "export declare function external(): void;\n//# sourceMappingURL=generated.d.ts.map\n")
	writeWorkspaceFile(t, root, "dist/generated.d.ts.map", `{"version":3,"file":"generated.d.ts","sources":["../source.ts"],"names":[],"mappings":"AAAA"}`)
	writeWorkspaceFile(t, root, "source.ts", `export function external(): void {}`)
	producer := New(Options{Files: FileScopeWalk})
	description, err := producer.Describe()
	if err != nil {
		t.Fatal(err)
	}
	byName := make(map[string]facts.RelationDescription)
	all := make([]facts.Predicate, 0, len(description.BaseRelations))
	for _, relation := range description.BaseRelations {
		byName[relation.Predicate.Name] = relation
		all = append(all, relation.Predicate)
	}
	collect := func(predicates []facts.Predicate) facts.Batch {
		t.Helper()
		batch, err := producer.Collect(context.Background(), backend.CollectionRequest{RepoRoot: root, WorkspaceRoot: root, Predicates: predicates})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := facts.ValidateBatch(description, predicates, batch); err != nil {
			t.Fatal(err)
		}
		return batch
	}
	full := collect(all)
	rowSet := func(rows []facts.Row) map[string]bool {
		result := make(map[string]bool, len(rows))
		for _, row := range rows {
			result[fmt.Sprintf("%#v", row)] = true
		}
		return result
	}
	selections := [][]string{
		{"ts.module"},
		{"ts.declaration", "ts.declaration_origin"},
		{"ts.module_export", "ts.export_incomplete"},
		{"ts.call_declaration", "ts.call_declaration_incomplete"},
		{"ts.fn_signature", "ts.signature_param", "ts.signature_returns", "ts.declaration_type"},
		{"ts.type_kind", "ts.type_display"},
	}
	for _, relation := range description.BaseRelations {
		selections = append(selections, []string{relation.Predicate.Name})
	}
	for _, names := range selections {
		predicates := make([]facts.Predicate, len(names))
		for i, name := range names {
			predicates[i] = byName[name].Predicate
		}
		selected := collect(predicates)
		for _, predicate := range predicates {
			if !reflect.DeepEqual(rowSet(selected.Relations[predicate]), rowSet(full.Relations[predicate])) {
				t.Errorf("%s rows differ: selected=%v full=%v", predicate, selected.Relations[predicate], full.Relations[predicate])
			}
			for _, row := range selected.Relations[predicate] {
				for index, argument := range byName[predicate.Name].Arguments {
					if argument.ReportSupport == facts.ReportNone {
						continue
					}
					id := row[index].String
					got, gotOK := selected.Anchors[id]
					want, wantOK := full.Anchors[id]
					if gotOK != wantOK || !reflect.DeepEqual(got, want) {
						t.Errorf("%s support for %q differs: selected=%+v/%t full=%+v/%t", predicate, id, got, gotOK, want, wantOK)
					}
				}
			}
		}
	}
}

func TestBuildSnapshotExposesStableFunctionKeysAndCalls(t *testing.T) {
	repoRoot, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	workspaceRoot := filepath.Join(repoRoot, "testdata/fixtures/workspace")

	snapshot, _, _, err := New(Options{}).buildLintaiSnapshot(context.Background(), repoRoot, workspaceRoot)
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

func TestTwoTypeScriptProducersKeepConstructionSettingsIndependent(t *testing.T) {
	// R1 API fixture: two TypeScript producer instances use separate file
	// scopes through the public producer contract, without shared settings.
	root := t.TempDir()
	writeWorkspaceFile(t, root, "tsconfig.json", `{"compilerOptions":{"module":"preserve","moduleResolution":"bundler"},"files":["src/one.ts"]}`)
	writeWorkspaceFile(t, root, "src/one.ts", "export const one = 1;\n")
	writeWorkspaceFile(t, root, "src/two.ts", "export const two = 2;\n")

	collectModules := func(producer *Backend) []string {
		t.Helper()
		description, err := producer.Describe()
		if err != nil {
			t.Fatal(err)
		}
		requested := make([]facts.Predicate, len(description.BaseRelations))
		for index, relation := range description.BaseRelations {
			requested[index] = relation.Predicate
		}
		batch, err := producer.Collect(context.Background(), backend.CollectionRequest{
			RepoRoot: root, WorkspaceRoot: root, Predicates: requested,
		})
		if err != nil {
			t.Fatal(err)
		}
		validated, err := facts.ValidateBatch(description, requested, batch)
		if err != nil {
			t.Fatal(err)
		}
		var result []string
		for _, row := range validated.Rows(facts.Predicate{Name: "ts.module", Arity: 1}) {
			result = append(result, row[0].String)
		}
		slices.Sort(result)
		return result
	}

	tsconfigProducer := New(Options{Files: FileScopeTsconfig})
	walkProducer := New(Options{Files: FileScopeWalk})
	if got := collectModules(tsconfigProducer); !slices.Equal(got, []string{"ts:module:src/one.ts"}) {
		t.Fatalf("tsconfig producer modules = %v", got)
	}
	if got := collectModules(walkProducer); !slices.Equal(got, []string{"ts:module:src/one.ts", "ts:module:src/two.ts"}) {
		t.Fatalf("walk producer modules = %v", got)
	}
	if got := collectModules(tsconfigProducer); !slices.Equal(got, []string{"ts:module:src/one.ts"}) {
		t.Fatalf("second producer changed the first producer's settings: %v", got)
	}
	if foo, prefixedFoo := producerEntityID("foo.ts"), producerEntityID("ts:foo.ts"); foo == prefixedFoo || foo != "ts:foo.ts" || prefixedFoo != "ts:ts:foo.ts" {
		t.Fatalf("producer-local entity IDs are not uniquely namespaced: %q and %q", foo, prefixedFoo)
	}
}

func TestExactSourceAndExcludedGlobalUseOneProductionScope(t *testing.T) {
	root := t.TempDir()
	writeWorkspaceFile(t, root, "tsconfig.json", `{"compilerOptions":{"module":"preserve","moduleResolution":"bundler"},"files":["src/main.ts","src/globals.ts"]}`)
	writeWorkspaceFile(t, root, "src/main.ts", `export function read() { return hiddenGlobal.secret; }`)
	writeWorkspaceFile(t, root, "src/globals.ts", `declare global { var hiddenGlobal: { secret: string }; } export {};`)
	writeWorkspaceFile(t, root, "src/extra.ts", `export const selected = 1;`)
	options := Options{
		Files: FileScopeTsconfig, WorkspaceExcludes: []string{"src/globals.ts", "src/extra.ts"},
		Sources: []string{"src/extra.ts"},
	}
	snapshot, err := New(options).BuildSnapshot(context.Background(), root, root)
	if err != nil {
		t.Fatal(err)
	}
	modules := make([]string, 0)
	for _, fact := range snapshot.Facts {
		if fact.Relation == "module_path" {
			modules = append(modules, fact.Values[1].String)
		}
	}
	slices.Sort(modules)
	if !slices.Equal(modules, []string{"src/extra.ts", "src/main.ts"}) {
		t.Fatalf("exact and ordinary primary modules = %v", modules)
	}
	var readID, returnType string
	for _, fact := range snapshot.Facts {
		if fact.Relation == "fn" && fact.Values[2].String == "read" {
			readID = fact.Values[0].String
		}
	}
	var signature string
	for _, fact := range snapshot.Facts {
		if fact.Relation == "fn_signature" && fact.Values[0].String == readID {
			signature = fact.Values[1].String
		}
	}
	for _, fact := range snapshot.Facts {
		if fact.Relation == "signature_returns" && fact.Values[0].String == signature {
			returnType = fact.Values[1].String
		}
	}
	if readID == "" || !slices.ContainsFunc(snapshot.Facts, func(fact facts.Fact) bool {
		return fact.Relation == "type_kind" && fact.Values[0].String == returnType && fact.Values[1].String == "string"
	}) {
		t.Fatalf("excluded global declaration did not affect selected code's inferred return type")
	}
	if !slices.Contains(snapshot.InputPaths, filepath.Join(root, "src/globals.ts")) {
		t.Fatal("compiler-read excluded global was absent from captured dependency inputs")
	}
	scopeOptions := workspace.ScopeOptions{Exclude: options.WorkspaceExcludes, Sources: options.Sources}
	explanation, err := lintaiapi.ExplainScope(context.Background(), lintaiapi.BuildSnapshotRequest{
		WorkspaceRoot: root, ScopeOptions: &scopeOptions, TsconfigFiles: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(explanation.CompilerEligible, modules) || !slices.Equal(explanation.ExactSources, []string{"src/extra.ts"}) {
		t.Fatalf("scope explanation differs from production modules: %+v modules=%v", explanation, modules)
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
	build := func(scope FileScope) facts.Snapshot {
		t.Helper()
		snapshot, err := New(Options{Files: scope}).BuildSnapshot(context.Background(), root, root)
		if err != nil {
			t.Fatal(err)
		}
		if err := facts.Validate(snapshot); err != nil {
			t.Fatal(err)
		}
		return snapshot
	}
	walk, filtered := build(FileScopeWalk), build(FileScopeTsconfig)
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
	findFunction := func(snapshot facts.Snapshot, file, name string) string {
		for _, fact := range snapshot.Facts {
			if fact.Relation == "fn" && fact.Values[1].String == file && fact.Values[2].String == name {
				return fact.Values[0].String
			}
		}
		return ""
	}
	target := findFunction(walk, "src/excluded/effect.ts", "effect")
	run := findFunction(filtered, "src/main.ts", "run")
	want := map[int]string{2: "module:src/main.ts", 4: run, 5: run, 9: ""}
	for site, edge := range before["call_edge"] {
		if edge.Values[1].String != target {
			continue
		}
		location := before["location"][site]
		line := int(location.Values[2].Number)
		owner, ok := want[line]
		if !ok {
			t.Fatalf("unexpected call at %v", location)
		}
		delete(want, line)
		if _, ok := after["call_unresolved"][site]; !ok || !slices.Equal(location.Values, after["location"][site].Values) {
			t.Errorf("classification changed identity: %v", edge)
		}
		if owner != "" && after["site_owner"][site].Values[1].String != owner {
			t.Errorf("owner changed: %v", after["site_owner"][site])
		}
		if owner == "" && after["site_attribution_unsupported"][site].Values[1].String != "instance_construction" {
			t.Errorf("lost unsupported owner: %v", after["site_attribution_unsupported"][site])
		}
	}
	if len(want) != 0 {
		t.Errorf("missing original calls: %v", want)
	}
	for _, line := range []int{14, 15} {
		found := false
		for site := range after["fn_value_escape"] {
			location := after["location"][site]
			if int(location.Values[2].Number) != line {
				continue
			}
			found = true
			if _, ok := after["fn_value_escape_unknown"][site]; !ok {
				t.Errorf("filtered callback target was not unknown: %v", location)
			}
			if _, ok := after["call_unresolved"][site]; ok {
				t.Errorf("callback argument became an invocation: %v", location)
			}
			if _, ok := after["call_declaration_incomplete"][site]; ok {
				t.Errorf("callback argument got call provenance: %v", location)
			}
		}
		if !found {
			t.Errorf("missing callback event on line %d", line)
		}
	}

}

func TestCapturedInputRevisionIgnoresAbsoluteRoot(t *testing.T) {
	t.Parallel()

	rootOne := t.TempDir()
	rootTwo := t.TempDir()

	writeWorkspaceFile(t, rootOne, "src/a.ts", "export const alpha = 1;\n")
	writeWorkspaceFile(t, rootOne, "src/b.ts", "export const beta = 2;\n")
	configContents := []byte("[workspace]\nexclude = []\n")
	configDigest := sha256.Sum256(configContents)
	configOne := writeWorkspaceFile(t, rootOne, "lintai.toml", string(configContents))
	writeWorkspaceFile(t, rootTwo, "src/a.ts", "export const alpha = 1;\n")
	writeWorkspaceFile(t, rootTwo, "src/b.ts", "export const beta = 2;\n")
	configTwo := writeWorkspaceFile(t, rootTwo, "lintai.toml", string(configContents))

	options := func(path string) Options {
		return Options{Files: FileScopeWalk, ConfigPath: path, ConfigPresent: true, ConfigDigest: hex.EncodeToString(configDigest[:])}
	}
	left, err := New(options(configOne)).BuildSnapshot(context.Background(), rootOne, rootOne)
	if err != nil {
		t.Fatal(err)
	}
	right, err := New(options(configTwo)).BuildSnapshot(context.Background(), rootTwo, rootTwo)
	if err != nil {
		t.Fatal(err)
	}
	if left.InputRevision != right.InputRevision {
		t.Fatalf("expected stable input revision across roots, got %q != %q", left.InputRevision, right.InputRevision)
	}
	if !slices.Contains(left.InputPaths, configOne) || !slices.Contains(right.InputPaths, configTwo) {
		t.Fatalf("lintai.toml was missing from producer input evidence: left=%v right=%v", left.InputPaths, right.InputPaths)
	}
	for _, snapshot := range []facts.Snapshot{left, right} {
		seen := make(map[string]struct{}, len(snapshot.InputPaths))
		for _, path := range snapshot.InputPaths {
			if _, exists := seen[path]; exists {
				t.Fatalf("captured input %q was published more than once: %v", path, snapshot.InputPaths)
			}
			seen[path] = struct{}{}
		}
	}
}

func TestSymlinkedWorkspaceRootKeepsScopeAndIdentity(t *testing.T) {
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	writeWorkspaceFile(t, root, "tsconfig.json", `{"files":["src/a.ts"]}`)
	writeWorkspaceFile(t, root, "src/a.ts", "export const alpha = 1;\n")
	writeWorkspaceFile(t, root, "source-links.json", `{"version":1,"entries":[]}`)
	alias := filepath.Join(t.TempDir(), "workspace")
	if err := os.Symlink(root, alias); err != nil {
		t.Skipf("symlinks are unavailable: %v", err)
	}
	options := Options{Files: FileScopeTsconfig, Sources: []string{"src/a.ts"}, SourceLinks: filepath.Join(root, "source-links.json")}
	realSnapshot, err := New(options).BuildSnapshot(context.Background(), root, root)
	if err != nil {
		t.Fatal(err)
	}
	options.SourceLinks = filepath.Join(alias, "source-links.json")
	aliasSnapshot, err := New(options).BuildSnapshot(context.Background(), alias, alias)
	if err != nil {
		t.Fatal(err)
	}
	if realSnapshot.InputRevision != aliasSnapshot.InputRevision || !reflect.DeepEqual(realSnapshot.Scope, aliasSnapshot.Scope) {
		t.Fatalf("symlinked root changed scope or input revision: %q != %q", realSnapshot.InputRevision, aliasSnapshot.InputRevision)
	}
	if len(realSnapshot.Facts) != len(aliasSnapshot.Facts) {
		t.Fatalf("symlinked root changed fact count: %d != %d", len(realSnapshot.Facts), len(aliasSnapshot.Facts))
	}
	for _, fact := range realSnapshot.Facts {
		if !slices.ContainsFunc(aliasSnapshot.Facts, func(other facts.Fact) bool { return reflect.DeepEqual(fact, other) }) {
			t.Fatalf("symlinked root lost fact: %+v", fact)
		}
	}
	if !slices.ContainsFunc(aliasSnapshot.Facts, func(fact facts.Fact) bool {
		return fact.Relation == "module_path" && len(fact.Values) > 1 && fact.Values[1].String == "src/a.ts"
	}) {
		t.Fatal("symlinked root did not produce the workspace-relative module path")
	}
	scopeOptions := workspace.ScopeOptions{Sources: options.Sources}
	explanation, err := lintaiapi.ExplainScope(context.Background(), lintaiapi.BuildSnapshotRequest{
		WorkspaceRoot: alias, ScopeOptions: &scopeOptions, TsconfigFiles: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(explanation.CompilerEligible, []string{"src/a.ts"}) || !slices.Equal(explanation.ExactSources, []string{"src/a.ts"}) {
		t.Fatalf("symlinked root scope explanation = %+v", explanation)
	}
	if got := workspace.RelativePath(alias, filepath.Join(root, "src/a.ts")); got != "src/a.ts" {
		t.Fatalf("relative path from symlinked root = %q", got)
	}
	if !slices.Contains(aliasSnapshot.InputPaths, filepath.Join(alias, "src/a.ts")) {
		t.Fatalf("symlinked root input paths = %v", aliasSnapshot.InputPaths)
	}
	plan, err := workspace.PlanSources(alias, workspace.ScopeOptions{Sources: []string{filepath.Join(alias, "src/a.ts")}})
	if err != nil {
		t.Fatalf("absolute exact source through symlinked root: %v", err)
	}
	if valid, err := workspace.ValidateScope(plan); err != nil || !valid {
		t.Fatalf("validate scope with absolute exact source: valid=%t err=%v", valid, err)
	}
	other := t.TempDir()
	writeWorkspaceFile(t, other, "tsconfig.json", `{"files":["src/a.ts"]}`)
	writeWorkspaceFile(t, other, "src/a.ts", "export const alpha = 1;\n")
	writeWorkspaceFile(t, other, "source-links.json", `{"version":1,"entries":[]}`)
	if err := os.Remove(alias); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(other, alias); err != nil {
		t.Fatal(err)
	}
	if valid, err := workspace.ValidateScope(plan); err != nil || valid {
		t.Fatalf("retargeted workspace validation: valid=%t err=%v", valid, err)
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
	for _, files := range []FileScope{FileScopeTsconfig, FileScopeWalk} {
		snapshot, _, _, err := New(Options{Files: files}).buildLintaiSnapshot(context.Background(), repoRoot, workspaceRoot)
		if err != nil {
			t.Fatalf("%s: %v", files, err)
		}
		var alias *lintaiapi.ImportEdge
		for index := range snapshot.ImportEdges {
			if snapshot.ImportEdges[index].Specifier == "@/lib/helper" {
				alias = &snapshot.ImportEdges[index]
			}
		}
		if alias == nil || !alias.Internal || alias.ToPath != "src/lib/helper.ts" {
			t.Fatalf("%s: expected @/lib/helper to resolve to src/lib/helper.ts, got %+v", files, alias)
		}
		adjustments := convertAdjustments(workspaceRoot, snapshot.ConfigAdjustments)
		got := make([]string, 0, len(adjustments))
		for _, item := range adjustments {
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
	snapshot, _, _, err := New(Options{Files: FileScopeWalk}).buildLintaiSnapshot(context.Background(), root, root)
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

func TestMetadataPreservingInputEditChangesCapturedRevision(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	file := writeWorkspaceFile(t, root, "src/main.ts", "export const value = 1;\n")
	build := func() facts.Snapshot {
		t.Helper()
		result, err := New(Options{Files: FileScopeWalk}).BuildSnapshot(context.Background(), root, root)
		if err != nil {
			t.Fatal(err)
		}
		return result
	}
	before := build()
	info, err := os.Stat(file)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(file, []byte("export const value = 2;\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(file, info.ModTime(), info.ModTime()); err != nil {
		t.Fatal(err)
	}
	updatedInfo, err := os.Stat(file)
	if err != nil {
		t.Fatal(err)
	}
	after := build()
	if before.InputRevision == after.InputRevision {
		t.Fatalf("metadata-preserving content edit kept revision %q", before.InputRevision)
	}
	if !info.ModTime().Equal(updatedInfo.ModTime()) {
		t.Fatal("test edit did not preserve the file modification time")
	}
	if after.InputRevision == "" {
		t.Fatal("captured input revision was empty")
	}
}

func TestSnapshotVersionTracksAssociationConfigAndMissingInputs(t *testing.T) {
	root := t.TempDir()
	writeWorkspaceFile(t, root, "tsconfig.json", `{"compilerOptions":{"strict":true,"module":"preserve","moduleResolution":"bundler"},"include":["src/**/*.ts"]}`)
	writeWorkspaceFile(t, root, "dist/index.d.ts", `export declare const load: number;`)
	writeWorkspaceFile(t, root, "src/index.ts", `export const load = 1;`)
	writeWorkspaceFile(t, root, "src/app.ts", `import { load } from "../dist"; export const result = load;`)
	linksPath := writeWorkspaceFile(t, root, "source-links.json", `{"version":1,"entries":[{"checkedEntry":"dist/index.d.ts","sourceEntry":"src/index.ts"}]}`)
	options := Options{Files: FileScopeTsconfig, SourceLinks: "source-links.json"}
	build := func() (*lintaiapi.Snapshot, string) {
		t.Helper()
		snapshot, _, version, err := New(options).buildLintaiSnapshot(context.Background(), root, root)
		if err != nil {
			t.Fatal(err)
		}
		return snapshot, version
	}
	initial, initialVersion := build()
	mapPath := filepath.Join(root, "dist/index.d.ts.map")
	if !hasSnapshotInput(initial, mapPath) {
		t.Fatalf("missing declaration map path was not included in snapshot inputs: %v", initial.Inputs)
	}
	if err := os.WriteFile(mapPath, []byte(`{"version":3,"file":"index.d.ts","sources":[],"mappings":""}`), 0o644); err != nil {
		t.Fatal(err)
	}
	_, mapCreatedVersion := build()
	if initialVersion == mapCreatedVersion {
		t.Fatal("creating a previously missing declaration map did not invalidate the snapshot")
	}
	missingSource := filepath.Join(root, "src/missing.ts")
	if err := os.WriteFile(linksPath, []byte(`{"version":1,"entries":[{"checkedEntry":"dist/index.d.ts","sourceEntry":"src/missing.ts"}]}`), 0o644); err != nil {
		t.Fatal(err)
	}
	missing, missingVersion := build()
	if mapCreatedVersion == missingVersion || !hasSnapshotInput(missing, missingSource) {
		t.Fatalf("source-links changes or missing source were not retained: version=%s inputs=%v", missingVersion, missing.Inputs)
	}
	writeWorkspaceFile(t, root, "src/missing.ts", `export const load = 2;`)
	_, createdSourceVersion := build()
	if missingVersion == createdSourceVersion {
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
	snapshot, err := New(Options{Files: FileScopeTsconfig, SourceLinks: "source-links.json"}).BuildSnapshot(context.Background(), root, root)
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
	options := Options{Files: FileScopeTsconfig}
	build := func() (*lintaiapi.Snapshot, string) {
		t.Helper()
		snapshot, _, version, err := New(options).buildLintaiSnapshot(context.Background(), root, root)
		if err != nil {
			t.Fatal(err)
		}
		return snapshot, version
	}
	// Without node_modules, resolution probes no file under it.
	if before, _ := build(); !hasSnapshotInput(before, filepath.Join(root, "node_modules")) {
		t.Fatalf("missing node_modules directory was not retained as an input: %v", before.Inputs)
	}
	writeWorkspaceFile(t, root, "node_modules/latepkg/package.json", `{"types":"index.d.ts"}`)
	declarationPath := filepath.Join(root, "node_modules/latepkg/index.d.ts")
	withoutDeclaration, withoutDeclarationVersion := build()
	if !hasSnapshotInput(withoutDeclaration, declarationPath) {
		t.Fatalf("missing package declaration candidate was not retained as an input: %v", withoutDeclaration.Inputs)
	}
	writeWorkspaceFile(t, root, "node_modules/latepkg/index.d.ts", `export declare const value: string;`)
	if _, afterVersion := build(); withoutDeclarationVersion == afterVersion {
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

	snapshot, _, _, err := New(Options{}).buildLintaiSnapshot(context.Background(), root, root)
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

	snapshot, _, _, err := New(Options{}).buildLintaiSnapshot(context.Background(), root, root)
	if err != nil {
		t.Fatal(err)
	}

	var importMetaFound bool
	for _, access := range snapshot.Accesses {
		switch {
		case access.FilePath == "src/env.ts" && access.AccessPath == "import.meta.env":
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

	snapshot, _, _, err := New(Options{}).buildLintaiSnapshot(context.Background(), root, root)
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
	options := Options{Files: FileScopeTsconfig}
	unreadable, _, unreadableVersion, err := New(options).buildLintaiSnapshot(context.Background(), root, root)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.ContainsFunc(unreadable.DeclarationSourceOutcomes, func(item lintaiapi.DeclarationSourceOutcomeFact) bool { return item.Outcome == "unavailable_map" }) ||
		!slices.ContainsFunc(unreadable.Exports, func(item lintaiapi.ExportFact) bool { return item.Name == "value" && len(item.Targets) == 1 }) || !hasSnapshotInput(unreadable, mapPath) {
		t.Fatalf("unreadable optional map lost checked facts, outcome or input: %+v", unreadable)
	}
	if err := os.Chmod(mapPath, 0o600); err != nil {
		t.Fatal(err)
	}
	_, _, readableVersion, err := New(options).buildLintaiSnapshot(context.Background(), root, root)
	if err != nil {
		t.Fatal(err)
	}
	if unreadableVersion == readableVersion {
		t.Fatal("making the map readable did not change the snapshot version")
	}
	linksPath := writeWorkspaceFile(t, root, "source-links.json", `{}`)
	if err := os.Chmod(linksPath, 0); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(linksPath, 0o600) })
	options.SourceLinks = "source-links.json"
	if _, _, _, err := New(options).buildLintaiSnapshot(context.Background(), root, root); err == nil || !strings.Contains(err.Error(), "invalid --source-links") {
		t.Fatalf("unreadable requested configuration was not an error: %v", err)
	}
}

// V07: conflicting signatures remain whole; a parameter from one context
// cannot join to the return of the other through the same signature.
func TestWholeSignatureProjection(t *testing.T) {
	root := t.TempDir()
	files := map[string]string{
		"shared/api.ts":        `export const parse = (value: string | undefined) => value;`,
		"loose/tsconfig.json":  `{"compilerOptions":{"strict":false},"include":["**/*.ts","../shared/**/*.ts"]}`,
		"loose/index.ts":       `export { parse } from "../shared/api";`,
		"strict/tsconfig.json": `{"compilerOptions":{"strict":true},"include":["**/*.ts","../shared/**/*.ts"]}`,
		"strict/index.ts":      `export { parse } from "../shared/api";`,
		"tsconfig.json":        `{"files":["shared/api.ts"],"references":[{"path":"./loose"},{"path":"./strict"}]}`,
	}
	for path, text := range files {
		writeWorkspaceFile(t, root, path, text)
	}
	snapshot, err := New(Options{Files: FileScopeTsconfig}).BuildSnapshot(context.Background(), root, root)
	if err != nil {
		t.Fatal(err)
	}
	var function string
	for _, fact := range snapshot.Facts {
		if fact.Relation == "fn" && fact.Values[1].String == "shared/api.ts" && fact.Values[2].String == "parse" {
			function = fact.Values[0].String
		}
	}
	signatures := make(map[string]bool)
	params, returns := make(map[string]string), make(map[string]string)
	for _, fact := range snapshot.Facts {
		switch fact.Relation {
		case "fn_signature":
			if fact.Values[0].String == function {
				signatures[fact.Values[1].String] = true
			}
		case "signature_param":
			if fact.Values[1].Number == 0 {
				params[fact.Values[0].String] = fact.Values[2].String
			}
		case "signature_returns":
			returns[fact.Values[0].String] = fact.Values[1].String
		}
	}
	if function == "" || len(signatures) != 2 {
		t.Fatalf("signature observations: %v", signatures)
	}
	seen := make(map[string]bool)
	for signature := range signatures {
		if params[signature] == "" || params[signature] != returns[signature] {
			t.Fatalf("signature mixed observations: %s param=%s return=%s", signature, params[signature], returns[signature])
		}
		seen[params[signature]] = true
	}
	if len(seen) != 2 {
		t.Fatalf("fixture did not produce differing signatures: %v", seen)
	}
}
