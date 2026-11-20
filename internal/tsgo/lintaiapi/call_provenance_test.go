package lintaiapi

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/bigbizze/lintai/internal/tsgo/internal/ast"
	"github.com/bigbizze/lintai/internal/tsgo/internal/bundled"
	"github.com/bigbizze/lintai/internal/tsgo/internal/checker"
	"github.com/bigbizze/lintai/internal/tsgo/internal/compiler"
	"github.com/bigbizze/lintai/internal/tsgo/internal/tspath"
	"github.com/bigbizze/lintai/internal/tsgo/internal/vfs/osvfs"
)

func TestCallDeclarationObservationsBeforeMerge(t *testing.T) {
	const source = `interface Reader { read(): void }
interface Writer { write(): void }
interface Other { write(): void }
interface Derived extends Writer {}
interface Reused { write: Writer["write"] }
class ParameterWriter { constructor(public write: Writer["write"]) {} }
declare class GetterWriter { get write(): Writer["write"]; }
class NestedGetter {
  get write() {
    class NestedWriter { write(): void {} }
    return new NestedWriter().write;
  }
}

interface StringCall { invoke: (value: string) => void }
interface NumberCall { invoke: (value: number) => void }
interface Capabilities { store: Reader & Writer }
type Shared = () => void;
interface Inline {
  inline: (((value: number) => void));
  object: { (value: number): void };
  shared: Shared;
  sharedOther: Shared;
}

class Getter { get returned(): () => void { return () => {}; } }
class Overloaded {
  constructor() {}
  choose(value: string): string;
  choose(value: number): number;
  choose(value: string | number): string | number { return value; }
}
function tag(parts: TemplateStringsArray): string { return parts[0]; }
function exercise(cap: Capabilities, other: Other, inline: Inline, getter: Getter,
  inherited: Derived, combined: ((x: "both" | "left") => void) | ((x: "both" | "right") => void),
  unknown: any, free: Shared, overloaded: Overloaded, k: "shared" | "sharedOther", reused: Reused,
  parameter: ParameterWriter, getterWriter: GetterWriter, intersected: StringCall & NumberCall,
  nestedGetter: NestedGetter) {
  cap.store.read();
  cap.store.write();
  const store = cap.store;
  store.write();
  other.write();
  inline.inline(1);
  inline.object(1);
  inline.shared();
  inline["shared"]();
  inline[k]();
  (inline.shared satisfies Shared)();
  (inline.shared as Shared)();
  reused.write();
  const object = { write: free, free };
  object.write();
  object.free();
  parameter.write();
  getterWriter.write();
  nestedGetter.write();
  intersected.invoke("text");
  intersected["invoke"](1);
  getter.returned();
  inherited.write();
  cap.store?.write();
  cap.store["write"]();
  combined("both");
  unknown();
  free();
  overloaded.choose("text");
  overloaded.choose(1);
  new Overloaded();
  tag` + "`hello`" + `;
}
`
	root := t.TempDir()
	files := writeWorkspace(t, root, map[string]string{"src/calls.ts": source})
	host := compiler.NewCompilerHost(bundled.WrapFS(osvfs.FS()), bundled.LibPath(), nil, nil, nil)
	// Inspect raw per-program observations before merge and retention.
	timing := &extractTiming{}
	configs := newConfigCache(host)
	plan, err := planOrdinary(context.Background(), host, root, files, nil, false, false, configs, timing)
	if err != nil {
		t.Fatal(err)
	}
	owner := plan.programs[0]
	operations := operationsForRelations(nil)
	operations.plan, operations.owner = plan, owner
	operations.moduleFiles = make(map[string]bool)
	for file := range plan.owners {
		operations.moduleFiles[file] = true
	}
	partial, missing, err := buildSnapshotForGroup(context.Background(), host, root, files,
		owner.group, owner.prepared, nil, nil, configs, nil, nil, timing, operations)
	if err != nil {
		t.Fatal(err)
	}
	if len(missing) != 0 {
		t.Fatalf("missing primary files: %v", missing)
	}
	declarations := make(map[string]DeclarationFact)
	for _, declaration := range partial.Declarations {
		declarations[declaration.ID] = declaration
	}
	unresolved := make(map[string]CallUnresolved)
	for _, call := range partial.CallsUnresolved {
		unresolved[call.SemanticKey] = call
	}
	observations := make(map[string]callDeclarationObservation)
	for _, observation := range partial.callDeclarationObservations {
		if _, duplicate := observations[observation.Site]; duplicate {
			t.Fatalf("duplicate invocation observation: %+v", observation)
		}
		observations[observation.Site] = observation
	}
	runtimeLocations := make(map[string]SourceLocation)
	for _, call := range partial.CallsUnresolved {
		runtimeLocations[call.SemanticKey] = call.Range
	}
	for _, call := range partial.CallEdges {
		runtimeLocations[call.SemanticKey] = call.Range
	}
	for _, call := range partial.ExternalCalls {
		runtimeLocations[call.SemanticKey] = call.Range
	}
	var writes []string
	var shared []string
	var overloads []callDeclarationObservation
	for _, test := range []struct {
		call, owner, member, reason string
		kind                        ast.Kind
		selectedSource              string
		runtimeFlexible             bool
	}{
		{call: "cap.store.read()", owner: "Reader", member: "read"},
		{call: "cap.store.write()", owner: "Writer", member: "write"},
		{call: "store.write()", owner: "Writer", member: "write"},
		{call: "other.write()", owner: "Other", member: "write"},
		{call: "inline.inline(1)", owner: "Inline", member: "inline"},
		{call: "inline.object(1)", owner: "Inline", member: "object"},
		{call: "inline.shared()", reason: "unsupported", selectedSource: "type Shared = () => void;"},
		{call: `inline["shared"]()`, reason: "unsupported", selectedSource: "type Shared = () => void;"},
		{call: "inline[k]()", reason: "unsupported", selectedSource: "type Shared = () => void;"},
		{call: "(inline.shared satisfies Shared)()", reason: "unsupported", selectedSource: "type Shared = () => void;"},
		{call: "(inline.shared as Shared)()", reason: "unsupported", selectedSource: "type Shared = () => void;"},
		{call: "reused.write()", owner: "Writer", member: "write", reason: "unsupported"},
		{call: "parameter.write()", owner: "Writer", member: "write", reason: "unsupported"},
		{call: "getterWriter.write()", owner: "Writer", member: "write", reason: "unsupported"},
		{call: "nestedGetter.write()", owner: "NestedWriter", member: "write", reason: "unsupported"},
		{call: `intersected.invoke("text")`, owner: "StringCall", member: "invoke"},
		{call: `intersected["invoke"](1)`, owner: "NumberCall", member: "invoke"},
		{call: "object.write()", reason: "unsupported", selectedSource: "type Shared = () => void;"},
		{call: "object.free()", reason: "unsupported", selectedSource: "type Shared = () => void;"},
		{call: "getter.returned()"},
		{call: "inherited.write()", owner: "Writer", member: "write"},
		{call: "cap.store?.write()", owner: "Writer", member: "write"},
		{call: `cap.store["write"]()`, owner: "Writer", member: "write"},
		{call: `combined("both")`, reason: "synthetic"},
		{call: "unknown()", reason: "unresolved"},
		{call: "free()"},
		{call: `overloaded.choose("text")`, owner: "Overloaded", member: "choose", selectedSource: "choose(value: string): string;", runtimeFlexible: true},
		{call: "overloaded.choose(1)", owner: "Overloaded", member: "choose", selectedSource: "choose(value: number): number;", runtimeFlexible: true},
		{call: "new Overloaded()", kind: ast.KindNewExpression, selectedSource: "constructor() {}", runtimeFlexible: true},
		{call: "tag`hello`", kind: ast.KindTaggedTemplateExpression, selectedSource: "function tag(parts: TemplateStringsArray): string", runtimeFlexible: true},
	} {
		t.Run(test.call, func(t *testing.T) {
			offset := strings.Index(source, "  "+test.call) + 2
			if offset < 2 {
				t.Fatal("fixture call not found")
			}
			line := strings.Count(source[:offset], "\n") + 1
			column := offset - strings.LastIndex(source[:offset], "\n")
			var observation callDeclarationObservation
			for site, location := range runtimeLocations {
				if location.StartLine == line && location.StartColumn == column {
					observation = observations[site]
					break
				}
			}
			if observation.Site == "" {
				t.Fatalf("missing observation for %s", test.call)
			}
			location, ok := runtimeLocations[observation.Site]
			if !ok || location.StartLine != strings.Count(source[:offset], "\n")+1 {
				t.Fatalf("lost original runtime site/location: %+v", observation)
			}
			if _, unresolvedSite := unresolved[observation.Site]; !test.runtimeFlexible && !unresolvedSite {
				t.Fatalf("capability call lost runtime-unresolved status: %+v", observation)
			}
			if strings.Join(observation.Incomplete, ",") != test.reason {
				t.Fatalf("incomplete outcome: %+v", observation)
			}
			if test.reason == "synthetic" || test.reason == "unresolved" {
				if observation.Declaration != "" || observation.Member != nil {
					t.Fatalf("invented concrete evidence: %+v", observation)
				}
				return
			}
			declaration, exists := declarations[observation.Declaration]
			if !exists || test.call == "getter.returned()" && declaration.Kind != "signature" {
				t.Fatalf("missing or misattributed signature endpoint: %+v", observation)
			}
			if test.selectedSource != "" {
				selectedOffset := strings.Index(source, test.selectedSource)
				if selectedOffset < 0 || declaration.Location.File != "src/calls.ts" || declaration.Location.StartLine != strings.Count(source[:selectedOffset], "\n")+1 {
					t.Fatalf("selected wrong source declaration for %s: %+v", test.call, declaration)
				}
			}
			if test.selectedSource == "type Shared = () => void;" {
				shared = append(shared, observation.Declaration)
			}
			if test.owner == "Overloaded" {
				if declaration.Function != "" {
					t.Fatalf("overload incorrectly linked to implementation function: %+v", declaration)
				}
				overloads = append(overloads, observation)
			}
			if test.member == "" {
				if observation.Member != nil {
					t.Fatalf("manufactured member relationship: %+v", observation)
				}
				return
			}
			member := observation.Member
			if member == nil || member.Declaration != observation.Declaration || member.Name != test.member || declarations[member.OwnerDeclaration].Name != test.owner {
				t.Fatalf("wrong lexical member identity: %+v", observation)
			}
			if test.owner == "Writer" {
				writes = append(writes, observation.Declaration)
			}
		})
	}
	if len(overloads) != 2 || overloads[0].Declaration == overloads[1].Declaration || overloads[0].Member == nil || overloads[1].Member == nil || overloads[0].Member.OwnerDeclaration != overloads[1].Member.OwnerDeclaration {
		t.Fatalf("overloads must select distinct declarations with the same lexical class owner: %+v", overloads)
	}
	if owner := declarations[overloads[0].Member.OwnerDeclaration]; owner.Kind != "class" {
		t.Fatalf("overload owner is not the class declaration: %+v", owner)
	}
	implementationLine := strings.Count(source[:strings.Index(source, "choose(value: string | number)")], "\n") + 1
	if !slices.ContainsFunc(partial.Functions, func(function Function) bool {
		return function.Name == "Overloaded.choose" && function.ContainerName == "Overloaded" && function.Range.StartLine == implementationLine
	}) {
		t.Fatal("method implementation was not independently collected")
	}
	if len(shared) != 7 {
		t.Fatalf("missing shared alias observations: %v", shared)
	}
	for _, declaration := range shared {
		if declaration != shared[0] {
			t.Fatal("dot, static-element, dynamic-element, and wrapped alias calls selected different declarations")
		}
	}
	for _, write := range writes {
		if write != writes[0] {
			t.Fatal("direct, aliased, inherited, optional, and static-element writes selected different declarations")
		}
	}
}

