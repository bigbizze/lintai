package main

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"testing"
)

// The goldens pin the facts and diagnostics that the TypeScript extraction
// produces. A TypeScript upgrade must explain every difference before the
// goldens are regenerated with LINTAI_UPDATE_GOLDEN=1.
func TestGoldenParity(t *testing.T) {
	repoRoot, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	workspace := filepath.Join(repoRoot, "testdata/fixtures/workspace")
	smoke := filepath.Join(repoRoot, "testdata/npm-smoke")
	cases := []struct {
		golden string
		args   []string
		want   int
		clean  func(*testing.T, []byte) []byte
	}{
		{"workspace-facts.txt", []string{"facts", "--workspace-root", workspace}, exitClean, nil},
		{"npm-smoke-facts-tsconfig.txt", []string{"facts", "--workspace-root", smoke}, exitClean, nil},
		{"npm-smoke-facts-walk.txt", []string{"facts", "--workspace-root", smoke, "--files", "walk"}, exitClean, nil},
		{"workspace-diagnostics.json", []string{
			"--json", "--repo-root", repoRoot, "--workspace-root", workspace,
			"--rules", filepath.Join(repoRoot, "testdata/fixtures/rules/*.ts"),
		}, exitViolations, stripSnapshotVersions},
	}
	update := os.Getenv("LINTAI_UPDATE_GOLDEN") == "1"
	for _, item := range cases {
		t.Run(item.golden, func(t *testing.T) {
			var stdout bytes.Buffer
			err := run(context.Background(), item.args, &stdout)
			if code := exitCode(err, io.Discard); code != item.want {
				t.Fatalf("exit code %d, want %d: %v", code, item.want, err)
			}
			got := stdout.Bytes()
			if item.clean != nil {
				got = item.clean(t, got)
			}
			path := filepath.Join(repoRoot, "testdata/golden", item.golden)
			if update {
				if err := os.WriteFile(path, got, 0o644); err != nil {
					t.Fatal(err)
				}
				return
			}
			want, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(got, want) {
				t.Fatalf("%s differs from the golden:\n--- got\n%s\n--- want\n%s", item.golden, got, want)
			}
		})
	}
}

// The snapshot version hashes file contents and is not part of the
// extraction result.
func stripSnapshotVersions(t *testing.T, data []byte) []byte {
	var items []map[string]any
	if err := json.Unmarshal(data, &items); err != nil {
		t.Fatal(err)
	}
	for _, item := range items {
		if provenance, ok := item["provenance"].(map[string]any); ok {
			delete(provenance, "snapshot_version")
		}
	}
	out, err := json.MarshalIndent(items, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	return append(out, '\n')
}
