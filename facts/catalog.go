package facts

import (
	"fmt"
	"math"
	"path"
	"regexp"
	"slices"
	"sort"
	"strconv"
	"strings"

	"codeberg.org/TauCeti/mangle-go/parse"
	"codeberg.org/TauCeti/mangle-go/symbols"
	"github.com/bigbizze/lintai/diagnostics"
	"github.com/bigbizze/lintai/internal/canonical"
)

type Predicate struct {
	Name  string
	Arity int
}

func (p Predicate) String() string { return fmt.Sprintf("%s/%d", p.Name, p.Arity) }

type ArgumentRole string

const (
	ValueRole  ArgumentRole = "value"
	PathRole   ArgumentRole = "path"
	EntityRole ArgumentRole = "entity"
)

type ReportSupport string

const (
	ReportNone     ReportSupport = "none"
	ReportRequired ReportSupport = "required"
	ReportOptional ReportSupport = "optional"
)

type ArgumentDescription struct {
	Name            string
	Representations []ValueKind
	Role            ArgumentRole
	ReportSupport   ReportSupport
}

type RelationDescription struct {
	Predicate     Predicate
	Arguments     []ArgumentDescription
	Documentation string
}

type ProducerDescription struct {
	Namespace              string
	ImplementationID       string
	ImplementationStamp    string
	ContractVersion        string
	EffectiveConfiguration any
	Schema                 Schema
	BaseRelations          []RelationDescription
	Helpers                []RelationDescription
}

type TrackingKey struct {
	Scheme string
	Data   any
}

type AnchorSupport struct {
	Location    diagnostics.SourceLocation
	Description string
	Tracking    *TrackingKey
}

type Row []Value

// Batch contains only the relations named by Collect's request. A nil or
// empty relation row set still represents successful production of that
// predicate.
type Batch struct {
	Relations     map[Predicate][]Row
	Anchors       map[string]AnchorSupport
	AnalyzedFiles []string
	InputRevision string
	Scope         any
	// WatchHints request future checks; they do not establish validity.
	WatchHints []string
}

// ValidatedFacts owns a defensive copy of a producer batch. Callers can only
// obtain copies, so a producer cannot mutate rows after publication.
type ValidatedFacts struct {
	relations     map[Predicate][]Row
	anchors       map[string]AnchorSupport
	analyzedFiles []string
	inputRevision string
	scope         any
	namespace     string
}

func (v ValidatedFacts) Namespace() string { return v.namespace }

func (v ValidatedFacts) Relations() []Predicate {
	result := make([]Predicate, 0, len(v.relations))
	for predicate := range v.relations {
		result = append(result, predicate)
	}
	sort.Slice(result, func(i, j int) bool {
		if result[i].Name != result[j].Name {
			return result[i].Name < result[j].Name
		}
		return result[i].Arity < result[j].Arity
	})
	return result
}

func (v ValidatedFacts) Rows(predicate Predicate) []Row {
	rows := v.relations[predicate]
	result := make([]Row, len(rows))
	for i, row := range rows {
		result[i] = slices.Clone(row)
	}
	return result
}

func (v ValidatedFacts) Anchors() map[string]AnchorSupport {
	result := make(map[string]AnchorSupport, len(v.anchors))
	for id, support := range v.anchors {
		result[id] = cloneAnchor(support)
	}
	return result
}

func (v ValidatedFacts) AnalyzedFiles() []string { return slices.Clone(v.analyzedFiles) }
func (v ValidatedFacts) InputRevision() string   { return v.inputRevision }
func (v ValidatedFacts) Scope() any              { return cloneCanonical(v.scope) }

func (v ValidatedFacts) Facts() []Fact {
	result := make([]Fact, 0)
	for _, predicate := range v.Relations() {
		for _, row := range v.relations[predicate] {
			result = append(result, Fact{Relation: predicate.Name, Values: slices.Clone(row)})
		}
	}
	return result
}

var namespacePattern = regexp.MustCompile(`^[a-z][a-z0-9_]*$`)

