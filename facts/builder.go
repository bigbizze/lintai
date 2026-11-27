package facts

import (
	"fmt"
	"reflect"
	"slices"
	"sort"
	"strings"
	"unicode/utf8"

	"codeberg.org/TauCeti/mangle-go/ast"
)

// Relation is an opaque handle bound to the builder that resolved it.
type Relation struct {
	owner *Builder
	state *relationState
}

func (r Relation) Wanted() bool { return r.owner != nil && r.state != nil && r.state.wanted }

type keyedAtom struct {
	key  string
	atom ast.Atom
}
type relationState struct {
	description      RelationDescription
	wanted, resolved bool
	rows             []keyedAtom
}

// Builder validates producer output and owns the resulting store-ready atoms.
type Builder struct {
	descriptor Descriptor
	relations  map[string]*relationState
	anchors    map[string]AnchorSupport
	err        error
	sealed     bool
}

func NewBuilder(descriptor Descriptor, requested []Predicate) (*Builder, error) {
	if descriptor.base == nil {
		return nil, fmt.Errorf("invalid producer descriptor")
	}
	b := &Builder{descriptor: descriptor, relations: make(map[string]*relationState, len(descriptor.base)), anchors: make(map[string]AnchorSupport)}
	for predicate, description := range descriptor.base {
		local := strings.TrimPrefix(predicate.Name, descriptor.description.Namespace+".")
		b.relations[local] = &relationState{description: description}
	}
	for _, predicate := range requested {
		description, ok := descriptor.base[predicate]
		if !ok {
			return nil, fmt.Errorf("producer %q was asked for unknown base predicate %s", descriptor.description.Namespace, predicate)
		}
		state := b.relations[strings.TrimPrefix(description.Predicate.Name, descriptor.description.Namespace+".")]
		if state.wanted {
			return nil, fmt.Errorf("producer %q request repeats %s", descriptor.description.Namespace, predicate)
		}
		state.wanted = true
	}
	return b, nil
}

func (b *Builder) fail(err error) {
	if b.err == nil {
		b.err = err
	}
}

// Relation resolves a local base relation, declaring production even if empty.
func (b *Builder) Relation(local string, arity int) (Relation, error) {
	if b.sealed {
		return Relation{}, fmt.Errorf("fact builder is sealed")
	}
	state, ok := b.relations[local]
	if !ok || state.description.Predicate.Arity != arity {
		err := fmt.Errorf("producer %q has no base relation %s/%d", b.descriptor.description.Namespace, local, arity)
		b.fail(err)
		return Relation{}, err
	}
	state.resolved = true
	return Relation{owner: b, state: state}, nil
}

// Add accepts local entity keys and qualifies them exactly once.
func (b *Builder) Add(relation Relation, values ...Value) {
	if b.sealed {
		return
	}
	if relation.owner != b || relation.state == nil {
		b.fail(fmt.Errorf("zero or foreign relation handle"))
		return
	}
	state := relation.state
	if !state.wanted {
		return
	}
	predicate := state.description.Predicate
	if len(values) != predicate.Arity {
		b.fail(fmt.Errorf("producer %q row of %s has arity %d, want %d", b.descriptor.description.Namespace, predicate, len(values), predicate.Arity))
		return
	}
	row := slices.Clone(values)
	args := make([]ast.BaseTerm, len(row))
	for i, value := range row {
		argument := state.description.Arguments[i]
		if !slices.Contains(argument.Representations, value.Kind) {
			b.fail(fmt.Errorf("producer %q row of %s argument %s has unaccepted representation %d", b.descriptor.description.Namespace, predicate, argument.Name, value.Kind))
			return
		}
		if argument.Role == PathRole && (value.Kind != StringKind || !normalizedPath(value.String)) {
			b.fail(fmt.Errorf("producer %q row of %s argument %s has invalid path %q", b.descriptor.description.Namespace, predicate, argument.Name, value.String))
			return
		}
		if argument.Role == EntityRole {
			if value.Kind != StringKind || value.String == "" {
				b.fail(fmt.Errorf("producer %q row of %s argument %s has invalid entity %q", b.descriptor.description.Namespace, predicate, argument.Name, value.String))
				return
			}
			value.String = b.descriptor.description.Namespace + ":" + value.String
			row[i] = value
		}
		if value.Kind == NameKind && len(argument.AllowedNames) > 0 && !slices.Contains(argument.AllowedNames, value.String) {
			b.fail(fmt.Errorf("producer %q row of %s argument %s has unaccepted name %q", b.descriptor.description.Namespace, predicate, argument.Name, "/"+value.String))
			return
		}
		constant, err := value.constant()
		if err != nil {
			b.fail(fmt.Errorf("producer %q row of %s: %w", b.descriptor.description.Namespace, predicate, err))
			return
		}
		args[i] = constant
	}
	key, err := canonicalRowKey(row)
	if err != nil {
		b.fail(err)
		return
	}
	state.rows = append(state.rows, keyedAtom{key: key, atom: ast.NewAtom(predicate.Name, args...)})
}

