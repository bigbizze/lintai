package observe

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLongPathReparsePoint(t *testing.T) {
	root := t.TempDir()
	for len(root) < 260 {
		root = filepath.Join(root, strings.Repeat("long-path-", 4))
	}
	target := filepath.Join(root, "target")
	if err := os.MkdirAll(target, 0o700); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(root, "link")
	if err := os.Symlink(target, link); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	if !isReparsePoint(link) {
		t.Fatal("long-path reparse point was not recognized")
	}
}
