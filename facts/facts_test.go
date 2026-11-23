package facts

import (
	"math"
	"strings"
	"testing"

	"codeberg.org/TauCeti/mangle-go/factstore"
	"github.com/bigbizze/lintai/diagnostics"
)

func builderFixture(t *testing.T) (Descriptor, Predicate, AnchorSupport) {
	t.Helper()
	description, err := DescribeSchema("catalog", "fixture", "v1", "fixture/1", map[string]any{"nested": []any{"original"}}, Schema{Declarations: `Decl object(Entity, Kind) descr [extensional(), arg_role(Entity, "entity"), arg_report_support(Entity, "required"), arg_representation(Kind, "name"), arg_allowed_name(Kind, "collection")]. Decl unused(Value) descr [extensional()].`, Prelude: `Decl helper(Value). helper(Value) :- unused(Value).`})
	if err != nil {
		t.Fatal(err)
	}
	descriptor, err := ValidateDescription(description)
	if err != nil {
		t.Fatal(err)
	}
	description.EffectiveConfiguration.(map[string]any)["nested"].([]any)[0] = "changed"
	if descriptor.Description().EffectiveConfiguration.(map[string]any)["nested"].([]any)[0] != "original" {
		t.Fatal("descriptor retained producer configuration")
	}
	detached := descriptor.Description()
	detached.EffectiveConfiguration.(map[string]any)["nested"].([]any)[0] = "caller mutation"
	detached.BaseRelations[0].Arguments[0].Representations[0] = NumberKind
	again := descriptor.Description()
	if again.EffectiveConfiguration.(map[string]any)["nested"].([]any)[0] != "original" || again.BaseRelations[0].Arguments[0].Representations[0] != StringKind {
		t.Fatal("description accessor exposed descriptor state")
	}
	return descriptor, Predicate{Name: "catalog.object", Arity: 2}, AnchorSupport{Location: diagnostics.SourceLocation{File: "catalog/object.sql", StartLine: 1, StartColumn: 1, EndLine: 1, EndColumn: 2}, Description: "object", Tracking: &TrackingKey{Scheme: "fixture/1", Data: map[string]any{"nested": []any{"original"}}}}
}

func TestBuilderCoverageHandlesValidationAndOwnership(t *testing.T) {
	descriptor, predicate, support := builderFixture(t)
	makeBuilder := func() *Builder {
		b, err := NewBuilder(descriptor, []Predicate{predicate})
		if err != nil {
			t.Fatal(err)
		}
		return b
	}
	if _, err := NewBuilder(descriptor, []Predicate{predicate, predicate}); err == nil {
		t.Fatal("accepted duplicate demand")
	}
	if _, err := NewBuilder(descriptor, []Predicate{{Name: "catalog.helper", Arity: 1}}); err == nil {
		t.Fatal("accepted helper demand")
	}
	if _, err := makeBuilder().Finish(); err == nil || !strings.Contains(err.Error(), "omitted requested") {
		t.Fatalf("omission: %v", err)
	}
	empty := makeBuilder()
	if _, err := empty.Relation("object", 2); err != nil {
		t.Fatal(err)
	}
	result, err := empty.Finish()
	if err != nil || result.Count(predicate) != 0 {
		t.Fatalf("empty production: %v", err)
	}
	for _, tc := range []struct {
		name string
		add  func(*Builder, Relation)
	}{
		{"arity", func(b *Builder, r Relation) { b.Add(r, String("id")) }},
		{"representation", func(b *Builder, r Relation) { b.Add(r, Number(1), Name("collection")) }},
		{"empty entity", func(b *Builder, r Relation) { b.Add(r, String(""), Name("collection")) }},
		{"allowed name", func(b *Builder, r Relation) { b.Add(r, String("id"), Name("other")) }},
		{"zero handle", func(b *Builder, _ Relation) { b.Add(Relation{}, String("ignored")) }},
		{"foreign handle", func(b *Builder, _ Relation) {
			other := makeBuilder()
			r, _ := other.Relation("unused", 1)
			b.Add(r, String("ignored"))
		}},
		{"missing support", func(b *Builder, r Relation) { b.Add(r, String("id"), Name("collection")) }},
		{"conflicting support", func(b *Builder, r Relation) {
			b.Anchor("id", support)
			altered := support
			altered.Description = "different"
			b.Anchor("id", altered)
			b.Add(r, String("id"), Name("collection"))
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			b := makeBuilder()
			r, _ := b.Relation("object", 2)
			tc.add(b, r)
			if _, err := b.Finish(); err == nil {
				t.Fatal("invalid output was accepted")
			}
		})
	}
	b := makeBuilder()
	r, _ := b.Relation("object", 2)
	unwanted, _ := b.Relation("unused", 1)
	b.Add(unwanted, Number(123), Number(456))
	b.Add(r, String("other:local"), Name("collection"))
	b.Add(r, String("other:local"), Name("collection"))
	b.Anchor("other:local", support)
	b.Anchor("other:local", support)
	validated, err := b.Finish()
	if err != nil {
		t.Fatal(err)
	}
	support.Tracking.Data.(map[string]any)["nested"].([]any)[0] = "changed"
	anchors := validated.Anchors()
	id := "catalog:other:local"
	if anchors[id].Tracking.Data.(map[string]any)["nested"].([]any)[0] != "original" || validated.Count(predicate) != 1 {
		t.Fatalf("support/row not detached and deduplicated: %+v", anchors)
	}
	anchors[id].Tracking.Data.(map[string]any)["nested"].([]any)[0] = "mutated"
	atoms := validated.Atoms(predicate)
	atoms[0].Args[0] = nil
	b.Add(r, String("later"), Name("collection"))
	b.Anchor("later", support)
	if validated.Atoms(predicate)[0].Args[0] == nil || validated.Anchors()[id].Tracking.Data.(map[string]any)["nested"].([]any)[0] != "original" || validated.Count(predicate) != 1 {
		t.Fatal("published facts were mutated")
	}
	if _, ok := validated.Reportable(predicate)[id]; !ok {
		t.Fatal("reportable index omitted supported row")
	}
	store := factstore.NewMultiIndexedArrayInMemoryStore()
	for _, atom := range validated.Atoms(predicate) {
		store.Add(atom)
	}
	formatted, err := FormatFacts(store, []string{"catalog.object"}, nil)
	if err != nil || formatted != `catalog.object("catalog:other:local",/collection).` {
		t.Fatalf("format: %q %v", formatted, err)
	}
	if _, err := FormatFacts(store, []string{"catalog.object"}, []string{"object"}); err == nil {
		t.Fatal("accepted unqualified relation")
	}
}

