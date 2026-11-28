package engine

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"codeberg.org/TauCeti/mangle-go/ast"
	"codeberg.org/TauCeti/mangle-go/parse"
	"github.com/bigbizze/lintai/backend"
	"github.com/bigbizze/lintai/backend/typescript"
	"github.com/bigbizze/lintai/diagnostics"
	"github.com/bigbizze/lintai/facts"
	"github.com/bigbizze/lintai/observe"
	"github.com/bigbizze/lintai/suppressions"
)

func typeScriptCatalogForTest(t *testing.T) ([]parse.SourceUnit, map[ast.PredicateSym]ast.Decl) {
	t.Helper()
	description, err := typescript.New().Describe()
	if err != nil {
		t.Fatal(err)
	}
	declarations, err := namespacedUnit(description.Schema.Declarations, "ts")
	if err != nil {
		t.Fatal(err)
	}
	prelude, err := namespacedUnit(description.Schema.Prelude, "ts")
	if err != nil {
		t.Fatal(err)
	}
	engineUnit, err := engineSchemaUnit()
	if err != nil {
		t.Fatal(err)
	}
	units := []parse.SourceUnit{declarations, prelude, engineUnit}
	decls := make(map[ast.PredicateSym]ast.Decl)
	for i, unit := range units {
		namespace := "ts"
		if i == 2 {
			namespace = "lintai"
		}
		for _, declaration := range unit.Decls {
			if isPackageDecl(declaration) {
				continue
			}
			key := ast.PredicateSym{Symbol: namespace + "." + declaration.DeclaredAtom.Predicate.Symbol, Arity: declaration.DeclaredAtom.Predicate.Arity}
			declaration.DeclaredAtom.Predicate = key
			decls[key] = declaration
		}
	}
	return units, decls
}

func qualifyTestFacts(t *testing.T, items []testFact) ([]testFact, map[string]facts.AnchorSupport, map[facts.Predicate]map[string]struct{}) {
	t.Helper()
	description, err := typescript.New().Describe()
	if err != nil {
		t.Fatal(err)
	}
	metadata := make(map[string]facts.RelationDescription, len(description.BaseRelations))
	for _, relation := range description.BaseRelations {
		metadata[relation.Predicate.Name] = relation
	}
	qualified := make([]testFact, len(items))
	anchors := make(map[string]facts.AnchorSupport)
	for i, item := range items {
		name := "ts." + item.Relation
		row := append([]facts.Value(nil), item.Values...)
		for index, argument := range metadata[name].Arguments {
			if argument.Role == facts.EntityRole && row[index].Kind == facts.StringKind {
				if row[index].String == "" && (argument.Name == "Caller" || argument.Name == "From" || argument.Name == "Owner") {
					row[index].String = "ts:unsupported"
				} else if !startsWithNamespace(row[index].String) {
					row[index].String = "ts:" + row[index].String
				}
			}
		}
		qualified[i] = testFact{Relation: name, Values: row}
		if item.Relation == "location" && len(row) == 6 {
			id := row[0].String
			anchors[id] = facts.AnchorSupport{
				Location:    diagnostics.SourceLocation{File: row[1].String, StartLine: int(row[2].Number), StartColumn: int(row[3].Number), EndLine: int(row[4].Number), EndColumn: int(row[5].Number)},
				Description: "test source entity " + id,
			}
		}
	}
	reportable := make(map[facts.Predicate]map[string]struct{})
	for _, item := range qualified {
		predicate := facts.Predicate{Name: item.Relation, Arity: len(item.Values)}
		for index, argument := range metadata[item.Relation].Arguments {
			if argument.Role != facts.EntityRole || argument.ReportSupport == facts.ReportNone || item.Values[index].Kind != facts.StringKind {
				continue
			}
			id := item.Values[index].String
			if _, ok := anchors[id]; !ok {
				continue
			}
			if reportable[predicate] == nil {
				reportable[predicate] = make(map[string]struct{})
			}
			reportable[predicate][id] = struct{}{}
		}
	}
	return qualified, anchors, reportable
}

func startsWithNamespace(value string) bool {
	return strings.HasPrefix(value, "ts:")
}

