package query

import (
	"fmt"
	"path/filepath"
	"strings"

	"github.com/bigbizze/lintai/internal/analysis"
	"github.com/bigbizze/lintai/internal/backend"
	"github.com/bigbizze/lintai/internal/diagnostics"
	"github.com/bmatcuk/doublestar/v4"
	"github.com/dop251/goja"
)

type Evaluator struct {
	runtime      *goja.Runtime
	snapshot     *analysis.Snapshot
	capabilities backend.CapabilityManifest
}

func NewEvaluator(runtime *goja.Runtime, snapshot *analysis.Snapshot, capabilities backend.CapabilityManifest) *Evaluator {
	return &Evaluator{
		runtime:      runtime,
		snapshot:     snapshot,
		capabilities: capabilities,
	}
}

func (e *Evaluator) EvaluateAssertion(assertion Assertion) ([]any, error) {
	if assertion.Query == nil {
		return nil, fmt.Errorf("assertion %s is missing a query plan", assertion.AssertionID)
	}
	results, err := e.Resolve(*assertion.Query)
	if err != nil {
		return nil, err
	}
	if assertion.Terminal != "isEmpty" {
		return nil, fmt.Errorf("unsupported terminal %q", assertion.Terminal)
	}
	return results, nil
}

func (e *Evaluator) Resolve(plan Plan) ([]any, error) {
	if !e.capabilities.SupportsQueryKind(plan.Entity) {
		return nil, fmt.Errorf("unsupported capability: query kind %q", plan.Entity)
	}
	switch plan.Entity {
	case "functions":
		return e.resolveFunctions(plan)
	case "imports":
		return e.resolveImports(plan)
	case "calls":
		return e.resolveCalls(plan)
	case "typeRefs":
		return e.resolveTypeRefs(plan)
	default:
		return nil, fmt.Errorf("unsupported query kind %q", plan.Entity)
	}
}

func (e *Evaluator) resolveFunctions(plan Plan) ([]any, error) {
	results := make([]analysis.Function, 0, len(e.snapshot.Functions))
	results = append(results, e.snapshot.Functions...)
	for _, op := range plan.Ops {
		switch op.Type {
		case "in":
			filtered := make([]analysis.Function, 0, len(results))
			for _, fn := range results {
				match, err := matchPattern(op.Value, fn.FilePath)
				if err != nil {
					return nil, err
				}
				if match {
					filtered = append(filtered, fn)
				}
			}
			results = filtered
		case "where":
			filtered, err := filterWhere(e.runtime, results, functionView, op.Handler)
			if err != nil {
				return nil, err
			}
			results = filtered
		case "calling":
			targets, err := e.resolveFunctions(*op.Query)
			if err != nil {
				return nil, err
			}
			targetSet := toFunctionKeySet(targets)
			filtered := make([]analysis.Function, 0, len(results))
			for _, fn := range results {
				if e.callsTarget(fn.SemanticKey, targetSet, false) {
					filtered = append(filtered, fn)
				}
			}
			results = filtered
		case "transitivelyCalling":
			targets, err := e.resolveFunctions(*op.Query)
			if err != nil {
				return nil, err
			}
			targetSet := toFunctionKeySet(targets)
			filtered := make([]analysis.Function, 0, len(results))
			for _, fn := range results {
				if e.callsTarget(fn.SemanticKey, targetSet, true) {
					filtered = append(filtered, fn)
				}
			}
			results = filtered
		default:
			return nil, fmt.Errorf("unsupported operator %q for functions()", op.Type)
		}
	}
	final := make([]any, 0, len(results))
	for _, item := range results {
		final = append(final, item)
	}
	return final, nil
}