// ValidateBatch checks a producer description and an exact requested
// relation set, then freezes the producer-owned values behind ValidatedFacts.
func ValidateBatch(description ProducerDescription, requested []Predicate, batch Batch) (ValidatedFacts, error) {
	if !namespacePattern.MatchString(description.Namespace) || description.Namespace == "lintai" {
		return ValidatedFacts{}, fmt.Errorf("invalid or reserved producer namespace %q", description.Namespace)
	}
	if description.ImplementationID == "" || description.ImplementationStamp == "" || description.ContractVersion == "" {
		return ValidatedFacts{}, fmt.Errorf("producer %q must declare implementation id, stamp, and contract version", description.Namespace)
	}
	if _, err := canonical.Marshal(description.EffectiveConfiguration); err != nil {
		return ValidatedFacts{}, fmt.Errorf("producer %q effective configuration: %w", description.Namespace, err)
	}
	if _, err := canonical.Marshal(batch.Scope); err != nil {
		return ValidatedFacts{}, fmt.Errorf("producer %q scope: %w", description.Namespace, err)
	}

	metadata := make(map[Predicate]RelationDescription, len(description.BaseRelations)+len(description.Helpers))
	localNames := make(map[string]struct{}, len(description.BaseRelations)+len(description.Helpers))
	for _, relation := range append(slices.Clone(description.BaseRelations), description.Helpers...) {
		if err := validateRelation(description.Namespace, relation); err != nil {
			return ValidatedFacts{}, err
		}
		if _, exists := metadata[relation.Predicate]; exists {
			return ValidatedFacts{}, fmt.Errorf("producer %q declares %s more than once", description.Namespace, relation.Predicate)
		}
		local := strings.TrimPrefix(relation.Predicate.Name, description.Namespace+".")
		if _, exists := localNames[local]; exists {
			return ValidatedFacts{}, fmt.Errorf("producer %q declares multiple arities for %q", description.Namespace, local)
		}
		localNames[local] = struct{}{}
		metadata[relation.Predicate] = relation
	}
	if err := validateExtensionalCatalog(description); err != nil {
		return ValidatedFacts{}, err
	}
	wanted := make(map[Predicate]struct{}, len(requested))
	for _, predicate := range requested {
		if _, ok := metadata[predicate]; !ok {
			return ValidatedFacts{}, fmt.Errorf("producer %q was asked for unknown predicate %s", description.Namespace, predicate)
		}
		if _, duplicate := wanted[predicate]; duplicate {
			return ValidatedFacts{}, fmt.Errorf("producer %q request repeats %s", description.Namespace, predicate)
		}
		wanted[predicate] = struct{}{}
	}
	result := ValidatedFacts{
		relations:     make(map[Predicate][]Row, len(wanted)),
		anchors:       make(map[string]AnchorSupport, len(batch.Anchors)),
		analyzedFiles: slices.Clone(batch.AnalyzedFiles),
		inputRevision: batch.InputRevision,
		scope:         cloneCanonical(batch.Scope),
		namespace:     description.Namespace,
	}
	for predicate := range batch.Relations {
		if _, ok := wanted[predicate]; !ok {
			return ValidatedFacts{}, fmt.Errorf("producer %q returned unrequested predicate %s", description.Namespace, predicate)
		}
	}
	for predicate := range wanted {
		rows, present := batch.Relations[predicate]
		if !present {
			return ValidatedFacts{}, fmt.Errorf("producer %q omitted requested predicate %s", description.Namespace, predicate)
		}
		descriptor := metadata[predicate]
		frozen := make([]Row, 0, len(rows))
		rowKeys := make(map[string]struct{}, len(rows))
		for rowIndex, row := range rows {
			if len(row) != predicate.Arity {
				return ValidatedFacts{}, fmt.Errorf("producer %q row %d of %s has arity %d, want %d", description.Namespace, rowIndex, predicate, len(row), predicate.Arity)
			}
			for index, value := range row {
				argument := descriptor.Arguments[index]
				if !slices.Contains(argument.Representations, value.Kind) {
					return ValidatedFacts{}, fmt.Errorf("producer %q row %d of %s argument %s has unaccepted representation %d", description.Namespace, rowIndex, predicate, argument.Name, value.Kind)
				}
				if argument.Role == PathRole && value.Kind != StringKind {
					return ValidatedFacts{}, fmt.Errorf("producer %q row %d of %s path argument %s is not a string", description.Namespace, rowIndex, predicate, argument.Name)
				}
				if _, err := value.constant(); err != nil {
					return ValidatedFacts{}, fmt.Errorf("producer %q row %d of %s: %w", description.Namespace, rowIndex, predicate, err)
				}
				if argument.Role == EntityRole {
					if value.Kind != StringKind || !ownsEntity(description.Namespace, value.String) {
						return ValidatedFacts{}, fmt.Errorf("producer %q row %d of %s argument %s has foreign or invalid entity %q", description.Namespace, rowIndex, predicate, argument.Name, value.String)
					}
				}
				if argument.ReportSupport == ReportRequired {
					if value.Kind != StringKind {
						return ValidatedFacts{}, fmt.Errorf("producer %q required report argument %s of %s is not an entity string", description.Namespace, argument.Name, predicate)
					}
					if _, present := batch.Anchors[value.String]; !present {
						return ValidatedFacts{}, fmt.Errorf("producer %q omitted required location support for %q in %s", description.Namespace, value.String, predicate)
					}
				}
			}
			key, err := canonicalRowKey(row)
			if err != nil {
				return ValidatedFacts{}, err
			}
			if _, duplicate := rowKeys[key]; duplicate {
				continue
			}
			rowKeys[key] = struct{}{}
			frozen = append(frozen, slices.Clone(row))
		}
		sort.Slice(frozen, func(i, j int) bool {
			left, _ := canonicalRowKey(frozen[i])
			right, _ := canonicalRowKey(frozen[j])
			return left < right
		})
		result.relations[predicate] = frozen
	}
	for id, support := range batch.Anchors {
		if !ownsEntity(description.Namespace, id) {
			return ValidatedFacts{}, fmt.Errorf("producer %q supplied anchor with foreign or invalid entity %q", description.Namespace, id)
		}
		if err := validateAnchor(support); err != nil {
			return ValidatedFacts{}, fmt.Errorf("producer %q anchor %q: %w", description.Namespace, id, err)
		}
		result.anchors[id] = cloneAnchor(support)
	}
	for _, file := range result.analyzedFiles {
		if !normalizedPath(file) {
			return ValidatedFacts{}, fmt.Errorf("producer %q has non-normalized analyzed path %q", description.Namespace, file)
		}
	}
	sort.Strings(result.analyzedFiles)
	result.analyzedFiles = slices.Compact(result.analyzedFiles)
	return result, nil
}

