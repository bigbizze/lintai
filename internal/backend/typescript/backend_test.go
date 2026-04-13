package typescript

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestBuildSnapshotExposesStableFunctionKeysAndCalls(t *testing.T) {
	repoRoot, err := filepath.Abs("../../..")
	if err != nil {
		t.Fatal(err)
	}
	workspaceRoot := filepath.Join(repoRoot, "testdata/fixtures/workspace")

	snapshot, err := New().BuildSnapshot(context.Background(), repoRoot, workspaceRoot)
	if err != nil {
		t.Fatal(err)
	}

	if len(snapshot.FunctionsByKey) != len(snapshot.Functions) {
		t.Fatalf("expected FunctionsByKey to cover all functions, got %d keys for %d functions", len(snapshot.FunctionsByKey), len(snapshot.Functions))
	}

	for _, fn := range snapshot.Functions {
		if strings.Contains(fn.SemanticKey, "@") {
			t.Fatalf("expected stable semantic key without position marker, got %q", fn.SemanticKey)
		}
	}

	foundHelperEffect := false
	foundComputeHelper := false
	for _, edge := range snapshot.CallEdges {
		switch {
		case edge.FromName == "loadValue" && edge.ToName == "fetchData":
			foundHelperEffect = true
		case edge.FromName == "compute" && edge.ToName == "loadValue":
			foundComputeHelper = true
		}
	}
	if !foundHelperEffect || !foundComputeHelper {
		t.Fatalf("expected fixture call edges, got %+v", snapshot.CallEdges)
	}
}

func TestSnapshotVersionIgnoresFileOrderAndAbsoluteRoot(t *testing.T) {
	t.Parallel()

	rootOne := t.TempDir()
	rootTwo := t.TempDir()

	firstOne := writeWorkspaceFile(t, rootOne, "src/a.ts", "export const alpha = 1;\n")
	secondOne := writeWorkspaceFile(t, rootOne, "src/b.ts", "export const beta = 2;\n")
	firstTwo := writeWorkspaceFile(t, rootTwo, "src/a.ts", "export const alpha = 1;\n")
	secondTwo := writeWorkspaceFile(t, rootTwo, "src/b.ts", "export const beta = 2;\n")

	left, err := snapshotVersion(rootOne, []string{secondOne, firstOne})
	if err != nil {
		t.Fatal(err)
	}
	right, err := snapshotVersion(rootTwo, []string{firstTwo, secondTwo})
	if err != nil {
		t.Fatal(err)
	}
	if left != right {
		t.Fatalf("expected stable hash across order and roots, got %q != %q", left, right)
	}
}

func TestSnapshotVersionUsesDelimitersBetweenPathsAndContents(t *testing.T) {
	t.Parallel()

	rootOne := t.TempDir()
	rootTwo := t.TempDir()

	ab := writeWorkspaceFile(t, rootOne, "ab", "cd")
	abc := writeWorkspaceFile(t, rootTwo, "abc", "d")

	left, err := snapshotVersion(rootOne, []string{ab})
	if err != nil {
		t.Fatal(err)
	}
	right, err := snapshotVersion(rootTwo, []string{abc})
	if err != nil {
		t.Fatal(err)
	}
	if left == right {
		t.Fatalf("expected different hashes for path/content boundary cases, got %q", left)
	}
}

