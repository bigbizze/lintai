package typescript

import (
	_ "embed"
	"fmt"
	"math"
	"slices"
	"strings"

	"github.com/bigbizze/lintai/facts"
	"github.com/bigbizze/lintai/internal/ambient"
	"github.com/bigbizze/lintai/internal/tsgo/lintaiapi"
)

//go:embed schema.mg
var schemaText string

//go:embed prelude.mg
var preludeText string

// Schema returns the declarations and prelude owned by this backend.
func Schema() facts.Schema {
	return facts.Schema{Declarations: schemaText, Prelude: preludeText}
}

// factsFromSnapshot projects completed TypeScript records into public facts.
func factsFromSnapshot(snapshot *lintaiapi.Snapshot, sources []ambient.Source, adjustments []lintaiapi.ConfigAdjustment, namespaces ...string) ([]facts.Fact, error) {
	namespace := "ts"
	if len(namespaces) != 0 {
		namespace = namespaces[0]
	}
	b := builder{}
	for _, module := range snapshot.Modules {
		b.add("module", str(module.EntityID))
		b.add("module_path", str(module.EntityID), str(module.Path))
		b.location(module.EntityID, module.Range)
	}
	for _, fn := range snapshot.Functions {
		id := str(fn.SemanticKey)
		b.add("fn", id, str(fn.FilePath), str(fn.Name))
		b.add("fn_kind", id, facts.Name(fn.Kind))
		if fn.ContainerName != "" {
			b.add("fn_container", id, str(fn.ContainerName))
		}
		if fn.IsExported {
			b.add("fn_exported", id)
		}
		if fn.IsAsync {
			b.add("fn_async", id)
		}
		if fn.SignatureOmitted {
			b.add("fn_signature_omitted", id)
		}
		if fn.SignatureUnresolved {
			b.add("fn_signature_unresolved", id)
		}
		for _, observation := range fn.SignatureObservations {
			signature := lintaiapi.SignatureID(fn.SemanticKey, observation, namespace)
			sid := str(signature)
			b.add("fn_signature", id, sid)
			b.add("signature_arity", sid, facts.Number(int64(len(observation.ParameterTypes))))
			for index, typeID := range observation.ParameterTypes {
				if typeID != "" {
					b.add("signature_param", sid, facts.Number(int64(index)), str(typeID))
				}
			}
			if err := b.marks(sid, observation.ParameterMarks, "signature_param_optional", "signature_param_rest"); err != nil {
				return nil, err
			}
			if observation.ReturnType != "" {
				b.add("signature_returns", sid, str(observation.ReturnType))
			}
			if observation.Complete {
				b.add("signature_complete", sid)
			} else {
				b.add("signature_incomplete", sid, facts.Name("unresolved"))
			}
			b.location(signature, fn.Range)
		}
		if fn.SignatureConflict {
			b.add("fn_signature_conflict", id)
		}
		b.location(fn.SemanticKey, fn.Range)
	}
	for _, site := range snapshot.Sites {
		id := str(site.ID)
		b.add("site", id, str(site.File))
		if site.UnsupportedReason != "" {
			b.add("site_attribution_unsupported", id, facts.Name(site.UnsupportedReason))
		} else {
			b.add("site_owner", id, str(site.Owner))
		}
		b.location(site.ID, site.Range)
	}
	for _, site := range snapshot.SuspensionSites {
		b.add("suspension_site", str(site.SemanticKey), facts.Name(site.Kind))
	}
	for _, edge := range snapshot.ImportEdges {
		id := str(edge.SemanticKey)
		b.add("import_edge", id, str(edge.FromPath), str(edge.ToPath), str(edge.Specifier))
		form, err := name(edge.Form)
		if err != nil {
			return nil, err
		}
		b.add("import_form", id, form)
		if edge.Internal {
			b.add("import_internal", id)
		}
		if edge.Unresolved {
			b.add("import_unresolved", id)
		}
		if edge.Package != "" {
			b.add("import_package", id, str(edge.Package))
		}
		if edge.IsTypeOnly {
			b.add("import_type_only", id)
		}
		if edge.Retained {
			b.add("import_retained", id)
		}
		for _, symbol := range edge.ImportedSymbols {
			kind, err := name(symbol.Kind)
			if err != nil {
				return nil, err
			}
			b.add("import_symbol", id, str(symbol.Name), kind)
			if symbol.IsTypeOnly {
				b.add("import_symbol_type_only", id, str(symbol.Name))
			}
		}
		b.location(edge.SemanticKey, edge.Range)
	}
	callSites := make(map[string]bool)
	for _, edge := range snapshot.CallEdges {
		callSites[edge.SemanticKey] = true
		b.add("call_edge", str(edge.SemanticKey), str(edge.ToSemanticKey))
	}
	for _, call := range snapshot.ExternalCalls {
		callSites[call.SemanticKey] = true
		b.add("external_call", str(call.SemanticKey), facts.Name(call.OriginKind), str(call.Module), str(call.Name))
	}
	for _, call := range snapshot.CallsUnresolved {
		callSites[call.SemanticKey] = true
		b.add("call_unresolved", str(call.SemanticKey))
	}
	for site := range callSites {
		b.add("call_site", str(site))
	}
	for _, site := range snapshot.CallTargetConflicts {
		b.add("call_target_conflict", str(site))
	}
	for _, escape := range snapshot.FunctionEscapes {
		b.add("fn_value_escape", str(escape.Site), facts.Name("argument"))
		if escape.Target != "" {
			b.add("fn_value_escape_target", str(escape.Site), str(escape.Target))
		}
		if escape.Unknown {
			b.add("fn_value_escape_unknown", str(escape.Site))
		}
	}
	for _, item := range snapshot.CallDeclarations {
		b.add("call_declaration", str(item.Site), str(item.Declaration))
	}
	for _, item := range snapshot.DeclarationMembers {
		b.add("declaration_member", str(item.Declaration), str(item.OwnerDeclaration), str(item.Name))
	}
	for _, item := range snapshot.CallDeclarationIncomplete {
		reason, err := name(item.Reason)
		if err != nil {
			return nil, err
		}
		b.add("call_declaration_incomplete", str(item.Site), reason)
	}
	for _, item := range snapshot.ImportsOpaque {
		b.add("import_opaque", str(item.SemanticKey))
	}
	for _, ref := range snapshot.FnRefs {
		b.add("fn_ref", str(ref.SemanticKey), str(ref.ToSemanticKey))
	}
	for _, directive := range snapshot.Directives {
		b.add("module_directive", str(directive.SemanticKey), str(directive.Text))
	}
	for _, parseError := range snapshot.ParseErrors {
		b.add("module_parse_error", str(parseError.SemanticKey), str(parseError.Message))
	}
	for _, adjustment := range adjustments {
		action, err := name(adjustment.Action)
		if err != nil {
			return nil, err
		}
		b.add("config_adjusted", str(adjustment.Config), str(adjustment.Option), action)
	}
	for _, ref := range snapshot.TypeRefs {
		id := str(ref.SemanticKey)
		b.add("type_ref", id, str(ref.Name))
		if ref.TargetPath != "" {
			b.add("type_ref_target", id, str(ref.TargetPath))
		}
	}
	for _, item := range snapshot.Types {
		if err := b.typeFacts(item); err != nil {
			return nil, err
		}
	}
	for _, item := range snapshot.Declarations {
		id := str(item.ID)
		kind, err := name(item.Kind)
		if err != nil {
			return nil, err
		}
		b.add("declaration", id, str(item.Name), kind)
		if item.OriginKind != "" {
			originKind, err := name(item.OriginKind)
			if err != nil {
				return nil, err
			}
			b.add("declaration_origin", id, originKind, str(item.Origin))
		}
		for _, space := range item.TypeSubjects {
			b.add("declaration_type_subject", id, facts.Name(space))
		}
		for _, value := range item.Types {
			space, err := name(value.Space)
			if err != nil {
				return nil, err
			}
			b.add("declaration_type", id, space, str(value.Type))
		}
		for _, function := range item.FunctionObservations {
			b.add("declaration_fn", id, str(function))
		}
		if len(item.FunctionObservations) == 0 && item.Function != "" {
			b.add("declaration_fn", id, str(item.Function))
		}
		for _, reason := range item.FunctionIncomplete {
			b.add("declaration_fn_incomplete", id, facts.Name(reason))
		}
		for _, value := range item.TypeIncomplete {
			b.add("declaration_type_incomplete", id, facts.Name(value.Space), facts.Name(value.Reason))
		}
		for _, reason := range item.Incomplete {
			value, err := name(reason)
			if err != nil {
				return nil, err
			}
			b.add("declaration_metadata_incomplete", id, value)
		}
		b.location(item.ID, item.Location)
	}
	for _, item := range snapshot.Exports {
		id := str(item.ID)
		space, err := name(item.Space)
		if err != nil {
			return nil, err
		}
		b.add("module_export", id, str("module:"+item.File), str(item.Name), space)
		for _, target := range item.Targets {
			b.add("export_target", id, str(target))
		}
		for _, site := range item.Via {
			b.add("export_via", id, str(site))
		}
		for _, value := range item.Reexports {
			reexportSpace, err := name(value.Space)
			if err != nil {
				return nil, err
			}
			b.add("export_reexport", id, str(value.Site), str(value.Module), str(value.SourceName), reexportSpace)
		}
		for _, reason := range item.Incomplete {
			value, err := name(reason)
			if err != nil {
				return nil, err
			}
			b.add("export_incomplete", id, value)
		}
		location := item.Location
		if location == (lintaiapi.SourceLocation{}) {
			for _, module := range snapshot.Modules {
				if module.Path == item.File {
					location = module.Range
					break
				}
			}
		}
		b.location(item.ID, location)
	}
	for _, item := range snapshot.ModuleExportsIncomplete {
		reason, err := name(item.Reason)
		if err != nil {
			return nil, err
		}
		b.add("module_exports_incomplete", str("module:"+item.File), reason)
	}
	for _, item := range snapshot.DeclarationSourceCandidates {
		method, err := name(item.Method)
		if err != nil {
			return nil, err
		}
		b.add("declaration_source_candidate", str(item.CheckedDecl), str(item.SourceDecl), method)
	}
	for _, item := range snapshot.DeclarationSources {
		method, err := name(item.Method)
		if err != nil {
			return nil, err
		}
		b.add("declaration_source", str(item.CheckedDecl), str(item.SourceDecl), method)
	}
	for _, item := range snapshot.DeclarationSourceOutcomes {
		outcome, err := name(item.Outcome)
		if err != nil {
			return nil, err
		}
		b.add("declaration_source_outcome", str(item.CheckedDecl), outcome)
	}
	for _, item := range snapshot.SourceEntries {
		id := str(item.ID)
		b.add("source_entry", id, str(item.CheckedEntry), str(item.SourceEntry))
		if item.Tsconfig != "" {
			b.add("source_entry_tsconfig", id, str(item.Tsconfig))
		}
		if item.Applied {
			b.add("source_entry_applied", id)
		} else {
			b.add("source_entry_unapplied", id)
		}
		for _, reason := range item.Incomplete {
			value, err := name(reason)
			if err != nil {
				return nil, err
			}
			b.add("source_entry_incomplete", id, value)
		}
	}
	for _, item := range snapshot.SourceBindings {
		id := str(item.ID)
		space, err := name(item.Space)
		if err != nil {
			return nil, err
		}
		b.add("source_binding_attempt", id, str(item.Entry), str(item.CheckedFile), str(item.Name), space)
		if item.SourceFile != "" {
			b.add("source_binding", id, str(item.SourceFile), str(item.Name), space, facts.Name("source_entry"))
		}
		for _, declaration := range item.CheckedDeclarations {
			b.add("source_binding_checked_decl", id, str(declaration))
		}
		for _, declaration := range item.SourceDeclarations {
			b.add("source_binding_source_decl", id, str(declaration))
		}
		for _, reason := range item.Incomplete {
			value, err := name(reason)
			if err != nil {
				return nil, err
			}
			b.add("source_binding_incomplete", id, value)
		}
	}
	for _, item := range snapshot.ExportSites {
		b.location(item.ID, item.Location)
	}
	for _, access := range snapshot.Accesses {
		b.add("meta_access", str(access.SemanticKey), str(access.AccessPath))
	}
	for _, source := range sources {
		sourceName, err := name(source.ID)
		if err != nil {
			return nil, err
		}
		capabilities := make([]string, 0, len(source.Matchers))
		for _, matcher := range source.Matchers {
			if !slices.Contains(capabilities, matcher.Capability) {
				capabilities = append(capabilities, matcher.Capability)
			}
		}
		for _, capability := range capabilities {
			capabilityName, err := name(capability)
			if err != nil {
				return nil, err
			}
			b.add("ambient_source", sourceName, capabilityName)
		}
	}
	for _, use := range snapshot.AmbientUses {
		id := str(use.SemanticKey)
		sourceName, err := name(use.Source)
		if err != nil {
			return nil, err
		}
		capabilityName, err := name(use.Capability)
		if err != nil {
			return nil, err
		}
		b.add("ambient_use", id, sourceName, capabilityName)
		if use.HasKey {
			b.add("ambient_key", id, str(use.Key))
		}
		if use.Dynamic {
			b.add("ambient_dynamic", id)
		}
		if use.Write {
			b.add("ambient_write", id)
		}

	}
	for _, ref := range snapshot.GlobalRefs {
		b.add("global_ref", str(ref.SemanticKey), str(ref.Name))
	}

	return b.items, nil
}

