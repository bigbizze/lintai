package typescript

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/bigbizze/lintai/backend"
)

func TestBuildSnapshotExposesStableFunctionKeysAndCalls(t *testing.T) {
	repoRoot, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	workspaceRoot := filepath.Join(repoRoot, "testdata/fixtures/workspace")

	snapshot, err := New().buildAnalysisSnapshot(context.Background(), repoRoot, workspaceRoot, backend.SnapshotOptions{})
	if err != nil {
		t.Fatal(err)
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
	writeWorkspaceFile(t, root, "src/data/db.ts", `export default async function db(input: string): Promise<number> {
	return input.length;
}

export type DbConfig = {
	endpoint: string;
};

export function helper(config: DbConfig): number {
	return config.endpoint.length;
}
`)
	writeWorkspaceFile(t, root, "src/service/repository.ts", `import db, { helper as runHelper, type DbConfig } from "../data/db";
import * as dbModule from "../data/db";

type LocalConfig = DbConfig;

export class Repository {
	async save(config: DbConfig): Promise<number> {
		await db("sample input");
		return runHelper(config) + (await dbModule.default(config.endpoint));
	}
}

export function mirror(config: LocalConfig): DbConfig {
	return config;
}
`)

	snapshot, err := New().buildAnalysisSnapshot(context.Background(), root, root, backend.SnapshotOptions{})
	if err != nil {
		t.Fatal(err)
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

func TestBuildSnapshotExposesImportMetaAccessesAndIgnoresStrings(t *testing.T) {
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
	writeWorkspaceFile(t, root, "src/strings.ts", `export function literalEnv() {
	return "import.meta.env.API_URL";
}
`)

	snapshot, err := New().buildAnalysisSnapshot(context.Background(), root, root, backend.SnapshotOptions{})
	if err != nil {
		t.Fatal(err)
	}

	var importMetaFound bool
	for _, access := range snapshot.Accesses {
		switch {
		case access.FilePath == "src/env.ts" && access.Root == "import.meta" && access.AccessPath == "import.meta.env" && access.Origin == "special_form":
			importMetaFound = true
			if access.Range.File != "src/env.ts" || access.Range.StartLine != 2 {
				t.Fatalf("unexpected import.meta access range %+v", access.Range)
			}
		case access.FilePath == "src/strings.ts":
			t.Fatalf("expected string literal to be ignored, got %+v", access)
		}
	}
	if !importMetaFound {
		t.Fatalf("expected import.meta access in snapshot, got %+v", snapshot.Accesses)
	}
}

func TestBuiltinAmbientSources(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	writeWorkspaceFile(t, root, "tsconfig.json", `{"compilerOptions":{"strict":true,"module":"esnext","moduleResolution":"bundler","target":"es2022","lib":["es2022","dom"]}}`)
	writeWorkspaceFile(t, root, "src/a.ts", `import { randomUUID } from "node:crypto";
import * as nodeCrypto from "node:crypto";
import { setTimeout as later } from "node:timers";
import { cwd } from "node:process";
export function a() {
	Math.random();
	crypto.randomUUID();
	randomUUID();
	nodeCrypto.randomBytes(4);
	crypto.getRandomValues(new Uint8Array(2));
	fetch("x");
	globalThis.fetch("y");
	setTimeout(() => {}, 1);
	later(() => {}, 1);
	setInterval(() => {}, 2);
	process.cwd();
	cwd();
	process.argv;
	const record = { fetch: 1, random: 2 };
	return [record.fetch, Math.max(1)];
}
`)

	snapshot, err := New().buildAnalysisSnapshot(context.Background(), root, root, backend.SnapshotOptions{})
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, use := range snapshot.AmbientUses {
		got = append(got, use.Source+"."+use.Capability)
	}
	slices.Sort(got)
	want := []string{
		"network.fetch", "network.fetch",
		"process.argv", "process.cwd", "process.cwd",
		"random.bytes", "random.bytes", "random.number", "random.uuid", "random.uuid",
		"timer.schedule", "timer.schedule", "timer.schedule",
	}
	if !slices.Equal(got, want) {
		t.Fatalf("got %q, want %q", got, want)
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
