package lintaiapi

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/bigbizze/lintai/internal/tsgo/internal/ast"
	"github.com/bigbizze/lintai/internal/tsgo/internal/checker"
	"github.com/bigbizze/lintai/internal/tsgo/internal/compiler"
	"github.com/bigbizze/lintai/internal/tsgo/internal/module"
	"github.com/bigbizze/lintai/internal/tsgo/internal/packagejson"
	"github.com/bigbizze/lintai/internal/tsgo/internal/tsoptions"
	"github.com/bigbizze/lintai/internal/tsgo/internal/tspath"
)

type auxiliarySourceProgram struct {
	program  *compiler.Program
	checker  *checker.Checker
	resolver module.Resolver
	done     func()
	inputs   []SnapshotInput
}

type auxiliarySourceView struct {
	file *ast.SourceFile
	ex   *extractor
}

type auxiliarySourcePrograms struct {
	ctx           context.Context
	host          compiler.CompilerHost
	workspaceRoot string
	configs       *configCache
	timing        *extractTiming
	programs      map[string]*auxiliarySourceProgram
}

func newAuxiliarySourcePrograms(ctx context.Context, host compiler.CompilerHost, workspaceRoot string, configs *configCache, timing *extractTiming) *auxiliarySourcePrograms {
	return &auxiliarySourcePrograms{
		ctx: ctx, host: host, workspaceRoot: workspaceRoot, configs: configs, timing: timing,
		programs: make(map[string]*auxiliarySourceProgram),
	}
}

func (a *auxiliarySourcePrograms) close() {
	for _, item := range a.programs {
		if item.done != nil {
			item.done()
		}
	}
}

func (a *auxiliarySourcePrograms) forEntry(entry configuredSourceEntry) (*auxiliarySourceView, string) {
	if !a.host.FS().FileExists(tspath.RootedFilePathFromAbsolute(entry.SourcePath)) {
		return nil, AssociationUnresolved
	}
	configPath := entry.ConfigPath
	cacheKey := configPath
	if configPath == "" {
		nearest, err := findNearestConfigPath(a.workspaceRoot, entry.SourcePath)
		if err != nil {
			return nil, AssociationUnsupported
		}
		if nearest != "" {
			selected, reason := a.selectSourceConfig(nearest, entry.SourcePath)
			if reason != "" {
				return nil, reason
			}
			configPath = selected
			cacheKey = configPath
		}
	}
	var config *tsoptions.ParsedCommandLine
	if configPath != "" {
		var err error
		config, err = a.configs.program(configPath)
		if err != nil {
			return nil, AssociationUnsupported
		}
		if entry.ConfigPath != "" {
			parsed, err := a.configs.load(configPath)
			if err != nil || !configIncludesFile(parsed, entry.SourcePath, a.host.FS().CaseSensitivity()) {
				return nil, AssociationUnsupported
			}
		}
	} else {
		cacheKey = "synthetic:" + entry.SourcePath
		var err error
		config, err = syntheticConfig(a.host, a.workspaceRoot, []string{entry.SourcePath})
		if err != nil {
			return nil, AssociationUnsupported
		}
	}
	item := a.programs[cacheKey]
	if item == nil {
		started := time.Now()
		a.timing.auxiliaryPrograms++
		var resolver module.Resolver
		program := compiler.NewProgram(compiler.ProgramOptions{
			ProgramConfig: compiler.ProgramConfig{Config: config, UseSourceOfProjectReference: true},
			ProgramHosts:  compiler.ProgramHosts{Host: a.host},
			ProgramFactories: compiler.ProgramFactories{
				CreateModuleResolver: func(options module.ResolverOptions) module.Resolver {
					resolver = module.NewResolver(options)
					return resolver
				},
			},
		})
		if len(program.GetConfigFileParsingDiagnostics()) > 0 {
			a.timing.auxiliaryProgram += time.Since(started)
			return nil, AssociationUnsupported
		}
		typeChecker, done := program.GetTypeChecker(a.ctx)
		item = &auxiliarySourceProgram{program: program, checker: typeChecker, resolver: resolver, done: done}
		a.programs[cacheKey] = item
		inputExtractor := &extractor{workspaceRoot: a.workspaceRoot, program: program, host: a.host}
		inputExtractor.recordProgramInputs()
		item.inputs = inputExtractor.inputs
		a.timing.auxiliaryProgram += time.Since(started)
	}
	file := item.program.GetSourceFile(tspath.RootedFilePathFromAbsolute(entry.SourcePath))
	if file == nil || file.Symbol == nil || !ast.IsExternalModule(file) {
		return nil, AssociationUnsupported
	}
	ex := &extractor{
		workspaceRoot: a.workspaceRoot, host: a.host, program: item.program, resolver: item.resolver, checker: item.checker,
		declarations: make(map[string]*DeclarationFact), endpointOnly: true,
	}
	ex.inputs = append(ex.inputs, item.inputs...)
	return &auxiliarySourceView{file: file, ex: ex}, ""
}

