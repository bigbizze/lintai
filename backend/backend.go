// Package backend defines the public contract implemented by analysis
// providers.
package backend

import (
	"context"
	"fmt"

	"github.com/bigbizze/lintai/facts"
)

// FileScope selects which walked workspace files become modules.
type FileScope string

const (
	// FileScopeTsconfig includes files a tsconfig compiles, plus walked files
	// imported by those files. Solution-style configs use their references.
	FileScopeTsconfig FileScope = "tsconfig"
	// FileScopeWalk includes every walked workspace file.
	FileScopeWalk FileScope = "walk"
)

// ParseFileScope parses a file scope. An empty value selects FileScopeTsconfig.
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

// SnapshotOptions selects source files for a backend snapshot.
type SnapshotOptions struct {
	// Excludes are doublestar globs, relative to the workspace root, of
	// files to leave out.
	Excludes []string
	Files    FileScope
}

// Backend builds a versioned, language-neutral fact snapshot for a workspace.
// The backend owns its declarations and prelude as well as fact extraction.
// Declarations must include facts.LocationRelation/6 with a string entity id,
// file path, and four numeric source coordinates so reported entities can be
// mapped to diagnostics.
type Backend interface {
	ID() string
	BuildSnapshot(ctx context.Context, repoRoot, workspaceRoot string, options SnapshotOptions) (facts.Snapshot, error)
}
