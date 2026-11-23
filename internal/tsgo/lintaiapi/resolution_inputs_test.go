package lintaiapi

import (
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"os"
	"path/filepath"
	"slices"
	"testing"
	"unicode/utf16"

	"github.com/bigbizze/lintai/internal/tsgo/internal/tspath"
	"github.com/bigbizze/lintai/internal/tsgo/internal/vfs/osvfs"
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

func TestCompilerDecodesAndHashesTheCapturedBytes(t *testing.T) {
	text := "export function value() { return 1; }\n"
	utf16Bytes := func(order binary.ByteOrder) []byte {
		var result []byte
		if order == binary.LittleEndian {
			result = []byte{0xff, 0xfe}
		} else {
			result = []byte{0xfe, 0xff}
		}
		for _, unit := range utf16.Encode([]rune(text)) {
			pair := make([]byte, 2)
			order.PutUint16(pair, unit)
			result = append(result, pair...)
		}
		return result
	}
	for _, test := range []struct {
		name string
		raw  []byte
	}{
		{"utf8-bom", append([]byte{0xef, 0xbb, 0xbf}, []byte(text)...)},
		{"utf16-le", utf16Bytes(binary.LittleEndian)},
		{"utf16-be", utf16Bytes(binary.BigEndian)},
	} {
		t.Run(test.name, func(t *testing.T) {
			root, err := filepath.EvalSymlinks(t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			file := filepath.Join(root, "a.ts")
			if err := os.WriteFile(file, test.raw, 0o600); err != nil {
				t.Fatal(err)
			}
			snapshot, err := BuildSnapshot(context.Background(), BuildSnapshotRequest{WorkspaceRoot: root, Files: []string{file}})
			if err != nil || len(snapshot.Functions) != 1 {
				t.Fatalf("decoded compiler facts: %+v %v", snapshot, err)
			}
			fsys := trackResolutionInputs(osvfs.FS())
			path := tspath.RootedFilePathFromAbsolute(file)
			got, ok := fsys.ReadFile(path)
			if !ok || got != text {
				t.Fatalf("decoded %q, ok=%v", got, ok)
			}
			if err := os.WriteFile(file, []byte("changed"), 0o600); err != nil {
				t.Fatal(err)
			}
			again, ok := fsys.ReadFile(path)
			if !ok || again != text {
				t.Fatal("repeated read did not use captured text")
			}
			sum := sha256.Sum256(test.raw)
			found := false
			for _, record := range fsys.observations() {
				if record.Kind == "content" && record.Path == file {
					found = true
					if record.Value != hex.EncodeToString(sum[:]) {
						t.Fatalf("digest %s does not describe original bytes", record.Value)
					}
				}
			}
			if !found {
				t.Fatal("missing raw content observation")
			}
		})
	}
}
