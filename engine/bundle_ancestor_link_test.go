package engine

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
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
