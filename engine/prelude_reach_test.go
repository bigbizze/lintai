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
