package main

import (
	"context"

	"github.com/bigbizze/lintai/backend"
	"github.com/bigbizze/lintai/diagnostics"
	"github.com/bigbizze/lintai/facts"
	"github.com/bigbizze/lintai/observe"
)

// externalProducer demonstrates an immutable producer implemented in a separate module.
type externalProducer struct{}

func (externalProducer) Describe() (facts.ProducerDescription, error) {
	return facts.DescribeSchema("example", "consumer.example", "v1", "example/1", nil, facts.Schema{Declarations: `Decl item(Entity) descr [extensional(), arg_role(Entity, "entity"), arg_report_support(Entity, "required")].`})
}
func (externalProducer) Prepare(ctx context.Context, _ backend.CollectionRequest, _ observe.Mode) (backend.Generation, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return externalGeneration{}, nil
}
func (externalProducer) Close() error { return nil }

type externalGeneration struct{}

func (externalGeneration) Metadata() backend.GenerationMetadata {
	return backend.GenerationMetadata{InputRevision: "immutable-example-v1", ReusableFromObservations: true}
}
func (externalGeneration) Emit(ctx context.Context, builder *facts.Builder) error {
	item, err := builder.Relation("item", 1)
	if err != nil {
		return err
	}
	builder.Add(item, facts.String("example"))
	builder.Anchor("example", facts.AnchorSupport{Location: diagnostics.SourceLocation{File: "example.txt", StartLine: 1, StartColumn: 1, EndLine: 1, EndColumn: 2}, Description: "example item"})
	return ctx.Err()
}

var _ backend.Producer = externalProducer{}