func TestProducerAnchorTrackingIsExplicitAndConflictsAreRejected(t *testing.T) {
	// R1 anchor fixture covers a tracking key, explicit null tracking, and
	// incompatible complete support for one semantic entity.
	description := facts.ProducerDescription{
		Namespace: "catalog", ImplementationID: "fixture.catalog",
		ImplementationStamp: "fixture-v1", ContractVersion: "fixture-contract/1",
		EffectiveConfiguration: map[string]any{},
		Schema:                 facts.Schema{Declarations: `Decl item(Entity) descr [extensional(), arg(Entity, "entity id")].`},
		BaseRelations: []facts.RelationDescription{{
			Predicate: facts.Predicate{Name: "catalog.item", Arity: 1},
			Arguments: []facts.ArgumentDescription{{
				Name: "Entity", Representations: []facts.ValueKind{facts.StringKind},
				Role: facts.EntityRole, ReportSupport: facts.ReportRequired,
			}},
		}},
	}
	requested := []facts.Predicate{{Name: "catalog.item", Arity: 1}}
	location := diagnostics.SourceLocation{File: "catalog/object.sql", StartLine: 2, StartColumn: 1, EndLine: 2, EndColumn: 8}
	keyed := facts.AnchorSupport{
		Location: location, Description: "catalog object",
		Tracking: &facts.TrackingKey{Scheme: "catalog.object/1", Data: map[string]any{"name": "users"}},
	}
	nullTracking := facts.AnchorSupport{Location: location, Description: "catalog object without a stable key"}
	descriptor, err := facts.ValidateDescription(description)
	if err != nil {
		t.Fatal(err)
	}
	build := func(id string, support facts.AnchorSupport) (facts.ValidatedFacts, error) {
		builder, err := facts.NewBuilder(descriptor, requested)
		if err != nil {
			return facts.ValidatedFacts{}, err
		}
		relation, err := builder.Relation("item", 1)
		if err != nil {
			return facts.ValidatedFacts{}, err
		}
		builder.Add(relation, facts.String(id))
		builder.Anchor(id, support)
		return builder.Finish()
	}
	keyedFacts, err := build("users", keyed)
	if err != nil {
		t.Fatal(err)
	}
	nullFacts, err := build("unstable", nullTracking)
	if err != nil {
		t.Fatal(err)
	}
	if got := keyedFacts.Anchors()["catalog:users"].Tracking; got == nil || got.Scheme != "catalog.object/1" {
		t.Fatalf("keyed support lost its tracking key: %+v", got)
	}
	if got := nullFacts.Anchors()["catalog:unstable"].Tracking; got != nil {
		t.Fatalf("explicit null tracking was inferred as a key: %+v", got)
	}
	invalidSupports := []struct {
		name    string
		support facts.AnchorSupport
		message string
	}{
		{name: "empty description", support: facts.AnchorSupport{Location: location, Description: "  "}, message: "description is empty"},
		{name: "invalid location", support: facts.AnchorSupport{Location: diagnostics.SourceLocation{File: "catalog/../object.sql", StartLine: 1, StartColumn: 1, EndLine: 1, EndColumn: 2}, Description: "catalog object"}, message: "location must use positive"},
		{name: "empty tracking scheme", support: facts.AnchorSupport{Location: location, Description: "catalog object", Tracking: &facts.TrackingKey{Data: map[string]any{"name": "users"}}}, message: "tracking scheme is empty"},
		{name: "invalid tracking data", support: facts.AnchorSupport{Location: location, Description: "catalog object", Tracking: &facts.TrackingKey{Scheme: "catalog.object/1", Data: map[string]any{"rank": float64(1.5)}}}, message: "invalid tracking data"},
	}
	for _, item := range invalidSupports {
		t.Run(item.name, func(t *testing.T) {
			if _, err := build("invalid", item.support); err == nil || !strings.Contains(err.Error(), item.message) {
				t.Fatalf("expected anchor validation failure containing %q, got %v", item.message, err)
			}
		})
	}
}