func (b *builder) typeFacts(item lintaiapi.TypeFact) error {
	id := str(item.ID)
	kind, err := name(item.Kind)
	if err != nil {
		return err
	}
	b.add("type_kind", id, kind)
	if item.Named {
		b.add("type_named", id, str(item.Origin), str(item.Name))
		if item.OriginKind != "" {
			originKind, err := name(item.OriginKind)
			if err != nil {
				return err
			}
			b.add("type_origin_kind", id, originKind)
		}
	}
	for index, arg := range item.Args {
		b.add("type_arg", id, facts.Number(int64(index)), str(arg))
	}
	if err := b.marks(id, item.ArgMarks, "type_arg_optional", "type_arg_rest"); err != nil {
		return err
	}
	if item.Readonly {
		b.add("type_readonly", id)
	}
	for _, member := range item.Members {
		b.add("type_member", id, str(member))
	}
	if item.Literal != nil {
		value, err := literalValue(*item.Literal)
		if err != nil {
			return err
		}
		b.add("type_literal", id, facts.Name(item.Literal.Kind), value)
	}
	if item.Kind == "type_param" {
		b.add("type_param_name", id, str(item.ParamName))
	}
	for _, prop := range item.Props {
		b.add("type_prop", id, str(prop.Name), str(prop.Type))
		if prop.Optional {
			b.add("type_prop_optional", id, str(prop.Name))
		}
		if prop.Readonly {
			b.add("type_prop_readonly", id, str(prop.Name))
		}
	}
	if item.Kind == "function" {
		for index, param := range item.SigParams {
			b.add("type_sig_param", id, facts.Number(int64(index)), str(param))
		}
		if err := b.marks(id, item.SigMarks, "type_sig_param_optional", "type_sig_param_rest"); err != nil {
			return err
		}
		b.add("type_sig_returns", id, str(item.SigReturns))
	}
	for _, reason := range item.Incomplete {
		value, err := name(reason)
		if err != nil {
			return err
		}
		b.add("type_incomplete", id, value)
	}
	b.add("type_display", id, str(item.Display))
	if item.Location != nil {
		b.location(item.ID, *item.Location)
	}
	return nil
}

