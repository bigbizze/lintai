package main

import (
	"context"
	"errors"
	"slices"

	"github.com/bigbizze/lintai/backend"
	"github.com/bigbizze/lintai/facts"
	"github.com/bigbizze/lintai/observe"
)

// externalProducer represents an in-memory inventory with its own revision and scope.
// The caller changes it only between session operations.
type externalProducer struct {
	namespace   string
	revision    string
	specifiers  []string
	failPrepare bool
	failEmit    bool
	prepares    int
}

func (p *externalProducer) Describe() (facts.ProducerDescription, error) {
	return facts.DescribeSchema(p.namespace, "consumer."+p.namespace, "v1", "inventory/1",
		map[string]any{"inventory": "external modules"}, facts.Schema{Declarations: `
			Decl external_boundary(Specifier) descr [extensional(), arg(Specifier, "module specifier")].
		`})
}
func (p *externalProducer) Prepare(ctx context.Context, _ backend.CollectionRequest, _ observe.Mode) (backend.Generation, error) {
	p.prepares++
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if p.failPrepare {
		return nil, errors.New("inventory preparation failed")
	}
	return externalGeneration{revision: p.revision, specifiers: slices.Clone(p.specifiers), failEmit: p.failEmit}, nil
}
func (*externalProducer) Close() error { return nil }

type externalGeneration struct {
	revision   string
	specifiers []string
	failEmit   bool
}

func (g externalGeneration) Metadata() backend.GenerationMetadata {
	return backend.GenerationMetadata{
		InputRevision: g.revision, Scope: map[string]any{"environment": "production"},
		// In-memory changes have no filesystem observations, so each refresh
		// must prepare again and obtain the current revision.
		ReusableFromObservations: false,
	}
}
func (g externalGeneration) Emit(ctx context.Context, builder *facts.Builder) error {
	item, err := builder.Relation("external_boundary", 1)
	if err != nil {
		return err
	}
	for _, specifier := range g.specifiers {
		builder.Add(item, facts.String(specifier))
	}
	if g.failEmit {
		return errors.New("inventory emission failed")
	}
	return ctx.Err()
}

var _ backend.Producer = (*externalProducer)(nil)
