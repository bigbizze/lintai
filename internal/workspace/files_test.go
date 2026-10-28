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
