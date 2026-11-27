package facts

import (
	"fmt"
	"math"
	"path"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"unicode/utf8"

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
	// AllowedNames contains name values without their leading slash; an empty
	// set leaves name values open. Other representations are unaffected.
	AllowedNames  []string
	Role          ArgumentRole
	ReportSupport ReportSupport
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

var namespacePattern = regexp.MustCompile(`^[a-z][a-z0-9_]*$`)

// Descriptor is a validated, detached producer description.
type Descriptor struct {
	description ProducerDescription
	base        map[Predicate]RelationDescription
}

// ValidateDescription validates the producer contract once for a catalog.
func ValidateDescription(description ProducerDescription) (Descriptor, error) {
	if !namespacePattern.MatchString(description.Namespace) || description.Namespace == "lintai" {
		return Descriptor{}, fmt.Errorf("invalid or reserved producer namespace %q", description.Namespace)
	}
	if description.ImplementationID == "" || description.ImplementationStamp == "" || description.ContractVersion == "" || !utf8.ValidString(description.ImplementationID) || !utf8.ValidString(description.ImplementationStamp) || !utf8.ValidString(description.ContractVersion) {
		return Descriptor{}, fmt.Errorf("producer %q must declare implementation id, stamp, and contract version", description.Namespace)
	}
	if _, err := canonical.MarshalStructured(description.EffectiveConfiguration); err != nil {
		return Descriptor{}, fmt.Errorf("producer %q effective configuration: %w", description.Namespace, err)
	}
	description = cloneDescription(description)
	all := make(map[Predicate]struct{}, len(description.BaseRelations)+len(description.Helpers))
	locals := make(map[string]struct{}, len(all))
	base := make(map[Predicate]RelationDescription, len(description.BaseRelations))
	for _, relation := range append(slices.Clone(description.BaseRelations), description.Helpers...) {
		if err := validateRelation(description.Namespace, relation); err != nil {
			return Descriptor{}, err
		}
		if _, exists := all[relation.Predicate]; exists {
			return Descriptor{}, fmt.Errorf("producer %q declares %s more than once", description.Namespace, relation.Predicate)
		}
		local := strings.TrimPrefix(relation.Predicate.Name, description.Namespace+".")
		if _, exists := locals[local]; exists {
			return Descriptor{}, fmt.Errorf("producer %q declares multiple arities for %q", description.Namespace, local)
		}
		locals[local] = struct{}{}
		all[relation.Predicate] = struct{}{}
	}
	for _, relation := range description.BaseRelations {
		base[relation.Predicate] = relation
	}
	if err := validateExtensionalCatalog(description); err != nil {
		return Descriptor{}, err
	}
	return Descriptor{description: description, base: base}, nil
}

func (d Descriptor) Description() ProducerDescription { return cloneDescription(d.description) }

func cloneDescription(d ProducerDescription) ProducerDescription {
	d.EffectiveConfiguration = cloneCanonical(d.EffectiveConfiguration)
	copyRelations := func(in []RelationDescription) []RelationDescription {
		out := slices.Clone(in)
		for i := range out {
			out[i].Arguments = slices.Clone(out[i].Arguments)
			for j := range out[i].Arguments {
				out[i].Arguments[j].Representations = slices.Clone(out[i].Arguments[j].Representations)
				out[i].Arguments[j].AllowedNames = slices.Clone(out[i].Arguments[j].AllowedNames)
			}
		}
		return out
	}
	d.BaseRelations, d.Helpers = copyRelations(d.BaseRelations), copyRelations(d.Helpers)
	return d
}

func validateExtensionalCatalog(description ProducerDescription) error {
	extensional := make(map[Predicate]struct{})
	helpers := make(map[Predicate]struct{})
	for _, source := range []string{description.Schema.Declarations, description.Schema.Prelude} {
		unit, err := parse.Unit(strings.NewReader(source))
		if err != nil {
			return fmt.Errorf("producer %q catalog declarations: %w", description.Namespace, err)
		}
		for _, declaration := range unit.Decls {
			if declaration.DeclaredAtom.Predicate == symbols.Package || declaration.DeclaredAtom.Predicate == symbols.Use {
				continue
			}
			local := declaration.DeclaredAtom.Predicate
			predicate := Predicate{Name: description.Namespace + "." + local.Symbol, Arity: local.Arity}
			if _, exists := extensional[predicate]; exists {
				return fmt.Errorf("producer %q catalog declares predicate %s more than once", description.Namespace, predicate)
			}
			if _, exists := helpers[predicate]; exists {
				return fmt.Errorf("producer %q catalog declares predicate %s more than once", description.Namespace, predicate)
			}
			if declaration.IsExtensional() {
				extensional[predicate] = struct{}{}
			} else {
				helpers[predicate] = struct{}{}
			}
		}
	}
	baseRelations := make(map[Predicate]struct{}, len(description.BaseRelations))
	for _, relation := range description.BaseRelations {
		baseRelations[relation.Predicate] = struct{}{}
	}
	for predicate := range extensional {
		if _, ok := baseRelations[predicate]; !ok {
			return fmt.Errorf("producer %q catalog declares extensional predicate %s absent from BaseRelations", description.Namespace, predicate)
		}
	}
	for predicate := range baseRelations {
		if _, ok := extensional[predicate]; !ok {
			return fmt.Errorf("producer %q BaseRelations predicate %s has no extensional catalog declaration", description.Namespace, predicate)
		}
	}
	describedHelpers := make(map[Predicate]struct{}, len(description.Helpers))
	for _, relation := range description.Helpers {
		describedHelpers[relation.Predicate] = struct{}{}
	}
	for predicate := range helpers {
		if _, ok := describedHelpers[predicate]; !ok {
			return fmt.Errorf("producer %q catalog helper %s absent from Helpers", description.Namespace, predicate)
		}
	}
	for predicate := range describedHelpers {
		if _, ok := helpers[predicate]; !ok {
			return fmt.Errorf("producer %q helper %s has no non-extensional catalog declaration", description.Namespace, predicate)
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
	names := make(map[string]bool, len(relation.Arguments))
	for _, argument := range relation.Arguments {
		if argument.Name == "" || !utf8.ValidString(argument.Name) || names[argument.Name] || len(argument.Representations) == 0 {
			return fmt.Errorf("producer %q relation %s has incomplete argument metadata", namespace, relation.Predicate)
		}
		names[argument.Name] = true
		seenKinds := make(map[ValueKind]bool)
		for _, kind := range argument.Representations {
			if kind < StringKind || kind > FloatKind || seenKinds[kind] {
				return fmt.Errorf("producer %q relation %s argument %s has invalid representation %d", namespace, relation.Predicate, argument.Name, kind)
			}
			seenKinds[kind] = true
		}
		seenNames := make(map[string]bool)
		for _, name := range argument.AllowedNames {
			if !seenKinds[NameKind] || seenNames[name] {
				return fmt.Errorf("producer %q relation %s argument %s has invalid allowed name %q", namespace, relation.Predicate, argument.Name, name)
			}
			if _, err := Name(name).constant(); err != nil {
				return fmt.Errorf("producer %q relation %s argument %s: %w", namespace, relation.Predicate, argument.Name, err)
			}
			seenNames[name] = true
		}
		if argument.Role != ValueRole && argument.Role != PathRole && argument.Role != EntityRole {
			return fmt.Errorf("producer %q relation %s argument %s has invalid role %q", namespace, relation.Predicate, argument.Name, argument.Role)
		}
		if argument.Role != ValueRole && (len(argument.Representations) != 1 || argument.Representations[0] != StringKind) {
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

func validateAnchor(support AnchorSupport) error {
	location := support.Location
	if strings.TrimSpace(support.Description) == "" || !utf8.ValidString(support.Description) || strings.ContainsRune(support.Description, 0) {
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
	return value != "" && value != "." && value != "/" && utf8.ValidString(value) && !strings.ContainsRune(value, 0) && !strings.Contains(value, `\`) && path.Clean(value) == value
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
	case []string:
		return slices.Clone(typed)
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

func canonicalRowKey(row []Value) (string, error) {
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
