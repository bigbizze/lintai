package workspace

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/bmatcuk/doublestar/v4"
)

var ignoredDirNames = map[string]struct{}{
	"node_modules": {},
	"dist":         {},
}

// ListSourceFiles returns the source files under root. It skips
// node_modules, dist and every directory whose name starts with a dot, such
// as .git and .next. It also skips the files that match an exclude glob.
// Exclude globs are doublestar patterns relative to root.
func ListSourceFiles(root string, excludes []string) ([]string, error) {
	for _, pattern := range excludes {
		if !doublestar.ValidatePattern(pattern) {
			return nil, fmt.Errorf("invalid exclude glob %q", pattern)
		}
	}
	files := make([]string, 0)
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if path == root {
			return nil
		}
		relative := RelativePath(root, path)
		if entry.IsDir() {
			if _, ignored := ignoredDirNames[entry.Name()]; ignored || strings.HasPrefix(entry.Name(), ".") || matchesAny(excludes, relative+"/") {
				return filepath.SkipDir
			}
			return nil
		}
		if !isSourceFile(path) || matchesAny(excludes, relative) {
			return nil
		}
		files = append(files, path)
		return nil
	})
	return files, err
}

func matchesAny(patterns []string, relative string) bool {
	for _, pattern := range patterns {
		if doublestar.MatchUnvalidated(pattern, relative) || doublestar.MatchUnvalidated(pattern, strings.TrimSuffix(relative, "/")) {
			return true
		}
	}
	return false
}

func RelativePath(root, path string) string {
	relative, err := filepath.Rel(root, path)
	if err != nil {
		return filepath.ToSlash(path)
	}
	return filepath.ToSlash(relative)
}

func ReadFile(path string) (string, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	return string(raw), nil
}

func isSourceFile(path string) bool {
	if strings.HasSuffix(path, ".d.ts") {
		return false
	}
	switch filepath.Ext(path) {
	case ".ts", ".tsx", ".js", ".jsx":
		return true
	default:
		return false
	}
}
