package facts

import (
	"testing"
)

func TestGenericSnapshotValidatesAndFormatsTypedFacts(t *testing.T) {
	snapshot := Snapshot{
		Version: "external-fixture-v1",
		Schema: Schema{Declarations: `
Decl object(Id, Kind)
  descr [extensional(), arg(Id, "stable object id"), arg(Kind, "object kind")].
Decl location(Id, File, StartLine, StartColumn, EndLine, EndColumn)
  descr [extensional(), arg(Id, "entity id"), arg(File, "file"), arg(StartLine, "start line"), arg(StartColumn, "start column"), arg(EndLine, "end line"), arg(EndColumn, "end column")].
`, Prelude: ""},
		Facts: []Fact{
			{Relation: "object", Values: []Value{String("catalog/orders"), Name("table")}},
			{Relation: "object", Values: []Value{String("catalog/orders"), Name("table")}},
			{Relation: "location", Values: []Value{String("catalog/orders"), String("schema.sql"), Number(3), Number(1), Number(3), Number(12)}},
		},
	}
	if err := Validate(snapshot); err != nil {
		t.Fatal(err)
	}
	formatted, err := Format(snapshot, nil)
	if err != nil {
		t.Fatal(err)
	}
	want := "location(\"catalog/orders\",\"schema.sql\",3,1,3,12).\n" +
		"object(\"catalog/orders\",/table)."
	if formatted != want {
		t.Fatalf("formatted snapshot = %q, want %q", formatted, want)
	}
	filtered, err := Format(snapshot, []string{"object"})
	if err != nil {
		t.Fatal(err)
	}
	if filtered != "object(\"catalog/orders\",/table)." {
		t.Fatalf("filtered snapshot = %q", filtered)
	}
	if _, err := Format(snapshot, []string{"missing"}); err == nil {
		t.Fatal("expected an unknown relation error")
	}
}
