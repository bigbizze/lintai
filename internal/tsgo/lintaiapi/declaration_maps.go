package lintaiapi

import (
	"encoding/json"
	"path/filepath"
	"slices"
	"strings"

	"github.com/bigbizze/lintai/internal/tsgo/internal/ast"
	"github.com/bigbizze/lintai/internal/tsgo/internal/core"
	"github.com/bigbizze/lintai/internal/tsgo/internal/parser"
	"github.com/bigbizze/lintai/internal/tsgo/internal/scanner"
	"github.com/bigbizze/lintai/internal/tsgo/internal/sourcemap"
	"github.com/bigbizze/lintai/internal/tsgo/internal/tspath"
	"github.com/bigbizze/lintai/internal/tsgo/internal/vfs"
)

type declarationMapHost struct {
	fs              vfs.FS
	caseSensitivity tspath.CaseSensitivity
}

func (h declarationMapHost) CaseSensitivity() tspath.CaseSensitivity { return h.caseSensitivity }

func (h declarationMapHost) ReadFile(path tspath.RootedFilePath) (string, bool) {
	return h.fs.ReadFile(path)
}

func (h declarationMapHost) GetECMALineInfo(path tspath.RootedFilePath) *sourcemap.ECMALineInfo {
	text, ok := h.ReadFile(path)
	if !ok {
		return nil
	}
	file := parser.ParseSourceFile(ast.SourceFileParseOptions{FileName: path}, text, core.EnsureScriptKindFromFileName(path))
	return sourcemap.CreateECMALineInfo(text, file.ECMALineMap())
}

type rawDeclarationMap struct {
	Version        int               `json:"version"`
	File           string            `json:"file"`
	SourceRoot     *string           `json:"sourceRoot"`
	Sources        []*string         `json:"sources"`
	SourcesContent []json.RawMessage `json:"sourcesContent"`
	Mappings       string            `json:"mappings"`
}

type exactMapSegment struct {
	sourcePath string
	sourcePos  int
}

