package engine

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/bigbizze/lintai/backend"
	"github.com/bigbizze/lintai/backend/typescript"
	"github.com/bigbizze/lintai/diagnostics"
	"github.com/bigbizze/lintai/internal/testpack"
	"github.com/bigbizze/lintai/observe"
	"github.com/bigbizze/lintai/suppressions"
)

type coverageProducer struct {
	*parallelFixtureProducer
	files    []string
	revision string
}

func (p *coverageProducer) Prepare(ctx context.Context, request backend.CollectionRequest, mode observe.Mode) (backend.Generation, error) {
	generation, err := p.parallelFixtureProducer.Prepare(ctx, request, mode)
	if err != nil {
		return nil, err
	}
	g := generation.(*parallelGeneration)
	g.metadata.AnalyzedFiles = append([]string(nil), p.files...)
	g.metadata.InputRevision = p.revision
	return g, nil
}
func secondCoverageProducer() *coverageProducer {
	p := newParallelFixtureProducer()
	p.description.Namespace = "other"
	p.description.Schema.Declarations = strings.ReplaceAll(p.description.Schema.Declarations, "catalog.", "other.")
	p.description.Schema.Prelude = strings.ReplaceAll(p.description.Schema.Prelude, "catalog.", "other.")
	p.description.BaseRelations[0].Predicate.Name = "other.item"
	p.description.Helpers[0].Predicate.Name = "other.usable"
	return &coverageProducer{parallelFixtureProducer: p, files: []string{"catalog/items.sql"}, revision: "other:1"}
}

