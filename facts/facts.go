// Package facts defines the language-neutral fact and schema types exchanged
// between backends and the LintAI engine.
package facts

import (
	"fmt"
	"math"
	"sort"
	"strings"

	"codeberg.org/TauCeti/mangle-go/ast"
	"codeberg.org/TauCeti/mangle-go/factstore"
)

type ValueKind uint8

const (
	StringKind ValueKind = iota + 1
	NumberKind
	NameKind
	FloatKind
)

// Value is one typed Mangle constant. Name values are closed vocabulary
// symbols and render with a leading slash; strings remain quoted strings.
type Value struct {
	Kind   ValueKind
	String string
	Number int64
	Float  float64
}

func String(value string) Value { return Value{Kind: StringKind, String: value} }
func Number(value int64) Value  { return Value{Kind: NumberKind, Number: value} }

// Float creates a float64 constant. Use Number for integers.
func Float(value float64) Value { return Value{Kind: FloatKind, Float: value} }

// Name creates a Mangle name constant. Its value is written without the
// leading slash, for example Name("clock") represents /clock.
func Name(value string) Value { return Value{Kind: NameKind, String: value} }

type Fact struct {
	Relation string
	Values   []Value
}

// Schema contains a producer's base relation declarations and derived-fact prelude.
type Schema struct {
	Declarations string
	Prelude      string
}

// Store converts generic facts to the Mangle store consumed by the engine.
func Store(items []Fact) (factstore.ReadOnlyFactStore, error) {
	store := factstore.NewMultiIndexedArrayInMemoryStore()
	for _, item := range items {
		args := make([]ast.BaseTerm, 0, len(item.Values))
		for _, value := range item.Values {
			constant, err := value.constant()
			if err != nil {
				return nil, fmt.Errorf("%s: %w", item.Relation, err)
			}
			args = append(args, constant)
		}
		store.Add(ast.NewAtom(item.Relation, args...))
	}
	return store, nil
}

// FormatFacts renders already validated, qualified facts. allowedRelations is
// the exact public catalog view accepted by the caller, so unqualified names
// cannot act as aliases for a producer namespace.
func FormatFacts(items []Fact, allowedRelations, relations []string) (string, error) {
	allowed := make(map[string]struct{}, len(allowedRelations))
	for _, relation := range allowedRelations {
		if _, duplicate := allowed[relation]; duplicate {
			return "", fmt.Errorf("relation %q appears more than once in the catalog", relation)
		}
		allowed[relation] = struct{}{}
	}
	wanted := make(map[string]struct{}, len(allowed))
	if len(relations) == 0 {
		for relation := range allowed {
			wanted[relation] = struct{}{}
		}
	} else {
		for _, relation := range relations {
			if _, ok := allowed[relation]; !ok {
				return "", fmt.Errorf("unknown base relation %q", relation)
			}
			wanted[relation] = struct{}{}
		}
	}
	store, err := Store(items)
	if err != nil {
		return "", err
	}
	lines := make([]string, 0, store.EstimateFactCount())
	for _, predicate := range store.ListPredicates() {
		if _, ok := wanted[predicate.Symbol]; !ok {
			continue
		}
		if err := store.GetFacts(ast.NewQuery(predicate), func(fact ast.Atom) error {
			lines = append(lines, fact.String()+".")
			return nil
		}); err != nil {
			return "", err
		}
	}
	sort.Strings(lines)
	return strings.Join(lines, "\n"), nil
}

func (value Value) constant() (ast.Constant, error) {
	switch value.Kind {
	case StringKind:
		return ast.String(value.String), nil
	case NumberKind:
		return ast.Number(value.Number), nil
	case NameKind:
		return ast.Name("/" + value.String)
	case FloatKind:
		if math.IsNaN(value.Float) || math.IsInf(value.Float, 0) {
			return ast.Constant{}, fmt.Errorf("invalid float %v", value.Float)
		}
		return ast.Float64(value.Float), nil
	default:
		return ast.Constant{}, fmt.Errorf("unsupported fact value kind %d", value.Kind)
	}
}
