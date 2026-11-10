package lintaiapi

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/bigbizze/lintai/internal/tsgo/internal/ast"
	"github.com/bigbizze/lintai/internal/tsgo/internal/bundled"
	"github.com/bigbizze/lintai/internal/tsgo/internal/compiler"
	"github.com/bigbizze/lintai/internal/tsgo/internal/core"
	"github.com/bigbizze/lintai/internal/tsgo/internal/module"
	"github.com/bigbizze/lintai/internal/tsgo/internal/sourcemap"
	"github.com/bigbizze/lintai/internal/tsgo/internal/tspath"
	"github.com/bigbizze/lintai/internal/tsgo/internal/vfs"
	"github.com/bigbizze/lintai/internal/tsgo/internal/vfs/osvfs"
)

// These files were emitted by TypeScript 5.9.3 with --declaration
// --declarationMap --emitDeclarationOnly --target es2022.
func TestDeclarationMapTypeScriptEmittedDeclarations(t *testing.T) {
	root := t.TempDir()
	writeWorkspace(t, root, map[string]string{
		"src/app.ts": `export * from "../dist/source"; export { default } from "../dist/source";`,
		"source.ts": `export function load(value: string): string { return value; }
export class Widget {}
export const count = 1;
export const arrow = (value: number) => value + 1;
export interface Options { label: string }
export type Label = string;
export default function entry() { return 1; }
`,
		"dist/source.d.ts": `export declare function load(value: string): string;
export declare class Widget {
}
export declare const count = 1;
export declare const arrow: (value: number) => number;
export interface Options {
    label: string;
}
export type Label = string;
export default function entry(): number;
//# sourceMappingURL=source.d.ts.map`,
		"dist/source.d.ts.map": `{"version":3,"file":"source.d.ts","sourceRoot":"","sources":["../source.ts"],"names":[],"mappings":"AAAA,wBAAgB,IAAI,CAAC,KAAK,EAAE,MAAM,GAAG,MAAM,CAAkB;AAC7D,qBAAa,MAAM;CAAG;AACtB,eAAO,MAAM,KAAK,IAAI,CAAC;AACvB,eAAO,MAAM,KAAK,GAAI,OAAO,MAAM,WAAc,CAAC;AAClD,MAAM,WAAW,OAAO;IAAG,KAAK,EAAE,MAAM,CAAA;CAAE;AAC1C,MAAM,MAAM,KAAK,GAAG,MAAM,CAAC;AAC3B,MAAM,CAAC,OAAO,UAAU,KAAK,WAAgB"}`,
	})
	snapshot, err := BuildSnapshot(context.Background(), BuildSnapshotRequest{WorkspaceRoot: root, Files: []string{filepath.Join(root, "src/app.ts")}})
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]string{"load": "function", "Widget": "class", "count": "variable", "arrow": "variable", "Options": "interface", "Label": "type_alias", "default": "function"}
	assertDeclarationMapLinks(t, snapshot, want, "dist/source.d.ts", "source.ts")
}

func TestDeclarationMapScaling(t *testing.T) {
	for _, count := range []int{20, 40, 80} {
		t.Run(fmt.Sprint(count), func(t *testing.T) {
			root, want := makeDeclarationMapScalingWorkspace(t, count)
			snapshot, err := BuildSnapshot(context.Background(), BuildSnapshotRequest{WorkspaceRoot: root, Files: []string{filepath.Join(root, "src/app.ts")}})
			if err != nil {
				t.Fatal(err)
			}
			assertDeclarationMapLinks(t, snapshot, want, "dist/index.d.ts", "source/index.ts")
		})
	}
}

func assertDeclarationMapLinks(t *testing.T, snapshot *Snapshot, want map[string]string, checkedFile, sourceFile string) {
	t.Helper()
	if len(snapshot.DeclarationSources) != len(want) || len(snapshot.DeclarationSourceCandidates) != len(want) || len(snapshot.DeclarationSourceOutcomes) != 0 {
		t.Fatalf("map result: links=%d candidates=%d outcomes=%+v, want %d links", len(snapshot.DeclarationSources), len(snapshot.DeclarationSourceCandidates), snapshot.DeclarationSourceOutcomes, len(want))
	}
	seen := make(map[string]bool, len(want))
	for _, link := range snapshot.DeclarationSources {
		checked := semanticDeclaration(t, snapshot, link.CheckedDecl)
		source := semanticDeclaration(t, snapshot, link.SourceDecl)
		kind, ok := want[checked.Name]
		if !ok || seen[checked.Name] || checked.Kind != kind || source.Kind != kind || checked.ID == source.ID || link.Method != "declaration_map" || checked.Location.File != checkedFile || source.Location.File != sourceFile || source.Name != checked.Name || checked.Location.StartLine <= 0 || source.Location.StartLine <= 0 {
			t.Fatalf("unexpected link: checked=%+v source=%+v link=%+v", checked, source, link)
		}
		seen[checked.Name] = true
	}
}

