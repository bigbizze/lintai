package backend

import (
	"context"
	"fmt"

	"github.com/bigbizze/lintai/internal/analysis"
)

// FileScope selects the files that become modules.
type FileScope string

const (
	// FileScopeTsconfig keeps a walked file under a tsconfig.json only when
	// tsc compiles it: it is a root file of that tsconfig or of a referenced
	// tsconfig, or a compiled file imports it.
	FileScopeTsconfig FileScope = "tsconfig"
	// FileScopeWalk keeps every walked file.
	FileScopeWalk FileScope = "walk"
)

// ParseFileScope returns the scope for a flag value. An empty value is
// FileScopeTsconfig.
func ParseFileScope(value string) (FileScope, error) {
	switch FileScope(value) {
	case "", FileScopeTsconfig:
		return FileScopeTsconfig, nil
	case FileScopeWalk:
		return FileScopeWalk, nil
	default:
		return "", fmt.Errorf("invalid file scope %q: use tsconfig or walk", value)
	}
}

type SnapshotOptions struct {
	// Excludes are doublestar globs, relative to the workspace root, of
	// files to leave out.
	Excludes []string
	Files    FileScope
}

type Backend interface {
	ID() string
	Relations() []string
	BuildSnapshot(ctx context.Context, repoRoot, workspaceRoot string, options SnapshotOptions) (*analysis.Snapshot, error)
}
