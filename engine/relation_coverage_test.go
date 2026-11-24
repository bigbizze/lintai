package engine

import (
	"context"
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"testing"

	"codeberg.org/TauCeti/mangle-go/ast"
	"codeberg.org/TauCeti/mangle-go/parse"
	"github.com/bigbizze/lintai/backend/typescript"
	"github.com/bigbizze/lintai/bundle"
	"github.com/bigbizze/lintai/facts"
	"github.com/bigbizze/lintai/runtime"
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
	session := Open(Options{}, typescript.New())
	defer session.Close()
	catalog, err := session.describeProducers()
	if err != nil {
		t.Fatal(err)
	}
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

	// Exercise the two coverage failures in memory, without modifying fixtures.
	t.Run("coverage failures", func(t *testing.T) {
		predicate := facts.Predicate{Name: "ts.example", Arity: 1}
		for _, tc := range []struct {
			name      string
			used      bool
			allow     bool
			wantError string
		}{
			{name: "neither", wantError: "neither used nor allowlisted"},
			{name: "both", used: true, allow: true, wantError: "both used and allowlisted"},
		} {
			t.Run(tc.name, func(t *testing.T) {
				users := map[facts.Predicate]*relationUsers{}
				if tc.used {
					users[predicate] = &relationUsers{direct: map[string]struct{}{"example.ts": {}}}
				}
				allow := map[string]string{}
				if tc.allow {
					allow[predicate.Name] = "reason"
				}
				problems := relationCoverageProblems([]facts.Predicate{predicate}, users, allow)
				if len(problems) != 1 || !strings.Contains(problems[0], tc.wantError) {
					t.Fatalf("got %v, want one error containing %q", problems, tc.wantError)
				}
			})
		}
	})

	// imports_reach traverses nested helpers, including a negated base
	// predicate. A direct base user must remain distinct from those helpers.
	t.Run("direct and transitive helper use", func(t *testing.T) {
		unit, err := parse.Unit(strings.NewReader(`Decl result(M, F, T). result(M, F, T) :- ts.module_path(M, F), ts.imports_reach(F, T).`))
		if err != nil {
			t.Fatal(err)
		}
		users := map[facts.Predicate]*relationUsers{}
		collectRelationUsers(unit, nil, "example.ts", catalog.sources, bases, byName, helpers, helperByName, users)
		if got := users[byName["ts.module_path"]]; got == nil || len(got.direct) != 1 || len(got.via) != 0 {
			t.Fatalf("module_path users: %#v", got)
		}
		for _, name := range []string{"ts.import_edge", "ts.import_type_only", "ts.import_retained"} {
			got := users[byName[name]]
			if got == nil || len(got.direct) != 0 || len(got.via) != 1 {
				t.Errorf("%s helper users: %#v", name, got)
			}
		}
	})

	// An explicit helper root must select its dependencies even when no rule
	// clause references it, and package-local premises use the producer namespace.
	t.Run("explicit helper root in custom namespace", func(t *testing.T) {
		custom := Open(Options{}, typescript.New(typescript.Options{Namespace: "custom"}))
		defer custom.Close()
		customCatalog, err := custom.describeProducers()
		if err != nil {
			t.Fatal(err)
		}
		customDescription := customCatalog.entries[0].description
		customBases := make(map[ast.PredicateSym]facts.Predicate)
		customByName := make(map[string]facts.Predicate)
		for _, relation := range customDescription.BaseRelations {
			predicate := relation.Predicate
			customBases[ast.PredicateSym{Symbol: predicate.Name, Arity: predicate.Arity}] = predicate
			customByName[predicate.Name] = predicate
		}
		customHelpers := make(map[ast.PredicateSym]struct{})
		customHelperByName := make(map[string]ast.PredicateSym)
		for _, relation := range customDescription.Helpers {
			predicate := ast.PredicateSym{Symbol: relation.Predicate.Name, Arity: relation.Predicate.Arity}
			customHelpers[predicate] = struct{}{}
			customHelperByName[predicate.Symbol] = predicate
		}
		unit, err := parse.Unit(strings.NewReader(`Decl result(M).`))
		if err != nil {
			t.Fatal(err)
		}
		users := map[facts.Predicate]*relationUsers{}
		collectRelationUsers(unit, []string{"custom.imports_reach", "custom.module_multi_config", "custom.fn_signature_unresolved"}, "root.ts", customCatalog.sources,
			customBases, customByName, customHelpers, customHelperByName, users)
		for _, name := range []string{"custom.import_edge", "custom.import_type_only", "custom.import_retained"} {
			got := users[customByName[name]]
			if got == nil || len(got.direct) != 0 {
				t.Errorf("%s helper users: %#v", name, got)
				continue
			}
			if _, ok := got.via["`custom.imports_reach` (`root.ts`)"]; !ok || len(got.via) != 1 {
				t.Errorf("%s helper users: %#v", name, got.via)
			}
		}
		for name, helper := range map[string]string{
			"custom.module_config":        "custom.module_multi_config",
			"custom.fn_signature":         "custom.fn_signature_unresolved",
			"custom.signature_incomplete": "custom.fn_signature_unresolved",
		} {
			got := users[customByName[name]]
			if got == nil || len(got.direct) != 0 {
				t.Errorf("%s helper users: %#v", name, got)
				continue
			}
			if _, ok := got.via["`"+helper+"` (`root.ts`)"]; !ok || len(got.via) != 1 {
				t.Errorf("%s helper users: %#v", name, got.via)
			}
		}
	})

	groups := fixtureRuleGroups(t, root)
	if len(groups) == 0 {
		t.Fatal("no fixture rule files found")
	}
	assetRoot := filepath.Join(root, "packages", "client")
	users := make(map[facts.Predicate]*relationUsers)
	workspaces := make([]string, 0, len(groups))
	for workspace := range groups {
		workspaces = append(workspaces, workspace)
	}
	slices.Sort(workspaces)
	for _, workspace := range workspaces {
		paths := groups[workspace]
		artifacts, buildFailures, err := bundle.BuildAll(context.Background(), assetRoot, root, workspace, t.TempDir(), paths)
		if err != nil {
			t.Fatal(err)
		}
		if len(buildFailures) != 0 {
			t.Fatalf("bundle fixture rules in %s: %v", workspace, buildFailures)
		}
		prepared, prepareFailures, err := bundle.PrepareAll(context.Background(), assetRoot, root, workspace, artifacts, map[string]any{}, bundle.PrepareOptions{Sandbox: true})
		if err != nil {
			t.Fatal(err)
		}
		if len(prepareFailures) != 0 {
			t.Fatalf("prepare fixture rules in %s: %v", workspace, prepareFailures)
		}
		for _, path := range paths {
			artifact, ok := artifacts[path]
			if !ok {
				t.Fatalf("no bundle for %s", path)
			}
			code, err := os.ReadFile(artifact.PureBundlePath)
			if err != nil {
				t.Fatal(err)
			}
			inspection, err := runtime.InspectPureBundle(string(code))
			if err != nil {
				t.Fatalf("%s: %v", path, err)
			}
			if !inspection.IsRule { // A shared rule module is counted through its consuming rule.
				continue
			}
			ready, ok := prepared[path]
			if !ok {
				t.Fatalf("%s: no prepared rule", path)
			}
			output, err := inspection.Rule.Program(ready.Env, ready.Setup)
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
			roots := append(inspection.Metadata.Report, inspection.Metadata.ExpectNonEmpty...)
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
	if t.Failed() {
		return
	}
	families := appendixFamilies(t, root)
	var inventory strings.Builder
	inventory.WriteString("# Relation coverage inventory\n\nGenerated by `TestRelationCoverageInventory` from the TypeScript producer catalog and fixture rules. " +
		"Only rules under `testdata/fixtures/` count; npm and oxlint smoke rules do not.\n\n")
	inventory.WriteString("| Relation | Appendix A family | Direct users | Via helpers | Allowlist reason |\n|---|---|---|---|---|\n")
	for _, predicate := range predicates {
		family, ok := families[predicate]
		if !ok {
			t.Errorf("%s is absent from Appendix A; record it in the report and assign a family by its description", predicate)
			continue
		}
		var direct, via string
		if user := users[predicate]; user != nil {
			direct = formatRelationUsers(user.direct)
			via = formatRelationUsers(user.via)
		}
		fmt.Fprintf(&inventory, "| `%s` | %s | %s | %s | %s |\n", predicate, family, direct, via, allow[predicate.Name])
	}
	if t.Failed() {
		return
	}
	inventoryPath := filepath.Join(root, "docs", "followups", "relation-coverage-inventory.md")
	if os.Getenv("LINTAI_UPDATE_GOLDEN") == "1" {
		if err := os.MkdirAll(filepath.Dir(inventoryPath), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(inventoryPath, []byte(inventory.String()), 0o644); err != nil {
			t.Fatal(err)
		}
		return
	}
	want, err := os.ReadFile(inventoryPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(want) != inventory.String() {
		t.Fatal("relation coverage inventory is out of date; run LINTAI_UPDATE_GOLDEN=1 go test ./engine -run '^TestRelationCoverageInventory$'")
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

func formatRelationUsers(users map[string]struct{}) string {
	items := make([]string, 0, len(users))
	for user := range users {
		items = append(items, user)
	}
	slices.Sort(items)
	return strings.Join(items, "<br>")
}

func appendixFamilies(t *testing.T, root string) map[facts.Predicate]string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(root, "specs", "lintai-refactor-implementation-r6.md"))
	if err != nil {
		t.Fatal(err)
	}
	heading := regexp.MustCompile(`^### (A\.[1-9]) (.+)$`)
	row := regexp.MustCompile("^\\| `(ts\\.[a-z0-9_]+)/([0-9]+)` ")
	families := map[facts.Predicate]string{}
	family := ""
	for _, line := range strings.Split(string(data), "\n") {
		if match := heading.FindStringSubmatch(line); match != nil {
			family = match[1] + " " + match[2]
			if match[1] == "A.9" {
				family = "Reporting support (A.9)"
			}
			continue
		}
		if strings.HasPrefix(line, "### A.10") || strings.HasPrefix(line, "## Appendix B") {
			family = ""
		}
		if match := row.FindStringSubmatch(line); family != "" && match != nil {
			arity, err := strconv.Atoi(match[2])
			if err != nil {
				t.Fatal(err)
			}
			predicate := facts.Predicate{Name: match[1], Arity: arity}
			if _, exists := families[predicate]; exists {
				t.Fatalf("%s occurs more than once in Appendix A", predicate)
			}
			families[predicate] = family
		}
	}
	return families
}
