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
)

type declarationMapHost struct {
	extractor *extractor
}

func (h declarationMapHost) CaseSensitivity() tspath.CaseSensitivity {
	return h.extractor.host.FS().CaseSensitivity()
}

func (h declarationMapHost) ReadFile(path tspath.RootedFilePath) (string, bool) {
	file := h.extractor.mapFile(path)
	return file.text, file.exists
}

func (h declarationMapHost) GetECMALineInfo(path tspath.RootedFilePath) *sourcemap.ECMALineInfo {
	file := h.extractor.mapFile(path)
	parsed := file.sourceFile()
	if parsed == nil {
		return nil
	}
	if file.lineInfo == nil {
		file.lineInfo = sourcemap.CreateECMALineInfo(file.text, parsed.ECMALineMap())
	}
	return file.lineInfo
}

type rawDeclarationMap struct {
	Version        int               `json:"version"`
	File           string            `json:"file"`
	SourceRoot     *string           `json:"sourceRoot"`
	Sources        []*string         `json:"sources"`
	SourcesContent []json.RawMessage `json:"sourcesContent"`
	Mappings       string            `json:"mappings"`
}

// These caches live on one extractor, and therefore one checked program. Path
// keys use that program's case sensitivity; no independent realpath is applied.
type declarationMapCache struct {
	files map[tspath.PathKey]*declarationMapFile
	maps  map[tspath.PathKey]*declarationMapIndex
}

type declarationMapFile struct {
	path     tspath.RootedFilePath
	text     string
	exists   bool
	file     *ast.SourceFile
	lineInfo *sourcemap.ECMALineInfo
}

func (f *declarationMapFile) sourceFile() *ast.SourceFile {
	if !f.exists {
		return nil
	}
	if f.file == nil {
		f.file = parser.ParseSourceFile(ast.SourceFileParseOptions{FileName: f.path}, f.text, core.EnsureScriptKindFromFileName(f.path))
	}
	return f.file
}

type generatedMapAnchor struct {
	line   int
	column core.UTF16Offset
}

// declarationMapIndex holds the map state for one generated file. The exact
// anchor index and the compiler mapper each decode the map once.
type declarationMapIndex struct {
	generatedUnreadable bool
	mapPath             string
	discovered          bool
	outcome             string
	raw                 rawDeclarationMap
	exact               map[generatedMapAnchor][]sourcemap.Mapping
	decodeError         bool
	mapper              *sourcemap.DocumentPositionMapper
	mapperReady         bool
}

type exactMapSegment struct {
	sourcePath string
	sourcePos  int
}

func (e *extractor) mapFile(path tspath.RootedFilePath) *declarationMapFile {
	if e.declarationMaps == nil {
		e.declarationMaps = &declarationMapCache{
			files: make(map[tspath.PathKey]*declarationMapFile),
			maps:  make(map[tspath.PathKey]*declarationMapIndex),
		}
	}
	key := e.program.PathKeyForFileName(path)
	if file := e.declarationMaps.files[key]; file != nil {
		return file
	}
	text, exists := e.host.FS().ReadFile(path)
	file := &declarationMapFile{path: path, text: text, exists: exists}
	e.declarationMaps.files[key] = file
	return file
}

