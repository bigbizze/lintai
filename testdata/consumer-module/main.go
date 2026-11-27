package main

import (
	"context"
	"log"
	"path/filepath"
	"strings"

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
	options := engine.Options{RepoRoot: repoRoot, WorkspaceRoot: filepath.Join(repoRoot, "testdata", "fixtures", "workspace")}
	ts := typescript.New()
	session, err := engine.Open(options, ts, producer)
	if err != nil {
		_ = ts.Close()
		_ = producer.Close()
		log.Fatal(err)
	}
	defer session.Close()
	rows, err := session.Facts(context.Background(), []string{"ts.module", "example.item"})
	if err != nil {
		log.Fatal(err)
	}
	if !strings.Contains(rows, "ts.module(") || !strings.Contains(rows, "example.item(") {
		log.Fatalf("missing expected relations from bound producers: %s", rows)
	}
}
