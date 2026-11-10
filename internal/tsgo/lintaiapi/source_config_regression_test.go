package lintaiapi

import (
	"context"
	"path/filepath"
	"slices"
	"testing"
)

func TestSourceConfigOwnersAgreeOnlyWhenProgramInputsAgree(t *testing.T) {
	const options = `"module":"preserve","moduleResolution":"bundler","paths":{"@fixture/*":["./src/*"]}`
	root := t.TempDir()
	owner := `{"compilerOptions":{` + options + `},"files":["src/index.ts"]}`
	writeWorkspace(t, root, map[string]string{
		"tsconfig.json":             `{"compilerOptions":{"module":"preserve","moduleResolution":"bundler"},"files":["app/main.ts"]}`,
		"app/main.ts":               `export { Widget } from "../contracts/dist";`,
		"contracts/dist/index.d.ts": `export declare class Widget {}`,
		"contracts/src/index.ts":    `export class Widget {}`,
		"contracts/src/extra.ts":    `export const extra = 1;`,
		"contracts/tsconfig.json":   `{"files":[],"references":[{"path":"./a.json"},{"path":"./b.json"}]}`,
		"contracts/a.json":          owner,
		"contracts/b.json":          owner,
		"source-links.json":         `{"version":1,"entries":[{"checkedEntry":"contracts/dist/index.d.ts","sourceEntry":"contracts/src/index.ts"}]}`,
	})
	build := func(t *testing.T) *Snapshot {
		t.Helper()
		snapshot, err := BuildSnapshot(context.Background(), BuildSnapshotRequest{
			WorkspaceRoot: root, Files: []string{filepath.Join(root, "app/main.ts")}, SourceLinks: "source-links.json", TsconfigFiles: true,
		})
		if err != nil {
			t.Fatal(err)
		}
		return snapshot
	}

	equivalent := build(t)
	if len(equivalent.SourceEntries) != 1 || !equivalent.SourceEntries[0].Applied || len(equivalent.SourceEntries[0].Incomplete) != 0 {
		t.Fatalf("equivalent owners were not accepted: %+v", equivalent.SourceEntries)
	}
	for _, space := range []string{SpaceType, SpaceValue} {
		index := slices.IndexFunc(equivalent.SourceBindings, func(binding SourceBindingFact) bool { return binding.Name == "Widget" && binding.Space == space })
		if index < 0 {
			t.Fatalf("missing %s association for equivalent owners: %+v", space, equivalent.SourceBindings)
		}
		binding := equivalent.SourceBindings[index]
		if binding.SourceFile != "contracts/src/index.ts" || len(binding.CheckedDeclarations) != 1 || len(binding.SourceDeclarations) != 1 || len(binding.Incomplete) != 0 || binding.CheckedDeclarations[0] == binding.SourceDeclarations[0] {
			t.Fatalf("equivalent owners lost checked or source evidence in %s space: %+v", space, binding)
		}
	}
	if slices.ContainsFunc(equivalent.Modules, func(item Module) bool { return item.Path == "contracts/src/index.ts" }) {
		t.Fatalf("auxiliary source became an analyzed root: %+v", equivalent.Modules)
	}

	cases := []struct {
		name   string
		second string
	}{
		{"compiler options", `{"compilerOptions":{` + options + `,"strict":false},"files":["src/index.ts"]}`},
		{"root files", `{"compilerOptions":{` + options + `},"files":["src/index.ts","src/extra.ts"]}`},
		{"project references", `{"compilerOptions":{` + options + `},"files":["src/index.ts"],"references":[{"path":"./missing"}]}`},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			writeWorkspace(t, root, map[string]string{"contracts/b.json": test.second})
			different := build(t)
			if len(different.SourceEntries) != 1 || !different.SourceEntries[0].Applied || !slices.Contains(different.SourceEntries[0].Incomplete, AssociationUnsupported) {
				t.Fatalf("different %s did not make owner selection unsupported: %+v", test.name, different.SourceEntries)
			}
			for _, space := range []string{SpaceType, SpaceValue} {
				before := slices.IndexFunc(equivalent.SourceBindings, func(binding SourceBindingFact) bool { return binding.Name == "Widget" && binding.Space == space })
				after := slices.IndexFunc(different.SourceBindings, func(binding SourceBindingFact) bool { return binding.Name == "Widget" && binding.Space == space })
				if after < 0 {
					t.Fatalf("missing checked %s binding with different owners: %+v", space, different.SourceBindings)
				}
				binding := different.SourceBindings[after]
				if binding.SourceFile != "" || len(binding.SourceDeclarations) != 0 || !slices.Contains(binding.Incomplete, AssociationUnsupported) || !slices.Equal(binding.CheckedDeclarations, equivalent.SourceBindings[before].CheckedDeclarations) {
					t.Fatalf("different owners did not retain checked %s evidence: %+v", space, binding)
				}
			}
		})
	}
}

