package facts

import "testing"

func TestFormatFactsUsesQualifiedCatalog(t *testing.T) {
	items := []Fact{
		{Relation: "catalog.object", Values: []Value{String("catalog:orders"), Name("collection")}},
		{Relation: "catalog.object", Values: []Value{String("catalog:orders"), Name("collection")}},
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