func TestBuildSnapshotExposesExpandedMetadataAndCapabilities(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	writeWorkspaceFile(t, root, "tsconfig.json", `{
  "compilerOptions": {
    "target": "es2022",
    "module": "nodenext",
    "moduleResolution": "nodenext",
    "noEmit": true
  },
  "include": ["src/**/*.ts"]
}
`)
	writeWorkspaceFile(t, root, "src/data/db.ts", `export default async function db(query: string): Promise<number> {
	return query.length;
}

export type DbConfig = {
	dsn: string;
};

export function helper(config: DbConfig): number {
	return config.dsn.length;
}
`)
	writeWorkspaceFile(t, root, "src/service/repository.ts", `import db, { helper as runHelper, type DbConfig } from "../data/db";
import * as dbModule from "../data/db";

type LocalConfig = DbConfig;

export class Repository {
	async save(config: DbConfig): Promise<number> {
		await db("select 1");
		return runHelper(config) + (await dbModule.default(config.dsn));
	}
}

export function mirror(config: LocalConfig): DbConfig {
	return config;
}
`)

	snapshot, err := New().BuildSnapshot(context.Background(), root, root)
	if err != nil {
		t.Fatal(err)
	}

	if !New().Capabilities().SupportsQueryKind("typeRefs") {
		t.Fatal("expected backend to advertise typeRefs query support")
	}
	if !New().Capabilities().SupportsQueryKind("accesses") {
		t.Fatal("expected backend to advertise accesses query support")
	}

	var dbFnFound bool
	for _, fn := range snapshot.Functions {
		if fn.Name != "db" {
			continue
		}
		dbFnFound = true
		if !fn.IsExported || !fn.IsAsync {
			t.Fatalf("expected db to be exported async, got %+v", fn)
		}
		if fn.ParameterCount != 1 {
			t.Fatalf("expected db parameter count 1, got %+v", fn)
		}
		if fn.ReturnTypeText != "Promise<number>" {
			t.Fatalf("expected db return type Promise<number>, got %+v", fn)
		}
		if len(fn.ParameterTypeTexts) != 1 || fn.ParameterTypeTexts[0] != "string" {
			t.Fatalf("expected db parameter types [string], got %+v", fn.ParameterTypeTexts)
		}
	}
	if !dbFnFound {
		t.Fatalf("expected to find db function in snapshot, got %+v", snapshot.Functions)
	}

	var mixedImportFound bool
	var namespaceImportFound bool
	for _, edge := range snapshot.ImportEdges {
		if edge.FromPath != "src/service/repository.ts" {
			continue
		}
		switch len(edge.ImportedSymbols) {
		case 3:
			mixedImportFound = true
			if !edge.HasDefaultImport || !edge.HasNamedImports || edge.HasNamespaceImport || edge.IsTypeOnly {
				t.Fatalf("expected mixed import flags, got %+v", edge)
			}
			if edge.ImportedSymbols[0].Name != "db" || edge.ImportedSymbols[0].Kind != "default" || edge.ImportedSymbols[0].IsTypeOnly {
				t.Fatalf("unexpected default import symbol %+v", edge.ImportedSymbols[0])
			}
			if edge.ImportedSymbols[1].Name != "runHelper" || edge.ImportedSymbols[1].Kind != "named" || edge.ImportedSymbols[1].IsTypeOnly {
				t.Fatalf("unexpected named import symbol %+v", edge.ImportedSymbols[1])
			}
			if edge.ImportedSymbols[2].Name != "DbConfig" || edge.ImportedSymbols[2].Kind != "named" || !edge.ImportedSymbols[2].IsTypeOnly {
				t.Fatalf("unexpected type-only import symbol %+v", edge.ImportedSymbols[2])
			}
		case 1:
			if edge.ImportedSymbols[0].Kind == "namespace" {
				namespaceImportFound = true
				if !edge.HasNamespaceImport || edge.HasDefaultImport || edge.HasNamedImports || edge.IsTypeOnly {
					t.Fatalf("expected namespace import flags, got %+v", edge)
				}
			}
		}
	}
	if !mixedImportFound || !namespaceImportFound {
		t.Fatalf("expected repository imports to expose structured symbols, got %+v", snapshot.ImportEdges)
	}

	var targetFound bool
	for _, ref := range snapshot.TypeRefs {
		if ref.FilePath == "src/service/repository.ts" && ref.Name == "DbConfig" && ref.TargetPath == "src/data/db.ts" {
			targetFound = true
			break
		}
	}
	if !targetFound {
		t.Fatalf("expected DbConfig type ref to resolve to src/data/db.ts, got %+v", snapshot.TypeRefs)
	}
}

func TestBuildSnapshotExposesAccessesAndIgnoresShadowedAmbientRoots(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	writeWorkspaceFile(t, root, "tsconfig.json", `{
  "compilerOptions": {
    "target": "es2022",
    "module": "nodenext",
    "moduleResolution": "nodenext",
    "noEmit": true
  },
  "include": ["src/**/*.ts"]
}
`)
	writeWorkspaceFile(t, root, "src/env.ts", `export function readEnv() {
	return import.meta.env.API_URL;
}
`)
	writeWorkspaceFile(t, root, "src/browser.ts", `export function readLocation() {
	return window.location.href;
}
`)
	writeWorkspaceFile(t, root, "src/shadowed.ts", `const window = { location: { href: "local" } };

export function localWindow() {
	return window.location.href;
}
`)

	snapshot, err := New().BuildSnapshot(context.Background(), root, root)
	if err != nil {
		t.Fatal(err)
	}

	var importMetaFound bool
	var windowFound bool
	for _, access := range snapshot.Accesses {
		switch {
		case access.FilePath == "src/env.ts" && access.Root == "import.meta" && access.AccessPath == "import.meta.env" && access.Origin == "special_form":
			importMetaFound = true
			if access.Range.File != "src/env.ts" || access.Range.StartLine != 2 {
				t.Fatalf("unexpected import.meta access range %+v", access.Range)
			}
		case access.FilePath == "src/browser.ts" && access.Root == "window" && access.AccessPath == "window.location" && access.Origin == "ambient_decl":
			windowFound = true
		case access.FilePath == "src/shadowed.ts":
			t.Fatalf("expected shadowed window access to be ignored, got %+v", access)
		}
	}
	if !importMetaFound {
		t.Fatalf("expected import.meta access in snapshot, got %+v", snapshot.Accesses)
	}
	if !windowFound {
		t.Fatalf("expected ambient window access in snapshot, got %+v", snapshot.Accesses)
	}
}

func writeWorkspaceFile(t *testing.T, root, relativePath, contents string) string {
	t.Helper()

	path := filepath.Join(root, filepath.FromSlash(relativePath))
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(contents), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}
