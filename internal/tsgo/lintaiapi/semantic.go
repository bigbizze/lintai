package lintaiapi

import (
	"path/filepath"
	"slices"
	"strconv"
	"strings"

	"github.com/bigbizze/lintai/internal/tsgo/internal/ast"
	"github.com/bigbizze/lintai/internal/tsgo/internal/checker"
	"github.com/bigbizze/lintai/internal/tsgo/internal/scanner"
	"github.com/bigbizze/lintai/internal/tsgo/internal/tspath"
)

const (
	SpaceType  = "type"
	SpaceValue = "value"
)

const (
	AssociationUnresolved  = "unresolved"
	AssociationUnsupported = "unsupported"
	AssociationConflict    = "conflict"
)

type syntaxExportContribution struct {
	publicName string
	sourceName string
	site       string
	module     string
	typeOnly   bool
	reexport   bool
	sourceNode *ast.Node
}

func (e *extractor) collectSemanticExports(file *ast.SourceFile) {
	if file == nil || !ast.IsExternalModule(file) || file.Symbol == nil {
		return
	}
	moduleIdentity := e.canonicalFileIdentity(file.FileName().AsString())
	moduleFile := e.relativePath(file.FileName().AsString())
	contributions := make(map[string][]syntaxExportContribution)
	var starSources []string
	var inventoryReasons []string

	addContribution := func(item syntaxExportContribution, node *ast.Node) {
		if item.publicName == "" || item.site == "" {
			return
		}
		contributions[item.publicName] = append(contributions[item.publicName], item)
		if item.reexport && item.sourceName == "*" && item.module != "" {
			starSources = append(starSources, item.module)
		}
		if item.site != e.importSiteByNode[node] {
			e.addExportSite(item.site, node)
		}
	}

	for _, statement := range file.Statements.Nodes {
		switch statement.Kind {
		case ast.KindExportDeclaration:
			exportDeclaration := statement.AsExportDeclaration()
			modulePath := ""
			unresolved := false
			if exportDeclaration.ModuleSpecifier != nil && ast.IsStringLiteralLike(exportDeclaration.ModuleSpecifier.AsNode()) {
				resolved := e.resolveModuleSpecifier(file, exportDeclaration.ModuleSpecifier.AsNode())
				if resolved != nil && resolved.IsResolved() {
					modulePath = e.canonicalFileIdentity(resolved.ResolvedFileName.AsString())
				} else {
					unresolved = true
				}
			}
			site := e.importSiteByNode[statement]
			if site == "" {
				site = e.exportSiteID(file, statement)
			}
			typeOnly := exportDeclaration.IsTypeOnly
			switch {
			case exportDeclaration.ExportClause == nil:
				if modulePath != "" || unresolved {
					contributions["*"] = append(contributions["*"], syntaxExportContribution{
						publicName: "*", sourceName: "*", site: site, module: modulePath,
						typeOnly: typeOnly, reexport: true, sourceNode: statement,
					})
					if modulePath != "" {
						starSources = append(starSources, modulePath)
					}
				}
				if unresolved {
					inventoryReasons = append(inventoryReasons, AssociationUnresolved)
				}
			case ast.IsNamedExports(exportDeclaration.ExportClause):
				for _, specifier := range exportDeclaration.ExportClause.AsNamedExports().Elements.Nodes {
					if specifier == nil || specifier.Name() == nil {
						continue
					}
					item := specifier.AsExportSpecifier()
					publicName := item.Name().Text()
					sourceName := publicName
					if item.PropertyName != nil {
						sourceName = item.PropertyName.Text()
					}
					isTypeOnly := typeOnly || item.IsTypeOnly
					isTypeOnly = isTypeOnly || modulePath == "" && !unresolved && localBindingIsTypeOnly(file, sourceName)
					addContribution(syntaxExportContribution{
						publicName: publicName, sourceName: sourceName, site: site, module: modulePath,
						typeOnly: isTypeOnly, reexport: modulePath != "" || unresolved, sourceNode: specifier,
					}, statement)
				}
			case ast.IsNamespaceExport(exportDeclaration.ExportClause):
				name := exportDeclaration.ExportClause.AsNamespaceExport().Name()
				if name != nil {
					addContribution(syntaxExportContribution{
						publicName: name.Text(), sourceName: "*", site: site, module: modulePath,
						typeOnly: typeOnly, reexport: modulePath != "" || unresolved, sourceNode: exportDeclaration.ExportClause,
					}, statement)
				}
			}
		case ast.KindExportAssignment:
			assignment := statement.AsExportAssignment()
			name := "default"
			if assignment.IsExportEquals {
				name = "export="
			}
			site := e.exportSiteID(file, statement)
			addContribution(syntaxExportContribution{publicName: name, sourceName: name, site: site, sourceNode: statement}, statement)
		default:
			if ast.GetCombinedModifierFlags(statement)&(ast.ModifierFlagsExport|ast.ModifierFlagsDefault) == 0 {
				continue
			}
			name := ""
			if ast.GetCombinedModifierFlags(statement)&ast.ModifierFlagsDefault != 0 {
				name = "default"
			} else if statement.Name() != nil {
				name = statement.Name().Text()
			}
			if name == "" && ast.IsVariableStatement(statement) {
				site := e.exportSiteID(file, statement)
				for _, declaration := range statement.AsVariableStatement().DeclarationList.AsVariableDeclarationList().Declarations.Nodes {
					if declaration.Name() != nil {
						name = declaration.Name().Text()
						addContribution(syntaxExportContribution{publicName: name, sourceName: name, site: site, sourceNode: declaration}, statement)
					}
				}
				continue
			}
			if name != "" {
				addContribution(syntaxExportContribution{publicName: name, sourceName: name, site: e.exportSiteID(file, statement), sourceNode: statement}, statement)
			}
		}
	}

	exports := e.checker.GetExportsOfModule(file.Symbol)
	slices.SortFunc(exports, func(left, right *ast.Symbol) int { return strings.Compare(left.Name(), right.Name()) })
	seenBindings := make(map[string]struct{})
	bindings := make([]string, 0, len(exports)*2)
	for _, exported := range exports {
		if exported == nil {
			continue
		}
		name := exported.Name()
		if name == "" {
			continue
		}
		resolved := e.checker.SkipAlias(exported)
		spaces := e.effectiveBindingSpaces(file, exported)
		var targetIDs []string
		if resolved != nil {
			for _, declaration := range resolved.Declarations() {
				targetIDs = append(targetIDs, e.declarationID(declaration))
			}
		}
		targetIDs = sortedUnique(targetIDs)
		items := e.exportContributionsForBinding(file, name, targetIDs, contributions)
		if len(spaces) == 0 {
			spaces = []string{SpaceValue}
		}
		for _, space := range spaces {
			binding := name + "\x00" + space
			bindings = append(bindings, binding)
			seenBindings[binding] = struct{}{}
			declarations := declarationsInSpace(resolved, space)
			complete := resolved != nil && len(declarations) > 0
			reasons := []string(nil)
			var spaceTargets []string
			ambiguous := e.starBindingAmbiguous(file, name)
			spaceItems := items
			if ambiguous {
				complete = false
				reasons = append(reasons, AssociationConflict)
				spaceItems = e.exportContributionsForBinding(file, name, nil, contributions)
			} else if resolved != nil {
				for _, declaration := range declarations {
					id, supported := e.addDeclaration(declaration, resolved, name, space)
					if id != "" {
						spaceTargets = append(spaceTargets, id)
					}
					if !supported {
						complete = false
						if fact := e.declarations[id]; fact != nil && slices.Contains(fact.Incomplete, AssociationUnsupported) {
							reasons = append(reasons, AssociationUnsupported)
						} else {
							reasons = append(reasons, AssociationUnresolved)
						}
					}
				}
			}
			spaceTargets = sortedUnique(spaceTargets)
			if !complete {
				if len(reasons) == 0 {
					reasons = append(reasons, AssociationUnresolved)
				}
			}
			e.addExport(moduleIdentity, moduleFile, name, space, spaceTargets, complete, reasons, spaceItems)
		}
	}

	// Explicit named exports remain visible even when resolution failed and
	// the checker cannot produce an export symbol. The affected binding is
	// incomplete so absence cannot be used as a proof.
	for name, items := range contributions {
		if name == "*" {
			continue
		}
		for _, item := range items {
			if !item.reexport || item.sourceName == "*" {
				continue
			}
			spaces := e.exportContributionSpaces(item)
			if len(spaces) == 0 {
				spaces = []string{SpaceValue}
			}
			for _, space := range spaces {
				binding := name + "\x00" + space
				if _, exists := seenBindings[binding]; exists {
					continue
				}
				seenBindings[binding] = struct{}{}
				bindings = append(bindings, binding)
				e.addExport(moduleIdentity, moduleFile, name, space, nil, false, []string{AssociationUnresolved}, []syntaxExportContribution{item})
			}
		}
	}

	// export = is not expanded into properties of its assigned value. Some
	// compiler paths omit the assignment from GetExportsOfModule, so retain
	// the explicit value binding from its own syntax.
	if _, found := seenBindings["export=\x00value"]; !found {
		for _, statement := range file.Statements.Nodes {
			if statement.Kind != ast.KindExportAssignment || !statement.AsExportAssignment().IsExportEquals {
				continue
			}
			var symbol *ast.Symbol
			if expression := statement.AsExportAssignment().Expression; expression != nil {
				symbol = e.checker.GetSymbolAtLocation(expression)
			}
			var targets []string
			complete := symbol != nil && len(symbol.Declarations()) > 0
			if symbol != nil {
				symbol = e.checker.SkipAlias(symbol)
				for _, declaration := range symbol.Declarations() {
					id, supported := e.addDeclaration(declaration, symbol, "export=", SpaceValue)
					if id != "" {
						targets = append(targets, id)
					}
					complete = complete && supported
				}
			}
			if len(targets) == 0 {
				id, _ := e.addDeclaration(statement, nil, "export=", SpaceValue)
				targets = append(targets, id)
				complete = true
			}
			targets = sortedUnique(targets)
			items := contributions["export="]
			reasons := []string(nil)
			if !complete {
				reasons = append(reasons, AssociationUnresolved)
			}
			e.addExport(moduleIdentity, moduleFile, "export=", SpaceValue, targets, complete, reasons, items)
			bindings = append(bindings, "export=\x00value")
			seenBindings["export=\x00value"] = struct{}{}
		}
	}

	bindings = sortedUnique(bindings)
	inventoryReasons = sortedUnique(inventoryReasons)
	starSources = sortedUnique(starSources)
	e.collectStarSourceObservations(starSources, map[string]struct{}{moduleIdentity: {}})
	e.moduleExportObservations = append(e.moduleExportObservations, ModuleExportObservation{
		Identity: moduleIdentity, File: moduleFile, Bindings: bindings, Complete: len(inventoryReasons) == 0,
		Reasons: inventoryReasons, StarSources: starSources, Retained: true,
	})
}