func (a *auxiliarySourcePrograms) selectSourceConfig(nearest, sourceFile string) (string, string) {
	caseSensitivity := a.host.FS().CaseSensitivity()
	visited := make(map[string]struct{})
	var owners []string
	var collect func(string) error
	collect = func(configPath string) error {
		if _, seen := visited[configPath]; seen {
			return nil
		}
		visited[configPath] = struct{}{}
		parsed, err := a.configs.load(configPath)
		if err != nil {
			return err
		}
		if configIncludesFile(parsed, sourceFile, caseSensitivity) {
			owners = append(owners, configPath)
			return nil
		}
		for _, reference := range parsed.references {
			if err := collect(reference); err != nil {
				return err
			}
		}
		return nil
	}
	if err := collect(nearest); err != nil {
		return "", AssociationUnsupported
	}
	owners = sortedUnique(owners)
	if len(owners) == 0 {
		return nearest, ""
	}
	if len(owners) == 1 {
		return owners[0], ""
	}
	var signature string
	for index, owner := range owners {
		command, err := a.configs.program(owner)
		if err != nil {
			return "", AssociationUnsupported
		}
		roots := make([]string, 0, len(command.FileNames()))
		for _, file := range command.FileNames() {
			roots = append(roots, normalizeSlash(file.AsString()))
		}
		slices.Sort(roots)
		current := fmt.Sprintf("%#v|%s", command.CompilerOptions(), strings.Join(roots, "\x00"))
		if index == 0 {
			signature = current
		} else if current != signature {
			return "", AssociationUnsupported
		}
	}
	return owners[0], ""
}

func configIncludesFile(config *parsedConfig, file string, caseSensitivity tspath.CaseSensitivity) bool {
	want := caseSensitivity.PathKey(tspath.RootedPathFromAbsolute(file)).AsString()
	for candidate := range config.rootFiles {
		rooted, ok := tspath.TryRootedPathFromAbsolute(candidate)
		if ok && caseSensitivity.PathKey(rooted).AsString() == want {
			return true
		}
	}
	return false
}

