package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/bigbizze/lintai/internal/backend/typescript"
	"github.com/bigbizze/lintai/internal/diagnostics"
	"github.com/bigbizze/lintai/internal/engine"
	"github.com/bigbizze/lintai/internal/facts"
	"github.com/bigbizze/lintai/internal/server"
)

func main() {
	if err := run(context.Background(), os.Args[1:], os.Stdout); err != nil {
		fmt.Fprintln(os.Stderr, err.Error())
		os.Exit(1)
	}
}

func run(ctx context.Context, args []string, stdout io.Writer) error {
	if len(args) > 0 {
		switch args[0] {
		case "serve":
			return runServe(ctx)
		case "schema":
			_, err := fmt.Fprintf(stdout, "%s\n%s", facts.Schema, facts.Prelude)
			return err
		case "facts":
			return runFacts(ctx, args[1:], stdout)
		}
	}
	return runOnce(ctx, args, stdout)
}

type stringList []string

func (l *stringList) String() string {
	return strings.Join(*l, ",")
}

func (l *stringList) Set(value string) error {
	*l = append(*l, value)
	return nil
}

func runFacts(ctx context.Context, args []string, stdout io.Writer) error {
	flags := flag.NewFlagSet("lintai facts", flag.ContinueOnError)
	flags.SetOutput(os.Stderr)
	workspaceRoot := flags.String("workspace-root", ".", "workspace root to analyze")
	var relations stringList
	flags.Var(&relations, "relation", "base relation to print (repeatable; default: all)")
	if err := flags.Parse(args); err != nil {
		return err
	}
	absoluteWorkspace, err := filepath.Abs(*workspaceRoot)
	if err != nil {
		return err
	}
	snapshot, err := typescript.New().BuildSnapshot(ctx, absoluteWorkspace, absoluteWorkspace)
	if err != nil {
		return err
	}
	store, err := facts.FromSnapshot(snapshot)
	if err != nil {
		return err
	}
	text, err := facts.Format(store, relations)
	if err != nil {
		return err
	}
	if text != "" {
		fmt.Fprintln(stdout, text)
	}
	return nil
}

func runServe(ctx context.Context) error {
	runner := engine.New(typescript.New())
	return server.New(runner).Serve(ctx, os.Stdin, os.Stdout)
}

func runOnce(ctx context.Context, args []string, stdout io.Writer) error {
	flags := flag.NewFlagSet("lintai", flag.ContinueOnError)
	flags.SetOutput(os.Stderr)

	workspaceRoot := flags.String("workspace-root", ".", "workspace root to analyze")
	repoRoot := flags.String("repo-root", ".", "repository root containing scripts and packages")
	assetRoot := flags.String("asset-root", "", "asset root containing lintai helper scripts")
	rules := flags.String("rules", "lintai-rules/**/*.ts", "comma-separated list of rule globs, relative to --workspace-root")
	envJSON := flags.String("env-json", "{}", "JSON object passed to every rule as env")
	jsonOutput := flags.Bool("json", false, "emit diagnostics as JSON")
	explain := flags.Bool("explain", false, "attach the proof of each violation to JSON diagnostics (requires --json)")
	factLimit := flags.Int("fact-limit", engine.DefaultFactLimit, "maximum number of facts a rule may derive")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if *explain && !*jsonOutput {
		return fmt.Errorf("--explain requires --json")
	}
	if *factLimit <= 0 {
		return fmt.Errorf("--fact-limit must be positive")
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
		FactLimit:     *factLimit,
		Explain:       *explain,
	})
	if err != nil {
		return err
	}
	if *jsonOutput {
		encoder := json.NewEncoder(stdout)
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
			fmt.Fprintf(stdout, "%s[%s/%s] %s\n", location, item.RuleID, item.AssertionID, item.Message)
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