func TestSourceConfigOwnerDirectoriesCanChangeResolution(t *testing.T) {
	root := t.TempDir()
	owner := `{"compilerOptions":{"module":"preserve","moduleResolution":"bundler","types":["shared"]},"files":["../src/index.ts"]}`
	writeWorkspace(t, root, map[string]string{
		"tsconfig.json":                                     `{"compilerOptions":{"module":"preserve","moduleResolution":"bundler"},"files":["app/main.ts"]}`,
		"app/main.ts":                                       `export { Widget } from "../contracts/dist";`,
		"contracts/dist/index.d.ts":                         `export declare class Widget {}`,
		"contracts/src/index.ts":                            `export { Widget } from "ambient-api";`,
		"contracts/tsconfig.json":                           `{"files":[],"references":[{"path":"./a"},{"path":"./b"}]}`,
		"contracts/a/tsconfig.json":                         owner,
		"contracts/b/tsconfig.json":                         owner,
		"contracts/a/node_modules/@types/shared/index.d.ts": `declare module "ambient-api" { export class Widget { name: string } }`,
		"contracts/b/node_modules/@types/shared/index.d.ts": `declare module "ambient-api" { export class Widget { value: number } }`,
	})
	build := func(selector string) *Snapshot {
		t.Helper()
		config := ""
		if selector != "" {
			config = `,"sourceTsconfig":"contracts/` + selector + `/tsconfig.json"`
		}
		writeWorkspace(t, root, map[string]string{
			"source-links.json": `{"version":1,"entries":[{"checkedEntry":"contracts/dist/index.d.ts","sourceEntry":"contracts/src/index.ts"` + config + `}]}`,
		})
		snapshot, err := BuildSnapshot(context.Background(), BuildSnapshotRequest{
			WorkspaceRoot: root, Files: []string{filepath.Join(root, "app/main.ts")}, SourceLinks: "source-links.json", TsconfigFiles: true,
		})
		if err != nil {
			t.Fatal(err)
		}
		return snapshot
	}
	valueBinding := func(snapshot *Snapshot) SourceBindingFact {
		t.Helper()
		index := slices.IndexFunc(snapshot.SourceBindings, func(binding SourceBindingFact) bool { return binding.Name == "Widget" && binding.Space == SpaceValue })
		if index < 0 {
			t.Fatalf("missing checked value binding: %+v", snapshot.SourceBindings)
		}
		return snapshot.SourceBindings[index]
	}
	first := valueBinding(build("a"))
	second := valueBinding(build("b"))
	if first.SourceFile != "contracts/src/index.ts" || len(first.Incomplete) != 0 || len(first.SourceDeclarations) != 1 ||
		second.SourceFile != "contracts/src/index.ts" || len(second.Incomplete) != 0 || len(second.SourceDeclarations) != 1 ||
		first.SourceDeclarations[0] == second.SourceDeclarations[0] || !slices.Equal(first.CheckedDeclarations, second.CheckedDeclarations) {
		t.Fatalf("owner directories did not resolve to different source declarations: first=%+v second=%+v", first, second)
	}
	ambiguous := build("")
	binding := valueBinding(ambiguous)
	if len(ambiguous.SourceEntries) != 1 || !slices.Contains(ambiguous.SourceEntries[0].Incomplete, AssociationUnsupported) ||
		binding.SourceFile != "" || !slices.Contains(binding.Incomplete, AssociationUnsupported) || !slices.Equal(binding.CheckedDeclarations, first.CheckedDeclarations) {
		t.Fatalf("different resolution contexts were treated as equivalent: entries=%+v binding=%+v", ambiguous.SourceEntries, binding)
	}
}

