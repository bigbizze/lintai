package engine

import (
	"fmt"
	"testing"

	"codeberg.org/TauCeti/mangle-go/ast"
	"codeberg.org/TauCeti/mangle-go/factstore"

	"github.com/bigbizze/lintai/facts"
	"github.com/bigbizze/lintai/runtime"
)

type testFact struct {
	Relation string
	Values   []facts.Value
}

func testStore(items []testFact) (factstore.ReadOnlyFactStore, error) {
	store := factstore.NewMultiIndexedArrayInMemoryStore()
	for _, item := range items {
		args := make([]ast.BaseTerm, len(item.Values))
		for i, value := range item.Values {
			switch value.Kind {
			case facts.StringKind:
				args[i] = ast.String(value.String)
			case facts.NumberKind:
				args[i] = ast.Number(value.Number)
			case facts.FloatKind:
				args[i] = ast.Float64(value.Float)
			case facts.NameKind:
				name, err := ast.Name("/" + value.String)
				if err != nil {
					return nil, err
				}
				args[i] = name
			default:
				return nil, fmt.Errorf("invalid value kind %d", value.Kind)
			}
		}
		store.Add(ast.NewAtom(item.Relation, args...))
	}
	return store, nil
}

// fn_reaches_ambient must not build the call closure. On this chain the
// closure has N*(N-1)/2 rows; the 0.6.3 prelude, which joined against
// calls_reach, failed this test with "fact size limit reached".
func TestFnReachesAmbientScalesLinearly(t *testing.T) {
	const n = 500
	fn := func(i int) string { return fmt.Sprintf("f%d", i) }
	var items []testFact
	add := func(relation string, values ...facts.Value) {
		items = append(items, testFact{Relation: relation, Values: values})
	}
	for i := range n {
		add("fn", facts.String(fn(i)), facts.String("a.ts"), facts.String(fn(i)))
		add("location", facts.String(fn(i)), facts.String("a.ts"), facts.Number(1), facts.Number(1), facts.Number(1), facts.Number(2))
		if i+1 < n {
			add("site_owner", facts.String(fn(i)+"c"), facts.String(fn(i)))
			add("call_edge", facts.String(fn(i)+"c"), facts.String(fn(i+1)))
		}
	}
	add("ambient_use", facts.String("use"), facts.Name("clock"), facts.Name("read"))
	add("site_owner", facts.String("use"), facts.String(fn(n-1)))
	items, anchors, reportable := qualifyTestFacts(t, items)
	base, err := testStore(items)
	if err != nil {
		t.Fatal(err)
	}
	catalog, schemaDecls := typeScriptCatalogForTest(t)
	output := runtime.ProgramOutput{Text: `Decl v(Fn). v(Fn) :- ts.fn(Fn, _, _), ts.owner_reaches_ambient(Fn, /clock).`}
	violations, perr := evaluateProgram(base, catalog, schemaDecls, anchors, reportable, []string{"ts"}, output, []string{"v"}, nil, evalOptions{factLimit: 10 * n})
	if perr != nil {
		t.Fatal(perr)
	}
	if len(violations) != n {
		t.Fatalf("got %d rows, want %d", len(violations), n)
	}
}

func TestTypeOpacityPolicyAcceptsLibraryLeavesAndChecksArguments(t *testing.T) {
	var items []testFact
	add := func(relation string, values ...facts.Value) {
		items = append(items, testFact{Relation: relation, Values: values})
	}
	add("fn_signature", facts.String("fn:hidden"), facts.String("sig:hidden"))
	add("fn_signature", facts.String("fn:plain"), facts.String("sig:plain"))
	add("signature_param", facts.String("sig:hidden"), facts.Number(0), facts.String("promise-hidden"))
	add("signature_param", facts.String("sig:plain"), facts.Number(0), facts.String("promise-string"))
	add("location", facts.String("fn:hidden"), facts.String("src/api.ts"), facts.Number(1), facts.Number(1), facts.Number(1), facts.Number(2))
	add("location", facts.String("fn:plain"), facts.String("src/api.ts"), facts.Number(2), facts.Number(1), facts.Number(2), facts.Number(2))
	for _, id := range []string{"promise-hidden", "array-hidden", "promise-string", "array-string"} {
		name := "Promise"
		if id == "array-hidden" || id == "array-string" {
			name = "Array"
		}
		add("type_named", facts.String(id), facts.String("lib"), facts.String(name))
		add("type_origin_kind", facts.String(id), facts.Name("lib"))
		add("type_incomplete", facts.String(id), facts.Name("members_omitted"))
	}
	add("type_arg", facts.String("promise-hidden"), facts.Number(0), facts.String("array-hidden"))
	add("type_arg", facts.String("array-hidden"), facts.Number(0), facts.String("hidden"))
	add("type_incomplete", facts.String("hidden"), facts.Name("members_omitted"))
	add("type_arg", facts.String("promise-string"), facts.Number(0), facts.String("array-string"))
	add("type_arg", facts.String("array-string"), facts.Number(0), facts.String("string"))

	items, anchors, reportable := qualifyTestFacts(t, items)
	base, err := testStore(items)
	if err != nil {
		t.Fatal(err)
	}
	catalog, schemaDecls := typeScriptCatalogForTest(t)
	output := runtime.ProgramOutput{Text: `
Decl permitted_opaque_leaf(Type).
permitted_opaque_leaf(T) :- ts.type_named(T, "lib", "Promise"), ts.type_origin_kind(T, /lib).
permitted_opaque_leaf(T) :- ts.type_named(T, "lib", "Array"), ts.type_origin_kind(T, /lib).
Decl disallowed_incomplete(Type).
disallowed_incomplete(T) :- ts.type_incomplete(T, Reason), Reason != /members_omitted.
disallowed_incomplete(T) :- ts.type_incomplete(T, /members_omitted), !permitted_opaque_leaf(T).
Decl reaches_disallowed_incomplete(Type).
reaches_disallowed_incomplete(T) :- disallowed_incomplete(T).
reaches_disallowed_incomplete(T) :- ts.type_child(T, Child), reaches_disallowed_incomplete(Child).
Decl violation(Fn).
violation(Fn) :- ts.fn_signature(Fn, Sig), ts.signature_param(Sig, _, T), reaches_disallowed_incomplete(T).
`}
	violations, perr := evaluateProgram(base, catalog, schemaDecls, anchors, reportable, []string{"ts"}, output, []string{"violation"}, nil, evalOptions{})
	if perr != nil {
		t.Fatal(perr)
	}
	if len(violations) != 1 || violations[0].anchor != "ts:fn:hidden" {
		t.Fatalf("opacity policy violations: %v", violations)
	}
}
