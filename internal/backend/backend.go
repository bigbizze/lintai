package backend

import (
	"context"

	"github.com/bigbizze/lintai/internal/analysis"
)

type Backend interface {
	ID() string
	Relations() []string
	BuildSnapshot(ctx context.Context, repoRoot, workspaceRoot string, excludes []string) (*analysis.Snapshot, error)
}
