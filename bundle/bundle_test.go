package bundle

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"github.com/bigbizze/lintai/observe"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/bigbizze/lintai/runtime"
)

func TestBuildAllAndPrepareAllFixtureRules(t *testing.T) {
	t.Parallel()

	repoRoot := repoRootForBundleTest(t)
	assetRoot := filepath.Join(repoRoot, "packages", "client")
	workspaceRoot := filepath.Join(repoRoot, "testdata/fixtures/workspace")
	rulePaths := []string{
		filepath.Join(repoRoot, "testdata/fixtures/rules/pure-no-effects.ts"),
		filepath.Join(repoRoot, "testdata/fixtures/rules/pure-no-service-imports.ts"),
	}

	artifacts, buildFailures, err := BuildAll(context.Background(), assetRoot, repoRoot, repoRoot, t.TempDir(), rulePaths)
	if err != nil {
		t.Fatal(err)
	}
	if len(buildFailures) != 0 {
		t.Fatalf("expected no build failures, got %#v", buildFailures)
	}
	if len(artifacts) != len(rulePaths) {
		t.Fatalf("expected %d build results, got %d", len(rulePaths), len(artifacts))
	}

	prepared, prepareFailures, err := PrepareAll(context.Background(), assetRoot, repoRoot, workspaceRoot, artifacts, map[string]any{}, PrepareOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if len(prepareFailures) != 0 {
		t.Fatalf("expected no prepare failures, got %#v", prepareFailures)
	}
	if len(prepared) != len(rulePaths) {
		t.Fatalf("expected %d prepare results, got %d", len(rulePaths), len(prepared))
	}

	effects := prepared[rulePaths[0]]
	if effects.RuleID != "arch.pure-no-effects" {
		t.Fatalf("unexpected rule id %q", effects.RuleID)
	}
	effectsEnv, ok := effects.Env.(map[string]any)
	if !ok {
		t.Fatalf("expected env object, got %#v", effects.Env)
	}
	if effectsEnv["pureDir"] != "src/pure" {
		t.Fatalf("expected pureDir default, got %#v", effectsEnv["pureDir"])
	}

	imports := prepared[rulePaths[1]]
	if imports.RuleID != "arch.pure-no-service-imports" {
		t.Fatalf("unexpected rule id %q", imports.RuleID)
	}
	importsEnv, ok := imports.Env.(map[string]any)
	if !ok {
		t.Fatalf("expected env object, got %#v", imports.Env)
	}
	if importsEnv["serviceDir"] != "src/services" {
		t.Fatalf("expected serviceDir default, got %#v", importsEnv["serviceDir"])
	}

	// The repository-root launcher and an output directory inside the workspace
	// must use the same captured helper view without treating output as input.
	workspace := t.TempDir()
	unused := filepath.Join(workspace, "unrelated", "data.json")
	if err := os.MkdirAll(filepath.Dir(unused), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(unused, []byte(`{"value":"before"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	rule := filepath.Join(workspace, "rule.ts")
	if err := os.WriteFile(rule, []byte(`import { dl, rule } from "@lintai/sdk";
export default rule("fixture.root-launcher").version(1).program(dl`+"`"+`Decl violation(Entity).`+"`"+`).report("violation").message(() => "ok");`), 0o600); err != nil {
		t.Fatal(err)
	}
	rootArtifacts, rootFailures, err := BuildAll(context.Background(), repoRoot, repoRoot, workspace, filepath.Join(workspace, "out"), []string{rule})
	if err != nil || rootFailures[rule] != nil || len(rootArtifacts) != 1 {
		t.Fatalf("repository-root launcher: %v %v", err, rootFailures[rule])
	}
	assertUnrelatedData := func(artifact Artifacts, file string) {
		t.Helper()
		physical, err := filepath.EvalSymlinks(file)
		if err != nil {
			t.Fatal(err)
		}
		for _, input := range artifact.Observations.Records() {
			if input.Kind == observe.Content && input.Path == physical {
				t.Fatalf("unrelated data was captured as a bundle input: %s", file)
			}
		}
		if err := os.WriteFile(file, []byte(`{"value":"after"}`), 0o600); err != nil {
			t.Fatal(err)
		}
		if !bundleUnchanged(artifact.Observations) {
			t.Fatalf("editing unrelated data invalidated the bundle: %s", file)
		}
	}
	assertUnrelatedData(rootArtifacts[rule], unused)
	secondRule := filepath.Join(workspace, "second.ts")
	if err := os.WriteFile(secondRule, []byte(`import { dl, rule } from "@lintai/sdk";
export default rule("fixture.second").version(1).program(dl`+"`"+`Decl violation(Entity).`+"`"+`).report("violation").message(() => "ok");`), 0o600); err != nil {
		t.Fatal(err)
	}
	multiple, multipleFailures, err := BuildAll(context.Background(), repoRoot, repoRoot, workspace, filepath.Join(workspace, "multiple-out"), []string{rule, secondRule})
	if err != nil || len(multipleFailures) != 0 || len(multiple) != 2 {
		t.Fatalf("multiple rules with output inside workspace: artifacts=%d failures=%v error=%v", len(multiple), multipleFailures, err)
	}

	// A rule can live outside the workspace in a much larger package tree.
	external := t.TempDir()
	externalUnused := filepath.Join(external, "unrelated", "data.json")
	if err := os.MkdirAll(filepath.Dir(externalUnused), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(external, "package.json"), []byte(`{}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(externalUnused, []byte(`{"value":"before"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	source, err := os.ReadFile(rule)
	if err != nil {
		t.Fatal(err)
	}
	externalRule := filepath.Join(external, "rule.ts")
	if err := os.WriteFile(externalRule, source, 0o600); err != nil {
		t.Fatal(err)
	}
	externalArtifacts, externalFailures, err := BuildAll(context.Background(), repoRoot, repoRoot, workspace, filepath.Join(workspace, "external-out"), []string{externalRule})
	if err != nil || externalFailures[externalRule] != nil || len(externalArtifacts) != 1 {
		t.Fatalf("external rule: %v %v", err, externalFailures[externalRule])
	}
	assertUnrelatedData(externalArtifacts[externalRule], externalUnused)

	t.Run("unrelated JSON in imported directory", func(t *testing.T) {
		workspace := t.TempDir()
		helpers := filepath.Join(workspace, "helpers")
		if err := os.MkdirAll(helpers, 0o755); err != nil {
			t.Fatal(err)
		}
		for name, content := range map[string]string{
			"index.ts":     `import data from "./data.json"; export const message = data.message;`,
			"data.json":    `{"message":"consumed"}`,
			"notes.json":   `{"value":"` + strings.Repeat("../", 64) + `"}`,
			"package.json": `{"main":"index.ts"}`,
		} {
			if err := os.WriteFile(filepath.Join(helpers, name), []byte(content), 0o600); err != nil {
				t.Fatal(err)
			}
		}
		configPackage := filepath.Join(workspace, "node_modules", "fixture-config")
		if err := os.MkdirAll(configPackage, 0o755); err != nil {
			t.Fatal(err)
		}
		for name, content := range map[string]string{
			"index.ts":     `export const suffix = "";`,
			"package.json": `{"main":"index.ts","tsconfig":"options.json"}`,
			"options.json": `{}`,
			"notes.json":   `{"value":"before"}`,
		} {
			if err := os.WriteFile(filepath.Join(configPackage, name), []byte(content), 0o600); err != nil {
				t.Fatal(err)
			}
		}
		if err := os.WriteFile(filepath.Join(workspace, "tsconfig.json"), []byte(`{"extends":"fixture-config"}`), 0o600); err != nil {
			t.Fatal(err)
		}
		rule := filepath.Join(workspace, "rule.ts")
		if err := os.WriteFile(rule, []byte(`import { dl, rule } from "@lintai/sdk";
import { message } from "./helpers";
import { suffix } from "fixture-config";
export default rule("fixture.directory-json").version(1).program(dl`+"`"+`Decl violation(Entity).`+"`"+`).report("violation").message(() => message + suffix);`), 0o600); err != nil {
			t.Fatal(err)
		}
		build := func() Artifacts {
			t.Helper()
			artifacts, failures, err := BuildAll(context.Background(), assetRoot, repoRoot, workspace, t.TempDir(), []string{rule})
			if err != nil || failures[rule] != nil || len(artifacts) != 1 {
				t.Fatalf("build: %v %v", err, failures[rule])
			}
			return artifacts[rule]
		}
		before := build()
		for _, notes := range []string{filepath.Join(helpers, "notes.json"), filepath.Join(configPackage, "notes.json")} {
			physicalNotes, err := filepath.EvalSymlinks(notes)
			if err != nil {
				t.Fatal(err)
			}
			for _, input := range before.Observations.Records() {
				if input.Kind == observe.Content && input.Path == physicalNotes {
					t.Errorf("unrelated JSON was captured as an input: %s", notes)
				}
			}
			if err := os.WriteFile(notes, []byte(`{"value":"after"}`), 0o600); err != nil {
				t.Fatal(err)
			}
		}
		if !bundleUnchanged(before.Observations) {
			t.Error("editing unrelated JSON invalidated the manifest")
		}
		after := build()
		if before.Hash != after.Hash {
			t.Errorf("editing unrelated JSON changed artifact identity: %s != %s", before.Hash, after.Hash)
		}
		if err := os.Mkdir(filepath.Join(helpers, "unrelated"), 0o755); err != nil {
			t.Fatal(err)
		}
		if bundleUnchanged(after.Observations) {
			t.Error("a changed directory listing did not invalidate freshness")
		}
		afterDirectory := build()
		if after.Hash != afterDirectory.Hash {
			t.Errorf("unrelated directory changed artifact identity: %s != %s", after.Hash, afterDirectory.Hash)
		}
		if err := os.WriteFile(filepath.Join(helpers, "data.json"), []byte(`{"message":"changed"}`), 0o600); err != nil {
			t.Fatal(err)
		}
		if bundleUnchanged(afterDirectory.Observations) {
			t.Error("editing imported JSON did not invalidate the manifest")
		}
		afterData := build()
		if err := os.WriteFile(filepath.Join(configPackage, "options.json"), []byte(`{"compilerOptions":{"useDefineForClassFields":true}}`), 0o600); err != nil {
			t.Fatal(err)
		}
		if bundleUnchanged(afterData.Observations) {
			t.Error("editing the extended config did not invalidate the manifest")
		}
	})
	t.Run("deduplicate colliding extends boundary markers", func(t *testing.T) {
		workspace := t.TempDir()
		write := func(relative, content string) string {
			t.Helper()
			file := filepath.Join(workspace, relative)
			if err := os.MkdirAll(filepath.Dir(file), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(file, []byte(content), 0o600); err != nil {
				t.Fatal(err)
			}
			return file
		}
		write("node_modules/fixture-config/package.json", `{}`)
		write("node_modules/fixture-config/tsconfig.json", `{"compilerOptions":{"strict":true}}`)
		write("tsconfig.json", `{"extends":["fixture-config","fixture-config/tsconfig"]}`)
		rule := write("rule.ts", `import { dl, rule } from "@lintai/sdk";
export default rule("fixture.config-marker-dedup").version(1).program(dl`+"`"+`Decl violation(Entity).`+"`"+`).report("violation").message(() => "ok");`)
		artifacts, failures, err := BuildAll(context.Background(), assetRoot, repoRoot, workspace, t.TempDir(), []string{rule})
		if err != nil || failures[rule] != nil || len(artifacts) != 1 {
			t.Fatalf("valid config extends aliases should build once: artifacts=%d failures=%v error=%v", len(artifacts), failures[rule], err)
		}
	})
}

func TestAbsoluteRequireResolveSetup(t *testing.T) {
	repoRoot := repoRootForBundleTest(t)
	assetRoot := filepath.Join(repoRoot, "packages", "client")
	workspace := t.TempDir()
	target := filepath.Join(workspace, "target.js")
	if err := os.WriteFile(target, []byte(`module.exports = {};`), 0o600); err != nil {
		t.Fatal(err)
	}
	target, err := filepath.EvalSymlinks(target)
	if err != nil {
		t.Fatal(err)
	}
	literal, err := json.Marshal(target)
	if err != nil {
		t.Fatal(err)
	}
	rule := filepath.Join(workspace, "rule.ts")
	source := `import { dl, rule } from "@lintai/sdk";
export default rule("fixture.resolve").version(1).setup(() => ({resolved: require.resolve(` + string(literal) + `)}))
.program(dl` + "`" + `Decl violation(Entity).` + "`" + `).report("violation").message(() => "ok");`
	if err := os.WriteFile(rule, []byte(source), 0o600); err != nil {
		t.Fatal(err)
	}
	build := func() map[string]Artifacts {
		t.Helper()
		artifacts, failures, err := BuildAll(context.Background(), assetRoot, repoRoot, workspace, t.TempDir(), []string{rule})
		if err != nil || failures[rule] != nil {
			t.Fatalf("build: %v %v", err, failures[rule])
		}
		return artifacts
	}
	first, second := build(), build()
	if first[rule].Hash != second[rule].Hash {
		t.Errorf("unchanged absolute require.resolve changed identity: %s != %s", first[rule].Hash, second[rule].Hash)
	}
	prepared, failures, err := PrepareAll(context.Background(), assetRoot, repoRoot, workspace, first, nil, PrepareOptions{})
	if err != nil || len(failures) != 0 {
		t.Fatalf("unrestricted setup: %v %v", err, failures)
	}
	setup, ok := prepared[rule].Setup.(map[string]any)
	if !ok || setup["resolved"] != target {
		t.Fatalf("require.resolve returned %+v, want %s", prepared[rule].Setup, target)
	}
}

func TestBuildAllPreservesLocallyBoundRequire(t *testing.T) {
	t.Parallel()

	repoRoot := repoRootForBundleTest(t)
	assetRoot := filepath.Join(repoRoot, "packages", "client")
	workspace := t.TempDir()
	modulePath := filepath.Join(workspace, "module.cjs")
	if err := os.WriteFile(modulePath, []byte(`module.exports = { value: "module" };`), 0o600); err != nil {
		t.Fatal(err)
	}
	absoluteModule, err := json.Marshal(modulePath)
	if err != nil {
		t.Fatal(err)
	}
	rulePath := filepath.Join(workspace, "rule.ts")
	source := `import { dl, rule } from "@lintai/sdk";
declare const require: (value: string) => any;
const imported = require(` + string(absoluteModule) + `).value;
function localCall() {
	function require(value: string) { return value; }
	return require("/api");
}
function localResolve() {
	const require = { resolve: (value: string) => value };
	return require.resolve("/resolve");
}
export default rule("fixture.local-require").version(1).program(dl` + "`" + `Decl violation(Entity).` + "`" + `)
	.report("violation").message(() => [localCall(), localResolve(), imported].join("|"));`
	if err := os.WriteFile(rulePath, []byte(source), 0o600); err != nil {
		t.Fatal(err)
	}
	for range 2 {
		artifacts, failures, err := BuildAll(context.Background(), assetRoot, repoRoot, workspace, t.TempDir(), []string{rulePath})
		if err != nil || failures[rulePath] != nil || len(artifacts) != 1 {
			t.Fatalf("build: %v %v", err, failures[rulePath])
		}
		code, err := os.ReadFile(artifacts[rulePath].PureBundlePath)
		if err != nil {
			t.Fatal(err)
		}
		loaded, err := runtime.LoadPureBundle(string(code))
		if err != nil {
			t.Fatal(err)
		}
		message, err := loaded.Message(nil, "violation")
		if err != nil || message != "/api|/resolve|module" {
			t.Errorf("locally bound require changed the diagnostic: %q, %v", message, err)
		}
	}
}

func TestBuildAllReportsPerRuleErrors(t *testing.T) {
	t.Parallel()

	repoRoot := repoRootForBundleTest(t)
	assetRoot := filepath.Join(repoRoot, "packages", "client")
	validRule := filepath.Join(repoRoot, "testdata/fixtures/rules/pure-no-effects.ts")
	brokenRule := writeRuleFile(t, "broken-build.ts", "export default ;\n")

	artifacts, buildFailures, err := BuildAll(context.Background(), assetRoot, repoRoot, repoRoot, t.TempDir(), []string{validRule, brokenRule})
	if err != nil {
		t.Fatal(err)
	}
	if len(artifacts) != 1 {
		t.Fatalf("expected 1 successful artifact, got %d", len(artifacts))
	}
	if _, ok := artifacts[validRule]; !ok {
		t.Fatalf("expected valid rule artifact for %q", validRule)
	}
	buildErr, ok := buildFailures[brokenRule]
	if !ok {
		t.Fatalf("expected build failure for %q", brokenRule)
	}
	if !strings.Contains(buildErr.Error(), "Unexpected") {
		t.Fatalf("expected parse failure, got %q", buildErr.Error())
	}
}

func TestPrepareAllReportsPerRuleErrors(t *testing.T) {
	t.Parallel()

	repoRoot := repoRootForBundleTest(t)
	assetRoot := filepath.Join(repoRoot, "packages", "client")
	workspaceRoot := filepath.Join(repoRoot, "testdata/fixtures/workspace")
	validRule := filepath.Join(repoRoot, "testdata/fixtures/rules/pure-no-effects.ts")
	brokenRule := writeRuleFile(t, "broken-prepare.ts", `
import { dl, rule } from "@lintai/sdk";

export default rule("arch.broken-setup")
	.version(7)
	.setup(() => {
		throw new Error("setup boom");
	})
	.program(dl`+"`Decl v(F). v(F) :- ts.module(F).`"+`)
	.report("v")
	.message(() => "broken");
`)

	artifacts, buildFailures, err := BuildAll(context.Background(), assetRoot, repoRoot, repoRoot, t.TempDir(), []string{validRule, brokenRule})
	if err != nil {
		t.Fatal(err)
	}
	if len(buildFailures) != 0 {
		t.Fatalf("expected no build failures, got %#v", buildFailures)
	}

	prepared, prepareFailures, err := PrepareAll(context.Background(), assetRoot, repoRoot, workspaceRoot, artifacts, map[string]any{}, PrepareOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if len(prepared) != 1 {
		t.Fatalf("expected 1 prepared rule, got %d", len(prepared))
	}
	prepareErr, ok := prepareFailures[brokenRule]
	if !ok {
		t.Fatalf("expected prepare failure for %q", brokenRule)
	}
	if prepareErr.RuleID != "arch.broken-setup" {
		t.Fatalf("unexpected prepare rule id %q", prepareErr.RuleID)
	}
	if prepareErr.RuleVersion != 7 {
		t.Fatalf("unexpected prepare rule version %d", prepareErr.RuleVersion)
	}
	if !strings.Contains(prepareErr.Message, "setup boom") {
		t.Fatalf("expected setup error, got %q", prepareErr.Message)
	}
}

func TestPrepareAllAllowsRequireInsideSetup(t *testing.T) {
	t.Parallel()

	repoRoot := repoRootForBundleTest(t)
	assetRoot := filepath.Join(repoRoot, "packages", "client")
	workspaceRoot := filepath.Join(repoRoot, "testdata/fixtures/workspace")
	rulePath := writeRuleFile(t, "require-in-setup.ts", `
import { dl, rule } from "@lintai/sdk";

	export default rule("arch.require-in-setup")
		.version(1)
		.setup(({ workspaceRoot }) => {
			const fs = require("node:fs");
			const path = require("node:path");
			const target = path.join(workspaceRoot, "src", "pure", "helper.ts");
			return { exists: fs.existsSync(target) };
		})
	.program(dl`+"`Decl v(F). v(F) :- ts.module(F).`"+`)
	.report("v")
	.message(() => "broken");
`)

	artifacts, buildFailures, err := BuildAll(context.Background(), assetRoot, repoRoot, repoRoot, t.TempDir(), []string{rulePath})
	if err != nil {
		t.Fatal(err)
	}
	if len(buildFailures) != 0 {
		t.Fatalf("expected no build failures, got %#v", buildFailures)
	}

	prepared, prepareFailures, err := PrepareAll(context.Background(), assetRoot, repoRoot, workspaceRoot, artifacts, map[string]any{}, PrepareOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if len(prepareFailures) != 0 {
		t.Fatalf("expected no prepare failures, got %#v", prepareFailures)
	}
	result := prepared[rulePath]
	setup, ok := result.Setup.(map[string]any)
	if !ok {
		t.Fatalf("expected setup object, got %#v", result.Setup)
	}
	if setup["exists"] != true {
		t.Fatalf("expected setup to confirm fixture path exists, got %#v", setup)
	}
}

func TestBuildAllDoesNotExplodeOnTopLevelNodeBuiltinImports(t *testing.T) {
	t.Parallel()

	repoRoot := repoRootForBundleTest(t)
	assetRoot := filepath.Join(repoRoot, "packages", "client")
	rulePath := writeRuleFile(t, "top-level-node-import.ts", `
import fs from "node:fs";
import { dl, rule } from "@lintai/sdk";

export default rule("arch.top-level-node-import")
	.version(1)
	.setup(() => ({ cwdExists: typeof fs.existsSync === "function" }))
	.program(dl`+"`Decl v(F). v(F) :- ts.module(F).`"+`)
	.report("v")
	.message(() => "broken");
`)

	artifacts, buildFailures, err := BuildAll(context.Background(), assetRoot, repoRoot, repoRoot, t.TempDir(), []string{rulePath})
	if err != nil {
		t.Fatal(err)
	}
	if len(buildFailures) != 0 {
		t.Fatalf("expected no build failures, got %#v", buildFailures)
	}
	if _, ok := artifacts[rulePath]; !ok {
		t.Fatalf("expected build artifact for %q", rulePath)
	}
}

func repoRootForBundleTest(t *testing.T) string {
	t.Helper()

	repoRoot, err := filepath.Abs("..")
	if err != nil {
		t.Fatal(err)
	}
	return repoRoot
}

func writeRuleFile(t *testing.T, name, contents string) string {
	t.Helper()

	path := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(path, []byte(contents), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestBundleEvidence(t *testing.T) {
	file := filepath.Join(t.TempDir(), "rule.ts")
	helper := filepath.Join(filepath.Dir(file), "helper.mjs")
	digest := sha256.Sum256([]byte("rule"))
	value := observe.Value(fmt.Sprintf("%x", digest))
	set, err := observe.New([]observe.Record{{Kind: observe.Content, Path: file, Value: value}, {Kind: observe.Content, Path: helper, Value: value}})
	if err != nil || validateBundleEvidence(set, file, helper) != nil {
		t.Fatalf("valid evidence: %v", err)
	}
	if validateBundleEvidence(observe.Set{}, file, helper) == nil {
		t.Fatal("empty bundle evidence accepted")
	}
	staged, err := observe.New([]observe.Record{{Kind: observe.Content, Path: file, Value: value, Stamp: &observe.Stamp{Size: "4", MtimeNs: "1", ObservedAtNs: "3"}}, {Kind: observe.Content, Path: helper, Value: value}})
	if err != nil || validateBundleEvidence(staged, file, helper) == nil {
		t.Fatal("staged content stamp accepted")
	}
}