func TestFailedSourceConfigTracksMissingReferenceUntilRecovery(t *testing.T) {
	root := t.TempDir()
	writeWorkspace(t, root, map[string]string{
		"tsconfig.json":              `{"compilerOptions":{"module":"preserve","moduleResolution":"bundler"},"files":["app/main.ts"]}`,
		"app/main.ts":                `export { Widget } from "../contracts/dist";`,
		"contracts/dist/index.d.ts":  `export declare class Widget {}`,
		"contracts/src/index.ts":     `export { Widget } from "../dep/dist/index";`,
		"contracts/dep/src/index.ts": `export class Widget {}`,
		"contracts/custom.json":      `{"compilerOptions":{"module":"invalid"},"files":["src/index.ts"],"references":[{"path":"./dep"}]}`,
		"source-links.json":          `{"version":1,"entries":[{"checkedEntry":"contracts/dist/index.d.ts","sourceEntry":"contracts/src/index.ts","sourceTsconfig":"contracts/custom.json"}]}`,
	})
	build := func() *Snapshot {
		t.Helper()
		snapshot, err := BuildSnapshot(context.Background(), BuildSnapshotRequest{
			WorkspaceRoot: root, Files: []string{filepath.Join(root, "app/main.ts")}, SourceLinks: "source-links.json", TsconfigFiles: true,
		})
		if err != nil {
			t.Fatal(err)
		}
		return snapshot
	}
	before := build()
	missingReference := filepath.ToSlash(filepath.Join(root, "contracts/dep/tsconfig.json"))
	if !slices.ContainsFunc(before.Inputs, func(input SnapshotInput) bool { return input.Path == missingReference && !input.Exists }) {
		t.Fatalf("missing reference was lost when its parent config failed: %+v", before.Inputs)
	}
	if len(before.SourceEntries) != 1 || !slices.Contains(before.SourceEntries[0].Incomplete, AssociationUnsupported) {
		t.Fatalf("invalid parent config did not leave an unsupported association: %+v", before.SourceEntries)
	}

	writeWorkspace(t, root, map[string]string{
		"contracts/custom.json": `{"compilerOptions":{"module":"preserve","moduleResolution":"bundler"},"files":["src/index.ts"],"references":[{"path":"./dep"}]}`,
	})
	withoutReference := build()
	if !slices.ContainsFunc(withoutReference.SourceBindings, func(binding SourceBindingFact) bool {
		return binding.Name == "Widget" && binding.Space == SpaceValue && len(binding.CheckedDeclarations) == 1 && binding.SourceFile == "" && len(binding.Incomplete) != 0
	}) {
		t.Fatalf("missing reference did not leave the checked binding incomplete: %+v", withoutReference.SourceBindings)
	}

	writeWorkspace(t, root, map[string]string{
		"contracts/dep/tsconfig.json": `{"compilerOptions":{"composite":true,"declaration":true,"rootDir":"src","outDir":"dist","module":"preserve","moduleResolution":"bundler"},"files":["src/index.ts"]}`,
	})
	after := build()
	if !slices.ContainsFunc(after.SourceBindings, func(binding SourceBindingFact) bool {
		return binding.Name == "Widget" && binding.Space == SpaceValue && binding.SourceFile == "contracts/src/index.ts" && len(binding.CheckedDeclarations) == 1 && len(binding.SourceDeclarations) == 1 && len(binding.Incomplete) == 0 && binding.CheckedDeclarations[0] != binding.SourceDeclarations[0]
	}) {
		t.Fatalf("creating the observed reference did not recover the source binding: %+v", after.SourceBindings)
	}
}
