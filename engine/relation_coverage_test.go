package engine

import (
	"context"
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"codeberg.org/TauCeti/mangle-go/ast"
	"codeberg.org/TauCeti/mangle-go/parse"
	"github.com/bigbizze/lintai/backend/typescript"
	"github.com/bigbizze/lintai/facts"
	"github.com/bigbizze/lintai/internal/testpack"
)

type relationUsers struct {
	direct map[string]struct{}
	via    map[string]struct{}
}

type uncoveredRelation struct {
	Relation string `json:"relation"`
	Reason   string `json:"reason"`
}

// TestRelationCoverageInventory uses the assembled producer catalog and the
// same dependency selection as rule preparation. Dependency coverage does not
// imply that a fixture positively emits every possible reason.
func TestRelationCoverageInventory(t *testing.T) {
	root, err := filepath.Abs("..")
	if err != nil {
		t.Fatal(err)
	}
	session := openTestSession(t, Config{}, typescript.New())
	defer session.Close()
	catalog := session.catalog
	description := catalog.entries[0].description
	bases := make(map[ast.PredicateSym]facts.Predicate, len(description.BaseRelations))
	byName := make(map[string]facts.Predicate, len(description.BaseRelations))
	for _, relation := range description.BaseRelations {
		predicate := relation.Predicate
		bases[ast.PredicateSym{Symbol: predicate.Name, Arity: predicate.Arity}] = predicate
		byName[predicate.Name] = predicate
	}
	helpers := make(map[ast.PredicateSym]struct{}, len(description.Helpers))
	helperByName := make(map[string]ast.PredicateSym, len(description.Helpers))
	for _, relation := range description.Helpers {
		predicate := ast.PredicateSym{Symbol: relation.Predicate.Name, Arity: relation.Predicate.Arity}
		helpers[predicate] = struct{}{}
		helperByName[predicate.Symbol] = predicate
	}

	groups := fixtureRuleGroups(t, root)
	if len(groups) == 0 {
		t.Fatal("no fixture rule files found")
	}

	users := make(map[facts.Predicate]*relationUsers)
	workspaces := make([]string, 0, len(groups))
	for workspace := range groups {
		workspaces = append(workspaces, workspace)
	}
	slices.Sort(workspaces)
	for _, workspace := range workspaces {
		paths := groups[workspace]
		packRoot := testpack.Build(t, root, t.TempDir(), paths...)
		rules, err := openRuleCatalog(context.Background(), packRoot, nil)
		if err != nil {
			t.Fatal(err)
		}
		if err := rules.loadMetadata(context.Background(), true); err != nil {
			t.Fatal(err)
		}
		defer rules.close()
		for _, entry := range rules.rules {
			path := filepath.Join(filepath.Dir(paths[0]), filepath.Base(entry.path))
			if entry.loadError != nil {
				t.Fatalf("%s: %v", path, entry.loadError)
			}
			if err := entry.loaded.Configure(nil); err != nil {
				t.Fatalf("%s: %v", path, err)
			}
			output, err := entry.loaded.Program()
			if err != nil {
				t.Fatalf("%s: %v", path, err)
			}
			unit, err := parse.Unit(strings.NewReader(splitConstantTerminators(output.Text)))
			if err != nil {
				t.Fatalf("%s: parse rule program: %v", path, err)
			}
			relative, err := filepath.Rel(root, path)
			if err != nil {
				t.Fatal(err)
			}
			roots := append(entry.metadata.Report, entry.metadata.ExpectNonEmpty...)
			collectRelationUsers(unit, roots, filepath.ToSlash(relative), catalog.sources, bases, byName, helpers, helperByName, users)
		}
	}

	predicates := make([]facts.Predicate, 0, len(description.BaseRelations))
	for _, relation := range description.BaseRelations {
		predicates = append(predicates, relation.Predicate)
	}
	slices.SortFunc(predicates, func(a, b facts.Predicate) int { return strings.Compare(a.Name, b.Name) })
	allowPath := filepath.Join(root, "testdata", "fixtures", "relation-examples", "uncovered.json")
	data, err := os.ReadFile(allowPath)
	if err != nil {
		t.Fatal(err)
	}
	var entries []uncoveredRelation
	if err := json.Unmarshal(data, &entries); err != nil {
		t.Fatal(err)
	}
	allow := make(map[string]string, len(entries))
	for _, entry := range entries {
		if _, ok := byName[entry.Relation]; !ok {
			t.Errorf("allowlist contains unknown relation %q", entry.Relation)
		}
		if _, duplicate := allow[entry.Relation]; duplicate {
			t.Errorf("allowlist contains %q more than once", entry.Relation)
		}
		if strings.TrimSpace(entry.Reason) == "" {
			t.Errorf("allowlist reason is empty for %q", entry.Relation)
		}
		allow[entry.Relation] = entry.Reason
	}
	for _, problem := range relationCoverageProblems(predicates, users, allow) {
		t.Error(problem)
	}
}