// Anchor adds detached reporting support for a producer-local key.
func (b *Builder) Anchor(localKey string, support AnchorSupport) {
	if b.sealed {
		return
	}
	if localKey == "" || !utf8.ValidString(localKey) {
		b.fail(fmt.Errorf("producer %q supplied an empty anchor key", b.descriptor.description.Namespace))
		return
	}
	if err := validateAnchor(support); err != nil {
		b.fail(fmt.Errorf("producer %q anchor %q: %w", b.descriptor.description.Namespace, localKey, err))
		return
	}
	key := b.descriptor.description.Namespace + ":" + localKey
	detached := cloneAnchor(support)
	if old, ok := b.anchors[key]; ok {
		if !reflect.DeepEqual(old, detached) {
			b.fail(fmt.Errorf("producer %q supplied conflicting anchor support for %q", b.descriptor.description.Namespace, key))
		}
		return
	}
	b.anchors[key] = detached
}

// ValidatedFacts contains immutable, already-converted atoms and reporting support.
type ValidatedFacts struct {
	namespace  string
	relations  map[Predicate][]ast.Atom
	anchors    map[string]AnchorSupport
	reportable map[Predicate]map[string]struct{}
}

func (v ValidatedFacts) Namespace() string { return v.namespace }
func (v ValidatedFacts) Relations() []Predicate {
	out := make([]Predicate, 0, len(v.relations))
	for p := range v.relations {
		out = append(out, p)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Name != out[j].Name {
			return out[i].Name < out[j].Name
		}
		return out[i].Arity < out[j].Arity
	})
	return out
}
func (v ValidatedFacts) Atoms(p Predicate) []ast.Atom {
	out := slices.Clone(v.relations[p])
	for i := range out {
		out[i].Args = slices.Clone(out[i].Args)
	}
	return out
}
func (v ValidatedFacts) Count(p Predicate) int { return len(v.relations[p]) }
func (v ValidatedFacts) Anchors() map[string]AnchorSupport {
	out := make(map[string]AnchorSupport, len(v.anchors))
	for id, support := range v.anchors {
		out[id] = cloneAnchor(support)
	}
	return out
}
func (v ValidatedFacts) Reportable(p Predicate) map[string]struct{} {
	out := make(map[string]struct{}, len(v.reportable[p]))
	for id := range v.reportable[p] {
		out[id] = struct{}{}
	}
	return out
}

// Finish validates complete coverage and support, then seals the builder.
func (b *Builder) Finish() (ValidatedFacts, error) {
	if b.sealed {
		return ValidatedFacts{}, fmt.Errorf("fact builder is sealed")
	}
	b.sealed = true
	if b.err != nil {
		return ValidatedFacts{}, b.err
	}
	out := ValidatedFacts{namespace: b.descriptor.description.Namespace, relations: make(map[Predicate][]ast.Atom), anchors: b.anchors, reportable: make(map[Predicate]map[string]struct{})}
	for _, state := range b.relations {
		if !state.wanted {
			continue
		}
		p := state.description.Predicate
		if !state.resolved {
			return ValidatedFacts{}, fmt.Errorf("producer %q omitted requested predicate %s", out.namespace, p)
		}
		sort.Slice(state.rows, func(i, j int) bool { return state.rows[i].key < state.rows[j].key })
		var previous string
		for i, row := range state.rows {
			if i > 0 && row.key == previous {
				continue
			}
			previous = row.key
			for index, argument := range state.description.Arguments {
				if argument.ReportSupport == ReportNone {
					continue
				}
				id, _ := row.atom.Args[index].(ast.Constant).StringValue()
				_, exists := b.anchors[id]
				if !exists && argument.ReportSupport == ReportRequired {
					return ValidatedFacts{}, fmt.Errorf("producer %q omitted required location support for %q in %s", out.namespace, id, p)
				}
				if exists {
					if out.reportable[p] == nil {
						out.reportable[p] = make(map[string]struct{})
					}
					out.reportable[p][id] = struct{}{}
				}
			}
			out.relations[p] = append(out.relations[p], row.atom)
		}
		if out.relations[p] == nil {
			out.relations[p] = []ast.Atom{}
		}
	}
	return out, nil
}
