package suppressions

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/bigbizze/lintai/diagnostics"
)

func finding(entity string) diagnostics.Diagnostic {
	return diagnostics.Diagnostic{RuleID: "rule", DiagnosticKind: diagnostics.KindArchitectureViolation, Severity: diagnostics.SeverityError, SourceLocation: &diagnostics.SourceLocation{File: "src/a.ts"}, EntityIdentity: &diagnostics.EntityIdentity{SemanticKey: entity}}
}

func scopeForTest() Scope {
	return Scope{Files: map[string]struct{}{"src/a.ts": {}}, Rules: map[string]struct{}{"rule": {}}, KnownRules: map[string]struct{}{"rule": {}}, AllRulesLoaded: true}
}

func TestValidation(t *testing.T) {
	for _, content := range []string{
		`{`, `null`, `[]`, `{"src/a.ts":null}`,
		`{"src/a.ts":{"rule":{"count":0}}}`,
		`{"src/a.ts":{"rule":{"count":-1}}}`,
		`{"src/a.ts":{"rule":{"count":1.5}}}`,
		`{"src/a.ts":{"rule":{"count":1,"unknown":true}}}`,
		`{"src/a.ts":{"rule":{"count":2,"entities":{"a":1}}}}`,
		`{"src/a.ts":{"rule":{"count":1,"entities":{"a":0}}}}`,
		`{"src/a.ts":{"rule":{"count":1,"entities":{"":1}}}}`,
		`{"src/a.ts":{"rule":{"count":1,"entities":{}}}}`,
		`{"src/a.ts":{"rule":{"count":1,"entities":null}}}`,
		`{} {}`,
	} {
		t.Run(content, func(t *testing.T) {
			if _, err := Parse("counts.json", []byte(content)); err == nil {
				t.Fatal("accepted invalid suppressions")
			}
		})
	}
}

func TestCountAndEntityMatching(t *testing.T) {
	for _, test := range []struct {
		name                         string
		entry                        Entry
		keys                         []string
		reported, suppressed, unused int
	}{
		{"equal", Entry{Count: 2}, []string{"a", "b"}, 0, 2, 0},
		{"below", Entry{Count: 2}, []string{"a"}, 0, 1, 1},
		{"above", Entry{Count: 1}, []string{"a", "b"}, 2, 0, 0},
		{"swap", Entry{Count: 1, Entities: map[string]int{"old": 1}}, []string{"new"}, 1, 0, 1},
		{"entity excess", Entry{Count: 2, Entities: map[string]int{"a": 1, "b": 1}}, []string{"a", "a", "b"}, 2, 1, 0},
		{"unlisted", Entry{Count: 1, Entities: map[string]int{"a": 1}}, []string{"a", "b"}, 1, 1, 0},
		{"empty key", Entry{Count: 1, Entities: map[string]int{"a": 1}}, []string{""}, 1, 0, 1},
	} {
		t.Run(test.name, func(t *testing.T) {
			items := make([]diagnostics.Diagnostic, 0, len(test.keys))
			for _, key := range test.keys {
				items = append(items, finding(key))
			}
			result := Apply(File{"src/a.ts": {"rule": test.entry}}, items, scopeForTest())
			if len(result.Reported) != test.reported || len(result.Suppressed) != test.suppressed || len(result.Unused) != test.unused {
				t.Fatalf("unexpected result: %+v", result)
			}
		})
	}
}

func TestNeverSuppressNonCandidates(t *testing.T) {
	warning, failure, unlocated := finding("a"), finding("a"), finding("a")
	warning.Severity = diagnostics.SeverityWarning
	failure.DiagnosticKind = diagnostics.KindRuleExecutionError
	unlocated.SourceLocation = nil
	items := []diagnostics.Diagnostic{warning, failure, unlocated}
	result := Apply(File{"src/a.ts": {"rule": {Count: 100}}}, items, scopeForTest())
	if !reflect.DeepEqual(result.Reported, items) {
		t.Fatalf("noncandidates suppressed: %+v", result)
	}
}