func fixtureRuleGroups(t *testing.T, root string) map[string][]string {
	t.Helper()
	fixtureRoot := filepath.Join(root, "testdata", "fixtures")
	groups := map[string][]string{}
	err := filepath.WalkDir(fixtureRoot, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() || filepath.Ext(path) != ".ts" {
			return nil
		}
		relative, err := filepath.Rel(fixtureRoot, path)
		if err != nil {
			return err
		}
		parts := strings.Split(filepath.ToSlash(relative), "/")
		for i, part := range parts[:len(parts)-1] {
			// A workspace's src/rules can be ordinary TypeScript source.
			if part == "lintai-rules" || part == "rules" && !slices.Contains(parts[:i], "workspace") {
				parent := filepath.Join(append([]string{fixtureRoot}, parts[:i]...)...)
				workspace := parent
				if part == "rules" {
					sibling := filepath.Join(parent, "workspace")
					if i == 0 || parts[0] == "relation-examples" && i == 2 {
						workspace = sibling
					} else if info, err := os.Stat(sibling); err == nil && info.IsDir() {
						workspace = sibling
					}
				}
				groups[workspace] = append(groups[workspace], path)
				break
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	for workspace, paths := range groups {
		if info, err := os.Stat(workspace); err != nil || !info.IsDir() {
			t.Fatalf("fixture workspace %s is not a directory", workspace)
		}
		slices.Sort(paths)
	}
	return groups
}

func collectRelationUsers(unit parse.SourceUnit, roots []string, path string, catalog []parse.SourceUnit,
	bases map[ast.PredicateSym]facts.Predicate, byName map[string]facts.Predicate,
	helpers map[ast.PredicateSym]struct{}, helperByName map[string]ast.PredicateSym, users map[facts.Predicate]*relationUsers) {
	add := func(predicate facts.Predicate, direct bool, user string) {
		if users[predicate] == nil {
			users[predicate] = &relationUsers{direct: map[string]struct{}{}, via: map[string]struct{}{}}
		}
		if direct {
			users[predicate].direct[user] = struct{}{}
		} else {
			users[predicate].via[user] = struct{}{}
		}
	}
	usedHelpers := map[ast.PredicateSym]struct{}{}
	visitClausePredicates(unit.Clauses, func(predicate ast.PredicateSym) {
		predicate.Symbol = qualifyPredicate(sourcePackageName(unit), predicate.Symbol)
		if base, ok := bases[predicate]; ok {
			add(base, true, "`"+path+"`")
		}
		if _, ok := helpers[predicate]; ok {
			usedHelpers[predicate] = struct{}{}
		}
	})
	for _, name := range roots {
		if base, ok := byName[name]; ok {
			add(base, true, "`"+path+"`")
		}
		if helper, ok := helperByName[name]; ok {
			usedHelpers[helper] = struct{}{}
		}
	}
	selected, _ := catalogForRule(catalog, unit, roots)
	for helper := range usedHelpers {
		helperCatalog, _ := catalogForRule(selected, parse.SourceUnit{}, []string{helper.Symbol})
		for _, source := range helperCatalog {
			visitClausePredicates(source.Clauses, func(predicate ast.PredicateSym) {
				predicate.Symbol = qualifyPredicate(sourcePackageName(source), predicate.Symbol)
				if base, ok := bases[predicate]; ok {
					add(base, false, "`"+helper.Symbol+"` (`"+path+"`)")
				}
			})
		}
	}
}

func visitClausePredicates(clauses []ast.Clause, visit func(ast.PredicateSym)) {
	for _, clause := range clauses {
		for _, premise := range clause.Premises {
			switch atom := premise.(type) {
			case ast.Atom:
				visit(atom.Predicate)
			case ast.NegAtom:
				visit(atom.Atom.Predicate)
			}
		}
	}
}

func relationCoverageProblems(predicates []facts.Predicate, users map[facts.Predicate]*relationUsers, allow map[string]string) []string {
	var problems []string
	for _, predicate := range predicates {
		user := users[predicate]
		used := user != nil && len(user.direct)+len(user.via) > 0
		_, allowed := allow[predicate.Name]
		switch {
		case !used && !allowed:
			problems = append(problems, fmt.Sprintf("%s is neither used nor allowlisted", predicate))
		case used && allowed:
			problems = append(problems, fmt.Sprintf("%s is both used and allowlisted", predicate))
		}
	}
	return problems
}
