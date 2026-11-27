package main

import (
	"context"
	"log"
	"path/filepath"

	"github.com/bigbizze/lintai/backend"
	"github.com/bigbizze/lintai/backend/typescript"
	"github.com/bigbizze/lintai/engine"
	"github.com/bigbizze/lintai/facts"
	"github.com/bigbizze/lintai/observe"
)

func main() {
	repoRoot, err := filepath.Abs("../..")
	if err != nil {
		log.Fatal(err)
	}
	producer := externalProducer{}
	description, err := producer.Describe()
	if err != nil {
		log.Fatal(err)
	}
	descriptor, err := facts.ValidateDescription(description)
	if err != nil {
		log.Fatal(err)
	}
	predicates := []facts.Predicate{{Name: "example.item", Arity: 1}}
	generation, err := producer.Prepare(context.Background(), backend.CollectionRequest{RepoRoot: repoRoot, WorkspaceRoot: repoRoot, Predicates: predicates}, observe.Full)
	if err != nil {
		log.Fatal(err)
	}
	if generation.Metadata().InputRevision == "" {
		log.Fatal("external producer has no revision")
	}
	builder, err := facts.NewBuilder(descriptor, predicates)
	if err != nil {
		log.Fatal(err)
	}
	if err := generation.Emit(context.Background(), builder); err != nil {
		log.Fatal(err)
	}
	emitted, err := builder.Finish()
	if err != nil || emitted.Count(predicates[0]) != 1 {
		log.Fatalf("external producer emission: %v", err)
	}
	session := engine.Open(engine.Options{}, typescript.New(), producer)
	defer session.Close()
	result, err := session.Check(context.Background(), engine.Options{
		RepoRoot:      repoRoot,
		WorkspaceRoot: filepath.Join(repoRoot, "testdata", "fixtures", "workspace"),
	})
	if err != nil {
		log.Fatal(err)
	}
	if result.SnapshotVersion == "" {
		log.Fatal("TypeScript backend returned an empty snapshot version")
	}
}
