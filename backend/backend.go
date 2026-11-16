// Package backend defines the public contract implemented by analysis
// providers.
package backend

import (
	"context"

	"github.com/bigbizze/lintai/facts"
)

type Producer interface {
	Describe() (facts.ProducerDescription, error)
	Collect(context.Context, CollectionRequest) (facts.Batch, error)
	Close() error
}

type CollectionRequest struct {
	RepoRoot      string
	WorkspaceRoot string
	Predicates    []facts.Predicate
}