func makeDeclarationMapScalingWorkspace(tb testing.TB, count int) (string, map[string]string) {
	tb.Helper()
	root := tb.TempDir()
	for _, dir := range []string{"src", "source", "dist"} {
		if err := os.MkdirAll(filepath.Join(root, dir), 0o755); err != nil {
			tb.Fatal(err)
		}
	}
	generatedPath := filepath.Join(root, "dist/index.d.ts")
	sourcePath := filepath.Join(root, "source/index.ts")
	generator := sourcemap.NewGenerator("index.d.ts", "", tspath.RootedDirectoryPathFromAbsolute(filepath.Dir(generatedPath)), tspath.CaseSensitive)
	sourceIndex := generator.AddSource(tspath.RootedFilePathFromAbsolute(sourcePath))
	var generated, source strings.Builder
	want := make(map[string]string, count)
	for i := range count {
		name := fmt.Sprintf("method%03d", i)
		generatedLine := fmt.Sprintf("export declare function %s(value: number): number;\n", name)
		sourceLine := fmt.Sprintf("export function %s(value: number): number { return value + 1; }\n", name)
		// Include ordinary token segments as well as each declaration anchor.
		for _, token := range []string{"export", name, "value", "number", "):"} {
			gc, sc := strings.Index(generatedLine, token), strings.Index(sourceLine, token)
			if gc < 0 || sc < 0 {
				tb.Fatalf("missing fixture token %q", token)
			}
			if err := generator.AddSourceMapping(i, core.UTF16Offset(gc), sourceIndex, i, core.UTF16Offset(sc)); err != nil {
				tb.Fatal(err)
			}
		}
		generated.WriteString(generatedLine)
		source.WriteString(sourceLine)
		want[name] = "function"
	}
	generated.WriteString("//# sourceMappingURL=index.d.ts.map\n")
	data, err := json.Marshal(generator.RawSourceMap())
	if err != nil {
		tb.Fatal(err)
	}
	for path, text := range map[string]string{generatedPath: generated.String(), sourcePath: source.String(), generatedPath + ".map": string(data), filepath.Join(root, "src/app.ts"): `export * from "../dist";`} {
		if err := os.WriteFile(path, []byte(text), 0o644); err != nil {
			tb.Fatal(err)
		}
	}
	return root, want
}

// Run with -run '^$' -bench '^BenchmarkDeclarationMapScaling$' -benchtime=1x.
func BenchmarkDeclarationMapScaling(b *testing.B) {
	for _, count := range []int{20, 40, 80} {
		b.Run(fmt.Sprint(count), func(b *testing.B) {
			root, want := makeDeclarationMapScalingWorkspace(b, count)
			req := BuildSnapshotRequest{WorkspaceRoot: root, Files: []string{filepath.Join(root, "src/app.ts")}}
			b.ReportAllocs()
			b.ResetTimer()
			for b.Loop() {
				snapshot, err := BuildSnapshot(context.Background(), req)
				if err != nil {
					b.Fatal(err)
				}
				if len(snapshot.DeclarationSources) != len(want) || len(snapshot.DeclarationSourceOutcomes) != 0 {
					b.Fatalf("links=%d outcomes=%+v", len(snapshot.DeclarationSources), snapshot.DeclarationSourceOutcomes)
				}
			}
		})
	}
}

type declarationMapCountingFS struct {
	vfs.FS
	mu    sync.Mutex
	reads map[string]int
}

func (fs *declarationMapCountingFS) ReadFile(path tspath.RootedFilePath) (string, bool) {
	fs.mu.Lock()
	fs.reads[path.AsString()]++
	fs.mu.Unlock()
	return fs.FS.ReadFile(path)
}

