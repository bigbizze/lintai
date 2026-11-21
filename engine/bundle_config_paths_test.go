package engine

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/bigbizze/lintai/bundle"
	"github.com/bigbizze/lintai/runtime"
)

func TestBundleConfigPathsBase(t *testing.T) {
	repo, err := filepath.Abs("..")
	if err != nil {
		t.Fatal(err)
	}
	asset := filepath.Join(repo, "packages/client")
	for _, scenario := range []struct {
		name          string
		configs       map[string]string
		parentConfigs map[string]string
		links         map[string]string
	}{
		{
			name: "no inherited base",
			configs: map[string]string{
				"config/base.json":           `{}`,
				"lintai-rules/tsconfig.json": `{"extends":"../config/base.json","compilerOptions":{"paths":{"@fixture/policy":["../rules-support/a.ts"]}}}`,
			},
		},
		{
			name: "inherited paths without base use declaring file",
			configs: map[string]string{
				"config/base.json":           `{"compilerOptions":{"paths":{"@fixture/policy":["../rules-support/a.ts"]}}}`,
				"lintai-rules/tsconfig.json": `{"extends":"../config/base.json"}`,
			},
		},
		{
			name: "inherited base and ordered extends",
			configs: map[string]string{
				"config/first.json":          `{"compilerOptions":{"baseUrl":"../wrong"}}`,
				"config/base.json":           `{"compilerOptions":{"baseUrl":"../rules-support/nested/deeper"}}`,
				"lintai-rules/tsconfig.json": `{"extends":["../config/first.json","../config/base.json"],"compilerOptions":{"paths":{"@fixture/policy":["../../a.ts"]}}}`,
			},
		},
		{
			name: "relative extends selects exact file before json",
			configs: map[string]string{
				"config/choice":              `{"compilerOptions":{"baseUrl":"../rules-support/nested/deeper"}}`,
				"config/choice.json":         `{"compilerOptions":{"baseUrl":"/"}}`,
				"lintai-rules/tsconfig.json": `{"extends":"../config/choice","compilerOptions":{"paths":{"@fixture/policy":["../../a.ts"]}}}`,
			},
		},
		{
			name: "descendant base applies to inherited paths",
			configs: map[string]string{
				"config/base.json":           `{"compilerOptions":{"paths":{"@fixture/policy":["../../a.ts"]}}}`,
				"lintai-rules/tsconfig.json": `{"extends":"../config/base.json","compilerOptions":{"baseUrl":"../rules-support/nested/deeper"}}`,
			},
		},
		{
			name: "configDir in inherited base",
			configs: map[string]string{
				"config/base.json":           `{"compilerOptions":{"baseUrl":"${configDir}/../rules-support/nested/deeper"}}`,
				"lintai-rules/tsconfig.json": `{"extends":"../config/base.json","compilerOptions":{"paths":{"@fixture/policy":["../../a.ts"]}}}`,
			},
		},
		{
			name: "configDir in inherited paths",
			configs: map[string]string{
				"config/base.json":           `{"compilerOptions":{"paths":{"@fixture/policy":["${configDir}/../rules-support/a.ts"]}}}`,
				"lintai-rules/tsconfig.json": `{"extends":"../config/base.json"}`,
			},
		},
		{
			name: "package config with conditional exports",
			configs: map[string]string{
				"node_modules/fixture-config/package.json": `{"exports":{".":{"node":"./node.json","browser":"./browser.json"}}}`,
				"node_modules/fixture-config/node.json":    `{"compilerOptions":{"baseUrl":"../../rules-support/nested/deeper"}}`,
				"node_modules/fixture-config/browser.json": `{"compilerOptions":{"baseUrl":"../../rules-support/nested/deeper"}}`,
				"lintai-rules/tsconfig.json":               `{"extends":"fixture-config","compilerOptions":{"paths":{"@fixture/policy":["../../a.ts"]}}}`,
			},
		},
		{
			name: "conditional paths keep their own bases",
			configs: map[string]string{
				"node_modules/fixture-config/package.json": `{"exports":{".":{"node":"./node.json","browser":"./browser.json"}}}`,
				"node_modules/fixture-config/node.json":    `{"compilerOptions":{"baseUrl":"/","paths":{"@fixture/policy":["${configDir}/../rules-support/a.ts"]}}}`,
				"node_modules/fixture-config/browser.json": `{"compilerOptions":{"baseUrl":"../../rules-support/nested/deeper","paths":{"@fixture/policy":["../../a.ts"]}}}`,
				"lintai-rules/tsconfig.json":               `{"extends":"fixture-config"}`,
			},
		},
		{
			name: "inherited base through package link",
			configs: map[string]string{
				"config-package/package.json": `{"tsconfig":"options.json"}`,
				"config-package/options.json": `{"compilerOptions":{"baseUrl":"../rules-support/nested/deeper"}}`,
				"lintai-rules/tsconfig.json":  `{"extends":"fixture-config","compilerOptions":{"paths":{"@fixture/policy":["../../a.ts"]}}}`,
			},
			links: map[string]string{"node_modules/fixture-config": "../config-package"},
		},
		{
			name: "near package shadows ancestor config",
			configs: map[string]string{
				"node_modules/fixture-config/tsconfig.json": `{"compilerOptions":{"baseUrl":"../../rules-support/nested/deeper"}}`,
				"lintai-rules/tsconfig.json":                `{"extends":"fixture-config","compilerOptions":{"paths":{"@fixture/policy":["../../a.ts"]}}}`,
			},
			parentConfigs: map[string]string{
				"node_modules/fixture-config/tsconfig.json": `{"compilerOptions":{"baseUrl":"/"}}`,
			},
		},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			workspace := t.TempDir()
			writeTestFile(t, workspace, "rules-support/a.ts", `export const message = "A";`)
			writeTestFile(t, workspace, "rules-support/nested/deeper/unused.ts", `export {};`)
			for name, content := range scenario.configs {
				writeTestFile(t, workspace, name, content)
			}
			for name, content := range scenario.parentConfigs {
				writeTestFile(t, filepath.Dir(workspace), name, content)
			}
			for name, target := range scenario.links {
				link := filepath.Join(workspace, name)
				if err := os.MkdirAll(filepath.Dir(link), 0o755); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(target, link); err != nil {
					t.Fatal(err)
				}
			}
			rule := writeTestFile(t, workspace, "lintai-rules/rule.ts", `import { dl, rule } from "@lintai/sdk";
import { message } from "@fixture/policy";
export default rule("fixture.paths-base").version(1).setup(() => ({marker: message}))
.program(dl`+"`"+`Decl violation(Entity).`+"`"+`).report("violation").message(() => message);`)
			artifacts, failures, err := bundle.BuildAll(context.Background(), asset, repo, workspace, t.TempDir(), []string{rule})
			if err != nil || failures[rule] != nil || len(artifacts) != 1 {
				t.Fatalf("build: artifacts=%d failures=%v err=%v", len(artifacts), failures, err)
			}
			if message := bundlePathsMessage(t, asset, repo, workspace, rule, artifacts); message != "A" {
				t.Fatalf("setup/pure message: %q", message)
			}
		})
	}
}