func (e *extractor) recoverDeclarationMap(checked *ast.Node, requested bool, publicName string) {
	if checked == nil {
		return
	}
	checkedFile := ast.GetSourceFileOfNode(checked)
	if checkedFile == nil || !checkedFile.IsDeclarationFile {
		return
	}
	checkedID := e.declarationID(checked)
	if checkedID == "" {
		return
	}
	if e.mapAttempted == nil {
		e.mapAttempted = make(map[string]struct{})
	}
	if _, done := e.mapAttempted[checkedID]; done {
		return
	}
	e.mapAttempted[checkedID] = struct{}{}
	generatedPath := tspath.RootedFilePathFromAbsolute(checkedFile.FileName().AsString())
	generatedText, ok := e.host.FS().ReadFile(generatedPath)
	if !ok {
		if requested {
			e.addMapOutcome(checkedID, "unavailable_map")
		}
		return
	}
	mapPath, discovered, candidates, earlyOutcome := e.findDeclarationMap(generatedPath, generatedText)
	for _, candidate := range candidates {
		e.inputs = append(e.inputs, SnapshotInput{Path: candidate, Exists: e.host.FS().FileExists(tspath.RootedFilePathFromAbsolute(candidate))})
	}
	if earlyOutcome != "" {
		e.addMapOutcome(checkedID, earlyOutcome)
		return
	}
	if mapPath == "" {
		if requested {
			outcome := "missing_map"
			if discovered {
				outcome = "unavailable_map"
			}
			e.addMapOutcome(checkedID, outcome)
		} else {
			delete(e.mapAttempted, checkedID)
		}
		return
	}
	e.inputs = append(e.inputs, SnapshotInput{Path: normalizeSlash(mapPath), Exists: true, OptionalMap: true})
	mapRooted := tspath.RootedFilePathFromAbsolute(mapPath)
	mapText, ok := e.host.FS().ReadFile(mapRooted)
	if !ok {
		e.addMapOutcome(checkedID, "unavailable_map")
		return
	}
	var raw rawDeclarationMap
	if err := json.Unmarshal([]byte(mapText), &raw); err != nil || raw.Version != 3 || raw.File == "" || len(raw.Sources) == 0 {
		e.addMapOutcome(checkedID, "malformed_map")
		return
	}
	for _, content := range raw.SourcesContent {
		if len(content) > 0 && string(content) != "null" {
			e.addMapOutcome(checkedID, "unsupported")
			return
		}
	}
	for _, source := range raw.Sources {
		if source == nil || *source == "" {
			e.addMapOutcome(checkedID, "malformed_map")
			return
		}
	}
	start := scanner.GetTokenPosOfNode(checked, checkedFile, false)
	generatedLine, generatedColumn := scanner.GetECMALineAndUTF16CharacterOfPosition(checkedFile, start)
	decoder := sourcemap.DecodeMappings(raw.Mappings)
	var exact []exactMapSegment
	seenSegments := make(map[exactMapSegment]struct{})
	mapHost := declarationMapHost{fs: e.host.FS(), caseSensitivity: e.host.FS().CaseSensitivity()}
	for mapping := range decoder.Values() {
		if mapping.GeneratedLine != generatedLine || int(mapping.GeneratedCharacter) != int(generatedColumn) {
			continue
		}
		if !mapping.IsSourceMapping() {
			continue
		}
		if int(mapping.SourceIndex) < 0 || int(mapping.SourceIndex) >= len(raw.Sources) {
			e.addMapOutcome(checkedID, "malformed_map")
			return
		}
		sourcePath, ok := declarationMapSourcePath(mapPath, raw.SourceRoot, *raw.Sources[mapping.SourceIndex])
		if !ok {
			e.addMapOutcome(checkedID, "unsupported")
			return
		}
		sourceRooted := tspath.RootedFilePathFromAbsolute(sourcePath)
		sourceText, exists := e.host.FS().ReadFile(sourceRooted)
		e.inputs = append(e.inputs, SnapshotInput{Path: normalizeSlash(sourcePath), Exists: exists})
		if !exists {
			e.addMapOutcome(checkedID, "unavailable_source")
			return
		}
		sourceFile := parser.ParseSourceFile(ast.SourceFileParseOptions{FileName: sourceRooted}, sourceText, core.EnsureScriptKindFromFileName(sourceRooted))
		if len(sourceFile.Diagnostics()) > 0 {
			e.addMapOutcome(checkedID, "unavailable_source")
			return
		}
		if mapping.SourceLine >= len(sourceFile.ECMALineMap()) {
			e.addMapOutcome(checkedID, "malformed_map")
			return
		}
		sourcePos := scanner.ComputePositionOfLineAndUTF16Character(sourceFile.ECMALineMap(), mapping.SourceLine, mapping.SourceCharacter, sourceText, true)
		actualLine, actualColumn := scanner.GetECMALineAndUTF16CharacterOfPosition(sourceFile, sourcePos)
		if actualLine != mapping.SourceLine || actualColumn != mapping.SourceCharacter {
			e.addMapOutcome(checkedID, "malformed_map")
			return
		}
		segment := exactMapSegment{sourcePath: sourcePath, sourcePos: sourcePos}
		if _, exists := seenSegments[segment]; !exists {
			seenSegments[segment] = struct{}{}
			exact = append(exact, segment)
		}
	}
	if decoder.Error() != nil {
		e.addMapOutcome(checkedID, "malformed_map")
		return
	}
	if len(exact) == 0 {
		e.addMapOutcome(checkedID, "unresolved")
		return
	}
	mapper := sourcemap.GetDocumentPositionMapper(mapHost, generatedPath)
	if mapper == nil {
		e.addMapOutcome(checkedID, "malformed_map")
		return
	}
	generatedPosition := &sourcemap.DocumentPosition{FileName: generatedPath, Pos: start}
	var mapperAnchorValid bool
	for _, segment := range exact {
		sourceRooted := tspath.RootedFilePathFromAbsolute(segment.sourcePath)
		mapped := mapper.GetSourcePosition(generatedPosition)
		if mapped != nil && e.program.PathKeyForFileName(mapped.FileName) == e.program.PathKeyForFileName(sourceRooted) && mapped.Pos == segment.sourcePos {
			back := mapper.GetGeneratedPosition(mapped)
			mapperAnchorValid = back != nil && e.program.PathKeyForFileName(back.FileName) == e.program.PathKeyForFileName(generatedPath) && back.Pos == start
			if mapperAnchorValid {
				break
			}
		}
	}
	if !mapperAnchorValid {
		e.addMapOutcome(checkedID, "unresolved")
		return
	}
	var candidateIDs []string
	var unsupportedAtAnchor bool
	for _, segment := range exact {
		sourceRooted := tspath.RootedFilePathFromAbsolute(segment.sourcePath)
		sourceText, _ := e.host.FS().ReadFile(sourceRooted)
		sourceFile := parser.ParseSourceFile(ast.SourceFileParseOptions{FileName: sourceRooted}, sourceText, core.EnsureScriptKindFromFileName(sourceRooted))
		var nodes []*ast.Node
		collectDeclarationsAt(sourceFile.AsNode(), sourceFile, segment.sourcePos, &nodes, &unsupportedAtAnchor)
		for _, node := range nodes {
			if !compatibleMappedDeclaration(checked, node, publicName) || mappedInsideNestedOwner(node, sourceFile, segment.sourcePos) {
				continue
			}
			id, supported := e.addDeclarationEndpoint(node, node.Symbol(), publicName)
			if id == "" || !supported {
				continue
			}
			candidateIDs = append(candidateIDs, id)
			e.declarationSourceCandidates = append(e.declarationSourceCandidates, DeclarationSourceFact{CheckedDecl: checkedID, SourceDecl: id, Method: "declaration_map"})
		}
	}
	candidateIDs = sortedUnique(candidateIDs)
	if len(exact) > 1 || len(candidateIDs) > 1 {
		e.addMapOutcome(checkedID, "ambiguous")
		return
	}
	if len(candidateIDs) != 1 {
		if unsupportedAtAnchor {
			e.addMapOutcome(checkedID, "unsupported")
			return
		}
		e.addMapOutcome(checkedID, "unresolved")
		return
	}
	if checkedID == candidateIDs[0] {
		return
	}
	e.declarationSources = append(e.declarationSources, DeclarationSourceFact{CheckedDecl: checkedID, SourceDecl: candidateIDs[0], Method: "declaration_map"})
}

