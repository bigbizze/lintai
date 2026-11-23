package typescript

import (
	"context"
	_ "embed"
	"fmt"
	"math"
	"slices"
	"strings"

	"github.com/bigbizze/lintai/diagnostics"
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

// emitSnapshot projects detached compiler records directly into the fact builder.
func emitSnapshot(ctx context.Context, output *facts.Builder, description facts.ProducerDescription, snapshot *lintaiapi.Snapshot, sources []ambient.Source, adjustments []lintaiapi.ConfigAdjustment, namespace string) error {
	b := projection{output: output, relations: make(map[string]facts.Relation, len(description.BaseRelations)), snapshot: snapshot, descriptions: make(map[string]string), anchors: make(map[string]struct{})}
	for _, relation := range description.BaseRelations {
		local := strings.TrimPrefix(relation.Predicate.Name, namespace+".")
		handle, err := output.Relation(local, relation.Predicate.Arity)
		if err != nil {
			return err
		}
		b.relations[local] = handle
	}
	for _, fn := range snapshot.Functions {
		text := snapshot.Tracking[fn.SemanticKey].Description
		if strings.HasPrefix(text, "function") {
			text = "signature" + strings.TrimPrefix(text, "function")
		} else if text != "" {
			text = "signature " + text
		} else {
			text = "signature"
		}
		if fn.Signature != nil {
			b.descriptions[lintaiapi.SignatureID(fn.SemanticKey, *fn.Signature, namespace)] = text
		}
	}
	for _, item := range snapshot.Types {
		if item.Description != "" {
			b.descriptions[item.ID] = item.Description
		}
	}
	for _, module := range snapshot.Modules {
		b.output.Add(b.relations["module"], str(module.EntityID))
		b.output.Add(b.relations["module_path"], str(module.EntityID), str(module.Path))
		b.location(module.EntityID, module.Range)
	}
	for _, file := range snapshot.CompilerFiles {
		if !file.Eligible {
			continue
		}
		module := str("module:" + file.Path)
		for _, config := range file.RootConfigs {
			b.output.Add(b.relations["module_config"], module, str(config))
		}
		if file.OwnerConfig != nil {
			b.output.Add(b.relations["module_owner_config"], module, str(*file.OwnerConfig))
		}
	}
	for _, fn := range snapshot.Functions {
		id := str(fn.SemanticKey)
		b.output.Add(b.relations["fn"], id, str(fn.FilePath), str(fn.Name))
		b.output.Add(b.relations["fn_kind"], id, facts.Name(fn.Kind))
		if fn.ContainerName != "" {
			b.output.Add(b.relations["fn_container"], id, str(fn.ContainerName))
		}
		if fn.IsExported {
			b.output.Add(b.relations["fn_exported"], id)
		}
		if fn.IsAsync {
			b.output.Add(b.relations["fn_async"], id)
		}
		if fn.SignatureOmitted {
			b.output.Add(b.relations["fn_signature_omitted"], id)
		}
		if observation := fn.Signature; observation != nil {
			signature := lintaiapi.SignatureID(fn.SemanticKey, *observation, namespace)
			sid := str(signature)
			b.output.Add(b.relations["fn_signature"], id, sid)
			b.output.Add(b.relations["signature_arity"], sid, facts.Number(int64(len(observation.ParameterTypes))))
			for index, typeID := range observation.ParameterTypes {
				if typeID != "" {
					b.output.Add(b.relations["signature_param"], sid, facts.Number(int64(index)), str(typeID))
				}
			}
			if err := b.marks(sid, observation.ParameterMarks, b.relations["signature_param_optional"], b.relations["signature_param_rest"]); err != nil {
				return err
			}
			if observation.ReturnType != "" {
				b.output.Add(b.relations["signature_returns"], sid, str(observation.ReturnType))
			}
			if observation.Complete {
				b.output.Add(b.relations["signature_complete"], sid)
			} else {
				b.output.Add(b.relations["signature_incomplete"], sid, facts.Name("unresolved"))
			}
			b.location(signature, fn.Range)
		}
		b.location(fn.SemanticKey, fn.Range)
	}
	for _, site := range snapshot.Sites {
		id := str(site.ID)
		b.output.Add(b.relations["site"], id, str(site.File))
		if site.UnsupportedReason != "" {
			b.output.Add(b.relations["site_attribution_unsupported"], id, facts.Name(site.UnsupportedReason))
		} else {
			b.output.Add(b.relations["site_owner"], id, str(site.Owner))
		}
		b.location(site.ID, site.Range)
	}
	for _, site := range snapshot.SuspensionSites {
		b.output.Add(b.relations["suspension_site"], str(site.SemanticKey), facts.Name(site.Kind))
	}
	for _, edge := range snapshot.ImportEdges {
		id := str(edge.SemanticKey)
		b.output.Add(b.relations["import_edge"], id, str(edge.FromPath), str(edge.ToPath), str(edge.Specifier))
		form, err := name(edge.Form)
		if err != nil {
			return err
		}
		b.output.Add(b.relations["import_form"], id, form)
		if edge.Internal {
			b.output.Add(b.relations["import_internal"], id)
		}
		if edge.Unresolved {
			b.output.Add(b.relations["import_unresolved"], id)
		}
		if edge.Package != "" {
			b.output.Add(b.relations["import_package"], id, str(edge.Package))
		}
		if edge.IsTypeOnly {
			b.output.Add(b.relations["import_type_only"], id)
		}
		if edge.Retained {
			b.output.Add(b.relations["import_retained"], id)
		}
		for _, symbol := range edge.ImportedSymbols {
			kind, err := name(symbol.Kind)
			if err != nil {
				return err
			}
			b.output.Add(b.relations["import_symbol"], id, str(symbol.Name), kind)
			if symbol.IsTypeOnly {
				b.output.Add(b.relations["import_symbol_type_only"], id, str(symbol.Name))
			}
		}
		b.location(edge.SemanticKey, edge.Range)
	}
	callSites := make(map[string]bool)
	for _, edge := range snapshot.CallEdges {
		callSites[edge.SemanticKey] = true
		b.output.Add(b.relations["call_edge"], str(edge.SemanticKey), str(edge.ToSemanticKey))
	}
	for _, call := range snapshot.ExternalCalls {
		callSites[call.SemanticKey] = true
		b.output.Add(b.relations["external_call"], str(call.SemanticKey), facts.Name(call.OriginKind), str(call.Module), str(call.Name))
	}
	for _, call := range snapshot.CallsUnresolved {
		callSites[call.SemanticKey] = true
		b.output.Add(b.relations["call_unresolved"], str(call.SemanticKey))
	}
	for site := range callSites {
		b.output.Add(b.relations["call_site"], str(site))
	}
	for _, escape := range snapshot.FunctionEscapes {
		b.output.Add(b.relations["fn_value_escape"], str(escape.Site), facts.Name("argument"))
		if escape.Target != "" {
			b.output.Add(b.relations["fn_value_escape_target"], str(escape.Site), str(escape.Target))
		}
		if escape.Unknown {
			b.output.Add(b.relations["fn_value_escape_unknown"], str(escape.Site))
		}
	}
	for _, item := range snapshot.CallDeclarations {
		b.output.Add(b.relations["call_declaration"], str(item.Site), str(item.Declaration))
	}
	for _, item := range snapshot.DeclarationMembers {
		b.output.Add(b.relations["declaration_member"], str(item.Declaration), str(item.OwnerDeclaration), str(item.Name))
	}
	for _, item := range snapshot.CallDeclarationIncomplete {
		reason, err := name(item.Reason)
		if err != nil {
			return err
		}
		b.output.Add(b.relations["call_declaration_incomplete"], str(item.Site), reason)
	}
	for _, item := range snapshot.ImportsOpaque {
		b.output.Add(b.relations["import_opaque"], str(item.SemanticKey))
	}
	for _, ref := range snapshot.FnRefs {
		b.output.Add(b.relations["fn_ref"], str(ref.SemanticKey), str(ref.ToSemanticKey))
	}
	for _, directive := range snapshot.Directives {
		b.output.Add(b.relations["module_directive"], str(directive.SemanticKey), str(directive.Text))
	}
	for _, parseError := range snapshot.ParseErrors {
		b.output.Add(b.relations["module_parse_error"], str(parseError.SemanticKey), str(parseError.Message))
	}
	for _, adjustment := range adjustments {
		action, err := name(adjustment.Action)
		if err != nil {
			return err
		}
		b.output.Add(b.relations["config_adjusted"], str(adjustment.Config), str(adjustment.Option), action)
	}
	for _, adjustment := range snapshot.SourceConfigAdjustments {
		action, err := name(adjustment.Action)
		if err != nil {
			return err
		}
		b.output.Add(b.relations["source_config_adjusted"], str(adjustment.Config), str(adjustment.Option), action)
	}
	for _, ref := range snapshot.TypeRefs {
		id := str(ref.SemanticKey)
		b.output.Add(b.relations["type_ref"], id, str(ref.Name))
		if ref.TargetPath != "" {
			b.output.Add(b.relations["type_ref_target"], id, str(ref.TargetPath))
		}
	}
	for _, item := range snapshot.Types {
		if err := b.typeFacts(item); err != nil {
			return err
		}
	}
	for _, item := range snapshot.Declarations {
		id := str(item.ID)
		kind, err := name(item.Kind)
		if err != nil {
			return err
		}
		b.output.Add(b.relations["declaration"], id, str(item.Name), kind)
		if item.OriginKind != "" {
			originKind, err := name(item.OriginKind)
			if err != nil {
				return err
			}
			b.output.Add(b.relations["declaration_origin"], id, originKind, str(item.Origin))
		}
		for _, space := range item.TypeSubjects {
			b.output.Add(b.relations["declaration_type_subject"], id, facts.Name(space))
		}
		for _, value := range item.Types {
			space, err := name(value.Space)
			if err != nil {
				return err
			}
			b.output.Add(b.relations["declaration_type"], id, space, str(value.Type))
		}
		if item.Function != "" {
			b.output.Add(b.relations["declaration_fn"], id, str(item.Function))
		}
		for _, value := range item.TypeIncomplete {
			b.output.Add(b.relations["declaration_type_incomplete"], id, facts.Name(value.Space), facts.Name(value.Reason))
		}
		for _, reason := range item.Incomplete {
			value, err := name(reason)
			if err != nil {
				return err
			}
			b.output.Add(b.relations["declaration_metadata_incomplete"], id, value)
		}
		b.location(item.ID, item.Location)
	}
	for _, item := range snapshot.Exports {
		id := str(item.ID)
		space, err := name(item.Space)
		if err != nil {
			return err
		}
		b.output.Add(b.relations["module_export"], id, str("module:"+item.File), str(item.Name), space)
		for _, target := range item.Targets {
			b.output.Add(b.relations["export_target"], id, str(target))
		}
		for _, site := range item.Via {
			b.output.Add(b.relations["export_via"], id, str(site))
		}
		for _, value := range item.Reexports {
			reexportSpace, err := name(value.Space)
			if err != nil {
				return err
			}
			b.output.Add(b.relations["export_reexport"], id, str(value.Site), str(value.Module), str(value.SourceName), reexportSpace)
		}
		for _, reason := range item.Incomplete {
			value, err := name(reason)
			if err != nil {
				return err
			}
			b.output.Add(b.relations["export_incomplete"], id, value)
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
			return err
		}
		b.output.Add(b.relations["module_exports_incomplete"], str("module:"+item.File), reason)
	}
	for _, item := range snapshot.DeclarationSourceCandidates {
		method, err := name(item.Method)
		if err != nil {
			return err
		}
		b.output.Add(b.relations["declaration_source_candidate"], str(item.CheckedDecl), str(item.SourceDecl), method)
	}
	for _, item := range snapshot.DeclarationSources {
		method, err := name(item.Method)
		if err != nil {
			return err
		}
		b.output.Add(b.relations["declaration_source"], str(item.CheckedDecl), str(item.SourceDecl), method)
	}
	for _, item := range snapshot.DeclarationSourceOutcomes {
		outcome, err := name(item.Outcome)
		if err != nil {
			return err
		}
		b.output.Add(b.relations["declaration_source_outcome"], str(item.CheckedDecl), outcome)
	}
	for _, item := range snapshot.SourceEndpoints {
		kind, err := name(item.Kind)
		if err != nil {
			return err
		}
		originKind, err := name(item.OriginKind)
		if err != nil {
			return err
		}
		b.output.Add(b.relations["source_endpoint"], str(item.ID), str(item.Name), kind, originKind, str(item.Origin))
		for _, reason := range item.Incomplete {
			value, err := name(reason)
			if err != nil {
				return err
			}
			b.output.Add(b.relations["source_endpoint_incomplete"], str(item.ID), value)
		}
	}
	for _, item := range snapshot.SourceEntries {
		id := str(item.ID)
		b.output.Add(b.relations["source_entry"], id, str(item.CheckedEntry), str(item.SourceEntry))
		if item.Tsconfig != "" {
			b.output.Add(b.relations["source_entry_tsconfig"], id, str(item.Tsconfig))
		}
		if item.Applied {
			b.output.Add(b.relations["source_entry_applied"], id)
		} else {
			b.output.Add(b.relations["source_entry_unapplied"], id)
		}
		for _, reason := range item.Incomplete {
			value, err := name(reason)
			if err != nil {
				return err
			}
			b.output.Add(b.relations["source_entry_incomplete"], id, value)
		}
	}
	for _, item := range snapshot.SourceBindings {
		id := str(item.ID)
		space, err := name(item.Space)
		if err != nil {
			return err
		}
		b.output.Add(b.relations["source_binding_attempt"], id, str(item.Entry), str(item.CheckedFile), str(item.Name), space)
		if item.SourceFile != "" {
			b.output.Add(b.relations["source_binding"], id, str(item.SourceFile), str(item.Name), space, facts.Name("source_entry"))
		}
		for _, declaration := range item.CheckedDeclarations {
			b.output.Add(b.relations["source_binding_checked_decl"], id, str(declaration))
		}
		for _, declaration := range item.SourceDeclarations {
			b.output.Add(b.relations["source_binding_source_decl"], id, str(declaration))
		}
		for _, reason := range item.Incomplete {
			value, err := name(reason)
			if err != nil {
				return err
			}
			b.output.Add(b.relations["source_binding_incomplete"], id, value)
		}
	}
	for _, item := range snapshot.ExportSites {
		b.location(item.ID, item.Location)
	}
	for _, access := range snapshot.Accesses {
		b.output.Add(b.relations["meta_access"], str(access.SemanticKey), str(access.AccessPath))
	}
	for _, source := range sources {
		sourceName, err := name(source.ID)
		if err != nil {
			return err
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
				return err
			}
			b.output.Add(b.relations["ambient_source"], sourceName, capabilityName)
		}
	}
	for _, use := range snapshot.AmbientUses {
		id := str(use.SemanticKey)
		sourceName, err := name(use.Source)
		if err != nil {
			return err
		}
		capabilityName, err := name(use.Capability)
		if err != nil {
			return err
		}
		b.output.Add(b.relations["ambient_use"], id, sourceName, capabilityName)
		if use.HasKey {
			b.output.Add(b.relations["ambient_key"], id, str(use.Key))
		}
		if use.Dynamic {
			b.output.Add(b.relations["ambient_dynamic"], id)
		}
		if use.Write {
			b.output.Add(b.relations["ambient_write"], id)
		}

	}
	for _, ref := range snapshot.GlobalRefs {
		b.output.Add(b.relations["global_ref"], str(ref.SemanticKey), str(ref.Name))
	}

	for _, endpoint := range snapshot.SourceEndpoints {
		location := endpoint.Location
		if _, exists := b.anchors[endpoint.ID]; exists || location.File == "" || strings.HasPrefix(location.File, "bundled://") || location.StartLine <= 0 || location.StartColumn <= 0 || location.EndLine <= 0 || location.EndColumn <= 0 {
			continue
		}
		support := snapshot.SourceTracking[endpoint.ID]
		text := support.Description
		if text == "" {
			text = "TypeScript declaration"
		}
		anchor := facts.AnchorSupport{Location: diagnostics.SourceLocation{File: location.File, StartLine: location.StartLine, StartColumn: location.StartColumn, EndLine: location.EndLine, EndColumn: location.EndColumn}, Description: text}
		if support.Data != nil {
			anchor.Tracking = &facts.TrackingKey{Scheme: "lintai.typescript.subject/1", Data: support.Data}
		}
		output.Anchor(endpoint.ID, anchor)
		b.anchors[endpoint.ID] = struct{}{}
	}
	return ctx.Err()
}

