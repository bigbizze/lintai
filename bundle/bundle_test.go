package bundle

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
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
		for _, input := range artifact.Manifest.Inputs {
			if input.AbsolutePath == physical {
				t.Fatalf("unrelated data was captured as a bundle input: %s", file)
			}
		}
		if err := os.WriteFile(file, []byte(`{"value":"after"}`), 0o600); err != nil {
			t.Fatal(err)
		}
		if !artifact.Manifest.Unchanged() {
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

func TestInputManifestValidation(t *testing.T) {
	file := filepath.Join(t.TempDir(), "rule.ts")
	if err := os.WriteFile(file, []byte("rule"), 0o600); err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256([]byte("rule"))
	valid := InputManifest{
		Format:      InputManifestFormat,
		Inputs:      []Input{{Path: "rule.ts", AbsolutePath: file, SHA256: hex.EncodeToString(digest[:])}},
		Probes:      []ConfigProbe{},
		Resolutions: []string{},
		Tool:        ToolIdentity{Esbuild: "test", ConfigResolver: "test", Script: hex.EncodeToString(digest[:])},
	}
	if err := valid.validate(); err != nil || !valid.Unchanged() {
		t.Fatalf("valid manifest: %v", err)
	}
	for _, format := range []string{"", "lintai.bundle-inputs/0", "lintai.bundle-inputs/2"} {
		invalid := valid
		invalid.Format = format
		if err := invalid.validate(); err == nil || invalid.Unchanged() {
			t.Fatalf("accepted unsupported format %q", format)
		}
	}
	collision := valid
	collision.Inputs = append(append([]Input(nil), valid.Inputs...), Input{
		Path: "rule.ts", AbsolutePath: filepath.Join(filepath.Dir(file), "other.ts"), SHA256: hex.EncodeToString(digest[:]),
	})
	if err := collision.validate(); err == nil {
		t.Fatal("accepted different physical inputs with the same logical path")
	}
	var incomplete ConfigProbe
	if err := json.Unmarshal([]byte(`{"path":"/missing"}`), &incomplete); err == nil {
		t.Fatal("accepted a probe without its required observations")
	}
	if err := os.Remove(file); err != nil {
		t.Fatal(err)
	}
	if valid.Unchanged() {
		t.Fatal("deleted input was accepted as unchanged")
	}
}