func (e *extractor) exportContributionsForBinding(file *ast.SourceFile, name string, targetIDs []string, contributions map[string][]syntaxExportContribution) []syntaxExportContribution {
	items := contributions[name]
	if len(items) == 0 {
		items = contributions["*"]
	}
	result := make([]syntaxExportContribution, 0, len(items))
	for _, item := range items {
		if item.publicName != "*" && item.publicName != name {
			continue
		}
		if item.reexport && item.module == "" {
			if len(targetIDs) == 0 {
				result = append(result, item)
			}
			continue
		}
		if item.reexport && item.sourceName == "*" && item.publicName != "*" {
			// A namespace export names the binding directly; it does not
			// enumerate the namespace's members.
			result = append(result, item)
			continue
		}
		candidateIDs := e.contributionDeclarations(file, item, name)
		if len(targetIDs) > 0 && len(candidateIDs) > 0 && !slices.Equal(sortedUnique(candidateIDs), targetIDs) {
			continue
		}
		if item.reexport && item.module != "" && len(candidateIDs) == 0 {
			continue
		}
		result = append(result, item)
	}
	return result
}

func (e *extractor) contributionDeclarations(file *ast.SourceFile, item syntaxExportContribution, publicName string) []string {
	if item.reexport && item.module != "" {
		sourceName := item.sourceName
		if sourceName == "*" {
			sourceName = publicName
		}
		moduleFile := e.sourceFileByIdentity(item.module)
		if moduleFile == nil || moduleFile.Symbol == nil {
			return nil
		}
		if exported := e.exportedSymbol(moduleFile, sourceName); exported != nil {
			resolved := e.checker.SkipAlias(exported)
			if resolved == nil {
				return nil
			}
			return e.declarationIDs(resolved.Declarations())
		}
		return nil
	}
	if file == nil || item.sourceNode == nil {
		return nil
	}
	var symbol *ast.Symbol
	switch item.sourceNode.Kind {
	case ast.KindExportSpecifier:
		specifier := item.sourceNode.AsExportSpecifier()
		name := specifier.PropertyName
		if name == nil {
			name = specifier.Name()
		}
		if name != nil {
			symbol = e.checker.GetSymbolAtLocation(name.AsNode())
		}
	case ast.KindExportAssignment:
		if expression := item.sourceNode.AsExportAssignment().Expression; expression != nil {
			symbol = e.checker.GetSymbolAtLocation(expression)
		}
	default:
		symbol = item.sourceNode.Symbol()
		if symbol == nil && item.sourceNode.Name() != nil {
			symbol = item.sourceNode.Name().Symbol()
		}
	}
	if symbol == nil {
		return nil
	}
	if resolved := e.checker.SkipAlias(symbol); resolved != nil {
		return e.declarationIDs(resolved.Declarations())
	}
	return nil
}

func (e *extractor) exportContributionSpaces(item syntaxExportContribution) []string {
	if item.typeOnly {
		return []string{SpaceType}
	}
	if item.reexport && item.sourceName != "*" {
		if item.module != "" {
			moduleFile := e.sourceFileByIdentity(item.module)
			if moduleFile != nil && moduleFile.Symbol != nil {
				if exported := e.exportedSymbol(moduleFile, item.sourceName); exported != nil {
					if spaces := e.effectiveBindingSpaces(moduleFile, exported); len(spaces) > 0 {
						return spaces
					}
				}
			}
		}
		// An unresolved named re-export, or one whose source name is absent,
		// may denote a type, a value, or a declaration occupying both spaces.
		// Preserve its public spelling without guessing the missing space.
		return []string{SpaceType, SpaceValue}
	}
	if item.reexport && item.sourceName == "*" && item.publicName != "*" {
		return []string{SpaceValue}
	}
	return []string{SpaceValue}
}

