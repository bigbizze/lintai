package facts

import (
	"reflect"
	"testing"
)

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

func TestValidatedScopeIsDetached(t *testing.T) {
	description, err := DescribeSchema("catalog", "catalog.fixture", "fixture-v1", "fixture/1", nil, Schema{
		Declarations: `Decl object(Entity) descr [extensional()].`,
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name   string
		scope  any
		want   any
		mutate func(any)
	}{
		{
			name: "root string slice", scope: []string{"original"}, want: []string{"original"},
			mutate: func(value any) { value.([]string)[0] = "changed" },
		},
		{
			name: "array", scope: []any{[]string{"first"}, map[string]any{"nested": []string{"second"}}},
			want: []any{[]string{"first"}, map[string]any{"nested": []string{"second"}}},
			mutate: func(value any) {
				array := value.([]any)
				array[0].([]string)[0] = "changed"
				object := array[1].(map[string]any)
				object["nested"].([]string)[0] = "changed"
				object["new"] = true
			},
		},
		{
			name: "map", scope: map[string]any{"strings": []string{"first"}, "array": []any{[]string{"second"}, map[string]any{"value": []string{"third"}}}},
			want: map[string]any{"strings": []string{"first"}, "array": []any{[]string{"second"}, map[string]any{"value": []string{"third"}}}},
			mutate: func(value any) {
				object := value.(map[string]any)
				object["strings"].([]string)[0] = "changed"
				array := object["array"].([]any)
				array[0].([]string)[0] = "changed"
				array[1].(map[string]any)["value"].([]string)[0] = "changed"
				array[1] = "replaced"
				object["new"] = true
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			validated, err := ValidateBatch(description, nil, Batch{Scope: tc.scope})
			if err != nil {
				t.Fatal(err)
			}
			tc.mutate(tc.scope)
			first := validated.Scope()
			if !reflect.DeepEqual(first, tc.want) {
				t.Fatalf("producer mutation affected scope: got %#v, want %#v", first, tc.want)
			}
			tc.mutate(first)
			if again := validated.Scope(); !reflect.DeepEqual(again, tc.want) {
				t.Fatalf("caller mutation affected scope: got %#v, want %#v", again, tc.want)
			}
		})
	}
	if _, err := ValidateBatch(description, nil, Batch{Scope: map[string]any{"unsupported": []int{1}}}); err == nil {
		t.Fatal("accepted an unsupported scope value")
	}
}