func (e *extractor) findDeclarationMap(generated tspath.RootedFilePath, generatedText string) (string, bool, []string, string) {
	var url string
	for _, line := range strings.Split(generatedText, "\n") {
		if index := strings.LastIndex(line, "sourceMappingURL="); index >= 0 {
			url = strings.TrimSpace(strings.TrimSuffix(strings.TrimSpace(line[index+len("sourceMappingURL="):]), "*/"))
		}
	}
	if strings.HasPrefix(url, "data:") || tspath.IsUrl(url) {
		return "", true, nil, "unsupported"
	}
	var candidates []string
	if url != "" {
		if tspath.PathIsAbsolute(url) {
			absolute, ok := tspath.TryRootedFilePathFromAbsolute(url)
			if !ok {
				return "", true, nil, "unsupported"
			}
			candidates = append(candidates, normalizeSlash(absolute.AsString()))
		} else {
			candidates = append(candidates, normalizeSlash(filepath.Clean(filepath.Join(filepath.FromSlash(generated.Directory().AsString()), filepath.FromSlash(url)))))
		}
	}
	candidates = append(candidates, normalizeSlash(generated.AppendSuffix(".map").AsString()))
	for _, candidate := range candidates {
		rooted, ok := tspath.TryRootedFilePathFromAbsolute(candidate)
		if !ok {
			return "", url != "", candidates, "unsupported"
		}
		if e.host.FS().FileExists(rooted) {
			return candidate, true, candidates, ""
		}
	}
	return "", url != "", candidates, ""
}

