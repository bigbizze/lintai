package typescript

import (
	"fmt"
	"slices"
	"sort"
	"strings"

	"github.com/bigbizze/lintai/internal/analysis"
	"github.com/bigbizze/lintai/internal/diagnostics"
	"github.com/bigbizze/lintai/internal/workspace"
)

const importMetaPrefix = "import.meta."

type accessMatch struct {
	start      int
	end        int
	accessPath string
}

func extractAccesses(workspaceRoot string, files []string) ([]analysis.Access, error) {
	result := make([]analysis.Access, 0)
	counts := make(map[string]int)
	sorted := make([]string, len(files))
	copy(sorted, files)
	sort.Strings(sorted)
	for _, file := range sorted {
		source, err := workspace.ReadFile(file)
		if err != nil {
			return nil, err
		}
		relative := workspace.RelativePath(workspaceRoot, file)
		matches := scanImportMetaAccesses(source)
		for _, match := range matches {
			location := sourceLocationForRange(relative, source, match.start, match.end)
			baseKey := fmt.Sprintf("%s::access::%s", relative, match.accessPath)
			counts[baseKey]++
			semanticKey := withOrdinal(baseKey, counts[baseKey])
			result = append(result, analysis.Access{
				EntityID:    "access:" + semanticKey,
				SemanticKey: semanticKey,
				Root:        "import.meta",
				AccessPath:  match.accessPath,
				Origin:      "special_form",
				FilePath:    relative,
				Range:       location,
			})
		}
	}
	slices.SortFunc(result, func(left, right analysis.Access) int {
		return strings.Compare(left.SemanticKey, right.SemanticKey)
	})
	return result, nil
}

func scanImportMetaAccesses(source string) []accessMatch {
	matches := make([]accessMatch, 0)
	for index := 0; index < len(source); {
		switch source[index] {
		case '/':
			if index+1 < len(source) {
				switch source[index+1] {
				case '/':
					index += 2
					for index < len(source) && source[index] != '\n' {
						index++
					}
					continue
				case '*':
					index += 2
					for index+1 < len(source) && !(source[index] == '*' && source[index+1] == '/') {
						index++
					}
					if index+1 < len(source) {
						index += 2
					}
					continue
				}
			}
		case '\'', '"':
			index = skipQuotedString(source, index, source[index])
			continue
		case '`':
			index = skipTemplateLiteral(source, index)
			continue
		}

		if strings.HasPrefix(source[index:], importMetaPrefix) && !isIdentifierPart(source, index-1) {
			nameStart := index + len(importMetaPrefix)
			if isIdentifierStart(source, nameStart) {
				nameEnd := nameStart + 1
				for isIdentifierPart(source, nameEnd) {
					nameEnd++
				}
				matches = append(matches, accessMatch{
					start:      index,
					end:        nameEnd,
					accessPath: importMetaPrefix + source[nameStart:nameEnd],
				})
				index = nameEnd
				continue
			}
		}
		index++
	}
	return matches
}

func skipQuotedString(source string, index int, quote byte) int {
	index++
	for index < len(source) {
		if source[index] == '\\' {
			index += 2
			continue
		}
		if source[index] == quote {
			return index + 1
		}
		index++
	}
	return index
}

func skipTemplateLiteral(source string, index int) int {
	index++
	for index < len(source) {
		if source[index] == '\\' {
			index += 2
			continue
		}
		if source[index] == '`' {
			return index + 1
		}
		index++
	}
	return index
}

func isIdentifierStart(source string, index int) bool {
	if index < 0 || index >= len(source) {
		return false
	}
	ch := source[index]
	return ch == '_' || ch == '$' || (ch >= 'A' && ch <= 'Z') || (ch >= 'a' && ch <= 'z')
}

func isIdentifierPart(source string, index int) bool {
	if index < 0 || index >= len(source) {
		return false
	}
	ch := source[index]
	return isIdentifierStart(source, index) || (ch >= '0' && ch <= '9')
}

func sourceLocationForRange(filePath, source string, start, end int) diagnostics.SourceLocation {
	startLine, startColumn := lineColumnForOffset(source, start)
	endLine, endColumn := lineColumnForOffset(source, end)
	return diagnostics.SourceLocation{
		File:        filePath,
		StartLine:   startLine,
		StartColumn: startColumn,
		EndLine:     endLine,
		EndColumn:   endColumn,
	}
}

func lineColumnForOffset(source string, offset int) (int, int) {
	if offset < 0 {
		offset = 0
	}
	if offset > len(source) {
		offset = len(source)
	}
	line := 1
	column := 1
	for index := 0; index < offset; index++ {
		if source[index] == '\n' {
			line++
			column = 1
			continue
		}
		column++
	}
	return line, column
}

func withOrdinal(base string, ordinal int) string {
	if ordinal <= 1 {
		return base
	}
	return fmt.Sprintf("%s#%d", base, ordinal)
}
