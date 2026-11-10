package lintaiapi

import (
	"context"
	"encoding/json"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/bigbizze/lintai/internal/tsgo/internal/ast"
	"github.com/bigbizze/lintai/internal/tsgo/internal/bundled"
	"github.com/bigbizze/lintai/internal/tsgo/internal/compiler"
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
	partial, missing, err := buildSnapshotForGroup(context.Background(), host, root, files,
		configGroup{PrimaryFiles: files}, nil, nil, nil, nil, nil, &extractTiming{})
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
		if _, duplicate := observations[observation.InvocationKey]; duplicate {
			t.Fatalf("duplicate invocation observation: %+v", observation)
		}
		observations[observation.InvocationKey] = observation
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
			kind := test.kind
			if kind == ast.KindUnknown {
				kind = ast.KindCallExpression
			}
			key, _ := json.Marshal([]any{"src/calls.ts", offset, offset + len(test.call), kind})
			observation, ok := observations[string(key)]
			if !ok {
				t.Fatalf("missing observation for %s", key)
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
				if declaration.Function != "" || len(declaration.FunctionObservations) != 0 {
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

func TestCallProvenanceProgramReconciliationAndRetention(t *testing.T) {
	root := t.TempDir()
	const source = `import { capability, uncertainMember, uncertain, task } from "contract";
import { stable } from "./stable";
capability.write();
uncertainMember.write();
uncertain();
task();
stable();
Math.abs(-1);
declare function make(): () => void;
declare function fluent(): { next(): void };
function partition() {
make()();
fluent().next();
}
const values: number[] = [];
values.map(task);
function escaped(first: (n: number) => number, second: (n: number) => number) {
  values.map(first);
  values.map(second);
}
`
	config := `{"compilerOptions":{"strict":true,"target":"es2022","module":"preserve","moduleResolution":"bundler","baseUrl":".","paths":{"contract":["./contract.ts"]}},"files":["../shared/calls.ts"]}`
	files := writeWorkspace(t, root, map[string]string{
		"red/tsconfig.json":  config,
		"blue/tsconfig.json": config,
		"red/contract.ts": `interface Writer { write(): void }
export declare const capability: Writer;
export declare const uncertainMember: Writer;
export function uncertain(): number { return 1; }
export function task(): number { return 1; }`,
		"blue/contract.ts": `interface Writer { write(): void }
export declare const capability: Writer;
export declare const uncertainMember: any;
export declare const uncertain: any;
export function task(): number { return 2; }`,
		"shared/calls.ts":  source,
		"shared/stable.ts": `export function stable(): number { return 1; }`,
		"unrelated.ts":     `export const unrelated = 1;`,
	})
	host := compiler.NewCompilerHost(bundled.WrapFS(osvfs.FS()), bundled.LibPath(), nil, nil, nil)
	configs := newConfigCache(host)
	var partials []*Snapshot
	// BuildSnapshot normally assigns each primary file to its owning group.
	// Deliberately analyze the SAME primary file under two real path mappings
	// here to reach differing observations without widening runtime traversal.
	for _, group := range []string{"red", "blue"} {
		configPath := filepath.Join(root, group, "tsconfig.json")
		parsed, err := configs.program(configPath)
		if err != nil {
			t.Fatal(err)
		}
		partial, missing, err := buildSnapshotForGroup(context.Background(), host, root, files,
			configGroup{ConfigPath: configPath, Config: parsed, PrimaryFiles: []string{
				filepath.Join(root, "shared/calls.ts"), filepath.Join(root, "shared/stable.ts"),
			}}, nil, nil, configs, nil, nil, &extractTiming{})
		if err != nil || len(missing) != 0 {
			t.Fatalf("build %s: missing=%v err=%v", group, missing, err)
		}
		partials = append(partials, partial)
	}
	// This checked program never analyzes the invocations. It must not count
	// as an empty/incomplete observation of them.
	unrelated := filepath.Join(root, "unrelated.ts")
	absent, _, err := buildSnapshotForGroup(context.Background(), host, root, files,
		configGroup{PrimaryFiles: []string{unrelated}}, nil, nil, configs, nil, nil, &extractTiming{})
	if err != nil {
		t.Fatal(err)
	}
	observationAt := func(partial *Snapshot, call string) callDeclarationObservation {
		t.Helper()
		offset := strings.Index(source, "\n"+call) + 1
		if offset == 0 {
			t.Fatalf("fixture call not found: %s", call)
		}
		key, _ := json.Marshal([]any{"shared/calls.ts", offset, offset + len(call), ast.KindCallExpression})
		for _, observation := range partial.callDeclarationObservations {
			if observation.InvocationKey == string(key) {
				return observation
			}
		}
		t.Fatalf("missing actual checker observation for %s", call)
		return callDeclarationObservation{}
	}
	merged := &Snapshot{}
	for _, partial := range []*Snapshot{partials[0], partials[1], absent, partials[0]} {
		mergeSnapshot(merged, partial)
	}
	finalizeSemanticObservations(merged)
	keepModuleFiles(merged)
	finalizeCallDeclarationObservations(merged)
	keepRetainedSemantics(merged)
	keepReachableTypes(merged)
	sortSnapshot(merged)

	for _, test := range []struct {
		call                string
		conflict, different bool
		incomplete          bool
	}{
		{call: "capability.write()", conflict: true},
		{call: "uncertainMember.write()", conflict: true, incomplete: true},
		{call: "uncertain()", conflict: true, different: true, incomplete: true},
		{call: "task()", conflict: true, different: true},
		{call: "stable()"},
		{call: "Math.abs(-1)"},
	} {
		t.Run(test.call, func(t *testing.T) {
			red := observationAt(partials[0], test.call)
			blue := observationAt(partials[1], test.call)
			if (red.Site != blue.Site) != test.different {
				t.Fatalf("fixture did not reach expected runtime ID partition: red=%+v blue=%+v", red, blue)
			}
			if test.incomplete {
				if red.Declaration == "" || blue.Declaration != "" || !slices.Equal(blue.Incomplete, []string{"unresolved"}) {
					t.Fatalf("fixture did not reach complete/incomplete partition: red=%+v blue=%+v", red, blue)
				}
			} else if red.Declaration == "" || blue.Declaration == "" || len(red.Incomplete)+len(blue.Incomplete) != 0 {
				t.Fatalf("fixture lacks complete checked evidence: red=%+v blue=%+v", red, blue)
			}
			if test.call == "capability.write()" && (red.Member == nil || blue.Member == nil || *red.Member == *blue.Member) {
				t.Fatal("fixture did not select differing lexical member tuples")
			}
			for _, observation := range []callDeclarationObservation{red, blue} {
				if test.call == "Math.abs(-1)" {
					if !slices.ContainsFunc(merged.ExternalCalls, func(call ExternalCall) bool {
						return call.SemanticKey == observation.Site && call.FromModule
					}) {
						t.Fatal("lost module-scope external runtime classification")
					}
				} else if test.call != "stable()" {
					if !slices.ContainsFunc(merged.CallsUnresolved, func(call CallUnresolved) bool {
						return call.SemanticKey == observation.Site && call.FromModule
					}) {
						t.Fatal("lost unresolved site when filtering the runtime target")
					}
				}
				if got := slices.Contains(merged.CallDeclarationIncomplete, CallDeclarationIncompleteFact{Site: observation.Site, Reason: "conflict"}); got != test.conflict {
					t.Fatalf("conflict=%v for %+v", got, observation)
				}
				if observation.Declaration != "" {
					fact := CallDeclarationFact{Site: observation.Site, Declaration: observation.Declaration}
					if !slices.Contains(merged.CallDeclarations, fact) {
						t.Fatalf("lost original declaration/site pair: %+v", fact)
					}
					declaration := semanticDeclaration(t, merged, observation.Declaration)
					if test.call == "Math.abs(-1)" && declaration.OriginKind != OriginKindLib {
						t.Fatalf("lost checked library declaration: %+v", declaration)
					}
					if test.call == "task()" && (len(declaration.Types) == 0 || declaration.Function != "") {
						t.Fatalf("lost independently collected types or retained a filtered function: %+v", declaration)
					}
					if test.call == "stable()" && declaration.Function == "" {
						t.Fatal("lost surviving direct function association")
					}
					if observation.Member != nil {
						if !slices.Contains(merged.DeclarationMembers, *observation.Member) {
							t.Fatalf("lost lexical member: %+v", observation.Member)
						}
						owner := semanticDeclaration(t, merged, observation.Member.OwnerDeclaration)
						if test.call == "capability.write()" && owner.Location.File != "red/contract.ts" && owner.Location.File != "blue/contract.ts" {
							t.Fatalf("wrong lexical owner: %+v", owner)
						}
						if len(owner.Types) != 0 {
							t.Fatalf("metadata-only owner became a type root: %+v", owner)
						}
					}
				}
				for _, reason := range observation.Incomplete {
					if !slices.Contains(merged.CallDeclarationIncomplete, CallDeclarationIncompleteFact{Site: observation.Site, Reason: reason}) {
						t.Fatalf("lost original incomplete reason: %+v", observation)
					}
				}
				other := blue
				if observation.Site == blue.Site {
					other = red
				}
				if test.different && other.Declaration != "" && slices.Contains(merged.CallDeclarations, CallDeclarationFact{Site: observation.Site, Declaration: other.Declaration}) {
					t.Fatal("invented cross-program declaration/site pair")
				}
			}
		})
	}
	t.Run("nested and fluent invocation partition", func(t *testing.T) {
		keys := make(map[string]bool)
		for _, call := range []string{"make()", "make()()", "fluent()", "fluent().next()"} {
			red := observationAt(partials[0], call)
			blue := observationAt(partials[1], call)
			if keys[red.InvocationKey] || red.InvocationKey != blue.InvocationKey {
				t.Fatalf("invocations collided or lost cross-program correlation: red=%+v blue=%+v", red, blue)
			}
			keys[red.InvocationKey] = true
			for _, observation := range []callDeclarationObservation{red, blue} {
				if observation.Declaration == "" || !slices.Contains(merged.CallDeclarations, CallDeclarationFact{Site: observation.Site, Declaration: observation.Declaration}) {
					t.Fatalf("lost invocation's own declaration/site pair: %+v", observation)
				}
				if slices.Contains(merged.CallDeclarationIncomplete, CallDeclarationIncompleteFact{Site: observation.Site, Reason: "conflict"}) {
					t.Fatalf("false nested/fluent invocation conflict: %+v", observation)
				}
			}
		}
	})
	// Both ordinary escaped parameters and the callback whose collected target
	// was filtered out remain unsupported, without conflicts between values.
	escapes := 0
	for _, call := range merged.CallsUnresolved {
		if strings.Contains(call.SemanticKey, "::escaping_callback::") || strings.Contains(call.FromSemanticKey, "escaped") {
			escapes++
			if !slices.Contains(merged.CallDeclarationIncomplete, CallDeclarationIncompleteFact{Site: call.SemanticKey, Reason: "unsupported"}) ||
				slices.Contains(merged.CallDeclarationIncomplete, CallDeclarationIncompleteFact{Site: call.SemanticKey, Reason: "conflict"}) {
				t.Fatalf("escaped value lost unsupported outcome or gained conflict: %+v", call)
			}
		}
	}
	if escapes != 3 {
		t.Fatalf("want two parameter escapes and one materialized filtered escape, got %d", escapes)
	}
	for i, fact := range merged.CallDeclarations {
		semanticDeclaration(t, merged, fact.Declaration)
		if i > 0 && fact == merged.CallDeclarations[i-1] {
			t.Fatalf("duplicate public declaration fact: %+v", fact)
		}
	}
	for _, member := range merged.DeclarationMembers {
		semanticDeclaration(t, merged, member.Declaration)
		semanticDeclaration(t, merged, member.OwnerDeclaration)
	}
}
