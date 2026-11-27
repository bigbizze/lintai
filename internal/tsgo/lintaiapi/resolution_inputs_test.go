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
	if !slices.ContainsFunc(before.CompilerInputs, func(input CompilerObservation) bool { return input.Path == nodeModules && observationMissing(input) }) {
		t.Fatalf("missing external node_modules directory was not tracked: %+v", before.CompilerInputs)
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
	text := "export function value() { return '🙂'; }\r\n"
	utf16Bytes := func(order binary.ByteOrder, text string) []byte {
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
		name, decoded string
		raw           []byte
	}{
		{"utf8", text, []byte(text)},
		{"utf8-bom", text, []byte("\uFEFF" + text)},
		{"utf8-double-bom", "\uFEFF" + text, []byte("\uFEFF\uFEFF" + text)},
		{"utf16-le", text, utf16Bytes(binary.LittleEndian, text)},
		{"utf16-be", text, utf16Bytes(binary.BigEndian, text)},
		{"utf16-le-double-bom", "\uFEFF" + text, utf16Bytes(binary.LittleEndian, "\uFEFF"+text)},
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
			wantTextHash := sha256.Sum256([]byte(test.decoded))
			if got := snapshot.SourceDigests["a.ts"]; got != hex.EncodeToString(wantTextHash[:]) {
				t.Fatalf("source digest %s, want %x for decoded text", got, wantTextHash)
			}
			fsys := trackResolutionInputs(osvfs.FS())
			path := tspath.RootedFilePathFromAbsolute(file)
			got, ok := fsys.ReadFile(path)
			if !ok || got != test.decoded {
				t.Fatalf("decoded %q, ok=%v", got, ok)
			}
			if err := os.WriteFile(file, []byte("changed"), 0o600); err != nil {
				t.Fatal(err)
			}
			again, ok := fsys.ReadFile(path)
			if !ok || again != test.decoded {
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

func TestCompilerRealpathMemoizesTheCapturedAnswer(t *testing.T) {
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	first := filepath.Join(root, "first")
	second := filepath.Join(root, "second")
	for _, name := range []string{first, second} {
		if err := os.WriteFile(name, []byte(name), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	link := filepath.Join(root, "link")
	if err := os.Symlink(first, link); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	fsys := trackResolutionInputs(osvfs.FS())
	path := tspath.RootedPathFromAbsolute(link)
	got := fsys.Realpath(path)
	if got.AsString() != filepath.ToSlash(first) {
		t.Fatalf("resolved %q, want %q", got.AsString(), first)
	}
	if err := os.Remove(link); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(second, link); err != nil {
		t.Fatal(err)
	}
	if again := fsys.Realpath(path); again != got {
		t.Fatalf("resolution changed from %q to %q within a collection", got.AsString(), again.AsString())
	}
	if !slices.ContainsFunc(fsys.observations(), func(record CompilerObservation) bool {
		return record.Kind == "resolved" && record.Path == link && record.Value == first
	}) {
		t.Fatal("resolution observation does not match the compiler's answer")
	}

	missing := filepath.Join(root, "missing")
	missingPath := tspath.RootedPathFromAbsolute(missing)
	if got := fsys.Realpath(missingPath); got != missingPath {
		t.Fatalf("missing resolution returned %q", got.AsString())
	}
	if !slices.ContainsFunc(fsys.observations(), func(record CompilerObservation) bool {
		return record.Kind == "resolved" && record.Path == missing && record.Value == "missing"
	}) {
		t.Fatal("failed realpath did not record its failure")
	}
	if err := os.Symlink(first, missing); err != nil {
		t.Fatal(err)
	}
	if got := fsys.Realpath(missingPath); got != missingPath {
		t.Fatalf("missing resolution was not memoized: %q", got.AsString())
	}
}