func TestBoundProducerOwnershipAndConfigurationIdentity(t *testing.T) {
	valid := newParallelFixtureProducer()
	invalid := newParallelFixtureProducer()
	invalid.description.Namespace = "invalid-namespace"
	if session, err := Open(Config{}, valid, invalid); err == nil || session != nil || valid.closed.Load() || invalid.closed.Load() {
		t.Fatal("failed Open took ownership of supplied producers")
	}
	snapshots := make(map[string]string)
	for _, tc := range []struct {
		kind  string
		shape any
	}{
		{"null", []any(nil)}, {"null", map[string]any(nil)}, {"array", []any{}}, {"object", map[string]any{}},
	} {
		valid.description.EffectiveConfiguration = map[string]any{"shape": []any{tc.shape}}
		session, err := Open(Config{}, valid)
		if err != nil {
			t.Fatal(err)
		}
		delete(valid.description.EffectiveConfiguration.(map[string]any), "shape")
		facts, err := session.catalog.collect(context.Background(), "", nil, nil, observe.Full)
		if err != nil {
			t.Fatal(err)
		}
		if previous, exists := snapshots[facts.snapshotID]; exists && previous != tc.kind {
			t.Fatalf("configuration shapes collapsed: %s and %s", previous, tc.kind)
		}
		snapshots[facts.snapshotID] = tc.kind
		if err := session.Close(); err != nil || !valid.closed.Load() {
			t.Fatalf("closing owned producer: %v", err)
		}
		valid = newParallelFixtureProducer()
	}
	if len(snapshots) != 3 {
		t.Fatalf("null/array/object identity: %v", snapshots)
	}
}

func TestDocumentNamesAndLookup(t *testing.T) {
	root := t.TempDir()
	outside := filepath.Join(t.TempDir(), "item.ts")
	inside := filepath.Join(root, "item.ts")
	a, b := strings.Repeat("a", 64), strings.Repeat("b", 64)
	metadata := backend.GenerationMetadata{InputRevision: "1", SourceDigests: map[string]string{inside: a, outside: b}}
	digests := make(map[string]string)
	if _, _, err := captureGenerationMetadata(metadata, root, "p", digests, map[string]map[string]struct{}{}); err != nil {
		t.Fatal(err)
	}
	if digests["item.ts"] != a || digests[filepath.ToSlash(outside)] != b {
		t.Fatalf("digests are not keyed by document: %v", digests)
	}
	diag := func(file string) diagnostics.Diagnostic {
		return diagnostics.Diagnostic{SourceLocation: &diagnostics.SourceLocation{File: file}, Message: file}
	}
	result := newPublishedResult(Result{AnalyzedFiles: []string{"item.ts", "no-findings.ts"}, Diagnostics: []diagnostics.Diagnostic{diag("item.ts"), diag(filepath.ToSlash(outside))}}, root, digests, observe.Set{}, true)
	for _, file := range []string{"item.ts", "./item.ts", inside, outside} {
		if len(result.result.DiagnosticsFor(file)) != 1 {
			t.Fatalf("no findings for %s", file)
		}
	}
	all := result.project(nil, true)
	if !slices.Equal(groupFiles(all), []string{filepath.ToSlash(outside), "item.ts", "no-findings.ts"}) || !slices.Equal(groupDigests(all), []string{b, a, ""}) {
		t.Fatalf("all documents: %+v", all)
	}
	metadata.SourceDigests[inside] = b
	if _, _, err := captureGenerationMetadata(metadata, root, "p", digests, map[string]map[string]struct{}{}); err == nil {
		t.Fatal("conflicting identity accepted")
	}
}

func TestSourceDigestRequiresRangeOwner(t *testing.T) {
	root := t.TempDir()
	digests, owners := map[string]string{}, map[string]map[string]struct{}{}
	current := strings.Repeat("c", 64)
	if _, _, err := captureGenerationMetadata(backend.GenerationMetadata{InputRevision: "1", SourceDigests: map[string]string{"a.ts": current}}, root, "b", digests, owners); err != nil {
		t.Fatal(err)
	}
	finding := func(anchor string) diagnostics.Diagnostic {
		return diagnostics.Diagnostic{SourceLocation: &diagnostics.SourceLocation{File: "a.ts", StartLine: 2}, EntityIdentity: &diagnostics.EntityIdentity{SemanticKey: anchor}}
	}
	var result Result
	evidence := &executionEvidence{sourceDigests: digests, digestOwners: owners, anchorOwners: map[string]string{"a:x": "a", "b:x": "b"}}
	result.Diagnostics = []diagnostics.Diagnostic{finding("b:x")}
	if got := verifiedSourceDigests(result, root, evidence)["a.ts"]; got != current {
		t.Fatalf("owner-captured digest dropped: %q", got)
	}
	result.Diagnostics = append(result.Diagnostics, finding("a:x"))
	if got, ok := verifiedSourceDigests(result, root, evidence)["a.ts"]; ok {
		t.Fatalf("range from producer without a capture was verified by another producer's digest %q", got)
	}
}

