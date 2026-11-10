package typescript

import (
	"context"
	"crypto/sha1"
	"encoding/hex"
	"fmt"
	"io"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"sync"

	"github.com/bigbizze/lintai/backend"
	"github.com/bigbizze/lintai/diagnostics"
	"github.com/bigbizze/lintai/facts"
	"github.com/bigbizze/lintai/internal/ambient"
	"github.com/bigbizze/lintai/internal/analysis"
	"github.com/bigbizze/lintai/internal/tsgo/lintaiapi"
	"github.com/bigbizze/lintai/internal/workspace"
)

// Backend extracts facts from TypeScript and JavaScript files.
type Backend struct {
	// Log receives one line for each tsconfig that lintai adjusted for
	// TypeScript 7, once per config and adjustment set. Nil discards them.
	Log io.Writer

	reportedMu sync.Mutex
	reported   map[string]string
}

var _ backend.Backend = (*Backend)(nil)

func New() *Backend {
	return &Backend{}
}

func (b *Backend) ID() string {
	return "typescript"
}

// BuildSnapshot extracts the facts of the source files under workspaceRoot.
func (b *Backend) BuildSnapshot(ctx context.Context, repoRoot, workspaceRoot string, options backend.SnapshotOptions) (facts.Snapshot, error) {
	snapshot, err := b.buildAnalysisSnapshot(ctx, repoRoot, workspaceRoot, options)
	if err != nil {
		return facts.Snapshot{}, err
	}
	items, err := factsFromAnalysis(snapshot)
	if err != nil {
		return facts.Snapshot{}, err
	}
	return facts.Snapshot{Version: snapshot.Version, Schema: Schema(), Facts: items, InputPaths: snapshot.InputPaths}, nil
}

func (b *Backend) buildAnalysisSnapshot(ctx context.Context, repoRoot, workspaceRoot string, options backend.SnapshotOptions) (*analysis.Snapshot, error) {
	scope, err := backend.ParseFileScope(string(options.Files))
	if err != nil {
		return nil, err
	}
	walked, err := workspace.ListSourceFiles(workspaceRoot, options.Excludes)
	if err != nil {
		return nil, err
	}
	sources := ambient.Builtin()
	extracted, err := lintaiapi.BuildSnapshot(ctx, lintaiapi.BuildSnapshotRequest{
		WorkspaceRoot:   workspaceRoot,
		Files:           walked,
		SourceLinks:     options.SourceLinks,
		AmbientMatchers: convertAmbientMatchers(sources),
		TsconfigFiles:   scope == backend.FileScopeTsconfig,
	})
	if err != nil {
		return nil, err
	}
	files := moduleFiles(workspaceRoot, walked, extracted.Modules)
	adjustments := convertAdjustments(workspaceRoot, extracted.ConfigAdjustments)
	b.reportAdjustments(adjustments)

	modules := convertModules(extracted.Modules)
	functions := convertFunctions(extracted.Functions)
	importEdges := convertImportEdges(extracted.ImportEdges)
	callEdges := convertCallEdges(extracted.CallEdges)
	typeRefs := convertTypeRefs(extracted.TypeRefs)
	inputPaths := make([]string, 0, len(extracted.Inputs))
	for _, input := range extracted.Inputs {
		inputPaths = append(inputPaths, input.Path)
	}
	versionFiles := slices.Concat(files, extracted.ConfigFiles)
	if options.SourceLinks != "" {
		links := options.SourceLinks
		if !filepath.IsAbs(links) {
			links = filepath.Join(workspaceRoot, links)
		}
		versionFiles = append(versionFiles, links)
	}
	version, err := snapshotVersionWithInputs(workspaceRoot, versionFiles, extracted.Inputs)
	if err != nil {
		return nil, err
	}

	return &analysis.Snapshot{
		Version:                     version,
		BackendID:                   b.ID(),
		WorkspaceRoot:               workspaceRoot,
		Modules:                     modules,
		Functions:                   functions,
		ImportEdges:                 importEdges,
		CallEdges:                   callEdges,
		ExternalCalls:               convertExternalCalls(extracted.ExternalCalls),
		CallsUnresolved:             convertCallsUnresolved(extracted.CallsUnresolved),
		ImportsOpaque:               convertImportsOpaque(extracted.ImportsOpaque),
		FnRefs:                      convertFnRefs(extracted.FnRefs),
		SuspensionSites:             convertSuspensionSites(extracted.SuspensionSites),
		UnsupportedAttributions:     convertUnsupportedAttributions(extracted.UnsupportedAttributions),
		TypeRefs:                    typeRefs,
		Accesses:                    convertAccesses(extracted.Accesses),
		AmbientSources:              convertAmbientSources(sources),
		AmbientUses:                 convertAmbientUses(extracted.AmbientUses),
		GlobalRefs:                  convertGlobalRefs(extracted.GlobalRefs),
		Directives:                  convertDirectives(extracted.Directives),
		ParseErrors:                 convertParseErrors(extracted.ParseErrors),
		Types:                       convertTypes(extracted.Types),
		Declarations:                convertDeclarations(extracted.Declarations),
		Exports:                     convertExports(extracted.Exports),
		ModuleExportsIncomplete:     convertModuleExportsIncomplete(extracted.ModuleExportsIncomplete),
		ExportSites:                 convertExportSites(extracted.ExportSites),
		DeclarationSourceCandidates: convertDeclarationSources(extracted.DeclarationSourceCandidates),
		DeclarationSources:          convertDeclarationSources(extracted.DeclarationSources),
		DeclarationSourceOutcomes:   convertDeclarationSourceOutcomes(extracted.DeclarationSourceOutcomes),
		SourceEntries:               convertSourceEntries(extracted.SourceEntries),
		SourceBindings:              convertSourceBindings(extracted.SourceBindings),
		InputPaths:                  inputPaths,
		Adjustments:                 adjustments,
	}, nil
}