func localBindingIsTypeOnly(file *ast.SourceFile, name string) bool {
	if file == nil || name == "" {
		return false
	}
	for _, statement := range file.Statements.Nodes {
		switch statement.Kind {
		case ast.KindImportDeclaration:
			clause := statement.AsImportDeclaration().ImportClause
			if clause == nil {
				continue
			}
			importClause := clause.AsImportClause()
			if importClause.Name() != nil && importClause.Name().Text() == name && ast.IsTypeOnlyImportDeclaration(clause.AsNode()) {
				return true
			}
			if importClause.NamedBindings == nil {
				continue
			}
			switch importClause.NamedBindings.Kind {
			case ast.KindNamespaceImport:
				if importClause.NamedBindings.AsNamespaceImport().Name().Text() == name && ast.IsTypeOnlyImportDeclaration(importClause.NamedBindings) {
					return true
				}
			case ast.KindNamedImports:
				for _, specifier := range importClause.NamedBindings.AsNamedImports().Elements.Nodes {
					if specifier.Name() != nil && specifier.Name().Text() == name && ast.IsTypeOnlyImportDeclaration(specifier) {
						return true
					}
				}
			}
		case ast.KindImportEqualsDeclaration:
			if statement.Name() != nil && statement.Name().Text() == name && statement.AsImportEqualsDeclaration().IsTypeOnly {
				return true
			}
		}
	}
	return false
}

func (e *extractor) sourceFileByIdentity(identity string) *ast.SourceFile {
	if identity == "" || tspath.IsUrl(identity) || strings.HasPrefix(identity, "^") {
		return nil
	}
	filePath := identity
	if !filepath.IsAbs(filePath) {
		filePath = filepath.Join(e.workspaceRoot, filepath.FromSlash(identity))
	}
	rooted, ok := tspath.TryRootedFilePathFromAbsolute(filePath)
	if !ok {
		return nil
	}
	return e.program.GetSourceFile(rooted)
}

func (e *extractor) exportInventoryIncomplete(file *ast.SourceFile) []string {
	if file == nil {
		return []string{AssociationUnresolved}
	}
	sources, reasons := e.starExportInventory(file)
	identity := e.canonicalFileIdentity(file.FileName().AsString())
	e.collectStarSourceObservations(sources, map[string]struct{}{identity: {}})
	for _, observation := range e.moduleExportObservations {
		reasons = append(reasons, observation.Reasons...)
	}
	return sortedUnique(reasons)
}

func (e *extractor) starExportInventory(file *ast.SourceFile) ([]string, []string) {
	if file == nil {
		return nil, []string{AssociationUnresolved}
	}
	var sources, reasons []string
	for _, statement := range file.Statements.Nodes {
		if statement.Kind != ast.KindExportDeclaration {
			continue
		}
		declaration := statement.AsExportDeclaration()
		if declaration.ExportClause != nil || declaration.ModuleSpecifier == nil || !ast.IsStringLiteralLike(declaration.ModuleSpecifier.AsNode()) {
			continue
		}
		resolved := e.resolveModuleSpecifier(file, declaration.ModuleSpecifier.AsNode())
		if resolved == nil || !resolved.IsResolved() {
			reasons = append(reasons, AssociationUnresolved)
			continue
		}
		sources = append(sources, e.canonicalFileIdentity(resolved.ResolvedFileName.AsString()))
	}
	return sortedUnique(sources), sortedUnique(reasons)
}

func (e *extractor) collectStarSourceObservations(modules []string, visited map[string]struct{}) {
	for _, identity := range modules {
		if _, done := visited[identity]; done {
			continue
		}
		visited[identity] = struct{}{}
		file := e.sourceFileByIdentity(identity)
		if file == nil || file.Symbol == nil || !ast.IsExternalModule(file) {
			e.moduleExportObservations = append(e.moduleExportObservations, ModuleExportObservation{
				Identity: identity, File: identity, Complete: false, Reasons: []string{AssociationUnresolved},
			})
			continue
		}
		sources, reasons := e.starExportInventory(file)
		e.moduleExportObservations = append(e.moduleExportObservations, ModuleExportObservation{
			Identity: identity, File: e.relativePath(file.FileName().AsString()),
			Complete: len(reasons) == 0, Reasons: reasons, StarSources: sources,
		})
		e.collectStarSourceObservations(sources, visited)
	}
}

func (e *extractor) addDeclaration(node *ast.Node, symbol *ast.Symbol, publicName string, space string) (string, bool) {
	if node == nil {
		return "", false
	}
	kind, supported := declarationKind(node)
	id := e.declarationID(node)
	if id == "" {
		return "", false
	}
	name := publicName
	if symbol != nil {
		if qualified := qualifiedSymbolName(symbol); qualified != "" {
			name = qualified
		} else if symbol.Name() != "" && !isInternalSymbolName(symbol.Name()) {
			name = symbol.Name()
		}
	}
	file := ast.GetSourceFileOfNode(node)
	originKind, origin := e.declarationOrigin(node)
	location := e.nodeLocation(file, node)
	fact := e.declarations[id]
	if fact == nil {
		fact = &DeclarationFact{ID: id, Name: name, Kind: kind, OriginKind: originKind, Origin: origin, Location: location, TypesComplete: !e.endpointOnly}
		e.declarations[id] = fact
	}
	if e.declarationNodes == nil {
		e.declarationNodes = make(map[string]*ast.Node)
	}
	e.declarationNodes[id] = node
	if !e.endpointOnly && file != nil && file.IsDeclarationFile {
		e.recoverDeclarationMap(node, false, name)
	}
	if !supported {
		fact.Incomplete = append(fact.Incomplete, AssociationUnsupported)
		fact.TypesComplete = false
		return id, false
	}
	if e.endpointOnly {
		return id, true
	}
	fact.TypesComplete = true
	if symbol == nil {
		if node.Kind == ast.KindExportAssignment && !node.AsExportAssignment().IsExportEquals {
			if expression := node.AsExportAssignment().Expression; expression != nil {
				typeValue := e.checker.GetTypeAtLocation(expression)
				if typeValue != nil {
					e.addDeclarationType(fact, SpaceValue, e.emitFunctionType(typeValue, expression))
				} else {
					fact.TypesComplete = false
					fact.Incomplete = append(fact.Incomplete, AssociationUnresolved)
				}
			}
		}
		return id, true
	}
	var typeValue *checker.Type
	switch space {
	case SpaceType:
		typeValue = e.checker.GetDeclaredTypeOfSymbol(symbol)
	case SpaceValue:
		typeValue = e.checker.GetTypeOfSymbol(symbol)
	}
	if typeValue == nil {
		fact.TypesComplete = false
		fact.Incomplete = append(fact.Incomplete, AssociationUnresolved)
		return id, false
	}
	e.addDeclarationType(fact, space, e.emitFunctionType(typeValue, node))
	if function := e.lookupFunction(node); function != nil {
		fact.FunctionObservations = append(fact.FunctionObservations, function.SemanticKey)
		fact.Function = function.SemanticKey
		if slices.ContainsFunc(function.SignatureObservations, func(item FunctionSignatureObservation) bool { return !item.Complete }) {
			fact.Incomplete = append(fact.Incomplete, AssociationUnresolved)
		}
	}
	return id, true
}