func TestBuilderRejectsNonFiniteAndInvalidPaths(t *testing.T) {
	description, err := DescribeSchema("catalog", "fixture", "v1", "v1", nil, Schema{Declarations: `Decl item(Path, Value) descr [extensional(), arg_role(Path, "path"), arg_representation(Value, "float")].`})
	if err != nil {
		t.Fatal(err)
	}
	d, err := ValidateDescription(description)
	if err != nil {
		t.Fatal(err)
	}
	p := Predicate{Name: "catalog.item", Arity: 2}
	for _, row := range [][]Value{{String("a/../b"), Float(1)}, {String("a"), Float(math.Inf(1))}} {
		b, _ := NewBuilder(d, []Predicate{p})
		r, _ := b.Relation("item", 2)
		b.Add(r, row...)
		if _, err := b.Finish(); err == nil {
			t.Fatalf("accepted %v", row)
		}
	}
}

func TestDescriptorRejectsInvalidSchemaAndMetadata(t *testing.T) {
	descriptor, _, _ := builderFixture(t)
	cases := []struct {
		name   string
		mutate func(*ProducerDescription)
	}{
		{"reserved namespace", func(d *ProducerDescription) { d.Namespace = "lintai" }},
		{"missing implementation", func(d *ProducerDescription) { d.ImplementationStamp = "" }},
		{"unsupported configuration", func(d *ProducerDescription) { d.EffectiveConfiguration = []int{1} }},
		{"duplicate predicate", func(d *ProducerDescription) { d.BaseRelations = append(d.BaseRelations, d.BaseRelations[0]) }},
		{"unknown representation", func(d *ProducerDescription) {
			d.BaseRelations[0].Arguments[0].Representations = []ValueKind{ValueKind(99)}
		}},
		{"invalid allowed name", func(d *ProducerDescription) {
			d.BaseRelations[0].Arguments[1].AllowedNames = []string{"contains space"}
		}},
		{"unknown role", func(d *ProducerDescription) { d.BaseRelations[0].Arguments[0].Role = "other" }},
		{"missing base declaration", func(d *ProducerDescription) { d.Schema.Declarations = "" }},
		{"missing helper declaration", func(d *ProducerDescription) { d.Schema.Prelude = "" }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			d := descriptor.Description()
			tc.mutate(&d)
			if _, err := ValidateDescription(d); err == nil {
				t.Fatal("invalid descriptor accepted")
			}
		})
	}
}
