package engine

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/bigbizze/lintai/internal/rulepack"
	"github.com/bigbizze/lintai/suppressions"
)

func TestLockedChecksRejectChangesBeforeAnyVM(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	opts := Config{WorkspaceRoot: root, RulesLock: DefaultRulesLockFile}
	session := openTestSession(t, opts)
	code := "var LintAIPureModule = {default:{id:'test',versionValue:1,programValue:{__lintaiKind:'fragment',text:'Decl v(M).'},reportPredicates:['v'],messageFn:()=>'test'}};"
	pack := rulepack.Pack{Format: rulepack.Format, ABI: rulepack.ABI, Entries: []rulepack.Entry{{Entry: "rules/test.js", Code: code}}}
	savePack := func(pack rulepack.Pack) {
		data, err := json.Marshal(pack)
		if err != nil {
			t.Fatal(err)
		}
		writeTestFile(t, root, rulepack.Path, string(data))
	}
	allowance := func(file string, count int, reason string) suppressions.Entry {
		return suppressions.Entry{File: file, Rule: "test", Assertion: suppressions.Assertion{Name: "v", Arity: 2},
			Groups: []suppressions.Group{{Namespace: "ts", Count: count, Description: "existing", Reasons: []suppressions.Reason{{Name: "Reason", Value: [2]string{"s", reason}}}}}}
	}
	saveEntries := func(file ...suppressions.Entry) {
		data, err := suppressions.Encode(file)
		if err != nil {
			t.Fatal(err)
		}
		writeTestFile(t, root, suppressions.DefaultFile, string(data))
	}
	saveAllowances := func(count int, reason string) { saveEntries(allowance("src/a.ts", count, reason)) }
	physical, err := filepath.EvalSymlinks(root)
	if err != nil {
		t.Fatal(err)
	}
	absolute := filepath.ToSlash(filepath.Join(physical, "src", "a.ts"))
	savePack(pack)
	// A target is compared by the file it names, before the file exists.
	if err := os.Symlink(root, filepath.Join(root, "linked")); err != nil {
		t.Skip(err)
	}
	if _, err := session.ApproveRules(ctx, filepath.Join("linked", suppressions.DefaultFile)); err == nil || !strings.Contains(err.Error(), "conflicts") {
		t.Fatalf("lock target naming the suppressions file: %v", err)
	}
	// A dangling link in a reserved location is rejected before its target can
	// be used as the lock destination, even when the target differs by case.
	if err := os.Symlink("SHARED.json", filepath.Join(root, suppressions.DefaultFile)); err != nil {
		t.Fatal(err)
	}
	if _, err := session.ApproveRules(ctx, "shared.json"); err == nil || !strings.Contains(err.Error(), suppressions.DefaultFile) || !strings.Contains(err.Error(), "dangling symlink") {
		t.Fatalf("dangling suppressions location: %v", err)
	}
	if _, err := os.Lstat(filepath.Join(root, "shared.json")); !os.IsNotExist(err) {
		t.Fatalf("lock target created behind a dangling suppressions link: %v", err)
	}
	if err := os.Remove(filepath.Join(root, suppressions.DefaultFile)); err != nil {
		t.Fatal(err)
	}
	saveAllowances(2, "original")
	if _, err := session.ApproveRules(ctx, ""); err != nil {
		t.Fatal(err)
	}
	// A link in the target path that moves before the write does not move it.
	for _, dir := range []string{"a", "b"} {
		if err := os.Mkdir(filepath.Join(root, dir), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	destination := filepath.Join(root, "destination")
	if err := os.Symlink(filepath.Join(root, "a"), destination); err != nil {
		t.Fatal(err)
	}
	session.hooks.beforeWrite = func() {
		if err := os.Remove(destination); err != nil {
			t.Error(err)
		}
		if err := os.Symlink(filepath.Join(root, "b"), destination); err != nil {
			t.Error(err)
		}
	}
	if _, err := session.ApproveRules(ctx, filepath.Join("destination", "new", DefaultRulesLockFile)); err != nil {
		t.Fatal(err)
	}
	session.hooks.beforeWrite = nil
	if _, err := ReadRulesLock(filepath.Join(root, "a", "new", DefaultRulesLockFile)); err != nil {
		t.Fatalf("lock not written through the bound link: %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, "b", "new")); !os.IsNotExist(err) {
		t.Fatalf("lock written through the moved link: %v", err)
	}
	lockPath := filepath.Join(root, DefaultRulesLockFile)
	approved, err := os.ReadFile(lockPath)
	if err != nil {
		t.Fatal(err)
	}
	unrelated := "[workspace]\nsources = [\"src/**\"]\n"
	writeTestFile(t, root, "lintai.toml", unrelated)
	if _, err := session.ApproveRules(ctx, "lintai.toml"); err == nil || !strings.Contains(err.Error(), "not a lintai rules lock") {
		t.Fatalf("unrelated lock target: %v", err)
	}
	if data, _ := os.ReadFile(filepath.Join(root, "lintai.toml")); string(data) != unrelated {
		t.Fatalf("unrelated file changed: %q", data)
	}
	policy := `{"version":1,"rules":[{"id":"approval","enabled":true}],"owner":"operations"}`
	writeTestFile(t, root, "app-policy.json", policy)
	if _, err := session.ApproveRules(ctx, "app-policy.json"); err == nil || !strings.Contains(err.Error(), "not a lintai rules lock") {
		t.Fatalf("application policy lock target: %v", err)
	}
	if data, _ := os.ReadFile(filepath.Join(root, "app-policy.json")); string(data) != policy {
		t.Fatalf("application policy changed: %q", data)
	}
	writeTestFile(t, root, DefaultRulesLockFile, `{"format":"lintai.rules-lock/4"}`)
	if _, err := session.ApproveRules(ctx, ""); err != nil {
		t.Fatalf("obsolete lock reapproval: %v", err)
	}
	for _, tc := range []struct {
		name   string
		change func(*Config, *rulepack.Pack)
	}{
		{"altered bytes", func(_ *Config, p *rulepack.Pack) { p.Entries[0].Code += " " }},
		{"entry name", func(_ *Config, p *rulepack.Pack) { p.Entries[0].Entry = "other.js" }},
		{"added", func(_ *Config, p *rulepack.Pack) {
			p.Entries = append(p.Entries, rulepack.Entry{Entry: "z.js", Code: "throw 1"})
		}},
		{"removed", func(_ *Config, p *rulepack.Pack) { p.Entries = []rulepack.Entry{} }},
		{"unsupported ABI", func(_ *Config, p *rulepack.Pack) { p.ABI = "unknown" }},
		{"options", func(o *Config, _ *rulepack.Pack) {
			o.Rule = map[string]RulePolicy{"test": {Options: map[string]any{"k": []any{}}}}
		}},
		{"severity", func(o *Config, _ *rulepack.Pack) { o.Rule = map[string]RulePolicy{"test": {Severity: "off"}} }},
		{"allowance count", func(_ *Config, _ *rulepack.Pack) { saveAllowances(3, "original") }},
		{"equivalent allowance count", func(_ *Config, _ *rulepack.Pack) { saveEntries(allowance(absolute, 3, "original")) }},
		{"allowance reason", func(_ *Config, _ *rulepack.Pack) { saveAllowances(1, "different") }},
		{"missing lock", func(_ *Config, _ *rulepack.Pack) {
			if err := os.Remove(lockPath); err != nil {
				t.Fatal(err)
			}
		}},
		{"ambiguous lock", func(_ *Config, _ *rulepack.Pack) {
			writeTestFile(t, root, DefaultRulesLockFile, strings.Replace(string(approved), "{", "{\"abi\":\"x\",", 1))
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			writeTestFile(t, root, DefaultRulesLockFile, string(approved))
			saveAllowances(2, "original")
			changed := pack
			changed.Entries = append([]rulepack.Entry{}, pack.Entries...)
			request := opts
			tc.change(&request, &changed)
			savePack(changed)
			result, err := openTestSession(t, request).Check(ctx)
			if err != nil || len(result.Diagnostics) != 1 || result.Diagnostics[0].Phase != "integrity" || result.RulesLoaded != 0 {
				t.Fatalf("rejected check: %+v, %v", result, err)
			}
		})
	}
	writeTestFile(t, root, DefaultRulesLockFile, string(approved))
	savePack(pack)
	saveAllowances(1, "original")
	result, err := session.Check(ctx)
	if err != nil || len(result.Diagnostics) != 0 || result.RulesLoaded != 1 {
		t.Fatalf("tightened allowance: %+v %v", result, err)
	}
	// The equivalent absolute spelling has the approved count of the relative
	// one, and approval saves the relative spelling.
	saveEntries(allowance(absolute, 2, "original"))
	result, err = session.Check(ctx)
	if err != nil || len(result.Diagnostics) != 0 || result.RulesLoaded != 1 {
		t.Fatalf("equivalent allowance: %+v %v", result, err)
	}
	externalLock := filepath.Join(t.TempDir(), "respelled.lock.json")
	if _, err := session.ApproveRules(ctx, externalLock); err != nil {
		t.Fatal(err)
	}
	if lock, err := ReadRulesLock(externalLock); err != nil || len(lock.Suppressions) != 1 || lock.Suppressions[0].File != "src/a.ts" {
		t.Fatalf("approval did not save the relative spelling: %+v %v", lock, err)
	}
	lock, err := ReadRulesLock(lockPath)
	if err != nil {
		t.Fatal(err)
	}
	lock.Suppressions[0].File = absolute
	respelled, err := encodeRulesLock(lockPath, lock)
	if err != nil {
		t.Fatal(err)
	}
	writeTestFile(t, root, DefaultRulesLockFile, string(respelled))
	saveAllowances(2, "original")
	result, err = session.Check(ctx)
	if err != nil || len(result.Diagnostics) != 0 || result.RulesLoaded != 1 {
		t.Fatalf("equivalent spelling in approval: %+v %v", result, err)
	}
	// Equivalent duplicate entries are an invalid policy in the suppressions
	// file and in the lock, so their counts never combine.
	saveEntries(allowance("src/a.ts", 1, "original"), allowance(absolute, 1, "original"))
	result, err = session.Check(ctx)
	if err != nil || len(result.Diagnostics) != 1 || result.Diagnostics[0].Phase != "integrity" || !strings.Contains(result.Diagnostics[0].Message, "invalid policy") || result.RulesLoaded != 0 {
		t.Fatalf("duplicate equivalent allowances: %+v %v", result, err)
	}
	lock, err = ReadRulesLock(lockPath)
	if err != nil {
		t.Fatal(err)
	}
	lock.Suppressions = suppressions.File{allowance("src/a.ts", 1, "original"), allowance(absolute, 1, "original")}
	duplicated, err := encodeRulesLock(lockPath, lock)
	if err != nil {
		t.Fatal(err)
	}
	writeTestFile(t, root, DefaultRulesLockFile, string(duplicated))
	saveAllowances(2, "original")
	result, err = session.Check(ctx)
	if err != nil || len(result.Diagnostics) != 1 || result.Diagnostics[0].Phase != "integrity" || !strings.Contains(result.Diagnostics[0].Message, "invalid policy") || result.RulesLoaded != 0 {
		t.Fatalf("duplicate equivalent approvals: %+v %v", result, err)
	}
	writeTestFile(t, root, DefaultRulesLockFile, string(approved))
	if err := os.Remove(filepath.Join(root, rulepack.Path)); err != nil {
		t.Fatal(err)
	}
	result, err = session.Check(ctx)
	if err != nil || len(result.Diagnostics) != 1 || result.Diagnostics[0].Phase != "integrity" || !strings.Contains(result.Diagnostics[0].Message, "lintai build-rules") {
		t.Fatalf("missing pack: %+v %v", result, err)
	}
}
