package engine

import (
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

func qualifyTestFacts(t *testing.T, items []facts.Fact) ([]facts.Fact, map[string]facts.AnchorSupport, map[facts.Predicate]map[string]struct{}) {
	t.Helper()
	description, err := typescript.New().Describe()
	if err != nil {
		t.Fatal(err)
	}
	metadata := make(map[string]facts.RelationDescription, len(description.BaseRelations))
	for _, relation := range description.BaseRelations {
		metadata[relation.Predicate.Name] = relation
	}
	qualified := make([]facts.Fact, len(items))
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
		qualified[i] = facts.Fact{Relation: name, Values: row}
		if item.Relation == facts.LocationRelation && len(row) == 6 {
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
	batchFor := func(id string, support facts.AnchorSupport) facts.Batch {
		return facts.Batch{
			Relations: map[facts.Predicate][]facts.Row{requested[0]: {{facts.String(id)}}},
			Anchors:   map[string]facts.AnchorSupport{id: support},
		}
	}
	keyedFacts, err := facts.ValidateBatch(description, requested, batchFor("catalog:users", keyed))
	if err != nil {
		t.Fatal(err)
	}
	nullFacts, err := facts.ValidateBatch(description, requested, batchFor("catalog:unstable", nullTracking))
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
			if _, err := facts.ValidateBatch(description, requested, batchFor("catalog:invalid", item.support)); err == nil || !strings.Contains(err.Error(), item.message) {
				t.Fatalf("expected anchor validation failure containing %q, got %v", item.message, err)
			}
		})
	}
	conflict := keyed
	conflict.Tracking = &facts.TrackingKey{Scheme: "catalog.object/1", Data: map[string]any{"name": "accounts"}}
	if sameAnchor(keyed, conflict) {
		t.Fatal("conflicting complete support for the same entity was accepted")
	}
}
