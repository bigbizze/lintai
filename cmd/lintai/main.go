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
	"github.com/bigbizze/lintai/internal/server"
)

func main() {
	if err := run(context.Background(), os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, err.Error())
		os.Exit(1)
	}
}

func run(ctx context.Context, args []string) error {
	if len(args) > 0 && args[0] == "serve" {
		return runServe(ctx)
	}
	return runOnce(ctx, args)
}

func runServe(ctx context.Context) error {
	runner := engine.New(typescript.New())
	return server.New(runner).Serve(ctx, os.Stdin, os.Stdout)
}

func runOnce(ctx context.Context, args []string) error {
	flags := flag.NewFlagSet("lintai", flag.ContinueOnError)
	flags.SetOutput(os.Stderr)

	workspaceRoot := flags.String("workspace-root", ".", "workspace root to analyze")
	repoRoot := flags.String("repo-root", ".", "repository root containing scripts and packages")
	rules := flags.String("rules", "testdata/fixtures/rules/*.ts", "comma-separated list of rule globs")
	envJSON := flags.String("env-json", "{}", "JSON object passed to every rule as env")
	jsonOutput := flags.Bool("json", false, "emit diagnostics as JSON")
	if err := flags.Parse(args); err != nil {
		return err
	}

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
	result, err := runner.Analyze(ctx, engine.Options{
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
		if err := encoder.Encode(result.Diagnostics); err != nil {
			return err
		}
	} else {
		for _, item := range result.Diagnostics {
			location := ""
			if item.SourceLocation != nil {
				location = fmt.Sprintf("%s:%d:%d: ", item.SourceLocation.File, item.SourceLocation.StartLine, item.SourceLocation.StartColumn)
			}
			fmt.Printf("%s[%s/%s] %s\n", location, item.RuleID, item.AssertionID, item.Message)
		}
	}
	for _, item := range result.Diagnostics {
		if item.Severity == diagnostics.SeverityError {
			return fmt.Errorf("lintai found %d diagnostics", len(result.Diagnostics))
		}
	}
	return nil
}
