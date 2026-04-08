package workspace

import (
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

var ignoredDirNames = map[string]struct{}{
	".git":         {},
	"node_modules": {},
	"dist":         {},
}

func ListSourceFiles(root string) ([]string, error) {
	files := make([]string, 0)
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() {
			if _, ignored := ignoredDirNames[entry.Name()]; ignored && path != root {
				return filepath.SkipDir
			}
			return nil
		}
		if !isSourceFile(path) {
			return nil
		}
		files = append(files, path)
		return nil
	})
	return files, err
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
