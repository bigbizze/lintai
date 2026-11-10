package lintaiapi

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"unicode/utf16"
	"unicode/utf8"

	"github.com/bigbizze/lintai/internal/tsgo/internal/core"
	"github.com/bigbizze/lintai/internal/tsgo/internal/sourcemap"
	"github.com/bigbizze/lintai/internal/tsgo/internal/tspath"
	"github.com/bigbizze/lintai/internal/tsgo/internal/vfs/osvfs"
)

func TestSourceEntryUsesCompilerResolutionForSymlinkedWorkspace(t *testing.T) {
	for _, preserve := range []bool{false, true} {
		t.Run(fmt.Sprintf("preserveSymlinks=%t", preserve), func(t *testing.T) {
			root, err := filepath.EvalSymlinks(t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			writeWorkspace(t, root, map[string]string{
				"tsconfig.json":                                 fmt.Sprintf(`{"compilerOptions":{"module":"preserve","moduleResolution":"bundler","preserveSymlinks":%t},"include":["src/**/*.ts"]}`, preserve),
				"packages/checked/tsconfig.json":                `{"compilerOptions":{"module":"preserve","moduleResolution":"bundler"},"include":["src/**/*.ts"]}`,
				"packages/checked/src/index.ts":                 `export interface Widget { value: string }`,
				"node_modules/@fixture/checked/package.json":    `{"name":"@fixture/checked","types":"dist/index.d.ts"}`,
				"node_modules/@fixture/checked/dist/index.d.ts": `export interface Widget { value: string }`,
				"node_modules/@fixture/unused/dist/index.d.ts":  `export interface Widget { value: string }`,
				"src/app.ts": `export type { Widget } from "@fixture/checked";`,
				"source-links.json": `{"version":1,"entries":[
					{"checkedEntry":"node_modules/@fixture/checked/dist/index.d.ts","sourceEntry":"packages/checked/src/index.ts","sourceTsconfig":"packages/checked/tsconfig.json"},
					{"checkedEntry":"node_modules/@fixture/unused/dist/index.d.ts","sourceEntry":"packages/checked/src/index.ts"}
				]}`,
			})
			alias := filepath.Join(t.TempDir(), "workspace")
			if err := os.Symlink(root, alias); err != nil {
				t.Skipf("symlinks are unavailable: %v", err)
			}
			snapshot, err := BuildSnapshot(context.Background(), BuildSnapshotRequest{
				WorkspaceRoot: alias,
				Files:         []string{filepath.Join(alias, "src/app.ts")},
				SourceLinks:   "source-links.json",
			})
			if err != nil {
				t.Fatal(err)
			}
			for _, entry := range snapshot.SourceEntries {
				wantApplied := entry.CheckedEntry == "node_modules/@fixture/checked/dist/index.d.ts"
				if entry.Applied != wantApplied || len(entry.Incomplete) != 0 {
					t.Fatalf("source entry outcome: %+v", entry)
				}
			}
			if len(snapshot.SourceEntries) != 2 || len(snapshot.SourceBindings) != 1 {
				t.Fatalf("unexpected entries or binding attempts: %+v, %+v", snapshot.SourceEntries, snapshot.SourceBindings)
			}
			binding := snapshot.SourceBindings[0]
			wantFile := "node_modules/@fixture/checked/dist/index.d.ts"
			if !preserve {
				absolute := tspath.RootedPathFromAbsolute(filepath.ToSlash(filepath.Join(root, wantFile)))
				wantFile = osvfs.FS().CaseSensitivity().PathKey(absolute).AsString()
			}
			public := semanticExport(t, snapshot, "src/app.ts", "Widget", SpaceType)
			if binding.CheckedFile != wantFile || binding.SourceFile != "packages/checked/src/index.ts" || binding.Method != "source_entry" ||
				len(binding.Incomplete) != 0 || len(binding.SourceDeclarations) != 1 || !slices.Equal(binding.CheckedDeclarations, public.Targets) {
				t.Fatalf("binding did not retain the compiler's checked declaration and source endpoint: %+v, export=%+v", binding, public)
			}
		})
	}
}

func TestExportEqualsExpressionKeepsTypeRootAndConfiguredBinding(t *testing.T) {
	root := t.TempDir()
	writeWorkspace(t, root, map[string]string{
		"tsconfig.json":                      `{"compilerOptions":{"strict":true,"module":"preserve","moduleResolution":"bundler"},"include":["src/**/*.ts"]}`,
		"packages/contracts/tsconfig.json":   `{"compilerOptions":{"strict":true,"module":"preserve","moduleResolution":"bundler"},"include":["src/**/*.ts"]}`,
		"node_modules/knex/package.json":     `{"name":"knex","types":"index.d.ts"}`,
		"node_modules/knex/index.d.ts":       `export interface Knex { raw(sql: string): string }`,
		"packages/contracts/dist/index.d.ts": `import type { Knex } from "knex"; declare const api: { db: Knex }; export = api;`,
		"packages/contracts/src/index.ts":    `import type { Knex } from "knex"; declare const db: Knex; export = { db };`,
		"src/app.ts":                         `import api = require("../packages/contracts/dist"); export const use = () => api.db;`,
		"source-links.json":                  `{"version":1,"entries":[{"checkedEntry":"packages/contracts/dist/index.d.ts","sourceEntry":"packages/contracts/src/index.ts"}]}`,
	})
	snapshot, err := BuildSnapshot(context.Background(), BuildSnapshotRequest{
		WorkspaceRoot: root,
		Files: []string{
			filepath.Join(root, "src/app.ts"),
			filepath.Join(root, "packages/contracts/src/index.ts"),
		},
		SourceLinks: "source-links.json",
	})
	if err != nil {
		t.Fatal(err)
	}
	public := semanticExport(t, snapshot, "packages/contracts/src/index.ts", "export=", SpaceValue)
	if len(public.Targets) != 1 || len(public.Incomplete) != 0 {
		t.Fatalf("export assignment is incomplete: %+v", public)
	}
	declaration := semanticDeclaration(t, snapshot, public.Targets[0])
	if declaration.Kind != "export_expression" || len(declaration.Incomplete) != 0 || len(declaration.Types) != 1 || declaration.Types[0].Space != SpaceValue {
		t.Fatalf("export expression did not retain its value type: %+v", declaration)
	}
	types := newTypeIndex(t, snapshot)
	valueType := types.get(declaration.Types[0].Type)
	dbIndex := slices.IndexFunc(valueType.Props, func(prop TypeProp) bool { return prop.Name == "db" })
	if dbIndex < 0 {
		t.Fatalf("exported object lost its db type: %+v", valueType)
	}
	dbType := types.get(valueType.Props[dbIndex].Type)
	if dbType.Name != "Knex" || dbType.OriginKind != OriginKindPackage || dbType.Origin != "knex" {
		t.Fatalf("exported object did not retain the Knex dependency: %+v", dbType)
	}
	if _, found := findSemanticExport(snapshot, "packages/contracts/src/index.ts", "db", SpaceValue); found {
		t.Fatal("export assignment invented a named export for its db property")
	}
	if len(snapshot.SourceBindings) != 1 {
		t.Fatalf("export assignment binding was not matched: %+v", snapshot.SourceBindings)
	}
	binding := snapshot.SourceBindings[0]
	if binding.Name != "export=" || binding.Space != SpaceValue || binding.Method != "source_entry" ||
		!binding.CheckedComplete || !binding.SourceComplete || len(binding.Incomplete) != 0 ||
		len(binding.CheckedDeclarations) != 1 || !slices.Equal(binding.SourceDeclarations, public.Targets) {
		t.Fatalf("export assignment binding did not retain its checked and source endpoints: %+v", binding)
	}
}

func TestSourceEntryKeepsCheckedTypesAndRecordsOverloadMembershipSets(t *testing.T) {
	root := t.TempDir()
	writeWorkspace(t, root, map[string]string{
		"tsconfig.json":                    `{"compilerOptions":{"strict":true,"module":"preserve","moduleResolution":"bundler"},"include":["src/**/*.ts"]}`,
		"packages/contracts/tsconfig.json": `{"compilerOptions":{"strict":true,"module":"preserve","moduleResolution":"bundler","noEmit":true},"include":["src/**/*.ts"]}`,
		"packages/contracts/dist/index.d.ts": `export declare function parse(value: string): string;
export declare function parse(value: number): number;
export interface Options { name: string }`,
		"packages/contracts/src/index.ts": `export function parse(value: string): string;
export function parse(value: number): number;
export function parse(value: string | number): string | number { return value; }
export type { Options } from "./options";`,
		"packages/contracts/src/options.ts": `export interface Options { name: string }`,
		"src/app.ts": `import { parse } from "../packages/contracts/dist";
export const result = parse("value");`,
		"source-links.json": `{"version":1,"entries":[{"checkedEntry":"packages/contracts/dist/index.d.ts","sourceEntry":"packages/contracts/src/index.ts"}]}`,
	})
	checkedPath := filepath.Join(root, "packages/contracts/dist/index.d.ts")
	snapshot, err := BuildSnapshot(context.Background(), BuildSnapshotRequest{
		WorkspaceRoot: root,
		Files:         []string{filepath.Join(root, "src/app.ts")},
		SourceLinks:   "source-links.json",
		TsconfigFiles: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(snapshot.SourceEntries) != 1 || !snapshot.SourceEntries[0].Applied || snapshot.SourceEntries[0].Incomplete != nil {
		t.Fatalf("source entry outcome: %+v", snapshot.SourceEntries)
	}
	var parse *SourceBindingFact
	for index := range snapshot.SourceBindings {
		if snapshot.SourceBindings[index].Name == "parse" && snapshot.SourceBindings[index].Space == SpaceValue {
			parse = &snapshot.SourceBindings[index]
			break
		}
	}
	if parse == nil || parse.SourceFile != "packages/contracts/src/index.ts" || parse.SourceName != "parse" || parse.Method != "source_entry" {
		t.Fatalf("configured parse binding: %+v", parse)
	}
	if len(parse.CheckedDeclarations) != 2 || len(parse.SourceDeclarations) != 3 {
		t.Fatalf("overload membership sets: checked=%v source=%v", parse.CheckedDeclarations, parse.SourceDeclarations)
	}
	if !parse.CheckedComplete || !parse.SourceComplete || len(parse.Incomplete) != 0 {
		t.Fatalf("complete overload binding was marked incomplete: %+v", parse)
	}
	for _, declarationID := range slices.Concat(parse.CheckedDeclarations, parse.SourceDeclarations) {
		declaration := semanticDeclaration(t, snapshot, declarationID)
		if len(declaration.Types) != 0 {
			t.Fatalf("source association endpoint became a type root: %+v", declaration)
		}
	}
	checkedDeclarations := make(map[string]struct{})
	for _, binding := range snapshot.SourceBindings {
		for _, declarationID := range binding.CheckedDeclarations {
			checkedDeclarations[declarationID] = struct{}{}
		}
	}
	if len(snapshot.DeclarationSourceOutcomes) != len(checkedDeclarations) {
		t.Fatalf("expected one missing-map outcome for each configured checked declaration: %+v", snapshot.DeclarationSourceOutcomes)
	}
	for _, outcome := range snapshot.DeclarationSourceOutcomes {
		if outcome.Outcome != "missing_map" {
			t.Fatalf("unexpected configured map outcome: %+v", outcome)
		}
		if _, found := checkedDeclarations[outcome.CheckedDecl]; !found {
			t.Fatalf("map outcome is unrelated to a configured binding: %+v", outcome)
		}
		declaration := semanticDeclaration(t, snapshot, outcome.CheckedDecl)
		if declaration.Location.File != "packages/contracts/dist/index.d.ts" || slices.Contains(declaration.Incomplete, AssociationUnresolved) {
			t.Fatalf("missing source map changed checked declaration facts: %+v", declaration)
		}
	}
	for _, path := range []string{checkedPath, filepath.Join(root, "packages/contracts/src/options.ts")} {
		if !slices.ContainsFunc(snapshot.Inputs, func(input SnapshotInput) bool { return input.Path == path && input.Exists }) {
			t.Fatalf("checked entry or auxiliary program file %s is missing from snapshot inputs: %+v", path, snapshot.Inputs)
		}
	}
	if !slices.ContainsFunc(snapshot.Inputs, func(input SnapshotInput) bool {
		return input.Path == filepath.Join(root, "packages/contracts/dist/index.d.ts.map") && !input.Exists
	}) {
		t.Fatalf("absent declaration map is missing from snapshot inputs: %+v", snapshot.Inputs)
	}
	ordinary, err := BuildSnapshot(context.Background(), BuildSnapshotRequest{
		WorkspaceRoot: root,
		Files:         []string{filepath.Join(root, "src/app.ts")},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(ordinary.DeclarationSourceOutcomes) != 0 {
		t.Fatalf("ordinary missing maps produced a requested-recovery outcome: %+v", ordinary.DeclarationSourceOutcomes)
	}
}

func TestUnavailableSourceEntryIsReportedAsUnresolved(t *testing.T) {
	root := t.TempDir()
	writeWorkspace(t, root, map[string]string{
		"tsconfig.json":                      `{"compilerOptions":{"strict":true,"module":"preserve","moduleResolution":"bundler"},"include":["src/**/*.ts"]}`,
		"packages/contracts/dist/index.d.ts": `export declare const known: string;`,
		"src/app.ts":                         `import { known } from "../packages/contracts/dist"; export function use(): string { return known; }`,
		"source-links.json":                  `{"version":1,"entries":[{"checkedEntry":"packages/contracts/dist/index.d.ts","sourceEntry":"packages/contracts/src/index.ts"}]}`,
	})
	snapshot, err := BuildSnapshot(context.Background(), BuildSnapshotRequest{
		WorkspaceRoot: root,
		Files:         []string{filepath.Join(root, "src/app.ts")},
		SourceLinks:   "source-links.json",
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(snapshot.SourceEntries) != 1 || !snapshot.SourceEntries[0].Applied || !slices.Equal(snapshot.SourceEntries[0].Incomplete, []string{AssociationUnresolved}) {
		t.Fatalf("unavailable source entry outcome: %+v", snapshot.SourceEntries)
	}
	if len(snapshot.SourceBindings) != 1 {
		t.Fatalf("checked binding attempt was lost: %+v", snapshot.SourceBindings)
	}
	binding := snapshot.SourceBindings[0]
	if binding.Name != "known" || binding.SourceFile != "" || !slices.Equal(binding.Incomplete, []string{AssociationUnresolved}) || len(binding.CheckedDeclarations) != 1 {
		t.Fatalf("unavailable source binding outcome: %+v", binding)
	}
	checked := semanticDeclaration(t, snapshot, binding.CheckedDeclarations[0])
	if slices.Contains(checked.Incomplete, AssociationUnresolved) {
		t.Fatalf("source failure changed known checked declaration facts: %+v", checked)
	}
	var use *Function
	for index := range snapshot.Functions {
		if snapshot.Functions[index].Name == "use" {
			use = &snapshot.Functions[index]
			break
		}
	}
	if use == nil || !slices.ContainsFunc(snapshot.Types, func(item TypeFact) bool {
		return item.ID == use.ReturnType && item.Kind == TypeKindString
	}) {
		t.Fatalf("the consumer's checked program type was changed by source recovery failure: %+v", use)
	}
}

func TestDeclarationMapCreatesOnlyAnExactValidatedLink(t *testing.T) {
	root := t.TempDir()
	generated := `export declare function load(value: string): string;
//# sourceMappingURL=index.d.ts.map
`
	source := `export function load(value: string): string { return value; }`
	writeWorkspace(t, root, map[string]string{
		"tsconfig.json":                      `{"compilerOptions":{"strict":true,"module":"preserve","moduleResolution":"bundler"},"include":["src/**/*.ts"]}`,
		"packages/contracts/dist/index.d.ts": generated,
		"packages/contracts/src/index.ts":    source,
		"src/app.ts":                         `export { load } from "../packages/contracts/dist";`,
	})
	generatedPath := filepath.Join(root, "packages/contracts/dist/index.d.ts")
	sourcePath := filepath.Join(root, "packages/contracts/src/index.ts")
	mapPath := writeStartDeclarationMap(t, generatedPath, sourcePath)
	snapshot, err := BuildSnapshot(context.Background(), BuildSnapshotRequest{
		WorkspaceRoot: root, Files: []string{filepath.Join(root, "src/app.ts")},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(snapshot.DeclarationSourceCandidates) != 1 || len(snapshot.DeclarationSources) != 1 || len(snapshot.DeclarationSourceOutcomes) != 0 {
		t.Fatalf("exact map result: candidates=%+v links=%+v outcomes=%+v", snapshot.DeclarationSourceCandidates, snapshot.DeclarationSources, snapshot.DeclarationSourceOutcomes)
	}
	link := snapshot.DeclarationSources[0]
	if link.CheckedDecl == link.SourceDecl || link.Method != "declaration_map" {
		t.Fatalf("map link equated or mislabeled declaration endpoints: %+v", link)
	}
	checked := semanticDeclaration(t, snapshot, link.CheckedDecl)
	sourceDecl := semanticDeclaration(t, snapshot, link.SourceDecl)
	if checked.Location.File != "packages/contracts/dist/index.d.ts" || sourceDecl.Location.File != "packages/contracts/src/index.ts" || len(sourceDecl.Types) != 0 {
		t.Fatalf("declaration endpoints or source type retention: checked=%+v source=%+v", checked, sourceDecl)
	}
	if !slices.ContainsFunc(snapshot.Inputs, func(input SnapshotInput) bool { return input.Path == mapPath && input.Exists }) ||
		!slices.ContainsFunc(snapshot.Inputs, func(input SnapshotInput) bool { return input.Path == sourcePath && input.Exists }) {
		t.Fatalf("mapped source inputs were not retained: %+v", snapshot.Inputs)
	}
}

func TestMapLinkOutsideConfiguredSourceSetMarksBindingConflict(t *testing.T) {
	root := t.TempDir()
	writeWorkspace(t, root, map[string]string{
		"tsconfig.json":                      `{"compilerOptions":{"strict":true,"module":"preserve","moduleResolution":"bundler"},"include":["src/**/*.ts"]}`,
		"packages/contracts/tsconfig.json":   `{"compilerOptions":{"strict":true,"module":"preserve","moduleResolution":"bundler"},"include":["src/**/*.ts"]}`,
		"packages/contracts/dist/index.d.ts": "export declare function load(): void;\n//# sourceMappingURL=index.d.ts.map\n",
		"packages/contracts/src/legacy.ts":   "export function load() {}\n",
		"packages/contracts/src/index.ts":    "export function load() {}\n",
		"src/app.ts":                         `import { load } from "../packages/contracts/dist"; load();`,
		"source-links.json":                  `{"version":1,"entries":[{"checkedEntry":"packages/contracts/dist/index.d.ts","sourceEntry":"packages/contracts/src/index.ts"}]}`,
	})
	writeStartDeclarationMap(t, filepath.Join(root, "packages/contracts/dist/index.d.ts"), filepath.Join(root, "packages/contracts/src/legacy.ts"))
	snapshot, err := BuildSnapshot(context.Background(), BuildSnapshotRequest{
		WorkspaceRoot: root,
		Files:         []string{filepath.Join(root, "src/app.ts")},
		SourceLinks:   "source-links.json",
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(snapshot.DeclarationSources) != 1 || semanticDeclaration(t, snapshot, snapshot.DeclarationSources[0].SourceDecl).Location.File != "packages/contracts/src/legacy.ts" {
		t.Fatalf("precise map link was not preserved: %+v", snapshot.DeclarationSources)
	}
	if len(snapshot.SourceBindings) != 1 {
		t.Fatalf("configured binding attempts: %+v", snapshot.SourceBindings)
	}
	binding := snapshot.SourceBindings[0]
	if binding.SourceFile != "packages/contracts/src/index.ts" || !slices.Equal(binding.Incomplete, []string{AssociationConflict}) {
		t.Fatalf("binding with a map link outside its source set: %+v", binding)
	}
}

func TestDeclarationMapRejectsNearbyUnrelatedAndAmbiguousAnchors(t *testing.T) {
	tests := []struct {
		name           string
		generated      string
		mapAt          string
		source         string
		mappingTargets []int
		wantOutcome    string
		wantCandidates int
	}{
		{
			name:           "nearby point",
			generated:      "export declare function load(): void;\n//# sourceMappingURL=index.d.ts.map\n",
			mapAt:          "load",
			source:         "export function load() {}\n",
			mappingTargets: []int{0},
			wantOutcome:    "unresolved",
		},
		{
			name:           "unrelated declaration",
			generated:      "export declare function load(): void;\n//# sourceMappingURL=index.d.ts.map\n",
			mapAt:          "export",
			source:         "export function other() {}\nexport function load() {}\n",
			mappingTargets: []int{0},
			wantOutcome:    "unresolved",
		},
		{
			// The second segment moves backward in the source, so the generator
			// keeps both segments at one generated position. Both points are in
			// the same declaration, but the map has no single anchor.
			name:           "two source segments at one anchor",
			generated:      "export declare function load(): void;\n//# sourceMappingURL=index.d.ts.map\n",
			mapAt:          "export",
			source:         "export function load() {}\n",
			mappingTargets: []int{strings.Index("export function load() {}\n", "load"), 0},
			wantOutcome:    "ambiguous",
			wantCandidates: 1,
		},
		{
			name:           "nested declaration anchor",
			generated:      "export declare function load(): void;\n//# sourceMappingURL=index.d.ts.map\n",
			mapAt:          "export",
			source:         "export function load() { function load() {} }\n",
			mappingTargets: []int{strings.LastIndex("export function load() { function load() {} }\n", "function load")},
			wantOutcome:    "unresolved",
		},
		{
			name:           "same name under unrelated function",
			generated:      "export declare function load(): void;\n//# sourceMappingURL=index.d.ts.map\n",
			mapAt:          "export",
			source:         "function owner() { function load() {} }\n",
			mappingTargets: []int{strings.Index("function owner() { function load() {} }\n", "function load")},
			wantOutcome:    "unresolved",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			root := t.TempDir()
			generatedPath := filepath.Join(root, "dist/index.d.ts")
			sourcePath := filepath.Join(root, "src/index.ts")
			writeWorkspace(t, root, map[string]string{
				"tsconfig.json":   `{"compilerOptions":{"strict":true,"module":"preserve","moduleResolution":"bundler"},"include":["src/**/*.ts"]}`,
				"dist/index.d.ts": test.generated,
				"src/index.ts":    test.source,
				"src/app.ts":      `export { load } from "../dist";`,
			})
			generator := sourcemap.NewGenerator("index.d.ts", "", tspath.RootedDirectoryPathFromAbsolute(filepath.Dir(generatedPath)), tspath.CaseSensitive)
			sourceIndex := generator.AddSource(tspath.RootedFilePathFromAbsolute(sourcePath))
			generatedAnchor := strings.Index(test.generated, test.mapAt)
			generatedLine, generatedColumn := sourceMapLineColumn(test.generated, generatedAnchor)
			for _, sourceAnchor := range test.mappingTargets {
				sourceLine, sourceColumn := sourceMapLineColumn(test.source, sourceAnchor)
				if err := generator.AddSourceMapping(generatedLine, core.UTF16Offset(generatedColumn), sourceIndex, sourceLine, core.UTF16Offset(sourceColumn)); err != nil {
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
			snapshot, err := BuildSnapshot(context.Background(), BuildSnapshotRequest{
				WorkspaceRoot: root, Files: []string{filepath.Join(root, "src/app.ts")},
			})
			if err != nil {
				t.Fatal(err)
			}
			if len(snapshot.DeclarationSources) != 0 || len(snapshot.DeclarationSourceOutcomes) != 1 || snapshot.DeclarationSourceOutcomes[0].Outcome != test.wantOutcome {
				t.Fatalf("unexpected map validation result: links=%+v outcomes=%+v", snapshot.DeclarationSources, snapshot.DeclarationSourceOutcomes)
			}
			if len(snapshot.DeclarationSourceCandidates) != test.wantCandidates {
				t.Fatalf("candidate declarations=%+v, want %d", snapshot.DeclarationSourceCandidates, test.wantCandidates)
			}
		})
	}
}

func TestDeclarationMapReportsMalformedUnavailableAndEmbeddedSource(t *testing.T) {
	for _, outcome := range []string{"malformed_map", "unavailable_source", "unsupported"} {
		t.Run(outcome, func(t *testing.T) {
			root := t.TempDir()
			generated := "export declare function load(): void;\n//# sourceMappingURL=index.d.ts.map\n"
			source := "export function load() {}\n"
			generatedPath := filepath.Join(root, "dist/index.d.ts")
			sourcePath := filepath.Join(root, "src/index.ts")
			files := map[string]string{
				"tsconfig.json":   `{"compilerOptions":{"strict":true,"module":"preserve","moduleResolution":"bundler"},"include":["src/**/*.ts"]}`,
				"dist/index.d.ts": generated,
				"src/app.ts":      `export { load } from "../dist";`,
			}
			if outcome != "unavailable_source" {
				files["src/index.ts"] = source
			}
			writeWorkspace(t, root, files)
			mapPath := generatedPath + ".map"
			if outcome == "malformed_map" {
				if err := os.WriteFile(mapPath, []byte("{not json"), 0o644); err != nil {
					t.Fatal(err)
				}
			} else {
				generator := sourcemap.NewGenerator("index.d.ts", "", tspath.RootedDirectoryPathFromAbsolute(filepath.Dir(generatedPath)), tspath.CaseSensitive)
				sourceIndex := generator.AddSource(tspath.RootedFilePathFromAbsolute(sourcePath))
				if outcome == "unsupported" {
					if err := generator.SetSourceContent(sourceIndex, source); err != nil {
						t.Fatal(err)
					}
				}
				if err := generator.AddSourceMapping(0, core.UTF16Offset(0), sourceIndex, 0, core.UTF16Offset(0)); err != nil {
					t.Fatal(err)
				}
				contents, err := json.Marshal(generator.RawSourceMap())
				if err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(mapPath, contents, 0o644); err != nil {
					t.Fatal(err)
				}
			}
			snapshot, err := BuildSnapshot(context.Background(), BuildSnapshotRequest{
				WorkspaceRoot: root, Files: []string{filepath.Join(root, "src/app.ts")},
			})
			if err != nil {
				t.Fatal(err)
			}
			if len(snapshot.DeclarationSources) != 0 || len(snapshot.DeclarationSourceOutcomes) != 1 || snapshot.DeclarationSourceOutcomes[0].Outcome != outcome {
				t.Fatalf("unexpected map result: links=%+v outcomes=%+v", snapshot.DeclarationSources, snapshot.DeclarationSourceOutcomes)
			}
		})
	}
}

func TestProjectReferenceRedirectControlsTheCheckedDeclaration(t *testing.T) {
	for _, disabled := range []bool{false, true} {
		name := "enabled"
		if disabled {
			name = "disabled"
		}
		t.Run(name, func(t *testing.T) {
			root := t.TempDir()
			writeWorkspace(t, root, map[string]string{
				"contracts/tsconfig.json": `{"compilerOptions":{"composite":true,"declaration":true,"rootDir":"src","outDir":"dist"},"include":["src/**/*.ts"]}`,
				"contracts/src/index.ts":  "export interface Contract { id: string }\nexport function load(): Contract { return { id: \"x\" }; }\n",
				"app/tsconfig.json":       fmt.Sprintf(`{"compilerOptions":{"target":"es2022","module":"nodenext","moduleResolution":"nodenext","noEmit":true,"baseUrl":".","paths":{"@contracts":["../contracts/dist/index.d.ts"]},"disableSourceOfProjectReferenceRedirect":%t},"references":[{"path":"../contracts"}],"include":["src/**/*.ts"]}`, disabled),
				"app/src/consumer.ts":     `import { load } from "@contracts"; export const result = load();`,
			})
			snapshot, err := BuildSnapshot(context.Background(), BuildSnapshotRequest{
				WorkspaceRoot: root,
				Files:         []string{filepath.Join(root, "app/src/consumer.ts")},
				TsconfigFiles: true,
			})
			if err != nil {
				t.Fatal(err)
			}
			sourcePresent := slices.ContainsFunc(snapshot.Inputs, func(input SnapshotInput) bool {
				return input.Exists && strings.HasSuffix(filepath.ToSlash(input.Path), "/contracts/src/index.ts")
			})
			generatedPresent := slices.ContainsFunc(snapshot.Inputs, func(input SnapshotInput) bool {
				return input.Exists && strings.HasSuffix(filepath.ToSlash(input.Path), "/contracts/dist/index.d.ts")
			})
			if sourcePresent == disabled || generatedPresent {
				t.Fatalf("source presence=%t, generated declaration presence=%t, redirects disabled=%t", sourcePresent, generatedPresent, disabled)
			}
		})
	}
}

func TestSourceEntryReportsIncompleteExportInventoryAndKeepsKnownBinding(t *testing.T) {
	root := t.TempDir()
	writeWorkspace(t, root, map[string]string{
		"tsconfig.json":                      `{"compilerOptions":{"strict":true,"module":"preserve","moduleResolution":"bundler"},"include":["src/**/*.ts"]}`,
		"packages/contracts/tsconfig.json":   `{"compilerOptions":{"strict":true,"module":"preserve","moduleResolution":"bundler"},"include":["src/**/*.ts"]}`,
		"packages/contracts/dist/index.d.ts": `export declare const known: number;`,
		"packages/contracts/src/index.ts":    `export const known = 1; export * from "./missing";`,
		"src/app.ts":                         `import { known } from "../packages/contracts/dist"; export const result = known;`,
		"source-links.json":                  `{"version":1,"entries":[{"checkedEntry":"packages/contracts/dist/index.d.ts","sourceEntry":"packages/contracts/src/index.ts"}]}`,
	})
	snapshot, err := BuildSnapshot(context.Background(), BuildSnapshotRequest{
		WorkspaceRoot: root,
		Files:         []string{filepath.Join(root, "src/app.ts")},
		SourceLinks:   "source-links.json",
	})
	if err != nil {
		t.Fatal(err)
	}
	if !slices.ContainsFunc(snapshot.SourceEntries, func(item SourceEntryFact) bool {
		return item.Applied && slices.Contains(item.Incomplete, AssociationUnresolved)
	}) {
		t.Fatalf("unresolved source star did not mark the source entry inventory incomplete: %+v", snapshot.SourceEntries)
	}
	var known *SourceBindingFact
	for index := range snapshot.SourceBindings {
		if snapshot.SourceBindings[index].Name == "known" && snapshot.SourceBindings[index].Space == SpaceValue {
			known = &snapshot.SourceBindings[index]
			break
		}
	}
	if known == nil || known.SourceFile != "packages/contracts/src/index.ts" || len(known.Incomplete) != 0 {
		t.Fatalf("known positive source binding was lost with the incomplete inventory: %+v", known)
	}
}

func TestSourceLinksRejectInvalidConfiguration(t *testing.T) {
	cases := []struct {
		name string
		json string
	}{
		{name: "unsupported version", json: `{"version":2,"entries":[]}`},
		{name: "unknown field", json: `{"version":1,"entries":[],"extra":true}`},
		{name: "workspace escape", json: `{"version":1,"entries":[{"checkedEntry":"../outside.d.ts","sourceEntry":"src/index.ts"}]}`},
		{name: "missing source config", json: `{"version":1,"entries":[{"checkedEntry":"dist/index.d.ts","sourceEntry":"src/index.ts","sourceTsconfig":"missing.json"}]}`},
		{name: "contradictory entries", json: `{"version":1,"entries":[{"checkedEntry":"dist/index.d.ts","sourceEntry":"src/one.ts"},{"checkedEntry":"dist/index.d.ts","sourceEntry":"src/two.ts"}]}`},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			root := t.TempDir()
			writeWorkspace(t, root, map[string]string{"source-links.json": test.json})
			_, err := BuildSnapshot(context.Background(), BuildSnapshotRequest{WorkspaceRoot: root, SourceLinks: "source-links.json"})
			if err == nil || !strings.HasPrefix(err.Error(), "invalid --source-links:") {
				t.Fatalf("invalid config returned %v", err)
			}
		})
	}
}

func TestSourceTsconfigThatDoesNotOwnTheEntryIsUnsupported(t *testing.T) {
	root := t.TempDir()
	writeWorkspace(t, root, map[string]string{
		"tsconfig.json":                      `{"compilerOptions":{"strict":true,"module":"preserve","moduleResolution":"bundler"},"include":["src/**/*.ts"]}`,
		"packages/contracts/tsconfig.json":   `{"compilerOptions":{"strict":true,"module":"preserve","moduleResolution":"bundler"},"include":["other.ts"]}`,
		"packages/contracts/other.ts":        `export const other = 1;`,
		"packages/contracts/dist/index.d.ts": `export declare const known: number;`,
		"packages/contracts/src/index.ts":    `export const known = 1;`,
		"src/app.ts":                         `import { known } from "../packages/contracts/dist"; export const result = known;`,
		"source-links.json":                  `{"version":1,"entries":[{"checkedEntry":"packages/contracts/dist/index.d.ts","sourceEntry":"packages/contracts/src/index.ts","sourceTsconfig":"packages/contracts/tsconfig.json"}]}`,
	})
	snapshot, err := BuildSnapshot(context.Background(), BuildSnapshotRequest{
		WorkspaceRoot: root,
		Files:         []string{filepath.Join(root, "src/app.ts")},
		SourceLinks:   "source-links.json",
	})
	if err != nil {
		t.Fatal(err)
	}
	if !slices.ContainsFunc(snapshot.SourceEntries, func(item SourceEntryFact) bool {
		return item.Applied && slices.Contains(item.Incomplete, AssociationUnsupported)
	}) {
		t.Fatalf("source config that omits its entry was not reported unsupported: %+v", snapshot.SourceEntries)
	}
	if !slices.ContainsFunc(snapshot.SourceBindings, func(item SourceBindingFact) bool {
		return item.Name == "known" && slices.Contains(item.Incomplete, AssociationUnsupported) && item.SourceFile == ""
	}) {
		t.Fatalf("checked binding was not retained as an unsupported source attempt: %+v", snapshot.SourceBindings)
	}
}

// sourceMapLineColumn converts a byte offset to a source-map line and UTF-16
// column.
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

// writeStartDeclarationMap writes generatedPath.map with one segment from the
// start of the generated file to the start of sourcePath.
func writeStartDeclarationMap(t *testing.T, generatedPath, sourcePath string) string {
	t.Helper()
	generator := sourcemap.NewGenerator(filepath.Base(generatedPath), "", tspath.RootedDirectoryPathFromAbsolute(filepath.Dir(generatedPath)), tspath.CaseSensitive)
	sourceIndex := generator.AddSource(tspath.RootedFilePathFromAbsolute(sourcePath))
	if err := generator.AddSourceMapping(0, core.UTF16Offset(0), sourceIndex, 0, core.UTF16Offset(0)); err != nil {
		t.Fatal(err)
	}
	contents, err := json.Marshal(generator.RawSourceMap())
	if err != nil {
		t.Fatal(err)
	}
	mapPath := generatedPath + ".map"
	if err := os.WriteFile(mapPath, contents, 0o644); err != nil {
		t.Fatal(err)
	}
	return mapPath
}

func TestTypeOnlyReexportsPreserveExportAndSourceBindingSpaces(t *testing.T) {
	root := t.TempDir()
	writeWorkspace(t, root, map[string]string{
		"tsconfig.json":       `{"compilerOptions":{"module":"preserve","moduleResolution":"bundler"},"include":["src/**/*.ts"]}`,
		"checked/base.d.ts":   `export declare class Widget { value: string }`,
		"checked/index.d.ts":  `export type * from "./base";`,
		"checked/value.d.ts":  `export declare class Widget { value: string }`,
		"src/base.ts":         `export class Widget { value = "x" }`,
		"src/middle.ts":       `export type { Widget } from "./base";`,
		"src/root.ts":         `export * from "./middle";`,
		"src/direct.ts":       `export type * from "./base";`,
		"src/unrestricted.ts": `export * from "./base";`,
		"src/combined.ts":     `export * from "./unrestricted"; export type * from "./base";`,
		"src/alias-first.ts":  `export * from "./middle"; export * from "./base";`,
		"src/namespace.ts":    `export type * as api from "./base";`,
		"src/top.ts":          `export { Widget } from "./root";`,
		"src/app.ts":          `export type { Widget } from "../checked"; import { Widget as ValueWidget } from "../checked/value"; export const instance: ValueWidget | undefined = undefined;`,
		"source-links.json":   `{"version":1,"entries":[{"checkedEntry":"checked/index.d.ts","sourceEntry":"src/direct.ts"},{"checkedEntry":"checked/value.d.ts","sourceEntry":"src/direct.ts"}]}`,
	})
	files := []string{filepath.Join(root, "src/app.ts"), filepath.Join(root, "src/root.ts"), filepath.Join(root, "src/direct.ts"), filepath.Join(root, "src/middle.ts"), filepath.Join(root, "src/base.ts"), filepath.Join(root, "src/unrestricted.ts"), filepath.Join(root, "src/combined.ts"), filepath.Join(root, "src/top.ts"), filepath.Join(root, "src/namespace.ts"), filepath.Join(root, "src/alias-first.ts")}
	snapshot, err := BuildSnapshot(context.Background(), BuildSnapshotRequest{WorkspaceRoot: root, Files: files, SourceLinks: "source-links.json"})
	if err != nil {
		t.Fatal(err)
	}
	for _, file := range []string{"src/middle.ts", "src/root.ts", "src/direct.ts", "src/top.ts", "src/alias-first.ts"} {
		if _, found := findSemanticExport(snapshot, file, "Widget", SpaceValue); found {
			t.Fatalf("%s falsely exports a value", file)
		}
		semanticExport(t, snapshot, file, "Widget", SpaceType)
	}
	semanticExport(t, snapshot, "src/combined.ts", "Widget", SpaceValue)
	if _, found := findSemanticExport(snapshot, "src/namespace.ts", "api", SpaceValue); found {
		t.Fatal("type-only namespace falsely exported a value")
	}
	if ns := semanticExport(t, snapshot, "src/namespace.ts", "api", SpaceType); len(ns.Targets) != 1 || len(ns.Incomplete) != 0 {
		t.Fatalf("type-only namespace lost its checked endpoint: %+v", ns)
	}
	if len(snapshot.SourceBindings) != 3 {
		t.Fatalf("unexpected checked bindings: %+v", snapshot.SourceBindings)
	}
	for _, binding := range snapshot.SourceBindings {
		if binding.Space == SpaceValue {
			if binding.CheckedFile != "checked/value.d.ts" || len(binding.SourceDeclarations) != 0 || !slices.Contains(binding.Incomplete, AssociationUnresolved) {
				t.Fatalf("type-only source falsely supplied a value: %+v", binding)
			}
		} else if binding.Space != SpaceType || len(binding.Incomplete) != 0 || len(binding.SourceDeclarations) != 1 {
			t.Fatalf("type-only checked/source binding is incomplete: %+v", binding)
		}
	}
}

func TestSourceEntriesUseResolvedCheckedIdentity(t *testing.T) {
	for _, preserve := range []bool{false, true} {
		for _, sameSource := range []bool{false, true} {
			t.Run(fmt.Sprintf("preserve=%t/sameSource=%t", preserve, sameSource), func(t *testing.T) {
				root := t.TempDir()
				writeWorkspace(t, root, map[string]string{
					"tsconfig.json":               fmt.Sprintf(`{"compilerOptions":{"module":"preserve","moduleResolution":"bundler","preserveSymlinks":%t},"include":["src/**/*.ts"]}`, preserve),
					"packages/checked/index.d.ts": `export declare const value: number;`,
					"src/a.ts":                    `export const value = 1;`,
					"src/b.ts":                    `export const value = 2;`,
					"src/app.ts":                  `import { value as a } from "one"; import { value as b } from "two"; export const sum = a + b;`,
				})
				for _, name := range []string{"one", "two"} {
					alias := filepath.Join(root, "node_modules", name)
					if err := os.MkdirAll(filepath.Dir(alias), 0o755); err != nil {
						t.Fatal(err)
					}
					if err := os.Symlink(filepath.Join(root, "packages/checked"), alias); err != nil {
						t.Skipf("symlinks unavailable: %v", err)
					}
				}
				other := "src/b.ts"
				if sameSource {
					other = "src/a.ts"
				}
				writeWorkspace(t, root, map[string]string{"source-links.json": fmt.Sprintf(`{"version":1,"entries":[{"checkedEntry":"node_modules/one/index.d.ts","sourceEntry":"src/a.ts"},{"checkedEntry":"node_modules/two/index.d.ts","sourceEntry":%q}]}`, other)})
				snapshot, err := BuildSnapshot(context.Background(), BuildSnapshotRequest{WorkspaceRoot: root, Files: []string{filepath.Join(root, "src/app.ts")}, SourceLinks: "source-links.json"})
				if !preserve && !sameSource {
					if err == nil || !strings.Contains(err.Error(), "contradictory entries") {
						t.Fatalf("same resolved file accepted conflicting entries: %v", err)
					}
					return
				}
				if err != nil {
					t.Fatal(err)
				}
				want := 2
				if !preserve {
					want = 1
				}
				if len(snapshot.SourceEntries) != want || len(snapshot.SourceBindings) != want {
					t.Fatalf("compiler identities were merged or duplicated: entries=%+v bindings=%+v", snapshot.SourceEntries, snapshot.SourceBindings)
				}
				for _, entry := range snapshot.SourceEntries {
					if !entry.Applied {
						t.Fatalf("entry unexpectedly unapplied: %+v", entry)
					}
				}
			})
		}
	}
	t.Run("mixed program symlink options", func(t *testing.T) {
		root := t.TempDir()
		app := `import { value as a } from "one"; import { value as b } from "two"; export const sum = a + b;`
		writeWorkspace(t, root, map[string]string{
			"a/tsconfig.json": `{"compilerOptions":{"module":"preserve","moduleResolution":"bundler","preserveSymlinks":false},"files":["app.ts"]}`,
			"b/tsconfig.json": `{"compilerOptions":{"module":"preserve","moduleResolution":"bundler","preserveSymlinks":true},"files":["app.ts"]}`,
			"a/app.ts":        app, "b/app.ts": app,
			"packages/checked/index.d.ts": `export declare const value: number;`,
			"source/index.ts":             `export const value = 1;`,
			"source-links.json":           `{"version":1,"entries":[{"checkedEntry":"node_modules/one/index.d.ts","sourceEntry":"source/index.ts"},{"checkedEntry":"node_modules/two/index.d.ts","sourceEntry":"source/index.ts"}]}`,
		})
		for _, name := range []string{"one", "two"} {
			alias := filepath.Join(root, "node_modules", name)
			if err := os.MkdirAll(filepath.Dir(alias), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.Symlink(filepath.Join(root, "packages/checked"), alias); err != nil {
				t.Skipf("symlinks unavailable: %v", err)
			}
		}
		snapshot, err := BuildSnapshot(context.Background(), BuildSnapshotRequest{WorkspaceRoot: root, Files: []string{filepath.Join(root, "a/app.ts"), filepath.Join(root, "b/app.ts")}, SourceLinks: "source-links.json"})
		if err != nil {
			t.Fatal(err)
		}
		files := make(map[string]bool)
		declarations := make(map[string]bool)
		for _, binding := range snapshot.SourceBindings {
			files[binding.CheckedFile] = true
			for _, id := range binding.CheckedDeclarations {
				declarations[id] = true
			}
		}
		if len(snapshot.SourceEntries) != 2 || len(snapshot.SourceBindings) != 3 || len(files) != 3 || len(declarations) != 3 {
			t.Fatalf("a shared resolved file collapsed preserved instances: entries=%+v bindings=%+v", snapshot.SourceEntries, snapshot.SourceBindings)
		}
	})
}
