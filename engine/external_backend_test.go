package engine_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/bigbizze/lintai/backend"
	"github.com/bigbizze/lintai/backend/typescript"
	"github.com/bigbizze/lintai/diagnostics"
	"github.com/bigbizze/lintai/engine"
	"github.com/bigbizze/lintai/facts"
)

// FX1 covers V01, V02, V03, V05, V16, V24 and R1's fake-only, mixed,
// colliding-name, opaque-revision, empty-coverage, and invalid-batch cases.
type catalogFake struct {
	description facts.ProducerDescription
	rows        map[facts.Predicate][]facts.Row
	anchors     map[string]facts.AnchorSupport
	revision    string
	scope       any
	analyzed    []string
	omit        facts.Predicate
}

func (p *catalogFake) Describe() (facts.ProducerDescription, error) { return p.description, nil }

func (p *catalogFake) Collect(_ context.Context, request backend.CollectionRequest) (facts.Batch, error) {
	batch := facts.Batch{Relations: make(map[facts.Predicate][]facts.Row), Anchors: p.anchors, InputRevision: p.revision, Scope: p.scope, AnalyzedFiles: p.analyzed}
	for _, predicate := range request.Predicates {
		if predicate == p.omit {
			continue
		}
		rows := p.rows[predicate]
		batch.Relations[predicate] = rows
	}
	return batch, nil
}

func (p *catalogFake) Close() error { return nil }

func fakeProducer(namespace string, predicates ...string) *catalogFake {
	description := facts.ProducerDescription{
		Namespace: namespace, ImplementationID: "fixture." + namespace,
		ImplementationStamp: "fixture-v1", ContractVersion: "fixture-contract/1",
		EffectiveConfiguration: map[string]any{"kind": "catalog-fixture"},
	}
	var schema strings.Builder
	rows := make(map[facts.Predicate][]facts.Row)
	for _, local := range predicates {
		predicate := facts.Predicate{Name: namespace + "." + local, Arity: 1}
		relation := facts.RelationDescription{
			Predicate:     predicate,
			Arguments:     []facts.ArgumentDescription{{Name: "Entity", Representations: []facts.ValueKind{facts.StringKind}, Role: facts.EntityRole, ReportSupport: facts.ReportOptional}},
			Documentation: "A fixture entity.",
		}
		description.BaseRelations = append(description.BaseRelations, relation)
		rows[predicate] = []facts.Row{}
		schema.WriteString("Decl " + local + "(Entity) descr [extensional(), arg(Entity, \"entity id\")].\n")
	}
	var prelude strings.Builder
	if len(predicates) > 0 {
		helper := facts.Predicate{Name: namespace + ".present", Arity: 1}
		description.Helpers = append(description.Helpers, facts.RelationDescription{
			Predicate:     helper,
			Arguments:     []facts.ArgumentDescription{{Name: "Entity", Representations: []facts.ValueKind{facts.StringKind}, Role: facts.EntityRole, ReportSupport: facts.ReportOptional}},
			Documentation: "An entity present in the fixture catalog.",
		})
		prelude.WriteString("Decl present(Entity) descr [arg(Entity, \"entity id\")].\npresent(Entity) :- " + predicates[0] + "(Entity).\n")
	}
	description.Schema = facts.Schema{Declarations: schema.String(), Prelude: prelude.String()}
	return &catalogFake{
		description: description, rows: rows,
		anchors: make(map[string]facts.AnchorSupport), revision: "opaque-revision-v1", scope: "opaque fixture scope",
	}
}

func setFakeRow(producer *catalogFake, local, entity string) {
	predicate := facts.Predicate{Name: producer.description.Namespace + "." + local, Arity: 1}
	producer.rows[predicate] = []facts.Row{{facts.String(entity)}}
	producer.anchors[entity] = facts.AnchorSupport{
		Location:    diagnostics.SourceLocation{File: "catalog/object.sql", StartLine: 2, StartColumn: 1, EndLine: 2, EndColumn: 8},
		Description: "catalog object " + entity,
		Tracking:    &facts.TrackingKey{Scheme: "fixture.object/1", Data: map[string]any{"name": strings.TrimPrefix(entity, producer.description.Namespace+":")}},
	}
}

