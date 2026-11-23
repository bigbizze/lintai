// Package facts defines the language-neutral fact and schema types exchanged
// between backends and the LintAI engine.
package facts

import (
	"fmt"
	"math"
	"regexp"
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

// Value is one typed Mangle constant. Name values render with a leading slash;
// strings remain quoted strings.
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

// Schema contains a producer's base relation declarations and derived-fact prelude.
type Schema struct {
	Declarations string
	Prelude      string
}

// FormatFacts renders already validated, qualified facts. allowedRelations is
// the exact public catalog view accepted by the caller, so unqualified names
// cannot act as aliases for a producer namespace.
func FormatFacts(store factstore.ReadOnlyFactStore, allowedRelations, relations []string) (string, error) {
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

var namePattern = regexp.MustCompile(`^[A-Za-z0-9._~%\-]+(/[A-Za-z0-9._~%\-]+)*$`)

func (value Value) constant() (ast.Constant, error) {
	switch value.Kind {
	case StringKind:
		return ast.String(value.String), nil
	case NumberKind:
		return ast.Number(value.Number), nil
	case NameKind:
		if !namePattern.MatchString(value.String) {
			return ast.Constant{}, fmt.Errorf("invalid name %q", value.String)
		}
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
