package query

import (
	"fmt"
	"path/filepath"
	"strings"

	"github.com/bigbizze/lintai/internal/analysis"
	"github.com/bigbizze/lintai/internal/backend"
	"github.com/bigbizze/lintai/internal/diagnostics"
	"github.com/dop251/goja"
	"github.com/bmatcuk/doublestar/v4"
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
			filtered := make([]analysis.Function, 0, len(results))
			handler, ok := goja.AssertFunction(op.Handler)
			if !ok {
				return nil, fmt.Errorf(".where() received a non-function predicate")
			}
			for _, fn := range results {
				value, err := handler(goja.Undefined(), e.runtime.ToValue(functionView(fn)))
				if err != nil {
					return nil, err
				}
				if value.ToBoolean() {
					filtered = append(filtered, fn)
				}
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

func functionView(fn analysis.Function) map[string]any {
	return map[string]any{
		"name":          fn.Name,
		"kind":          fn.Kind,
		"filePath":      fn.FilePath,
		"semanticKey":   fn.SemanticKey,
		"containsAwait": fn.ContainsAwait,
		"sourceLocation": map[string]any{
			"file":        fn.Range.File,
			"startLine":   fn.Range.StartLine,
			"startColumn": fn.Range.StartColumn,
			"endLine":     fn.Range.EndLine,
			"endColumn":   fn.Range.EndColumn,
		},
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
	default:
		return nil
	}
}

func EntityView(entity any) map[string]any {
	switch typed := entity.(type) {
	case analysis.Function:
		return functionView(typed)
	case analysis.ImportEdge:
		return map[string]any{
			"specifier":   typed.Specifier,
			"fromPath":    typed.FromPath,
			"toPath":      typed.ToPath,
			"semanticKey": typed.SemanticKey,
			"sourceLocation": map[string]any{
				"file":        typed.Range.File,
				"startLine":   typed.Range.StartLine,
				"startColumn": typed.Range.StartColumn,
				"endLine":     typed.Range.EndLine,
				"endColumn":   typed.Range.EndColumn,
			},
		}
	case analysis.CallEdge:
		return map[string]any{
			"fromName":    typed.FromName,
			"toName":      typed.ToName,
			"fromPath":    typed.FromPath,
			"toPath":      typed.ToPath,
			"semanticKey": typed.SemanticKey,
			"sourceLocation": map[string]any{
				"file":        typed.Range.File,
				"startLine":   typed.Range.StartLine,
				"startColumn": typed.Range.StartColumn,
				"endLine":     typed.Range.EndLine,
				"endColumn":   typed.Range.EndColumn,
			},
		}
	default:
		return map[string]any{}
	}
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
	pattern = filepath.ToSlash(pattern)
	path = filepath.ToSlash(path)
	match, err := doublestar.Match(pattern, path)
	if err != nil {
		return false, err
	}
	if match {
		return true, nil
	}
	if !strings.Contains(pattern, "**") && strings.HasSuffix(pattern, "/**") {
		return strings.HasPrefix(path, strings.TrimSuffix(pattern, "/**")), nil
	}
	return false, nil
}