func (b *projection) typeFacts(item lintaiapi.TypeFact) error {
	id := str(item.ID)
	kind, err := name(item.Kind)
	if err != nil {
		return err
	}
	b.output.Add(b.relations["type_kind"], id, kind)
	if item.Named {
		b.output.Add(b.relations["type_named"], id, str(item.Origin), str(item.Name))
		if item.OriginKind != "" {
			originKind, err := name(item.OriginKind)
			if err != nil {
				return err
			}
			b.output.Add(b.relations["type_origin_kind"], id, originKind)
		}
	}
	for index, arg := range item.Args {
		b.output.Add(b.relations["type_arg"], id, facts.Number(int64(index)), str(arg))
	}
	if err := b.marks(id, item.ArgMarks, b.relations["type_arg_optional"], b.relations["type_arg_rest"]); err != nil {
		return err
	}
	if item.Readonly {
		b.output.Add(b.relations["type_readonly"], id)
	}
	for _, member := range item.Members {
		b.output.Add(b.relations["type_member"], id, str(member))
	}
	if item.Literal != nil {
		value, err := literalValue(*item.Literal)
		if err != nil {
			return err
		}
		b.output.Add(b.relations["type_literal"], id, facts.Name(item.Literal.Kind), value)
	}
	if item.Kind == "type_param" {
		b.output.Add(b.relations["type_param_name"], id, str(item.ParamName))
	}
	for _, prop := range item.Props {
		b.output.Add(b.relations["type_prop"], id, str(prop.Name), str(prop.Type))
		if prop.Optional {
			b.output.Add(b.relations["type_prop_optional"], id, str(prop.Name))
		}
		if prop.Readonly {
			b.output.Add(b.relations["type_prop_readonly"], id, str(prop.Name))
		}
	}
	if item.Kind == "function" {
		for index, param := range item.SigParams {
			b.output.Add(b.relations["type_sig_param"], id, facts.Number(int64(index)), str(param))
		}
		if err := b.marks(id, item.SigMarks, b.relations["type_sig_param_optional"], b.relations["type_sig_param_rest"]); err != nil {
			return err
		}
		b.output.Add(b.relations["type_sig_returns"], id, str(item.SigReturns))
	}
	for _, reason := range item.Incomplete {
		value, err := name(reason)
		if err != nil {
			return err
		}
		b.output.Add(b.relations["type_incomplete"], id, value)
	}
	b.output.Add(b.relations["type_display"], id, str(item.Display))
	if item.Location != nil {
		b.location(item.ID, *item.Location)
	}
	return nil
}