func (e *extractor) applyConfiguredSourceEntries(entries []configuredSourceEntry, auxiliary *auxiliarySourcePrograms) error {
	for _, entry := range entries {
		checked := e.checkedSourceFile(entry.CheckedPath)
		if checked == nil {
			continue
		}
		e.sourceEntryFacts = append(e.sourceEntryFacts, SourceEntryFact{
			ID: entry.ID, CheckedEntry: entry.CheckedEntry, SourceEntry: entry.SourceEntry, Tsconfig: entry.SourceConfig, Applied: true,
		})
		e.inputs = append(e.inputs, SnapshotInput{Path: entry.CheckedPath, Exists: true}, SnapshotInput{Path: entry.SourcePath, Exists: e.host.FS().FileExists(tspath.RootedFilePathFromAbsolute(entry.SourcePath))})
		e.recordSourceConfigCandidates(entry.SourcePath)
		sourceProgram, sourceOutcome := e.sourceViewInProgram(entry), ""
		if sourceProgram == nil {
			sourceProgram, sourceOutcome = auxiliary.forEntry(entry)
		}
		if sourceOutcome == "" {
			e.inputs = append(e.inputs, sourceProgram.ex.inputs...)
			for _, reason := range sourceProgram.ex.exportInventoryIncomplete(sourceProgram.file) {
				e.sourceEntryFacts = append(e.sourceEntryFacts, SourceEntryFact{ID: entry.ID, Incomplete: []string{reason}})
			}
		}
		bindings := e.moduleBindings(checked)
		for _, binding := range bindings {
			assoc := sourceBindingID(entry.ID, e.canonicalFileIdentity(checked.FileName().AsString()), binding.name, binding.space)
			item := SourceBindingFact{
				ID: assoc, Entry: entry.ID, CheckedFile: e.canonicalFileIdentity(checked.FileName().AsString()),
				Name: binding.name, Space: binding.space,
			}
			var checkedReasons []string
			item.CheckedDeclarations, checkedReasons = bindingDeclarationEndpoints(e, binding)
			for _, declaration := range binding.declarations {
				e.recoverDeclarationMap(declaration, true, binding.name)
			}
			item.CheckedComplete = len(checkedReasons) == 0
			if item.CheckedComplete {
				item.CheckedObservations = [][]string{item.CheckedDeclarations}
			}
			item.Incomplete = append(item.Incomplete, checkedReasons...)
			if sourceOutcome != "" {
				item.Incomplete = append(item.Incomplete, sourceOutcome)
				e.sourceEntryFacts = append(e.sourceEntryFacts, SourceEntryFact{ID: entry.ID, Incomplete: []string{sourceOutcome}})
			} else if sourceBinding, found := sourceProgram.ex.findModuleBinding(sourceProgram.file, binding.name, binding.space); !found {
				item.Incomplete = append(item.Incomplete, AssociationUnresolved)
				e.sourceEntryFacts = append(e.sourceEntryFacts, SourceEntryFact{ID: entry.ID, Incomplete: []string{AssociationUnresolved}})
			} else {
				var sourceReasons []string
				item.SourceDeclarations, sourceReasons = bindingDeclarationEndpoints(sourceProgram.ex, sourceBinding)
				item.SourceComplete = len(sourceReasons) == 0
				if item.SourceComplete {
					item.SourceObservations = [][]string{item.SourceDeclarations}
					item.SourceFile = sourceProgram.ex.canonicalFileIdentity(sourceProgram.file.FileName().AsString())
					item.SourceFiles = []string{item.SourceFile}
					item.SourceName = binding.name
					item.Method = "source_entry"
				}
				item.Incomplete = append(item.Incomplete, sourceReasons...)
			}
			item.Incomplete = sortedUnique(item.Incomplete)
			e.sourceBindingFacts = append(e.sourceBindingFacts, item)
		}
		if sourceOutcome == "" {
			e.sourceProgramDeclarations = append(e.sourceProgramDeclarations, sourceProgram.ex.sortedDeclarations()...)
		}
		if len(bindings) == 0 && sourceOutcome != "" {
			e.sourceEntryFacts = append(e.sourceEntryFacts, SourceEntryFact{ID: entry.ID, Incomplete: []string{sourceOutcome}})
		}
	}
	return nil
}

