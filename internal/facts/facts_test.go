package facts_test

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"codeberg.org/TauCeti/mangle-go/ast"
	"github.com/bigbizze/lintai/internal/backend"
	"github.com/bigbizze/lintai/internal/backend/typescript"
	"github.com/bigbizze/lintai/internal/facts"
)

// entityArgs lists the argument positions that hold entity ids. Reports
// anchor on entity ids, so each one needs a location row.
var entityArgs = map[string][]int{
	"module":             {0},
	"fn":                 {0},
	"import_edge":        {0},
	"call_edge":          {0, 1, 2},
	"external_call":      {0, 1},
	"type_ref":           {0},
	"meta_access":        {0},
	"ambient_use":        {0},
	"module_directive":   {0},
	"module_parse_error": {0},
	"site_in_fn":         {0, 1},
}

func TestFixtureFactsHaveLocationsAndAreDeterministic(t *testing.T) {
	repoRoot, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	workspaceRoot := filepath.Join(repoRoot, "testdata/fixtures/workspace")
	build := func() string {
		snapshot, err := typescript.New().BuildSnapshot(context.Background(), workspaceRoot, workspaceRoot, backend.SnapshotOptions{})
		if err != nil {
			t.Fatal(err)
		}
		store, err := facts.FromSnapshot(snapshot)
		if err != nil {
			t.Fatal(err)
		}
		decls, err := facts.SchemaDecls()
		if err != nil {
			t.Fatal(err)
		}
		for sym := range decls {
			positions, ok := entityArgs[sym.Symbol]
			if !ok {
				continue
			}
			rows := 0
			_ = store.GetFacts(ast.NewQuery(sym), func(atom ast.Atom) error {
				rows++
				for _, position := range positions {
					query := ast.NewQuery(ast.PredicateSym{Symbol: "location", Arity: 6})
					query.Args[0] = atom.Args[position]
					if !hasAny(store, query) {
						t.Errorf("%s: entity %v has no location", atom, atom.Args[position])
					}
				}
				return nil
			})
			if rows == 0 {
				t.Errorf("fixture workspace has no %s facts", sym.Symbol)
			}
		}
		text, err := facts.Format(store, nil)
		if err != nil {
			t.Fatal(err)
		}
		return text
	}
	first := build()
	if second := build(); first != second {
		t.Fatal("two builds produced different facts")
	}
	for _, want := range []string{
		`import_symbol_type_only("src/server/handler.ts::import::./config","Config").`,
		`type_ref_target("src/server/handler.ts::type_ref::Config","src/server/config.ts").`,
		`site_in_fn("src/server/config.ts::access::import.meta.env","src/server/config.ts::function::mode").`,
		`import_edge("src/server/handler.ts::import::server-only","src/server/handler.ts","server-only","server-only").`,
	} {
		if !strings.Contains(first, want) {
			t.Errorf("missing fact %s", want)
		}
	}
}

func hasAny(store interface {
	GetFacts(ast.Atom, func(ast.Atom) error) error
}, query ast.Atom) bool {
	found := false
	_ = store.GetFacts(query, func(ast.Atom) error {
		found = true
		return nil
	})
	return found
}
