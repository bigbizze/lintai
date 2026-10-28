// Package backend defines the public contract implemented by analysis
// providers.
package backend

import (
	"context"

	"github.com/bigbizze/lintai/facts"
)

// Backend builds a versioned, language-neutral fact snapshot for a workspace.
// The backend owns its declarations and prelude as well as fact extraction.
// Declarations must include facts.LocationRelation/6 with a string entity id,
// file path, and four numeric source coordinates so reported entities can be
// mapped to diagnostics.
type Backend interface {
	ID() string
	BuildSnapshot(ctx context.Context, repoRoot, workspaceRoot string, excludes []string) (facts.Snapshot, error)
}
