package engine

import (
	"context"
	"maps"
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
		facts, err := session.catalog.collect(context.Background(), fileKeys{}, nil, nil, observe.Full)
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

func TestSourceIdentityUsesDiagnosticFileMapping(t *testing.T) {
	root := t.TempDir()
	outside := filepath.Join(t.TempDir(), "item.ts")
	inside := filepath.Join(root, "item.ts")
	relative := strings.TrimLeft(filepath.ToSlash(outside), "/")
	if volume := filepath.VolumeName(outside); volume != "" {
		relative = strings.TrimLeft(strings.TrimPrefix(filepath.ToSlash(outside), volume), "/")
	}
	a, b := strings.Repeat("a", 64), strings.Repeat("b", 64)
	metadata := backend.GenerationMetadata{InputRevision: "1", SourceDigests: map[string]string{inside: a, outside: b}}
	digests := make(map[string]string)
	if _, err := captureGenerationMetadata(metadata, fileKeys{root: root, physical: root}, "p", digests, map[string]map[string]struct{}{}); err != nil {
		t.Fatal(err)
	}
	metadata.SourceDigests[inside] = b
	diag := func(file string) diagnostics.Diagnostic {
		return diagnostics.Diagnostic{SourceLocation: &diagnostics.SourceLocation{File: file}, Message: file}
	}
	result := Result{files: fileKeys{root: root, physical: root}, AnalyzedFiles: []string{"item.ts", "no-findings.ts"}, Diagnostics: []diagnostics.Diagnostic{diag(inside), diag(filepath.ToSlash(outside))}}
	p := newPublishedResult(result, digests, observe.Set{}, true)
	files := []string{"./item.ts", outside, relative, inside}
	projected := p.projectFiles(files, false)
	if !slices.Equal(groupFiles(projected), files) || !slices.Equal(groupDigests(projected), []string{a, b, "", a}) || len(projected.Files[2].Diagnostics) != 0 {
		t.Fatalf("projected: %+v", projected)
	}
	for i, file := range files {
		if !slices.EqualFunc(result.DiagnosticsFor(file), projected.Files[i].Diagnostics, func(a, b diagnostics.Diagnostic) bool { return a.Message == b.Message }) {
			t.Fatalf("inconsistent grouping for %s", file)
		}
	}
	if len(p.projectFiles(nil, true).Files) != 3 {
		t.Fatal("all must include analyzed and diagnostic-bearing groups")
	}
	if _, err := captureGenerationMetadata(metadata, fileKeys{root: root, physical: root}, "p", digests, map[string]map[string]struct{}{}); err == nil {
		t.Fatal("conflicting identity accepted")
	}
}

func TestSourceDigestRequiresRangeOwner(t *testing.T) {
	root := t.TempDir()
	digests, owners := map[string]string{}, map[string]map[string]struct{}{}
	current := strings.Repeat("c", 64)
	if _, err := captureGenerationMetadata(backend.GenerationMetadata{InputRevision: "1", SourceDigests: map[string]string{"a.ts": current}}, fileKeys{root: root, physical: root}, "b", digests, owners); err != nil {
		t.Fatal(err)
	}
	finding := func(anchor string) diagnostics.Diagnostic {
		return diagnostics.Diagnostic{SourceLocation: &diagnostics.SourceLocation{File: "a.ts", StartLine: 2}, EntityIdentity: &diagnostics.EntityIdentity{SemanticKey: anchor}}
	}
	result := Result{files: fileKeys{root: root, physical: root}}
	evidence := &executionEvidence{sourceDigests: digests, digestOwners: owners, anchorOwners: map[string]string{"a:x": "a", "b:x": "b"}}
	result.Diagnostics = []diagnostics.Diagnostic{finding("b:x")}
	if got := verifiedSourceDigests(result, evidence)["a.ts"]; got != current {
		t.Fatalf("owner-captured digest dropped: %q", got)
	}
	result.Diagnostics = append(result.Diagnostics, finding("a:x"))
	if got, ok := verifiedSourceDigests(result, evidence)["a.ts"]; ok {
		t.Fatalf("range from producer without a capture was verified by another producer's digest %q", got)
	}
}

func TestFileKeysAcceptEverySourceSpelling(t *testing.T) {
	physical := t.TempDir()
	link := filepath.Join(t.TempDir(), "link")
	if err := os.Symlink(physical, link); err != nil {
		t.Skip(err)
	}
	keys := newFileKeys(link, map[string]string{"alias/a.js": "src/a.js"})
	for _, spelling := range []string{"src/a.js", "alias/a.js", filepath.Join(link, "src", "a.js"), filepath.Join(link, "alias", "a.js")} {
		if got := keys.key(spelling); got != "src/a.js" {
			t.Fatalf("%s -> %s", spelling, got)
		}
	}
	resolved, err := filepath.EvalSymlinks(physical)
	if err != nil {
		t.Fatal(err)
	}
	if got := keys.key(filepath.Join(resolved, "src", "a.js")); got != "src/a.js" {
		t.Fatalf("physical spelling -> %s", got)
	}

	// The configured root is a symlink inside its target, and a finding is in
	// a file outside the workspace. A request keeps its spellings, order and
	// duplicates, and every spelling of one file finds the same findings.
	nested := filepath.Join(physical, "current")
	if err := os.Symlink(physical, nested); err != nil {
		t.Fatal(err)
	}
	located := func(file string) diagnostics.Diagnostic {
		return diagnostics.Diagnostic{SourceLocation: &diagnostics.SourceLocation{File: file}}
	}
	result := Result{files: newFileKeys(nested, nil), Diagnostics: []diagnostics.Diagnostic{located("src/a.js"), located("../deps/shared.ts")}}
	shared := filepath.Join(filepath.Dir(resolved), "deps", "shared.ts")
	requested := []string{"src/a.js", filepath.Join(nested, "src", "a.js"), filepath.Join(resolved, "src", "a.js"), "../deps/shared.ts", shared, "src/a.js"}
	published := newPublishedResult(result, nil, observe.Set{}, true)
	projected := published.projectFiles(requested, false)
	if !slices.Equal(groupFiles(projected), requested) {
		t.Fatalf("requested spellings not kept: %v", groupFiles(projected))
	}
	for i, group := range projected.Files {
		if len(group.Diagnostics) != 1 || len(result.DiagnosticsFor(requested[i])) != 1 {
			t.Fatalf("%s: %d findings", group.File, len(group.Diagnostics))
		}
	}
	all := published.projectFiles(nil, true)
	if want := slices.Sorted(slices.Values([]string{"src/a.js", filepath.ToSlash(shared)})); !slices.Equal(groupFiles(all), want) || len(all.Files[0].Diagnostics) != 1 || len(all.Files[1].Diagnostics) != 1 {
		t.Fatalf("all files: %+v", all.Files)
	}
}

func TestSourceDigestUsesOneFileKeyForEverySpelling(t *testing.T) {
	root := t.TempDir()
	keys := fileKeys{root: root, physical: root, aliases: map[string]string{"alias/a.ts": "src/a.ts"}}
	old, current := strings.Repeat("a", 64), strings.Repeat("c", 64)
	digests, owners := map[string]string{}, map[string]map[string]struct{}{}
	if _, err := captureGenerationMetadata(backend.GenerationMetadata{InputRevision: "1", SourceDigests: map[string]string{"alias/a.ts": old, "src/a.ts": current}}, keys, "p", digests, owners); err == nil {
		t.Fatal("two captures of one file were accepted")
	}
	digests, owners = map[string]string{}, map[string]map[string]struct{}{}
	if _, err := captureGenerationMetadata(backend.GenerationMetadata{InputRevision: "1", SourceDigests: map[string]string{"alias/a.ts": current}}, keys, "p", digests, owners); err != nil {
		t.Fatal(err)
	}
	result := Result{files: keys, Diagnostics: []diagnostics.Diagnostic{{SourceLocation: &diagnostics.SourceLocation{File: "src/a.ts", StartLine: 1}, EntityIdentity: &diagnostics.EntityIdentity{SemanticKey: "p:x"}}}}
	evidence := &executionEvidence{sourceDigests: digests, digestOwners: owners, anchorOwners: map[string]string{"p:x": "p"}}
	if got := verifiedSourceDigests(result, evidence)["src/a.ts"]; got != current {
		t.Fatalf("alias-supplied digest not verified for its canonical file: %q", got)
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
	if err := os.Symlink(filepath.Join(parent, "a"), link); err != nil {
		t.Skip(err)
	}
	producer := &movingProducer{parallelFixtureProducer: newParallelFixtureProducer()}
	producer.move = func() {
		producer.move = nil
		if err := os.Remove(link); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(filepath.Join(parent, "b"), link); err != nil {
			t.Fatal(err)
		}
	}
	session := openTestSession(t, Config{WorkspaceRoot: link}, producer)
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

func TestProjectAllOmitsIdentityAliases(t *testing.T) {
	root := t.TempDir()
	result := Result{files: fileKeys{root: root, physical: root, aliases: map[string]string{"a.ts": "a.ts"}}, AnalyzedFiles: []string{"a.ts"}}
	projected := newPublishedResult(result, nil, observe.Set{}, true).projectFiles(nil, true)
	if !slices.Equal(groupFiles(projected), []string{"a.ts"}) || len(projected.Aliases) != 0 {
		t.Fatalf("files: %v", projected.Files)
	}
}

type aliasValidationProducer struct {
	*parallelFixtureProducer
	analyzed []string
	aliases  map[string]string
}

func (producer *aliasValidationProducer) Prepare(ctx context.Context, request backend.CollectionRequest, mode observe.Mode) (backend.Generation, error) {
	generation, err := producer.parallelFixtureProducer.Prepare(ctx, request, mode)
	if err != nil {
		return nil, err
	}
	prepared := generation.(*parallelGeneration)
	prepared.metadata.AnalyzedFiles = slices.Clone(producer.analyzed)
	prepared.metadata.FileAliases = maps.Clone(producer.aliases)
	return prepared, nil
}

func TestResolveFileAliasesIsOrderIndependentAndRejectsAmbiguity(t *testing.T) {
	root := t.TempDir()
	keys := fileKeys{root: root, physical: root}
	analyzed := map[string]struct{}{"a/shared.ts": {}, "b/shared.ts": {}}
	declare := func(namespace, alias, file string) aliasDeclaration {
		return aliasDeclaration{namespace, alias, file}
	}
	coalesced := []aliasDeclaration{declare("p", "link.ts", "a/shared.ts"), declare("q", "link.ts", "a/shared.ts"), declare("q", "a/shared.ts", "a/shared.ts")}
	for _, declarations := range [][]aliasDeclaration{coalesced, {coalesced[2], coalesced[1], coalesced[0]}} {
		got, err := resolveFileAliases(keys, slices.Clone(declarations), analyzed, false)
		if err != nil || len(got) != 1 || got["link.ts"] != "a/shared.ts" {
			t.Fatalf("aliases = %v, %v", got, err)
		}
	}
	for name, declarations := range map[string][]aliasDeclaration{
		"conflict": {declare("p", "shared.ts", "a/shared.ts"), declare("q", "shared.ts", "b/shared.ts")},
		"chain":    {declare("p", "x.ts", "y.ts"), declare("q", "y.ts", "a/shared.ts")},
		"cycle":    {declare("p", "x.ts", "y.ts"), declare("q", "y.ts", "x.ts")},
		"shadow":   {declare("p", "b/shared.ts", "a/shared.ts")},
	} {
		for _, order := range [][]aliasDeclaration{declarations, {declarations[len(declarations)-1], declarations[0]}} {
			if _, err := resolveFileAliases(keys, slices.Clone(order), analyzed, false); err == nil {
				t.Errorf("%s: expected an error", name)
			}
		}
	}

	t.Run("shadow of a diagnostic range outside analyzed files", func(t *testing.T) {
		repo, err := filepath.Abs("..")
		if err != nil {
			t.Fatal(err)
		}
		workspace := t.TempDir()
		program := "Decl violation(Entity, Code). violation(Entity, Code) :- catalog.usable(Entity, Code), other.item(_, Code)."
		writeTestFile(t, workspace, "lintai-rules/alias.ts", parallelRuleSource("fixture.alias", program, "(row) => row.Code"))

		catalog := &aliasValidationProducer{parallelFixtureProducer: newParallelFixtureProducer()}
		other := &aliasValidationProducer{
			parallelFixtureProducer: newParallelFixtureProducer(),
			analyzed:                []string{"other.sql"},
			aliases:                 map[string]string{"catalog/items.sql": "other.sql"},
		}
		other.description.Namespace = "other"
		other.description.BaseRelations[0].Predicate.Name = "other.item"
		other.description.Helpers[0].Predicate.Name = "other.usable"
		other.description.Schema.Prelude = strings.ReplaceAll(other.description.Schema.Prelude, "catalog.item", "other.item")
		for id, support := range other.anchors {
			support.Location.File = "other.sql"
			other.anchors[id] = support
		}
		session := openTestSession(t, parallelFixtureOptions(t, repo, workspace), catalog, other)
		assertShadow := func() {
			t.Helper()
			result, err := session.RefreshAll(context.Background())
			if err == nil || !strings.Contains(err.Error(), `producer "other"`) || !strings.Contains(err.Error(), "catalog/items.sql") || !strings.Contains(err.Error(), "other.sql") || len(result.Files) != 0 {
				t.Fatalf("expected a producer error rejecting the diagnostic file alias, got %+v, %v", result, err)
			}
		}

		assertShadow()
		other.aliases = nil
		result, err := session.RefreshAll(context.Background())
		if err != nil || !slices.Equal(groupFiles(result), []string{"catalog/items.sql", "other.sql"}) || len(result.Files[0].Diagnostics) != 3 || len(result.WorkspaceProblems) != 0 {
			t.Fatalf("expected the unaliased diagnostic file after recovery, got %+v, %v", result, err)
		}
		catalogEmits, otherEmits := catalog.emitCalls.Load(), other.emitCalls.Load()
		other.aliases = map[string]string{"catalog/items.sql": "other.sql"}
		assertShadow()
		if catalog.emitCalls.Load() != catalogEmits || other.emitCalls.Load() != otherEmits {
			t.Fatal("unchanged generation should reuse cached anchors when validating aliases")
		}
		for name, aliases := range map[string]map[string]string{
			"empty alias":                      {"": "other.sql"},
			"empty destination":                {"link.sql": ""},
			"destination is not an input file": {"link.sql": "missing.sql"},
		} {
			other.aliases = aliases
			if result, err := session.RefreshAll(context.Background()); err == nil || !strings.Contains(err.Error(), `producer "other"`) || len(result.Files) != 0 {
				t.Fatalf("%s: expected a producer error, got %+v, %v", name, result, err)
			}
		}
		other.aliases = map[string]string{"link.sql": "other.sql"}
		if result, err := session.RefreshAll(context.Background()); err != nil || result.Aliases["link.sql"] != "other.sql" {
			t.Fatalf("expected the alias of an analyzed file, got %+v, %v", result, err)
		}
	})
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