func (e *extractor) addDeclarationType(fact *DeclarationFact, space string, typeID string) {
	if typeID == "" {
		fact.TypesComplete = false
		fact.Incomplete = append(fact.Incomplete, AssociationUnresolved)
		return
	}
	item := DeclarationType{Space: space, Type: typeID}
	for _, existing := range fact.Types {
		if existing == item {
			return
		}
	}
	fact.Types = append(fact.Types, item)
}

func (e *extractor) addExport(moduleIdentity string, file string, name string, space string, targetIDs []string, complete bool, reasons []string, contributions []syntaxExportContribution) {
	id := exportEntityID(moduleIdentity, name, space)
	fact := e.exports[id]
	if fact == nil {
		fact = &ExportFact{ID: id, File: file, Name: name, Space: space}
		e.exports[id] = fact
	}
	targetIDs = sortedUnique(targetIDs)
	fact.Targets = sortedUnique(append(fact.Targets, targetIDs...))
	if complete {
		fact.TargetObservations = append(fact.TargetObservations, slices.Clone(targetIDs))
	}
	fact.Incomplete = append(fact.Incomplete, reasons...)
	for _, item := range contributions {
		if item.typeOnly && space != SpaceType {
			continue
		}
		if !item.reexport && !declarationContributesSpace(item.sourceNode, space) {
			continue
		}
		fact.Via = append(fact.Via, item.site)
		if item.reexport && item.module != "" {
			fact.Reexports = append(fact.Reexports, ExportReexportFact{Site: item.site, Module: item.module, SourceName: item.sourceName, Space: space})
		}
		if location, ok := e.exportSiteLocation(item.site); ok && (fact.Location == (SourceLocation{}) || sourceLocationLess(location, fact.Location)) {
			fact.Location = location
		}
	}
	fact.Via = sortedUnique(fact.Via)
	fact.Reexports = uniqueReexports(fact.Reexports)
	fact.Incomplete = sortedUnique(fact.Incomplete)
}

func (e *extractor) exportSiteID(file *ast.SourceFile, node *ast.Node) string {
	position := scanner.GetTokenPosOfNode(node, file, false)
	return e.relativePath(file.FileName().AsString()) + "::export-site::" + strconv.Itoa(position)
}

func (e *extractor) addExportSite(site string, node *ast.Node) {
	if site == "" || node == nil {
		return
	}
	file := ast.GetSourceFileOfNode(node)
	if file == nil {
		return
	}
	for _, existing := range e.exportSites {
		if existing.ID == site {
			return
		}
	}
	e.exportSites = append(e.exportSites, ExportSiteFact{ID: site, Location: e.nodeLocation(file, node)})
}

func (e *extractor) exportSiteLocation(site string) (SourceLocation, bool) {
	for _, item := range e.imports {
		if item.SemanticKey == site {
			return item.Range, true
		}
	}
	for _, item := range e.exportSites {
		if item.ID == site {
			return item.Location, true
		}
	}
	return SourceLocation{}, false
}

func (e *extractor) canonicalFileIdentity(path string) string {
	var pathKey string
	if tspath.IsUrl(path) {
		pathKey = e.program.PathKeyForFileName(tspath.RootedFilePath(path)).AsString()
	} else {
		rooted := tspath.RootedFilePathFromAbsolute(path)
		pathKey = e.program.PathKeyForFileName(rooted).AsString()
	}
	pathKey = normalizeSlash(pathKey)
	root := normalizeSlash(e.program.PathKeyForFileName(tspath.RootedFilePathFromAbsolute(e.workspaceRoot)).AsString())
	if isWithinRoot(root, pathKey) {
		if relative, err := filepath.Rel(filepath.FromSlash(root), filepath.FromSlash(pathKey)); err == nil {
			return filepath.ToSlash(relative)
		}
	}
	return pathKey
}

func (e *extractor) sortedDeclarations() []DeclarationFact {
	result := make([]DeclarationFact, 0, len(e.declarations))
	for _, fact := range e.declarations {
		fact.Types = sortedDeclarationTypes(fact.Types)
		fact.Incomplete = sortedUnique(fact.Incomplete)
		if fact.TypesComplete && len(fact.TypeObservations) == 0 {
			fact.TypeObservations = [][]DeclarationType{sortedDeclarationTypes(fact.Types)}
		}
		fact.FunctionObservations = sortedUnique(fact.FunctionObservations)
		result = append(result, *fact)
	}
	slices.SortFunc(result, func(left, right DeclarationFact) int { return strings.Compare(left.ID, right.ID) })
	return result
}

func (e *extractor) sortedExports() []ExportFact {
	result := make([]ExportFact, 0, len(e.exports))
	for _, fact := range e.exports {
		fact.Targets = sortedUnique(fact.Targets)
		fact.Via = sortedUnique(fact.Via)
		fact.Incomplete = sortedUnique(fact.Incomplete)
		fact.Reexports = uniqueReexports(fact.Reexports)
		fact.TargetObservations = uniqueStringSets(fact.TargetObservations)
		result = append(result, *fact)
	}
	slices.SortFunc(result, func(left, right ExportFact) int { return strings.Compare(left.ID, right.ID) })
	return result
}

func exportSpaces(symbol *ast.Symbol) []string {
	if symbol == nil {
		return nil
	}
	flags := symbol.Flags()
	spaces := make([]string, 0, 2)
	if flags&ast.SymbolFlagsType != 0 {
		spaces = append(spaces, SpaceType)
	}
	if flags&(ast.SymbolFlagsValue|ast.SymbolFlagsNamespace) != 0 {
		spaces = append(spaces, SpaceValue)
	}
	return spaces
}

// exportedSymbol indexes each module once per extractor. Besides avoiding a
// repeated checker inventory, this lets both exports and source bindings use
// the same effective-space rules through re-export chains.
func (e *extractor) exportedSymbol(file *ast.SourceFile, name string) *ast.Symbol {
	if file == nil || file.Symbol == nil {
		return nil
	}
	if e.moduleExportIndex == nil {
		e.moduleExportIndex = make(map[*ast.SourceFile]map[string]*ast.Symbol)
	}
	index, ok := e.moduleExportIndex[file]
	if !ok {
		index = make(map[string]*ast.Symbol)
		for _, symbol := range e.checker.GetExportsOfModule(file.Symbol) {
			if symbol != nil {
				index[symbol.Name()] = symbol
			}
		}
		e.moduleExportIndex[file] = index
	}
	return index[name]
}

func (e *extractor) effectiveBindingSpaces(file *ast.SourceFile, exported *ast.Symbol) []string {
	if exported == nil {
		return nil
	}
	spaces := exportSpaces(e.checker.SkipAlias(exported))
	if len(spaces) == 0 {
		spaces = exportSpaces(exported)
	}
	if restricted, _ := e.bindingIsTypeOnly(file, exported.Name(), exported, make(map[*ast.SourceFile]bool)); restricted {
		return []string{SpaceType}
	}
	return spaces
}