func (e *Evaluator) resolveImports(plan Plan) ([]any, error) {
	results := make([]analysis.ImportEdge, 0, len(e.snapshot.ImportEdges))
	results = append(results, e.snapshot.ImportEdges...)
	for _, op := range plan.Ops {
		switch op.Type {
		case "from":
			filtered := make([]analysis.ImportEdge, 0, len(results))
			for _, edge := range results {
				match, err := matchPattern(op.Value, edge.FromPath)
				if err != nil {
					return nil, err
				}
				if match {
					filtered = append(filtered, edge)
				}
			}
			results = filtered
		case "to":
			filtered := make([]analysis.ImportEdge, 0, len(results))
			for _, edge := range results {
				match, err := matchPattern(op.Value, edge.ToPath)
				if err != nil {
					return nil, err
				}
				if match {
					filtered = append(filtered, edge)
				}
			}
			results = filtered
		case "in":
			filtered := make([]analysis.ImportEdge, 0, len(results))
			for _, edge := range results {
				match, err := matchPattern(op.Value, edge.FromPath)
				if err != nil {
					return nil, err
				}
				if match {
					filtered = append(filtered, edge)
				}
			}
			results = filtered
		case "where":
			filtered, err := filterWhere(e.runtime, results, importEdgeView, op.Handler)
			if err != nil {
				return nil, err
			}
			results = filtered
		default:
			return nil, fmt.Errorf("unsupported operator %q for imports()", op.Type)
		}
	}
	final := make([]any, 0, len(results))
	for _, item := range results {
		final = append(final, item)
	}
	return final, nil
}

func (e *Evaluator) resolveCalls(plan Plan) ([]any, error) {
	results := make([]analysis.CallEdge, 0, len(e.snapshot.CallEdges))
	results = append(results, e.snapshot.CallEdges...)
	for _, op := range plan.Ops {
		switch op.Type {
		case "from":
			filtered := make([]analysis.CallEdge, 0, len(results))
			for _, edge := range results {
				match, err := matchPattern(op.Value, edge.FromPath)
				if err != nil {
					return nil, err
				}
				if match {
					filtered = append(filtered, edge)
				}
			}
			results = filtered
		case "to":
			filtered := make([]analysis.CallEdge, 0, len(results))
			for _, edge := range results {
				match, err := matchPattern(op.Value, edge.ToPath)
				if err != nil {
					return nil, err
				}
				if match {
					filtered = append(filtered, edge)
				}
			}
			results = filtered
		case "where":
			filtered, err := filterWhere(e.runtime, results, callEdgeView, op.Handler)
			if err != nil {
				return nil, err
			}
			results = filtered
		default:
			return nil, fmt.Errorf("unsupported operator %q for calls()", op.Type)
		}
	}
	final := make([]any, 0, len(results))
	for _, item := range results {
		final = append(final, item)
	}
	return final, nil
}

func (e *Evaluator) resolveTypeRefs(plan Plan) ([]any, error) {
	results := make([]analysis.TypeRef, 0, len(e.snapshot.TypeRefs))
	results = append(results, e.snapshot.TypeRefs...)
	for _, op := range plan.Ops {
		switch op.Type {
		case "in":
			filtered := make([]analysis.TypeRef, 0, len(results))
			for _, ref := range results {
				match, err := matchPattern(op.Value, ref.FilePath)
				if err != nil {
					return nil, err
				}
				if match {
					filtered = append(filtered, ref)
				}
			}
			results = filtered
		case "where":
			filtered, err := filterWhere(e.runtime, results, typeRefView, op.Handler)
			if err != nil {
				return nil, err
			}
			results = filtered
		default:
			return nil, fmt.Errorf("unsupported operator %q for typeRefs()", op.Type)
		}
	}
	final := make([]any, 0, len(results))
	for _, item := range results {
		final = append(final, item)
	}
	return final, nil
}

func functionView(fn analysis.Function) map[string]any {
	return map[string]any{
		"name":               fn.Name,
		"kind":               fn.Kind,
		"filePath":           fn.FilePath,
		"containerName":      fn.ContainerName,
		"semanticKey":        fn.SemanticKey,
		"containsAwait":      fn.ContainsAwait,
		"isExported":         fn.IsExported,
		"isAsync":            fn.IsAsync,
		"parameterCount":     fn.ParameterCount,
		"returnTypeText":     fn.ReturnTypeText,
		"parameterTypeTexts": append([]string(nil), fn.ParameterTypeTexts...),
		"sourceLocation":     sourceLocationView(fn.Range),
	}
}

func importEdgeView(edge analysis.ImportEdge) map[string]any {
	importedSymbols := make([]map[string]any, 0, len(edge.ImportedSymbols))
	for _, symbol := range edge.ImportedSymbols {
		importedSymbols = append(importedSymbols, map[string]any{
			"name":       symbol.Name,
			"kind":       symbol.Kind,
			"isTypeOnly": symbol.IsTypeOnly,
		})
	}
	return map[string]any{
		"specifier":          edge.Specifier,
		"fromPath":           edge.FromPath,
		"toPath":             edge.ToPath,
		"semanticKey":        edge.SemanticKey,
		"importedSymbols":    importedSymbols,
		"hasDefaultImport":   edge.HasDefaultImport,
		"hasNamespaceImport": edge.HasNamespaceImport,
		"hasNamedImports":    edge.HasNamedImports,
		"isTypeOnly":         edge.IsTypeOnly,
		"sourceLocation":     sourceLocationView(edge.Range),
	}
}

