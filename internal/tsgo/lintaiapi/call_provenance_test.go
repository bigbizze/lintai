package lintaiapi

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/bigbizze/lintai/internal/tsgo/internal/bundled"
	"github.com/bigbizze/lintai/internal/tsgo/internal/compiler"
	"github.com/bigbizze/lintai/internal/tsgo/internal/vfs/osvfs"
)

func TestCallDeclarationObservationsBeforeMerge(t *testing.T) {
	const source = `interface Reader { read(): void }
interface Writer { write(): void }
interface Other { write(): void }
interface Derived extends Writer {}
interface Capabilities { store: Reader & Writer }
type Shared = () => void;
interface Inline {
  inline: (((value: number) => void));
  object: { (value: number): void };
  shared: Shared;
}
class Getter { get returned(): () => void { return () => {}; } }
function exercise(cap: Capabilities, other: Other, inline: Inline, getter: Getter,
  inherited: Derived, combined: ((x: "both" | "left") => void) | ((x: "both" | "right") => void),
  unknown: any, free: Shared) {
  cap.store.read();
  cap.store.write();
  const store = cap.store;
  store.write();
  other.write();
  inline.inline(1);
  inline.object(1);
  inline.shared();
  getter.returned();
  inherited.write();
  cap.store?.write();
  cap.store["write"]();
  combined("both");
  unknown();
  free();
}
`
	root := t.TempDir()
	files := writeWorkspace(t, root, map[string]string{"src/calls.ts": source})
	host := compiler.NewCompilerHost(bundled.WrapFS(osvfs.FS()), bundled.LibPath(), nil, nil, nil)
	// Exercise the partial snapshot: public flattening, reconciliation, and
	// retention deliberately belong to the subsequent integration task.
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
	var writes []string
	for _, test := range []struct {
		call, owner, member, reason string
	}{
		{call: "cap.store.read()", owner: "Reader", member: "read"},
		{call: "cap.store.write()", owner: "Writer", member: "write"},
		{call: "store.write()", owner: "Writer", member: "write"},
		{call: "other.write()", owner: "Other", member: "write"},
		{call: "inline.inline(1)", owner: "Inline", member: "inline"},
		{call: "inline.object(1)", owner: "Inline", member: "object"},
		{call: "inline.shared()", reason: "unsupported"},
		{call: "getter.returned()"},
		{call: "inherited.write()", owner: "Writer", member: "write"},
		{call: "cap.store?.write()", owner: "Writer", member: "write"},
		{call: `cap.store["write"]()`, owner: "Writer", member: "write"},
		{call: `combined("both")`, reason: "synthetic"},
		{call: "unknown()", reason: "unresolved"},
		{call: "free()"},
	} {
		t.Run(test.call, func(t *testing.T) {
			offset := strings.Index(source, "  "+test.call) + 2
			if offset < 2 {
				t.Fatal("fixture call not found")
			}
			key, _ := json.Marshal([]any{"invocation-v1", "src/calls.ts", offset, "call"})
			observation, ok := observations[string(key)]
			if !ok {
				t.Fatalf("missing observation for %s", key)
			}
			call, ok := unresolved[observation.Site]
			if !ok || call.Range.StartLine != strings.Count(source[:offset], "\n")+1 {
				t.Fatalf("lost original runtime-unresolved site/location: %+v", observation)
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
	for _, write := range writes {
		if write != writes[0] {
			t.Fatal("direct, aliased, inherited, optional, and static-element writes selected different declarations")
		}
	}
}
