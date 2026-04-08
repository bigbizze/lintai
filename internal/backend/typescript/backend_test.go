package typescript

import (
	"context"
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
