package facts

import (
	"fmt"
	"regexp"
	"strings"

	"codeberg.org/TauCeti/mangle-go/ast"
	"codeberg.org/TauCeti/mangle-go/parse"
	"codeberg.org/TauCeti/mangle-go/symbols"
)

// DescribeSchema builds relation metadata from the producer's embedded
// declaration and prelude files. Argument role, representation, and reporting
// metadata live beside the declarations they describe.
func DescribeSchema(namespace, implementationID, implementationStamp, contractVersion string, configuration any, schema Schema) (ProducerDescription, error) {
	declarations, err := parse.Unit(strings.NewReader(schema.Declarations))
	if err != nil {
		return ProducerDescription{}, fmt.Errorf("producer declarations: %w", err)
	}
	prelude, err := parse.Unit(strings.NewReader(schema.Prelude))
	if err != nil {
		return ProducerDescription{}, fmt.Errorf("producer prelude: %w", err)
	}
	result := ProducerDescription{
		Namespace: namespace, ImplementationID: implementationID,
		ImplementationStamp: implementationStamp, ContractVersion: contractVersion,
		EffectiveConfiguration: configuration, Schema: schema,
	}
	for _, declaration := range declarations.Decls {
		if declaration.DeclaredAtom.Predicate == symbols.Package || declaration.DeclaredAtom.Predicate == symbols.Use {
			continue
		}
		item, err := relationDescription(namespace, declaration)
		if err != nil {
			return ProducerDescription{}, err
		}
		result.BaseRelations = append(result.BaseRelations, item)
	}
	for _, declaration := range prelude.Decls {
		if declaration.DeclaredAtom.Predicate == symbols.Package || declaration.DeclaredAtom.Predicate == symbols.Use {
			continue
		}
		item, err := relationDescription(namespace, declaration)
		if err != nil {
			return ProducerDescription{}, err
		}
		result.Helpers = append(result.Helpers, item)
	}
	return result, nil
}

func relationDescription(namespace string, declaration ast.Decl) (RelationDescription, error) {
	local := declaration.DeclaredAtom.Predicate.Symbol
	if !regexp.MustCompile(`^[a-z][a-z0-9_]*$`).MatchString(local) {
		return RelationDescription{}, fmt.Errorf("invalid local predicate name %q", local)
	}
	arguments := make([]ArgumentDescription, len(declaration.DeclaredAtom.Args))
	for index, term := range declaration.DeclaredAtom.Args {
		name := fmt.Sprint(term)
		role, err := declaredArgumentRole(declaration, name)
		if err != nil {
			return RelationDescription{}, err
		}
		representations, err := declaredArgumentRepresentations(declaration, name)
		if err != nil {
			return RelationDescription{}, err
		}
		reportSupport, err := declaredReportSupport(declaration, name)
		if err != nil {
			return RelationDescription{}, err
		}
		arguments[index] = ArgumentDescription{
			Name: name, Representations: representations,
			Role: role, ReportSupport: reportSupport,
		}
	}
	doc := relationDocumentation(declaration)
	return RelationDescription{
		Predicate: Predicate{Name: namespace + "." + local, Arity: declaration.DeclaredAtom.Predicate.Arity},
		Arguments: arguments, Documentation: doc,
	}, nil
}

func relationDocumentation(declaration ast.Decl) string {
	for _, descriptor := range declaration.Descr {
		if descriptor.Predicate.Symbol == "doc" && len(descriptor.Args) == 1 {
			if value, ok := descriptor.Args[0].(ast.Constant); ok {
				text, _ := value.StringValue()
				return text
			}
		}
	}
	return ""
}

func descriptorValues(declaration ast.Decl, descriptorName, argumentName string) ([]string, error) {
	values := make([]string, 0)
	for _, descriptor := range declaration.Descr {
		if descriptor.Predicate.Symbol != descriptorName || len(descriptor.Args) < 2 || fmt.Sprint(descriptor.Args[0]) != argumentName {
			continue
		}
		value, ok := descriptor.Args[1].(ast.Constant)
		if !ok {
			return nil, fmt.Errorf("%s metadata for %s must be a string constant", descriptorName, argumentName)
		}
		text, err := value.StringValue()
		if err != nil {
			return nil, fmt.Errorf("%s metadata for %s must be a string constant", descriptorName, argumentName)
		}
		values = append(values, text)
	}
	return values, nil
}

func declaredArgumentRole(declaration ast.Decl, name string) (ArgumentRole, error) {
	values, err := descriptorValues(declaration, "arg_role", name)
	if err != nil {
		return "", err
	}
	if len(values) == 0 {
		return ValueRole, nil
	}
	if len(values) != 1 {
		return "", fmt.Errorf("argument %s has multiple arg_role descriptors", name)
	}
	switch ArgumentRole(values[0]) {
	case ValueRole, PathRole, EntityRole:
		return ArgumentRole(values[0]), nil
	default:
		return "", fmt.Errorf("argument %s has unknown role %q", name, values[0])
	}
}

func declaredArgumentRepresentations(declaration ast.Decl, name string) ([]ValueKind, error) {
	values, err := descriptorValues(declaration, "arg_representation", name)
	if err != nil {
		return nil, err
	}
	if len(values) == 0 {
		return []ValueKind{StringKind}, nil
	}
	result := make([]ValueKind, 0, len(values))
	for _, value := range values {
		switch value {
		case "string":
			result = append(result, StringKind)
		case "integer":
			result = append(result, NumberKind)
		case "float":
			result = append(result, FloatKind)
		case "name":
			result = append(result, NameKind)
		default:
			return nil, fmt.Errorf("argument %s has unknown representation %q", name, value)
		}
	}
	if len(result) == 0 {
		return nil, fmt.Errorf("argument %s has no representations", name)
	}
	return result, nil
}

func declaredReportSupport(declaration ast.Decl, name string) (ReportSupport, error) {
	values, err := descriptorValues(declaration, "arg_report_support", name)
	if err != nil {
		return "", err
	}
	if len(values) == 0 {
		return ReportNone, nil
	}
	if len(values) != 1 {
		return "", fmt.Errorf("argument %s has multiple arg_report_support descriptors", name)
	}
	switch ReportSupport(values[0]) {
	case ReportNone, ReportRequired, ReportOptional:
		return ReportSupport(values[0]), nil
	default:
		return "", fmt.Errorf("argument %s has unknown report support %q", name, values[0])
	}
}