func (e *extractor) bindingIsTypeOnly(file *ast.SourceFile, name string, symbol *ast.Symbol, visiting map[*ast.SourceFile]bool) (bool, bool) {
	seen := make(map[*ast.Symbol]bool)
	for current := symbol; current != nil && current.Flags()&ast.SymbolFlagsAlias != 0 && !seen[current]; {
		seen[current] = true
		if e.checker.GetTypeOnlyAliasDeclaration(current) != nil {
			return true, true
		}
		current = e.checker.GetImmediateAliasedSymbol(current)
	}
	if file == nil || file.Symbol == nil || file.Symbol.Exports()[name] != nil {
		return false, true
	}
	if visiting[file] {
		return false, false
	}
	visiting[file] = true
	defer delete(visiting, file)
	found, unrestricted := false, false
	e.forEachStarBinding(file, name, func(star *ast.Node, source *ast.SourceFile, exported *ast.Symbol) bool {
		restricted, known := e.bindingIsTypeOnly(source, name, exported, visiting)
		if !known {
			return true
		}
		found = true
		if !star.IsTypeOnly() && !restricted {
			unrestricted = true
			return false
		}
		return true
	})
	return found && !unrestricted, found
}

func (e *extractor) forEachStarBinding(file *ast.SourceFile, name string, visit func(*ast.Node, *ast.SourceFile, *ast.Symbol) bool) {
	if file == nil || file.Symbol == nil || name == "default" || name == "export=" {
		return
	}
	stars := file.Symbol.Exports()[ast.InternalSymbolNameExportStar]
	if stars == nil {
		return
	}
	for _, star := range stars.Declarations() {
		if star.ModuleSpecifier() == nil {
			continue
		}
		resolved := e.resolveModuleSpecifier(file, star.ModuleSpecifier())
		if resolved == nil || !resolved.IsResolved() {
			continue
		}
		source := e.program.GetSourceFile(resolved.ResolvedFileName)
		if exported := e.exportedSymbol(source, name); exported != nil && !visit(star, source, exported) {
			return
		}
	}
}

func (e *extractor) starBindingAmbiguous(file *ast.SourceFile, name string) bool {
	return e.starBindingAmbiguousFrom(file, name, make(map[bindingVisit]bool))
}

type bindingVisit struct {
	file *ast.SourceFile
	name string
}

func (e *extractor) starBindingAmbiguousFrom(file *ast.SourceFile, name string, visiting map[bindingVisit]bool) bool {
	if file == nil || file.Symbol == nil {
		return false
	}
	key := bindingVisit{file, name}
	if visiting[key] {
		return false
	}
	visiting[key] = true
	defer delete(visiting, key)
	if explicit := file.Symbol.Exports()[name]; explicit != nil {
		seen := make(map[*ast.Symbol]bool)
		for current := explicit; current != nil && current.Flags()&ast.SymbolFlagsAlias != 0 && !seen[current]; current = e.checker.GetImmediateAliasedSymbol(current) {
			seen[current] = true
			for _, declaration := range current.Declarations() {
				if declaration == nil || declaration.Name() == nil || declaration.Kind != ast.KindExportSpecifier && declaration.Kind != ast.KindImportSpecifier {
					continue
				}
				sourceName := declaration.Name().Text()
				if declaration.Kind == ast.KindExportSpecifier && declaration.AsExportSpecifier().PropertyName != nil {
					sourceName = declaration.AsExportSpecifier().PropertyName.Text()
				} else if declaration.Kind == ast.KindImportSpecifier && declaration.AsImportSpecifier().PropertyName != nil {
					sourceName = declaration.AsImportSpecifier().PropertyName.Text()
				}
				owner := declaration.Parent
				for owner != nil && owner.Kind != ast.KindExportDeclaration && owner.Kind != ast.KindImportDeclaration && owner.Kind != ast.KindSourceFile {
					owner = owner.Parent
				}
				if owner == nil || owner.Kind == ast.KindSourceFile || owner.ModuleSpecifier() == nil {
					continue
				}
				origin := ast.GetSourceFileOfNode(owner)
				resolved := e.resolveModuleSpecifier(origin, owner.ModuleSpecifier())
				if resolved != nil && resolved.IsResolved() && e.starBindingAmbiguousFrom(e.program.GetSourceFile(resolved.ResolvedFileName), sourceName, visiting) {
					return true
				}
			}
		}
		return false
	}
	var first []string
	ambiguous := false
	e.forEachStarBinding(file, name, func(_ *ast.Node, source *ast.SourceFile, exported *ast.Symbol) bool {
		if e.starBindingAmbiguousFrom(source, name, visiting) {
			ambiguous = true
			return false
		}
		resolved := e.checker.SkipAlias(exported)
		if resolved == nil {
			return true
		}
		// The checker reports a star collision for distinct symbols even when
		// one is type-only and the other has a value meaning.
		ids := e.declarationIDs(resolved.Declarations())
		if len(ids) == 0 {
			return true
		}
		if first == nil {
			first = ids
		} else if !slices.Equal(first, ids) {
			ambiguous = true
			return false
		}
		return true
	})
	return ambiguous
}

func declarationsInSpace(symbol *ast.Symbol, space string) []*ast.Node {
	if symbol == nil {
		return nil
	}
	var result []*ast.Node
	for _, node := range symbol.Declarations() {
		if node == nil {
			continue
		}
		switch node.Kind {
		case ast.KindModuleDeclaration, ast.KindSourceFile, ast.KindNamespaceExport:
			// Namespace-only bindings can be imported with `export type * as`.
			// A namespace merged with a class is not the class's type declaration.
			if space == SpaceType && symbol.Flags()&ast.SymbolFlagsType != 0 {
				continue
			}
		default:
			if !declarationContributesSpace(node, space) {
				continue
			}
		}
		result = append(result, node)
	}
	return result
}

func declarationContributesSpace(node *ast.Node, space string) bool {
	if node == nil {
		return true
	}
	switch node.Kind {
	case ast.KindInterfaceDeclaration, ast.KindTypeAliasDeclaration, ast.KindJSTypeAliasDeclaration:
		return space == SpaceType
	case ast.KindVariableDeclaration, ast.KindBindingElement, ast.KindFunctionDeclaration,
		ast.KindModuleDeclaration, ast.KindSourceFile, ast.KindNamespaceExport,
		ast.KindMethodDeclaration, ast.KindPropertyDeclaration, ast.KindPropertySignature, ast.KindExportAssignment:
		return space == SpaceValue
	default:
		return true
	}
}

func declarationKind(node *ast.Node) (string, bool) {
	switch node.Kind {
	case ast.KindFunctionDeclaration:
		return "function", true
	case ast.KindVariableDeclaration, ast.KindBindingElement:
		return "variable", true
	case ast.KindTypeAliasDeclaration, ast.KindJSTypeAliasDeclaration:
		return "type_alias", true
	case ast.KindInterfaceDeclaration:
		return "interface", true
	case ast.KindClassDeclaration:
		return "class", true
	case ast.KindEnumDeclaration:
		return "enum", true
	case ast.KindModuleDeclaration:
		return "namespace", true
	case ast.KindSourceFile:
		return "namespace", true
	case ast.KindNamespaceExport:
		return "namespace", true
	case ast.KindMethodDeclaration:
		return "method", true
	case ast.KindPropertyDeclaration, ast.KindPropertySignature:
		return "property", true
	case ast.KindExportAssignment:
		return "export_expression", true
	case ast.KindCallSignature, ast.KindConstructSignature, ast.KindIndexSignature, ast.KindMethodSignature, ast.KindFunctionType, ast.KindConstructorType:
		return "signature", true
	default:
		return node.Kind.String(), false
	}
}

func sortedDeclarationTypes(items []DeclarationType) []DeclarationType {
	result := slices.Clone(items)
	slices.SortFunc(result, func(left, right DeclarationType) int {
		if left.Space != right.Space {
			return strings.Compare(left.Space, right.Space)
		}
		return strings.Compare(left.Type, right.Type)
	})
	return slices.Compact(result)
}