// moduleFiles returns the walked files that are modules of the snapshot.
func moduleFiles(workspaceRoot string, walked []string, modules []lintaiapi.Module) []string {
	paths := make(map[string]struct{}, len(modules))
	for _, item := range modules {
		paths[item.Path] = struct{}{}
	}
	result := make([]string, 0, len(modules))
	for _, file := range walked {
		if _, ok := paths[workspace.RelativePath(workspaceRoot, file)]; ok {
			result = append(result, file)
		}
	}
	return result
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
			EntityID:              item.EntityID,
			SemanticKey:           item.SemanticKey,
			Name:                  item.Name,
			Kind:                  item.Kind,
			FilePath:              item.FilePath,
			ContainerName:         item.ContainerName,
			IsExported:            item.IsExported,
			IsAsync:               item.IsAsync,
			ParameterCount:        item.ParameterCount,
			SignatureObservations: convertSignatureObservations(item.SignatureObservations),
			SignatureConflict:     item.SignatureConflict,
			SignatureOmitted:      item.SignatureOmitted,
			SignatureUnresolved:   item.SignatureUnresolved,
			Range:                 convertLocation(item.Range),
		})
	}
	return result
}

func convertSuspensionSites(items []lintaiapi.SuspensionSite) []analysis.SuspensionSite {
	result := make([]analysis.SuspensionSite, 0, len(items))
	for _, item := range items {
		result = append(result, analysis.SuspensionSite{EntityID: item.EntityID, SemanticKey: item.SemanticKey, FilePath: item.FilePath, Kind: item.Kind, FunctionSemanticKey: item.FunctionSemanticKey, UnsupportedReason: item.UnsupportedReason, AtModule: item.AtModule, Range: convertLocation(item.Range)})
	}
	return result
}

func convertAccesses(items []lintaiapi.Access) []analysis.Access {
	result := make([]analysis.Access, 0, len(items))
	for _, item := range items {
		result = append(result, analysis.Access{
			EntityID: item.EntityID, SemanticKey: item.SemanticKey,
			Root: "import.meta", AccessPath: item.AccessPath, Origin: "special_form", FilePath: item.FilePath,
			FunctionSemanticKey: item.FunctionSemanticKey, UnsupportedReason: item.UnsupportedReason, Range: convertLocation(item.Range),
		})
	}
	return result
}

func convertUnsupportedAttributions(items []lintaiapi.UnsupportedAttribution) []analysis.UnsupportedAttribution {
	result := make([]analysis.UnsupportedAttribution, 0, len(items))
	for _, item := range items {
		result = append(result, analysis.UnsupportedAttribution{Site: item.Site, Reason: item.Reason})
	}
	return result
}

