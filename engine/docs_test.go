package engine

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"codeberg.org/TauCeti/mangle-go/factstore"
	"github.com/bigbizze/lintai/runtime"
)

var (
	dlBlock               = regexp.MustCompile("(?s)\\.program\\((?:\\([^)]*\\)\\s*=>\\s*)?dl`(.*?)`\\s*\\)")
	quoted                = regexp.MustCompile(`"(?:[^"\\]|\\.)*"`)
	comment               = regexp.MustCompile(`(?m)#.*$`)
	interpolated          = regexp.MustCompile(`\$\{[^}]*\}`)
	templateInterpolation = regexp.MustCompile("\\$\\{`[^\\n]*?`\\}")
	clauseEnd             = regexp.MustCompile(`\.(?:\s|$)`)
	relationName          = regexp.MustCompile(`^(?:Decl\s+)?([a-z_][A-Za-z0-9_]*)\s*\(`)
	docSources            = []string{
		"README.md",
		"docs/api.md",
		"docs/examples.md",
		"testdata/npm-smoke/lintai-rules/actions-return-action-result.ts",
		"testdata/npm-smoke/lintai-rules/no-async.ts",
		"testdata/oxlint-smoke/lintai-rules/actions-return-action-result.ts",
		"testdata/oxlint-smoke/lintai-rules/no-async.ts",
		"testdata/fixtures/relation-examples/a1-source-identity/rules/client-directive.ts",
		"testdata/fixtures/relation-examples/a1-source-identity/rules/domain-async.ts",
		"testdata/fixtures/relation-examples/a1-source-identity/rules/domain-fetch.ts",
		"testdata/fixtures/relation-examples/a1-source-identity/rules/env-writes.ts",
		"testdata/fixtures/relation-examples/a1-source-identity/rules/meta-env.ts",
		"testdata/fixtures/relation-examples/a1-source-identity/rules/parse-errors-strict.ts",
		"testdata/fixtures/relation-examples/a1-source-identity/rules/reducer-methods.ts",
		"testdata/fixtures/relation-examples/a2-imports/rules/explicit-loads-strict.ts",
		"testdata/fixtures/relation-examples/a2-imports/rules/no-infrastructure-types.ts",
		"testdata/fixtures/relation-examples/a2-imports/rules/resolved-imports-strict.ts",
	}
)

// Every relation that a published rule program defines has a Decl, so a
// reader who copies an example gets a program that declares its relations.
func TestDocumentedProgramsDeclareTheirRelations(t *testing.T) {
	checked := 0
	catalog, declarations := typeScriptCatalogForTest(t)
	for _, path := range docSources {
		data, err := os.ReadFile(filepath.Join("..", path))
		if err != nil {
			t.Fatal(err)
		}
		for _, block := range dlBlock.FindAllStringSubmatch(string(data), -1) {
			declared := map[string]bool{}
			reported := map[string]bool{}
			var heads []string
			text := interpolated.ReplaceAllString(comment.ReplaceAllString(quoted.ReplaceAllString(block[1], `""`), ""), "x")
			for _, clause := range clauseEnd.Split(text, -1) {
				clause = strings.TrimSpace(clause)
				if clause == "" {
					continue
				}
				match := relationName.FindStringSubmatch(clause)
				if match == nil {
					t.Errorf("%s: cannot read the clause %q", path, clause)
					continue
				}
				if strings.HasPrefix(clause, "Decl") {
					declared[match[1]] = true
				} else if !reported[match[1]] {
					heads = append(heads, match[1])
					reported[match[1]] = true
				}
			}
			for _, head := range heads {
				checked++
				if !declared[head] {
					t.Errorf("%s: %s is defined without a Decl", path, head)
				}
			}
			program := templateInterpolation.ReplaceAllString(block[1], `"src/example/**"`)
			program = interpolated.ReplaceAllString(program, `"src/example/**"`)
			_, err := evaluateProgram(factstore.NewMultiIndexedArrayInMemoryStore(), catalog, declarations, nil, nil, []string{"ts"}, runtime.ProgramOutput{Text: program}, heads, nil, evalOptions{})
			if err != nil {
				t.Errorf("%s: example does not prepare (%s): %v", path, err.phase, err)
			}
		}
	}
	if checked == 0 {
		t.Fatal("no rule program was checked")
	}
}