// checkedSourceFile follows only the aliases recorded by this program's module
// and type-reference resolutions. The source file's own compiler path remains
// its identity, and a project-reference redirect is not the checked entry.
func (e *extractor) checkedSourceFile(path string) *ast.SourceFile {
	want := e.program.PathKeyForFileName(tspath.RootedFilePathFromAbsolute(path))
	if file := e.program.GetSourceFileByPath(want); file != nil && file.PathKey() == want {
		return file
	}
	var matched *ast.SourceFile
	conflict := false
	match := func(original tspath.RootedFilePath, resolved tspath.PathKey) {
		if original == "" || e.program.PathKeyForFileName(original) != want {
			return
		}
		file := e.program.GetSourceFileByPath(resolved)
		if file == nil || file.PathKey() != resolved {
			return
		}
		if matched != nil && matched.PathKey() != file.PathKey() {
			conflict = true
		}
		matched = file
	}
	for _, resolutions := range e.program.GetResolvedModules() {
		for _, resolution := range resolutions {
			if resolution.IsResolved() {
				match(resolution.OriginalPath, resolution.ResolvedPath)
			}
		}
	}
	for _, resolutions := range e.program.GetResolvedTypeReferenceDirectives() {
		for _, resolution := range resolutions {
			if resolution != nil && resolution.IsResolved() {
				match(resolution.OriginalPath, resolution.ResolvedPath)
			}
		}
	}
	if conflict {
		return nil
	}
	return matched
}

// bindingDeclarationEndpoints records the declarations of one exported binding
// as endpoints. A binding without declarations has an unresolved target. A
// declaration form that the extraction does not model is unsupported.
func bindingDeclarationEndpoints(e *extractor, binding moduleBinding) ([]string, []string) {
	var ids, reasons []string
	if !binding.complete {
		reasons = append(reasons, AssociationUnresolved)
	}
	for _, declaration := range binding.declarations {
		id, supported := e.addDeclarationEndpoint(declaration, binding.symbol, binding.name)
		if id == "" {
			reasons = append(reasons, AssociationUnresolved)
			continue
		}
		ids = append(ids, id)
		if !supported {
			reasons = append(reasons, AssociationUnsupported)
		}
	}
	return sortedUnique(ids), sortedUnique(reasons)
}

// sourceViewInProgram uses the consumer's checker when the configured source
// entry is already part of the checked program. Only a source entry outside
// that program needs an auxiliary program.
func (e *extractor) sourceViewInProgram(entry configuredSourceEntry) *auxiliarySourceView {
	file := e.program.GetSourceFile(tspath.RootedFilePathFromAbsolute(entry.SourcePath))
	if file == nil || file.IsDeclarationFile || file.Symbol == nil || !ast.IsExternalModule(file) {
		return nil
	}
	ex := &extractor{
		workspaceRoot: e.workspaceRoot, host: e.host, program: e.program, resolver: e.resolver, checker: e.checker,
		declarations: make(map[string]*DeclarationFact), endpointOnly: true,
	}
	return &auxiliarySourceView{file: file, ex: ex}
}

type moduleBinding struct {
	name         string
	space        string
	symbol       *ast.Symbol
	declarations []*ast.Node
	complete     bool
}

func (e *extractor) moduleBindings(file *ast.SourceFile) []moduleBinding {
	if e.checker == nil || file == nil || file.Symbol == nil {
		return nil
	}
	exports := e.checker.GetExportsOfModule(file.Symbol)
	slices.SortFunc(exports, func(left, right *ast.Symbol) int { return strings.Compare(left.Name(), right.Name()) })
	var result []moduleBinding
	for _, exported := range exports {
		if exported == nil || exported.Name() == "" {
			continue
		}
		symbol := e.checker.SkipAlias(exported)
		spaces := exportSpaces(symbol)
		if len(spaces) == 0 {
			spaces = exportSpaces(exported)
		}
		if exportSymbolIsTypeOnly(exported) {
			spaces = []string{SpaceType}
		}
		if len(spaces) == 0 {
			spaces = []string{SpaceValue}
		}
		for _, space := range spaces {
			var declarations []*ast.Node
			if symbol != nil {
				declarations = symbol.Declarations()
			}
			result = append(result, moduleBinding{
				name: exported.Name(), space: space, symbol: symbol,
				declarations: declarations, complete: symbol != nil && len(declarations) > 0,
			})
		}
	}
	if !slices.ContainsFunc(result, func(binding moduleBinding) bool { return binding.name == "export=" && binding.space == SpaceValue }) {
		for _, statement := range file.Statements.Nodes {
			if statement.Kind != ast.KindExportAssignment || !statement.AsExportAssignment().IsExportEquals {
				continue
			}
			symbol := e.checker.GetSymbolAtLocation(statement.AsExportAssignment().Expression)
			if symbol != nil {
				symbol = e.checker.SkipAlias(symbol)
				if symbol != nil {
					declarations := symbol.Declarations()
					result = append(result, moduleBinding{name: "export=", space: SpaceValue, symbol: symbol, declarations: declarations, complete: len(declarations) > 0})
				}
			}
		}
	}
	return result
}

