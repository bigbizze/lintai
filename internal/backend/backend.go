package backend

import (
	"context"

	"github.com/bigbizze/lintai/internal/analysis"
)

type CapabilityManifest struct {
	EntityKinds []string
	Operators   []string
	QueryKinds  []string
}

func (m CapabilityManifest) SupportsEntityKind(kind string) bool {
	for _, item := range m.EntityKinds {
		if item == kind {
			return true
		}
	}
	return false
}

func (m CapabilityManifest) SupportsQueryKind(kind string) bool {
	for _, item := range m.QueryKinds {
		if item == kind {
			return true
		}
	}
	return false
}

func (m CapabilityManifest) SupportsOperator(name string) bool {
	for _, item := range m.Operators {
		if item == name {
			return true
		}
	}
	return false
}

type Backend interface {
	ID() string
	Capabilities() CapabilityManifest
	BuildSnapshot(ctx context.Context, repoRoot, workspaceRoot string) (*analysis.Snapshot, error)
}
