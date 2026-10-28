package typescript

import (
	_ "embed"
	"fmt"

	"github.com/bigbizze/lintai/diagnostics"
	"github.com/bigbizze/lintai/facts"
	"github.com/bigbizze/lintai/internal/analysis"
)

//go:embed schema.mg
var schemaText string

//go:embed prelude.mg
var preludeText string

// Schema returns the declarations and prelude owned by this backend.
func Schema() facts.Schema {
	return facts.Schema{Declarations: schemaText, Prelude: preludeText}
}

// factsFromAnalysis converts the TypeScript-specific analysis model into the
// language-neutral backend contract.
func factsFromAnalysis(snapshot *analysis.Snapshot) ([]facts.Fact, error) {
	b := builder{}
	for _, module := range snapshot.Modules {
		b.add("module", str(module.Path))
		b.location(module.Path, module.Range)
	}
	for _, fn := range snapshot.Functions {
		id := str(fn.SemanticKey)
		b.add("fn", id, str(fn.FilePath), str(fn.Name))
		b.add("fn_kind", id, str(fn.Kind))
		if fn.ContainerName != "" {
			b.add("fn_container", id, str(fn.ContainerName))
		}
		if fn.IsExported {
			b.add("fn_exported", id)
		}
		if fn.IsAsync {
			b.add("fn_async", id)
		}
		if fn.ContainsAwait {
			b.add("fn_awaits", id)
		}
		for index, typeText := range fn.ParameterTypeTexts {
			b.add("fn_param", id, facts.Number(int64(index)), str(typeText))
		}
		b.add("fn_returns", id, str(fn.ReturnTypeText))
		b.location(fn.SemanticKey, fn.Range)
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
	for _, edge := range snapshot.CallEdges {
		id := str(edge.SemanticKey)
		b.add("call_edge", id, str(edge.FromSemanticKey), str(edge.ToSemanticKey))
		if !edge.FromModule {
			b.add("site_in_fn", id, str(edge.FromSemanticKey))
		}
		b.location(edge.SemanticKey, edge.Range)
	}
	for _, call := range snapshot.ExternalCalls {
		id := str(call.SemanticKey)
		b.add("external_call", id, str(call.FromSemanticKey), str(call.Module), str(call.Name))
		if !call.FromModule {
			b.add("site_in_fn", id, str(call.FromSemanticKey))
		}
		b.location(call.SemanticKey, call.Range)
	}
	for _, directive := range snapshot.Directives {
		b.add("module_directive", str(directive.SemanticKey), str(directive.FilePath), str(directive.Text))
		b.location(directive.SemanticKey, directive.Range)
	}
	for _, parseError := range snapshot.ParseErrors {
		b.add("module_parse_error", str(parseError.SemanticKey), str(parseError.FilePath), str(parseError.Message))
		b.location(parseError.SemanticKey, parseError.Range)
	}
	for _, ref := range snapshot.TypeRefs {
		id := str(ref.SemanticKey)
		b.add("type_ref", id, str(ref.FilePath), str(ref.Name))
		if ref.TargetPath != "" {
			b.add("type_ref_target", id, str(ref.TargetPath))
		}
		b.location(ref.SemanticKey, ref.Range)
	}
	bodies := functionBodiesByFile(snapshot.Functions)
	for _, access := range snapshot.Accesses {
		id := str(access.SemanticKey)
		b.add("meta_access", id, str(access.FilePath), str(access.AccessPath))
		if fn := innermostFunction(bodies[access.FilePath], access.Start, access.End); fn != "" {
			b.add("site_in_fn", id, str(fn))
		}
		b.location(access.SemanticKey, access.Range)
	}
	for _, source := range snapshot.AmbientSources {
		sourceName, err := name(source.ID)
		if err != nil {
			return nil, err
		}
		for _, capability := range source.Capabilities {
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
		b.add("ambient_use", id, sourceName, capabilityName, str(use.FilePath))
		if use.HasKey {
			b.add("ambient_key", id, str(use.Key))
		}
		if use.Dynamic {
			b.add("ambient_dynamic", id)
		}
		if use.Write {
			b.add("ambient_write", id)
		}
		if use.FunctionSemanticKey != "" {
			b.add("site_in_fn", id, str(use.FunctionSemanticKey))
		}
		b.location(use.SemanticKey, use.Range)
	}
	return b.items, nil
}

type builder struct {
	items []facts.Fact
}

func (b *builder) add(predicate string, args ...facts.Value) {
	b.items = append(b.items, facts.Fact{Relation: predicate, Values: args})
}

func (b *builder) location(entity string, location diagnostics.SourceLocation) {
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

type functionBody struct {
	key   string
	start int
	end   int
}

func functionBodiesByFile(functions []analysis.Function) map[string][]functionBody {
	result := make(map[string][]functionBody)
	for _, fn := range functions {
		if fn.BodyEnd <= fn.BodyStart {
			continue
		}
		result[fn.FilePath] = append(result[fn.FilePath], functionBody{key: fn.SemanticKey, start: fn.BodyStart, end: fn.BodyEnd})
	}
	return result
}

func innermostFunction(bodies []functionBody, start, end int) string {
	best := ""
	bestSize := -1
	for _, body := range bodies {
		if start < body.start || end > body.end {
			continue
		}
		size := body.end - body.start
		if bestSize == -1 || size < bestSize {
			best = body.key
			bestSize = size
		}
	}
	return best
}
