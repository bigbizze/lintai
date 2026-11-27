// Package testpack publishes explicitly selected fixture rules for engine and command tests.
package testpack

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/bigbizze/lintai/internal/rulepack"
)

// Build compiles the fixture entries with the repository's installed rule dependencies.
// Checked-in application fixtures are copied before publishing their pack.
func Build(t *testing.T, repo, workspace string, patterns ...string) string {
	t.Helper()
	stage, err := os.MkdirTemp(repo, "lintai-test-build-")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(stage)
	sdk := filepath.Join(stage, "node_modules", "@lintai", "sdk")
	if err := os.MkdirAll(sdk, 0755); err != nil {
		t.Fatal(err)
	}
	source, err := os.ReadFile(filepath.Join(repo, "packages", "sdk", "src", "index.ts"))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(sdk, "index.ts"), source, 0644); err != nil {
		t.Fatal(err)
	}
	names := []string{}
	for _, pattern := range patterns {
		if !filepath.IsAbs(pattern) {
			pattern = filepath.Join(workspace, pattern)
		}
		paths, err := filepath.Glob(pattern)
		if err != nil || len(paths) == 0 {
			t.Fatalf("fixture entries %s: %v", pattern, err)
		}
		for _, path := range paths {
			name, err := filepath.Rel(workspace, path)
			if err != nil || !filepath.IsLocal(name) {
				name = filepath.Join("rules", filepath.Base(path))
			}
			names = append(names, filepath.ToSlash(name))
			// Include sibling helpers and imported static JSON.
			dir := filepath.Join(stage, filepath.Dir(name))
			if err := os.MkdirAll(dir, 0755); err != nil {
				t.Fatal(err)
			}
			siblings, err := os.ReadDir(filepath.Dir(path))
			if err != nil {
				t.Fatal(err)
			}
			for _, entry := range siblings {
				if entry.IsDir() {
					continue
				}
				switch filepath.Ext(entry.Name()) {
				case ".ts", ".js", ".json":
				default:
					continue
				}
				data, err := os.ReadFile(filepath.Join(filepath.Dir(path), entry.Name()))
				if err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(filepath.Join(dir, entry.Name()), data, 0644); err != nil {
					t.Fatal(err)
				}
			}
		}
	}
	if err := rulepack.Build(context.Background(), stage, names); err != nil {
		t.Fatal(err)
	}
	if strings.HasPrefix(workspace, filepath.Join(repo, "testdata")+string(filepath.Separator)) {
		// Preserve the fixture's ancestor package.json and dependency lookup.
		copy, err := os.MkdirTemp(repo, "lintai-test-workspace-")
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { os.RemoveAll(copy) })
		if err := os.CopyFS(copy, os.DirFS(workspace)); err != nil {
			t.Fatal(err)
		}
		workspace = copy
	}
	path := filepath.Join(workspace, rulepack.Path)
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(stage, rulepack.Path))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0644); err != nil {
		t.Fatal(err)
	}
	return workspace
}
