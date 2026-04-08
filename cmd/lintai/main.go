package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/bigbizze/lintai/internal/backend/typescript"
	"github.com/bigbizze/lintai/internal/diagnostics"
	"github.com/bigbizze/lintai/internal/engine"
)

func main() {
	if err := run(context.Background()); err != nil {
		fmt.Fprintln(os.Stderr, err.Error())
		os.Exit(1)
	}
}

func run(ctx context.Context) error {
	workspaceRoot := flag.String("workspace-root", ".", "workspace root to analyze")
	repoRoot := flag.String("repo-root", ".", "repository root containing scripts and packages")
	rules := flag.String("rules", "testdata/fixtures/rules/*.ts", "comma-separated list of rule globs")
	envJSON := flag.String("env-json", "{}", "JSON object passed to every rule as env")
	jsonOutput := flag.Bool("json", false, "emit diagnostics as JSON")
	flag.Parse()

	absoluteWorkspace, err := filepath.Abs(*workspaceRoot)
	if err != nil {
		return err
	}
	absoluteRepo, err := filepath.Abs(*repoRoot)
	if err != nil {
		return err
	}
	env := map[string]any{}
	if err := json.Unmarshal([]byte(*envJSON), &env); err != nil {
		return fmt.Errorf("invalid --env-json value: %w", err)
	}
	ruleGlobs := strings.Split(*rules, ",")
	runner := engine.New(typescript.New())
	diagnosticsList, err := runner.Run(ctx, engine.Options{
		RepoRoot:      absoluteRepo,
		WorkspaceRoot: absoluteWorkspace,
		RuleGlobs:     ruleGlobs,
		Env:           env,
		Severity:      diagnostics.SeverityError,
	})
	if err != nil {
		return err
	}
	if *jsonOutput {
		encoder := json.NewEncoder(os.Stdout)
		encoder.SetIndent("", "  ")
		if err := encoder.Encode(diagnosticsList); err != nil {
			return err
		}
	} else {
		for _, item := range diagnosticsList {
			location := ""
			if item.SourceLocation != nil {
				location = fmt.Sprintf("%s:%d:%d: ", item.SourceLocation.File, item.SourceLocation.StartLine, item.SourceLocation.StartColumn)
			}
			fmt.Printf("%s[%s/%s] %s\n", location, item.RuleID, item.AssertionID, item.Message)
		}
	}
	for _, item := range diagnosticsList {
		if item.Severity == diagnostics.SeverityError {
			return fmt.Errorf("lintai found %d diagnostics", len(diagnosticsList))
		}
	}
	return nil
}
