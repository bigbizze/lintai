package engine

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/bigbizze/lintai/bundle"
	"github.com/bigbizze/lintai/runtime"
)

func TestBundleAncestorPackageLink(t *testing.T) {
	repo, err := filepath.Abs("..")
	if err != nil {
		t.Fatal(err)
	}
	parent := t.TempDir()
	workspace := filepath.Join(parent, "workspace")
	if err := os.MkdirAll(workspace, 0755); err != nil {
		t.Fatal(err)
	}
	writeTestFile(t, parent, "store/policy/package.json", `{"main":"index.js"}`)
	writeTestFile(t, parent, "store/policy/index.js", `export const message = "ancestor";`)
	if err := os.MkdirAll(filepath.Join(parent, "node_modules/@fixture"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(parent, "store/policy"), filepath.Join(parent, "node_modules/@fixture/policy")); err != nil {
		t.Fatal(err)
	}
	rule := writeTestFile(t, workspace, "lintai-rules/rule.ts", `import {dl, rule} from "@lintai/sdk";
 import {message} from "@fixture/policy";
 export default rule("fixture.ancestor").version(1).program(dl`+"`"+`Decl violation(Entity).`+"`"+`).report("violation").message(() => message);`)
	artifacts, failures, err := bundle.BuildAll(context.Background(), filepath.Join(repo, "packages/client"), repo, workspace, t.TempDir(), []string{rule})
	if err != nil || failures[rule] != nil || len(artifacts) != 1 {
		t.Fatalf("ancestor package: %v %v", err, failures[rule])
	}

	absoluteImport, err := json.Marshal(filepath.Join(parent, "store/policy/index.js"))
	if err != nil {
		t.Fatal(err)
	}
	writeTestFile(t, workspace, "lintai-rules/rule.ts", `import {dl, rule} from "@lintai/sdk";
 import {message} from `+string(absoluteImport)+`;
 export default rule("fixture.ancestor").version(1).program(dl`+"`"+`Decl violation(Entity).`+"`"+`).report("violation").message(() => message);`)
	artifacts, failures, err = bundle.BuildAll(context.Background(), filepath.Join(repo, "packages/client"), repo, workspace, t.TempDir(), []string{rule})
	if err != nil || failures[rule] != nil || len(artifacts) != 1 {
		t.Fatalf("absolute import: %v %v", err, failures[rule])
	}
	message := func(artifact bundle.Artifacts) string {
		t.Helper()
		code, err := os.ReadFile(artifact.PureBundlePath)
		if err != nil {
			t.Fatal(err)
		}
		loaded, err := runtime.LoadPureBundle(string(code))
		if err != nil {
			t.Fatal(err)
		}
		result, err := loaded.Message(nil, "violation")
		if err != nil {
			t.Fatal(err)
		}
		return result
	}
	if got := message(artifacts[rule]); got != "ancestor" {
		t.Fatalf("absolute import message: %q", got)
	}

	for _, test := range []struct{ name, metadata, specifier string }{
		{"exact", `{"imports":{"#policy":"@fixture/policy"}}`, "#policy"},
		{"wildcard", `{"imports":{"#policy/*":"@fixture/*"}}`, "#policy/policy"},
	} {
		redirectWorkspace := filepath.Join(parent, test.name, "middle/workspace")
		writeTestFile(t, redirectWorkspace, "lintai-rules/package.json", test.metadata)
		specifier, err := json.Marshal(test.specifier)
		if err != nil {
			t.Fatal(err)
		}
		redirectRule := writeTestFile(t, redirectWorkspace, "lintai-rules/rule.ts", `import {dl, rule} from "@lintai/sdk";
 import {message} from `+string(specifier)+`;
 export default rule("fixture.redirect").version(1).program(dl`+"`"+`Decl violation(Entity).`+"`"+`).report("violation").message(() => message);`)
		buildRedirect := func() bundle.Artifacts {
			t.Helper()
			built, failures, err := bundle.BuildAll(context.Background(), filepath.Join(repo, "packages/client"), repo, redirectWorkspace, t.TempDir(), []string{redirectRule})
			if err != nil || failures[redirectRule] != nil || len(built) != 1 {
				t.Fatalf("%s package import redirect: %v %v", test.name, err, failures[redirectRule])
			}
			return built[redirectRule]
		}
		outer := buildRedirect()
		if got := message(outer); got != "ancestor" {
			t.Fatalf("%s outer redirect message: %q", test.name, got)
		}
		writeTestFile(t, parent, test.name+"/middle/node_modules/@fixture/policy/package.json", `{"main":"index.js"}`)
		writeTestFile(t, parent, test.name+"/middle/node_modules/@fixture/policy/index.js", `export const message = "nearer";`)
		if outer.Manifest.Unchanged() {
			t.Fatalf("%s nearer redirected package did not invalidate the manifest", test.name)
		}
		nearer := buildRedirect()
		if got := message(nearer); got != "nearer" || nearer.Hash == outer.Hash {
			t.Fatalf("%s nearer redirect: message=%q hash changed=%v", test.name, got, nearer.Hash != outer.Hash)
		}
	}
}

func TestBundleRelativeRedirectCandidateDuringCapture(t *testing.T) {
	repo, err := filepath.Abs("..")
	if err != nil {
		t.Fatal(err)
	}
	workspace := t.TempDir()
	control := filepath.Join(t.TempDir(), "control")
	if err := os.WriteFile(control, []byte("disarmed"), 0o600); err != nil {
		t.Fatal(err)
	}
	asset := bundleEditAsset(t, repo, control)
	helper := filepath.Join(asset, "scripts", "bundle-rule.mjs")
	helperCode, err := os.ReadFile(helper)
	if err != nil {
		t.Fatal(err)
	}
	anchor := "\t\tconst configInputs = new Map();"
	if strings.Count(string(helperCode), anchor) != 1 {
		t.Fatal("candidate edit anchor is not unique")
	}
	candidate := filepath.Join(workspace, "alternate.ts")
	edit := fmt.Sprintf(`		if ((await fs.readFile(%q, "utf8")).trim() === "armed") {
			await fs.writeFile(%q, 'export const message = "new";');
		}
`, control, candidate)
	if err := os.WriteFile(helper, []byte(strings.Replace(string(helperCode), anchor, edit+anchor, 1)), 0o600); err != nil {
		t.Fatal(err)
	}
	writeTestFile(t, workspace, "package.json", `{"browser":{"./policy.js":"./alternate"}}`)
	writeTestFile(t, workspace, "policy.js", `export const message = "node";`)
	writeTestFile(t, workspace, "alternate.js", `export const message = "old";`)
	rule := writeTestFile(t, workspace, "rule.ts", `import {dl, rule} from "@lintai/sdk";
 import {message} from "./policy.js";
 export default rule("fixture.relative-redirect").version(1).program(dl`+"`"+`Decl violation(Entity).`+"`"+`).report("violation").message(() => message);`)
	build := func() (map[string]bundle.Artifacts, map[string]error) {
		t.Helper()
		artifacts, failures, err := bundle.BuildAll(context.Background(), asset, repo, workspace, t.TempDir(), []string{rule})
		if err != nil {
			t.Fatal(err)
		}
		return artifacts, failures
	}
	stable, failures := build()
	if failures[rule] != nil || len(stable) != 1 {
		t.Fatalf("stable redirect: %v", failures[rule])
	}
	if err := os.WriteFile(control, []byte("armed"), 0o600); err != nil {
		t.Fatal(err)
	}
	changed, failures := build()
	if len(changed) != 0 || failures[rule] == nil || !strings.Contains(failures[rule].Error(), "resolution changed") {
		t.Fatalf("new redirect candidate must reject the discovered view: artifacts=%d failure=%v", len(changed), failures[rule])
	}
	if stable[rule].Manifest.Unchanged() {
		t.Fatal("new redirect candidate did not invalidate the earlier artifact")
	}
	if err := os.WriteFile(control, []byte("disarmed"), 0o600); err != nil {
		t.Fatal(err)
	}
	updated, failures := build()
	if failures[rule] != nil || len(updated) != 1 || updated[rule].Hash == stable[rule].Hash {
		t.Fatalf("updated redirect: artifacts=%d failure=%v", len(updated), failures[rule])
	}
	code, err := os.ReadFile(updated[rule].PureBundlePath)
	if err != nil {
		t.Fatal(err)
	}
	loaded, err := runtime.LoadPureBundle(string(code))
	if err != nil {
		t.Fatal(err)
	}
	if message, err := loaded.Message(nil, "violation"); err != nil || message != "new" {
		t.Fatalf("updated redirect message: %q %v", message, err)
	}
}

func TestBundleChainedLinkRetargetInvalidatesWarmSession(t *testing.T) {
	repoRoot, err := filepath.Abs("..")
	if err != nil {
		t.Fatal(err)
	}
	workspace := t.TempDir()
	control := filepath.Join(t.TempDir(), "edit-control")
	if err := os.WriteFile(control, []byte("disarmed"), 0o600); err != nil {
		t.Fatal(err)
	}
	asset := bundleEditAsset(t, repoRoot, control)
	writeTestFile(t, workspace, "rules-support/a.ts", `export const message = "A";`)
	writeTestFile(t, workspace, "rules-support/b.ts", `export const message = "B";`)
	middle := filepath.Join(workspace, "rules-support/middle.ts")
	helper := filepath.Join(workspace, "rules-support/helper.ts")
	if err := os.Symlink("a.ts", middle); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("middle.ts", helper); err != nil {
		t.Fatal(err)
	}
	source := parallelRuleSource(
		"fixture.chained-link",
		"Decl violation(Entity, Code). violation(Entity, Code) :- catalog.usable(Entity, Code).",
		"() => message",
	)
	source = strings.Replace(
		source,
		`import { dl, rule } from "@lintai/sdk";`,
		`import { dl, rule } from "@lintai/sdk";
import { message } from "../rules-support/helper.ts";`,
		1,
	)
	writeTestFile(t, workspace, "lintai-rules/chained-link.ts", source)
	options := parallelFixtureOptions(repoRoot, workspace)
	options.AssetRoot = asset
	check := func(session *Session) []string {
		t.Helper()
		result, err := session.Check(context.Background(), options)
		if err != nil {
			t.Fatal(err)
		}
		if len(result.Diagnostics) != 3 {
			t.Fatalf("expected three chained-link findings, got %+v", result.Diagnostics)
		}
		messages := make([]string, len(result.Diagnostics))
		for index, diagnostic := range result.Diagnostics {
			if diagnostic.RuleID != "fixture.chained-link" {
				t.Fatalf("unexpected chained-link diagnostic: %+v", diagnostic)
			}
			messages[index] = diagnostic.Message
		}
		return messages
	}
	warm := Open(Options{}, newParallelFixtureProducer())
	defer warm.Close()
	if got := strings.Join(check(warm), ","); got != "A,A,A" {
		t.Fatalf("initial chained-link behavior: %q", got)
	}
	beforeRetarget := warm.executionObservation().bundleArtifacts
	if err := os.Remove(middle); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("b.ts", middle); err != nil {
		t.Fatal(err)
	}
	retained := check(warm)
	retainedBehavior := strings.Join(retained, ",")
	retainedBuilds := warm.executionObservation().bundleArtifacts
	fresh := Open(Options{}, newParallelFixtureProducer())
	defer fresh.Close()
	freshBehavior := strings.Join(check(fresh), ",")
	if retainedBehavior != "B,B,B" || freshBehavior != retainedBehavior {
		t.Fatalf("retained behavior differs from fresh B: retained=%q fresh=%q", retainedBehavior, freshBehavior)
	}
	if retainedBuilds != beforeRetarget+1 {
		t.Fatalf("retargeted intermediate link did not rebuild the retained artifact: before=%d after=%d", beforeRetarget, retainedBuilds)
	}
}