func TestCallProvenanceFollowsPlannedOwner(t *testing.T) {
	root := t.TempDir()
	const source = `import { task } from "contract";
import { stable } from "./stable";
task();
stable();
declare function make(): () => void;
declare function fluent(): { next(): void };
make()();
fluent().next();
const values: number[] = [];
values.map(task);
values.map((value) => value);
`
	config := `{"compilerOptions":{"strict":true,"module":"preserve","moduleResolution":"bundler","baseUrl":".","paths":{"contract":["./contract.ts"]}},"files":["../shared/calls.ts","../shared/stable.ts"]}`
	writeWorkspace(t, root, map[string]string{
		"tsconfig.json":      `{"files":[],"references":[{"path":"./red"},{"path":"./blue"}]}`,
		"red/tsconfig.json":  config,
		"blue/tsconfig.json": config,
		"red/contract.ts":    `export function task(): number { return 1; }`,
		"blue/contract.ts":   `export declare const task: any;`,
		"shared/calls.ts":    source,
		"shared/stable.ts":   `export function stable(): number { return 1; }`,
	})
	req := BuildSnapshotRequest{WorkspaceRoot: root, Files: []string{filepath.Join(root, "shared/calls.ts"), filepath.Join(root, "shared/stable.ts")}, TsconfigFiles: true}
	first, err := BuildSnapshot(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "blue/contract.ts"), []byte(`export function task(): string { return "changed"; }`), 0o644); err != nil {
		t.Fatal(err)
	}
	second, err := BuildSnapshot(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(first.Functions, second.Functions) || !reflect.DeepEqual(first.CallEdges, second.CallEdges) ||
		!reflect.DeepEqual(first.CallsUnresolved, second.CallsUnresolved) || !reflect.DeepEqual(first.CallDeclarations, second.CallDeclarations) ||
		!reflect.DeepEqual(first.ImportsOpaque, second.ImportsOpaque) || !reflect.DeepEqual(first.ImportEdges, second.ImportEdges) ||
		!reflect.DeepEqual(first.Exports, second.Exports) || !reflect.DeepEqual(first.Sites, second.Sites) {
		t.Fatal("an alternate non-owner view changed direct observations")
	}
	line := func(text string) int { return strings.Count(source[:strings.Index(source, text)], "\n") + 1 }
	var taskSite string
	for _, call := range first.CallsUnresolved {
		if call.Range.StartLine == line("task();") {
			taskSite = call.SemanticKey
		}
	}
	if taskSite == "" {
		t.Fatal("excluded implementation was not unresolved at runtime")
	}
	var taskDeclaration string
	for _, item := range first.CallDeclarations {
		if item.Site == taskSite {
			taskDeclaration = item.Declaration
		}
	}
	if taskDeclaration == "" {
		t.Fatal("excluded implementation lost independent declaration evidence")
	}
	if declaration := semanticDeclaration(t, first, taskDeclaration); declaration.Location.File != "red/contract.ts" || declaration.Function != "" {
		t.Fatalf("unexpected owner-selected declaration: %+v", declaration)
	}
	if !slices.ContainsFunc(first.CallEdges, func(call CallEdge) bool { return call.ToName == "stable" }) {
		t.Fatal("call to a retained implementation was lost")
	}
	for _, text := range []string{"make()();", "fluent().next();"} {
		ids := map[string]bool{}
		for _, site := range first.Sites {
			if site.Range.StartLine == line(text) && slices.ContainsFunc(first.CallDeclarations, func(item CallDeclarationFact) bool { return item.Site == site.ID }) {
				ids[site.ID] = true
			}
		}
		if len(ids) != 2 {
			t.Fatalf("nested invocations lost their separate declaration sites: %s: %v", text, ids)
		}
	}
	if len(first.FunctionEscapes) < 2 {
		t.Fatalf("missing callback evidence: %+v", first.FunctionEscapes)
	}
	for _, escape := range first.FunctionEscapes {
		if slices.ContainsFunc(first.CallDeclarations, func(item CallDeclarationFact) bool { return item.Site == escape.Site }) {
			t.Fatalf("callback argument became an invocation: %+v", escape)
		}
	}
	seen := map[CallDeclarationFact]bool{}
	for _, fact := range first.CallDeclarations {
		if seen[fact] {
			t.Fatalf("duplicate declaration fact: %+v", fact)
		}
		seen[fact] = true
		semanticDeclaration(t, first, fact.Declaration)
	}
}

