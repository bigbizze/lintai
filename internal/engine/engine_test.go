package engine

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/bigbizze/lintai/internal/backend/typescript"
	"github.com/bigbizze/lintai/internal/diagnostics"
)

func TestEngineRunsFixtureRules(t *testing.T) {
	repoRoot, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	runner := New(typescript.New())
	items, err := runner.Run(context.Background(), Options{
		RepoRoot:      repoRoot,
		AssetRoot:     filepath.Join(repoRoot, "packages", "eslint-plugin"),
		WorkspaceRoot: filepath.Join(repoRoot, "testdata/fixtures/workspace"),
		RuleGlobs:     []string{filepath.Join(repoRoot, "testdata/fixtures/rules/*.ts")},
		Env:           map[string]any{},
		Severity:      diagnostics.SeverityError,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(items) < 3 {
		t.Fatalf("expected at least 3 diagnostics, got %d", len(items))
	}
}

func TestEngineContinuesWhenOneRuleFailsDuringSetup(t *testing.T) {
	repoRoot, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	goodRule := filepath.Join(repoRoot, "testdata/fixtures/rules/pure-no-effects.ts")
	brokenRule := filepath.Join(t.TempDir(), "broken-setup.ts")
	if err := os.WriteFile(brokenRule, []byte(`
import { functions, rule } from "@lintai/sdk";

export default rule("arch.broken-setup")
	.version(3)
	.setup(() => {
		throw new Error("setup exploded");
	})
	.assert(() => functions().in("src/**").isEmpty())
	.message(() => "broken");
`), 0o644); err != nil {
		t.Fatal(err)
	}

	runner := New(typescript.New())
	items, err := runner.Run(context.Background(), Options{
		RepoRoot:      repoRoot,
		AssetRoot:     filepath.Join(repoRoot, "packages", "eslint-plugin"),
		WorkspaceRoot: filepath.Join(repoRoot, "testdata/fixtures/workspace"),
		RuleGlobs:     []string{goodRule, brokenRule},
		Env:           map[string]any{},
		Severity:      diagnostics.SeverityError,
	})
	if err != nil {
		t.Fatal(err)
	}

	foundArchitectureViolation := false
	foundSetupFailure := false
	for _, item := range items {
		if item.DiagnosticKind == diagnostics.KindArchitectureViolation {
			foundArchitectureViolation = true
		}
		if item.DiagnosticKind == diagnostics.KindRuleExecutionError && item.RuleID == "arch.broken-setup" && item.Phase == "setup" {
			foundSetupFailure = true
		}
	}
	if !foundArchitectureViolation {
		t.Fatalf("expected unaffected rules to still report violations, got %+v", items)
	}
	if !foundSetupFailure {
		t.Fatalf("expected setup failure diagnostic, got %+v", items)
	}
}

func TestEngineResolvesRelativeRuleGlobsFromWorkspaceRoot(t *testing.T) {
	repoRoot, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}

	workspaceRoot := t.TempDir()
	writeTestFile(t, workspaceRoot, "tsconfig.json", `{
  "compilerOptions": {
    "target": "es2022",
    "module": "nodenext",
    "moduleResolution": "nodenext",
    "noEmit": true
  },
  "include": ["src/**/*.ts"]
}
`)
	writeTestFile(t, workspaceRoot, "src/pure/bad.ts", `export async function bad(): Promise<number> {
	return 1;
}
`)
	writeTestFile(t, workspaceRoot, "lintai-rules/api-async-return.ts", `
import { functions, rule } from "@lintai/sdk";

export default rule("arch.api-async-return")
	.version(1)
	.assert(() =>
		functions()
			.in("src/pure/**")
			.where((fn) => fn.isExported && fn.isAsync && fn.returnTypeText !== "Promise<string>")
			.isEmpty(),
	)
	.message((fn) => "API async function must return Promise<string>: " + fn.name);
`)

	runner := New(typescript.New())
	items, err := runner.Run(context.Background(), Options{
		RepoRoot:      repoRoot,
		AssetRoot:     filepath.Join(repoRoot, "packages", "eslint-plugin"),
		WorkspaceRoot: workspaceRoot,
		RuleGlobs:     []string{"lintai-rules/**/*.ts"},
		Env:           map[string]any{},
		Severity:      diagnostics.SeverityError,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 1 {
		t.Fatalf("expected exactly one diagnostic, got %+v", items)
	}
	if items[0].RuleID != "arch.api-async-return" {
		t.Fatalf("unexpected diagnostic %+v", items[0])
	}
}

func TestEngineSupportsExpandedMetadataQueries(t *testing.T) {
	repoRoot, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}

	workspaceRoot := t.TempDir()
	writeTestFile(t, workspaceRoot, "tsconfig.json", `{
  "compilerOptions": {
    "target": "es2022",
    "module": "nodenext",
    "moduleResolution": "nodenext",
    "noEmit": true
  },
  "include": ["src/**/*.ts"]
}
`)
	writeTestFile(t, workspaceRoot, "src/data/db.ts", `export default async function db(query: string): Promise<number> {
	return query.length;
}

export type DbConfig = {
	dsn: string;
};
`)
	writeTestFile(t, workspaceRoot, "src/services/types.ts", `export interface ServiceConfig {
	id: string;
}
`)
	writeTestFile(t, workspaceRoot, "src/services/service.ts", `import db from "../data/db";
import type { DbConfig } from "../data/db";

export class Service {
	async save(config: DbConfig): Promise<number> {
		return db(config.dsn);
	}
}
`)
	writeTestFile(t, workspaceRoot, "src/pure/bad-import.ts", `import db from "../data/db";

export async function load(): Promise<number> {
	return db("x");
}
`)
	writeTestFile(t, workspaceRoot, "src/pure/good-import.ts", `import type { DbConfig } from "../data/db";

export function useConfig(config: DbConfig): string {
	return config.dsn;
}
`)
	writeTestFile(t, workspaceRoot, "src/pure/bad-type.ts", `import type { ServiceConfig } from "../services/types";

export function accept(config: ServiceConfig): string {
	return config.id;
}
`)
	writeTestFile(t, workspaceRoot, "src/pure/good-type.ts", `import type { DbConfig } from "../data/db";

export type LocalConfig = DbConfig;
`)
	writeTestFile(t, workspaceRoot, "src/api/bad.ts", `export async function bad(): Promise<number> {
	return 1;
}
`)
	writeTestFile(t, workspaceRoot, "src/api/good.ts", `export async function good(): Promise<string> {
	return "ok";
}
`)

	ruleRoot := t.TempDir()
	rulePaths := []string{
		writeTestFile(t, ruleRoot, "service-methods-no-db.ts", `
import { functions, rule } from "@lintai/sdk";

export default rule("arch.service-methods-no-db")
	.version(1)
	.assert(() =>
		functions()
			.where((fn) => fn.containerName === "Service")
			.calling(functions().where((fn) => fn.name === "db"))
			.isEmpty(),
	)
	.message((fn) => "Service method must not call db: " + fn.name);
`),
		writeTestFile(t, ruleRoot, "no-pure-db-import.ts", `
import { imports, rule } from "@lintai/sdk";

export default rule("arch.no-pure-db-import")
	.version(1)
	.assert(() =>
		imports()
			.from("src/pure/**")
			.where((edge) => edge.importedSymbols.some((symbol) => symbol.name === "db" && !symbol.isTypeOnly))
			.isEmpty(),
	)
	.message((edge) => "Pure module must not import db from " + edge.toPath);
`),
		writeTestFile(t, ruleRoot, "no-service-db-call-edge.ts", `
import { calls, rule } from "@lintai/sdk";

export default rule("arch.no-service-db-call-edge")
	.version(1)
	.assert(() =>
		calls()
			.where((edge) => edge.fromPath === "src/services/service.ts" && edge.toName === "db")
			.isEmpty(),
	)
	.message((edge) => "Service call to " + edge.toName + " is forbidden");
`),
		writeTestFile(t, ruleRoot, "api-async-return.ts", `
import { functions, rule } from "@lintai/sdk";

export default rule("arch.api-async-return")
	.version(1)
	.assert(() =>
		functions()
			.in("src/api/**")
			.where((fn) => fn.isExported && fn.isAsync && fn.returnTypeText !== "Promise<string>")
			.isEmpty(),
	)
	.message((fn) => "API async function must return Promise<string>: " + fn.name);
`),
		writeTestFile(t, ruleRoot, "no-pure-service-types.ts", `
import { rule, typeRefs } from "@lintai/sdk";

export default rule("arch.no-pure-service-types")
	.version(1)
	.assert(() =>
		typeRefs()
			.in("src/pure/**")
			.where((ref) => ref.targetPath.startsWith("src/services/"))
			.isEmpty(),
	)
	.message((ref) => "Pure module must not reference service type " + ref.name);
`),
		writeTestFile(t, ruleRoot, "no-direct-env-access.ts", `
import { accesses, rule } from "@lintai/sdk";

export default rule("arch.no-direct-env-access")
	.version(1)
	.assert(() =>
		accesses()
			.in("src/pure/**")
			.where((access) => access.accessPath === "import.meta.env")
			.isEmpty(),
	)
	.message((access) => "Pure module must not read " + access.accessPath);
`),
	}
	writeTestFile(t, workspaceRoot, "src/pure/bad-env.ts", `export function readEnv() {
	return import.meta.env.API_URL;
}
`)
	writeTestFile(t, workspaceRoot, "src/pure/good-env.ts", `export function literalEnv() {
	return "import.meta.env.API_URL";
}
`)

	runner := New(typescript.New())
	items, err := runner.Run(context.Background(), Options{
		RepoRoot:      repoRoot,
		AssetRoot:     filepath.Join(repoRoot, "packages", "eslint-plugin"),
		WorkspaceRoot: workspaceRoot,
		RuleGlobs:     rulePaths,
		Env:           map[string]any{},
		Severity:      diagnostics.SeverityError,
	})
	if err != nil {
		t.Fatal(err)
	}

	expected := map[string]string{
		"arch.service-methods-no-db":   "src/services/service.ts",
		"arch.no-pure-db-import":       "src/pure/bad-import.ts",
		"arch.no-service-db-call-edge": "src/services/service.ts",
		"arch.api-async-return":        "src/api/bad.ts",
		"arch.no-pure-service-types":   "src/pure/bad-type.ts",
		"arch.no-direct-env-access":    "src/pure/bad-env.ts",
	}
	if len(items) != len(expected) {
		t.Fatalf("expected %d diagnostics, got %d: %+v", len(expected), len(items), items)
	}

	seen := make(map[string]string, len(items))
	for _, item := range items {
		seen[item.RuleID] = item.SourceLocation.File
		if item.DiagnosticKind != diagnostics.KindArchitectureViolation {
			t.Fatalf("expected architecture violation diagnostics, got %+v", item)
		}
	}
	for ruleID, wantFile := range expected {
		if seen[ruleID] != wantFile {
			t.Fatalf("expected %s to report on %s, got %q (all %+v)", ruleID, wantFile, seen[ruleID], seen)
		}
	}

	cleanFiles := map[string]struct{}{
		"src/pure/good-import.ts": {},
		"src/pure/good-type.ts":   {},
		"src/api/good.ts":         {},
		"src/pure/good-env.ts":    {},
	}
	for _, item := range items {
		if _, ok := cleanFiles[item.SourceLocation.File]; ok {
			t.Fatalf("expected no diagnostics for clean file %s, got %+v", item.SourceLocation.File, item)
		}
	}
}

func writeTestFile(t *testing.T, root, relativePath, contents string) string {
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
