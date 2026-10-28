package typescript

import (
	"context"
	"crypto/sha1"
	"encoding/hex"
	"slices"
	"sort"

	"github.com/bigbizze/lintai/internal/ambient"
	"github.com/bigbizze/lintai/internal/analysis"
	"github.com/bigbizze/lintai/internal/diagnostics"
	"github.com/bigbizze/lintai/internal/facts"
	"github.com/bigbizze/lintai/internal/workspace"
	"github.com/microsoft/TypeScript/tsc/lintaiapi"
)

type Backend struct{}

func New() *Backend {
	return &Backend{}
}

func (b *Backend) ID() string {
	return "typescript"
}

func (b *Backend) Relations() []string {
	return facts.BaseRelations()
}

func (b *Backend) BuildSnapshot(ctx context.Context, repoRoot, workspaceRoot string) (*analysis.Snapshot, error) {
	files, err := workspace.ListSourceFiles(workspaceRoot)
	if err != nil {
		return nil, err
	}
	sources := ambient.Builtin()
	extracted, err := lintaiapi.BuildSnapshot(ctx, lintaiapi.BuildSnapshotRequest{
		WorkspaceRoot:   workspaceRoot,
		Files:           files,
		AmbientMatchers: convertAmbientMatchers(sources),
	})
	if err != nil {
		return nil, err
	}

	modules := convertModules(extracted.Modules)
	functions := convertFunctions(extracted.Functions)
	importEdges := convertImportEdges(extracted.ImportEdges)
	callEdges := convertCallEdges(extracted.CallEdges)
	typeRefs := convertTypeRefs(extracted.TypeRefs)
	accesses, err := extractAccesses(workspaceRoot, files)
	if err != nil {
		return nil, err
	}

	version, err := snapshotVersion(workspaceRoot, files)
	if err != nil {
		return nil, err
	}

	return &analysis.Snapshot{
		Version:        version,
		BackendID:      b.ID(),
		WorkspaceRoot:  workspaceRoot,
		Modules:        modules,
		Functions:      functions,
		ImportEdges:    importEdges,
		CallEdges:      callEdges,
		TypeRefs:       typeRefs,
		Accesses:       accesses,
		AmbientSources: convertAmbientSources(sources),
		AmbientUses:    convertAmbientUses(extracted.AmbientUses),
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
			EntityID:           item.EntityID,
			SemanticKey:        item.SemanticKey,
			Name:               item.Name,
			Kind:               item.Kind,
			FilePath:           item.FilePath,
			ContainerName:      item.ContainerName,
			ContainsAwait:      item.ContainsAwait,
			IsExported:         item.IsExported,
			IsAsync:            item.IsAsync,
			ParameterCount:     item.ParameterCount,
			ReturnTypeText:     item.ReturnTypeText,
			ParameterTypeTexts: append([]string(nil), item.ParameterTypeTexts...),
			Range:              convertLocation(item.Range),
			BodyStart:          item.BodyStart,
			BodyEnd:            item.BodyEnd,
		})
	}
	return result
}

func convertImportEdges(items []lintaiapi.ImportEdge) []analysis.ImportEdge {
	result := make([]analysis.ImportEdge, 0, len(items))
	for _, item := range items {
		importedSymbols := make([]analysis.ImportedSymbol, 0, len(item.ImportedSymbols))
		for _, symbol := range item.ImportedSymbols {
			importedSymbols = append(importedSymbols, analysis.ImportedSymbol{
				Name:       symbol.Name,
				Kind:       symbol.Kind,
				IsTypeOnly: symbol.IsTypeOnly,
			})
		}
		result = append(result, analysis.ImportEdge{
			EntityID:           item.EntityID,
			SemanticKey:        item.SemanticKey,
			Specifier:          item.Specifier,
			FromPath:           item.FromPath,
			ToPath:             item.ToPath,
			ImportedSymbols:    importedSymbols,
			HasDefaultImport:   item.HasDefaultImport,
			HasNamespaceImport: item.HasNamespaceImport,
			HasNamedImports:    item.HasNamedImports,
			IsTypeOnly:         item.IsTypeOnly,
			Range:              convertLocation(item.Range),
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
			TargetPath:  item.TargetPath,
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

func convertAmbientSources(sources []ambient.Source) []analysis.AmbientSource {
	result := make([]analysis.AmbientSource, 0, len(sources))
	for _, source := range sources {
		capabilities := make([]string, 0, len(source.Matchers))
		for _, matcher := range source.Matchers {
			if !slices.Contains(capabilities, matcher.Capability) {
				capabilities = append(capabilities, matcher.Capability)
			}
		}
		result = append(result, analysis.AmbientSource{ID: source.ID, Capabilities: capabilities})
	}
	return result
}

func convertAmbientMatchers(sources []ambient.Source) []lintaiapi.AmbientMatcher {
	result := make([]lintaiapi.AmbientMatcher, 0)
	for _, source := range sources {
		for _, matcher := range source.Matchers {
			modules := make([]lintaiapi.AmbientModuleBinding, 0, len(matcher.Modules))
			for _, binding := range matcher.Modules {
				modules = append(modules, lintaiapi.AmbientModuleBinding{
					Specifier: binding.Specifier,
					Export:    binding.Export,
					Path:      append([]string(nil), binding.Path...),
				})
			}
			result = append(result, lintaiapi.AmbientMatcher{
				Source:     source.ID,
				Capability: matcher.Capability,
				Root:       matcher.Root,
				Path:       append([]string(nil), matcher.Path...),
				Modules:    modules,
				Form:       string(matcher.Form),
				ArgCount:   matcher.ArgCount,
				KeyMember:  matcher.Key == ambient.KeyMember,
			})
		}
	}
	return result
}

func convertAmbientUses(items []lintaiapi.AmbientUse) []analysis.AmbientUse {
	result := make([]analysis.AmbientUse, 0, len(items))
	for _, item := range items {
		result = append(result, analysis.AmbientUse{
			EntityID:            item.EntityID,
			SemanticKey:         item.SemanticKey,
			Source:              item.Source,
			Capability:          item.Capability,
			FilePath:            item.FilePath,
			Key:                 item.Key,
			HasKey:              item.HasKey,
			Dynamic:             item.Dynamic,
			Write:               item.Write,
			FunctionSemanticKey: item.FunctionSemanticKey,
			Range:               convertLocation(item.Range),
		})
	}
	return result
}
