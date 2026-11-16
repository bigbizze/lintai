package facts

import "testing"

func TestFormatFactsUsesQualifiedCatalog(t *testing.T) {
	items := []Fact{
		{Relation: "catalog.object", Values: []Value{String("catalog:orders"), Name("collection")}},
		{Relation: "catalog.object", Values: []Value{String("catalog:orders"), Name("collection")}},
	}
	description, err := DescribeSchema("catalog", "catalog.fixture", "fixture-v1", "fixture/1", nil, Schema{
		Declarations: `Decl object(Entity, Kind) descr [extensional(), arg_representation(Kind, "name")].`,
	})
	if err != nil {
		t.Fatal(err)
	}
	predicate := Predicate{Name: "catalog.object", Arity: 2}
	if _, err := ValidateBatch(description, []Predicate{predicate}, Batch{
		Relations: map[Predicate][]Row{predicate: {Row(items[0].Values)}},
	}); err != nil {
		t.Fatalf("an open name vocabulary rejected a valid name: %v", err)
	}
	allowed := []string{"catalog.object"}
	formatted, err := FormatFacts(items, allowed, nil)
	if err != nil {
		t.Fatal(err)
	}
	if want := `catalog.object("catalog:orders",/collection).`; formatted != want {
		t.Fatalf("formatted facts = %q, want %q", formatted, want)
	}
	if _, err := FormatFacts(items, allowed, []string{"object"}); err == nil {
		t.Fatal("expected an unqualified relation to be rejected")
	}
}