func convertSignatureObservations(items []lintaiapi.FunctionSignatureObservation) []analysis.FunctionSignatureObservation {
	result := make([]analysis.FunctionSignatureObservation, 0, len(items))
	for _, item := range items {
		result = append(result, analysis.FunctionSignatureObservation{
			ParameterTypes: append([]string(nil), item.ParameterTypes...),
			ReturnType:     item.ReturnType,
		})
	}
	return result
}

func convertDeclarations(items []lintaiapi.DeclarationFact) []analysis.Declaration {
	result := make([]analysis.Declaration, 0, len(items))
	for _, item := range items {
		types := make([]analysis.DeclarationType, 0, len(item.Types))
		for _, value := range item.Types {
			types = append(types, analysis.DeclarationType{Space: value.Space, Type: value.Type})
		}
		result = append(result, analysis.Declaration{
			ID: item.ID, Name: item.Name, Kind: item.Kind, OriginKind: item.OriginKind, Origin: item.Origin,
			Types:    types,
			Function: item.Function, Incomplete: append([]string(nil), item.Incomplete...), Range: convertLocation(item.Location),
		})
	}
	return result
}

func convertExports(items []lintaiapi.ExportFact) []analysis.Export {
	result := make([]analysis.Export, 0, len(items))
	for _, item := range items {
		reexports := make([]analysis.ExportReexport, 0, len(item.Reexports))
		for _, value := range item.Reexports {
			reexports = append(reexports, analysis.ExportReexport{Site: value.Site, Module: value.Module, SourceName: value.SourceName, Space: value.Space})
		}
		var location *diagnostics.SourceLocation
		if item.Location != (lintaiapi.SourceLocation{}) {
			converted := convertLocation(item.Location)
			location = &converted
		}
		result = append(result, analysis.Export{
			ID: item.ID, File: item.File, Name: item.Name, Space: item.Space,
			Targets: append([]string(nil), item.Targets...), Via: append([]string(nil), item.Via...),
			Reexports: reexports, Incomplete: append([]string(nil), item.Incomplete...), Range: location,
		})
	}
	return result
}

func convertModuleExportsIncomplete(items []lintaiapi.ModuleExportsIncompleteFact) []analysis.ModuleExportsIncomplete {
	result := make([]analysis.ModuleExportsIncomplete, 0, len(items))
	for _, item := range items {
		result = append(result, analysis.ModuleExportsIncomplete{File: item.File, Reason: item.Reason})
	}
	return result
}

func convertExportSites(items []lintaiapi.ExportSiteFact) []analysis.ExportSite {
	result := make([]analysis.ExportSite, 0, len(items))
	for _, item := range items {
		result = append(result, analysis.ExportSite{ID: item.ID, Range: convertLocation(item.Location)})
	}
	return result
}

func convertDeclarationSources(items []lintaiapi.DeclarationSourceFact) []analysis.DeclarationSource {
	result := make([]analysis.DeclarationSource, 0, len(items))
	for _, item := range items {
		result = append(result, analysis.DeclarationSource{CheckedDecl: item.CheckedDecl, SourceDecl: item.SourceDecl, Method: item.Method})
	}
	return result
}

func convertDeclarationSourceOutcomes(items []lintaiapi.DeclarationSourceOutcomeFact) []analysis.DeclarationSourceOutcome {
	result := make([]analysis.DeclarationSourceOutcome, 0, len(items))
	for _, item := range items {
		result = append(result, analysis.DeclarationSourceOutcome{CheckedDecl: item.CheckedDecl, Outcome: item.Outcome})
	}
	return result
}

func convertSourceEntries(items []lintaiapi.SourceEntryFact) []analysis.SourceEntry {
	result := make([]analysis.SourceEntry, 0, len(items))
	for _, item := range items {
		result = append(result, analysis.SourceEntry{
			ID: item.ID, CheckedEntry: item.CheckedEntry, SourceEntry: item.SourceEntry, Tsconfig: item.Tsconfig,
			Applied: item.Applied, Incomplete: append([]string(nil), item.Incomplete...),
		})
	}
	return result
}

