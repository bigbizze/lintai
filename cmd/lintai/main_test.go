package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/bigbizze/lintai/backend/typescript"
	"github.com/bigbizze/lintai/diagnostics"
	"github.com/bigbizze/lintai/engine"
	"github.com/bigbizze/lintai/internal/testpack"
	"github.com/bigbizze/lintai/server"
)

// The fixture workspace contains planted violations of every fixture rule.
// A change to extraction, the prelude or a rule that adds or loses a
// violation changes this exact set.
func TestFixtureRulesReportPlantedViolations(t *testing.T) {
	repoRoot, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	args := []string{
		"--json", "--explain",
		"--workspace-root", testpack.Build(t, repoRoot, filepath.Join(repoRoot, "testdata/fixtures/workspace"), filepath.Join(repoRoot, "testdata/fixtures/rules/*.ts")),
	}
	first := runJSON(t, args)
	second := runJSON(t, args)
	if !bytes.Equal(first, second) {
		t.Fatalf("two runs produced different output:\n%s\n---\n%s", first, second)
	}

	var items []diagnostics.Diagnostic
	if err := json.Unmarshal(first, &items); err != nil {
		t.Fatal(err)
	}
	got := make([]string, 0, len(items))
	for _, item := range items {
		if item.DiagnosticKind != diagnostics.KindArchitectureViolation {
			t.Fatalf("unexpected rule error: %+v", item)
		}
		got = append(got, fmt.Sprintf("%s %s %s:%d", item.RuleID, item.AssertionID, item.SourceLocation.File, item.SourceLocation.StartLine))
		if len(item.Explanation) == 0 {
			t.Fatalf("expected an explanation for %s", got[len(got)-1])
		}
	}
	want := []string{
		"arch.core-no-service-reach violation src/core/entry.ts:1",
		"arch.core-no-service-reach violation src/core/leaf.ts:1",
		"arch.core-no-service-reach violation src/core/mid.ts:1",
		"arch.layers violation src/pure/helper.ts:1",
		"arch.pure-no-service-imports violation src/pure/helper.ts:1",
		"arch.pure-no-effects violation src/pure/helper.ts:3",
		"arch.pure-no-effects-strict reaches_effect src/pure/helper.ts:3",
		"arch.pure-no-effects-strict leaves_analysis src/pure/launder.ts:1",
		"arch.pure-no-effects violation src/pure/math.ts:3",
		"arch.pure-no-effects-strict reaches_effect src/pure/math.ts:3",
		"arch.pure-no-effects-strict reaches_effect src/pure/time.ts:1",
		"arch.ambient clock_in_pure src/pure/time.ts:2",
		"arch.pure-no-effects-strict reaches_effect src/pure/time.ts:2",
		"arch.pure-no-effects-strict leaves_analysis src/pure/wired.ts:3",
		"arch.server-only missing_marker src/server/config.ts:1",
		"arch.ambient env_outside_config src/server/handler.ts:5",
		"arch.ambient env_dynamic src/server/handler.ts:5",
		"arch.server-wires-services wires src/server/wire-clock.ts:1",
	}
	if !slices.Equal(got, want) {
		t.Fatalf("planted violations differ:\ngot:\n%s\nwant:\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}

}

// Each family has its own workspace, so its near-misses do not alter the
// existing fixture goldens. The documentation contains the exact tested rules.
func TestRelationExamples(t *testing.T) {
	repoRoot, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	docs, err := os.ReadFile(filepath.Join(repoRoot, "docs/examples.md"))
	if err != nil {
		t.Fatal(err)
	}
	families := []struct {
		name        string
		sourceLinks string
		want        []string
	}{
		{
			name: "a1-source-identity",
			want: []string{
				"arch.no-env-writes violation src/app/violation.ts:1",
				"arch.checked-syntax-strict violation src/checked/violation.ts:1",
				"arch.client-directive violation src/client/violation.ts:1",
				"arch.domain-no-async-exports violation src/domain/violation.ts:1",
				"arch.domain-no-global-fetch violation src/domain/violation.ts:2",
				"arch.reducer-no-async-methods violation src/reducers/violation.ts:2",
				"arch.ui-no-import-meta-env violation src/ui/violation.ts:1",
			},
		},
		{
			name: "a2-imports",
			want: []string{
				"arch.core-approved-packages violation src/core/packages.ts:2",
				"arch.core-no-internal-types violation src/core/dependencies.ts:3",
				"arch.core-resolved-imports-strict violation src/core/violation.ts:1",
				"arch.core-no-dynamic-loading-strict dynamic_load src/core/violation.ts:2",
				"arch.core-no-dynamic-loading-strict opaque_load src/core/violation.ts:3",
				"arch.no-broad-function-type violation src/types/violation.ts:1",
			},
		},
		{
			name: "a3-invocations",
			want: []string{
				"arch.domain-no-library-clock violation src/domain/violation.ts:2",
				"arch.known-overlap-strict violation src/overlap/shared.ts:1",
				"arch.owner-adjustment-strict violation src/overlap/shared.ts:1",
				"arch.query-no-writes-strict forbidden src/queries/violation.ts:6",
				"arch.query-no-writes-strict indeterminate src/queries/indeterminate.ts:2",
				"arch.security-callbacks-strict forbidden src/handlers/violation.ts:3",
				"arch.security-callbacks-strict indeterminate src/handlers/indeterminate.ts:2",
			},
		},
		{
			name: "a4-signatures",
			want: []string{
				"arch.handler-signatures wrong_arity src/handlers/violation.ts:1",
				"arch.handler-signatures optional_request src/handlers/violation.ts:2",
				"arch.handler-signatures rest_request src/handlers/violation.ts:3",
				"arch.id-lookup-return violation src/ids/violation.ts:1",
				"arch.id-lookup-return violation src/ids/lookalike.ts:2",
				"arch.secure-signature-evidence-strict omitted src/secure/omitted.ts:1",
			},
		},
		{
			name: "a5-structural-types",
			want: []string{
				"arch.api-generic-names violation src/api/violation.ts:1",
				"arch.event-id optional_id src/events/violation.ts:1",
				"arch.event-id mutable_id src/events/violation.ts:2",
				"arch.coordinate-tuples optional_element src/geometry/violation.ts:1",
				"arch.coordinate-tuples rest_element src/geometry/violation.ts:2",
				"arch.coordinate-tuples mutable_tuple src/geometry/violation.ts:3",
				"arch.hook-callbacks optional_event src/hooks/violation.ts:1",
				"arch.hook-callbacks rest_event src/hooks/violation.ts:2",
				"arch.hook-callbacks nonvoid_callback src/hooks/violation.ts:3",
				"arch.hook-callbacks missing_event src/hooks/violation.ts:4",
				"arch.registration-no-admin-role violation src/registration/violation.ts:1",
				"arch.parser-input-types-strict incomplete src/strict/violation.ts:2",
				"arch.parser-input-types-strict forbidden src/strict/violation.ts:3",
			},
		},
		{
			name: "a6-declarations",
			want: []string{
				"arch.public-declaration-types-strict incomplete src/api/conflict.ts:1",
				"arch.public-domain-no-async violation src/api/violation.ts:2",
				"arch.public-types-no-any violation src/api/violation.ts:1",
			},
		},
		{
			name: "a7-exports",
			want: []string{
				"arch.public-export-evidence-strict incomplete_binding src/api/incomplete.ts:1",
				"arch.public-export-evidence-strict incomplete_binding src/api/incomplete.ts:1",
				"arch.public-export-evidence-strict incomplete_inventory src/api/incomplete.ts:1",
				"arch.public-no-internal-reexports violation src/api/violation.ts:1",
			},
		},
		{
			name:        "a8-source-correspondence",
			sourceLinks: "source-links.json",
			want: []string{
				"arch.declaration-map-boundary-strict candidate src/app/ambiguous.ts:2",
				"arch.declaration-map-boundary-strict uncertain src/app/ambiguous.ts:2",
				"arch.declaration-map-boundary-strict uncertain src/app/incomplete.ts:2",
				"arch.declaration-map-boundary-strict precise src/app/violation.ts:2",
				"arch.configured-source-evidence-strict adjusted_source src/app/incomplete.ts:2",
				"arch.configured-source-evidence-strict adjusted_source src/app/violation.ts:2",
				"arch.configured-source-evidence-strict incomplete_binding src/app/incomplete.ts:2",
				"arch.configured-source-evidence-strict incomplete_entry src/app/incomplete.ts:2",
				"arch.no-private-configured-calls violation src/app/violation.ts:2",
				"arch.selected-source-links-applied-strict unused packages/contracts/src/public.ts:1",
			},
		},
	}
	for _, family := range families {
		t.Run(family.name, func(t *testing.T) {
			base := filepath.Join(repoRoot, "testdata/fixtures/relation-examples", family.name)
			ruleGlob := filepath.Join(base, "rules/*.ts")
			rulePaths, err := filepath.Glob(ruleGlob)
			if err != nil || len(rulePaths) == 0 {
				t.Fatalf("find family rules: %v (found %d)", err, len(rulePaths))
			}
			for _, path := range rulePaths {
				source, err := os.ReadFile(path)
				if err != nil {
					t.Fatal(err)
				}
				block := "```ts\n" + string(source) + "```\n"
				if count := strings.Count(string(docs), block); count != 1 {
					t.Errorf("%s: expected one exact rule source block in docs/examples.md, got %d", path, count)
				}
			}
			args := []string{
				"--json", "--explain",
				"--workspace-root", testpack.Build(t, repoRoot, filepath.Join(base, "workspace"), ruleGlob),
			}
			if family.sourceLinks != "" {
				args = append(args, "--source-links", family.sourceLinks)
			}
			first := runJSON(t, args)
			second := runJSON(t, args)
			if !bytes.Equal(first, second) {
				t.Fatalf("two runs produced different output:\n%s\n---\n%s", first, second)
			}
			var items []diagnostics.Diagnostic
			if err := json.Unmarshal(first, &items); err != nil {
				t.Fatal(err)
			}
			got := make([]string, 0, len(items))
			for _, item := range items {
				if item.DiagnosticKind != diagnostics.KindArchitectureViolation {
					t.Fatalf("unexpected rule error: %+v", item)
				}
				got = append(got, fmt.Sprintf("%s %s %s:%d", item.RuleID, item.AssertionID, item.SourceLocation.File, item.SourceLocation.StartLine))
				if len(item.Explanation) == 0 {
					t.Fatalf("expected an explanation for %s", got[len(got)-1])
				}
			}
			slices.Sort(got)
			want := slices.Clone(family.want)
			slices.Sort(want)
			if !slices.Equal(got, want) {
				t.Fatalf("planted violations differ:\ngot:\n%s\nwant:\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
			}
		})
	}
}

// V15/V18: serve re-resolves producer configuration and replaces the old
// producer when scope changes, including retraction of its previous files.
func TestServeSessionReplacesChangedProducerConfiguration(t *testing.T) {
	repo, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	for _, name := range []string{"a.ts", "b.ts"} {
		if err := os.WriteFile(filepath.Join(root, name), []byte("export const value = 1;\n"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	config := filepath.Join(root, "lintai.toml")
	setConfig := func(excluded string) {
		t.Helper()
		if err := os.WriteFile(config, []byte("[workspace]\nexclude = [\""+excluded+"\"]\n"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	setConfig("b.ts")
	testpack.Build(t, repo, root, filepath.Join(repo, "testdata/fixtures/rules/layers.ts"))
	instance := &serveSession{params: server.InitializeParams{WorkspaceRoot: root, RuleWorkers: 2}}
	defer instance.Close()
	first, err := instance.RefreshAll(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Contains(names(first), "a.ts") || slices.Contains(names(first), "b.ts") {
		t.Fatalf("initial scope: %v", first.Files)
	}
	previous := instance.session
	setConfig("a.ts")
	second, err := instance.RefreshAll(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if instance.session == previous || slices.Contains(names(second), "a.ts") || !slices.Contains(names(second), "b.ts") {
		t.Fatalf("changed configuration retained old producer scope: %v", second.Files)
	}
	if _, err := previous.Current(); err == nil {
		t.Fatal("old producer session was not closed")
	}
}

func runJSON(t *testing.T, args []string) []byte {
	t.Helper()
	var stdout bytes.Buffer
	err := run(context.Background(), args, &stdout)
	if code := exitCode(err, io.Discard); code != exitViolations {
		t.Fatalf("exit code %d, want %d: %v", code, exitViolations, err)
	}
	return stdout.Bytes()
}

// CI must be able to tell "the rules found violations" apart from "lintai
// could not check the workspace".
func TestExitCodes(t *testing.T) {
	repoRoot, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	workspace := filepath.Join(repoRoot, "testdata/fixtures/workspace")
	workspace = testpack.Build(t, repoRoot, workspace, filepath.Join(repoRoot, "testdata/fixtures/rules/layers.ts"))
	cases := []struct {
		name string
		args []string
		want int
	}{
		{"violations", []string{"--workspace-root", workspace}, exitViolations},
		{"parallel check", []string{"check", "--workspace-root", workspace, "--rule-workers", "1", "--rule-workers", "2"}, exitViolations},
		{"negative workers", []string{"--rule-workers", "-1"}, exitUsage},
		{"noninteger workers", []string{"--rule-workers", "1.5"}, exitUsage},
		{"clean", []string{"--workspace-root", workspace, "--exclude", "src/pure/**"}, exitClean},
		{"unknown flag", []string{"--no-such-flag"}, exitUsage},
		{"bad file scope", []string{"--workspace-root", workspace, "--files", "all"}, exitUsage},
		{"obsolete rule glob flag", []string{"--workspace-root", workspace, "--rules", "missing/*.ts"}, exitUsage},
		{"unknown relation", []string{"facts", "--workspace-root", workspace, "--relation", "no_such_relation"}, exitUsage},
	}
	for _, item := range cases {
		t.Run(item.name, func(t *testing.T) {
			err := run(context.Background(), item.args, io.Discard)
			if code := exitCode(err, io.Discard); code != item.want {
				t.Fatalf("exit code %d, want %d: %v", code, item.want, err)
			}
		})
	}
}

func TestFactsCommandPropagatesSourceLinksConfigurationErrors(t *testing.T) {
	workspace := t.TempDir()
	if err := os.WriteFile(filepath.Join(workspace, "source-links.json"), []byte(`{"version":2,"entries":[]}`), 0o644); err != nil {
		t.Fatal(err)
	}
	err := run(context.Background(), []string{
		"facts", "--workspace-root", workspace, "--source-links", "source-links.json",
	}, io.Discard)
	if code := exitCode(err, io.Discard); code != exitUsage {
		t.Fatalf("exit code %d, want %d: %v", code, exitUsage, err)
	}
	if err == nil || !strings.Contains(err.Error(), "invalid --source-links: unsupported version 2") {
		t.Fatalf("source-links config error was lost by the CLI: %v", err)
	}
}

func TestTypeScriptSettingsApplyConfigAndInvocationPrecedence(t *testing.T) {
	root := t.TempDir()
	config := `[workspace]
exclude = ["tmp/**"]
include = ["dist/**"]
sources = ["configured.d.ts"]

[producers.ts]
implementation = "lintai.typescript"
files = "walk"
source_links = "links.json"
`
	if err := os.WriteFile(filepath.Join(root, "lintai.toml"), []byte(config), 0o644); err != nil {
		t.Fatal(err)
	}
	settings, _, _, err := captureTypeScriptSettings(root, "", "", false, "", false, []string{"cli/**"}, []string{"cli.d.ts"})
	if err != nil {
		t.Fatal(err)
	}
	if len(settings) != 1 || settings[0].Files != typescript.FileScopeWalk || settings[0].SourceLinks != "links.json" ||
		!slices.Equal(settings[0].WorkspaceExcludes, []string{"tmp/**"}) || !slices.Equal(settings[0].Includes, []string{"dist/**"}) ||
		!slices.Equal(settings[0].Excludes, []string{"cli/**"}) || !slices.Equal(settings[0].Sources, []string{"configured.d.ts", "cli.d.ts"}) {
		t.Fatalf("configuration defaults or list appends were not applied: %+v", settings)
	}
	settings, _, _, err = captureTypeScriptSettings(root, "", "tsconfig", true, "", true, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if settings[0].Files != typescript.FileScopeTsconfig || settings[0].SourceLinks != "" {
		t.Fatalf("explicit scalar flags did not override file values, including clearing source_links: %+v", settings[0])
	}
	if err := os.WriteFile(filepath.Join(root, "lintai.toml"), []byte(config+`
[producers.catalog]
implementation = "lintai.typescript"
`), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, _, _, err := captureTypeScriptSettings(root, "", "walk", true, "", false, nil, nil); err == nil || !strings.Contains(err.Error(), "ambiguous") {
		t.Fatalf("scalar TypeScript flags were accepted with multiple configured instances: %v", err)
	}
	if _, _, _, err := captureTypeScriptSettings(root, "", "", false, "", true, nil, nil); err == nil || !strings.Contains(err.Error(), "ambiguous") {
		t.Fatalf("source-links flag was accepted with multiple configured instances: %v", err)
	}
	if err := os.Mkdir(filepath.Join(root, "cli"), 0o755); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"configured.d.ts", "cli.d.ts", "cli/ignored.ts", "ordinary.ts"} {
		if err := os.WriteFile(filepath.Join(root, name), []byte("export {};"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	var output bytes.Buffer
	if err := run(context.Background(), []string{
		"scope", "--explain", "--json", "--workspace-root", root, "--exclude", "cli/**", "--source", "cli.d.ts",
	}, &output); err != nil {
		t.Fatal(err)
	}
	var reports []struct {
		Producer string `json:"producer"`
		Scope    struct {
			Sources  []string `json:"sources"`
			Selected []struct {
				Path string `json:"path"`
			} `json:"selected"`
		} `json:"scope"`
	}
	if err := json.Unmarshal(output.Bytes(), &reports); err != nil {
		t.Fatal(err)
	}
	if len(reports) != 2 {
		t.Fatalf("got %d producer scopes, want 2", len(reports))
	}
	for _, report := range reports {
		selected := make([]string, 0, len(report.Scope.Selected))
		for _, file := range report.Scope.Selected {
			selected = append(selected, file.Path)
		}
		if !slices.Equal(report.Scope.Sources, []string{"cli.d.ts", "configured.d.ts"}) ||
			!slices.Equal(selected, []string{"cli.d.ts", "configured.d.ts", "ordinary.ts"}) {
			t.Fatalf("list additions were not applied to producer %s: %+v", report.Producer, report.Scope)
		}
	}
}

func TestSchemaRendersConfiguredProducerNamespaceWithoutCollection(t *testing.T) {
	root := t.TempDir()
	config := `[producers.app]
implementation = "lintai.typescript"
source_links = "missing-source-links.json"
`
	if err := os.WriteFile(filepath.Join(root, "lintai.toml"), []byte(config), 0o644); err != nil {
		t.Fatal(err)
	}

	var output bytes.Buffer
	if err := run(context.Background(), []string{"schema", "--workspace-root", root}, &output); err != nil {
		t.Fatalf("schema command failed: %v", err)
	}
	rendered := output.String()
	if !strings.Contains(rendered, "Package app!") || strings.Contains(rendered, "Package ts!") {
		t.Fatalf("schema did not use the configured producer namespace:\n%s", rendered)
	}
	if !strings.Contains(rendered, "Decl module(") || !strings.Contains(rendered, "Package lintai!") || !strings.Contains(rendered, "Decl glob(") {
		t.Fatalf("schema omitted producer relations or engine helpers:\n%s", rendered)
	}
}

func TestScopeExplainUsesConfiguredExactSourceSelection(t *testing.T) {
	root := t.TempDir()
	write := func(name, contents string) {
		t.Helper()
		path := filepath.Join(root, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(contents), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("lintai.toml", `[workspace]
exclude = ["src/extra.ts"]
sources = ["src/extra.ts"]

[producers.ts]
files = "tsconfig"
`)
	write("tsconfig.json", `{"compilerOptions":{"module":"preserve","moduleResolution":"bundler"},"files":["src/main.ts"]}`)
	write("src/main.ts", "export const main = 1;\n")
	write("src/extra.ts", "export const extra = 2;\n")
	write("src/loose.ts", "export const loose = 3;\n")
	for _, mode := range []string{"tsconfig", "walk"} {
		t.Run(mode, func(t *testing.T) {
			args := []string{"--explain", "--workspace-root", root, "--files", mode}
			var output bytes.Buffer
			if err := run(context.Background(), append([]string{"scope", "--json"}, args...), &output); err != nil {
				t.Fatal(err)
			}
			var reports []struct {
				Producer string                      `json:"producer"`
				Scope    typescript.ScopeExplanation `json:"scope"`
			}
			if err := json.Unmarshal(output.Bytes(), &reports); err != nil {
				t.Fatal(err)
			}
			if len(reports) != 1 || reports[0].Producer != "ts" {
				t.Fatalf("unexpected scope reports: %s", output.String())
			}
			scope := reports[0].Scope
			exactSelected := false
			for _, item := range scope.Selected {
				if item.Path == "src/extra.ts" && item.Exact {
					exactSelected = true
				}
			}
			eligible := []string{"src/extra.ts", "src/main.ts"}
			if mode == "walk" {
				eligible = []string{"src/extra.ts", "src/loose.ts", "src/main.ts"}
			}
			if !exactSelected || !slices.Equal(scope.ExactSources, []string{"src/extra.ts"}) ||
				!slices.Equal(scope.CompilerEligible, eligible) ||
				!slices.Equal(scope.Configurations, []string{"tsconfig.json"}) || len(scope.CompilerFiles) != 3 {
				t.Fatalf("unexpected scope: %s", output.String())
			}
			value := func(p *string) string {
				if p == nil {
					return ""
				}
				return *p
			}
			extra, loose, main := scope.CompilerFiles[0], scope.CompilerFiles[1], scope.CompilerFiles[2]
			if extra.Path != "src/extra.ts" || !extra.Eligible || value(extra.SelectedConfig) != "tsconfig.json" || value(extra.OwnerConfig) != "tsconfig.json" || value(extra.OwnerKind) != "configured" || extra.RootConfigs == nil || len(extra.RootConfigs) != 0 ||
				main.Path != "src/main.ts" || !main.Eligible || value(main.SelectedConfig) != "tsconfig.json" || value(main.OwnerKind) != "configured" || value(main.OwnerConfig) != "tsconfig.json" || !slices.Equal(main.RootConfigs, []string{"tsconfig.json"}) ||
				loose.Path != "src/loose.ts" || value(loose.SelectedConfig) != "tsconfig.json" || loose.OwnerConfig != nil || loose.RootConfigs == nil || len(loose.RootConfigs) != 0 || loose.Eligible != (mode == "walk") ||
				(mode == "walk" && value(loose.OwnerKind) != "synthetic") || (mode == "tsconfig" && loose.OwnerKind != nil) {
				t.Fatalf("unexpected compiler ownership: %s", output.String())
			}
			output.Reset()
			if err := run(context.Background(), append([]string{"scope"}, args...), &output); err != nil {
				t.Fatal(err)
			}
			looseText := "src/loose.ts: eligible=false selected=tsconfig.json owner=none (none) roots=[]"
			if mode == "walk" {
				looseText = "src/loose.ts: eligible=true selected=tsconfig.json owner=none (synthetic) roots=[]"
			}
			if !strings.Contains(output.String(), looseText) || !strings.Contains(output.String(), "src/main.ts: eligible=true selected=tsconfig.json owner=tsconfig.json (configured) roots=[tsconfig.json]") {
				t.Fatalf("text scope does not expose the JSON ownership: %s", output.String())
			}
			output.Reset()
			if err := run(context.Background(), []string{"facts", "--workspace-root", root, "--files", mode, "--relation", "ts.module_config", "--relation", "ts.module_owner_config"}, &output); err != nil {
				t.Fatal(err)
			}
			for _, row := range []string{
				`ts.module_config("ts:module:src/main.ts","tsconfig.json").`,
				`ts.module_owner_config("ts:module:src/main.ts","tsconfig.json").`,
				`ts.module_owner_config("ts:module:src/extra.ts","tsconfig.json").`,
			} {
				if !strings.Contains(output.String(), row) {
					t.Fatalf("scope ownership is missing from facts: %s", output.String())
				}
			}
			if strings.Contains(output.String(), `ts:module:src/loose.ts`) || strings.Contains(output.String(), `ts.module_config("ts:module:src/extra.ts"`) {
				t.Fatalf("scope facts invented a config membership or synthetic owner: %s", output.String())
			}
		})
	}
}

// Authoring mistakes give an error that names the cause. A helper module
// under the rule glob is not a rule and is skipped.
func TestAuthoringErrors(t *testing.T) {
	repoRoot, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	rulesDir := t.TempDir()
	rules := map[string]string{
		"no-version.ts": `import { dl, rule } from "@lintai/sdk";
export default rule("t.no-version").program(dl` + "`Decl v(F). v(F) :- ts.module(F).`" + `).report("v").message((row) => String(row.F));`,
		"negated-wildcard.ts": `import { dl, rule } from "@lintai/sdk";
export default rule("t.negated-wildcard").version(1).program(dl` + "`Decl v(F). v(F) :- ts.module(F), !ts.import_edge(_, F, _, \"server-only\").`" + `).report("v").message((row) => String(row.F));`,
		"unknown-column.ts": `import { dl, rule } from "@lintai/sdk";
export default rule("t.unknown-column").version(1).program(dl` + "`Decl v(F). v(F) :- ts.module(F).`" + `).report("v").message((row) => String(row.Nope));`,
		"expect-all.ts": `import { dl, rule } from "@lintai/sdk";
export default rule("t.expect-all").version(1).program(dl` + "`Decl a(M). a(M) :- ts.module_path(M, F), lintai.glob(F, \"none/**\"). Decl b(M). b(M) :- ts.module_path(M, F), lintai.glob(F, \"nada/**\").`" + `).report("a").expectNonEmpty("a", "b").message((row) => String(row.M));`,
	}
	for name, text := range rules {
		if err := os.WriteFile(filepath.Join(rulesDir, name), []byte(text), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	var stdout bytes.Buffer
	err = run(context.Background(), []string{
		"--json",
		"--workspace-root", testpack.Build(t, repoRoot, filepath.Join(repoRoot, "testdata/fixtures/workspace"), filepath.Join(rulesDir, "*.ts")),
	}, &stdout)
	if code := exitCode(err, io.Discard); code != exitRuleError {
		t.Fatalf("exit code %d, want %d: %v", code, exitRuleError, err)
	}
	var items []diagnostics.Diagnostic
	if err := json.Unmarshal(stdout.Bytes(), &items); err != nil {
		t.Fatal(err)
	}
	got := make(map[string]string)
	for _, item := range items {
		if item.DiagnosticKind != diagnostics.KindRuleExecutionError {
			t.Fatalf("unexpected violation: %+v", item)
		}
		got[item.RuleID] = item.Phase + ": " + item.Message
	}
	want := map[string]string{
		"t.no-version":       "rule t.no-version must call .version(n)",
		"t.negated-wildcard": "program_analysis: _ cannot be used in the negated atom",
		"t.unknown-column":   "message() read row.Nope, but the reported predicate has no argument Nope. Its arguments are: F",
		"t.expect-all":       "premise: expectNonEmpty: these predicates have no facts: a, b",
	}
	if len(got) != len(want) {
		t.Fatalf("got errors for %v, want %v", got, want)
	}
	for ruleID, prefix := range want {
		if !strings.Contains(got[ruleID], prefix) {
			t.Errorf("%s: got %q, want it to contain %q", ruleID, got[ruleID], prefix)
		}
	}
}

// List valid entries and report malformed entries and duplicate IDs in pack order.
func TestRulesCommand(t *testing.T) {
	repoRoot, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	workspace := t.TempDir()
	ruleSource := func(id string) string {
		return `import { dl, rule } from "@lintai/sdk";
export default rule(` + strconvQuote(id) + `).version(1).program(dl` + "`Decl v(M). v(M) :- ts.module_path(M, F), lintai.glob(F, \"src/**\").`" + `).report("v").message(() => "x");
`
	}
	files := map[string]string{
		"src/a.ts":                 "export const a = 1;\n",
		"lintai-rules/zeta.ts":     ruleSource("arch.zeta"),
		"lintai-rules/alpha.ts":    ruleSource("arch.alpha"),
		"lintai-rules/nested/b.ts": ruleSource("arch/nested.b"),
		"lintai-rules/metadata.ts": `import { rule } from "@lintai/sdk";
export default rule("arch.metadata").version(7)
	.facts(() => { throw new Error("facts callback ran"); })
	.program(() => { throw new Error("program callback ran"); })
	.report("reported_b", "reported_a")
	.expectNonEmpty("expected_b", "expected_a")
	.message(() => { throw new Error("message callback ran"); });
`,
		"lintai-rules/dup-one.ts": ruleSource("arch.dup"),
		"lintai-rules/dup-two.ts": ruleSource("arch.dup"),
		"lintai-rules/dup-config.ts": `import { dl, rule } from "@lintai/sdk";
export default rule("arch.dup-config").version(1).program(dl` + "`Decl v(M). v(M) :- ts.module_path(M, F), lintai.glob(F, \"src/**\").`" + `).report("v").message(() => "x");
`,
		"lintai-rules/dup-config-ok.ts":      ruleSource("arch.dup-config"),
		"lintai-rules/bad-id.ts":             "export default { id: \"Arch.Bad\", versionValue: 1 };\n",
		"lintai-rules/no-program.ts":         "export default { id: \"arch.no-program\", versionValue: 1 };\n",
		"lintai-rules/no-version.ts":         strings.Replace(ruleSource("arch.no-version"), ".version(1)", "", 1),
		"lintai-rules/fractional-version.ts": strings.Replace(ruleSource("arch.fractional-version"), ".version(1)", ".version(1.75)", 1),
	}
	for name, text := range files {
		path := filepath.Join(workspace, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(text), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	var stdout bytes.Buffer
	err = run(context.Background(), []string{"rules", "--json", "--workspace-root", testpack.Build(t, repoRoot, workspace, "lintai-rules/*.ts", "lintai-rules/nested/*.ts")}, &stdout)
	if code := exitCode(err, io.Discard); code != exitClean {
		t.Fatalf("exit code %d, want %d: %v", code, exitClean, err)
	}
	var list struct {
		Rules []struct {
			ID             string   `json:"id"`
			Path           string   `json:"path"`
			Version        int      `json:"version"`
			Report         []string `json:"report"`
			ExpectNonEmpty []string `json:"expectNonEmpty"`
		} `json:"rules"`
		Errors []struct{ Path, ID, Message string } `json:"errors"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &list); err != nil {
		t.Fatal(err)
	}
	gotRules := make([]string, 0, len(list.Rules))
	for _, item := range list.Rules {
		gotRules = append(gotRules, item.ID+" "+item.Path)
		if item.ExpectNonEmpty == nil {
			t.Errorf("%s metadata arrays must be JSON arrays, got report=%v expectNonEmpty=%v", item.ID, item.Report, item.ExpectNonEmpty)
		}
		if item.ID == "arch.metadata" {
			if item.Version != 7 || !slices.Equal(item.Report, []string{"reported_b", "reported_a"}) || !slices.Equal(item.ExpectNonEmpty, []string{"expected_b", "expected_a"}) {
				t.Errorf("loaded metadata differs: got version=%d report=%q expectNonEmpty=%q", item.Version, item.Report, item.ExpectNonEmpty)
			}
		} else if item.Version != 1 || !slices.Equal(item.Report, []string{"v"}) || len(item.ExpectNonEmpty) != 0 {
			t.Errorf("default metadata differs for %s: got version=%d report=%q expectNonEmpty=%q", item.ID, item.Version, item.Report, item.ExpectNonEmpty)
		}
	}
	wantRules := []string{
		"arch.alpha lintai-rules/alpha.ts",
		"arch.metadata lintai-rules/metadata.ts",
		"arch.zeta lintai-rules/zeta.ts",
		"arch/nested.b lintai-rules/nested/b.ts",
	}
	if !slices.Equal(gotRules, wantRules) {
		t.Fatalf("rules:\ngot  %q\nwant %q", gotRules, wantRules)
	}
	wantErrors := []struct{ path, id, message string }{
		{"lintai-rules/bad-id.ts", "", `rule id "Arch.Bad" is not valid`},
		{"lintai-rules/dup-config-ok.ts", "arch.dup-config", `rule id "arch.dup-config" is used by more than one rule file`},
		{"lintai-rules/dup-config.ts", "arch.dup-config", `rule id "arch.dup-config" is used by more than one rule file`},
		{"lintai-rules/dup-one.ts", "arch.dup", `rule id "arch.dup" is used by more than one rule file (lintai-rules/dup-one.ts, lintai-rules/dup-two.ts)`},
		{"lintai-rules/dup-two.ts", "arch.dup", `rule id "arch.dup" is used by more than one rule file`},
		{"lintai-rules/fractional-version.ts", "arch.fractional-version", "must call .version(n) with a positive integer"},
		{"lintai-rules/no-program.ts", "arch.no-program", "rule is missing program()"},
		{"lintai-rules/no-version.ts", "arch.no-version", "must call .version(n) with a positive integer"},
	}
	if len(list.Errors) != len(wantErrors) {
		t.Fatalf("errors: got %+v", list.Errors)
	}
	for index, want := range wantErrors {
		got := list.Errors[index]
		if got.Path != want.path || got.ID != want.id || !strings.Contains(got.Message, want.message) {
			t.Errorf("error %d: got %+v, want path %s, id %q and a message with %q", index, got, want.path, want.id, want.message)
		}
	}

	stdout.Reset()
	err = run(context.Background(), []string{"rules", "--workspace-root", testpack.Build(t, repoRoot, workspace, "lintai-rules/alpha.ts", "lintai-rules/metadata.ts")}, &stdout)
	if code := exitCode(err, io.Discard); code != exitClean {
		t.Fatalf("text listing exit code %d, want %d: %v", code, exitClean, err)
	}
	if got, want := stdout.String(), "arch.alpha lintai-rules/alpha.ts\narch.metadata lintai-rules/metadata.ts\n"; got != want {
		t.Fatalf("text listing:\ngot  %q\nwant %q", got, want)
	}

	stdout.Reset()
	err = run(context.Background(), []string{"--json", "--workspace-root", testpack.Build(t, repoRoot, workspace, "lintai-rules/alpha.ts", "lintai-rules/dup-*.ts", "lintai-rules/bad-id.ts")}, &stdout)
	if code := exitCode(err, io.Discard); code != exitRuleError {
		t.Fatalf("exit code %d, want %d: %v", code, exitRuleError, err)
	}
	var items []diagnostics.Diagnostic
	if err := json.Unmarshal(stdout.Bytes(), &items); err != nil {
		t.Fatal(err)
	}
	got := make([]string, 0, len(items))
	for _, item := range items {
		got = append(got, item.RuleID+" "+string(item.DiagnosticKind)+" "+item.Phase)
	}
	slices.Sort(got)
	want := []string{
		"Arch.Bad rule_execution_error load",
		"arch.alpha architecture_violation ",
		"arch.dup rule_execution_error config",
		"arch.dup rule_execution_error config",
		"arch.dup-config rule_execution_error config",
		"arch.dup-config rule_execution_error config",
	}
	if !slices.Equal(got, want) {
		t.Fatalf("normal run:\ngot  %q\nwant %q", got, want)
	}
}

func strconvQuote(value string) string {
	encoded, _ := json.Marshal(value)
	return string(encoded)
}

func names(result engine.FilesResult) []string {
	files := make([]string, len(result.Files))
	for i, group := range result.Files {
		files[i] = group.File
	}
	return files
}