// movingProducer retargets the workspace link while it prepares, as a
// concurrent change might, and records the root it was given.
type movingProducer struct {
	*parallelFixtureProducer
	move  func()
	roots []string
}

func (producer *movingProducer) Prepare(ctx context.Context, request backend.CollectionRequest, mode observe.Mode) (backend.Generation, error) {
	producer.roots = append(producer.roots, request.WorkspaceRoot)
	if producer.move != nil {
		producer.move()
	}
	return producer.parallelFixtureProducer.Prepare(ctx, request, mode)
}

func TestOperationUsesOneWorkspace(t *testing.T) {
	repo, err := filepath.Abs("..")
	if err != nil {
		t.Fatal(err)
	}
	parent := t.TempDir()
	program := "Decl violation(Entity, Code). violation(Entity, Code) :- catalog.usable(Entity, Code)."
	physical := map[string]string{}
	for _, name := range []string{"a", "b"} {
		workspace := filepath.Join(parent, name)
		writeTestFile(t, workspace, "lintai-rules/root.ts", parallelRuleSource("fixture.root", program, `(row) => "`+name+`:" + row.Code`))
		parallelFixtureOptions(t, repo, workspace)
		if physical[name], err = filepath.EvalSymlinks(workspace); err != nil {
			t.Fatal(err)
		}
	}
	link := filepath.Join(parent, "current")
	producer := &movingProducer{parallelFixtureProducer: newParallelFixtureProducer()}
	// A session needs an existing directory as its root.
	for _, root := range []string{link, filepath.Join(parent, "a", "lintai-rules", "root.ts")} {
		if _, err := Open(Config{WorkspaceRoot: root}, producer); err == nil {
			t.Fatalf("session opened at %s", root)
		}
	}
	if err := os.Symlink(filepath.Join(parent, "a"), link); err != nil {
		t.Skip(err)
	}
	producer.move = func() {
		producer.move = nil
		if err := os.Remove(link); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(filepath.Join(parent, "b"), link); err != nil {
			t.Fatal(err)
		}
	}
	// The absolute suppressions path names the workspace through the link.
	session := openTestSession(t, Config{WorkspaceRoot: link, SuppressionsLocation: filepath.Join(link, suppressions.DefaultFile)}, producer)
	messages := func(items []diagnostics.Diagnostic) []string {
		var result []string
		for _, item := range items {
			result = append(result, item.Message)
		}
		return result
	}

	// The link moves to b while the producer works. The result and the write
	// belong to a, and a later operation of the session stays on a.
	result, err := session.UpdateSuppressions(context.Background(), SuppressionUpdate{All: true})
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(messages(result.Suppressed), []string{"a:a", "a:b", "a:c"}) || result.SuppressionsPath != filepath.Join(physical["a"], suppressions.DefaultFile) {
		t.Fatalf("expected workspace a only: %+v", result)
	}
	if _, err := os.Stat(filepath.Join(physical["b"], suppressions.DefaultFile)); !os.IsNotExist(err) {
		t.Fatalf("suppressions written into b: %v", err)
	}
	refreshed, err := session.RefreshAll(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if refreshed.PhysicalRoot != physical["a"] || !slices.Equal(producer.roots, []string{physical["a"], physical["a"]}) {
		t.Fatalf("expected workspace a, got root %s and producer roots %v", refreshed.PhysicalRoot, producer.roots)
	}
	if _, err := session.ApproveRules(context.Background(), ""); err != nil {
		t.Fatal(err)
	}
	if _, err := ReadRulesLock(filepath.Join(physical["a"], DefaultRulesLockFile)); err != nil {
		t.Fatalf("lock not written into a: %v", err)
	}
	if _, err := os.Stat(filepath.Join(physical["b"], DefaultRulesLockFile)); !os.IsNotExist(err) {
		t.Fatalf("lock written into b: %v", err)
	}
}

// documentProducer reports its anchors under one file name, declares the
// document that name reached and the digest of the text it read.
type documentProducer struct {
	*parallelFixtureProducer
	file, document, digest string
}

func (producer *documentProducer) Prepare(ctx context.Context, request backend.CollectionRequest, mode observe.Mode) (backend.Generation, error) {
	generation, err := producer.parallelFixtureProducer.Prepare(ctx, request, mode)
	if err != nil {
		return nil, err
	}
	prepared := generation.(*parallelGeneration)
	prepared.metadata.InputRevision += producer.document + producer.digest
	prepared.metadata.AnalyzedFiles = []string{producer.file}
	prepared.metadata.SourceDigests = map[string]string{producer.file: producer.digest}
	if producer.document != "" {
		prepared.metadata.Documents = map[string]string{producer.file: producer.document}
	}
	return prepared, nil
}

func TestProducersReportingOneDocumentJoin(t *testing.T) {
	repo, err := filepath.Abs("..")
	if err != nil {
		t.Fatal(err)
	}
	workspace := t.TempDir()
	for _, namespace := range []string{"catalog", "other"} {
		program := "Decl violation(Entity, Code). violation(Entity, Code) :- " + namespace + ".usable(Entity, Code)."
		writeTestFile(t, workspace, "lintai-rules/"+namespace+".ts", parallelRuleSource("fixture."+namespace, program, "(row) => row.Code"))
	}
	options := parallelFixtureOptions(t, repo, workspace)
	physical, err := filepath.EvalSymlinks(workspace)
	if err != nil {
		t.Fatal(err)
	}
	digest := strings.Repeat("d", 64)
	catalog := &documentProducer{parallelFixtureProducer: newParallelFixtureProducer(), file: "catalog/items.sql", digest: digest}
	// The second producer reached the same file through a link.
	other := &documentProducer{parallelFixtureProducer: newParallelFixtureProducer(), file: "link/items.sql", document: filepath.Join(physical, "catalog", "items.sql"), digest: digest}
	other.description.Namespace = "other"
	other.description.BaseRelations[0].Predicate.Name = "other.item"
	other.description.Helpers[0].Predicate.Name = "other.usable"
	other.description.Schema.Prelude = strings.ReplaceAll(other.description.Schema.Prelude, "catalog.item", "other.item")
	for id, support := range other.anchors {
		support.Location.File = "link/items.sql"
		other.anchors[id] = support
	}
	session := openTestSession(t, options, catalog, other)
	result, err := session.RefreshAll(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(groupFiles(result), []string{"catalog/items.sql"}) || len(result.Files[0].Diagnostics) != 6 || result.Files[0].SourceDigest != digest {
		t.Fatalf("expected one group for the document: %+v", result.Files)
	}
	checked, err := session.Check(context.Background())
	if err != nil || len(checked.DiagnosticsFor(filepath.Join(physical, "catalog", "items.sql"))) != 6 || slices.ContainsFunc(checked.AnalyzedFiles, func(file string) bool { return file != "catalog/items.sql" }) {
		t.Fatalf("lookup by the physical path: %+v, %v", checked, err)
	}
	other.digest = strings.Repeat("e", 64)
	if _, err := session.RefreshAll(context.Background()); err == nil || !strings.Contains(err.Error(), "conflicting source identities") {
		t.Fatalf("conflicting text claims were accepted: %v", err)
	}
	other.digest, other.document = digest, "catalog/items.sql"
	if _, err := session.RefreshAll(context.Background()); err == nil || !strings.Contains(err.Error(), "not an absolute path") {
		t.Fatalf("relative document accepted: %v", err)
	}
}

func groupFiles(result FilesResult) []string {
	files := make([]string, len(result.Files))
	for i, group := range result.Files {
		files[i] = group.File
	}
	return files
}

func groupDigests(result FilesResult) []string {
	digests := make([]string, len(result.Files))
	for i, group := range result.Files {
		digests[i] = group.SourceDigest
	}
	return digests
}