func callEdgeView(edge analysis.CallEdge) map[string]any {
	return map[string]any{
		"fromName":       edge.FromName,
		"toName":         edge.ToName,
		"fromPath":       edge.FromPath,
		"toPath":         edge.ToPath,
		"semanticKey":    edge.SemanticKey,
		"sourceLocation": sourceLocationView(edge.Range),
	}
}

func typeRefView(ref analysis.TypeRef) map[string]any {
	return map[string]any{
		"name":           ref.Name,
		"filePath":       ref.FilePath,
		"targetPath":     ref.TargetPath,
		"semanticKey":    ref.SemanticKey,
		"sourceLocation": sourceLocationView(ref.Range),
	}
}

func sourceLocationView(location diagnostics.SourceLocation) map[string]any {
	return map[string]any{
		"file":        location.File,
		"startLine":   location.StartLine,
		"startColumn": location.StartColumn,
		"endLine":     location.EndLine,
		"endColumn":   location.EndColumn,
	}
}

func DiagnosticLocation(entity any) *diagnostics.SourceLocation {
	switch typed := entity.(type) {
	case analysis.Function:
		location := typed.Range
		return &location
	case analysis.ImportEdge:
		location := typed.Range
		return &location
	case analysis.CallEdge:
		location := typed.Range
		return &location
	case analysis.TypeRef:
		location := typed.Range
		return &location
	default:
		return nil
	}
}

func DiagnosticIdentity(entity any) *diagnostics.EntityIdentity {
	switch typed := entity.(type) {
	case analysis.Function:
		return &diagnostics.EntityIdentity{SemanticKey: typed.SemanticKey}
	case analysis.ImportEdge:
		return &diagnostics.EntityIdentity{SemanticKey: typed.SemanticKey}
	case analysis.CallEdge:
		return &diagnostics.EntityIdentity{SemanticKey: typed.SemanticKey}
	case analysis.TypeRef:
		return &diagnostics.EntityIdentity{SemanticKey: typed.SemanticKey}
	default:
		return nil
	}
}

func EntityView(entity any) map[string]any {
	switch typed := entity.(type) {
	case analysis.Function:
		return functionView(typed)
	case analysis.ImportEdge:
		return importEdgeView(typed)
	case analysis.CallEdge:
		return callEdgeView(typed)
	case analysis.TypeRef:
		return typeRefView(typed)
	default:
		return map[string]any{}
	}
}

func filterWhere[T any](runtime *goja.Runtime, items []T, view func(T) map[string]any, handlerValue goja.Value) ([]T, error) {
	handler, ok := goja.AssertFunction(handlerValue)
	if !ok {
		return nil, fmt.Errorf(".where() received a non-function predicate")
	}
	filtered := make([]T, 0, len(items))
	for _, item := range items {
		value, err := handler(goja.Undefined(), runtime.ToValue(view(item)))
		if err != nil {
			return nil, err
		}
		if value.ToBoolean() {
			filtered = append(filtered, item)
		}
	}
	return filtered, nil
}

func toFunctionKeySet(values []any) map[string]struct{} {
	result := make(map[string]struct{}, len(values))
	for _, item := range values {
		if fn, ok := item.(analysis.Function); ok {
			result[fn.SemanticKey] = struct{}{}
		}
	}
	return result
}

func (e *Evaluator) callsTarget(sourceKey string, targets map[string]struct{}, transitive bool) bool {
	if transitive {
		for target := range targets {
			if _, exists := e.snapshot.TransitiveCalls[sourceKey][target]; exists {
				return true
			}
		}
		return false
	}
	for _, edge := range e.snapshot.CallEdges {
		if edge.FromSemanticKey != sourceKey {
			continue
		}
		if _, exists := targets[edge.ToSemanticKey]; exists {
			return true
		}
	}
	return false
}

func matchPattern(pattern, path string) (bool, error) {
	pattern = strings.TrimPrefix(filepath.ToSlash(pattern), "/")
	path = strings.TrimPrefix(filepath.ToSlash(path), "/")
	return doublestar.Match(pattern, path)
}
