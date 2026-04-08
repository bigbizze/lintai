package typescript

import (
	"context"
	"crypto/sha1"
	"encoding/hex"
	"sort"

	"github.com/bigbizze/lintai/internal/analysis"
	"github.com/bigbizze/lintai/internal/backend"
	"github.com/bigbizze/lintai/internal/diagnostics"
	"github.com/bigbizze/lintai/internal/workspace"
	"github.com/microsoft/typescript-go/lintaiapi"
)

type Backend struct{}

func New() *Backend {
	return &Backend{}
}

func (b *Backend) ID() string {
	return "typescript"
}

func (b *Backend) Capabilities() backend.CapabilityManifest {
	return backend.CapabilityManifest{
		EntityKinds: []string{"module", "function", "import_edge", "call_edge", "type_ref"},
		QueryKinds:  []string{"functions", "imports", "calls"},
		Operators: []string{
			"in", "from", "to", "where", "calling", "transitivelyCalling", "isEmpty",
		},
	}
}

func (b *Backend) BuildSnapshot(ctx context.Context, repoRoot, workspaceRoot string) (*analysis.Snapshot, error) {
	_ = repoRoot
	files, err := workspace.ListSourceFiles(workspaceRoot)
	if err != nil {
		return nil, err
	}
	extracted, err := lintaiapi.BuildSnapshot(ctx, lintaiapi.BuildSnapshotRequest{
		WorkspaceRoot: workspaceRoot,
		Files:         files,
	})
	if err != nil {
		return nil, err
	}

	modules := convertModules(extracted.Modules)
	functions := convertFunctions(extracted.Functions)
	importEdges := convertImportEdges(extracted.ImportEdges)
	callEdges := convertCallEdges(extracted.CallEdges)
	typeRefs := convertTypeRefs(extracted.TypeRefs)

	functionsByName := make(map[string][]analysis.Function)
	functionsByKey := make(map[string]analysis.Function)
	for _, fn := range functions {
		functionsByName[fn.Name] = append(functionsByName[fn.Name], fn)
		functionsByKey[fn.SemanticKey] = fn
	}

	version, err := snapshotVersion(workspaceRoot, files)
	if err != nil {
		return nil, err
	}

	return &analysis.Snapshot{
		Version:         version,
		BackendID:       b.ID(),
		WorkspaceRoot:   workspaceRoot,
		Modules:         modules,
		Functions:       functions,
		ImportEdges:     importEdges,
		CallEdges:       callEdges,
		TypeRefs:        typeRefs,
		FunctionsByName: functionsByName,
		FunctionsByKey:  functionsByKey,
		TransitiveCalls: buildTransitiveCalls(functions, callEdges),
	}, nil
}

func convertModules(items []lintaiapi.Module) []analysis.Module {
	result := make([]analysis.Module, 0, len(items))
	for _, item := range items {
		result = append(result, analysis.Module{
			EntityID:    item.EntityID,
			SemanticKey: item.SemanticKey,
			Path:        item.Path,
			Range:       convertLocation(item.Range),
		})
	}
	return result
}

func convertFunctions(items []lintaiapi.Function) []analysis.Function {
	result := make([]analysis.Function, 0, len(items))
	for _, item := range items {
		result = append(result, analysis.Function{
			EntityID:      item.EntityID,
			SemanticKey:   item.SemanticKey,
			Name:          item.Name,
			Kind:          item.Kind,
			FilePath:      item.FilePath,
			ContainerName: item.ContainerName,
			ContainsAwait: item.ContainsAwait,
			Range:         convertLocation(item.Range),
			BodyStart:     item.BodyStart,
			BodyEnd:       item.BodyEnd,
		})
	}
	return result
}

func convertImportEdges(items []lintaiapi.ImportEdge) []analysis.ImportEdge {
	result := make([]analysis.ImportEdge, 0, len(items))
	for _, item := range items {
		result = append(result, analysis.ImportEdge{
			EntityID:    item.EntityID,
			SemanticKey: item.SemanticKey,
			Specifier:   item.Specifier,
			FromPath:    item.FromPath,
			ToPath:      item.ToPath,
			Range:       convertLocation(item.Range),
		})
	}
	return result
}

func convertCallEdges(items []lintaiapi.CallEdge) []analysis.CallEdge {
	result := make([]analysis.CallEdge, 0, len(items))
	for _, item := range items {
		result = append(result, analysis.CallEdge{
			EntityID:        item.EntityID,
			SemanticKey:     item.SemanticKey,
			FromSemanticKey: item.FromSemanticKey,
			ToSemanticKey:   item.ToSemanticKey,
			FromName:        item.FromName,
			ToName:          item.ToName,
			FromPath:        item.FromPath,
			ToPath:          item.ToPath,
			Range:           convertLocation(item.Range),
		})
	}
	return result
}

func convertTypeRefs(items []lintaiapi.TypeRef) []analysis.TypeRef {
	result := make([]analysis.TypeRef, 0, len(items))
	for _, item := range items {
		result = append(result, analysis.TypeRef{
			EntityID:    item.EntityID,
			SemanticKey: item.SemanticKey,
			Name:        item.Name,
			FilePath:    item.FilePath,
			Range:       convertLocation(item.Range),
		})
	}
	return result
}

func convertLocation(location lintaiapi.SourceLocation) diagnostics.SourceLocation {
	return diagnostics.SourceLocation{
		File:        location.File,
		StartLine:   location.StartLine,
		StartColumn: location.StartColumn,
		EndLine:     location.EndLine,
		EndColumn:   location.EndColumn,
	}
}

func snapshotVersion(root string, files []string) (string, error) {
	sorted := make([]string, len(files))
	copy(sorted, files)
	sort.Strings(sorted)

	hash := sha1.New()
	for _, file := range sorted {
		hash.Write([]byte(workspace.RelativePath(root, file)))
		hash.Write([]byte{0})
		source, err := workspace.ReadFile(file)
		if err != nil {
			return "", err
		}
		hash.Write([]byte(source))
		hash.Write([]byte{0})
	}
	return hex.EncodeToString(hash.Sum(nil)), nil
}

func buildTransitiveCalls(functions []analysis.Function, callEdges []analysis.CallEdge) map[string]map[string]struct{} {
	adjacency := make(map[string][]string)
	for _, edge := range callEdges {
		adjacency[edge.FromSemanticKey] = append(adjacency[edge.FromSemanticKey], edge.ToSemanticKey)
	}
	transitive := make(map[string]map[string]struct{}, len(functions))
	for _, fn := range functions {
		seen := make(map[string]struct{})
		stack := append([]string{}, adjacency[fn.SemanticKey]...)
		for len(stack) > 0 {
			last := stack[len(stack)-1]
			stack = stack[:len(stack)-1]
			if _, exists := seen[last]; exists {
				continue
			}
			seen[last] = struct{}{}
			stack = append(stack, adjacency[last]...)
		}
		transitive[fn.SemanticKey] = seen
	}
	return transitive
}
