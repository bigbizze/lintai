package typescript

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestBuildSnapshotExposesStableFunctionKeysAndCalls(t *testing.T) {
	repoRoot, err := filepath.Abs("../../..")
	if err != nil {
		t.Fatal(err)
	}
	workspaceRoot := filepath.Join(repoRoot, "testdata/fixtures/workspace")

	snapshot, err := New().BuildSnapshot(context.Background(), repoRoot, workspaceRoot)
	if err != nil {
		t.Fatal(err)
	}

	if len(snapshot.FunctionsByKey) != len(snapshot.Functions) {
		t.Fatalf("expected FunctionsByKey to cover all functions, got %d keys for %d functions", len(snapshot.FunctionsByKey), len(snapshot.Functions))
	}

	for _, fn := range snapshot.Functions {
		if strings.Contains(fn.SemanticKey, "@") {
			t.Fatalf("expected stable semantic key without position marker, got %q", fn.SemanticKey)
		}
	}

	foundHelperEffect := false
	foundComputeHelper := false
	for _, edge := range snapshot.CallEdges {
		switch {
		case edge.FromName == "loadValue" && edge.ToName == "fetchData":
			foundHelperEffect = true
		case edge.FromName == "compute" && edge.ToName == "loadValue":
			foundComputeHelper = true
		}
	}
	if !foundHelperEffect || !foundComputeHelper {
		t.Fatalf("expected fixture call edges, got %+v", snapshot.CallEdges)
	}
}

func TestSnapshotVersionIgnoresFileOrderAndAbsoluteRoot(t *testing.T) {
	t.Parallel()

	rootOne := t.TempDir()
	rootTwo := t.TempDir()

	firstOne := writeWorkspaceFile(t, rootOne, "src/a.ts", "export const alpha = 1;\n")
	secondOne := writeWorkspaceFile(t, rootOne, "src/b.ts", "export const beta = 2;\n")
	firstTwo := writeWorkspaceFile(t, rootTwo, "src/a.ts", "export const alpha = 1;\n")
	secondTwo := writeWorkspaceFile(t, rootTwo, "src/b.ts", "export const beta = 2;\n")

	left, err := snapshotVersion(rootOne, []string{secondOne, firstOne})
	if err != nil {
		t.Fatal(err)
	}
	right, err := snapshotVersion(rootTwo, []string{firstTwo, secondTwo})
	if err != nil {
		t.Fatal(err)
	}
	if left != right {
		t.Fatalf("expected stable hash across order and roots, got %q != %q", left, right)
	}
}

func TestSnapshotVersionUsesDelimitersBetweenPathsAndContents(t *testing.T) {
	t.Parallel()

	rootOne := t.TempDir()
	rootTwo := t.TempDir()

	ab := writeWorkspaceFile(t, rootOne, "ab", "cd")
	abc := writeWorkspaceFile(t, rootTwo, "abc", "d")

	left, err := snapshotVersion(rootOne, []string{ab})
	if err != nil {
		t.Fatal(err)
	}
	right, err := snapshotVersion(rootTwo, []string{abc})
	if err != nil {
		t.Fatal(err)
	}
	if left == right {
		t.Fatalf("expected different hashes for path/content boundary cases, got %q", left)
	}
}

func writeWorkspaceFile(t *testing.T, root, relativePath, contents string) string {
	t.Helper()

	path := filepath.Join(root, filepath.FromSlash(relativePath))
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(contents), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}
