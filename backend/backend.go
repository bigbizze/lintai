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
	WorkspaceRoot string
	Predicates    []facts.Predicate
}

type GenerationMetadata struct {
	InputRevision string
	Scope         any
	AnalyzedFiles []string
	// SourceDigests optionally identifies the immutable text used for file
	// coordinates. Values are lowercase hex SHA-256 of decoded UTF-8 text
	// after removing one leading BOM. Preserve line endings.
	SourceDigests map[string]string
	// Documents maps a file name this generation reports, in analyzed files,
	// source digests or anchor locations, to the canonical absolute path of
	// the document that supplied its text, recorded when the producer read
	// it. Names are interpreted lexically against WorkspaceRoot, without
	// looking up files again. Equivalent names within this generation may
	// declare the same destination, but conflicting destinations are invalid.
	// Declarations belong to this producer. A name without an entry is its
	// own document, relative to the workspace root when not absolute.
	// Non-file producers leave it empty.
	Documents                map[string]string
	Observations             observe.Set
	ReusableFromObservations bool
}

// Generation is a frozen preparation. Emit always projects this generation,
// even if its producer has since prepared a different request.
type Generation interface {
	Metadata() GenerationMetadata
	Emit(ctx context.Context, builder *facts.Builder) error
}
