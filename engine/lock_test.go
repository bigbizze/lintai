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
	opts := Options{WorkspaceRoot: root, RulesLock: DefaultRulesLockFile}
	session := openTestSession(t, opts)
	defer session.Close()
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
	lock, err := session.BuildRulesLock(ctx, opts)
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
	wrongTarget := opts
	wrongTarget.RulesLock = "lintai.toml"
	if _, err := session.BuildRulesLock(ctx, wrongTarget); err == nil || !strings.Contains(err.Error(), "not a lintai rules lock") {
		t.Fatalf("unrelated lock target: %v", err)
	}
	if data, _ := os.ReadFile(filepath.Join(root, "lintai.toml")); string(data) != unrelated {
		t.Fatalf("unrelated file changed: %q", data)
	}
	writeTestFile(t, root, DefaultRulesLockFile, `{"format":"lintai.rules-lock/4"}`)
	if relock, err := session.BuildRulesLock(ctx, opts); err != nil {
		t.Fatalf("obsolete lock reapproval: %v", err)
	} else if err := WriteRulesLock(ctx, lockPath, relock); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name   string
		change func(*Options, *rulepack.Pack)
	}{
		{"altered bytes", func(_ *Options, p *rulepack.Pack) { p.Entries[0].Code += " " }},
		{"entry name", func(_ *Options, p *rulepack.Pack) { p.Entries[0].Entry = "other.js" }},
		{"added", func(_ *Options, p *rulepack.Pack) {
			p.Entries = append(p.Entries, rulepack.Entry{Entry: "z.js", Code: "throw 1"})
		}},
		{"removed", func(_ *Options, p *rulepack.Pack) { p.Entries = []rulepack.Entry{} }},
		{"unsupported ABI", func(_ *Options, p *rulepack.Pack) { p.ABI = "unknown" }},
		{"options", func(o *Options, _ *rulepack.Pack) {
			o.Rule = map[string]RulePolicy{"test": {Options: map[string]any{"k": []any{}}}}
		}},
		{"severity", func(o *Options, _ *rulepack.Pack) { o.Rule = map[string]RulePolicy{"test": {Severity: "off"}} }},
		{"allowance count", func(_ *Options, _ *rulepack.Pack) { saveAllowances(3, "original") }},
		{"allowance reason", func(_ *Options, _ *rulepack.Pack) { saveAllowances(1, "different") }},
		{"missing lock", func(_ *Options, _ *rulepack.Pack) {
			if err := os.Remove(lockPath); err != nil {
				t.Fatal(err)
			}
		}},
		{"ambiguous lock", func(_ *Options, _ *rulepack.Pack) {
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
			result, err := session.Check(ctx, request)
			if err != nil || len(result.Diagnostics) != 1 || result.Diagnostics[0].Phase != "integrity" || session.stats.gojaRuntimes.Load() != 0 {
				t.Fatalf("rejected check: %+v, %v, VMs %d", result, err, session.stats.gojaRuntimes.Load())
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