func TestCatalogFakeAloneReportsProducerOwnedAnchorAndOpaqueRevision(t *testing.T) {
	// V01/V02/V03/V24: fake-only qualified facts, present-empty coverage,
	// no-rule coverage, producer-owned reporting, and an opaque revision.
	repoRoot, _ := filepath.Abs("..")
	workspace := t.TempDir()
	producer := fakeProducer("catalog", "item")
	setFakeRow(producer, "item", "catalog:users")
	writeCatalogRule(t, workspace, `Decl violation(Entity). violation(Entity) :- catalog.present(Entity).`, "violation")
	session := engine.Open(engine.Options{}, producer)
	defer session.Close()
	options := engine.Options{
		RepoRoot: repoRoot, AssetRoot: filepath.Join(repoRoot, "packages/client"), WorkspaceRoot: workspace,
		RuleGlobs: []string{"lintai-rules/**/*.ts"}, Env: map[string]any{}, Severity: diagnostics.SeverityError,
	}
	result, err := session.Check(context.Background(), options)
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Diagnostics) != 1 {
		t.Fatalf("expected one producer-owned diagnostic, got %+v", result.Diagnostics)
	}
	item := result.Diagnostics[0]
	if item.SourceLocation == nil || item.SourceLocation.File != "catalog/object.sql" {
		t.Fatalf("unexpected fake-owned location: %+v", item.SourceLocation)
	}
	if item.EntityIdentity == nil || item.EntityIdentity.SemanticKey != "catalog:users" {
		t.Fatalf("unexpected anchor identity: %+v", item.EntityIdentity)
	}
	if len(item.Provenance.ProducerNamespaces) != 1 || item.Provenance.ProducerNamespaces[0] != "catalog" || !strings.HasPrefix(item.Provenance.SnapshotVersion, "sha256:") {
		t.Fatalf("unexpected composed provenance: %+v", item.Provenance)
	}
	// V18/V20: an opaque revision is the producer's own validity claim.
	unchanged, err := session.Check(context.Background(), options)
	if err != nil || unchanged.SnapshotVersion != result.SnapshotVersion {
		t.Fatalf("opaque revision not reused: %+v %v", unchanged, err)
	}
	producer.revision = "opaque-revision-v2"
	producer.rows[facts.Predicate{Name: "catalog.item", Arity: 1}] = nil
	retracted, err := session.Check(context.Background(), options)
	if err != nil || len(retracted.Diagnostics) != 0 || retracted.SnapshotVersion == result.SnapshotVersion {
		t.Fatalf("opaque producer change did not retract its result: %+v %v", retracted, err)
	}

	empty := fakeProducer("empty", "item")
	emptyResult, err := engine.Open(engine.Options{}, empty).Check(context.Background(), engine.Options{
		RepoRoot: repoRoot, AssetRoot: filepath.Join(repoRoot, "packages/client"), WorkspaceRoot: t.TempDir(),
		RuleGlobs: []string{}, Env: map[string]any{},
	})
	if err != nil {
		t.Fatalf("present-empty relation should be successful: %v", err)
	}
	if len(emptyResult.AnalyzedFiles) != 0 {
		t.Fatalf("no-rule run claimed analyzed-file coverage: %v", emptyResult.AnalyzedFiles)
	}
}

