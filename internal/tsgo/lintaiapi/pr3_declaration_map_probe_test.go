package lintaiapi

import (
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
	"unicode/utf16"
	"unicode/utf8"

	"github.com/bigbizze/lintai/internal/tsgo/internal/ast"
	"github.com/bigbizze/lintai/internal/tsgo/internal/core"
	"github.com/bigbizze/lintai/internal/tsgo/internal/parser"
	"github.com/bigbizze/lintai/internal/tsgo/internal/sourcemap"
	"github.com/bigbizze/lintai/internal/tsgo/internal/tspath"
)

type declarationMapProbeHost struct {
	files map[string]string
}

func (h declarationMapProbeHost) CaseSensitivity() tspath.CaseSensitivity {
	return tspath.CaseSensitive
}

func (h declarationMapProbeHost) ReadFile(fileName tspath.RootedFilePath) (string, bool) {
	text, ok := h.files[fileName.AsString()]
	return text, ok
}

func (h declarationMapProbeHost) GetECMALineInfo(fileName tspath.RootedFilePath) *sourcemap.ECMALineInfo {
	text, ok := h.ReadFile(fileName)
	if !ok {
		return nil
	}
	file := parser.ParseSourceFile(ast.SourceFileParseOptions{FileName: fileName}, text, core.EnsureScriptKindFromFileName(fileName))
	return sourcemap.CreateECMALineInfo(text, file.ECMALineMap())
}

// This temporary probe records vendored declaration-map mapper behavior only.
// It does not validate source associations; production-path tests must reject
// unrelated and ambiguous anchors.
func TestDeclarationMapMapperBehaviorProbe(t *testing.T) {
	root := t.TempDir()
	generatedPath := filepath.Join(root, "dist/index.d.ts")
	sourcePath := filepath.Join(root, "src/index.ts")
	generated := "export declare function right(): void;\n//# sourceMappingURL=index.d.ts.map\n"
	source := "export function wrong() {}\nexport function right() {}\n"
	generatedAnchor := strings.Index(generated, "right")
	wrongAnchor := strings.Index(source, "wrong")
	rightAnchor := strings.Index(source, "right")

	mapperFor := func(sourceText string, sourceAnchor int, includeSource bool, includeSourcesContent bool) (*sourcemap.DocumentPositionMapper, *sourcemap.DocumentPosition) {
		t.Helper()
		mapPath := filepath.Join(root, "dist/index.d.ts.map")
		generator := sourcemap.NewGenerator("index.d.ts", "", tspath.RootedDirectoryPathFromAbsolute(filepath.Join(root, "dist")), tspath.CaseSensitive)
		sourceFile := tspath.RootedFilePathFromAbsolute(sourcePath)
		sourceIndex := generator.AddSource(sourceFile)
		if includeSourcesContent {
			if err := generator.SetSourceContent(sourceIndex, sourceText); err != nil {
				t.Fatal(err)
			}
		}
		generatedLine, generatedColumn := sourceMapLineColumn(generated, generatedAnchor)
		sourceLine, sourceColumn := sourceMapLineColumn(sourceText, sourceAnchor)
		if err := generator.AddSourceMapping(generatedLine, core.UTF16Offset(generatedColumn), sourceIndex, sourceLine, core.UTF16Offset(sourceColumn)); err != nil {
			t.Fatal(err)
		}
		mapJSON, err := json.Marshal(generator.RawSourceMap())
		if err != nil {
			t.Fatal(err)
		}
		files := map[string]string{generatedPath: generated, mapPath: string(mapJSON)}
		if includeSource {
			files[sourcePath] = sourceText
		}
		mapper := sourcemap.GetDocumentPositionMapper(declarationMapProbeHost{files: files}, tspath.RootedFilePathFromAbsolute(generatedPath))
		return mapper, &sourcemap.DocumentPosition{FileName: tspath.RootedFilePathFromAbsolute(generatedPath), Pos: generatedAnchor}
	}

	mapper, generatedPosition := mapperFor(source, wrongAnchor, true, false)
	if mapper == nil {
		t.Fatal("ordinary relative declaration map did not load")
	}
	mapped := mapper.GetSourcePosition(generatedPosition)
	if mapped == nil || mapped.FileName.AsString() != sourcePath || mapped.Pos != wrongAnchor {
		t.Fatalf("relative source path or anchor was not resolved: %+v", mapped)
	}
	nearby := mapper.GetSourcePosition(&sourcemap.DocumentPosition{FileName: generatedPosition.FileName, Pos: 0})
	if nearby == nil || nearby.Pos != wrongAnchor {
		t.Fatalf("expected the mapper to return its nearby source position, got %+v", nearby)
	}

	mapper, generatedPosition = mapperFor(source, rightAnchor, true, false)
	if mapped = mapper.GetSourcePosition(generatedPosition); mapped == nil || mapped.FileName.AsString() != sourcePath || mapped.Pos != rightAnchor {
		t.Fatalf("the exact source-map point did not resolve: %+v", mapped)
	}

	mapper, generatedPosition = mapperFor(source, rightAnchor, false, false)
	if mapper == nil || mapper.GetSourcePosition(generatedPosition) != nil {
		t.Fatal("a map to an unavailable source unexpectedly produced an anchor")
	}

	mapper, _ = mapperFor(source, rightAnchor, true, true)
	if mapper != nil {
		t.Fatal("the vendored mapper unexpectedly supports non-null sourcesContent")
	}
}

func sourceMapLineColumn(text string, offset int) (int, int) {
	line, column := 0, 0
	for pos := 0; pos < offset; {
		r, size := utf8.DecodeRuneInString(text[pos:])
		if r == '\r' {
			if pos+size < offset && text[pos+size] == '\n' {
				size++
			}
			line++
			column = 0
		} else if r == '\n' {
			line++
			column = 0
		} else {
			column += utf16.RuneLen(r)
		}
		pos += size
	}
	return line, column
}