func (e *extractor) findModuleBinding(file *ast.SourceFile, name, space string) (moduleBinding, bool) {
	for _, binding := range e.moduleBindings(file) {
		if binding.name == name && binding.space == space {
			return binding, true
		}
	}
	return moduleBinding{}, false
}

func exportSymbolIsTypeOnly(symbol *ast.Symbol) bool {
	if symbol == nil {
		return false
	}
	seenAlias := false
	typeOnly := false
	for _, declaration := range symbol.Declarations() {
		if declaration == nil {
			continue
		}
		switch declaration.Kind {
		case ast.KindExportSpecifier, ast.KindExportDeclaration, ast.KindNamespaceExport,
			ast.KindImportSpecifier, ast.KindImportClause, ast.KindNamespaceImport, ast.KindImportEqualsDeclaration:
			seenAlias = true
			if !ast.IsTypeOnlyImportOrExportDeclaration(declaration) {
				return false
			}
			typeOnly = true
		}
	}
	return seenAlias && typeOnly
}

func (e *extractor) addDeclarationEndpoint(node *ast.Node, symbol *ast.Symbol, publicName string) (string, bool) {
	previous := e.endpointOnly
	e.endpointOnly = true
	id, supported := e.addDeclaration(node, symbol, publicName, SpaceValue)
	e.endpointOnly = previous
	return id, supported
}

func (e *extractor) recordProgramInputs() {
	for _, file := range e.program.FilesByPath() {
		if file == nil {
			continue
		}
		path := normalizeSlash(file.FileName().AsString())
		// Bundled compiler libraries are virtual URLs served by the bundled
		// filesystem. They are not workspace inputs and must not be handed to
		// the OS-backed snapshot hasher as paths such as bundled:///libs/....
		if tspath.IsUrl(path) || strings.HasPrefix(path, "^/") {
			continue
		}
		if rooted, ok := tspath.TryRootedFilePathFromAbsolute(path); ok {
			e.inputs = append(e.inputs, SnapshotInput{Path: path, Exists: e.host.FS().FileExists(rooted)})
		}
	}
	e.program.PackageJsonCacheEntries(func(_ tspath.PathKey, entry *packagejson.InfoCacheEntry) bool {
		if entry != nil {
			path := normalizeSlash(entry.PackageDirectory.ResolveFile("package.json").AsString())
			e.inputs = append(e.inputs, SnapshotInput{Path: path, Exists: entry.Exists()})
		}
		return true
	})
}

func (e *extractor) recordSourceConfigCandidates(sourcePath string) {
	current := filepathDir(sourcePath)
	root := e.workspaceRoot
	if root != "/" {
		root = strings.TrimSuffix(root, "/")
	}
	for {
		candidate := normalizeSlash(current + "/tsconfig.json")
		rooted, ok := tspath.TryRootedFilePathFromAbsolute(candidate)
		e.inputs = append(e.inputs, SnapshotInput{Path: candidate, Exists: ok && e.host.FS().FileExists(rooted)})
		if current == root {
			return
		}
		parent := filepathDir(current)
		if parent == current || !isWithinRoot(root, parent) {
			return
		}
		current = parent
	}
}

func filepathDir(path string) string {
	index := strings.LastIndex(path, "/")
	if index <= 0 {
		return "/"
	}
	return path[:index]
}
