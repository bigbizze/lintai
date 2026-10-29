// Package facts defines the language-neutral fact and schema types exchanged
// between backends and the LintAI engine.
package facts

import (
	"fmt"
	"slices"
	"sort"
	"strings"

	"codeberg.org/TauCeti/mangle-go/ast"
	"codeberg.org/TauCeti/mangle-go/factstore"
	"codeberg.org/TauCeti/mangle-go/parse"
	"codeberg.org/TauCeti/mangle-go/symbols"
)

const LocationRelation = "location"

type ValueKind uint8

const (
	StringKind ValueKind = iota + 1
	NumberKind
	NameKind
)

// Value is one typed Mangle constant. Name values are closed vocabulary
// symbols and render with a leading slash; strings remain quoted strings.
type Value struct {
	Kind   ValueKind
	String string
	Number int64
}

func String(value string) Value { return Value{Kind: StringKind, String: value} }
func Number(value int64) Value  { return Value{Kind: NumberKind, Number: value} }

// Name creates a Mangle name constant. Its value is written without the
// leading slash, for example Name("clock") represents /clock.
func Name(value string) Value { return Value{Kind: NameKind, String: value} }

type Fact struct {
	Relation string
	Values   []Value
}

// Schema contains the backend's base relation declarations and derived-fact
// prelude. Keeping these with the snapshot lets independently developed
// backends define their own vocabularies.
type Schema struct {
	Declarations string
	Prelude      string
}

// Snapshot is a deterministic, versioned set of backend facts.
type Snapshot struct {
	Version string
	Schema  Schema
	Facts   []Fact
}

// Validate checks that a snapshot has a versioned schema and that every fact
// matches a declared base relation.
func Validate(snapshot Snapshot) error {
	if snapshot.Version == "" {
		return fmt.Errorf("backend snapshot version is required")
	}
	decls, _, err := ParseSchema(snapshot.Schema)
	if err != nil {
		return err
	}
	if _, ok := decls[ast.PredicateSym{Symbol: LocationRelation, Arity: 6}]; !ok {
		return fmt.Errorf("backend schema must declare %s/6", LocationRelation)
	}
	for _, fact := range snapshot.Facts {
		sym := ast.PredicateSym{Symbol: fact.Relation, Arity: len(fact.Values)}
		_, ok := decls[sym]
		if !ok {
			return fmt.Errorf("backend fact uses undeclared relation or wrong arity: %s/%d", fact.Relation, len(fact.Values))
		}
		for _, value := range fact.Values {
			if _, err := value.constant(); err != nil {
				return fmt.Errorf("backend fact %s: %w", fact.Relation, err)
			}
		}
		if fact.Relation == LocationRelation && !validLocation(fact.Values) {
			return fmt.Errorf("backend %s facts must use string entity and file values followed by four numbers", LocationRelation)
		}
	}
	return nil
}

func validLocation(values []Value) bool {
	return len(values) == 6 &&
		values[0].Kind == StringKind && values[1].Kind == StringKind &&
		values[2].Kind == NumberKind && values[3].Kind == NumberKind &&
		values[4].Kind == NumberKind && values[5].Kind == NumberKind
}

// ParseSchema parses declarations and prelude supplied by a backend.
func ParseSchema(schema Schema) (map[ast.PredicateSym]ast.Decl, parse.SourceUnit, error) {
	declarations, err := parse.Unit(strings.NewReader(schema.Declarations))
	if err != nil {
		return nil, parse.SourceUnit{}, fmt.Errorf("backend declarations: %w", err)
	}
	prelude, err := parse.Unit(strings.NewReader(schema.Prelude))
	if err != nil {
		return nil, parse.SourceUnit{}, fmt.Errorf("backend prelude: %w", err)
	}
	decls := make(map[ast.PredicateSym]ast.Decl)
	for _, decl := range declarations.Decls {
		if decl.DeclaredAtom.Predicate == symbols.Package || decl.DeclaredAtom.Predicate == symbols.Use {
			continue
		}
		decls[decl.DeclaredAtom.Predicate] = decl
	}
	return decls, prelude, nil
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

// Relations returns the declared base relation names in declaration order.
func Relations(schema Schema) ([]string, error) {
	unit, err := parse.Unit(strings.NewReader(schema.Declarations))
	if err != nil {
		return nil, fmt.Errorf("backend declarations: %w", err)
	}
	result := make([]string, 0, len(unit.Decls))
	for _, decl := range unit.Decls {
		if decl.DeclaredAtom.Predicate == symbols.Package || decl.DeclaredAtom.Predicate == symbols.Use {
			continue
		}
		result = append(result, decl.DeclaredAtom.Predicate.Symbol)
	}
	return result, nil
}

// Format renders snapshot facts in sorted Mangle syntax. When relations is
// empty, every declared base relation is rendered.
func Format(snapshot Snapshot, relations []string) (string, error) {
	decls, err := Relations(snapshot.Schema)
	if err != nil {
		return "", err
	}
	wanted := make(map[string]struct{}, len(decls))
	if len(relations) == 0 {
		for _, relation := range decls {
			wanted[relation] = struct{}{}
		}
	} else {
		for _, relation := range relations {
			if !slices.Contains(decls, relation) {
				return "", fmt.Errorf("unknown base relation %q", relation)
			}
			wanted[relation] = struct{}{}
		}
	}
	store, err := Store(snapshot.Facts)
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
	default:
		return ast.Constant{}, fmt.Errorf("unsupported fact value kind %d", value.Kind)
	}
}
