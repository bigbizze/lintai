package typescript

import (
	"context"
	"fmt"

	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/bigbizze/lintai/backend"
	"github.com/bigbizze/lintai/engine"
	"github.com/bigbizze/lintai/facts"
	"github.com/bigbizze/lintai/internal/ambient"
	"github.com/bigbizze/lintai/internal/testpack"
	"github.com/bigbizze/lintai/internal/tsgo/lintaiapi"
	"github.com/bigbizze/lintai/internal/workspace"
	"github.com/bigbizze/lintai/observe"
)

func hasCompilerInput(snapshot *lintaiapi.Snapshot, path string) bool {
	// Compiler observations use the resolved workspace spelling, including on macOS
	// where the temporary directory is reached through /var's symlink.
	prefix := filepath.Clean(path)
	var suffix []string
	for {
		if resolved, err := filepath.EvalSymlinks(prefix); err == nil {
			for i := len(suffix) - 1; i >= 0; i-- {
				resolved = filepath.Join(resolved, suffix[i])
			}
			path = resolved
			break
		}
		parent := filepath.Dir(prefix)
		if parent == prefix {
			break
		}
		suffix = append(suffix, filepath.Base(prefix))
		prefix = parent
	}
	return slices.ContainsFunc(snapshot.CompilerInputs, func(input lintaiapi.CompilerObservation) bool { return input.Path == path })
}

func hasGenerationObservation(generation *producerGeneration, path string) bool {
	return slices.ContainsFunc(generation.observations.Records(), func(record observe.Record) bool { return record.Path == filepath.Clean(path) })
}

// Inspect the retained producer generation after collecting and validating the
// complete public relation set. Observation tests use its local projection.
func (b *Backend) buildTestGeneration(ctx context.Context, repoRoot, workspaceRoot string) (*producerGeneration, error) {
	description, err := b.Describe()
	if err != nil {
		return nil, err
	}
	predicates := make([]facts.Predicate, 0, len(description.BaseRelations))
	for _, relation := range description.BaseRelations {
		predicates = append(predicates, relation.Predicate)
	}
	_, err = inspectEmission(ctx, b, backend.CollectionRequest{RepoRoot: repoRoot, WorkspaceRoot: workspaceRoot, Predicates: predicates})
	if err != nil {
		return nil, err
	}
	return b.current, nil
}

