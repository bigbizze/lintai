package main

import (
	"context"
	"crypto/sha256"
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/bigbizze/lintai/backend/typescript"
	"github.com/bigbizze/lintai/diagnostics"
	"github.com/bigbizze/lintai/engine"
)

func main() {
	if err := run(); err != nil {
		log.Fatal(err)
	}
}

func run() error {
	repoRoot, err := filepath.Abs("../..")
	if err != nil {
		return err
	}
	workspace, err := os.MkdirTemp("", "lintai-consumer-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(workspace)
	if err := buildFixture(repoRoot, workspace); err != nil {
		return err
	}

	// The code producer selects one file; the inventory describes an entirely
	// different, non-file scope. Their shared value is a module specifier.
	ts := typescript.New(typescript.Options{Sources: []string{"src/handler.ts"}})
	inventory := &externalProducer{namespace: "catalog", revision: "inventory-v1", specifiers: []string{"other-api"}}
	unused := &externalProducer{namespace: "unused", revision: "unused-v1", failPrepare: true}
	session, err := engine.Open(engine.Config{WorkspaceRoot: workspace}, ts, inventory, unused)
	if err != nil {
		_ = ts.Close()
		_ = inventory.Close()
		_ = unused.Close()
		return err
	}
	defer session.Close()
	ctx := context.Background()

	source, err := os.ReadFile(filepath.Join(workspace, "src", "handler.ts"))
	if err != nil {
		return err
	}
	digest := fmt.Sprintf("%x", sha256.Sum256(source))
	check := func() (engine.FilesResult, error) {
		result, err := session.RefreshFile(ctx, "src/handler.ts")
		if err != nil {
			return result, err
		}
		if result.RulesLoaded != 1 || len(result.WorkspaceProblems) != 0 || len(result.Files) != 1 ||
			result.Files[0].File != "src/handler.ts" || result.Files[0].SourceDigest != digest || result.SnapshotVersion == "" {
			return result, fmt.Errorf("unexpected publication: %+v", result)
		}
		return result, nil
	}

	first, err := check()
	if err != nil {
		return err
	}
	if len(first.Files[0].Diagnostics) != 0 {
		return fmt.Errorf("unmatched inventory should not report a finding: %+v", first.Files[0].Diagnostics)
	}
	fmt.Println("inventory-v1: no matching import")

	// Only the inventory changes. The file, rule, and TypeScript scope remain fixed.
	inventory.revision = "inventory-v2"
	inventory.specifiers = []string{"catalog-api"}
	matched, err := check()
	if err != nil {
		return err
	}
	if first.SnapshotVersion == matched.SnapshotVersion || len(matched.Files[0].Diagnostics) != 1 {
		return fmt.Errorf("changed inventory did not add the finding: %+v", matched)
	}
	finding := matched.Files[0].Diagnostics[0]
	if finding.RuleID != "fixture.external-boundary" || finding.DiagnosticKind != diagnostics.KindArchitectureViolation ||
		finding.Message != "Import catalog-api is marked as an external boundary" ||
		finding.SourceLocation == nil || *finding.SourceLocation != (diagnostics.SourceLocation{
		File: "src/handler.ts", StartLine: 1, StartColumn: 1, EndLine: 1, EndColumn: 38,
	}) ||
		finding.EntityIdentity == nil || !strings.HasPrefix(finding.EntityIdentity.SemanticKey, "ts:") ||
		strings.Join(finding.Provenance.ProducerNamespaces, ",") != "catalog,ts" {
		return fmt.Errorf("unexpected joined finding: %+v", finding)
	}
	fmt.Printf("inventory-v2: %s at %s:%d:%d-%d:%d\n", finding.Message, finding.SourceLocation.File,
		finding.SourceLocation.StartLine, finding.SourceLocation.StartColumn, finding.SourceLocation.EndLine, finding.SourceLocation.EndColumn)

	// A producer can fail after adding rows; no partial publication is usable.
	inventory.revision = "inventory-v3"
	inventory.failEmit = true
	if _, err := check(); err == nil || !strings.Contains(err.Error(), "inventory emission failed") {
		return fmt.Errorf("expected inventory emission failure, got %v", err)
	}
	if _, err := session.Current(); err == nil {
		return fmt.Errorf("failed composition left a published result")
	}
	fmt.Println("inventory-v3: emission failure invalidated the publication")

	inventory.revision = "inventory-v4"
	inventory.specifiers = []string{"other-api"}
	inventory.failEmit = false
	recovered, err := check()
	if err != nil {
		return err
	}
	if recovered.SnapshotVersion == matched.SnapshotVersion || len(recovered.Files[0].Diagnostics) != 0 {
		return fmt.Errorf("recovered inventory did not retract the finding: %+v", recovered)
	}
	if inventory.prepares != 4 || unused.prepares != 0 {
		return fmt.Errorf("unexpected preparation counts: inventory=%d unused=%d", inventory.prepares, unused.prepares)
	}
	fmt.Println("inventory-v4: recovered with no finding; failing unused producer was never called")
	return nil
}

// Publish the shared rule into a disposable workspace using the public CLI.
func buildFixture(repoRoot, workspace string) error {
	fixture := filepath.Join(repoRoot, "testdata", "fixtures", "external-backend", "mixed-facts")
	if err := os.CopyFS(workspace, os.DirFS(fixture)); err != nil {
		return err
	}
	sdk, err := os.ReadFile(filepath.Join(repoRoot, "packages", "sdk", "src", "index.ts"))
	if err != nil {
		return err
	}
	sdkPath := filepath.Join(workspace, "node_modules", "@lintai", "sdk", "index.ts")
	if err := os.MkdirAll(filepath.Dir(sdkPath), 0o755); err != nil {
		return err
	}
	if err := os.WriteFile(sdkPath, sdk, 0o644); err != nil {
		return err
	}
	config := "[rules]\nentries = [\"lintai-rules/external-boundary.ts\"]\n"
	if err := os.WriteFile(filepath.Join(workspace, "lintai.toml"), []byte(config), 0o644); err != nil {
		return err
	}
	command := exec.Command("go", "run", "-mod=readonly", "./cmd/lintai", "build-rules", "--workspace-root", workspace)
	command.Dir = repoRoot
	if output, err := command.CombinedOutput(); err != nil {
		return fmt.Errorf("build fixture rule: %w: %s", err, output)
	}
	return nil
}
