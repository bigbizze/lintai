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
	assetRoot := flags.String("asset-root", "", "asset root containing lintai helper scripts")
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
	absoluteAssetRoot, err := resolveAssetRoot(*assetRoot, absoluteRepo)
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
		AssetRoot:     absoluteAssetRoot,
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

func resolveAssetRoot(explicit, repoRoot string) (string, error) {
	if explicit != "" {
		return filepath.Abs(explicit)
	}
	candidates := []string{
		filepath.Join(repoRoot, "node_modules", "@lintai", "eslint-plugin"),
		filepath.Join(repoRoot, "packages", "eslint-plugin"),
		repoRoot,
	}
	for _, candidate := range candidates {
		if hasHelperScripts(candidate) {
			return candidate, nil
		}
	}
	return "", fmt.Errorf("could not find lintai asset root under %q or %q", candidates[0], candidates[1])
}

func hasHelperScripts(root string) bool {
	required := []string{
		filepath.Join(root, "scripts", "bundle-rule.mjs"),
		filepath.Join(root, "scripts", "prepare-rule.mjs"),
	}
	for _, item := range required {
		if _, err := os.Stat(item); err != nil {
			return false
		}
	}
	return true
}