func (b *Backend) buildLintaiSnapshot(ctx context.Context, repoRoot, workspaceRoot string) (*lintaiapi.Snapshot, []ambient.Source, string, error) {
	generation, sources, err := b.extractSnapshot(ctx, workspaceRoot, nil, nil)
	if err != nil {
		return nil, nil, "", err
	}
	return generation.snapshot, sources, generation.inputRevision, nil
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
export function choose(value: true | "ready"): true | "ready" { return value; }
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
	if byName["ts.import_package"].Arguments[1].Name != "Package" {
		t.Fatal("import_package catalog argument does not match Appendix A")
	}
	collect := func(predicates []facts.Predicate) emissionView {
		t.Helper()
		batch, err := inspectEmission(context.Background(), producer, backend.CollectionRequest{RepoRoot: root, WorkspaceRoot: root, Predicates: predicates})
		if err != nil {
			t.Fatal(err)
		}
		return batch
	}
	full := collect(all)
	fnKind := byName["ts.fn_kind"].Predicate
	if len(full.Relations[fnKind]) == 0 {
		t.Fatal("fixture did not produce a function kind")
	}
	var stringLiteral, booleanLiteral bool
	for _, row := range full.Relations[byName["ts.type_literal"].Predicate] {
		stringLiteral = stringLiteral || row[1] == facts.Name("string") && row[2] == facts.String("ready")
		booleanLiteral = booleanLiteral || row[1] == facts.Name("boolean") && row[2] == facts.Name("true")
	}
	if !stringLiteral || !booleanLiteral {
		t.Fatalf("fixture did not validate string and boolean literal values: %v", full.Relations[byName["ts.type_literal"].Predicate])
	}
	var calculate, signature, namedType string
	for _, row := range full.Relations[byName["ts.fn"].Predicate] {
		if row[2].String == "calculate" {
			calculate = row[0].String
		}
	}
	for _, row := range full.Relations[byName["ts.fn_signature"].Predicate] {
		if row[0].String == calculate {
			signature = row[1].String
		}
	}
	for _, row := range full.Relations[byName["ts.type_named"].Predicate] {
		if row[2].String == "Result" {
			namedType = row[0].String
		}
	}
	for _, item := range []struct{ id, category, name, preview string }{
		{signature, "signature", "calculate", "export function calculate"},
		{namedType, "type", "Result", "export type Result"},
	} {
		support, ok := full.Anchors[item.id]
		if item.id == "" || !ok || support.Tracking != nil || !strings.Contains(support.Description, item.category) || !strings.Contains(support.Description, item.name) || !strings.Contains(support.Description, item.preview) {
			t.Errorf("%s source description or null key missing: %+v", item.category, support)
		}
	}
	rowSet := func(rows [][]facts.Value) map[string]bool {
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
	// V04, V20, V36, V40: demand is order-independent, the retained
	// generation is reused, and returned support cannot mutate its cache.
	requested := []facts.Predicate{byName["ts.fn"].Predicate, byName["ts.site_owner"].Predicate}
	first := collect(requested)
	generation := producer.current
	baseline := collect([]facts.Predicate{requested[1], requested[0]})
	if producer.current != generation {
		t.Fatal("reordered unchanged demand rebuilt the producer generation")
	}
	if len(first.Relations[requested[0]]) == 0 || first.Anchors[calculate].Tracking == nil {
		t.Fatal("fixture did not produce a tracked function")
	}
	first.Relations[requested[0]][0][0].String = "changed by caller"
	first.Anchors[calculate].Tracking.Data.(map[string]any)["subject"].([]any)[0] = "changed by caller"
	first.Scope.(map[string]any)["primary"].([]string)[0] = "changed by caller"
	after := collect(requested)
	if producer.current != generation || !reflect.DeepEqual(after, baseline) {
		t.Fatal("caller mutation changed the retained producer generation")
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
		batch, err := inspectEmission(context.Background(), producer, backend.CollectionRequest{
			RepoRoot: root, WorkspaceRoot: root, Predicates: requested,
		})
		if err != nil {
			t.Fatal(err)
		}
		var result []string
		for _, row := range batch.Relations[facts.Predicate{Name: "ts.module", Arity: 1}] {
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
	generation := tsconfigProducer.current
	if got := collectModules(walkProducer); !slices.Equal(got, []string{"ts:module:src/one.ts", "ts:module:src/two.ts"}) {
		t.Fatalf("walk producer modules = %v", got)
	}
	if got := collectModules(tsconfigProducer); !slices.Equal(got, []string{"ts:module:src/one.ts"}) {
		t.Fatalf("second producer changed the first producer's settings: %v", got)
	}
	if tsconfigProducer.current != generation {
		t.Fatal("V20: unchanged producer generation was extracted again")
	}
	writeWorkspaceFile(t, root, "src/one.ts", "export const one = 2;\n")
	collectModules(tsconfigProducer)
	if tsconfigProducer.current == generation {
		t.Fatal("V18: changed content did not replace the producer generation")
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
	snapshot, err := New(options).buildTestGeneration(context.Background(), root, root)
	if err != nil {
		t.Fatal(err)
	}
	modules := make([]string, 0)
	for _, fact := range generationItems(t, snapshot) {
		if fact.Relation == "module_path" {
			modules = append(modules, fact.Values[1].String)
		}
	}
	slices.Sort(modules)
	if !slices.Equal(modules, []string{"src/extra.ts", "src/main.ts"}) {
		t.Fatalf("exact and ordinary primary modules = %v", modules)
	}
	var readID, returnType string
	for _, fact := range generationItems(t, snapshot) {
		if fact.Relation == "fn" && fact.Values[2].String == "read" {
			readID = fact.Values[0].String
		}
	}
	var signature string
	for _, fact := range generationItems(t, snapshot) {
		if fact.Relation == "fn_signature" && fact.Values[0].String == readID {
			signature = fact.Values[1].String
		}
	}
	for _, fact := range generationItems(t, snapshot) {
		if fact.Relation == "signature_returns" && fact.Values[0].String == signature {
			returnType = fact.Values[1].String
		}
	}
	if readID == "" || !slices.ContainsFunc(generationItems(t, snapshot), func(fact testFact) bool {
		return fact.Relation == "type_kind" && fact.Values[0].String == returnType && fact.Values[1].String == "string"
	}) {
		t.Fatalf("excluded global declaration did not affect selected code's inferred return type")
	}
	if !hasCompilerInput(snapshot.snapshot, filepath.Join(root, "src/globals.ts")) {
		t.Fatal("compiler-read excluded global was absent from captured dependency inputs")
	}
	explanation, err := ExplainScope(context.Background(), root, options)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(explanation.CompilerEligible, modules) || !slices.Equal(explanation.ExactSources, []string{"src/extra.ts"}) {
		t.Fatalf("scope explanation differs from production modules: %+v modules=%v", explanation, modules)
	}
}

// V04, V09, V10: a declaration retained for a call is not a type subject
// unless a retained export calls for its type in that space.
func TestFilteredExportsDefineDeclarationTypeSubjects(t *testing.T) {
	root := t.TempDir()
	writeWorkspaceFile(t, root, "tsconfig.json", `{"compilerOptions":{"module":"preserve","moduleResolution":"bundler"},"files":["src/main.ts"]}`)
	writeWorkspaceFile(t, root, "src/excluded/tsconfig.json", `{"files":["other.ts"]}`)
	writeWorkspaceFile(t, root, "src/excluded/other.ts", `export {};`)
	writeWorkspaceFile(t, root, "src/excluded/effect.ts", `
export function effect(value: string): boolean { return value.length > 0; }
export class Widget {}
export const valueOnly = 123;
`)
	writeWorkspaceFile(t, root, "src/main.ts", `
import { effect } from "./excluded/effect";
export type { Widget, valueOnly } from "./excluded/effect";
export function run(): boolean { return effect("ok"); }
`)
	producer := New(Options{Files: FileScopeTsconfig})
	description, err := producer.Describe()
	if err != nil {
		t.Fatal(err)
	}
	byName := make(map[string]facts.Predicate)
	var all []facts.Predicate
	for _, relation := range description.BaseRelations {
		byName[relation.Predicate.Name] = relation.Predicate
		all = append(all, relation.Predicate)
	}
	collect := func(producer *Backend, predicates []facts.Predicate) emissionView {
		t.Helper()
		batch, err := inspectEmission(context.Background(), producer, backend.CollectionRequest{RepoRoot: root, WorkspaceRoot: root, Predicates: predicates})
		if err != nil {
			t.Fatal(err)
		}
		return batch
	}
	full := collect(producer, all)
	rows := func(batch emissionView, name string) [][]facts.Value { return batch.Relations[byName["ts."+name]] }
	if slices.Contains(full.AnalyzedFiles, "src/excluded/effect.ts") || !slices.Contains(full.AnalyzedFiles, "src/main.ts") {
		t.Fatalf("fixture has wrong primary modules: %v", full.AnalyzedFiles)
	}
	ids := make(map[string]string)
	for _, row := range rows(full, "declaration") {
		ids[row[1].String] = row[0].String
	}
	for _, name := range []string{"effect", "Widget", "valueOnly", "run"} {
		if ids[name] == "" {
			t.Fatalf("missing %s declaration: %v", name, rows(full, "declaration"))
		}
	}
	if !slices.ContainsFunc(rows(full, "call_declaration"), func(row []facts.Value) bool { return row[1].String == ids["effect"] }) {
		t.Fatalf("excluded declaration was not retained for call provenance: %v", rows(full, "call_declaration"))
	}
	wantExports := map[string]struct{ space, declaration string }{
		"Widget": {"type", ids["Widget"]}, "valueOnly": {"type", ids["valueOnly"]}, "run": {"value", ids["run"]},
	}
	for _, row := range rows(full, "module_export") {
		binding, ok := wantExports[row[2].String]
		if !ok || row[1].String != "ts:module:src/main.ts" || row[3].String != binding.space ||
			!slices.ContainsFunc(rows(full, "export_target"), func(target []facts.Value) bool {
				return target[0].String == row[0].String && target[1].String == binding.declaration
			}) {
			t.Fatalf("unexpected retained export or target: %v", row)
		}
		delete(wantExports, row[2].String)
	}
	if len(wantExports) != 0 {
		t.Fatalf("missing retained exports: %v", wantExports)
	}
	want := map[string]string{ids["Widget"]: "type", ids["valueOnly"]: "value", ids["run"]: "value"}
	subjects := make(map[string]string)
	for _, row := range rows(full, "declaration_type_subject") {
		id, space := row[0].String, row[1].String
		if want[id] != space {
			t.Errorf("unexpected declaration type subject: %v", row)
		}
		subjects[id] = space
	}
	if !reflect.DeepEqual(subjects, want) {
		t.Errorf("declaration type subjects = %v, want %v", subjects, want)
	}
	for _, name := range []string{"declaration_type", "declaration_type_incomplete"} {
		for _, row := range rows(full, name) {
			if want[row[0].String] != row[1].String {
				t.Errorf("%s outside retained subject domain: %v", name, row)
			}
		}
	}
	for id, space := range want {
		if !slices.ContainsFunc(rows(full, "declaration_type"), func(row []facts.Value) bool { return row[0].String == id && row[1].String == space }) &&
			!slices.ContainsFunc(rows(full, "declaration_type_incomplete"), func(row []facts.Value) bool { return row[0].String == id && row[1].String == space }) {
			t.Errorf("subject %s /%s has no type or incomplete reason", id, space)
		}
	}
	// The walk scope exposes the excluded function's export type. It must not
	// remain in the filtered type population solely through that discarded root.
	walk := collect(New(Options{Files: FileScopeWalk}), []facts.Predicate{byName["ts.declaration_type"], byName["ts.type_kind"]})
	effectTypes := make(map[string]bool)
	for _, row := range rows(walk, "declaration_type") {
		if row[0].String == ids["effect"] && row[1] == facts.Name("value") {
			effectTypes[row[2].String] = true
		}
	}
	if len(effectTypes) == 0 {
		t.Fatal("walk scope did not expose the excluded export's value type")
	}
	for _, row := range rows(full, "type_kind") {
		if effectTypes[row[0].String] {
			t.Errorf("discarded export kept an otherwise unreachable type: %v", row)
		}
	}
	for _, name := range []string{"declaration_type_subject", "declaration_type", "declaration_type_incomplete"} {
		selected := collect(producer, []facts.Predicate{byName["ts."+name]})
		if !reflect.DeepEqual(rows(selected, name), rows(full, name)) {
			t.Errorf("%s-only collection differs from full collection: %v vs %v", name, rows(selected, name), rows(full, name))
		}
	}
}

// V06, V08: target filtering preserves invocation identity and exposes only
// the callback transfers of the now-unresolved invocation.
func TestFilteredCallTargetsRemainUnresolved(t *testing.T) {
	root := t.TempDir()
	writeWorkspaceFile(t, root, "tsconfig.json", `{"compilerOptions":{"module":"preserve","moduleResolution":"bundler"},"files":["src/main.ts"]}`)
	writeWorkspaceFile(t, root, "src/excluded/tsconfig.json", `{"files":["other.ts"]}`)
	writeWorkspaceFile(t, root, "src/excluded/other.ts", `export const other = 1;`)
	writeWorkspaceFile(t, root, "src/excluded/effect.ts", `export function effect(callback?: () => number) { return helper(); } function helper() { return 1; }`)
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
export function passing() { effect(kept); }
`
	writeWorkspaceFile(t, root, "src/main.ts", mainSource)
	build := func(scope FileScope) *producerGeneration {
		t.Helper()
		snapshot, err := New(Options{Files: scope}).buildTestGeneration(context.Background(), root, root)
		if err != nil {
			t.Fatal(err)
		}
		return snapshot
	}
	walk, filtered := build(FileScopeWalk), build(FileScopeTsconfig)
	index := func(snapshot *producerGeneration) map[string]map[string]testFact {
		result := make(map[string]map[string]testFact)
		for _, fact := range generationItems(t, snapshot) {
			if result[fact.Relation] == nil {
				result[fact.Relation] = make(map[string]testFact)
			}
			result[fact.Relation][fact.Values[0].String] = fact
		}
		return result
	}
	before, after := index(walk), index(filtered)
	findFunction := func(snapshot *producerGeneration, file, name string) string {
		for _, fact := range generationItems(t, snapshot) {
			if fact.Relation == "fn" && fact.Values[1].String == file && fact.Values[2].String == name {
				return fact.Values[0].String
			}
		}
		return ""
	}
	target := findFunction(walk, "src/excluded/effect.ts", "effect")
	run := findFunction(filtered, "src/main.ts", "run")
	want := map[int]string{2: "module:src/main.ts", 4: run, 5: run, 9: "", 16: findFunction(filtered, "src/main.ts", "passing")}
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
	// A call that ceases to be a collected local call must expose its direct
	// callback argument, while the context where the call remains local must not.
	for _, pair := range []struct {
		facts map[string]map[string]testFact
		want  bool
	}{{before, false}, {after, true}} {
		found := false
		for site := range pair.facts["fn_value_escape"] {
			if int(pair.facts["location"][site].Values[2].Number) == 16 {
				found = true
				target, ok := pair.facts["fn_value_escape_target"][site]
				if !ok || target.Values[1].String != findFunction(filtered, "src/main.ts", "kept") {
					t.Errorf("filtered call lost the collected callback target: %s", site)
				}
				if _, ok := pair.facts["call_site"][site]; ok {
					t.Errorf("callback argument became an invocation: %s", site)
				}
			}
		}
		if found != pair.want {
			t.Errorf("filtered local call callback event: got %t, want %t", found, pair.want)
		}
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
	// Strict policy handles callback transfer explicitly; an unknown callback
	// does not by itself make the containing invocation unresolved.
	repoRoot, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	writeWorkspaceFile(t, root, "lintai-rules/strict.ts", `import { dl, rule } from "@lintai/sdk";
export default rule("test.filtered-callback").version(1)
  .program(dl`+"`"+`Decl indeterminate(Fn, Name).
    indeterminate(Fn, Name) :- ts.fn(Fn, _, Name), Name = "escape", ts.site_owner(Site, Fn), ts.fn_value_escape_unknown(Site).`+"`"+`)
  .report("indeterminate").message((row) => row.Name);
`)
	session, err := engine.Open(engine.Options{}, New())
	if err != nil {
		t.Fatal(err)
	}
	defer session.Close()
	result, err := session.Check(context.Background(), engine.Options{
		RepoRoot: repoRoot, WorkspaceRoot: testpack.Build(t, repoRoot, root, "lintai-rules/*.ts"),
	})
	if err != nil {
		t.Fatal(err)
	}
	items := result.Diagnostics
	if len(items) != 1 || items[0].RuleID != "test.filtered-callback" || items[0].Message != "escape" {
		t.Fatalf("filtered callback strict rule = %+v, want escape indeterminate", items)
	}
}

// V06, V08: direct callback arguments have their own source identity and are
// distinct from the invocation. Unsupported transfers produce no evidence.
func TestDirectCallbackEvidence(t *testing.T) {
	root := t.TempDir()
	writeWorkspaceFile(t, root, "node_modules/external/package.json", `{"name":"external","types":"index.d.ts"}`)
	writeWorkspaceFile(t, root, "node_modules/external/index.d.ts", `export declare function accept(...values: unknown[]): void;`)
	writeWorkspaceFile(t, root, "src/main.ts", `import { accept } from "external";
function local(n: number) { return n; }
export function run(callback: (n: number) => number, loose: any, callbacks: Array<(n: number) => number>, flag: boolean, mixed: string | ((n: number) => number)) {
  missing();
  [1].map(callback);
  [1].map(local);
  [1].map(n => missing(n));
  accept(Math.abs);
  missing(callback);
  accept(loose);
  accept(...callbacks);
  accept`+"`"+`hello ${callback}`+"`"+`;
  [1].map(flag ? local : callback);
  [1].map(mixed);
  accept(notDeclared.property);
}
`)
	snapshot, err := New(Options{Files: FileScopeWalk}).buildTestGeneration(context.Background(), root, root)
	if err != nil {
		t.Fatal(err)
	}
	locations := make(map[string]int)
	functions := make(map[string]string)
	for _, item := range generationItems(t, snapshot) {
		switch item.Relation {
		case "location":
			if item.Values[1].String == "src/main.ts" {
				locations[item.Values[0].String] = int(item.Values[2].Number)
			}
		case "fn":
			functions[item.Values[2].String] = item.Values[0].String
		}
	}
	byLine := make(map[int][]string)
	targets := make(map[string]string)
	unknown := make(map[string]bool)
	call := make(map[string]bool)
	provenance := make(map[string]bool)
	references := make(map[string]string)
	for _, item := range generationItems(t, snapshot) {
		site := item.Values[0].String
		switch item.Relation {
		case "fn_value_escape":
			byLine[locations[site]] = append(byLine[locations[site]], site)
		case "fn_value_escape_target":
			targets[site] = item.Values[1].String
		case "fn_value_escape_unknown":
			unknown[site] = true
		case "call_site":
			call[site] = true
		case "call_declaration", "call_declaration_incomplete":
			provenance[site] = true
		case "fn_ref":
			references[site] = item.Values[1].String
		}
	}
	for _, line := range []int{5, 6, 7, 8, 9, 13} {
		if len(byLine[line]) != 1 {
			t.Fatalf("line %d: expected one argument event, got %v", line, byLine[line])
		}
		site := byLine[line][0]
		if call[site] || provenance[site] {
			t.Errorf("line %d: callback argument became an invocation: %s", line, site)
		}
	}
	for _, line := range []int{4, 10, 11, 12, 14, 15} {
		if len(byLine[line]) != 0 {
			t.Errorf("line %d: unsupported or absent transfer produced %v", line, byLine[line])
		}
	}
	for _, line := range []int{5, 8, 9, 13} {
		if !unknown[byLine[line][0]] || targets[byLine[line][0]] != "" {
			t.Errorf("line %d: expected unknown collected target", line)
		}
	}
	localSite := byLine[6][0]
	if targets[localSite] != functions["local"] || references[localSite] != functions["local"] || unknown[localSite] {
		t.Errorf("local callback evidence: target=%q reference=%q unknown=%t", targets[localSite], references[localSite], unknown[localSite])
	}
	if targets[byLine[7][0]] == "" || unknown[byLine[7][0]] {
		t.Error("inline callback did not retain its collected target")
	}
}

func TestCapturedInputRevisionIgnoresAbsoluteRoot(t *testing.T) {
	t.Parallel()

	rootOne, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	rootTwo, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}

	writeWorkspaceFile(t, rootOne, "src/a.ts", "export const alpha = 1;\n")
	writeWorkspaceFile(t, rootOne, "src/b.ts", "export const beta = 2;\n")
	writeWorkspaceFile(t, rootTwo, "src/a.ts", "export const alpha = 1;\n")
	writeWorkspaceFile(t, rootTwo, "src/b.ts", "export const beta = 2;\n")

	left, err := New(Options{Files: FileScopeWalk}).buildTestGeneration(context.Background(), rootOne, rootOne)
	if err != nil {
		t.Fatal(err)
	}
	right, err := New(Options{Files: FileScopeWalk}).buildTestGeneration(context.Background(), rootTwo, rootTwo)
	if err != nil {
		t.Fatal(err)
	}
	if left.inputRevision != right.inputRevision {
		t.Fatalf("expected stable input revision across roots, got %q != %q", left.inputRevision, right.inputRevision)
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
	realSnapshot, err := New(options).buildTestGeneration(context.Background(), root, root)
	if err != nil {
		t.Fatal(err)
	}
	options.SourceLinks = filepath.Join(alias, "source-links.json")
	absoluteSource := filepath.Join(alias, "src/a.ts")
	options.Sources = []string{absoluteSource}
	aliasProducer := New(options)
	aliasSnapshot, err := aliasProducer.buildTestGeneration(context.Background(), alias, alias)
	if err != nil {
		t.Fatal(err)
	}
	if options.Sources[0] != absoluteSource || aliasProducer.settings.Sources[0] != absoluteSource {
		t.Fatal("source normalization mutated caller or producer settings")
	}
	repeated, err := New(options).buildTestGeneration(context.Background(), alias, alias)
	if err != nil {
		t.Fatal(err)
	}
	if repeated.inputRevision != aliasSnapshot.inputRevision || !reflect.DeepEqual(repeated.scope, aliasSnapshot.scope) {
		t.Fatalf("unchanged symlinked-root identity: revision=%q repeated=%q", aliasSnapshot.inputRevision, repeated.inputRevision)
	}
	if !reflect.DeepEqual(realSnapshot.scope, aliasSnapshot.scope) {
		t.Fatalf("symlinked root changed scope: %v != %v", realSnapshot.scope, aliasSnapshot.scope)
	}
	if len(generationItems(t, realSnapshot)) != len(generationItems(t, aliasSnapshot)) {
		t.Fatalf("symlinked root changed fact count: %d != %d", len(generationItems(t, realSnapshot)), len(generationItems(t, aliasSnapshot)))
	}
	for _, fact := range generationItems(t, realSnapshot) {
		if !slices.ContainsFunc(generationItems(t, aliasSnapshot), func(other testFact) bool { return reflect.DeepEqual(fact, other) }) {
			t.Fatalf("symlinked root lost fact: %+v", fact)
		}
	}
	if !slices.ContainsFunc(generationItems(t, aliasSnapshot), func(fact testFact) bool {
		return fact.Relation == "module_path" && len(fact.Values) > 1 && fact.Values[1].String == "src/a.ts"
	}) {
		t.Fatal("symlinked root did not produce the workspace-relative module path")
	}
	explanation, err := ExplainScope(context.Background(), alias, options)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(explanation.CompilerEligible, []string{"src/a.ts"}) || !slices.Equal(explanation.ExactSources, []string{"src/a.ts"}) {
		t.Fatalf("symlinked root scope explanation = %+v", explanation)
	}
	if got := workspace.RelativePath(alias, filepath.Join(root, "src/a.ts")); got != "src/a.ts" {
		t.Fatalf("relative path from symlinked root = %q", got)
	}
	if !hasGenerationObservation(aliasSnapshot, alias) || !hasGenerationObservation(aliasSnapshot, filepath.Join(root, "src/a.ts")) {
		t.Fatal("symlinked root and captured source are missing from observations")
	}
	scopeOptions := workspace.ScopeOptions{Sources: []string{absoluteSource}}
	plan, err := workspace.PlanSources(alias, scopeOptions)
	if err != nil {
		t.Fatalf("absolute exact source through symlinked root: %v", err)
	}
	if scopeOptions.Sources[0] != absoluteSource {
		t.Fatal("source selection mutated caller options")
	}
	if changed, err := plan.Observations.Changed(context.Background(), observe.Full); err != nil || changed != nil {
		t.Fatalf("validate scope with absolute exact source: changed=%v err=%v", changed, err)
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
	if changed, err := plan.Observations.Changed(context.Background(), observe.Full); err != nil || changed == nil {
		t.Fatalf("retargeted workspace validation: changed=%v err=%v", changed, err)
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
	build := func() *producerGeneration {
		t.Helper()
		result, err := New(Options{Files: FileScopeWalk}).buildTestGeneration(context.Background(), root, root)
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
	if before.inputRevision == after.inputRevision {
		t.Fatalf("metadata-preserving content edit kept revision %q", before.inputRevision)
	}
	if !info.ModTime().Equal(updatedInfo.ModTime()) {
		t.Fatal("test edit did not preserve the file modification time")
	}
	if after.inputRevision == "" {
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
	if !hasCompilerInput(initial, mapPath) {
		t.Fatalf("missing declaration map path was not included in snapshot inputs: %v", initial.CompilerInputs)
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
	if mapCreatedVersion == missingVersion || !hasCompilerInput(missing, missingSource) {
		t.Fatalf("source-links changes or missing source were not retained: version=%s inputs=%v", missingVersion, missing.CompilerInputs)
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
	snapshot, err := New(Options{Files: FileScopeTsconfig, SourceLinks: "source-links.json"}).buildTestGeneration(context.Background(), root, root)
	if err != nil {
		t.Fatal(err)
	}
	entries := make(map[string]string)
	statuses := make(map[string][]string)
	attempts := make(map[string]int)
	for _, fact := range generationItems(t, snapshot) {
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
	if before, _ := build(); !hasCompilerInput(before, filepath.Join(root, "node_modules")) {
		t.Fatalf("missing node_modules directory was not retained as an input: %v", before.CompilerInputs)
	}
	writeWorkspaceFile(t, root, "node_modules/latepkg/package.json", `{"types":"index.d.ts"}`)
	declarationPath := filepath.Join(root, "node_modules/latepkg/index.d.ts")
	withoutDeclaration, withoutDeclarationVersion := build()
	if !hasCompilerInput(withoutDeclaration, declarationPath) {
		t.Fatalf("missing package declaration candidate was not retained as an input: %v", withoutDeclaration.CompilerInputs)
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
		if fn.Signature == nil || len(fn.Signature.ParameterTypes) != 1 {
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

	generation, err := New(Options{}).buildTestGeneration(context.Background(), root, root)
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, use := range generation.snapshot.AmbientUses {
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
		!slices.ContainsFunc(unreadable.Exports, func(item lintaiapi.ExportFact) bool { return item.Name == "value" && len(item.Targets) == 1 }) || !hasCompilerInput(unreadable, mapPath) {
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

// A function's owner supplies one whole signature even when other programs
// can see the same source file.
func TestWholeSignatureProjection(t *testing.T) {
	root := t.TempDir()
	files := map[string]string{
		"shared/api.ts": `export const parse = (value: string | undefined) => value;
export function zero() {}
export function marked(first?: string, ...rest: number[]) {}
export const mapped = [1].map((n) => n);`,
		"loose/tsconfig.json":  `{"compilerOptions":{"strict":false},"include":["**/*.ts","../shared/**/*.ts"]}`,
		"loose/index.ts":       `export { parse } from "../shared/api";`,
		"strict/tsconfig.json": `{"compilerOptions":{"strict":true},"include":["**/*.ts","../shared/**/*.ts"]}`,
		"strict/index.ts":      `export { parse } from "../shared/api";`,
		"tsconfig.json":        `{"files":["shared/api.ts"],"references":[{"path":"./loose"},{"path":"./strict"}]}`,
	}
	for path, text := range files {
		writeWorkspaceFile(t, root, path, text)
	}
	snapshot, err := New(Options{Files: FileScopeTsconfig}).buildTestGeneration(context.Background(), root, root)
	if err != nil {
		t.Fatal(err)
	}
	var function string
	for _, fact := range generationItems(t, snapshot) {
		if fact.Relation == "fn" && fact.Values[1].String == "shared/api.ts" && fact.Values[2].String == "parse" {
			function = fact.Values[0].String
		}
	}
	signatures := make(map[string]bool)
	params, returns := make(map[string]string), make(map[string]string)
	for _, fact := range generationItems(t, snapshot) {
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
	if function == "" || len(signatures) != 1 {
		t.Fatalf("signature observations: %v", signatures)
	}
	for signature := range signatures {
		if params[signature] == "" || params[signature] != returns[signature] {
			t.Fatalf("owner signature: %s param=%s return=%s", signature, params[signature], returns[signature])
		}
	}
	byName := make(map[string]string)
	for _, fact := range generationItems(t, snapshot) {
		if fact.Relation == "fn" && fact.Values[1].String == "shared/api.ts" {
			byName[fact.Values[2].String] = fact.Values[0].String
		}
	}
	var zeroSignature, markedSignature string
	var omitted bool
	for _, fact := range generationItems(t, snapshot) {
		switch fact.Relation {
		case "fn_signature":
			switch fact.Values[0].String {
			case byName["zero"]:
				zeroSignature = fact.Values[1].String
			case byName["marked"]:
				markedSignature = fact.Values[1].String
			}
		case "fn_signature_omitted":
			for name, id := range byName {
				if strings.HasPrefix(name, "<anonymous@") && fact.Values[0].String == id {
					omitted = true
				}
			}
		}
	}
	has := func(relation, signature string, position int64) bool {
		return slices.ContainsFunc(generationItems(t, snapshot), func(fact testFact) bool {
			return fact.Relation == relation && fact.Values[0].String == signature && fact.Values[1].Number == position
		})
	}
	if zeroSignature == "" || markedSignature == "" || !omitted || !has("signature_arity", zeroSignature, 0) || !has("signature_param_optional", markedSignature, 0) || !has("signature_param_rest", markedSignature, 1) {
		t.Fatal("zero-arity, optional/rest, or omitted signature evidence was lost")
	}
	// A partial whole observation retains its known slot and incompleteness
	// without acquiring slots or a return from another observation.
	partial := lintaiapi.FunctionSignatureObservation{ParameterTypes: []string{"", "type:known"}, ReturnType: "", Complete: false}
	complete := lintaiapi.FunctionSignatureObservation{ParameterTypes: []string{"type:first", "type:second"}, ReturnType: "type:result", Complete: true}
	partialSnapshot := &lintaiapi.Snapshot{Functions: []lintaiapi.Function{{SemanticKey: "fn:partial", Kind: "function", Signature: &partial}, {SemanticKey: "fn:complete", Kind: "function", Signature: &complete}}}
	description, err := New().Describe()
	if err != nil {
		t.Fatal(err)
	}
	requested := []facts.Predicate{{Name: "ts.signature_arity", Arity: 2}, {Name: "ts.signature_param", Arity: 3}, {Name: "ts.signature_returns", Arity: 2}, {Name: "ts.signature_complete", Arity: 1}, {Name: "ts.signature_incomplete", Arity: 2}}
	projected := inspectLocalSnapshot(t, partialSnapshot, nil, nil, description, requested, true)
	partialID := lintaiapi.SignatureID("fn:partial", partial)
	var partialArity, partialKnown, incomplete bool
	for _, fact := range projected {
		switch fact.Relation {
		case "signature_arity":
			partialArity = partialArity || fact.Values[0].String == partialID && fact.Values[1].Number == 2
		case "signature_param":
			if fact.Values[0].String == partialID {
				if fact.Values[1].Number != 1 || fact.Values[2].String != "type:known" {
					t.Fatalf("partial observation gained a foreign slot: %+v", fact)
				}
				partialKnown = true
			}
		case "signature_returns", "signature_complete":
			if fact.Values[0].String == partialID {
				t.Fatalf("partial observation gained complete evidence: %+v", fact)
			}
		case "signature_incomplete":
			incomplete = incomplete || fact.Values[0].String == partialID && fact.Values[1].String == "unresolved"
		}
	}
	if !partialArity || !partialKnown || !incomplete {
		t.Fatalf("partial signature evidence: arity=%t known=%t incomplete=%t", partialArity, partialKnown, incomplete)
	}
}

func TestGenerationValidatesUnchangedSymlinkMembership(t *testing.T) {
	root := t.TempDir()
	writeWorkspaceFile(t, root, "tsconfig.json", `{"include":["src/**/*.ts"]}`)
	writeWorkspaceFile(t, root, "src/a.ts", `export const value = 1;`)
	if err := os.Mkdir(filepath.Join(root, "empty"), 0o755); err != nil {
		t.Fatal(err)
	}
	for _, link := range []struct{ target, name string }{
		{"a.ts", "link.ts"},
		{"../empty", "linked-dir"},
		{"missing.ts", "dangling.ts"},
	} {
		if err := os.Symlink(link.target, filepath.Join(root, "src", link.name)); err != nil {
			t.Skipf("symlinks unavailable: %v", err)
		}
	}
	producer := New(Options{Files: FileScopeTsconfig})
	generation, err := producer.buildTestGeneration(context.Background(), root, root)
	if err != nil {
		t.Fatal(err)
	}
	again, err := producer.buildTestGeneration(context.Background(), root, root)
	if err != nil || again != generation {
		t.Fatalf("unchanged symlink membership was not reused: err=%v", err)
	}
	snapshot := generation.snapshot
	if len(snapshot.Modules) != 1 || snapshot.Modules[0].Path != "src/a.ts" {
		t.Fatalf("unexpected selected modules: %+v", snapshot.Modules)
	}
}

func TestSourceLinksValidationAndDemand(t *testing.T) {
	root := t.TempDir()
	writeWorkspaceFile(t, root, "tsconfig.json", `{"compilerOptions":{"module":"preserve","moduleResolution":"bundler"},"files":["src/app.ts"]}`)
	writeWorkspaceFile(t, root, "src/app.ts", `import { work } from "../dist/index"; export const result = work();`)
	writeWorkspaceFile(t, root, "dist/index.d.ts", `export declare function work(): string;`)
	writeWorkspaceFile(t, root, "source/index.ts", `export function work(): string { return "ok"; }`)
	writeWorkspaceFile(t, root, "source/tsconfig.json", `{"compilerOptions":{"module":"invalid"},"files":["index.ts"]}`)
	const valid = `{"version":1,"entries":[{"checkedEntry":"dist/index.d.ts","sourceEntry":"source/index.ts","sourceTsconfig":"source/tsconfig.json"}]}`
	writeWorkspaceFile(t, root, "source-links.json", `{"version":2,"entries":[]}`)
	producer := New(Options{Files: FileScopeTsconfig, Sources: []string{"src/app.ts"}, Excludes: []string{"source/**"}, SourceLinks: "source-links.json"})
	description, err := producer.Describe()
	if err != nil {
		t.Fatalf("description read invalid source links: %v", err)
	}
	predicates := make(map[string]facts.Predicate)
	for _, relation := range description.BaseRelations {
		predicates[relation.Predicate.Name] = relation.Predicate
	}
	request := backend.CollectionRequest{RepoRoot: root, WorkspaceRoot: root}
	request.Predicates = []facts.Predicate{predicates["ts.module"], predicates["ts.declaration"], predicates["ts.location"], predicates["ts.config_adjusted"]}
	if _, err := inspectEmission(context.Background(), producer, request); err == nil || !strings.Contains(err.Error(), "invalid --source-links") || producer.current != nil {
		t.Fatalf("ordinary demand did not reject invalid source links: %v", err)
	}
	writeWorkspaceFile(t, root, "source-links.json", valid)
	if _, err := inspectEmission(context.Background(), producer, request); err != nil {
		t.Fatalf("ordinary demand parsed the auxiliary config: %v", err)
	}
	generation := producer.current
	if len(generation.snapshot.SourceEntries) != 0 || len(generation.snapshot.SourceEndpoints) != 0 {
		t.Fatal("ordinary demand collected correspondence")
	}
	if _, err := inspectEmission(context.Background(), producer, request); err != nil || producer.current != generation {
		t.Fatalf("unchanged ordinary demand did not reuse its generation: %v", err)
	}
	writeWorkspaceFile(t, root, "source-links.json", `{"version":2,"entries":[]}`)
	if _, err := inspectEmission(context.Background(), producer, request); err == nil || (producer.current != nil && producer.current != generation) {
		t.Fatalf("failed validation published or returned a new generation: %v", err)
	}
	writeWorkspaceFile(t, root, "source-links.json", valid)
	request.Predicates = []facts.Predicate{predicates["ts.source_entry_incomplete"]}
	batch, err := inspectEmission(context.Background(), producer, request)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.ContainsFunc(batch.Relations[request.Predicates[0]], func(row []facts.Value) bool { return row[1] == facts.Name("unsupported") }) {
		t.Fatalf("correspondence demand did not inspect the auxiliary config: %v", batch.Relations[request.Predicates[0]])
	}
}

func TestCorrespondenceIsolationAndProjection(t *testing.T) {
	root := t.TempDir()
	writeWorkspaceFile(t, root, "tsconfig.json", `{"compilerOptions":{"module":"preserve","moduleResolution":"bundler","baseUrl":"."},"files":["src/app.ts"]}`)
	writeWorkspaceFile(t, root, "src/app.ts", `import { work } from "../dist/index"; export function use() { return work(); }`)
	writeWorkspaceFile(t, root, "dist/index.d.ts", "export declare function work(): string;\n//# sourceMappingURL=index.d.ts.map\n")
	writeWorkspaceFile(t, root, "source/index.ts", `export function work(): string { return "ok"; }`)
	writeWorkspaceFile(t, root, "source/tsconfig.json", `{"compilerOptions":{"module":"preserve","moduleResolution":"bundler","baseUrl":"."},"files":["index.ts"]}`)
	writeWorkspaceFile(t, root, "source-links.json", `{"version":1,"entries":[{"checkedEntry":"dist/index.d.ts","sourceEntry":"source/index.ts","sourceTsconfig":"source/tsconfig.json"}]}`)
	partition := map[string]bool{}
	for _, name := range []string{
		"declaration_source_candidate", "declaration_source", "declaration_source_outcome",
		"source_entry", "source_entry_tsconfig", "source_entry_applied", "source_entry_unapplied", "source_entry_incomplete",
		"source_binding_attempt", "source_binding", "source_binding_checked_decl", "source_binding_source_decl", "source_binding_incomplete",
		"source_endpoint", "source_endpoint_incomplete", "source_config_adjusted",
	} {
		partition["ts."+name] = true
	}
	for _, mode := range []FileScope{FileScopeTsconfig, FileScopeWalk} {
		t.Run(string(mode), func(t *testing.T) {
			base := Options{Files: mode, Sources: []string{"src/app.ts"}, Excludes: []string{"source/**"}}
			off := New(base)
			base.SourceLinks = "source-links.json"
			on := New(base)
			description, err := on.Describe()
			if err != nil {
				t.Fatal(err)
			}
			all := make([]facts.Predicate, 0, len(description.BaseRelations))
			byName := make(map[string]facts.RelationDescription)
			for _, relation := range description.BaseRelations {
				all = append(all, relation.Predicate)
				byName[relation.Predicate.Name] = relation
			}
			collect := func(producer *Backend, predicates []facts.Predicate) emissionView {
				t.Helper()
				batch, err := inspectEmission(context.Background(), producer, backend.CollectionRequest{RepoRoot: root, WorkspaceRoot: root, Predicates: predicates})
				if err != nil {
					t.Fatal(err)
				}
				return batch
			}
			rows := func(items [][]facts.Value) map[string]bool {
				set := make(map[string]bool)
				for _, row := range items {
					set[fmt.Sprintf("%#v", row)] = true
				}
				return set
			}
			compare := func(left, right emissionView, predicates []facts.Predicate) {
				t.Helper()
				for _, predicate := range predicates {
					if !reflect.DeepEqual(rows(left.Relations[predicate]), rows(right.Relations[predicate])) {
						t.Fatalf("%s differs: %v / %v", predicate, left.Relations[predicate], right.Relations[predicate])
					}
					for _, row := range left.Relations[predicate] {
						for i, arg := range byName[predicate.Name].Arguments {
							if arg.ReportSupport == facts.ReportNone {
								continue
							}
							id := row[i].String
							l, lok := left.Anchors[id]
							r, rok := right.Anchors[id]
							if lok != rok || !reflect.DeepEqual(l, r) {
								t.Fatalf("%s support for %s differs: %+v / %+v", predicate, id, l, r)
							}
						}
					}
				}
			}
			var ordinary []facts.Predicate
			for _, predicate := range all {
				if !partition[predicate.Name] {
					ordinary = append(ordinary, predicate)
				}
			}
			collect(on, []facts.Predicate{byName["ts.module_owner_config"].Predicate})
			membership := collect(on, []facts.Predicate{byName["ts.module_config"].Predicate})
			offFull, onFull := collect(off, all), collect(on, all)
			if len(membership.Relations[byName["ts.module_config"].Predicate]) == 0 {
				t.Fatal("membership demand reused the preceding owner-only generation as empty")
			}
			compare(membership, onFull, []facts.Predicate{byName["ts.module_config"].Predicate})
			compare(offFull, onFull, ordinary)
			compare(collect(on, ordinary), onFull, ordinary)
			for _, predicate := range all {
				if partition[predicate.Name] {
					compare(collect(on, []facts.Predicate{predicate}), onFull, []facts.Predicate{predicate})
				}
			}
			checked := onFull.Relations[byName["ts.source_binding_checked_decl"].Predicate]
			endpoints := onFull.Relations[byName["ts.source_endpoint"].Predicate]
			if len(checked) == 0 || len(endpoints) < 2 || len(onFull.Relations[byName["ts.module_owner_config"].Predicate]) == 0 || len(onFull.Relations[byName["ts.module_config"].Predicate]) == 0 {
				t.Fatalf("fixture lacks ownership or endpoint evidence: checked=%v endpoints=%v", checked, endpoints)
			}
			if !slices.ContainsFunc(onFull.Relations[byName["ts.call_declaration"].Predicate], func(row []facts.Value) bool { return row[1] == checked[0][1] }) {
				t.Fatal("fixture lacks an endpoint shared with ordinary call provenance")
			}
			sourceDeclarations := onFull.Relations[byName["ts.source_binding_source_decl"].Predicate]
			if len(sourceDeclarations) == 0 {
				t.Fatal("fixture lacks a source-only declaration endpoint")
			}
			sourceID := sourceDeclarations[0][1].String
			support, ok := onFull.Anchors[sourceID]
			if !ok || support.Location.File != "source/index.ts" || support.Description == "" {
				t.Fatalf("source-only endpoint lacks reporting support: %+v", support)
			}
			if slices.ContainsFunc(onFull.Relations[byName["ts.location"].Predicate], func(row []facts.Value) bool { return row[0].String == sourceID }) {
				t.Fatal("source-only endpoint added an ordinary location row")
			}
			entryConfig := onFull.Relations[byName["ts.source_entry_tsconfig"].Predicate]
			sourceAdjust := onFull.Relations[byName["ts.source_config_adjusted"].Predicate]
			owner := onFull.Relations[byName["ts.module_owner_config"].Predicate]
			ordinaryAdjust := onFull.Relations[byName["ts.config_adjusted"].Predicate]
			if len(entryConfig) != 1 || !slices.ContainsFunc(sourceAdjust, func(row []facts.Value) bool { return row[0] == entryConfig[0][1] }) || !slices.ContainsFunc(ordinaryAdjust, func(row []facts.Value) bool { return row[0] == owner[0][1] }) {
				t.Fatalf("config joins failed: entry=%v source=%v owner=%v ordinary=%v", entryConfig, sourceAdjust, owner, ordinaryAdjust)
			}
			writeWorkspaceFile(t, root, "source-links.json", `{"version":1,"entries":[]}`)
			empty := collect(on, all)
			compare(onFull, empty, ordinary)
			compare(empty, collect(New(base), all), all)
			writeWorkspaceFile(t, root, "source-links.json", `{"version":1,"entries":[{"checkedEntry":"dist/index.d.ts","sourceEntry":"source/index.ts","sourceTsconfig":"source/tsconfig.json"}]}`)
			compare(collect(on, all), onFull, all)
			mapPath := writeWorkspaceFile(t, root, "dist/index.d.ts.map", `{}`)
			mapChanged := collect(on, all)
			compare(onFull, mapChanged, ordinary)
			compare(mapChanged, collect(New(base), all), all)
			if reflect.DeepEqual(rows(onFull.Relations[byName["ts.declaration_source_outcome"].Predicate]), rows(mapChanged.Relations[byName["ts.declaration_source_outcome"].Predicate])) {
				t.Fatal("creating a previously missing map did not update correspondence")
			}
			if err := os.Remove(mapPath); err != nil {
				t.Fatal(err)
			}
			writeWorkspaceFile(t, root, "source/tsconfig.json", `{"compilerOptions":{"module":"preserve","moduleResolution":"bundler"},"files":["index.ts"]}`)
			configChanged := collect(on, all)
			compare(onFull, configChanged, ordinary)
			compare(configChanged, collect(New(base), all), all)
			if reflect.DeepEqual(rows(onFull.Relations[byName["ts.source_config_adjusted"].Predicate]), rows(configChanged.Relations[byName["ts.source_config_adjusted"].Predicate])) {
				t.Fatal("source-only config edit did not update source adjustments")
			}
			writeWorkspaceFile(t, root, "source/tsconfig.json", `{"compilerOptions":{"module":"preserve","moduleResolution":"bundler","baseUrl":"."},"files":["index.ts"]}`)
			writeWorkspaceFile(t, root, "source/index.ts", `export function renamed(): string { return "changed"; }`)
			changed := collect(on, all)
			fresh := collect(New(base), all)
			compare(changed, fresh, all)
			compare(onFull, changed, ordinary)
			if reflect.DeepEqual(rows(onFull.Relations[byName["ts.source_binding_incomplete"].Predicate]), rows(changed.Relations[byName["ts.source_binding_incomplete"].Predicate])) {
				t.Fatal("source-only edit did not update correspondence")
			}
			writeWorkspaceFile(t, root, "source/index.ts", `export function work(): string { return "ok"; }`)
			writeWorkspaceFile(t, root, "src/app.ts", `import { work } from "../dist/index"; import { work as sourceWork } from "../source/index"; export function use() { work(); return sourceWork(); }`)
			consumed := collect(on, all)
			writeWorkspaceFile(t, root, "source/index.ts", `export function work(): number { return 1; }`)
			consumedChanged := collect(on, all)
			compare(consumedChanged, collect(New(base), all), all)
			if reflect.DeepEqual(rows(consumed.Relations[byName["ts.fn_signature"].Predicate]), rows(consumedChanged.Relations[byName["ts.fn_signature"].Predicate])) {
				t.Fatal("ordinary consumer did not reflect its changed source input")
			}
			writeWorkspaceFile(t, root, "src/app.ts", `import { work } from "../dist/index"; export function use() { return work(); }`)
			writeWorkspaceFile(t, root, "source/index.ts", `export function work(): string { return "ok"; }`)
		})
	}
	custom := New(Options{Namespace: "project", Files: FileScopeTsconfig, Sources: []string{"src/app.ts"}, Excludes: []string{"source/**"}, SourceLinks: "source-links.json"})
	description, err := custom.Describe()
	if err != nil {
		t.Fatal(err)
	}
	byName := make(map[string]facts.Predicate)
	for _, relation := range description.BaseRelations {
		byName[relation.Predicate.Name] = relation.Predicate
	}
	predicates := []facts.Predicate{byName["project.module_config"], byName["project.module_owner_config"], byName["project.source_endpoint"]}
	batch, err := inspectEmission(context.Background(), custom, backend.CollectionRequest{RepoRoot: root, WorkspaceRoot: root, Predicates: predicates})
	if err != nil {
		t.Fatal(err)
	}
	for _, predicate := range predicates {
		rows := batch.Relations[predicate]
		if len(rows) == 0 || !strings.HasPrefix(rows[0][0].String, "project:") {
			t.Fatalf("custom namespace lost %s: %v", predicate, rows)
		}
	}
}

func TestOutsideOwnerConfigPathJoinsAdjustments(t *testing.T) {
	root := t.TempDir()
	workspace := filepath.Join(root, "workspace")
	writeWorkspaceFile(t, root, "outside.json", `{"compilerOptions":{"module":"preserve","moduleResolution":"bundler","baseUrl":"."},"files":["workspace/src/app.ts"]}`)
	writeWorkspaceFile(t, workspace, "tsconfig.json", `{"files":[],"references":[{"path":"../outside.json"}]}`)
	writeWorkspaceFile(t, workspace, "src/app.ts", `export const value = 1;`)
	producer := New(Options{Files: FileScopeTsconfig})
	description, err := producer.Describe()
	if err != nil {
		t.Fatal(err)
	}
	byName := make(map[string]facts.Predicate)
	for _, relation := range description.BaseRelations {
		byName[relation.Predicate.Name] = relation.Predicate
	}
	predicates := []facts.Predicate{byName["ts.module_config"], byName["ts.module_owner_config"], byName["ts.config_adjusted"]}
	batch, err := inspectEmission(context.Background(), producer, backend.CollectionRequest{RepoRoot: root, WorkspaceRoot: workspace, Predicates: predicates})
	if err != nil {
		t.Fatal(err)
	}
	canonicalRoot, err := filepath.EvalSymlinks(root)
	if err != nil {
		t.Fatal(err)
	}
	want := lintaiapi.PublicConfigPath(filepath.Join(canonicalRoot, "workspace"), filepath.Join(canonicalRoot, "outside.json"))
	if !filepath.IsAbs(filepath.FromSlash(want)) {
		t.Fatalf("outside config is not absolute: %q", want)
	}
	owners := batch.Relations[byName["ts.module_owner_config"]]
	memberships := batch.Relations[byName["ts.module_config"]]
	adjustments := batch.Relations[byName["ts.config_adjusted"]]
	if len(owners) != 1 || owners[0][1] != facts.String(want) ||
		!slices.ContainsFunc(memberships, func(row []facts.Value) bool { return row[1] == facts.String(want) }) ||
		!slices.ContainsFunc(adjustments, func(row []facts.Value) bool { return row[0] == facts.String(want) }) {
		t.Fatalf("outside config paths do not join: owner=%v membership=%v adjustments=%v", owners, memberships, adjustments)
	}
	if _, ok := batch.Anchors[want]; ok {
		t.Fatal("config path received entity support")
	}
}
