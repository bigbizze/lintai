package workspace

import (
	"os"
	"path/filepath"
	"testing"
)

func TestExactSourceAliasesKeepTheConfiguredSpelling(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "src"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "src", "a.js"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("src", filepath.Join(root, "alias")); err != nil {
		t.Skip(err)
	}
	plan, err := PlanSources(root, ScopeOptions{Sources: []string{"alias/a.js"}, Exclude: []string{"src/**"}})
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.Files) != 1 || plan.ExactAliases["alias/a.js"] != "src/a.js" {
		t.Fatalf("files %v aliases %v", plan.Files, plan.ExactAliases)
	}
}
