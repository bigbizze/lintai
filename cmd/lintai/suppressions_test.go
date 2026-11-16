package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/bigbizze/lintai/diagnostics"
	"github.com/bigbizze/lintai/suppressions"
)

func suppressionFixture(t *testing.T, files map[string]string) (string, string, func(...string) (int, []diagnostics.Diagnostic)) {
	t.Helper()
	repo, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	workspace := t.TempDir()
	writeFiles(t, workspace, files)
	check := func(flags ...string) (int, []diagnostics.Diagnostic) {
		t.Helper()
		args := []string{"--json", "--repo-root", repo, "--workspace-root", workspace}
		return runCode(t, append(args, flags...))
	}
	return repo, workspace, check
}
func requireCode(t *testing.T, check func(...string) (int, []diagnostics.Diagnostic), want int, flags ...string) []diagnostics.Diagnostic {
	t.Helper()
	code, items := check(flags...)
	if code != want {
		t.Fatalf("%v: exit %d want %d: %+v", flags, code, want, items)
	}
	return items
}

// V25, V32, V34, V35, V38: configured rule selection, real TypeScript keys,
// null-key counted duplicates, independent assertions, excluded-file prune
// coverage and whole-group fail-closed matching.
func TestSuppressionTrackingAndCounts(t *testing.T) {
	rule := `import { dl, rule } from '@lintai/sdk';
export default rule('sample').version(1).program(dl`
	rule += "`" + `Decl named(Fn). Decl calls(Site). Decl other(Site).
named(Fn) :- ts.fn_async(Fn). calls(Site) :- ts.call_site(Site), ts.site(Site, File), lintai.glob(File, "src/**"). other(Site) :- ts.call_site(Site), ts.site(Site, File), lintai.glob(File, "src/**").` + "`" + `).report('named','calls','other').message(() => 'violation');`
	_, workspace, runCheck := suppressionFixture(t, map[string]string{"src/a.ts": "declare function work(): void;\nexport async function first() { work(); work(); }\n", "policy/sample.ts": rule})
	check := func(flags ...string) (int, []diagnostics.Diagnostic) {
		return runCheck(append([]string{"--rules", "policy/**/*.ts"}, flags...)...)
	}
	requireCode(t, check, exitClean, "--suppress-all")
	file, err := suppressions.Load(filepath.Join(workspace, suppressions.DefaultFile))
	if err != nil {
		t.Fatal(err)
	}
	if len(file) != 3 {
		t.Fatalf("assertions did not get separate entries: %+v", file)
	}
	writeFiles(t, workspace, map[string]string{"src/a.ts": "declare function work(): void;\nexport function first() { work(); work(); }\n"})
	requireCode(t, check, exitViolations, "--suppress-rule", "sample")
	retained, err := suppressions.Load(filepath.Join(workspace, suppressions.DefaultFile))
	if err != nil {
		t.Fatal(err)
	}
	foundNamed := false
	for _, entry := range retained {
		foundNamed = foundNamed || entry.Assertion.Name == "named"
	}
	if !foundNamed {
		t.Fatal("re-recording removed an entry with no findings")
	}
	writeFiles(t, workspace, map[string]string{"src/a.ts": "declare function work(): void;\nexport async function first() { const x = 3; work(); work(); }\n"})
	requireCode(t, check, exitClean)
	writeFiles(t, workspace, map[string]string{"src/a.ts": "declare function work(): void; declare function alternate(): void;\nexport async function first() { work(); alternate(); }\n"})
	requireCode(t, check, exitClean)
	writeFiles(t, workspace, map[string]string{"src/a.ts": "declare function work(): void;\nexport async function first() { work(); }\n"})
	items := requireCode(t, check, exitViolations)
	if len(items) != 2 {
		t.Fatalf("want two unused counts: %+v", items)
	}
	path := filepath.Join(workspace, suppressions.DefaultFile)
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	requireCode(t, check, exitClean, "--exclude", "src/a.ts", "--prune-suppressions")
	after, err := os.ReadFile(path)
	if err != nil || !bytes.Equal(after, before) {
		t.Fatalf("excluded file granted prune authority: %v", err)
	}
	requireCode(t, check, exitClean, "--prune-suppressions")
	writeFiles(t, workspace, map[string]string{"src/a.ts": "declare function work(): void;\nexport async function first() { work(); work(); work(); }\n"})
	items = requireCode(t, check, exitViolations)
	if len(items) != 6 {
		t.Fatalf("both over-limit groups must report all findings: %+v", items)
	}
	writeFiles(t, workspace, map[string]string{"src/a.ts": "declare function work(): void;\nexport async function renamed() { work(); }\n"})
	items = requireCode(t, check, exitViolations)
	if len(items) != 2 {
		t.Fatalf("renamed function should reopen and leave unused approval: %+v", items)
	}
}

