package typescript

import (
	"context"
	"path/filepath"
	"slices"
	"testing"

	"github.com/bigbizze/lintai/backend"
	"github.com/bigbizze/lintai/internal/analysis"
)

func TestSourceConfigInputsInvalidateFailedAssociation(t *testing.T) {
	const validOptions = `{"module":"preserve","moduleResolution":"bundler"}`
	tests := []struct {
		name       string
		config     string
		dependency string
		changed    string
		repaired   string
	}{
		{
			name:     "invalid custom config",
			config:   `{"compilerOptions":{"module":"invalid"},"files":["src/index.ts"]}`,
			changed:  "contracts/custom.json",
			repaired: `{"compilerOptions":` + validOptions + `,"files":["src/index.ts"]}`,
		},
		{
			name:       "invalid inherited config",
			config:     `{"extends":"./base.json","files":["src/index.ts"]}`,
			dependency: `{"compilerOptions":{"module":"invalid"}}`,
			changed:    "contracts/base.json",
			repaired:   `{"compilerOptions":` + validOptions + `}`,
		},
		{
			name:     "missing inherited config",
			config:   `{"extends":"./base.json","files":["src/index.ts"]}`,
			changed:  "contracts/base.json",
			repaired: `{"compilerOptions":` + validOptions + `}`,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			root := t.TempDir()
			writeWorkspaceFile(t, root, "tsconfig.json", `{"compilerOptions":{"module":"preserve","moduleResolution":"bundler"},"files":["app/main.ts"]}`)
			writeWorkspaceFile(t, root, "app/main.ts", `export { Widget } from "../contracts/dist";`)
			writeWorkspaceFile(t, root, "contracts/dist/index.d.ts", `export declare class Widget {}`)
			writeWorkspaceFile(t, root, "contracts/src/index.ts", `export class Widget {}`)
			writeWorkspaceFile(t, root, "contracts/custom.json", test.config)
			if test.dependency != "" {
				writeWorkspaceFile(t, root, "contracts/base.json", test.dependency)
			}
			writeWorkspaceFile(t, root, "source-links.json", `{"version":1,"entries":[{"checkedEntry":"contracts/dist/index.d.ts","sourceEntry":"contracts/src/index.ts","sourceTsconfig":"contracts/custom.json"}]}`)
			options := backend.SnapshotOptions{Files: backend.FileScopeTsconfig, SourceLinks: "source-links.json"}
			build := func() *analysis.Snapshot {
				t.Helper()
				snapshot, err := New().buildAnalysisSnapshot(context.Background(), root, root, options)
				if err != nil {
					t.Fatal(err)
				}
				return snapshot
			}

			before := build()
			changedPath := filepath.Join(root, test.changed)
			if !slices.Contains(before.InputPaths, changedPath) {
				t.Fatalf("failed association did not report the input that can repair it: %s, inputs=%v", changedPath, before.InputPaths)
			}
			if len(before.SourceEntries) != 1 || !before.SourceEntries[0].Applied || !slices.Contains(before.SourceEntries[0].Incomplete, "unsupported") {
				t.Fatalf("invalid source config did not leave an unsupported association: %+v", before.SourceEntries)
			}
			for _, space := range []string{"type", "value"} {
				index := slices.IndexFunc(before.SourceBindings, func(binding analysis.SourceBinding) bool { return binding.Name == "Widget" && binding.Space == space })
				if index < 0 {
					t.Fatalf("missing checked %s binding: %+v", space, before.SourceBindings)
				}
				binding := before.SourceBindings[index]
				if len(binding.CheckedDeclarations) != 1 || len(binding.SourceDeclarations) != 0 || binding.SourceFile != "" || !slices.Contains(binding.Incomplete, "unsupported") {
					t.Fatalf("failed association lost checked evidence in %s space: %+v", space, binding)
				}
			}

			writeWorkspaceFile(t, root, test.changed, test.repaired)
			after := build()
			if before.Version == after.Version {
				t.Fatalf("repairing the observed input did not change the snapshot version: %s", before.Version)
			}
			if len(after.SourceEntries) != 1 || !after.SourceEntries[0].Applied || len(after.SourceEntries[0].Incomplete) != 0 {
				t.Fatalf("repaired association remained incomplete: %+v", after.SourceEntries)
			}
			for _, space := range []string{"type", "value"} {
				beforeIndex := slices.IndexFunc(before.SourceBindings, func(binding analysis.SourceBinding) bool { return binding.Name == "Widget" && binding.Space == space })
				afterIndex := slices.IndexFunc(after.SourceBindings, func(binding analysis.SourceBinding) bool { return binding.Name == "Widget" && binding.Space == space })
				if afterIndex < 0 {
					t.Fatalf("missing repaired %s binding: %+v", space, after.SourceBindings)
				}
				binding := after.SourceBindings[afterIndex]
				if binding.SourceFile != "contracts/src/index.ts" || binding.Method != "source_entry" || len(binding.Incomplete) != 0 ||
					len(binding.SourceDeclarations) != 1 || !slices.Equal(binding.CheckedDeclarations, before.SourceBindings[beforeIndex].CheckedDeclarations) || binding.SourceDeclarations[0] == binding.CheckedDeclarations[0] {
					t.Fatalf("repair did not preserve checked evidence and recover the %s source association: %+v", space, binding)
				}
			}
		})
	}
}