func TestScopeAndPruning(t *testing.T) {
	file := File{"src/a.ts": {"rule": {Count: 3, Entities: map[string]int{"a": 2, "b": 1}}, "unknown": {Count: 1}, "warning": {Count: 1}}, "excluded.ts": {"rule": {Count: 5}}}
	scope := scopeForTest()
	scope.KnownRules["warning"] = struct{}{}
	pruned := Prune(file, []diagnostics.Diagnostic{finding("a")}, scope, nil)
	want := File{"src/a.ts": {"rule": {Count: 1, Entities: map[string]int{"a": 1}}, "warning": {Count: 1}}, "excluded.ts": {"rule": {Count: 5}}}
	if !reflect.DeepEqual(pruned, want) {
		t.Fatalf("got %+v want %+v", pruned, want)
	}
	if file["src/a.ts"]["rule"].Count != 3 {
		t.Fatal("prune mutated input")
	}
	scope.AllRulesLoaded = false
	scope.Rules = nil
	if unused := Apply(file, nil, scope).Unused; len(unused) != 0 {
		t.Fatalf("failed rules or files treated as unused: %+v", unused)
	}
}

func TestRecordAndStableWrite(t *testing.T) {
	file := File{"other.ts": {"other": {Count: 3}}}
	recorded := Record(file, []diagnostics.Diagnostic{finding("b"), finding("a"), finding("a")}, nil)
	if recorded["src/a.ts"]["rule"].Entities["a"] != 2 {
		t.Fatal(recorded)
	}
	location := filepath.Join(t.TempDir(), "counts.json")
	if err := Write(location, recorded); err != nil {
		t.Fatal(err)
	}
	first, err := os.ReadFile(location)
	if err != nil {
		t.Fatal(err)
	}
	loaded, err := Load(location)
	if err != nil {
		t.Fatal(err)
	}
	if err := Write(location, loaded); err != nil {
		t.Fatal(err)
	}
	second, err := os.ReadFile(location)
	if err != nil {
		t.Fatal(err)
	}
	if string(first) != string(second) || first[len(first)-1] != '\n' {
		t.Fatal("unstable output")
	}
	fallback := Record(file, []diagnostics.Diagnostic{finding("a"), finding("")}, nil)
	if fallback["src/a.ts"]["rule"].Entities != nil {
		t.Fatal("missing key must use count mode")
	}
}

func TestLockLimits(t *testing.T) {
	locked := File{"src/a.ts": {"rule": {Count: 2, Entities: map[string]int{"a": 2}}}}
	for _, test := range []struct {
		name  string
		entry Entry
		fail  bool
	}{
		{"equal", Entry{Count: 2, Entities: map[string]int{"a": 2}}, false},
		{"lower", Entry{Count: 1, Entities: map[string]int{"a": 1}}, false},
		{"higher", Entry{Count: 3, Entities: map[string]int{"a": 3}}, true},
		{"new entity", Entry{Count: 2, Entities: map[string]int{"a": 1, "b": 1}}, true},
		{"count only", Entry{Count: 1}, true},
	} {
		t.Run(test.name, func(t *testing.T) {
			_, failures := Limit(File{"src/a.ts": {"rule": test.entry}}, locked)
			if (len(failures) > 0) != test.fail {
				t.Fatalf("unexpected failures: %+v", failures)
			}
		})
	}
	if _, failures := Limit(File{"new.ts": {"rule": {Count: 1}}}, locked); len(failures) != 1 {
		t.Fatal(failures)
	}
}

func TestPruneDeletedFilesPreservesRulesOutsideScope(t *testing.T) {
	file := File{"missing.ts": {"rule": {Count: 1}, "excluded": {Count: 1}}}
	scope := scopeForTest()
	scope.AllRulesLoaded = false
	got := Prune(file, nil, scope, func(string) bool { return false })
	want := File{"missing.ts": {"excluded": {Count: 1}}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %+v want %+v", got, want)
	}
}