func convertSourceBindings(items []lintaiapi.SourceBindingFact) []analysis.SourceBinding {
	result := make([]analysis.SourceBinding, 0, len(items))
	for _, item := range items {
		result = append(result, analysis.SourceBinding{
			ID: item.ID, Entry: item.Entry, CheckedFile: item.CheckedFile, Name: item.Name, Space: item.Space,
			SourceFile:          item.SourceFile,
			CheckedDeclarations: append([]string(nil), item.CheckedDeclarations...),
			SourceDeclarations:  append([]string(nil), item.SourceDeclarations...),
			Incomplete:          append([]string(nil), item.Incomplete...),
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
			Form:               item.Form,
			Internal:           item.Internal,
			Unresolved:         item.Unresolved,
			Package:            item.Package,
			ImportedSymbols:    importedSymbols,
			HasDefaultImport:   item.HasDefaultImport,
			HasNamespaceImport: item.HasNamespaceImport,
			HasNamedImports:    item.HasNamedImports,
			IsTypeOnly:         item.IsTypeOnly,
			Retained:           item.Retained,
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
			FromModule:      item.FromModule,
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

func convertExternalCalls(items []lintaiapi.ExternalCall) []analysis.ExternalCall {
	result := make([]analysis.ExternalCall, 0, len(items))
	for _, item := range items {
		result = append(result, analysis.ExternalCall{
			EntityID:        item.EntityID,
			SemanticKey:     item.SemanticKey,
			FromSemanticKey: item.FromSemanticKey,
			FromModule:      item.FromModule,
			FromPath:        item.FromPath,
			Module:          item.Module,
			Name:            item.Name,
			Range:           convertLocation(item.Range),
		})
	}
	return result
}

func convertDirectives(items []lintaiapi.ModuleDirective) []analysis.ModuleDirective {
	result := make([]analysis.ModuleDirective, 0, len(items))
	for _, item := range items {
		result = append(result, analysis.ModuleDirective{
			EntityID:    item.EntityID,
			SemanticKey: item.SemanticKey,
			FilePath:    item.FilePath,
			Text:        item.Text,
			Range:       convertLocation(item.Range),
		})
	}
	return result
}

func convertCallsUnresolved(items []lintaiapi.CallUnresolved) []analysis.CallUnresolved {
	result := make([]analysis.CallUnresolved, 0, len(items))
	for _, item := range items {
		result = append(result, analysis.CallUnresolved{
			EntityID:        item.EntityID,
			SemanticKey:     item.SemanticKey,
			FromSemanticKey: item.FromSemanticKey,
			FromModule:      item.FromModule,
			FromPath:        item.FromPath,
			Range:           convertLocation(item.Range),
		})
	}
	return result
}

func convertImportsOpaque(items []lintaiapi.ImportOpaque) []analysis.ImportOpaque {
	result := make([]analysis.ImportOpaque, 0, len(items))
	for _, item := range items {
		result = append(result, analysis.ImportOpaque{
			EntityID:    item.EntityID,
			SemanticKey: item.SemanticKey,
			FromPath:    item.FromPath,
			Range:       convertLocation(item.Range),
		})
	}
	return result
}

func convertFnRefs(items []lintaiapi.FnRef) []analysis.FnRef {
	result := make([]analysis.FnRef, 0, len(items))
	for _, item := range items {
		result = append(result, analysis.FnRef{
			EntityID:        item.EntityID,
			SemanticKey:     item.SemanticKey,
			FromSemanticKey: item.FromSemanticKey,
			FromModule:      item.FromModule,
			FromPath:        item.FromPath,
			ToSemanticKey:   item.ToSemanticKey,
			ToPath:          item.ToPath,
			Range:           convertLocation(item.Range),
		})
	}
	return result
}

func convertParseErrors(items []lintaiapi.ParseError) []analysis.ParseError {
	result := make([]analysis.ParseError, 0, len(items))
	for _, item := range items {
		result = append(result, analysis.ParseError{
			EntityID:    item.EntityID,
			SemanticKey: item.SemanticKey,
			FilePath:    item.FilePath,
			Message:     item.Message,
			Range:       convertLocation(item.Range),
		})
	}
	return result
}

func convertAdjustments(workspaceRoot string, items []lintaiapi.ConfigAdjustment) []analysis.ConfigAdjustment {
	result := make([]analysis.ConfigAdjustment, 0, len(items))
	for _, item := range items {
		result = append(result, analysis.ConfigAdjustment{
			Config: workspace.RelativePath(workspaceRoot, item.Config),
			Option: item.Option,
			Action: item.Action,
		})
	}
	return result
}

// reportAdjustments writes one line per adjusted tsconfig to b.Log, for
// example "lintai: adjusted tsconfig.json for TypeScript 7 (baseUrl
// rewritten, target removed)". A line that b.Log already received for the
// config is not written again, so lintai serve does not repeat it on every
// refresh. The adjustments are sorted by config.
func (b *Backend) reportAdjustments(adjustments []analysis.ConfigAdjustment) {
	if b.Log == nil {
		return
	}
	b.reportedMu.Lock()
	defer b.reportedMu.Unlock()
	if b.reported == nil {
		b.reported = make(map[string]string)
	}
	for start := 0; start < len(adjustments); {
		end := start
		changes := make([]string, 0)
		for end < len(adjustments) && adjustments[end].Config == adjustments[start].Config {
			changes = append(changes, adjustments[end].Option+" "+adjustments[end].Action)
			end++
		}
		config := adjustments[start].Config
		line := fmt.Sprintf("lintai: adjusted %s for TypeScript 7 (%s)\n", config, strings.Join(changes, ", "))
		if b.reported[config] != line {
			b.reported[config] = line
			_, _ = io.WriteString(b.Log, line)
		}
		start = end
	}
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

func convertTypes(items []lintaiapi.TypeFact) []analysis.TypeFact {
	result := make([]analysis.TypeFact, 0, len(items))
	for _, item := range items {
		converted := analysis.TypeFact{
			ID:         item.ID,
			Kind:       item.Kind,
			Named:      item.Named,
			OriginKind: item.OriginKind,
			Origin:     item.Origin,
			Name:       item.Name,
			Args:       item.Args,
			ArgMarks:   item.ArgMarks,
			Readonly:   item.Readonly,
			Members:    item.Members,
			ParamName:  item.ParamName,
			SigParams:  item.SigParams,
			SigMarks:   item.SigMarks,
			SigReturns: item.SigReturns,
			Incomplete: item.Incomplete,
			Display:    item.Display,
		}
		if item.Literal != nil {
			converted.Literal = &analysis.TypeLiteral{Kind: item.Literal.Kind, Text: item.Literal.Text, Number: item.Literal.Number, Bool: item.Literal.Bool}
		}
		for _, prop := range item.Props {
			converted.Props = append(converted.Props, analysis.TypeProp{Name: prop.Name, Type: prop.Type, Optional: prop.Optional, Readonly: prop.Readonly})
		}
		if item.Location != nil {
			location := convertLocation(*item.Location)
			converted.Location = &location
		}
		result = append(result, converted)
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
	return snapshotVersionWithInputs(root, files, nil)
}

func snapshotVersionWithInputs(root string, files []string, inputs []lintaiapi.SnapshotInput) (string, error) {
	byPath := make(map[string]lintaiapi.SnapshotInput, len(files)+len(inputs))
	required := make(map[string]bool, len(files))
	for _, file := range files {
		path := filepath.Clean(file)
		byPath[path] = lintaiapi.SnapshotInput{Path: path, Exists: true}
		required[path] = true
	}
	for _, input := range inputs {
		path := filepath.Clean(input.Path)
		current := byPath[path]
		current.Exists = current.Exists || input.Exists
		current.OptionalMap = current.OptionalMap || input.OptionalMap
		byPath[path] = current
	}
	paths := make([]string, 0, len(byPath))
	for file := range byPath {
		paths = append(paths, file)
	}
	sort.Strings(paths)
	hash := sha1.New()
	for _, file := range paths {
		relative := workspace.RelativePath(root, file)
		if strings.HasPrefix(relative, "../") || filepath.IsAbs(relative) {
			relative = filepath.ToSlash(file)
		}
		hash.Write([]byte(relative))
		hash.Write([]byte{0})
		if !byPath[file].Exists {
			hash.Write([]byte{0})
			hash.Write([]byte{0})
			continue
		}
		source, err := workspace.ReadFile(file)
		if err != nil {
			if required[file] || !byPath[file].OptionalMap {
				return "", err
			}
			hash.Write([]byte{2, 0})
			continue
		}
		hash.Write([]byte{1})
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

func convertGlobalRefs(items []lintaiapi.GlobalRef) []analysis.GlobalRef {
	result := make([]analysis.GlobalRef, 0, len(items))
	for _, item := range items {
		result = append(result, analysis.GlobalRef{
			EntityID:            item.EntityID,
			SemanticKey:         item.SemanticKey,
			Name:                item.Name,
			FilePath:            item.FilePath,
			FunctionSemanticKey: item.FunctionSemanticKey,
			Range:               convertLocation(item.Range),
		})
	}
	return result
}