func TestDeclarationMapReusesFilesAndSharedMap(t *testing.T) {
	const count = 80
	root, _ := makeDeclarationMapScalingWorkspace(t, count)
	generatedPath := filepath.Join(root, "dist/index.d.ts")
	otherPath := filepath.Join(root, "dist/other.d.ts")
	sourcePath := filepath.Join(root, "source/index.ts")
	mapPath := generatedPath + ".map"
	text, err := os.ReadFile(generatedPath)
	if err != nil {
		t.Fatal(err)
	}
	// Both generated files refer to index.d.ts.map; its "file" field names
	// only the first file, so only that file may receive precise links.
	if err := os.WriteFile(otherPath, text, 0o644); err != nil {
		t.Fatal(err)
	}
	fs := &declarationMapCountingFS{FS: osvfs.FS(), reads: make(map[string]int)}
	host := compiler.NewCompilerHost(bundled.WrapFS(fs), bundled.LibPath(), nil, nil, nil)
	config, err := syntheticConfig(host, root, []string{generatedPath, otherPath})
	if err != nil {
		t.Fatal(err)
	}
	program := compiler.NewProgram(compiler.ProgramOptions{
		ProgramConfig: compiler.ProgramConfig{Config: config},
		ProgramHosts:  compiler.ProgramHosts{Host: host},
		ProgramFactories: compiler.ProgramFactories{CreateModuleResolver: func(options module.ResolverOptions) module.Resolver {
			return module.NewResolver(options)
		}},
	})
	first := program.GetSourceFile(tspath.RootedFilePathFromAbsolute(generatedPath))
	second := program.GetSourceFile(tspath.RootedFilePathFromAbsolute(otherPath))
	if first == nil || second == nil {
		t.Fatal("compiler did not load the generated files")
	}
	fs.mu.Lock()
	fs.reads = make(map[string]int) // Count recovery, after program construction.
	fs.mu.Unlock()
	e := &extractor{workspaceRoot: root, host: host, program: program, declarations: make(map[string]*DeclarationFact)}
	firstIDs := make(map[string]bool, count)
	for _, node := range first.Statements.Nodes {
		firstIDs[e.declarationID(node)] = true
	}
	for _, file := range []*ast.SourceFile{first, second} {
		var index *declarationMapIndex
		var mapper *sourcemap.DocumentPositionMapper
		for _, declaration := range file.Statements.Nodes {
			e.recoverDeclarationMap(declaration, false, declaration.Name().Text())
			current := e.declarationMaps.maps[program.PathKeyForFileName(file.FileName())]
			if index == nil {
				index, mapper = current, current.mapper
			}
			if current != index || mapper == nil || current.mapper != mapper {
				t.Fatal("the map index or compiler mapper was rebuilt for a declaration")
			}
		}
	}
	if len(e.declarationSources) != count || len(e.declarationSourceOutcomes) != count {
		t.Fatalf("links=%d outcomes=%+v", len(e.declarationSources), e.declarationSourceOutcomes)
	}
	var parsed *ast.SourceFile
	for _, link := range e.declarationSources {
		if !firstIDs[link.CheckedDecl] {
			t.Fatal("a map linked a declaration in the wrong generated file")
		}
		file := ast.GetSourceFileOfNode(e.declarationNodes[link.SourceDecl])
		if parsed == nil {
			parsed = file
		} else if parsed != file {
			t.Fatal("mapped declarations did not share one parsed source file")
		}
	}
	for _, outcome := range e.declarationSourceOutcomes {
		if outcome.Outcome != "unresolved" {
			t.Fatalf("wrong generated file outcome: %+v", outcome)
		}
	}
	fs.mu.Lock()
	defer fs.mu.Unlock()
	for _, path := range []string{generatedPath, otherPath, sourcePath, mapPath} {
		if got := fs.reads[filepath.ToSlash(path)]; got != 1 {
			t.Errorf("recovery read %s %d times, want once", path, got)
		}
	}
}