// marks adds a row of optional or rest for each marked tuple element or
// signature parameter.
func (b *projection) marks(id facts.Value, marks []string, optional, rest facts.Relation) error {
	for index, mark := range marks {
		switch mark {
		case "":
		case "optional":
			b.output.Add(optional, id, facts.Number(int64(index)))
		case "rest":
			b.output.Add(rest, id, facts.Number(int64(index)))
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

type projection struct {
	output       *facts.Builder
	relations    map[string]facts.Relation
	snapshot     *lintaiapi.Snapshot
	descriptions map[string]string
	anchors      map[string]struct{}
}

func (b *projection) location(entity string, location lintaiapi.SourceLocation) {
	if location.File == "" || strings.HasPrefix(location.File, "bundled://") || location.StartLine <= 0 || location.StartColumn <= 0 || location.EndLine <= 0 || location.EndColumn <= 0 {
		return
	}
	b.output.Add(b.relations["location"], str(entity), str(location.File), facts.Number(int64(location.StartLine)), facts.Number(int64(location.StartColumn)), facts.Number(int64(location.EndLine)), facts.Number(int64(location.EndColumn)))
	support := b.snapshot.Tracking[entity]
	description := support.Description
	if description == "" {
		description = b.descriptions[entity]
	}
	if description == "" {
		description = "TypeScript " + strings.SplitN(entity, ":", 2)[0]
	}
	anchor := facts.AnchorSupport{Location: diagnostics.SourceLocation{File: location.File, StartLine: location.StartLine, StartColumn: location.StartColumn, EndLine: location.EndLine, EndColumn: location.EndColumn}, Description: description}
	if support.Data != nil {
		anchor.Tracking = &facts.TrackingKey{Scheme: "lintai.typescript.subject/1", Data: support.Data}
	}
	b.output.Anchor(entity, anchor)
	b.anchors[entity] = struct{}{}
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