func validateExtensionalCatalog(description ProducerDescription) error {
	declarations, err := parse.Unit(strings.NewReader(description.Schema.Declarations))
	if err != nil {
		return fmt.Errorf("producer %q catalog declarations: %w", description.Namespace, err)
	}
	extensional := make(map[Predicate]struct{})
	for _, declaration := range declarations.Decls {
		if declaration.DeclaredAtom.Predicate == symbols.Package || declaration.DeclaredAtom.Predicate == symbols.Use || !declaration.IsExtensional() {
			continue
		}
		local := declaration.DeclaredAtom.Predicate
		predicate := Predicate{Name: description.Namespace + "." + local.Symbol, Arity: local.Arity}
		if _, duplicate := extensional[predicate]; duplicate {
			return fmt.Errorf("producer %q catalog declares extensional predicate %s more than once", description.Namespace, predicate)
		}
		extensional[predicate] = struct{}{}
	}
	baseRelations := make(map[Predicate]struct{}, len(description.BaseRelations))
	for _, relation := range description.BaseRelations {
		baseRelations[relation.Predicate] = struct{}{}
	}
	for predicate := range extensional {
		if _, exists := baseRelations[predicate]; !exists {
			return fmt.Errorf("producer %q catalog declares extensional predicate %s absent from BaseRelations", description.Namespace, predicate)
		}
	}
	for predicate := range baseRelations {
		if _, exists := extensional[predicate]; !exists {
			return fmt.Errorf("producer %q BaseRelations predicate %s has no extensional catalog declaration", description.Namespace, predicate)
		}
	}
	return nil
}

func validateRelation(namespace string, relation RelationDescription) error {
	prefix := namespace + "."
	if !strings.HasPrefix(relation.Predicate.Name, prefix) || !namespacePattern.MatchString(strings.TrimPrefix(relation.Predicate.Name, prefix)) || relation.Predicate.Arity < 0 {
		return fmt.Errorf("producer %q has invalid qualified predicate %q/%d", namespace, relation.Predicate.Name, relation.Predicate.Arity)
	}
	if len(relation.Arguments) != relation.Predicate.Arity {
		return fmt.Errorf("producer %q relation %s has %d argument descriptors, want %d", namespace, relation.Predicate, len(relation.Arguments), relation.Predicate.Arity)
	}
	for _, argument := range relation.Arguments {
		if argument.Name == "" || len(argument.Representations) == 0 {
			return fmt.Errorf("producer %q relation %s has incomplete argument metadata", namespace, relation.Predicate)
		}
		if argument.Role != ValueRole && argument.Role != PathRole && argument.Role != EntityRole {
			return fmt.Errorf("producer %q relation %s argument %s has invalid role %q", namespace, relation.Predicate, argument.Name, argument.Role)
		}
		if argument.Role != ValueRole && !slices.Contains(argument.Representations, StringKind) {
			return fmt.Errorf("producer %q relation %s argument %s must accept strings for its %s role", namespace, relation.Predicate, argument.Name, argument.Role)
		}
		if argument.ReportSupport != ReportNone && argument.ReportSupport != ReportRequired && argument.ReportSupport != ReportOptional {
			return fmt.Errorf("producer %q relation %s argument %s has invalid report support %q", namespace, relation.Predicate, argument.Name, argument.ReportSupport)
		}
		if argument.Role != EntityRole && argument.ReportSupport != ReportNone {
			return fmt.Errorf("producer %q relation %s non-entity argument %s has report support", namespace, relation.Predicate, argument.Name)
		}
	}
	return nil
}