// declarationMap discovers and indexes a generated file's map once. Source
// validation remains per anchor so an unrelated bad source cannot mask a link.
func (e *extractor) declarationMap(generatedPath tspath.RootedFilePath) *declarationMapIndex {
	generated := e.mapFile(generatedPath) // Also creates the cache.
	key := e.program.PathKeyForFileName(generatedPath)
	if index := e.declarationMaps.maps[key]; index != nil {
		return index
	}
	index := &declarationMapIndex{}
	e.declarationMaps.maps[key] = index
	if !generated.exists {
		index.generatedUnreadable = true
		return index
	}
	mapPath, discovered, candidates, earlyOutcome := e.findDeclarationMap(generatedPath, generated.text)
	index.mapPath, index.discovered, index.outcome = mapPath, discovered, earlyOutcome
	for _, candidate := range candidates {
		e.inputs = append(e.inputs, SnapshotInput{Path: candidate, Exists: e.host.FS().FileExists(tspath.RootedFilePathFromAbsolute(candidate))})
	}
	if earlyOutcome != "" || mapPath == "" {
		return index
	}
	e.inputs = append(e.inputs, SnapshotInput{Path: normalizeSlash(mapPath), Exists: true, OptionalMap: true})
	mapFile := e.mapFile(tspath.RootedFilePathFromAbsolute(mapPath))
	if !mapFile.exists {
		index.outcome = "unavailable_map"
		return index
	}
	var raw rawDeclarationMap
	if err := json.Unmarshal([]byte(mapFile.text), &raw); err != nil || raw.Version != 3 || raw.File == "" || len(raw.Sources) == 0 {
		index.outcome = "malformed_map"
		return index
	}
	for _, content := range raw.SourcesContent {
		if len(content) > 0 && string(content) != "null" {
			index.outcome = "unsupported"
			return index
		}
	}
	for _, source := range raw.Sources {
		if source == nil || *source == "" {
			index.outcome = "malformed_map"
			return index
		}
	}
	index.raw = raw
	index.exact = make(map[generatedMapAnchor][]sourcemap.Mapping)
	decoder := sourcemap.DecodeMappings(raw.Mappings)
	for mapping := range decoder.Values() {
		if mapping.IsSourceMapping() {
			anchor := generatedMapAnchor{line: mapping.GeneratedLine, column: mapping.GeneratedCharacter}
			index.exact[anchor] = append(index.exact[anchor], *mapping)
		}
	}
	index.decodeError = decoder.Error() != nil
	return index
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
	index := e.declarationMap(generatedPath)
	if index.generatedUnreadable {
		if requested {
			e.addMapOutcome(checkedID, "unavailable_map")
		}
		return
	}
	if index.outcome != "" {
		e.addMapOutcome(checkedID, index.outcome)
		return
	}
	if index.mapPath == "" {
		if requested {
			outcome := "missing_map"
			if index.discovered {
				outcome = "unavailable_map"
			}
			e.addMapOutcome(checkedID, outcome)
		} else {
			delete(e.mapAttempted, checkedID)
		}
		return
	}
	start := scanner.GetTokenPosOfNode(checked, checkedFile, false)
	generatedLine, generatedColumn := scanner.GetECMALineAndUTF16CharacterOfPosition(checkedFile, start)
	var exact []exactMapSegment
	var candidateIDs []string
	var unsupportedAtAnchor bool
	seenSegments := make(map[exactMapSegment]struct{})
	for _, mapping := range index.exact[generatedMapAnchor{line: generatedLine, column: generatedColumn}] {
		if int(mapping.SourceIndex) < 0 || int(mapping.SourceIndex) >= len(index.raw.Sources) {
			e.addMapOutcome(checkedID, "malformed_map")
			return
		}
		sourcePath, ok := declarationMapSourcePath(index.mapPath, index.raw.SourceRoot, *index.raw.Sources[mapping.SourceIndex])
		if !ok {
			e.addMapOutcome(checkedID, "unsupported")
			return
		}
		source := e.mapFile(tspath.RootedFilePathFromAbsolute(sourcePath))
		e.inputs = append(e.inputs, SnapshotInput{Path: normalizeSlash(sourcePath), Exists: source.exists})
		sourceFile := source.sourceFile()
		if sourceFile == nil || len(sourceFile.Diagnostics()) > 0 {
			e.addMapOutcome(checkedID, "unavailable_source")
			return
		}
		if mapping.SourceLine >= len(sourceFile.ECMALineMap()) {
			e.addMapOutcome(checkedID, "malformed_map")
			return
		}
		sourcePos := scanner.ComputePositionOfLineAndUTF16Character(sourceFile.ECMALineMap(), mapping.SourceLine, mapping.SourceCharacter, source.text, true)
		actualLine, actualColumn := scanner.GetECMALineAndUTF16CharacterOfPosition(sourceFile, sourcePos)
		if actualLine != mapping.SourceLine || actualColumn != mapping.SourceCharacter {
			e.addMapOutcome(checkedID, "malformed_map")
			return
		}
		segment := exactMapSegment{sourcePath: sourcePath, sourcePos: sourcePos}
		if _, exists := seenSegments[segment]; !exists {
			seenSegments[segment] = struct{}{}
			exact = append(exact, segment)
			var nodes []*ast.Node
			collectMappedDeclarationsAt(sourceFile, sourcePos, &nodes, &unsupportedAtAnchor)
			for _, node := range nodes {
				if !compatibleMappedDeclaration(checked, node, publicName) || mappedInsideNestedOwner(node, sourceFile, sourcePos) {
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
	}
	if index.decodeError {
		e.addMapOutcome(checkedID, "malformed_map")
		return
	}
	if len(exact) == 0 {
		e.addMapOutcome(checkedID, "unresolved")
		return
	}
	candidateIDs = sortedUnique(candidateIDs)
	if len(exact) > 1 || len(candidateIDs) > 1 {
		e.addMapOutcome(checkedID, "ambiguous")
		return
	}
	if !index.mapperReady {
		index.mapper = sourcemap.GetDocumentPositionMapper(declarationMapHost{extractor: e}, generatedPath)
		index.mapperReady = true
	}
	mapper := index.mapper
	if mapper == nil {
		e.addMapOutcome(checkedID, "malformed_map")
		return
	}
	generatedPosition := &sourcemap.DocumentPosition{FileName: generatedPath, Pos: start}
	mapped := mapper.GetSourcePosition(generatedPosition)
	var mapperAnchorValid bool
	for _, segment := range exact {
		sourceRooted := tspath.RootedFilePathFromAbsolute(segment.sourcePath)
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

// Top-level statements in a parsed source file do not overlap. Find the one
// containing the anchor instead of walking every statement for every link.
func collectMappedDeclarationsAt(file *ast.SourceFile, position int, result *[]*ast.Node, unsupported *bool) {
	root := file.AsNode()
	if scanner.GetTokenPosOfNode(root, file, false) > position || position >= root.End() {
		return
	}
	*result = append(*result, root)
	statements := file.Statements.Nodes
	index, _ := slices.BinarySearchFunc(statements, position, func(node *ast.Node, pos int) int {
		if node.End() <= pos {
			return -1
		}
		return 1
	})
	if index < len(statements) {
		collectDeclarationsAt(statements[index], file, position, result, unsupported)
	}
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