func sortedUnique(items []string) []string {
	result := slices.Clone(items)
	slices.Sort(result)
	return slices.Compact(result)
}

func uniqueStringSets(items [][]string) [][]string {
	result := make([][]string, 0, len(items))
	seen := make(map[string]struct{}, len(items))
	for _, item := range items {
		item = sortedUnique(item)
		key := strings.Join(item, "\x00")
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		result = append(result, item)
	}
	slices.SortFunc(result, func(left, right []string) int {
		return strings.Compare(strings.Join(left, "\x00"), strings.Join(right, "\x00"))
	})
	return result
}

func uniqueReexports(items []ExportReexportFact) []ExportReexportFact {
	seen := make(map[ExportReexportFact]struct{}, len(items))
	result := make([]ExportReexportFact, 0, len(items))
	for _, item := range items {
		if _, ok := seen[item]; ok {
			continue
		}
		seen[item] = struct{}{}
		result = append(result, item)
	}
	slices.SortFunc(result, func(left, right ExportReexportFact) int {
		return strings.Compare(jsonList(jsonQuote(left.Site), jsonQuote(left.Module), jsonQuote(left.SourceName), jsonQuote(left.Space)), jsonList(jsonQuote(right.Site), jsonQuote(right.Module), jsonQuote(right.SourceName), jsonQuote(right.Space)))
	})
	return result
}

func sourceLocationLess(left SourceLocation, right SourceLocation) bool {
	if left.File != right.File {
		return left.File < right.File
	}
	if left.StartLine != right.StartLine {
		return left.StartLine < right.StartLine
	}
	return left.StartColumn < right.StartColumn
}

func exportEntityID(file, name, space string) string {
	key := jsonList(jsonQuote("export-v1"), jsonQuote(file), jsonQuote(name), jsonQuote(space))
	return "export:" + hashKey(key)
}

func finalizeSemanticObservations(snapshot *Snapshot) {
	snapshot.Declarations = mergeDeclarationObservations(snapshot.Declarations)
	snapshot.Exports = mergeExportObservations(snapshot.Exports)
	snapshot.ModuleExportsIncomplete = moduleInventoryOutcomes(snapshot.ModuleExportObservations)
	snapshot.FnSignatureConflicts = nil
	for index := range snapshot.Functions {
		snapshot.Functions[index] = mergeFunctionObservations(snapshot.Functions[index], Function{})
		if snapshot.Functions[index].SignatureConflict {
			snapshot.FnSignatureConflicts = append(snapshot.FnSignatureConflicts, snapshot.Functions[index].SemanticKey)
		}
	}
	snapshot.FnSignatureConflicts = sortedUnique(snapshot.FnSignatureConflicts)
	conflictedFunctions := make(map[string]struct{}, len(snapshot.FnSignatureConflicts))
	for _, function := range snapshot.FnSignatureConflicts {
		conflictedFunctions[function] = struct{}{}
	}
	for index := range snapshot.Declarations {
		if snapshot.Declarations[index].Function != "" {
			if _, conflict := conflictedFunctions[snapshot.Declarations[index].Function]; conflict {
				snapshot.Declarations[index].Incomplete = sortedUnique(append(snapshot.Declarations[index].Incomplete, AssociationConflict))
			}
		}
	}
	snapshot.SourceBindings = mergeSourceBindingObservations(snapshot.SourceBindings)
	snapshot.DeclarationSourceCandidates = uniqueDeclarationSources(snapshot.DeclarationSourceCandidates)
	snapshot.DeclarationSources, snapshot.DeclarationSourceOutcomes = finalizeDeclarationSourceObservations(snapshot.DeclarationSources, snapshot.DeclarationSourceOutcomes)
	snapshot.SourceBindings = conflictSourceBindingsWithMaps(snapshot.SourceBindings, snapshot.DeclarationSources)
	snapshot.SourceEntries = mergeSourceEntryFacts(snapshot.SourceEntries)
}

func uniqueSnapshotInputs(items []SnapshotInput) []SnapshotInput {
	byPath := make(map[string]SnapshotInput, len(items))
	for _, item := range items {
		current := byPath[item.Path]
		current.Path = item.Path
		current.Exists = current.Exists || item.Exists
		current.OptionalMap = current.OptionalMap || item.OptionalMap
		byPath[item.Path] = current
	}
	result := make([]SnapshotInput, 0, len(byPath))
	for _, item := range byPath {
		result = append(result, item)
	}
	slices.SortFunc(result, func(left, right SnapshotInput) int { return strings.Compare(left.Path, right.Path) })
	return result
}

func uniqueDeclarationSources(items []DeclarationSourceFact) []DeclarationSourceFact {
	seen := make(map[DeclarationSourceFact]struct{}, len(items))
	result := make([]DeclarationSourceFact, 0, len(items))
	for _, item := range items {
		if _, ok := seen[item]; ok {
			continue
		}
		seen[item] = struct{}{}
		result = append(result, item)
	}
	slices.SortFunc(result, func(left, right DeclarationSourceFact) int {
		return strings.Compare(left.CheckedDecl+"\x00"+left.SourceDecl+"\x00"+left.Method, right.CheckedDecl+"\x00"+right.SourceDecl+"\x00"+right.Method)
	})
	return result
}

func uniqueDeclarationSourceOutcomes(items []DeclarationSourceOutcomeFact) []DeclarationSourceOutcomeFact {
	seen := make(map[DeclarationSourceOutcomeFact]struct{}, len(items))
	result := make([]DeclarationSourceOutcomeFact, 0, len(items))
	for _, item := range items {
		if _, ok := seen[item]; ok {
			continue
		}
		seen[item] = struct{}{}
		result = append(result, item)
	}
	slices.SortFunc(result, func(left, right DeclarationSourceOutcomeFact) int {
		return strings.Compare(left.CheckedDecl+"\x00"+left.Outcome, right.CheckedDecl+"\x00"+right.Outcome)
	})
	return result
}

func mergeSourceEntryFacts(items []SourceEntryFact) []SourceEntryFact {
	byID := make(map[string]SourceEntryFact, len(items))
	for _, item := range items {
		current, exists := byID[item.ID]
		if !exists {
			byID[item.ID] = item
			continue
		}
		current.Applied = current.Applied || item.Applied
		if current.CheckedEntry == "" {
			current.CheckedEntry = item.CheckedEntry
		}
		if current.SourceEntry == "" {
			current.SourceEntry = item.SourceEntry
		}
		if current.Tsconfig == "" {
			current.Tsconfig = item.Tsconfig
		}
		current.Incomplete = sortedUnique(append(current.Incomplete, item.Incomplete...))
		byID[item.ID] = current
	}
	result := make([]SourceEntryFact, 0, len(byID))
	for _, item := range byID {
		result = append(result, item)
	}
	slices.SortFunc(result, func(left, right SourceEntryFact) int { return strings.Compare(left.ID, right.ID) })
	return result
}

