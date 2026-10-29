package engine

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

var (
	dlBlock      = regexp.MustCompile("(?s)\\.program\\((?:\\([^)]*\\)\\s*=>\\s*)?dl`(.*?)`")
	quoted       = regexp.MustCompile(`"(?:[^"\\]|\\.)*"`)
	clauseEnd    = regexp.MustCompile(`\.(?:\s|$)`)
	relationName = regexp.MustCompile(`^(?:Decl\s+)?([a-z_][A-Za-z0-9_]*)\s*\(`)
	docSources   = []string{
		"README.md",
		"docs/api.md",
		"docs/examples.md",
		"specs/structural-type-facts.md",
		"testdata/npm-smoke/lintai-rules/actions-return-action-result.ts",
		"testdata/npm-smoke/lintai-rules/no-async.ts",
		"testdata/oxlint-smoke/lintai-rules/actions-return-action-result.ts",
		"testdata/oxlint-smoke/lintai-rules/no-async.ts",
	}
)

// Every relation that a published rule program defines has a Decl, so a
// reader who copies an example gets a program that declares its relations.
func TestDocumentedProgramsDeclareTheirRelations(t *testing.T) {
	checked := 0
	for _, path := range docSources {
		data, err := os.ReadFile(filepath.Join("..", path))
		if err != nil {
			t.Fatal(err)
		}
		for _, block := range dlBlock.FindAllStringSubmatch(string(data), -1) {
			declared := map[string]bool{}
			var heads []string
			for _, clause := range clauseEnd.Split(quoted.ReplaceAllString(block[1], `""`), -1) {
				clause = strings.TrimSpace(clause)
				match := relationName.FindStringSubmatch(clause)
				if match == nil {
					continue
				}
				if strings.HasPrefix(clause, "Decl") {
					declared[match[1]] = true
				} else {
					heads = append(heads, match[1])
				}
			}
			for _, head := range heads {
				checked++
				if !declared[head] {
					t.Errorf("%s: %s is defined without a Decl", path, head)
				}
			}
		}
	}
	if checked == 0 {
		t.Fatal("no rule program was checked")
	}
}