// V35, V37, V38, V39: saved reason policy mismatch, replacement, preserved
// entry failure, and approval before an atomic suppression write.
func TestSuppressionReasonsReplacementAndLock(t *testing.T) {
	makeRule := func(id, column, reason string) string {
		return "import { dl, rule } from '@lintai/sdk';\nexport default rule('" + id + "').version(1).program(dl`Decl v(Fn, " + column + "). v(Fn, /" + reason + ") :- ts.fn_async(Fn).`).report('v').suppression('v', { by: ['" + column + "'] }).message(() => 'violation');\n"
	}
	repo, workspace, check := suppressionFixture(t, map[string]string{"src/a.ts": "export async function first() {}\n", "lintai-rules/a.ts": makeRule("a", "Reason", "clock"), "lintai-rules/b.ts": makeRule("b", "Reason", "clock")})
	requireCode(t, check, exitClean, "--suppress-all")
	path := filepath.Join(workspace, suppressions.DefaultFile)
	original, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	writeFiles(t, workspace, map[string]string{"lintai-rules/a.ts": makeRule("a", "Why", "clock")})
	requireCode(t, check, exitUsage)
	requireCode(t, check, exitUsage, "--suppress-rule", "b")
	after, err := os.ReadFile(path)
	if err != nil || !bytes.Equal(after, original) {
		t.Fatalf("preserved mismatch changed file: %v", err)
	}
	requireCode(t, check, exitClean, "--suppress-rule", "a")
	requireCode(t, check, exitClean)
	encoded, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var saved map[string]any
	if err := json.Unmarshal(encoded, &saved); err != nil {
		t.Fatal(err)
	}
	for _, raw := range saved["entries"].([]any) {
		entry := raw.(map[string]any)
		if entry["rule"] != "a" {
			continue
		}
		group := entry["groups"].([]any)[0].(map[string]any)
		reason := group["reasons"].([]any)[0].([]any)
		reason[1].([]any)[0] = "unsupported"
	}
	encoded, err = json.Marshal(saved)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, encoded, 0o644); err != nil {
		t.Fatal(err)
	}
	requireCode(t, check, exitUsage)
	requireCode(t, check, exitClean, "--suppress-rule", "a")
	writeFiles(t, workspace, map[string]string{"lintai-rules/a.ts": makeRule("a", "Why", "network")})
	requireCode(t, check, exitViolations)
	requireCode(t, check, exitClean, "--suppress-rule", "a")
	code, _ := runCode(t, []string{"lock", "--repo-root", repo, "--workspace-root", workspace})
	if code != exitClean {
		t.Fatalf("lock exit %d", code)
	}
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	writeFiles(t, workspace, map[string]string{"src/a.ts": "export async function first() {}\nexport async function second() {}\n"})
	requireCode(t, check, exitRuleError, "--rules-lock", "lintai-rules.lock.json", "--suppress-all")
	after, err = os.ReadFile(path)
	if err != nil || !bytes.Equal(after, before) {
		t.Fatalf("unapproved proposal changed file: %v", err)
	}
	requireCode(t, check, exitClean, "--suppress-all")
	code, _ = runCode(t, []string{"lock", "--repo-root", repo, "--workspace-root", workspace})
	if code != exitClean {
		t.Fatalf("lock exit %d", code)
	}
	requireCode(t, check, exitClean, "--rules-lock", "lintai-rules.lock.json")
}

// V32, V33: literal import subject keys survive binding edits but reopen
// when the specifier changes, through the complete reporting path.
func TestSuppressionImportSubject(t *testing.T) {
	rule := "import { dl, rule } from '@lintai/sdk';\nexport default rule('imports').version(1).program(dl`Decl v(Site). v(Site) :- ts.import_form(Site, /static), ts.site(Site, File), lintai.glob(File, \"src/**\").`).report('v').message(() => 'import');\n"
	_, workspace, check := suppressionFixture(t, map[string]string{"src/a.ts": `import { one, two } from "example-package";` + "\n", "lintai-rules/imports.ts": rule})
	requireCode(t, check, exitClean, "--suppress-all")
	writeFiles(t, workspace, map[string]string{"src/a.ts": `import { two, one } from "example-package";` + "\n"})
	requireCode(t, check, exitClean)
	writeFiles(t, workspace, map[string]string{"src/a.ts": `import { two, one } from "other-package";` + "\n"})
	items := requireCode(t, check, exitViolations)
	if len(items) != 2 {
		t.Fatalf("changed import did not reopen: %+v", items)
	}
}