func TestOpaqueProducerScopeContributesToResultIdentity(t *testing.T) {
	// V16: identical opaque revision and file coverage with a different scope
	// token produce different producer-result-v2 identities.
	repoRoot, _ := filepath.Abs("..")
	workspace := t.TempDir()
	writeCatalogRule(t, workspace, `Decl violation(Entity). violation(Entity) :- catalog.item(Entity).`, "violation")
	options := engine.Options{
		RepoRoot: repoRoot, AssetRoot: filepath.Join(repoRoot, "packages/client"), WorkspaceRoot: workspace,
		RuleGlobs: []string{"lintai-rules/**/*.ts"}, Env: map[string]any{},
	}
	left := fakeProducer("catalog", "item")
	left.scope = map[string]any{"partition": "a"}
	left.analyzed = []string{"catalog/objects.sql"}
	leftResult, err := engine.Open(engine.Options{}, left).Check(context.Background(), options)
	if err != nil {
		t.Fatal(err)
	}
	right := fakeProducer("catalog", "item")
	right.scope = []any{[]any{"partition", "a"}}
	right.analyzed = []string{"catalog/objects.sql"}
	rightResult, err := engine.Open(engine.Options{}, right).Check(context.Background(), options)
	if err != nil {
		t.Fatal(err)
	}
	if leftResult.SnapshotVersion == rightResult.SnapshotVersion {
		t.Fatalf("producer scope did not affect identity: %s", leftResult.SnapshotVersion)
	}
}