func declarationMapSourcePath(mapPath string, sourceRoot *string, source string) (string, bool) {
	if tspath.IsUrl(source) {
		return "", false
	}
	if tspath.PathIsAbsolute(source) {
		rooted, ok := tspath.TryRootedFilePathFromAbsolute(source)
		return normalizeSlash(rooted.AsString()), ok
	}
	base := filepath.Dir(filepath.FromSlash(mapPath))
	if sourceRoot != nil && *sourceRoot != "" {
		if tspath.IsUrl(*sourceRoot) {
			return "", false
		}
		if tspath.PathIsAbsolute(*sourceRoot) {
			base = filepath.FromSlash(*sourceRoot)
		} else {
			base = filepath.Join(base, filepath.FromSlash(*sourceRoot))
		}
	}
	path := filepath.Clean(filepath.Join(base, filepath.FromSlash(source)))
	rooted, ok := tspath.TryRootedFilePathFromAbsolute(path)
	if !ok {
		return "", false
	}
	return normalizeSlash(rooted.AsString()), true
}

func collectDeclarationsAt(node *ast.Node, file *ast.SourceFile, position int, result *[]*ast.Node, unsupported *bool) {
	if node == nil {
		return
	}
	start := scanner.GetTokenPosOfNode(node, file, false)
	if start <= position && position < node.End() {
		if ast.IsDeclaration(node) {
			if _, supported := declarationKind(node); supported {
				*result = append(*result, node)
			} else {
				*unsupported = true
			}
		}
		node.ForEachChild(func(child *ast.Node) bool {
			collectDeclarationsAt(child, file, position, result, unsupported)
			return false
		})
	}
}

func compatibleMappedDeclaration(checked, source *ast.Node, publicName string) bool {
	checkedKind, checkedSupported := declarationKind(checked)
	sourceKind, sourceSupported := declarationKind(source)
	if !checkedSupported || !sourceSupported || checkedKind != sourceKind {
		return false
	}
	checkedName, sourceName := declarationTextName(checked), declarationTextName(source)
	if checkedName != sourceName && publicName != "default" {
		return false
	}
	if !slices.Equal(declarationOwnerContext(checked), declarationOwnerContext(source)) {
		return false
	}
	return true
}

func declarationTextName(node *ast.Node) string {
	if node == nil || node.Name() == nil {
		return ""
	}
	return node.Name().Text()
}

func declarationOwnerContext(node *ast.Node) []string {
	var result []string
	for parent := node.Parent; parent != nil && parent.Kind != ast.KindSourceFile; parent = parent.Parent {
		switch parent.Kind {
		case ast.KindClassDeclaration, ast.KindInterfaceDeclaration, ast.KindModuleDeclaration, ast.KindEnumDeclaration:
			name := declarationTextName(parent)
			result = append(result, parent.Kind.String()+":"+name)
		default:
			if ast.IsFunctionLike(parent) {
				result = append(result, parent.Kind.String()+":"+declarationTextName(parent))
			} else if parent.Kind == ast.KindBlock && (parent.Parent == nil || !ast.IsFunctionLike(parent.Parent)) {
				result = append(result, parent.Kind.String())
			}
		}
	}
	slices.Reverse(result)
	return result
}

func mappedInsideNestedOwner(node *ast.Node, file *ast.SourceFile, position int) bool {
	return node.ForEachChild(func(child *ast.Node) bool {
		if scanner.GetTokenPosOfNode(child, file, false) > position || position >= child.End() {
			return false
		}
		if ast.IsFunctionLike(child) || child.Kind == ast.KindClassDeclaration || child.Kind == ast.KindModuleDeclaration {
			return true
		}
		return mappedInsideNestedOwner(child, file, position)
	})
}

func (e *extractor) addMapOutcome(checkedDecl, outcome string) {
	e.declarationSourceOutcomes = append(e.declarationSourceOutcomes, DeclarationSourceOutcomeFact{CheckedDecl: checkedDecl, Outcome: outcome})
}
