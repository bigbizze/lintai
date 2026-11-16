package main

import (
	"context"
	"log"
	"path/filepath"

	"github.com/bigbizze/lintai/backend/typescript"
	"github.com/bigbizze/lintai/engine"
)

func main() {
	repoRoot, err := filepath.Abs("../..")
	if err != nil {
		log.Fatal(err)
	}
	session := engine.Open(engine.Options{}, typescript.New())
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
