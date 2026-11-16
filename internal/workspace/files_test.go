package workspace

import (
	"os"
	"path/filepath"
	"slices"
	"testing"
)

func TestListSourceFilesSkipsToolDirectoriesAndExcludes(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	for _, file := range []string{
		"src/a.ts",
		"src/b.tsx",
		"src/types.d.ts",
		"src/generated/api.ts",
		"src/a.test.ts",
		".next/types/validator.ts",
		"node_modules/pkg/index.js",
		"dist/out.js",
		"scripts/build.mjs",
	} {
		path := filepath.Join(root, filepath.FromSlash(file))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, nil, 0o644); err != nil {
			t.Fatal(err)
		}
	}

	files, err := ListSourceFiles(root, []string{"src/generated/**", "**/*.test.ts"})
	if err != nil {
		t.Fatal(err)
	}
	got := make([]string, 0, len(files))
	for _, file := range files {
		got = append(got, RelativePath(root, file))
	}
	slices.Sort(got)
	if want := []string{"src/a.ts", "src/b.tsx"}; !slices.Equal(got, want) {
		t.Fatalf("got %q, want %q", got, want)
	}

	if _, err := ListSourceFiles(root, []string{"src/[a"}); err == nil {
		t.Fatal("expected an error for an invalid exclude glob")
	}
}

func TestScopePlanAppliesRestoreExactAndInvocationPrecedenceAndTracksMembership(t *testing.T) {
	root := t.TempDir()
	for _, file := range []string{
		"src/keep.ts", "src/drop.ts", "src/other.ts", "tmp/exact.ts", "dist/contracts/api.ts",
		".hidden/restore.ts", ".git/secret.ts", "node_modules/pkg/index.ts", "types/api.d.ts", "not-ignored.ts",
	} {
		path := filepath.Join(root, filepath.FromSlash(file))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte("export {};"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(root, ".gitignore"), []byte("not-ignored.ts\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	options := ScopeOptions{
		Exclude: []string{"src/**", "tmp/**"}, Include: []string{"src/keep.ts", "dist/contracts/**", ".hidden/restore.ts"},
		Sources: []string{"tmp/exact.ts", "src/drop.ts", "types/api.d.ts"}, InvocationExclude: []string{"src/keep.ts"},
	}
	plan, err := PlanSources(root, options)
	if err != nil {
		t.Fatal(err)
	}
	got := make([]string, 0, len(plan.Files))
	for _, file := range plan.Files {
		got = append(got, RelativePath(root, file))
	}
	want := []string{".hidden/restore.ts", "dist/contracts/api.ts", "not-ignored.ts", "src/drop.ts", "tmp/exact.ts", "types/api.d.ts"}
	if !slices.Equal(got, want) {
		t.Fatalf("selected files = %q, want %q", got, want)
	}
	if slices.Contains(got, "src/keep.ts") || slices.Contains(got, ".git/secret.ts") || slices.Contains(got, "node_modules/pkg/index.ts") {
		t.Fatalf("higher-priority invocation or hard exclusions were restored: %q", got)
	}
	wildcard, err := PlanSources(root, ScopeOptions{Include: []string{"dist/co*/api.ts"}})
	if err != nil {
		t.Fatal(err)
	}
	if !slices.ContainsFunc(wildcard.Matches, func(match ScopeMatch) bool { return match.Path == "dist/contracts/api.ts" }) {
		t.Fatalf("mid-segment wildcard did not restore dist/contracts/api.ts: %+v", wildcard.Matches)
	}
	reordered, err := PlanSources(root, ScopeOptions{
		Exclude: []string{"tmp/**", "src/**"}, Include: []string{".hidden/restore.ts", "dist/contracts/**", "src/keep.ts"},
		Sources: []string{"types/api.d.ts", "src/drop.ts", "tmp/exact.ts"}, InvocationExclude: []string{"src/keep.ts"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if reordered.Revision != plan.Revision {
		t.Fatal("reordering set-valued scope options changed the scope revision")
	}
	if !slices.Contains(plan.Pruned, ".git") || !slices.Contains(plan.Pruned, "node_modules") {
		t.Fatalf("scope explanation omitted pruned boundaries: %v", plan.Pruned)
	}
	if err := os.WriteFile(filepath.Join(root, "dist/contracts/new.ts"), []byte("export {};"), 0o644); err != nil {
		t.Fatal(err)
	}
	unchanged, err := ValidateScope(plan)
	if err != nil {
		t.Fatal(err)
	}
	if unchanged {
		t.Fatal("a newly eligible source did not invalidate scope membership")
	}
	if _, err := PlanSources(root, ScopeOptions{Sources: []string{".git/secret.ts"}}); err == nil {
		t.Fatal("an exact source inside .git was accepted")
	}
	if _, err := PlanSources(root, ScopeOptions{Sources: []string{"missing.ts"}}); err == nil {
		t.Fatal("a missing exact source was treated as an empty selection")
	}
	if _, err := PlanSources(root, ScopeOptions{Sources: []string{"README.md"}}); err == nil {
		t.Fatal("an exact source with an unsupported extension was accepted")
	}
}
