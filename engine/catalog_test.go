package engine

import (
	"context"
	"github.com/bigbizze/lintai/backend"
	"github.com/bigbizze/lintai/observe"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"codeberg.org/TauCeti/mangle-go/ast"
	"codeberg.org/TauCeti/mangle-go/parse"
	"github.com/bigbizze/lintai/backend/typescript"
	"github.com/bigbizze/lintai/diagnostics"
	"github.com/bigbizze/lintai/facts"
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

func TestOperationFileKeysUseTheProducersRootResolution(t *testing.T) {
	parent := t.TempDir()
	for _, name := range []string{"a", "b"} {
		if err := os.Mkdir(filepath.Join(parent, name), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	link := filepath.Join(parent, "current")
	if err := os.Symlink(filepath.Join(parent, "a"), link); err != nil {
		t.Skip(err)
	}
	read, err := filepath.EvalSymlinks(filepath.Join(parent, "a"))
	if err != nil {
		t.Fatal(err)
	}
	observed, err := observe.New([]observe.Record{{Kind: observe.Resolved, Path: link, Value: observe.Value(read)}})
	if err != nil {
		t.Fatal(err)
	}
	initial := newFileKeys(link, nil)
	if err := os.Remove(link); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(parent, "b"), link); err != nil {
		t.Fatal(err)
	}
	keys, err := operationFileKeys(initial, []observe.Set{observed}, func(set observe.Set) observe.Set { return set })
	if err != nil {
		t.Fatal(err)
	}
	if got := keys.key(filepath.Join(read, "x.ts")); got != "x.ts" {
		t.Fatalf("file the producer read -> %s", got)
	}
	moved, err := filepath.EvalSymlinks(filepath.Join(parent, "b"))
	if err != nil {
		t.Fatal(err)
	}
	if got := keys.key(filepath.Join(moved, "x.ts")); got == "x.ts" {
		t.Fatal("file in the newly linked workspace matched the published result")
	}
	other, err := observe.New([]observe.Record{{Kind: observe.Resolved, Path: link, Value: observe.Value(moved)}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := operationFileKeys(initial, []observe.Set{observed, other}, func(set observe.Set) observe.Set { return set }); err == nil {
		t.Fatal("producers that read different workspaces were composed")
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

func TestResolveFileAliasesIsOrderIndependentAndRejectsAmbiguity(t *testing.T) {
	root := t.TempDir()
	keys := fileKeys{root: root, physical: root}
	analyzed := map[string]struct{}{"a/shared.ts": {}, "b/shared.ts": {}}
	declare := func(namespace, alias, file string) aliasDeclaration {
		return aliasDeclaration{namespace, alias, file}
	}
	coalesced := []aliasDeclaration{declare("p", "link.ts", "a/shared.ts"), declare("q", "link.ts", "a/shared.ts"), declare("q", "a/shared.ts", "a/shared.ts")}
	for _, declarations := range [][]aliasDeclaration{coalesced, {coalesced[2], coalesced[1], coalesced[0]}} {
		got, err := resolveFileAliases(keys, slices.Clone(declarations), analyzed)
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
			if _, err := resolveFileAliases(keys, slices.Clone(order), analyzed); err == nil {
				t.Errorf("%s: expected an error", name)
			}
		}
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