func TestDeclarationMapSourceChangesBetweenExtractions(t *testing.T) {
	root, want := makeDeclarationMapScalingWorkspace(t, 20)
	req := BuildSnapshotRequest{WorkspaceRoot: root, Files: []string{filepath.Join(root, "src/app.ts")}}
	first, err := BuildSnapshot(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	assertDeclarationMapLinks(t, first, want, "dist/index.d.ts", "source/index.ts")
	sourcePath := filepath.Join(root, "source/index.ts")
	text, err := os.ReadFile(sourcePath)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(sourcePath, []byte(strings.Replace(string(text), "method000", "changed00", 1)), 0o644); err != nil {
		t.Fatal(err)
	}
	second, err := BuildSnapshot(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	if len(second.DeclarationSources) != 19 || len(second.DeclarationSourceOutcomes) != 1 || second.DeclarationSourceOutcomes[0].Outcome != "unresolved" {
		t.Fatalf("changed source reused stale map evidence: links=%d outcomes=%+v", len(second.DeclarationSources), second.DeclarationSourceOutcomes)
	}
	checked := semanticDeclaration(t, second, second.DeclarationSourceOutcomes[0].CheckedDecl)
	if checked.Name != "method000" {
		t.Fatalf("wrong declaration became unresolved after the source edit: %+v", checked)
	}
}

func TestDeclarationMapReverseAnchorRejectsDifferentGeneratedDeclaration(t *testing.T) {
	root := t.TempDir()
	generatedPath := filepath.Join(root, "dist/index.d.ts")
	sourcePath := filepath.Join(root, "source/index.ts")
	writeWorkspace(t, root, map[string]string{
		"src/app.ts":      `export { load } from "../dist";`,
		"dist/index.d.ts": "export declare function load(x: string): string;\nexport declare function load(x: number): number;\n//# sourceMappingURL=index.d.ts.map\n",
		"source/index.ts": "export function load(x: string | number): string | number { return x; }\n",
	})
	generator := sourcemap.NewGenerator("index.d.ts", "", tspath.RootedDirectoryPathFromAbsolute(filepath.Dir(generatedPath)), tspath.CaseSensitive)
	sourceIndex := generator.AddSource(tspath.RootedFilePathFromAbsolute(sourcePath))
	for line := range 2 {
		if err := generator.AddSourceMapping(line, 0, sourceIndex, 0, 0); err != nil {
			t.Fatal(err)
		}
	}
	contents, err := json.Marshal(generator.RawSourceMap())
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(generatedPath+".map", contents, 0o644); err != nil {
		t.Fatal(err)
	}
	snapshot, err := BuildSnapshot(context.Background(), BuildSnapshotRequest{WorkspaceRoot: root, Files: []string{filepath.Join(root, "src/app.ts")}})
	if err != nil {
		t.Fatal(err)
	}
	if len(snapshot.DeclarationSources) != 1 || len(snapshot.DeclarationSourceOutcomes) != 1 || snapshot.DeclarationSourceOutcomes[0].Outcome != "unresolved" {
		t.Fatalf("non-round-tripping overload was accepted: links=%+v outcomes=%+v", snapshot.DeclarationSources, snapshot.DeclarationSourceOutcomes)
	}
	checked := semanticDeclaration(t, snapshot, snapshot.DeclarationSources[0].CheckedDecl)
	rejected := semanticDeclaration(t, snapshot, snapshot.DeclarationSourceOutcomes[0].CheckedDecl)
	if checked.Location.StartLine != 1 || rejected.Location.StartLine != 2 {
		t.Fatalf("unexpected round-trip endpoints: accepted=%+v rejected=%+v", checked, rejected)
	}
}

func TestDeclarationMapKeepsOutcomesSeparateForSourcesInOneMap(t *testing.T) {
	root := t.TempDir()
	generatedPath := filepath.Join(root, "dist/index.d.ts")
	sourceDir := filepath.Join(root, "source")
	goodPath := filepath.Join(sourceDir, "good.ts")
	missingPath := filepath.Join(sourceDir, "missing.ts")
	badPath := filepath.Join(sourceDir, "bad.ts")
	goodSource := "const marker = '😀'; export function good() {}\r\n"
	writeWorkspace(t, root, map[string]string{
		"src/app.ts":      `export { good, missing, bad } from "../dist";`,
		"dist/index.d.ts": "export declare function good(): void;\nexport declare function missing(): void;\nexport declare function bad(): void;\n//# sourceMappingURL=index.d.ts.map\n",
		"source/good.ts":  goodSource,
		"source/bad.ts":   "export function bad( {",
	})
	generator := sourcemap.NewGenerator("index.d.ts", "../source/./nested/..", tspath.RootedDirectoryPathFromAbsolute(sourceDir), tspath.CaseSensitive)
	goodIndex := generator.AddSource(tspath.RootedFilePathFromAbsolute(goodPath))
	missingIndex := generator.AddSource(tspath.RootedFilePathFromAbsolute(missingPath))
	badIndex := generator.AddSource(tspath.RootedFilePathFromAbsolute(badPath))
	_, column := sourceMapLineColumn(goodSource, strings.Index(goodSource, "export"))
	for line, mapping := range []struct {
		index  sourcemap.SourceIndex
		column int
	}{{goodIndex, column}, {missingIndex, 0}, {badIndex, 0}} {
		if err := generator.AddSourceMapping(line, 0, mapping.index, 0, core.UTF16Offset(mapping.column)); err != nil {
			t.Fatal(err)
		}
	}
	contents, err := json.Marshal(generator.RawSourceMap())
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(generatedPath+".map", contents, 0o644); err != nil {
		t.Fatal(err)
	}
	snapshot, err := BuildSnapshot(context.Background(), BuildSnapshotRequest{WorkspaceRoot: root, Files: []string{filepath.Join(root, "src/app.ts")}})
	if err != nil {
		t.Fatal(err)
	}
	if len(snapshot.DeclarationSources) != 1 || len(snapshot.DeclarationSourceCandidates) != 1 || len(snapshot.DeclarationSourceOutcomes) != 2 {
		t.Fatalf("independent source results: links=%+v candidates=%+v outcomes=%+v", snapshot.DeclarationSources, snapshot.DeclarationSourceCandidates, snapshot.DeclarationSourceOutcomes)
	}
	link := snapshot.DeclarationSources[0]
	checked := semanticDeclaration(t, snapshot, link.CheckedDecl)
	source := semanticDeclaration(t, snapshot, link.SourceDecl)
	if checked.Name != "good" || source.Name != "good" || source.Location.File != "source/good.ts" || source.Location.StartColumn != column+1 {
		t.Fatalf("sourceRoot or UTF-16 anchor was not preserved: checked=%+v source=%+v", checked, source)
	}
	outcomes := make(map[string]string)
	for _, outcome := range snapshot.DeclarationSourceOutcomes {
		outcomes[semanticDeclaration(t, snapshot, outcome.CheckedDecl).Name] = outcome.Outcome
	}
	if outcomes["missing"] != "unavailable_source" || outcomes["bad"] != "unavailable_source" {
		t.Fatalf("source failures were not isolated: %+v", outcomes)
	}
	for path, exists := range map[string]bool{goodPath: true, missingPath: false, badPath: true, generatedPath + ".map": true} {
		found := false
		for _, input := range snapshot.Inputs {
			if input.Path == filepath.ToSlash(path) && input.Exists == exists {
				found = true
			}
		}
		if !found {
			t.Errorf("missing source-map input %s exists=%t: %+v", path, exists, snapshot.Inputs)
		}
	}
}

func TestDeclarationMapMissingURLTargetKeepsConfiguredFallback(t *testing.T) {
	root := t.TempDir()
	mapPath := filepath.Join(root, "dist/missing.d.ts.map")
	writeWorkspace(t, root, map[string]string{
		"src/app.ts":        `export { load } from "../dist";`,
		"dist/index.d.ts":   "export declare function load(): void;\n//# sourceMappingURL=missing.d.ts.map\n",
		"source/index.ts":   "export function load() {}\n",
		"source-links.json": `{"version":1,"entries":[{"checkedEntry":"dist/index.d.ts","sourceEntry":"source/index.ts"}]}`,
	})
	snapshot, err := BuildSnapshot(context.Background(), BuildSnapshotRequest{WorkspaceRoot: root, Files: []string{filepath.Join(root, "src/app.ts")}, SourceLinks: "source-links.json"})
	if err != nil {
		t.Fatal(err)
	}
	if len(snapshot.DeclarationSources) != 0 || len(snapshot.DeclarationSourceOutcomes) != 1 || snapshot.DeclarationSourceOutcomes[0].Outcome != "unavailable_map" {
		t.Fatalf("missing URL map outcome: links=%+v outcomes=%+v", snapshot.DeclarationSources, snapshot.DeclarationSourceOutcomes)
	}
	if len(snapshot.SourceBindings) != 1 || len(snapshot.SourceBindings[0].Incomplete) != 0 {
		t.Fatalf("configured binding fallback was lost: %+v", snapshot.SourceBindings)
	}
	for _, input := range snapshot.Inputs {
		if input.Path == filepath.ToSlash(mapPath) && !input.Exists {
			return
		}
	}
	t.Fatalf("missing URL target was not tracked: %+v", snapshot.Inputs)
}