func TestTypeScriptAndFakeComposeThroughCatalog(t *testing.T) {
	// V01/V24: TypeScript and an independent fake compose with two namespaces,
	// producer-owned location support, and no file-ID inference.
	repoRoot, _ := filepath.Abs("..")
	workspace := filepath.Join(repoRoot, "testdata/fixtures/external-backend/mixed-facts")
	fake := fakeProducer("catalog", "external_boundary")
	setFakeRow(fake, "external_boundary", "catalog:catalog-api")
	result, err := engine.Open(engine.Options{}, typescript.New(), fake).Check(context.Background(), engine.Options{
		RepoRoot: repoRoot, AssetRoot: filepath.Join(repoRoot, "packages/client"), WorkspaceRoot: workspace,
		RuleGlobs: []string{"lintai-rules/**/*.ts"}, Env: map[string]any{}, Severity: diagnostics.SeverityError,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Diagnostics) != 1 {
		t.Fatalf("expected one joined TypeScript/fake diagnostic, got %+v", result.Diagnostics)
	}
	item := result.Diagnostics[0]
	if item.RuleID != "fixture.external-boundary" || item.SourceLocation == nil || item.SourceLocation.File != "catalog/object.sql" {
		t.Fatalf("unexpected diagnostic from composed facts: %+v", item)
	}
	if got := strings.Join(item.Provenance.ProducerNamespaces, ","); got != "catalog,ts" {
		t.Fatalf("producer provenance = %q", got)
	}
}

func TestProducerNamespacesDisambiguateCollidingLocalPredicates(t *testing.T) {
	// V01: identical local names and unrelated local keys remain distinct after
	// Mangle's package-analysis qualification step.
	repoRoot, _ := filepath.Abs("..")
	workspace := t.TempDir()
	left, right := fakeProducer("left", "item"), fakeProducer("right", "item")
	setFakeRow(left, "item", "left:same")
	setFakeRow(right, "item", "right:same")
	writeCatalogRule(t, workspace, `Decl violation(Entity). violation(Entity) :- left.item(Entity), right.item(_).`, "violation")
	result, err := engine.Open(engine.Options{}, right, left).Check(context.Background(), engine.Options{
		RepoRoot: repoRoot, AssetRoot: filepath.Join(repoRoot, "packages/client"), WorkspaceRoot: workspace,
		RuleGlobs: []string{"lintai-rules/**/*.ts"}, Env: map[string]any{}, Severity: diagnostics.SeverityError,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Diagnostics) != 1 || result.Diagnostics[0].EntityIdentity.SemanticKey != "left:same" {
		t.Fatalf("colliding local relation names did not remain separate: %+v", result.Diagnostics)
	}
}

func TestCatalogRejectsMissingCoverageAndForeignOwnership(t *testing.T) {
	// V01/V03: duplicate namespaces, omitted coverage, and foreign entity
	// ownership fail before rule evaluation; an empty relation entry remains
	// successful empty coverage.
	repoRoot, _ := filepath.Abs("..")
	workspace := t.TempDir()
	writeCatalogRule(t, workspace, `Decl violation(Entity). violation(Entity) :- catalog.item(Entity).`, "violation")
	options := engine.Options{
		RepoRoot: repoRoot, AssetRoot: filepath.Join(repoRoot, "packages/client"), WorkspaceRoot: workspace,
		RuleGlobs: []string{"lintai-rules/**/*.ts"}, Env: map[string]any{},
	}
	duplicate := fakeProducer("catalog", "item")
	if _, err := engine.Open(engine.Options{}, duplicate, duplicate).Check(context.Background(), options); err == nil || !strings.Contains(err.Error(), "registered more than once") {
		t.Fatalf("expected duplicate-namespace failure, got %v", err)
	}
	missing := fakeProducer("catalog", "item")
	missing.omit = facts.Predicate{Name: "catalog.item", Arity: 1}
	if _, err := engine.Open(engine.Options{}, missing).Check(context.Background(), options); err == nil || !strings.Contains(err.Error(), "omitted requested predicate") {
		t.Fatalf("expected missing-coverage failure, got %v", err)
	}
	unlisted := fakeProducer("catalog", "item")
	unlisted.description.Schema.Declarations += `Decl unlisted(Entity) descr [extensional(), arg(Entity, "entity id")].`
	if _, err := engine.Open(engine.Options{}, unlisted).Check(context.Background(), options); err == nil || !strings.Contains(err.Error(), "absent from BaseRelations") {
		t.Fatalf("expected catalog extensional predicate mismatch, got %v", err)
	}
	undeclared := fakeProducer("catalog", "item")
	undeclaredRelation := undeclared.description.BaseRelations[0]
	undeclaredRelation.Predicate = facts.Predicate{Name: "catalog.undeclared", Arity: 1}
	undeclared.description.BaseRelations = append(undeclared.description.BaseRelations, undeclaredRelation)
	if _, err := engine.Open(engine.Options{}, undeclared).Check(context.Background(), options); err == nil || !strings.Contains(err.Error(), "no extensional catalog declaration") {
		t.Fatalf("expected catalog base relation mismatch, got %v", err)
	}
	foreign := fakeProducer("catalog", "item")
	foreign.rows[facts.Predicate{Name: "catalog.item", Arity: 1}] = []facts.Row{{facts.String("other:foreign")}}
	foreign.anchors["other:foreign"] = facts.AnchorSupport{Location: diagnostics.SourceLocation{File: "src/file.ts", StartLine: 1, StartColumn: 1, EndLine: 1, EndColumn: 2}, Description: "foreign"}
	if _, err := engine.Open(engine.Options{}, foreign).Check(context.Background(), options); err == nil || !strings.Contains(err.Error(), "foreign or invalid entity") {
		t.Fatalf("expected ownership failure, got %v", err)
	}
	foreignAnchor := fakeProducer("catalog", "item")
	foreignAnchor.anchors["other:foreign"] = facts.AnchorSupport{Location: diagnostics.SourceLocation{File: "src/file.ts", StartLine: 1, StartColumn: 1, EndLine: 1, EndColumn: 2}, Description: "foreign"}
	if _, err := engine.Open(engine.Options{}, foreignAnchor).Check(context.Background(), options); err == nil || !strings.Contains(err.Error(), "supplied anchor with foreign or invalid entity") {
		t.Fatalf("expected foreign-anchor failure, got %v", err)
	}
}

func TestRuleViewDoesNotBorrowAnotherRulesAnchorSupport(t *testing.T) {
	// V05/V24: an unrelated relation's support remains outside a rule's
	// reporting view, even when another rule can report the same ID.
	repoRoot, _ := filepath.Abs("..")
	workspace := t.TempDir()
	producer := fakeProducer("catalog", "item", "unlocated")
	queryOnly := facts.Predicate{Name: "catalog.unlocated", Arity: 1}
	producer.description.BaseRelations[1].Arguments[0] = facts.ArgumentDescription{
		Name: "Entity", Representations: []facts.ValueKind{facts.StringKind}, Role: facts.ValueRole,
		ReportSupport: facts.ReportNone,
	}
	setFakeRow(producer, "item", "catalog:users")
	producer.rows[queryOnly] = []facts.Row{{facts.String("catalog:users")}}

	badProgram := `Decl violation(Entity). violation(Entity) :- catalog.unlocated(Entity).`
	writeCatalogRuleFile(t, workspace, "query-only.ts", "fixture.query-only", badProgram, "violation")
	withoutOwner, err := engine.Open(engine.Options{}, producer).Check(context.Background(), engine.Options{
		RepoRoot: repoRoot, AssetRoot: filepath.Join(repoRoot, "packages/client"), WorkspaceRoot: workspace,
		RuleGlobs: []string{"lintai-rules/**/*.ts"}, Env: map[string]any{}, Severity: diagnostics.SeverityError,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(withoutOwner.Diagnostics) != 1 || withoutOwner.Diagnostics[0].Phase != "report" {
		t.Fatalf("query-only entity unexpectedly had reporting support: %+v", withoutOwner.Diagnostics)
	}

	writeCatalogRuleFile(t, workspace, "owner.ts", "fixture.anchor-owner",
		`Decl violation(Entity). violation(Entity) :- catalog.item(Entity).`, "violation")
	withOwner, err := engine.Open(engine.Options{}, producer).Check(context.Background(), engine.Options{
		RepoRoot: repoRoot, AssetRoot: filepath.Join(repoRoot, "packages/client"), WorkspaceRoot: workspace,
		RuleGlobs: []string{"lintai-rules/**/*.ts"}, Env: map[string]any{}, Severity: diagnostics.SeverityError,
	})
	if err != nil {
		t.Fatal(err)
	}
	var queryOnlyFailures, ownerDiagnostics int
	for _, diagnostic := range withOwner.Diagnostics {
		switch diagnostic.RuleID {
		case "fixture.query-only":
			queryOnlyFailures++
			if diagnostic.Phase != "report" || diagnostic.Message != withoutOwner.Diagnostics[0].Message {
				t.Fatalf("unrelated rule changed query-only reporting: %+v", diagnostic)
			}
		case "fixture.anchor-owner":
			ownerDiagnostics++
		default:
			t.Fatalf("unexpected diagnostic: %+v", diagnostic)
		}
	}
	if queryOnlyFailures != 1 || ownerDiagnostics != 1 {
		t.Fatalf("unexpected per-rule reporting results: %+v", withOwner.Diagnostics)
	}
}

func TestLintaiGlobDoesNotNeedTypeScript(t *testing.T) {
	// V02: the generic path helper is in the engine catalog and can be used
	// when the only producer is a non-TypeScript fake.
	repoRoot, _ := filepath.Abs("..")
	workspace := t.TempDir()
	producer := fakeProducer("catalog", "item")
	setFakeRow(producer, "item", "catalog:users")
	writeCatalogRule(t, workspace, `Decl candidate(Entity). candidate(Entity) :- catalog.item(Entity), lintai.glob("src/users/**", "src/**"). Decl violation(Entity). violation(Entity) :- candidate(Entity).`, "violation")
	result, err := engine.Open(engine.Options{}, producer).Check(context.Background(), engine.Options{
		RepoRoot: repoRoot, AssetRoot: filepath.Join(repoRoot, "packages/client"), WorkspaceRoot: workspace,
		RuleGlobs: []string{"lintai-rules/**/*.ts"}, Env: map[string]any{}, Severity: diagnostics.SeverityError,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Diagnostics) != 1 {
		t.Fatalf("generic lintai.glob did not run without TypeScript: %+v", result.Diagnostics)
	}
}

func TestNamespaceBindingRejectsAliasesAndRuleOwnedPackages(t *testing.T) {
	// V02: structured qualified base/helper bindings work without user Use;
	// unknown packages, unqualified producer aliases, and user Package/Use
	// declarations fail during rule preparation.
	repoRoot, _ := filepath.Abs("..")
	cases := []struct {
		name    string
		program string
		message string
	}{
		{"unknown namespace", `Decl violation(Entity). violation(Entity) :- unknown.item(Entity).`, "unknown.item"},
		{"unqualified alias", `Decl violation(Entity). violation(Entity) :- item(Entity).`, "item"},
		{"user Package", `Package custom! Decl violation(Entity). violation(Entity) :- catalog.item(Entity).`, "owned by the producer catalog"},
		{"user Use", `Use catalog! Decl violation(Entity). violation(Entity) :- catalog.item(Entity).`, "owned by the producer catalog"},
		{"producer redefinition", `Decl catalog.item(Entity). catalog.item(Entity) :- catalog.present(Entity). Decl violation(Entity). violation(Entity) :- catalog.item(Entity).`, "cannot redeclare base relation"},
	}
	for _, item := range cases {
		t.Run(item.name, func(t *testing.T) {
			workspace := t.TempDir()
			writeCatalogRule(t, workspace, item.program, "violation")
			result, err := engine.Open(engine.Options{}, fakeProducer("catalog", "item")).Check(context.Background(), engine.Options{
				RepoRoot: repoRoot, AssetRoot: filepath.Join(repoRoot, "packages/client"), WorkspaceRoot: workspace,
				RuleGlobs: []string{"lintai-rules/**/*.ts"}, Env: map[string]any{}, Severity: diagnostics.SeverityError,
			})
			if err != nil {
				t.Fatal(err)
			}
			if len(result.Diagnostics) != 1 || result.Diagnostics[0].Phase != "program_analysis" || !strings.Contains(result.Diagnostics[0].Message, item.message) {
				t.Fatalf("expected program preparation error containing %q, got %+v", item.message, result.Diagnostics)
			}
		})
	}
	workspace := t.TempDir()
	producer := fakeProducer("catalog", "item")
	setFakeRow(producer, "item", "catalog:users")
	writeCatalogRule(t, workspace, `Decl item(Entity). item(Entity) :- catalog.item(Entity). Decl violation(Entity). violation(Entity) :- item(Entity).`, "violation")
	result, err := engine.Open(engine.Options{}, producer).Check(context.Background(), engine.Options{
		RepoRoot: repoRoot, AssetRoot: filepath.Join(repoRoot, "packages/client"), WorkspaceRoot: workspace,
		RuleGlobs: []string{"lintai-rules/**/*.ts"}, Env: map[string]any{}, Severity: diagnostics.SeverityError,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Diagnostics) != 1 || result.Diagnostics[0].RuleID != "fixture.catalog" || result.Diagnostics[0].EntityIdentity == nil || result.Diagnostics[0].EntityIdentity.SemanticKey != "catalog:users" {
		t.Fatalf("rule-local predicate sharing a producer name did not prepare and run: %+v", result.Diagnostics)
	}
}

func writeCatalogRule(t *testing.T, workspace, program, report string) {
	writeCatalogRuleFile(t, workspace, "catalog.ts", "fixture.catalog", program, report)
}

func writeCatalogRuleFile(t *testing.T, workspace, filename, ruleID, program, report string) {
	t.Helper()
	rulePath := filepath.Join(workspace, "lintai-rules", filename)
	if err := os.MkdirAll(filepath.Dir(rulePath), 0o755); err != nil {
		t.Fatal(err)
	}
	source := `import { dl, rule } from "@lintai/sdk";
export default rule("` + ruleID + `").version(1)
	.program(dl` + "`" + program + "`" + `)
	.report("` + report + `").message((row) => String(row.Entity));
`
	if err := os.WriteFile(rulePath, []byte(source), 0o644); err != nil {
		t.Fatal(err)
	}
}