func TestObservationOriginValidation(t *testing.T) {
	root := t.TempDir()
	files := writeWorkspace(t, root, map[string]string{
		"a/tsconfig.json": `{"files":["a.ts"]}`,
		"a/a.ts":          `export function a(): string { return ""; }`,
		"b/tsconfig.json": `{"files":["b.ts"]}`,
		"b/b.ts":          `export function b(): number { return 1; }`,
		"c/tsconfig.json": `{"files":["included.ts"]}`,
		"c/included.ts":   `export const included = 1;`,
		"c/fallback.ts":   `export const fallback = 1;`,
		"free.ts":         `export const free = 1;`,
	})
	host := compiler.NewCompilerHost(bundled.WrapFS(osvfs.FS()), bundled.LibPath(), nil, nil, nil)
	plan, err := planOrdinary(context.Background(), host, root, files, nil, false, false, newConfigCache(host), &extractTiming{})
	if err != nil {
		t.Fatal(err)
	}
	path := func(relative string) string { return filepath.ToSlash(filepath.Join(root, relative)) }
	a, b := plan.owners[path("a/a.ts")], plan.owners[path("b/b.ts")]
	initial, fallback := plan.owners[path("free.ts")], plan.owners[path("c/fallback.ts")]
	if a == nil || b == nil || initial == nil || fallback == nil || initial == fallback || initial.group.ConfigPath != "" || fallback.group.ConfigPath != "" {
		t.Fatalf("fixture did not create distinct configured and synthetic programs: %+v", plan.files)
	}
	file := a.prepared.program.GetSourceFile(tspath.RootedFilePathFromAbsolute(path("a/a.ts")))
	other := b.prepared.program.GetSourceFile(tspath.RootedFilePathFromAbsolute(path("b/b.ts")))
	typeChecker, done := a.prepared.program.GetTypeChecker(context.Background())
	defer done()
	signature := typeChecker.GetSignatureFromDeclaration(file.Statements.Nodes[0])
	if signature == nil {
		t.Fatal("fixture has no signature")
	}
	typ := typeChecker.GetReturnTypeOfSignature(signature)
	ex := &extractor{
		workspaceRoot: root, program: a.prepared.program, checker: typeChecker, plan: plan, owner: a,
		operations: semanticOperations{typeStructure: true},
		typeFacts:  make(map[string]*TypeFact), typeIDs: make(map[*checker.Type]string),
		typeKeys: make(map[typeKeyAt]typeKeyResult), typeFactSeen: make(map[*checker.Type]struct{}),
		typeShapes: make(map[*checker.Type]*typeShape), typeTruncated: make(map[*checker.Type]struct{}),
	}
	valid := observationOrigin{rootFile: path("a/a.ts"), context: a}
	wrong := observationOrigin{rootFile: valid.rootFile, context: b}
	if got := ex.emitFunctionType(typ, wrong); got != "" || ex.originError == nil || len(ex.typeFacts) != 0 {
		t.Fatalf("wrong real context mutated type state: id=%q error=%v", got, ex.originError)
	}
	ex.originError = nil
	if got := ex.emitFunctionType(typ, valid); got == "" || ex.originError != nil {
		t.Fatalf("valid type contribution failed: id=%q error=%v", got, ex.originError)
	}
	before := len(ex.typeFacts)
	if got := ex.emitFunctionType(typ, wrong); got != "" || ex.originError == nil || len(ex.typeFacts) != before {
		t.Fatalf("wrong context bypassed a populated type cache: id=%q error=%v", got, ex.originError)
	}
	ex.originError = nil
	if ex.validateOrigin(valid, "function", other.AsNode()) || ex.originError == nil {
		t.Fatal("wrong direct-root node was accepted")
	}
	ex.originError = nil
	ex.collectAccess(other.AsNode(), nil, valid)
	if ex.originError == nil || len(ex.accesses) != 0 {
		t.Fatal("direct source-site collector accepted a foreign node")
	}
	synthetic := &extractor{plan: plan, owner: initial, program: initial.prepared.program}
	if synthetic.validateOrigin(observationOrigin{rootFile: path("free.ts"), context: fallback}, "type", nil) || synthetic.originError == nil {
		t.Fatal("distinct synthetic contexts were conflated")
	}
}