func finalizeDeclarationSourceObservations(links []DeclarationSourceFact, outcomes []DeclarationSourceOutcomeFact) ([]DeclarationSourceFact, []DeclarationSourceOutcomeFact) {
	byCheckedLinks := make(map[string][]DeclarationSourceFact)
	byCheckedOutcomes := make(map[string][]string)
	for _, item := range uniqueDeclarationSources(links) {
		byCheckedLinks[item.CheckedDecl] = append(byCheckedLinks[item.CheckedDecl], item)
	}
	for _, item := range outcomes {
		byCheckedOutcomes[item.CheckedDecl] = append(byCheckedOutcomes[item.CheckedDecl], item.Outcome)
	}
	checked := make(map[string]struct{}, len(byCheckedLinks)+len(byCheckedOutcomes))
	for id := range byCheckedLinks {
		checked[id] = struct{}{}
	}
	for id := range byCheckedOutcomes {
		checked[id] = struct{}{}
	}
	var finalLinks []DeclarationSourceFact
	var finalOutcomes []DeclarationSourceOutcomeFact
	for id := range checked {
		candidates := byCheckedLinks[id]
		outcomesForID := sortedUnique(byCheckedOutcomes[id])
		conflict := len(candidates) > 1 || len(outcomesForID) > 1 || len(candidates) > 0 && len(outcomesForID) > 0
		if conflict {
			finalOutcomes = append(finalOutcomes, DeclarationSourceOutcomeFact{CheckedDecl: id, Outcome: AssociationConflict})
			continue
		}
		if len(candidates) == 1 {
			finalLinks = append(finalLinks, candidates[0])
		}
		if len(outcomesForID) == 1 {
			finalOutcomes = append(finalOutcomes, DeclarationSourceOutcomeFact{CheckedDecl: id, Outcome: outcomesForID[0]})
		}
	}
	return uniqueDeclarationSources(finalLinks), uniqueDeclarationSourceOutcomes(finalOutcomes)
}

func mergeSourceBindingObservations(items []SourceBindingFact) []SourceBindingFact {
	byID := make(map[string]SourceBindingFact, len(items))
	for _, item := range items {
		current, exists := byID[item.ID]
		if !exists {
			byID[item.ID] = item
			continue
		}
		current.CheckedDeclarations = sortedUnique(append(current.CheckedDeclarations, item.CheckedDeclarations...))
		current.SourceDeclarations = sortedUnique(append(current.SourceDeclarations, item.SourceDeclarations...))
		current.CheckedComplete = current.CheckedComplete && item.CheckedComplete
		current.SourceComplete = current.SourceComplete && item.SourceComplete
		current.CheckedObservations = uniqueStringSets(append(current.CheckedObservations, item.CheckedObservations...))
		current.SourceObservations = uniqueStringSets(append(current.SourceObservations, item.SourceObservations...))
		current.SourceFiles = sortedUnique(append(current.SourceFiles, item.SourceFiles...))
		if item.SourceFile != "" {
			current.SourceFiles = sortedUnique(append(current.SourceFiles, item.SourceFile))
		}
		current.Incomplete = sortedUnique(append(current.Incomplete, item.Incomplete...))
		if current.SourceFile == "" {
			current.SourceFile = item.SourceFile
			current.SourceName = item.SourceName
			current.Method = item.Method
		}
		byID[item.ID] = current
	}
	result := make([]SourceBindingFact, 0, len(byID))
	for _, item := range byID {
		item.CheckedDeclarations = sortedUnique(item.CheckedDeclarations)
		item.SourceDeclarations = sortedUnique(item.SourceDeclarations)
		if item.SourceFile != "" {
			item.SourceFiles = sortedUnique(append(item.SourceFiles, item.SourceFile))
		}
		item.Incomplete = sortedUnique(item.Incomplete)
		item.SourceObservations = uniqueStringSets(item.SourceObservations)
		item.CheckedObservations = uniqueStringSets(item.CheckedObservations)
		if stringSetsDisagree(item.SourceObservations) || stringSetsDisagree(item.CheckedObservations) {
			item.Incomplete = sortedUnique(append(item.Incomplete, AssociationConflict))
		}
		if len(item.SourceFiles) > 1 {
			item.Incomplete = sortedUnique(append(item.Incomplete, AssociationConflict))
			item.SourceFile = ""
			item.SourceName = ""
			item.Method = ""
		} else if len(item.SourceFiles) == 1 {
			item.SourceFile = item.SourceFiles[0]
		}
		result = append(result, item)
	}
	slices.SortFunc(result, func(left, right SourceBindingFact) int { return strings.Compare(left.ID, right.ID) })
	return result
}

// conflictSourceBindingsWithMaps marks a binding /conflict when a precise map
// link leaves its observed source declaration set. A failed attempt observed
// no source set, so there is nothing to compare.
func conflictSourceBindingsWithMaps(bindings []SourceBindingFact, links []DeclarationSourceFact) []SourceBindingFact {
	for index := range bindings {
		if len(bindings[index].SourceDeclarations) == 0 {
			continue
		}
		for _, link := range links {
			if !slices.Contains(bindings[index].CheckedDeclarations, link.CheckedDecl) || slices.Contains(bindings[index].SourceDeclarations, link.SourceDecl) {
				continue
			}
			bindings[index].Incomplete = sortedUnique(append(bindings[index].Incomplete, AssociationConflict))
		}
	}
	return bindings
}

func mergeDeclarationObservations(items []DeclarationFact) []DeclarationFact {
	byID := make(map[string]DeclarationFact, len(items))
	for _, item := range items {
		current, exists := byID[item.ID]
		if !exists {
			byID[item.ID] = item
			continue
		}
		current.Types = sortedDeclarationTypes(append(current.Types, item.Types...))
		current.TypeObservations = append(current.TypeObservations, item.TypeObservations...)
		current.Incomplete = sortedUnique(append(current.Incomplete, item.Incomplete...))
		current.TypesComplete = current.TypesComplete || item.TypesComplete
		current.FunctionObservations = append(current.FunctionObservations, item.FunctionObservations...)
		if len(item.FunctionObservations) == 0 && item.Function != "" {
			current.FunctionObservations = append(current.FunctionObservations, item.Function)
		}
		byID[item.ID] = current
	}
	result := make([]DeclarationFact, 0, len(byID))
	for _, item := range byID {
		item.TypeObservations = uniqueDeclarationSets(item.TypeObservations)
		if item.TypesComplete && declarationSetsDisagree(item.TypeObservations) {
			item.Incomplete = sortedUnique(append(item.Incomplete, AssociationConflict))
		}
		if len(item.FunctionObservations) == 0 && item.Function != "" {
			item.FunctionObservations = append(item.FunctionObservations, item.Function)
		}
		item.FunctionObservations = sortedUnique(item.FunctionObservations)
		switch len(item.FunctionObservations) {
		case 0:
			item.Function = ""
		case 1:
			item.Function = item.FunctionObservations[0]
		default:
			item.Function = ""
			item.Incomplete = sortedUnique(append(item.Incomplete, AssociationConflict))
		}
		result = append(result, item)
	}
	slices.SortFunc(result, func(left, right DeclarationFact) int { return strings.Compare(left.ID, right.ID) })
	return result
}

func uniqueDeclarationSets(items [][]DeclarationType) [][]DeclarationType {
	result := make([][]DeclarationType, 0, len(items))
	seen := make(map[string]struct{}, len(items))
	for _, item := range items {
		item = sortedDeclarationTypes(item)
		parts := make([]string, len(item))
		for index, value := range item {
			parts[index] = value.Space + "\x00" + value.Type
		}
		key := strings.Join(parts, "\x01")
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		result = append(result, item)
	}
	slices.SortFunc(result, func(left, right []DeclarationType) int {
		return strings.Compare(declarationSetKey(left), declarationSetKey(right))
	})
	return result
}

