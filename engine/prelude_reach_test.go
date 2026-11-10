package engine

import (
	"fmt"
	"testing"

	"github.com/bigbizze/lintai/backend/typescript"
	"github.com/bigbizze/lintai/facts"
	"github.com/bigbizze/lintai/runtime"
)

// fn_reaches_ambient must not build the call closure. On this chain the
// closure has N*(N-1)/2 rows; the 0.6.3 prelude, which joined against
// calls_reach, failed this test with "fact size limit reached".
func TestFnReachesAmbientScalesLinearly(t *testing.T) {
	const n = 500
	fn := func(i int) string { return fmt.Sprintf("f%d", i) }
	var items []facts.Fact
	add := func(relation string, values ...facts.Value) {
		items = append(items, facts.Fact{Relation: relation, Values: values})
	}
	for i := range n {
		add("fn", facts.String(fn(i)), facts.String("a.ts"), facts.String(fn(i)))
		add("location", facts.String(fn(i)), facts.String("a.ts"), facts.Number(1), facts.Number(1), facts.Number(1), facts.Number(2))
		if i+1 < n {
			add("call_edge", facts.String(fn(i)+"c"), facts.String(fn(i)), facts.String(fn(i+1)))
		}
	}
	add("ambient_use", facts.String("use"), facts.Name("clock"), facts.Name("read"), facts.String("a.ts"))
	add("site_in_fn", facts.String("use"), facts.String(fn(n-1)))
	base, err := facts.Store(items)
	if err != nil {
		t.Fatal(err)
	}
	schemaDecls, prelude, err := facts.ParseSchema(typescript.Schema())
	if err != nil {
		t.Fatal(err)
	}
	output := runtime.ProgramOutput{Text: `Decl v(Fn). v(Fn) :- fn(Fn, _, _), fn_reaches_ambient(Fn, /clock).`}
	violations, perr := evaluateProgram(base, prelude, schemaDecls, output, []string{"v"}, nil, evalOptions{factLimit: 10 * n})
	if perr != nil {
		t.Fatal(perr)
	}
	if len(violations) != n {
		t.Fatalf("got %d rows, want %d", len(violations), n)
	}
}

func TestTypeOpacityPolicyAcceptsLibraryLeavesAndChecksArguments(t *testing.T) {
	var items []facts.Fact
	add := func(relation string, values ...facts.Value) {
		items = append(items, facts.Fact{Relation: relation, Values: values})
	}
	add("fn_param", facts.String("fn:hidden"), facts.Number(0), facts.String("promise-hidden"))
	add("fn_param", facts.String("fn:plain"), facts.Number(0), facts.String("promise-string"))
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

	base, err := facts.Store(items)
	if err != nil {
		t.Fatal(err)
	}
	schemaDecls, prelude, err := facts.ParseSchema(typescript.Schema())
	if err != nil {
		t.Fatal(err)
	}
	output := runtime.ProgramOutput{Text: `
Decl permitted_opaque_leaf(Type).
permitted_opaque_leaf(T) :- type_named(T, "lib", "Promise"), type_origin_kind(T, /lib).
permitted_opaque_leaf(T) :- type_named(T, "lib", "Array"), type_origin_kind(T, /lib).
Decl disallowed_incomplete(Type).
disallowed_incomplete(T) :- type_incomplete(T, Reason), Reason != /members_omitted.
disallowed_incomplete(T) :- type_incomplete(T, /members_omitted), !permitted_opaque_leaf(T).
Decl reaches_disallowed_incomplete(Type).
reaches_disallowed_incomplete(T) :- disallowed_incomplete(T).
reaches_disallowed_incomplete(T) :- type_child(T, Child), reaches_disallowed_incomplete(Child).
Decl violation(Fn).
violation(Fn) :- fn_param(Fn, _, T), reaches_disallowed_incomplete(T).
`}
	violations, perr := evaluateProgram(base, prelude, schemaDecls, output, []string{"violation"}, nil, evalOptions{})
	if perr != nil {
		t.Fatal(perr)
	}
	if len(violations) != 1 || violations[0].anchor != "fn:hidden" {
		t.Fatalf("opacity policy violations: %v", violations)
	}
}
