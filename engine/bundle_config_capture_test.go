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

func TestBundleConfigViewAcrossBuilds(t *testing.T) {
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
	writeTestFile(t, workspace, "rules-support/a.ts", `export const message = "A";`)
	writeTestFile(t, workspace, "rules-support/b.ts", `export const message = "B";`)
	writeTestFile(t, workspace, "lintai-rules/tsconfig.json", `{"compilerOptions":{"baseUrl":".","paths":{"@fixture/policy":["../rules-support/a.ts"]}}}`)
	rule := writeTestFile(t, workspace, "lintai-rules/alias.ts", `import { dl, rule } from "@lintai/sdk";
 import { message } from "@fixture/policy";
 export default rule("fixture.aba").version(1).setup(() => ({marker: message}))
 .program(dl`+"`"+`Decl violation(Entity).`+"`"+`).report("violation").message(() => message);`)
	build := func() map[string]bundle.Artifacts {
		t.Helper()
		artifacts, failures, err := bundle.BuildAll(context.Background(), asset, repo, workspace, t.TempDir(), []string{rule})
		if err != nil || failures[rule] != nil {
			t.Fatalf("build: %v %v", err, failures[rule])
		}
		return artifacts
	}
	stable := build()[rule]
	if err := os.WriteFile(control, []byte("config-aba"), 0o600); err != nil {
		t.Fatal(err)
	}
	artifacts := build()
	a := artifacts[rule]
	if a.Hash != stable.Hash {
		t.Fatalf("restored config changed captured A identity: %s != %s", a.Hash, stable.Hash)
	}
	prepared, fail, err := bundle.PrepareAll(context.Background(), asset, repo, workspace, artifacts, map[string]any{}, bundle.PrepareOptions{})
	if err != nil || len(fail) != 0 {
		t.Fatalf("prepare: %v %v", err, fail)
	}
	js, err := os.ReadFile(a.PureBundlePath)
	if err != nil {
		t.Fatal(err)
	}
	loaded, err := runtime.LoadPureBundle(string(js))
	if err != nil {
		t.Fatal(err)
	}
	msg, err := loaded.Message(nil, "violation")
	if err != nil {
		t.Fatal(err)
	}
	setup := prepared[rule].Setup.(map[string]any)["marker"]
	if setup != "A" || msg != "A" {
		t.Fatalf("incoherent output: prepare=%s pure=%s", setup, msg)
	}

	// An inherited config outside the workspace can change emitted code while
	// leaving esbuild's source graph unchanged.
	if err := os.WriteFile(control, []byte("disarmed"), 0o600); err != nil {
		t.Fatal(err)
	}
	external := t.TempDir()
	writeTestFile(t, external, "base.json", `{"extends":"./nested/options.json"}`)
	options := writeTestFile(t, external, "nested/options.json", `{"compilerOptions":{"useDefineForClassFields":false}}`)
	basePath, err := json.Marshal(filepath.Join(external, "base.json"))
	if err != nil {
		t.Fatal(err)
	}
	writeTestFile(t, workspace, "lintai-rules/tsconfig.json", `{"extends":`+string(basePath)+`}`)
	writeTestFile(t, workspace, "lintai-rules/alias.ts", `import { dl, rule } from "@lintai/sdk";
 class Marker { field: string; }
 const marker = () => Object.prototype.hasOwnProperty.call(new Marker(), "field") ? "present" : "absent";
 export default rule("fixture.aba").version(1).setup(() => ({marker: marker()}))
 .program(dl`+"`"+`Decl violation(Entity).`+"`"+`).report("violation").message(marker);`)
	observe := func(artifacts map[string]bundle.Artifacts) string {
		t.Helper()
		prepared, failures, err := bundle.PrepareAll(context.Background(), asset, repo, workspace, artifacts, map[string]any{}, bundle.PrepareOptions{})
		if err != nil || len(failures) != 0 {
			t.Fatalf("prepare external config: %v %v", err, failures)
		}
		js, err := os.ReadFile(artifacts[rule].PureBundlePath)
		if err != nil {
			t.Fatal(err)
		}
		loaded, err := runtime.LoadPureBundle(string(js))
		if err != nil {
			t.Fatal(err)
		}
		message, err := loaded.Message(nil, "violation")
		if err != nil || prepared[rule].Setup.(map[string]any)["marker"] != message {
			t.Fatalf("external config prepare/pure mismatch: %v %v", message, err)
		}
		return message
	}
	before := build()
	if got := observe(before); got != "absent" {
		t.Fatalf("external config false produced %q", got)
	}
	if err := os.WriteFile(options, []byte(`{"compilerOptions":{"useDefineForClassFields":true}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if before[rule].Manifest.Unchanged() {
		t.Fatal("external inherited config edit was not detected")
	}
	after := build()
	if got := observe(after); got != "present" || before[rule].Hash == after[rule].Hash {
		t.Fatalf("external config edit did not change behavior and identity: %q", got)
	}

	// Esbuild accepts a package export that names an extensionless config.
	// TypeScript's config resolver does not necessarily follow that export.
	packageRoot := filepath.Join(filepath.Dir(workspace), "node_modules", "fixture-config")
	writeTestFile(t, packageRoot, "package.json", `{"exports":{".":{"node":"./custom","browser":"./custom","default":"./custom"}}}`)
	packageOptions := writeTestFile(t, t.TempDir(), "options.json", `{"compilerOptions":{"useDefineForClassFields":false}}`)
	packageOptionsPath, err := json.Marshal(packageOptions)
	if err != nil {
		t.Fatal(err)
	}
	writeTestFile(t, packageRoot, "custom", `{"extends":`+string(packageOptionsPath)+`}`)
	writeTestFile(t, workspace, "lintai-rules/tsconfig.json", `{"extends":"fixture-config"}`)
	packageBefore := build()
	if got := observe(packageBefore); got != "absent" {
		t.Fatalf("package config false produced %q", got)
	}
	if err := os.WriteFile(control, []byte("config-external-aba:"+packageOptions), 0o600); err != nil {
		t.Fatal(err)
	}
	packageBetween := build()
	if got := observe(packageBetween); got != "absent" || packageBetween[rule].Hash != packageBefore[rule].Hash {
		t.Errorf("external config changed between builds: message=%q same hash=%v", got, packageBetween[rule].Hash == packageBefore[rule].Hash)
	}
	if err := os.WriteFile(control, []byte("disarmed"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(packageOptions, []byte(`{"compilerOptions":{"useDefineForClassFields":true}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if packageBefore[rule].Manifest.Unchanged() {
		t.Error("extensionless exported config did not track its inherited options")
	}
	packageAfter := build()
	if got := observe(packageAfter); got != "present" || packageAfter[rule].Hash == packageBefore[rule].Hash {
		t.Errorf("exported config edit did not change behavior and identity: %q", got)
	}

	// Wildcard exports may lead through a link to a config outside the package.
	if err := os.WriteFile(packageOptions, []byte(`{"compilerOptions":{"useDefineForClassFields":false}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	configDir := t.TempDir()
	writeTestFile(t, configDir, "choice", `{"extends":`+string(packageOptionsPath)+`}`)
	writeTestFile(t, packageRoot, "package.json", `{"exports":{"./*":"./configs/*"}}`)
	if err := os.Symlink(configDir, filepath.Join(packageRoot, "configs")); err != nil {
		t.Fatal(err)
	}
	writeTestFile(t, workspace, "lintai-rules/tsconfig.json", `{"extends":"fixture-config/choice"}`)
	wildcard := build()
	if got := observe(wildcard); got != "absent" {
		t.Errorf("linked wildcard config produced %q", got)
	}
	if err := os.WriteFile(packageOptions, []byte(`{"compilerOptions":{"useDefineForClassFields":true}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if wildcard[rule].Manifest.Unchanged() {
		t.Error("linked wildcard config did not track its inherited options")
	}

	if err := os.WriteFile(packageOptions, []byte(`{"compilerOptions":{"useDefineForClassFields":false}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	writeTestFile(t, packageRoot, "package.json", `{"exports":{"./*":"./*/choice"}}`)
	if err := os.Symlink(configDir, filepath.Join(packageRoot, "selected")); err != nil {
		t.Fatal(err)
	}
	writeTestFile(t, workspace, "lintai-rules/tsconfig.json", `{"extends":"fixture-config/selected"}`)
	wildcardLink := build()
	if got := observe(wildcardLink); got != "absent" {
		t.Errorf("wildcard-selected linked config produced %q", got)
	}
	if err := os.WriteFile(packageOptions, []byte(`{"compilerOptions":{"useDefineForClassFields":true}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if wildcardLink[rule].Manifest.Unchanged() {
		t.Error("wildcard-selected linked config did not track its inherited options")
	}

	if err := os.WriteFile(packageOptions, []byte(`{"compilerOptions":{"useDefineForClassFields":false}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	writeTestFile(t, packageRoot, "package.json", `{}`)
	writeTestFile(t, packageRoot, "standalone", `{"extends":`+string(packageOptionsPath)+`}`)
	writeTestFile(t, workspace, "lintai-rules/tsconfig.json", `{"extends":"fixture-config/standalone"}`)
	subpath := build()
	if got := observe(subpath); got != "absent" {
		t.Errorf("extensionless package subpath produced %q", got)
	}
	if err := os.WriteFile(packageOptions, []byte(`{"compilerOptions":{"useDefineForClassFields":true}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if subpath[rule].Manifest.Unchanged() {
		t.Error("extensionless package subpath did not track its inherited options")
	}

	// A package tsconfig selector can itself be absolute. Esbuild reads that
	// path internally, so restoring it after the two builds must not hide a
	// different pure-build configuration behind the stable hash.
	if err := os.WriteFile(packageOptions, []byte(`{"compilerOptions":{"useDefineForClassFields":false}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	writeTestFile(t, packageRoot, "package.json", `{"tsconfig":`+string(packageOptionsPath)+`}`)
	writeTestFile(t, workspace, "lintai-rules/tsconfig.json", `{"extends":"fixture-config"}`)
	absoluteStable := build()
	if got := observe(absoluteStable); got != "absent" {
		t.Fatalf("absolute selector stable config produced %q", got)
	}
	if err := os.Remove(control + ".hook"); err != nil {
		t.Fatalf("reset absolute selector edit marker: %v", err)
	}
	if err := os.WriteFile(control, []byte("config-external-aba:"+packageOptions), 0o600); err != nil {
		t.Fatal(err)
	}
	absoluteBetween := build()
	if _, err := os.Stat(control + ".hook"); err != nil {
		t.Fatalf("absolute selector edit hook did not run: %v", err)
	}
	if got := observe(absoluteBetween); got != "absent" || absoluteBetween[rule].Hash != absoluteStable[rule].Hash {
		t.Errorf("absolute selector A-to-B-to-A: message=%q same hash=%v", got, absoluteBetween[rule].Hash == absoluteStable[rule].Hash)
	}

	// A relative config path can also traverse past the mirrored filesystem
	// root. It must be bounded even if enough parent components reach the same
	// real file from both the original tree and the staging tree.
	if err := os.WriteFile(control, []byte("disarmed"), 0o600); err != nil {
		t.Fatal(err)
	}
	realOptions, err := filepath.EvalSymlinks(packageOptions)
	if err != nil {
		t.Fatal(err)
	}
	rootRelative := strings.TrimLeft(strings.TrimPrefix(realOptions, filepath.VolumeName(realOptions)), `/\`)
	traversal, err := json.Marshal(strings.Repeat("../", 64) + filepath.ToSlash(rootRelative))
	if err != nil {
		t.Fatal(err)
	}
	writeTestFile(t, workspace, "lintai-rules/tsconfig.json", `{"extends":`+string(traversal)+`}`)
	buildTraversal := func() (map[string]bundle.Artifacts, error) {
		t.Helper()
		artifacts, failures, err := bundle.BuildAll(context.Background(), asset, repo, workspace, t.TempDir(), []string{rule})
		if err == nil {
			err = failures[rule]
		}
		return artifacts, err
	}
	traversalStable, err := buildTraversal()
	if err != nil {
		if !strings.Contains(err.Error(), "bundle config path escapes captured view") {
			t.Fatalf("unexpected traversal rejection: %v", err)
		}
		return
	}
	if got := observe(traversalStable); got != "absent" {
		t.Fatalf("relative traversal stable config produced %q", got)
	}
	if err := os.WriteFile(control, []byte("config-external-aba:"+packageOptions), 0o600); err != nil {
		t.Fatal(err)
	}
	traversalBetween, err := buildTraversal()
	if err != nil {
		if !strings.Contains(err.Error(), "bundle config path escapes captured view") {
			t.Fatalf("unexpected traversal rejection: %v", err)
		}
		return
	}
	if got := observe(traversalBetween); got != "absent" || traversalBetween[rule].Hash != traversalStable[rule].Hash {
		t.Errorf("relative traversal A-to-B-to-A: message=%q same hash=%v", got, traversalBetween[rule].Hash == traversalStable[rule].Hash)
	}
}

func TestBundleConfigCannotSearchOutsideView(t *testing.T) {
	for _, scenario := range []struct{ name, specifier, file string }{
		{"package", "fixture-ancestor", "tsconfig.json"},
		{"json subpath", "fixture-ancestor/options.json", "options.json"},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			repo, err := filepath.Abs("..")
			if err != nil {
				t.Fatal(err)
			}
			privateTmp := t.TempDir()
			t.Setenv("TMPDIR", privateTmp)
			workspace := t.TempDir()
			control := filepath.Join(t.TempDir(), "control")
			if err := os.WriteFile(control, []byte("disarmed"), 0o600); err != nil {
				t.Fatal(err)
			}
			asset := bundleEditAsset(t, repo, control)
			helper := filepath.Join(asset, "scripts", "bundle-rule.mjs")
			code, err := os.ReadFile(helper)
			if err != nil {
				t.Fatal(err)
			}
			between := "\t\tconst pureBuild = await build({"
			after := "\t\tconst { hash, inputs, probes } = await ruleHash("
			if strings.Count(string(code), between) != 1 || strings.Count(string(code), after) != 1 {
				t.Fatal("ancestor edit anchors are not unique")
			}
			insert := fmt.Sprintf(`		if ((await fs.readFile(%q, "utf8")).trim() === "ancestor-aba") {
			const dir = path.join(path.dirname(view.root), "node_modules", "fixture-ancestor");
			await fs.mkdir(dir, {recursive: true});
			await fs.writeFile(path.join(dir, %q), JSON.stringify({compilerOptions: {useDefineForClassFields: false}}));
		}
`, control, scenario.file)
			cleanup := fmt.Sprintf(`		if ((await fs.readFile(%q, "utf8")).trim() === "ancestor-aba") {
			await fs.rm(path.join(path.dirname(view.root), "node_modules"), {recursive: true, force: true});
		}
`, control)
			code = []byte(strings.Replace(string(code), between, insert+between, 1))
			code = []byte(strings.Replace(string(code), after, cleanup+after, 1))
			if err := os.WriteFile(helper, code, 0o600); err != nil {
				t.Fatal(err)
			}
			extension, err := json.Marshal(scenario.specifier)
			if err != nil {
				t.Fatal(err)
			}
			writeTestFile(t, workspace, "lintai-rules/tsconfig.json", `{"extends":`+string(extension)+`}`)
			rule := writeTestFile(t, workspace, "lintai-rules/rule.ts", `import {dl, rule} from "@lintai/sdk";
class Marker { field: string; }
const marker = () => Object.prototype.hasOwnProperty.call(new Marker(), "field") ? "present" : "absent";
export default rule("fixture.ancestor-config").version(1).setup(() => ({marker: marker()}))
.program(dl`+"`"+`Decl violation(Entity).`+"`"+`).report("violation").message(marker);`)
			build := func() (map[string]bundle.Artifacts, error) {
				t.Helper()
				artifacts, failures, err := bundle.BuildAll(context.Background(), asset, repo, workspace, t.TempDir(), []string{rule})
				if err == nil {
					err = failures[rule]
				}
				return artifacts, err
			}
			stable, err := build()
			if err != nil {
				if !strings.Contains(err.Error(), `Expected string in JSON but found "unresolved"`) {
					t.Fatalf("unexpected unresolved-config rejection: %v", err)
				}
				return
			}
			if err := os.WriteFile(control, []byte("ancestor-aba"), 0o600); err != nil {
				t.Fatal(err)
			}
			betweenBuilds, err := build()
			if err != nil {
				if !strings.Contains(err.Error(), `Expected string in JSON but found "unresolved"`) {
					t.Fatalf("unexpected ancestor-config rejection: %v", err)
				}
				return
			}
			prepared, failures, err := bundle.PrepareAll(context.Background(), asset, repo, workspace, betweenBuilds, nil, bundle.PrepareOptions{})
			if err != nil || len(failures) != 0 {
				t.Fatalf("prepare: %v %v", err, failures)
			}
			js, err := os.ReadFile(betweenBuilds[rule].PureBundlePath)
			if err != nil {
				t.Fatal(err)
			}
			loaded, err := runtime.LoadPureBundle(string(js))
			if err != nil {
				t.Fatal(err)
			}
			message, err := loaded.Message(nil, "violation")
			if err != nil {
				t.Fatal(err)
			}
			setup := prepared[rule].Setup.(map[string]any)["marker"]
			if setup != message || stable[rule].Hash != betweenBuilds[rule].Hash {
				t.Fatalf("ancestor config escaped the view: prepare=%v pure=%s stable hash=%s edited hash=%s", setup, message, stable[rule].Hash, betweenBuilds[rule].Hash)
			}
		})
	}
}
