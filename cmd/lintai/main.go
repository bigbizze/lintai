package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/bigbizze/lintai/backend"
	"github.com/bigbizze/lintai/backend/typescript"
	"github.com/bigbizze/lintai/diagnostics"
	"github.com/bigbizze/lintai/engine"
	"github.com/bigbizze/lintai/facts"
	"github.com/bigbizze/lintai/server"
)

// Exit codes. CI can tell "the rules found violations" apart from "lintai
// could not check the workspace".
const (
	exitClean      = 0
	exitViolations = 1
	exitUsage      = 2
	exitRuleError  = 3
)

// exitError carries an exit code. An error without one is a usage or
// configuration error.
type exitError struct {
	code    int
	message string
}

func (e *exitError) Error() string {
	return e.message
}

func main() {
	os.Exit(exitCode(run(context.Background(), os.Args[1:], os.Stdout), os.Stderr))
}

func exitCode(err error, stderr io.Writer) int {
	if err == nil {
		return exitClean
	}
	if errors.Is(err, flag.ErrHelp) {
		return exitClean
	}
	fmt.Fprintln(stderr, err.Error())
	var coded *exitError
	if errors.As(err, &coded) {
		return coded.code
	}
	return exitUsage
}

func run(ctx context.Context, args []string, stdout io.Writer) error {
	if len(args) > 0 {
		switch args[0] {
		case "serve":
			return runServe(ctx)
		case "schema":
			schema := typescript.Schema()
			_, err := fmt.Fprintf(stdout, "%s\n%s", schema.Declarations, schema.Prelude)
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
	flags.String("asset-root", "", "ignored; the npm launcher passes it to every command")
	var excludes stringList
	flags.Var(&excludes, "exclude", "glob of workspace files to leave out of the snapshot (repeatable)")
	files := flags.String("files", string(backend.FileScopeTsconfig), "files in the snapshot: tsconfig (files that tsc compiles) or walk (every walked file)")
	var relations stringList
	flags.Var(&relations, "relation", "base relation to print (repeatable; default: all)")
	if err := flags.Parse(args); err != nil {
		return err
	}
	scope, err := backend.ParseFileScope(*files)
	if err != nil {
		return err
	}
	absoluteWorkspace, err := filepath.Abs(*workspaceRoot)
	if err != nil {
		return err
	}
	snapshot, err := typescript.New().BuildSnapshot(ctx, absoluteWorkspace, absoluteWorkspace, backend.SnapshotOptions{Excludes: excludes, Files: scope})
	if err != nil {
		return err
	}
	text, err := facts.Format(snapshot, relations)
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
	var excludes stringList
	flags.Var(&excludes, "exclude", "glob of workspace files to leave out of the snapshot (repeatable)")
	files := flags.String("files", string(backend.FileScopeTsconfig), "files in the snapshot: tsconfig (files that tsc compiles) or walk (every walked file)")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if *explain && !*jsonOutput {
		return fmt.Errorf("--explain requires --json")
	}
	if *factLimit <= 0 {
		return fmt.Errorf("--fact-limit must be positive")
	}
	scope, err := backend.ParseFileScope(*files)
	if err != nil {
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
		Excludes:      excludes,
		Files:         scope,
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
	return diagnosticsOutcome(result.Diagnostics)
}

// diagnosticsOutcome returns a rule error when a rule could not run, and a
// violation error when a rule reported an error-severity violation.
func diagnosticsOutcome(items []diagnostics.Diagnostic) error {
	ruleErrors, violations := 0, 0
	for _, item := range items {
		switch {
		case item.DiagnosticKind == diagnostics.KindRuleExecutionError:
			ruleErrors++
		case item.Severity == diagnostics.SeverityError:
			violations++
		}
	}
	switch {
	case ruleErrors > 0:
		return &exitError{code: exitRuleError, message: fmt.Sprintf("lintai: %d rule errors and %d violations", ruleErrors, violations)}
	case violations > 0:
		return &exitError{code: exitViolations, message: fmt.Sprintf("lintai: %d violations", violations)}
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
