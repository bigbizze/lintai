package typescript

import (
	"context"
	"slices"
	"testing"

	"github.com/bigbizze/lintai/backend"
	"github.com/bigbizze/lintai/facts"
)

func TestUnsupportedExportDeclarationValidates(t *testing.T) {
	root := t.TempDir()
	writeWorkspaceFile(t, root, "tsconfig.json", `{"compilerOptions":{"module":"commonjs"},"files":["api.ts"]}`)
	writeWorkspaceFile(t, root, "api.ts", "const api = { member: 123 };\nexport = api.member;\n")
	producer := New(Options{})
	description, err := producer.Describe()
	if err != nil {
		t.Fatal(err)
	}
	byName := make(map[string]facts.Predicate)
	var requested []facts.Predicate
	for _, relation := range description.BaseRelations {
		byName[relation.Predicate.Name] = relation.Predicate
		requested = append(requested, relation.Predicate)
	}
	batch, err := producer.Collect(context.Background(), backend.CollectionRequest{RepoRoot: root, WorkspaceRoot: root, Predicates: requested})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := facts.ValidateBatch(description, requested, batch); err != nil {
		t.Fatalf("extracted batch does not validate: %v", err)
	}
	var id string
	for _, row := range batch.Relations[byName["ts.declaration"]] {
		if row[1] == facts.String("member") && row[2] == facts.Name("unknown") {
			id = row[0].String
		}
	}
	if id == "" {
		t.Fatalf("missing unsupported exported property: %v", batch.Relations[byName["ts.declaration"]])
	}
	if !slices.ContainsFunc(batch.Relations[byName["ts.declaration_metadata_incomplete"]], func(row facts.Row) bool {
		return row[0].String == id && row[1] == facts.Name("unsupported")
	}) || !slices.ContainsFunc(batch.Relations[byName["ts.declaration_type_incomplete"]], func(row facts.Row) bool {
		return row[0].String == id && row[1] == facts.Name("value") && row[2] == facts.Name("unsupported")
	}) {
		t.Fatalf("missing unsupported metadata or value-type evidence for %s", id)
	}
}
