package engine

import (
	"cmp"
	"slices"
	"strings"
	"testing"

	"codeberg.org/TauCeti/mangle-go/analysis"
	"codeberg.org/TauCeti/mangle-go/ast"
	"codeberg.org/TauCeti/mangle-go/factstore"
	"codeberg.org/TauCeti/mangle-go/parse"
)

// orderedExplainTestStore supplies the same facts in either iteration order.
type orderedExplainTestStore struct {
	factstore.ReadOnlyFactStore
	reverse bool
}

func (store orderedExplainTestStore) GetFacts(query ast.Atom, callback func(ast.Atom) error) error {
	var matches []ast.Atom
	if err := store.ReadOnlyFactStore.GetFacts(query, func(atom ast.Atom) error {
		matches = append(matches, atom)
		return nil
	}); err != nil {
		return err
	}
	slices.SortFunc(matches, func(a, b ast.Atom) int {
		if order := cmp.Compare(a.String(), b.String()); order != 0 {
			return order
		}
		// [0] and [0.0] print and hash alike but are different values.
		if a.Predicate.Symbol == "choice" {
			integerList := ast.List([]ast.Constant{ast.Number(0)})
			if a.Args[0].Equals(integerList) != b.Args[0].Equals(integerList) {
				if a.Args[0].Equals(integerList) {
					return -1
				}
				return 1
			}
		}
		return 0
	})
	if store.reverse {
		slices.Reverse(matches)
	}
	for _, atom := range matches {
		if err := callback(atom); err != nil {
			return err
		}
	}
	return nil
}

func TestExplainStableNestedProof(t *testing.T) {
	unit, err := parse.Unit(strings.NewReader(`
		config("shared.ts", "tsconfig.json").
		config("shared.ts", "overlap.json").
		path("shared.ts", "src/shared.ts").
		choice([0]).
		choice([0.0]).
		reason([0], "integer").
		reason([0.0], "float").
		multi(M) :- config(M, A), config(M, B), A != B, choice(V), reason(V, R).
		violation(M) :- multi(M), path(M, P).
	`))
	if err != nil {
		t.Fatal(err)
	}
	info, err := analysis.AnalyzeOneUnit(unit, nil)
	if err != nil {
		t.Fatal(err)
	}
	strata, predToStratum, err := analysis.Stratify(analysis.Program{
		EdbPredicates: info.EdbPredicates,
		IdbPredicates: info.IdbPredicates,
		Rules:         info.Rules,
	})
	if err != nil {
		t.Fatal(err)
	}
	store := factstore.NewMultiIndexedArrayInMemoryStore()
	for _, atom := range info.InitialFacts {
		store.Add(atom)
	}
	if err := runEvaluation(info, strata, predToStratum, store, 0, nil); err != nil {
		t.Fatal(err)
	}
	goal := ast.NewAtom("violation", ast.String("shared.ts"))
	want := []string{
		`violation("shared.ts")`,
		`multi("shared.ts")`,
		`config("shared.ts","overlap.json")`,
		`config("shared.ts","tsconfig.json")`,
		`choice([0])`,
		`reason([0],"integer")`,
		`path("shared.ts","src/shared.ts")`,
	}
	for _, reverse := range []bool{false, true} {
		got := explain(info, orderedExplainTestStore{ReadOnlyFactStore: store, reverse: reverse}, goal)
		if !slices.Equal(got, want) {
			t.Errorf("reverse fact order %t: explanation = %q, want %q", reverse, got, want)
		}
	}
}