// marks adds a row of optional or rest for each marked tuple element or
// signature parameter.
func (b *builder) marks(id facts.Value, marks []string, optional, rest string) error {
	for index, mark := range marks {
		switch mark {
		case "":
		case "optional":
			b.add(optional, id, facts.Number(int64(index)))
		case "rest":
			b.add(rest, id, facts.Number(int64(index)))
		default:
			return fmt.Errorf("unknown type mark %q", mark)
		}
	}
	return nil
}

// literalValue returns the type_literal value of a literal type: a string,
// a number, /true or /false, or the digits of a bigint as a string.
func literalValue(literal lintaiapi.TypeLiteral) (facts.Value, error) {
	switch literal.Kind {
	case "string", "bigint":
		return str(literal.Text), nil
	case "boolean":
		if literal.Bool {
			return facts.Name("true"), nil
		}
		return facts.Name("false"), nil
	case "number":
		if literal.Number == math.Trunc(literal.Number) && math.Abs(literal.Number) <= 1<<53 {
			return facts.Number(int64(literal.Number)), nil
		}
		return facts.Float(literal.Number), nil
	}
	return facts.Value{}, fmt.Errorf("unknown literal kind %q", literal.Kind)
}

type builder struct {
	items []facts.Fact
}

func (b *builder) add(predicate string, args ...facts.Value) {
	b.items = append(b.items, facts.Fact{Relation: predicate, Values: args})
}

func (b *builder) location(entity string, location lintaiapi.SourceLocation) {
	if location.File == "" || strings.HasPrefix(location.File, "bundled://") || location.StartLine <= 0 || location.StartColumn <= 0 || location.EndLine <= 0 || location.EndColumn <= 0 {
		return
	}
	b.add("location",
		str(entity),
		str(location.File),
		facts.Number(int64(location.StartLine)),
		facts.Number(int64(location.StartColumn)),
		facts.Number(int64(location.EndLine)),
		facts.Number(int64(location.EndColumn)),
	)
}

func str(value string) facts.Value {
	return facts.String(value)
}

func name(value string) (facts.Value, error) {
	if value == "" {
		return facts.Value{}, fmt.Errorf("invalid empty name constant")
	}
	return facts.Name(value), nil
}