// V01, V25, V35, V38: a non-file producer's own tracking key and null key
// are matched generically; another producer's coverage cannot prune them.
func TestSuppressionProducerCoverage(t *testing.T) {
	repo, err := filepath.Abs("..")
	if err != nil {
		t.Fatal(err)
	}
	workspace := t.TempDir()
	p := newParallelFixtureProducer()
	support := p.anchors["item-b"]
	support.Location.File = "../catalog/items.sql"
	support.Tracking = nil
	p.anchors["item-b"] = support
	catalog := &coverageProducer{parallelFixtureProducer: p, revision: "catalog:1"}
	other := secondCoverageProducer()
	writeTestFile(t, workspace, "lintai-rules/catalog.ts", parallelRuleSource("fixture.catalog", "Decl violation(Entity, Code). violation(Entity, Code) :- catalog.usable(Entity, Code).", "(row) => row.Code"))
	writeTestFile(t, workspace, "lintai-rules/other.ts", parallelRuleSource("fixture.other", "Decl violation(Entity, Code). violation(Entity, Code) :- other.usable(Entity, Code).", "(row) => row.Code"))
	options := parallelFixtureOptions(t, repo, workspace)
	options.RuleWorkers = 2
	session := openTestSession(t, options, catalog, other)
	result, err := session.UpdateSuppressions(context.Background(), SuppressionUpdate{All: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Suppressed) != 6 {
		t.Fatalf("recording: %+v", result.Diagnostics)
	}
	wantEntities := map[string]bool{
		"catalog:item-a": false, "catalog:item-b": false, "catalog:item-c": false,
		"other:item-a": false, "other:item-b": false, "other:item-c": false,
	}
	for _, item := range result.Suppressed {
		if item.EntityIdentity == nil {
			t.Fatalf("missing producer-qualified entity: %+v", item)
		}
		id := item.EntityIdentity.SemanticKey
		if _, ok := wantEntities[id]; !ok {
			t.Fatalf("unexpected producer-qualified entity %q", id)
		}
		wantEntities[id] = true
	}
	for id, found := range wantEntities {
		if !found {
			t.Fatalf("missing producer-qualified entity %q", id)
		}
	}
	path := filepath.Join(workspace, suppressions.DefaultFile)
	originalBytes, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	oneProducer := newParallelFixtureProducer()
	nullSupport := oneProducer.anchors["item-b"]
	nullSupport.Location.File = "../catalog/items.sql"
	nullSupport.Tracking = nil
	oneProducer.anchors["item-b"] = nullSupport
	oneOptions := options
	oneOptions.RuleWorkers = 1
	oneOptions.SuppressionsLocation = "single-suppressions.json"
	single := openTestSession(t, oneOptions, &coverageProducer{parallelFixtureProducer: oneProducer, revision: "catalog:1"}, secondCoverageProducer())
	oneResult, err := single.UpdateSuppressions(context.Background(), SuppressionUpdate{All: true})
	if err != nil {
		t.Fatal(err)
	}
	if err := single.Close(); err != nil {
		t.Fatal(err)
	}
	oneBytes, err := os.ReadFile(filepath.Join(workspace, oneOptions.SuppressionsLocation))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(originalBytes, oneBytes) || !reflect.DeepEqual(result.Suppressed, oneResult.Suppressed) || result.FactRevision != oneResult.FactRevision {
		t.Fatal("one and multiple workers disagree on saved groups or decisions")
	}
	saved, err := suppressions.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	var foundNull, foundKey bool
	for _, entry := range saved {
		if entry.Rule == "fixture.catalog" {
			for _, g := range entry.Groups {
				foundNull = foundNull || g.Key == nil
				foundKey = foundKey || g.Key != nil && g.Key.Scheme == "fixture.item/1"
			}
		}
	}
	if !foundNull || !foundKey {
		t.Fatalf("missing producer support: %+v", saved)
	}
	matched, err := session.Check(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(matched.Suppressed) != 6 || len(matched.Diagnostics) != 0 {
		t.Fatalf("saved dependency allowance did not match: %+v", matched)
	}
	var matchedDependency bool
	for _, item := range matched.Suppressed {
		if item.SourceLocation != nil && item.SourceLocation.File == "../catalog/items.sql" {
			matchedDependency = true
		}
	}
	if !matchedDependency {
		t.Fatalf("dependency finding was not matched by its saved allowance: %+v", matched.Suppressed)
	}
	warningOptions := options
	warningOptions.Rule = map[string]RulePolicy{"fixture.catalog": {Severity: "warning"}, "fixture.other": {Severity: "warning"}}
	warningResult, err := openTestSession(t, warningOptions, catalog, other).Check(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(warningResult.Suppressed) != 0 || len(warningResult.UnusedSuppressions) != 0 || len(warningResult.Diagnostics) != 6 {
		t.Fatalf("warning findings must not use allowances: %+v", warningResult)
	}
	for _, item := range warningResult.Diagnostics {
		if item.Severity != diagnostics.SeverityWarning {
			t.Fatalf("unexpected severity: %+v", item)
		}
	}
	catalog.rows = nil
	catalog.revision = "catalog:2"
	result, err = session.UpdateSuppressions(context.Background(), SuppressionUpdate{Prune: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.UnusedSuppressions) != 0 {
		t.Fatalf("unrelated producer gave prune authority: %+v", result.UnusedSuppressions)
	}
	after, err := suppressions.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(after) != len(saved) {
		t.Fatalf("unrelated producer pruned catalog entries: %+v", after)
	}
	catalog.files = []string{"catalog/items.sql"}
	catalog.revision = "catalog:3"
	_, err = session.UpdateSuppressions(context.Background(), SuppressionUpdate{Prune: true})
	if err != nil {
		t.Fatal(err)
	}
	after, err = suppressions.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	var dependencyEntry bool
	for _, entry := range after {
		if entry.Rule == "fixture.catalog" {
			if entry.File != "../catalog/items.sql" {
				t.Fatalf("own primary coverage did not prune covered files: %+v", after)
			}
			dependencyEntry = true
		}
	}
	if !dependencyEntry {
		t.Fatalf("dependency allowance did not survive primary-file pruning: %+v", after)
	}
}

// V23, V25, V30, V39: a concurrent edit or cancellation blocks the guarded
// replacement, and the same target guard applies to lock regeneration.
func TestSavedWriteGuards(t *testing.T) {
	repo, err := filepath.Abs("..")
	if err != nil {
		t.Fatal(err)
	}
	workspace := t.TempDir()
	writeTestFile(t, workspace, "lintai-rules/r.ts", parallelRuleSource("fixture.guard", "Decl violation(Entity, Code). violation(Entity, Code) :- catalog.usable(Entity, Code).", "(row) => row.Code"))
	producer := newParallelFixtureProducer()
	options := parallelFixtureOptions(t, repo, workspace)
	session := openTestSession(t, options, producer)
	if _, err := session.UpdateSuppressions(context.Background(), SuppressionUpdate{All: true}); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(workspace, suppressions.DefaultFile)
	original, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	changed := append(append([]byte(nil), original...), '\n')
	session.hooks.afterDispatch = func() {
		if err := os.WriteFile(path, changed, 0o644); err != nil {
			t.Error(err)
		}
	}
	if _, err := session.UpdateSuppressions(context.Background(), SuppressionUpdate{All: true}); err == nil || !strings.Contains(err.Error(), "suppression_changed") {
		t.Fatalf("expected suppression_changed: %v", err)
	}
	current, err := os.ReadFile(path)
	if err != nil || !bytes.Equal(current, changed) {
		t.Fatalf("concurrent edit lost: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	session.hooks.afterDispatch = cancel
	if _, err := session.UpdateSuppressions(ctx, SuppressionUpdate{All: true}); err == nil {
		t.Fatal("cancelled check succeeded")
	}
	current, err = os.ReadFile(path)
	if err != nil || !bytes.Equal(current, changed) {
		t.Fatalf("cancelled check wrote suppression: %v", err)
	}
	session.hooks.afterDispatch = nil
	lockPath := filepath.Join(workspace, DefaultRulesLockFile)
	cancelledLock, cancelLock := context.WithCancel(context.Background())
	session.hooks.beforeWrite = cancelLock
	if _, err := session.ApproveRules(cancelledLock, ""); err == nil {
		t.Fatal("cancelled lock write succeeded")
	}
	if _, err := os.Stat(lockPath); !os.IsNotExist(err) {
		t.Fatalf("cancelled lock write left a file: %v", err)
	}
	session.hooks.beforeWrite = nil
	if _, err := session.ApproveRules(context.Background(), ""); err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(lockPath)
	if err != nil {
		t.Fatal(err)
	}
	external := append(append([]byte(nil), before...), '\n')
	session.hooks.beforeWrite = func() {
		if err := os.WriteFile(lockPath, external, 0o644); err != nil {
			t.Error(err)
		}
	}
	if _, err := session.ApproveRules(context.Background(), ""); err == nil || !strings.Contains(err.Error(), "rules_lock_changed") {
		t.Fatalf("expected rules_lock_changed: %v", err)
	}
	current, err = os.ReadFile(lockPath)
	if err != nil || !bytes.Equal(current, external) {
		t.Fatalf("lock edit lost: %v", err)
	}
	// A locked suppression update also verifies that approval stayed current.
	lockedOptions := options
	lockedOptions.RulesLock = DefaultRulesLockFile
	locked := openTestSession(t, lockedOptions, producer)
	locked.hooks.afterDispatch = func() {
		if err := os.WriteFile(lockPath, append(external, '\n'), 0o644); err != nil {
			t.Error(err)
		}
	}
	if _, err := locked.UpdateSuppressions(context.Background(), SuppressionUpdate{All: true}); err == nil || !strings.Contains(err.Error(), "rules_lock_changed") {
		t.Fatalf("approval edit must block write: %v", err)
	}
	current, err = os.ReadFile(path)
	if err != nil || !bytes.Equal(current, changed) {
		t.Fatalf("approval edit allowed a suppression write: %v", err)
	}
	session.hooks.beforeWrite = func() {
		if err := os.WriteFile(path, append(changed, '\n'), 0o644); err != nil {
			t.Error(err)
		}
	}
	if _, err := session.ApproveRules(context.Background(), ""); err == nil || !strings.Contains(err.Error(), "suppression_changed") {
		t.Fatalf("suppression edit must block lock write: %v", err)
	}
	session.hooks.beforeWrite = nil
	if err := os.WriteFile(lockPath, []byte(`{"format":"lintai.rules-lock/2","rules":{}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := session.ApproveRules(context.Background(), ""); err != nil {
		t.Fatal("explicit approval should replace obsolete format")
	}
}

// V37: the SDK preserves invalid declarations so program analysis rejects
// each distinct invalid policy instead of silently changing it.
func TestSuppressionPolicyValidation(t *testing.T) {
	repo, err := filepath.Abs("..")
	if err != nil {
		t.Fatal(err)
	}
	cases := []struct{ name, policy string }{
		{"unknown field", `.suppression("violation", { mode: () => "count" })`},
		{"duplicate declaration", `.suppression("violation", {}).suppression("violation", {})`},
		{"unknown assertion", `.suppression("missing", {})`},
		{"unknown column", `.suppression("violation", { by: ["Missing"] })`},
		{"anchor column", `.suppression("violation", { by: ["Entity"] })`},
		{"duplicate column", `.suppression("violation", { by: ["Code", "Code"] })`},
		{"invalid by", `.suppression("violation", { by: () => "Code" })`},
		{"invalid options", `.suppression("violation", null)`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			workspace := t.TempDir()
			source := parallelRuleSource("fixture.policy", "Decl violation(Entity, Code). violation(Entity, Code) :- catalog.usable(Entity, Code).", "(row) => row.Code")
			source = strings.Replace(source, `.report("violation").message(`, `.report("violation")`+tc.policy+`.message(`, 1)
			writeTestFile(t, workspace, "lintai-rules/r.ts", source)
			session := openTestSession(t, parallelFixtureOptions(t, repo, workspace), newParallelFixtureProducer())
			result, err := session.Check(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			if len(result.Diagnostics) != 1 || result.Diagnostics[0].Phase != "program_analysis" {
				t.Fatalf("policy accepted or wrong phase: %+v", result.Diagnostics)
			}
		})
	}
}

func TestSuppressionPolicyValidationOnPlanReuse(t *testing.T) {
	repo, err := filepath.Abs("..")
	if err != nil {
		t.Fatal(err)
	}
	workspace, rules := t.TempDir(), t.TempDir()
	writeTestFile(t, workspace, "src/a.ts", "export const value = 1;\n")
	paths := make(map[string]string)
	for _, kind := range []string{"valid", "invalid"} {
		policy := `{}`
		if kind == "invalid" {
			policy = `[] as any`
		}
		source := parallelRuleSource("fixture.policy-"+kind,
			"Decl violation(Entity). violation(Entity) :- ts.module(Entity).", `(row) => "finding"`)
		source = strings.Replace(source, `.report("violation").message(`,
			`.report("violation").suppression("violation", `+policy+`).message(`, 1)
		paths[kind] = writeTestFile(t, rules, kind+".ts", source)
	}
	options := Config{WorkspaceRoot: testpack.Build(t, repo, workspace, filepath.Join(rules, "*.ts")), RuleWorkers: 1}
	session := openTestSession(t, options, typescript.New(typescript.Options{Files: typescript.FileScopeWalk}))
	defer session.Close()
	var template *rulePlan
	for check := 0; check < 2; check++ {
		result, err := session.Check(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		byRule := make(map[string]diagnostics.Diagnostic)
		for _, diagnostic := range result.Diagnostics {
			byRule[diagnostic.RuleID] = diagnostic
		}
		if len(result.Diagnostics) != 2 {
			t.Fatalf("check %d: expected one result per rule: %+v", check, result.Diagnostics)
		}
		if invalid := byRule["fixture.policy-invalid"]; invalid.Phase != "program_analysis" ||
			!strings.Contains(invalid.Message, `must be an options object`) {
			t.Errorf("check %d: invalid rule did not get its options-object error: %+v", check, invalid)
		}
		if valid := byRule["fixture.policy-valid"]; valid.AssertionID != "violation" || valid.Message != "finding" || valid.Phase != "" {
			t.Errorf("check %d: valid finding was lost: %+v", check, valid)
		}
		entry := session.ruleEntries["rules/valid.ts"]
		if entry.plan == nil || entry.plan.ruleFacts == nil || len(entry.plan.suppressionRules) != 1 {
			t.Fatal("retained plan is missing facts or the validated suppression policy")
		}
		if session.ruleEntries["rules/invalid.ts"].plan != nil {
			t.Fatal("invalid suppression policy retained a plan")
		}
		if check == 0 {
			template = entry.plan
		} else if entry.plan != template {
			t.Error("valid plan was not reused")
		}
	}
	validSource, err := os.ReadFile(paths["valid"])
	if err != nil {
		t.Fatal(err)
	}
	invalidSource := strings.Replace(string(validSource), `.suppression("violation", {})`, `.suppression("violation", [] as any)`, 1)
	if invalidSource == string(validSource) {
		t.Fatal("suppression fixture did not contain the expected declaration")
	}
	if err := os.WriteFile(paths["valid"], []byte(invalidSource), 0o600); err != nil {
		t.Fatal(err)
	}
	testpack.Build(t, repo, workspace, filepath.Join(rules, "*.ts"))
	result, err := session.Check(context.Background())
	if err != nil || len(result.Diagnostics) != 2 {
		t.Fatalf("changed suppression declaration did not compile and fail independently: %+v %v", result.Diagnostics, err)
	}
	for _, diagnostic := range result.Diagnostics {
		if diagnostic.Phase != "program_analysis" {
			t.Fatalf("invalid declaration was accepted: %+v", diagnostic)
		}
	}
	if session.ruleEntries["rules/valid.ts"].plan != template {
		t.Fatal("failed compilation replaced the last valid template")
	}
}
