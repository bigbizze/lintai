package typescript

import (
	"context"
	"fmt"
	"os"
	"slices"
	"strings"
	"testing"

	"github.com/bigbizze/lintai/internal/tsgo/lintaiapi"
	"github.com/bigbizze/lintai/internal/workspace"
)

// V18, V20, V42: publication and reuse share content and scope validity. A
// changing capture is retried once, with no metadata shortcut.
func TestGenerationRetriesControlledInputChangesBeforePublication(t *testing.T) {
	for _, test := range []struct {
		name      string
		mutations int
		wantError bool
	}{
		{name: "one change is recaptured", mutations: 1},
		{name: "second change fails", mutations: 2, wantError: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			root := t.TempDir()
			file := writeWorkspaceFile(t, root, "src/main.ts", "export const value = 1;")
			producer := New(Options{Files: FileScopeWalk})
			attempts := 0
			producer.beforeValidation = func() {
				attempts++
				if attempts > test.mutations {
					return
				}
				info, err := os.Stat(file)
				if err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(file, []byte(fmt.Sprintf("export const value = %d;", attempts+1)), 0o644); err != nil {
					t.Fatal(err)
				}
				if err := os.Chtimes(file, info.ModTime(), info.ModTime()); err != nil {
					t.Fatal(err)
				}
				preserved, err := os.Stat(file)
				if err != nil || !preserved.ModTime().Equal(info.ModTime()) {
					t.Fatalf("test mutation did not preserve mtime: %v", err)
				}
			}
			got, err := producer.buildTestGeneration(context.Background(), root, root)
			if test.wantError {
				if err == nil || !strings.Contains(err.Error(), "inputs_changed") || attempts != 2 || producer.current != nil {
					t.Fatalf("expected two invalid attempts without publication, got attempts=%d err=%v", attempts, err)
				}
				return
			}
			if err != nil || attempts != 2 {
				t.Fatalf("expected successful second attempt, got attempts=%d err=%v", attempts, err)
			}
			producer.beforeValidation = nil
			fresh, err := New(Options{Files: FileScopeWalk}).buildTestGeneration(context.Background(), root, root)
			if err != nil || got.inputRevision != fresh.inputRevision {
				t.Fatalf("published revision differs from fresh capture: err=%v", err)
			}
			reused, err := producer.buildTestGeneration(context.Background(), root, root)
			if err != nil || reused != got {
				t.Fatalf("valid generation was not reused: err=%v", err)
			}
			info, err := os.Stat(file)
			if err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(file, []byte("export const value = 9;"), 0o644); err != nil {
				t.Fatal(err)
			}
			if err := os.Chtimes(file, info.ModTime(), info.ModTime()); err != nil {
				t.Fatal(err)
			}
			replaced, err := producer.buildTestGeneration(context.Background(), root, root)
			if err != nil || replaced == got || replaced.inputRevision == got.inputRevision {
				t.Fatalf("content edit did not replace retained generation: err=%v", err)
			}
		})
	}
	t.Run("source membership changes before validation", func(t *testing.T) {
		root := t.TempDir()
		writeWorkspaceFile(t, root, "src/main.ts", `export const result = 1;`)
		plan, err := workspace.PlanSources(root, workspace.ScopeOptions{})
		if err != nil {
			t.Fatal(err)
		}
		compiler, err := lintaiapi.BuildSnapshot(context.Background(), lintaiapi.BuildSnapshotRequest{
			WorkspaceRoot: plan.Root, Files: plan.Files, ExactFiles: plan.ExactFiles,
		})
		if err != nil {
			t.Fatal(err)
		}
		newFile := writeWorkspaceFile(t, root, "src/late.ts", "export const value = 1;")
		compilerStable, _, err := compiler.CompilerInputs.Validate(context.Background())
		if err != nil || !compilerStable {
			t.Fatalf("compiler inputs changed during scope-only edit: stable=%t err=%v", compilerStable, err)
		}
		scopeStable, err := workspace.ValidateScope(plan)
		if err != nil || scopeStable {
			t.Fatalf("scope-only edit did not invalidate the plan: stable=%t err=%v", scopeStable, err)
		}
		if err := os.Remove(newFile); err != nil {
			t.Fatal(err)
		}
		producer := New(Options{Files: FileScopeWalk})
		attempts := 0
		producer.beforeValidation = func() {
			attempts++
			if attempts == 1 {
				writeWorkspaceFile(t, root, "src/late.ts", "export const value = 1;")
			}
		}
		got, err := producer.buildTestGeneration(context.Background(), root, root)
		if err != nil || attempts != 2 {
			t.Fatalf("membership change: attempts=%d err=%v", attempts, err)
		}
		fresh, err := New(Options{Files: FileScopeWalk}).buildTestGeneration(context.Background(), root, root)
		if err != nil || got.inputRevision != fresh.inputRevision || !slices.Contains(got.scope["primary"].([]string), "src/late.ts") {
			t.Fatalf("published generation missed new member: err=%v scope=%v", err, got.scope)
		}
		producer.beforeValidation = nil
		writeWorkspaceFile(t, root, "src/another.ts", "export const another = 2;")
		replaced, err := producer.buildTestGeneration(context.Background(), root, root)
		if err != nil || replaced == got || !slices.Contains(replaced.scope["primary"].([]string), "src/another.ts") {
			t.Fatalf("scope change did not replace retained generation: err=%v", err)
		}
	})
	t.Run("missing resolution candidate created before validation", func(t *testing.T) {
		root := t.TempDir()
		writeWorkspaceFile(t, root, "src/main.ts", `import { value } from "./late"; export const result = value;`)
		producer := New(Options{Files: FileScopeWalk})
		attempts := 0
		producer.beforeValidation = func() {
			attempts++
			if attempts == 1 {
				writeWorkspaceFile(t, root, "src/late.ts", "export const value = 1;")
			}
		}
		got, err := producer.buildTestGeneration(context.Background(), root, root)
		if err != nil || attempts != 2 {
			t.Fatalf("resolution change: attempts=%d err=%v", attempts, err)
		}
		fresh, err := New(Options{Files: FileScopeWalk}).buildTestGeneration(context.Background(), root, root)
		if err != nil || got.inputRevision != fresh.inputRevision || !slices.ContainsFunc(got.snapshot.ImportEdges, func(edge lintaiapi.ImportEdge) bool {
			return edge.Specifier == "./late" && !edge.Unresolved
		}) {
			t.Fatalf("published generation missed resolved input: err=%v", err)
		}
	})
}
