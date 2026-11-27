// Package backend defines the public contract implemented by analysis providers.
package backend

import (
	"context"

	"github.com/bigbizze/lintai/facts"
	"github.com/bigbizze/lintai/observe"
)

type Producer interface {
	Describe() (facts.ProducerDescription, error)
	Prepare(ctx context.Context, request CollectionRequest, mode observe.Mode) (Generation, error)
	Close() error
}

type CollectionRequest struct {
	RepoRoot      string
	WorkspaceRoot string
	Predicates    []facts.Predicate
}

type GenerationMetadata struct {
	InputRevision            string
	Scope                    any
	AnalyzedFiles            []string
	Observations             observe.Set
	ReusableFromObservations bool
}

// Generation is a frozen preparation. Emit always projects this generation,
// even if its producer has since prepared a different request.
type Generation interface {
	Metadata() GenerationMetadata
	Emit(ctx context.Context, builder *facts.Builder) error
}
