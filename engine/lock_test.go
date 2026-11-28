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
	saveAllowances := func(count int, reason string) {
		file := suppressions.File{{File: "src/a.ts", Rule: "test", Assertion: suppressions.Assertion{Name: "v", Arity: 2},
			Groups: []suppressions.Group{{Namespace: "ts", Count: count, Description: "existing", Reasons: []suppressions.Reason{{Name: "Reason", Value: [2]string{"s", reason}}}}}}}
		data, err := suppressions.Encode(file)
		if err != nil {
			t.Fatal(err)
		}
		writeTestFile(t, root, suppressions.DefaultFile, string(data))
	}
	savePack(pack)
	saveAllowances(2, "original")
	lock, err := session.BuildRulesLock(ctx, "")
	if err != nil {
		t.Fatal(err)
	}
	lockPath := filepath.Join(root, DefaultRulesLockFile)
	if err := WriteRulesLock(ctx, lockPath, lock); err != nil {
		t.Fatal(err)
	}
	approved, err := os.ReadFile(lockPath)
	if err != nil {
		t.Fatal(err)
	}
	unrelated := "[workspace]\nsources = [\"src/**\"]\n"
	writeTestFile(t, root, "lintai.toml", unrelated)
	if _, err := session.BuildRulesLock(ctx, "lintai.toml"); err == nil || !strings.Contains(err.Error(), "not a lintai rules lock") {
		t.Fatalf("unrelated lock target: %v", err)
	}
	if data, _ := os.ReadFile(filepath.Join(root, "lintai.toml")); string(data) != unrelated {
		t.Fatalf("unrelated file changed: %q", data)
	}
	policy := `{"version":1,"rules":[{"id":"approval","enabled":true}],"owner":"operations"}`
	writeTestFile(t, root, "app-policy.json", policy)
	if _, err := session.BuildRulesLock(ctx, "app-policy.json"); err == nil || !strings.Contains(err.Error(), "not a lintai rules lock") {
		t.Fatalf("application policy lock target: %v", err)
	}
	if data, _ := os.ReadFile(filepath.Join(root, "app-policy.json")); string(data) != policy {
		t.Fatalf("application policy changed: %q", data)
	}
	writeTestFile(t, root, DefaultRulesLockFile, `{"format":"lintai.rules-lock/4"}`)
	if relock, err := session.BuildRulesLock(ctx, ""); err != nil {
		t.Fatalf("obsolete lock reapproval: %v", err)
	} else if err := WriteRulesLock(ctx, lockPath, relock); err != nil {
		t.Fatal(err)
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
	if err := os.Remove(filepath.Join(root, rulepack.Path)); err != nil {
		t.Fatal(err)
	}
	result, err = session.Check(ctx)
	if err != nil || len(result.Diagnostics) != 1 || result.Diagnostics[0].Phase != "integrity" || !strings.Contains(result.Diagnostics[0].Message, "lintai build-rules") {
		t.Fatalf("missing pack: %+v %v", result, err)
	}
}