func bundlePathsMessage(t *testing.T, asset, repo, workspace, rule string, artifacts map[string]bundle.Artifacts) string {
	t.Helper()
	prepared, failures, err := bundle.PrepareAll(context.Background(), asset, repo, workspace, artifacts, nil, bundle.PrepareOptions{})
	if err != nil || len(failures) != 0 {
		t.Fatalf("prepare: %v %v", err, failures)
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
		t.Fatalf("setup/pure mismatch: setup=%v message=%q err=%v", prepared[rule].Setup, message, err)
	}
	return message
}

func TestBundleConfigPathsCapturedAcrossBuilds(t *testing.T) {
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
	for _, name := range []string{"a", "b"} {
		writeTestFile(t, workspace, "rules-support/"+name+"/value.ts", `export const message = "`+strings.ToUpper(name)+`";`)
		writeTestFile(t, workspace, "rules-support/"+name+"/nested/unused.ts", `export {};`)
	}
	baseA := `{"compilerOptions":{"baseUrl":"../rules-support/a/nested"}}`
	base := writeTestFile(t, workspace, "config/base.json", baseA)
	writeTestFile(t, workspace, "lintai-rules/tsconfig.json", `{"extends":"../config/base.json","compilerOptions":{"paths":{"@fixture/policy":["../value.ts"]}}}`)
	rule := writeTestFile(t, workspace, "lintai-rules/rule.ts", `import { dl, rule } from "@lintai/sdk";
import { message } from "@fixture/policy";
export default rule("fixture.paths-snapshot").version(1).setup(() => ({marker: message}))
.program(dl`+"`"+`Decl violation(Entity).`+"`"+`).report("violation").message(() => message);`)
	build := func() map[string]bundle.Artifacts {
		t.Helper()
		artifacts, failures, err := bundle.BuildAll(context.Background(), asset, repo, workspace, t.TempDir(), []string{rule})
		if err != nil || failures[rule] != nil || len(artifacts) != 1 {
			t.Fatalf("build: artifacts=%d failures=%v err=%v", len(artifacts), failures, err)
		}
		return artifacts
	}
	stable := build()
	if err := os.WriteFile(control, []byte("config-path-base-aba:"+base), 0o600); err != nil {
		t.Fatal(err)
	}
	between := build()
	if _, err := os.Stat(control + ".hook"); err != nil {
		t.Fatalf("base config edit hook did not run: %v", err)
	}
	if got := bundlePathsMessage(t, asset, repo, workspace, rule, between); got != "A" || between[rule].Hash != stable[rule].Hash {
		t.Fatalf("A-to-B-to-A changed captured behavior or identity: message=%q same hash=%v", got, between[rule].Hash == stable[rule].Hash)
	}
	if err := os.WriteFile(control, []byte("disarmed"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(base, []byte(strings.Replace(baseA, "rules-support/a/nested", "rules-support/b/nested", 1)), 0o600); err != nil {
		t.Fatal(err)
	}
	if between[rule].Manifest.Unchanged() {
		t.Fatal("inherited base edit did not invalidate the captured manifest")
	}
	changed := build()
	if got := bundlePathsMessage(t, asset, repo, workspace, rule, changed); got != "B" || changed[rule].Hash == stable[rule].Hash {
		t.Fatalf("inherited base change did not update behavior and identity: message=%q same hash=%v", got, changed[rule].Hash == stable[rule].Hash)
	}
}

func TestBundleConfigPathsEscapeRejected(t *testing.T) {
	repo, err := filepath.Abs("..")
	if err != nil {
		t.Fatal(err)
	}
	for _, scenario := range []string{"declaring file", "descendant base", "duplicate baseUrl", "configDir substitution", "wildcard substitution"} {
		t.Run(scenario, func(t *testing.T) {
			workspace := t.TempDir()
			support := writeTestFile(t, workspace, "rules-support/a.ts", `export const message = "A";`)
			resolved, err := filepath.EvalSymlinks(support)
			if err != nil {
				t.Fatal(err)
			}
			rootRelative := strings.TrimLeft(strings.TrimPrefix(filepath.ToSlash(resolved), filepath.VolumeName(resolved)), "/")
			quote := func(value string) string {
				t.Helper()
				encoded, err := json.Marshal(value)
				if err != nil {
					t.Fatal(err)
				}
				return string(encoded)
			}
			var base, config, specifier string
			switch scenario {
			case "declaring file":
				base = `{}`
				config = `{"extends":"../config/base.json","compilerOptions":{"paths":{"@fixture/policy":[` + quote(strings.Repeat("../", 64)+rootRelative) + `]}}}`
				specifier = "@fixture/policy"
			case "descendant base":
				base = `{"compilerOptions":{"paths":{"@fixture/policy":[` + quote("../"+rootRelative) + `]}}}`
				config = `{"extends":"../config/base.json","compilerOptions":{"baseUrl":` + quote(filepath.VolumeName(resolved)+string(filepath.Separator)) + `}}`
				specifier = "@fixture/policy"
			case "duplicate baseUrl":
				base = `{"compilerOptions":{"baseUrl":` + quote(filepath.VolumeName(resolved)+string(filepath.Separator)) + `,"baseUrl":"../rules-support/nested/deeper"}}`
				config = `{"extends":"../config/base.json","compilerOptions":{"paths":{"@fixture/policy":[` + quote("../"+rootRelative) + `]}}}`
				specifier = "@fixture/policy"
			case "configDir substitution":
				configDir := filepath.Join(filepath.Dir(filepath.Dir(resolved)), "lintai-rules")
				depth := len(strings.FieldsFunc(strings.TrimPrefix(filepath.ToSlash(configDir), filepath.VolumeName(configDir)), func(r rune) bool { return r == '/' }))
				base = `{}`
				config = `{"extends":"../config/base.json","compilerOptions":{"paths":{"@fixture/policy":[` + quote("${configDir}"+strings.Repeat("../", depth+1)+rootRelative) + `]}}}`
				specifier = "@fixture/policy"
			case "wildcard substitution":
				base = `{"compilerOptions":{"baseUrl":` + quote(filepath.VolumeName(resolved)+string(filepath.Separator)) + `}}`
				config = `{"extends":"../config/base.json","compilerOptions":{"paths":{"@fixture/*":["*"]}}}`
				specifier = "@fixture/../" + rootRelative
			}
			writeTestFile(t, workspace, "config/base.json", base)
			writeTestFile(t, workspace, "lintai-rules/tsconfig.json", config)
			rule := writeTestFile(t, workspace, "lintai-rules/rule.ts", `import { message } from `+quote(specifier)+`; export default message;`)
			artifacts, failures, err := bundle.BuildAll(context.Background(), filepath.Join(repo, "packages/client"), repo, workspace, t.TempDir(), []string{rule})
			if err != nil || len(artifacts) != 0 || failures[rule] == nil || !strings.Contains(failures[rule].Error(), "bundle config path escapes captured view") {
				t.Fatalf("escaping alias was not rejected before publication: artifacts=%v failures=%v err=%v", artifacts, failures, err)
			}
		})
	}
}

func TestBundleConfigPathsUnestablishedBaseRejected(t *testing.T) {
	repo, err := filepath.Abs("..")
	if err != nil {
		t.Fatal(err)
	}
	workspace := t.TempDir()
	writeTestFile(t, workspace, "rules-support/a.ts", `export const message = "A";`)
	writeTestFile(t, workspace, "lintai-rules/tsconfig.json", `{"extends":"../config/missing.json","compilerOptions":{"paths":{"@fixture/policy":["../rules-support/a.ts"]}}}`)
	rule := writeTestFile(t, workspace, "lintai-rules/rule.ts", `import { message } from "@fixture/policy"; export default message;`)
	artifacts, failures, err := bundle.BuildAll(context.Background(), filepath.Join(repo, "packages/client"), repo, workspace, t.TempDir(), []string{rule})
	if err != nil || len(artifacts) != 0 || failures[rule] == nil || !strings.Contains(failures[rule].Error(), "bundle config paths base cannot be established") {
		t.Fatalf("unestablished base was not rejected: artifacts=%v failures=%v err=%v", artifacts, failures, err)
	}
}

func TestBundleConfigPathsChangedLookupRejected(t *testing.T) {
	repo, err := filepath.Abs("..")
	if err != nil {
		t.Fatal(err)
	}
	workspace := t.TempDir()
	support := writeTestFile(t, workspace, "rules-support/a.ts", `export const message = "A";`)
	resolved, err := filepath.EvalSymlinks(support)
	if err != nil {
		t.Fatal(err)
	}
	rootRelative := strings.TrimLeft(strings.TrimPrefix(filepath.ToSlash(resolved), filepath.VolumeName(resolved)), "/")
	baseURL, err := json.Marshal(filepath.VolumeName(resolved) + string(filepath.Separator))
	if err != nil {
		t.Fatal(err)
	}
	absolute, err := json.Marshal(resolved)
	if err != nil {
		t.Fatal(err)
	}
	writeTestFile(t, workspace, "lintai-rules/tsconfig.json", `{"compilerOptions":{"baseUrl":`+string(baseURL)+`,"paths":{"@fixture/safe":[`+string(absolute)+`],"@fixture/*":["*"]}}}`)
	rule := writeTestFile(t, workspace, "lintai-rules/rule.ts", `import { message } from "@fixture/safe"; export default message;`)
	control := filepath.Join(t.TempDir(), "control")
	if err := os.WriteFile(control, []byte("config-path-lookup:@fixture/../"+rootRelative), 0o600); err != nil {
		t.Fatal(err)
	}
	asset := bundleEditAsset(t, repo, control)
	artifacts, failures, err := bundle.BuildAll(context.Background(), asset, repo, workspace, t.TempDir(), []string{rule})
	if _, hookErr := os.Stat(control + ".hook"); hookErr != nil {
		t.Fatalf("lookup edit hook did not run: %v", hookErr)
	}
	if err != nil || len(artifacts) != 0 || failures[rule] == nil || !strings.Contains(failures[rule].Error(), "bundle config path escapes captured view") {
		t.Fatalf("changed escaping lookup was not rejected: artifacts=%v failures=%v err=%v", artifacts, failures, err)
	}
}