func ownsEntity(namespace, id string) bool {
	owner, key, ok := strings.Cut(id, ":")
	return ok && owner == namespace && key != ""
}

func validateAnchor(support AnchorSupport) error {
	location := support.Location
	if strings.TrimSpace(support.Description) == "" {
		return fmt.Errorf("description is empty")
	}
	if !normalizedPath(location.File) || location.StartLine < 1 || location.StartColumn < 1 || location.EndLine < 1 || location.EndColumn < 1 {
		return fmt.Errorf("location must use positive 1-based coordinates and a file")
	}
	if location.EndLine < location.StartLine || location.EndLine == location.StartLine && location.EndColumn < location.StartColumn {
		return fmt.Errorf("location end precedes its start")
	}
	if support.Tracking != nil {
		if support.Tracking.Scheme == "" {
			return fmt.Errorf("tracking scheme is empty")
		}
		if err := validateCanonicalData(support.Tracking.Data); err != nil {
			return fmt.Errorf("invalid tracking data: %w", err)
		}
		if _, err := canonical.Marshal(support.Tracking.Data); err != nil {
			return fmt.Errorf("invalid tracking data: %w", err)
		}
	}
	return nil
}

func validateCanonicalData(value any) error {
	switch typed := value.(type) {
	case nil, bool, string:
		return nil
	case int:
		if int64(typed) > (1<<53)-1 || int64(typed) < -((1<<53)-1) {
			return fmt.Errorf("integer is outside the safe range")
		}
	case int64:
		if typed > (1<<53)-1 || typed < -((1<<53)-1) {
			return fmt.Errorf("integer is outside the safe range")
		}
	case float64, float32:
		return fmt.Errorf("floating point values must use a tagged string")
	case []any:
		for _, item := range typed {
			if err := validateCanonicalData(item); err != nil {
				return err
			}
		}
	case map[string]any:
		for _, item := range typed {
			if err := validateCanonicalData(item); err != nil {
				return err
			}
		}
	default:
		return fmt.Errorf("unsupported value type %T", value)
	}
	return nil
}

func normalizedPath(value string) bool {
	return value != "" && value != "." && value != "/" && !strings.Contains(value, `\`) && path.Clean(value) == value
}

func cloneAnchor(support AnchorSupport) AnchorSupport {
	if support.Tracking != nil {
		key := *support.Tracking
		key.Data = cloneCanonical(key.Data)
		support.Tracking = &key
	}
	return support
}

func cloneCanonical(value any) any {
	switch typed := value.(type) {
	case []any:
		result := make([]any, len(typed))
		for i, item := range typed {
			result[i] = cloneCanonical(item)
		}
		return result
	case map[string]any:
		result := make(map[string]any, len(typed))
		for key, item := range typed {
			result[key] = cloneCanonical(item)
		}
		return result
	default:
		return value
	}
}

func validFiniteFloat(value float64) bool { return !math.IsNaN(value) && !math.IsInf(value, 0) }

func canonicalRowKey(row Row) (string, error) {
	values := make([]any, len(row))
	for index, value := range row {
		switch value.Kind {
		case StringKind:
			values[index] = []any{"s", value.String}
		case NameKind:
			values[index] = []any{"n", value.String}
		case NumberKind:
			values[index] = []any{"i", strconv.FormatInt(value.Number, 10)}
		case FloatKind:
			if !validFiniteFloat(value.Float) {
				return "", fmt.Errorf("fact contains invalid float %v", value.Float)
			}
			bits := math.Float64bits(value.Float)
			if value.Float == 0 {
				bits = 0
			}
			values[index] = []any{"f", fmt.Sprintf("%016x", bits)}
		default:
			return "", fmt.Errorf("unsupported fact value kind %d", value.Kind)
		}
	}
	encoded, err := canonical.Marshal(values)
	return string(encoded), err
}