func declarationSetKey(items []DeclarationType) string {
	parts := make([]string, len(items))
	for index, item := range items {
		parts[index] = item.Space + "\x00" + item.Type
	}
	return strings.Join(parts, "\x01")
}

func declarationSetsDisagree(items [][]DeclarationType) bool {
	if len(items) < 2 {
		return false
	}
	want := declarationSetKey(items[0])
	for _, item := range items[1:] {
		if declarationSetKey(item) != want {
			return true
		}
	}
	return false
}

func mergeExportObservations(items []ExportFact) []ExportFact {
	byID := make(map[string]ExportFact, len(items))
	for _, item := range items {
		current, exists := byID[item.ID]
		if !exists {
			byID[item.ID] = item
			continue
		}
		current.Targets = sortedUnique(append(current.Targets, item.Targets...))
		current.Via = sortedUnique(append(current.Via, item.Via...))
		current.Reexports = uniqueReexports(append(current.Reexports, item.Reexports...))
		current.Incomplete = sortedUnique(append(current.Incomplete, item.Incomplete...))
		current.TargetObservations = uniqueStringSets(append(current.TargetObservations, item.TargetObservations...))
		if current.Location == (SourceLocation{}) || item.Location != (SourceLocation{}) && sourceLocationLess(item.Location, current.Location) {
			current.Location = item.Location
		}
		byID[item.ID] = current
	}
	result := make([]ExportFact, 0, len(byID))
	for _, item := range byID {
		if stringSetsDisagree(item.TargetObservations) {
			item.Incomplete = sortedUnique(append(item.Incomplete, AssociationConflict))
		}
		result = append(result, item)
	}
	slices.SortFunc(result, func(left, right ExportFact) int { return strings.Compare(left.ID, right.ID) })
	return result
}

func stringSetsDisagree(items [][]string) bool {
	if len(items) < 2 {
		return false
	}
	want := strings.Join(items[0], "\x00")
	for _, item := range items[1:] {
		if strings.Join(item, "\x00") != want {
			return true
		}
	}
	return false
}

func moduleInventoryOutcomes(observations []ModuleExportObservation) []ModuleExportsIncompleteFact {
	byIdentity := make(map[string][]ModuleExportObservation)
	retained := make(map[string]bool)
	fileByIdentity := make(map[string]string)
	for _, observation := range observations {
		identity := observation.Identity
		if identity == "" {
			identity = observation.File
		}
		byIdentity[identity] = append(byIdentity[identity], observation)
		retained[identity] = retained[identity] || observation.Retained
		if observation.File != "" && (fileByIdentity[identity] == "" || observation.File < fileByIdentity[identity]) {
			fileByIdentity[identity] = observation.File
		}
	}
	reasonsByIdentity := make(map[string]map[string]struct{})
	for identity, values := range byIdentity {
		for _, observation := range values {
			for _, reason := range observation.Reasons {
				if reasonsByIdentity[identity] == nil {
					reasonsByIdentity[identity] = make(map[string]struct{})
				}
				reasonsByIdentity[identity][reason] = struct{}{}
			}
		}
		var complete [][]string
		for _, observation := range values {
			if observation.Retained && observation.Complete {
				complete = append(complete, observation.Bindings)
			}
		}
		if stringSetsDisagree(uniqueStringSets(complete)) {
			if reasonsByIdentity[identity] == nil {
				reasonsByIdentity[identity] = make(map[string]struct{})
			}
			reasonsByIdentity[identity][AssociationConflict] = struct{}{}
		}
	}
	// Propagate incompleteness only over direct star edges. The loop reaches
	// a fixed point and terminates for cyclic re-export graphs.
	for changed := true; changed; {
		changed = false
		for identity, values := range byIdentity {
			for _, observation := range values {
				for _, source := range observation.StarSources {
					for reason := range reasonsByIdentity[source] {
						if reasonsByIdentity[identity] == nil {
							reasonsByIdentity[identity] = make(map[string]struct{})
						}
						if _, exists := reasonsByIdentity[identity][reason]; !exists {
							reasonsByIdentity[identity][reason] = struct{}{}
							changed = true
						}
					}
				}
			}
		}
	}
	var result []ModuleExportsIncompleteFact
	for identity, reasons := range reasonsByIdentity {
		if !retained[identity] {
			continue
		}
		for reason := range reasons {
			file := fileByIdentity[identity]
			if file == "" {
				file = identity
			}
			result = append(result, ModuleExportsIncompleteFact{File: file, Reason: reason})
		}
	}
	slices.SortFunc(result, func(left, right ModuleExportsIncompleteFact) int {
		return strings.Compare(left.File+"\x00"+left.Reason, right.File+"\x00"+right.Reason)
	})
	return result
}

func mergeFunctionObservations(existing, incoming Function) Function {
	if existing.SemanticKey == "" {
		return incoming
	}
	if incoming.SemanticKey != "" {
		existing.SignatureObservations = append(existing.SignatureObservations, incoming.SignatureObservations...)
	}
	byKey := make(map[string]FunctionSignatureObservation)
	for _, observation := range existing.SignatureObservations {
		key := functionSignatureKey(observation)
		byKey[key] = observation
	}
	existing.SignatureObservations = existing.SignatureObservations[:0]
	for _, observation := range byKey {
		existing.SignatureObservations = append(existing.SignatureObservations, observation)
	}
	slices.SortFunc(existing.SignatureObservations, func(left, right FunctionSignatureObservation) int {
		return strings.Compare(functionSignatureKey(left), functionSignatureKey(right))
	})
	var complete []string
	for _, observation := range existing.SignatureObservations {
		if observation.Complete {
			complete = append(complete, functionSignatureKey(observation))
		}
	}
	existing.SignatureConflict = len(sortedUnique(complete)) > 1
	existing.ParameterTypes = nil
	existing.ReturnType = ""
	if len(existing.SignatureObservations) == 1 && existing.SignatureObservations[0].Complete {
		observation := existing.SignatureObservations[0]
		existing.ParameterTypes = slices.Clone(observation.ParameterTypes)
		existing.ReturnType = observation.ReturnType
	}
	return existing
}

func functionSignatureKey(observation FunctionSignatureObservation) string {
	params := make([]string, len(observation.ParameterTypes))
	for index, param := range observation.ParameterTypes {
		mark := ""
		if index < len(observation.ParameterMarks) {
			mark = observation.ParameterMarks[index]
		}
		params[index] = jsonList(jsonQuote(mark), jsonQuote(param))
	}
	return jsonList(jsonQuote(strconv.FormatBool(observation.Complete)), jsonList(params...), jsonQuote(observation.ReturnType))
}

func (e *extractor) functionSignatureObservation(node *ast.Node, params []string, returns string) FunctionSignatureObservation {
	marks := make([]string, len(params))
	if signature := e.checker.GetSignatureFromDeclaration(node); signature != nil {
		for index := range marks {
			if signature.HasRestParameter() && index == len(marks)-1 {
				marks[index] = MarkRest
			} else if index >= signature.MinArgumentCount() {
				marks[index] = MarkOptional
			}
		}
	}
	complete := returns != "" && !slices.Contains(params, "")
	return FunctionSignatureObservation{ParameterTypes: slices.Clone(params), ParameterMarks: marks, ReturnType: returns, Complete: complete}
}
