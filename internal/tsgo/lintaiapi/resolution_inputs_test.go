package lintaiapi

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"testing"
)

func TestExternalNodeModulesDirectoryIsTracked(t *testing.T) {
	parent := t.TempDir()
	root := filepath.Join(parent, "workspace")
	writeWorkspace(t, root, map[string]string{
		"tsconfig.json": `{"compilerOptions":{"module":"preserve","moduleResolution":"bundler"},"files":["src/app.ts"]}`,
		"src/app.ts":    `import { value } from "fixture-pkg"; export { value };`,
	})
	request := BuildSnapshotRequest{WorkspaceRoot: root, Files: []string{filepath.Join(root, "src/app.ts")}}
	build := func() *Snapshot {
		t.Helper()
		snapshot, err := BuildSnapshot(context.Background(), request)
		if err != nil {
			t.Fatal(err)
		}
		return snapshot
	}
	before := build()
	if !slices.ContainsFunc(before.ImportEdges, func(edge ImportEdge) bool { return edge.Specifier == "fixture-pkg" && edge.Unresolved }) {
		t.Fatalf("expected unresolved import: %+v", before.ImportEdges)
	}
	nodeModules := filepath.ToSlash(filepath.Join(parent, "node_modules"))
	if !slices.ContainsFunc(before.Inputs, func(input SnapshotInput) bool { return input.Path == nodeModules && !input.Exists }) {
		t.Fatalf("missing external node_modules directory was not tracked: %+v", before.Inputs)
	}
	packageDir := filepath.Join(parent, "node_modules", "fixture-pkg")
	if err := os.MkdirAll(packageDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(packageDir, "index.d.ts"), []byte("export declare const value: number;"), 0o644); err != nil {
		t.Fatal(err)
	}
	after := build()
	if !slices.ContainsFunc(after.ImportEdges, func(edge ImportEdge) bool { return edge.Specifier == "fixture-pkg" && !edge.Unresolved }) {
		t.Fatalf("installed package did not resolve: %+v", after.ImportEdges)
	}
}
