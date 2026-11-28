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
	"reflect"
	"slices"
	"sort"
	"strings"

	"github.com/bigbizze/lintai/backend"
	"github.com/bigbizze/lintai/backend/typescript"
	"github.com/bigbizze/lintai/diagnostics"
	"github.com/bigbizze/lintai/engine"
	"github.com/bigbizze/lintai/internal/rulepack"
	"github.com/bigbizze/lintai/internal/workspace"
	"github.com/bigbizze/lintai/observe"
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
		case "build-rules":
			return runBuildRules(ctx, args[1:])
		case "check":
			return runOnce(ctx, args[1:], stdout)
		case "serve":
			if len(args) != 1 {
				return fmt.Errorf("unexpected serve arguments: %v", args[1:])
			}
			return runServe(ctx)
		case "schema":
			return runSchema(ctx, args[1:], stdout)
		case "facts":
			return runFacts(ctx, args[1:], stdout)
		case "scope":
			return runScope(ctx, args[1:], stdout)
		case "lock":
			return runLock(ctx, args[1:], stdout)
		case "rules":
			return runRules(ctx, args[1:], stdout)
		}
	}
	return runOnce(ctx, args, stdout)
}

func runBuildRules(ctx context.Context, args []string) error {
	flags := flag.NewFlagSet("lintai build-rules", flag.ContinueOnError)
	root := flags.String("workspace-root", ".", "workspace containing lintai.toml")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if flags.NArg() != 0 {
		return fmt.Errorf("unexpected build-rules arguments: %v", flags.Args())
	}
	config, err := workspace.LoadWorkspaceConfig(filepath.Join(*root, "lintai.toml"), false)
	if err != nil {
		return err
	}
	return rulepack.Build(ctx, *root, config.Entries)
}

// The CLI constructs these producers for this session. Release them if
// validation fails before Open can take ownership.
func openSession(options engine.Config, producers ...backend.Producer) (*engine.Session, error) {
	session, err := engine.Open(options, producers...)
	if err != nil {
		for _, producer := range producers {
			_ = producer.Close()
		}
	}
	return session, err
}

func runSchema(ctx context.Context, args []string, stdout io.Writer) error {
	flags := flag.NewFlagSet("lintai schema", flag.ContinueOnError)
	flags.SetOutput(os.Stderr)
	workspaceRoot := flags.String("workspace-root", ".", "workspace root whose configured producer catalog to print")
	configPath := flags.String("config", "", "lintai.toml file (relative to the invocation directory)")
	if err := flags.Parse(args); err != nil {
		return err
	}
	absoluteWorkspace, err := filepath.Abs(*workspaceRoot)
	if err != nil {
		return err
	}
	settings, err := loadTypeScriptSettings(absoluteWorkspace, *configPath, "", false, "", false, nil, nil)
	if err != nil {
		return err
	}
	producers := make([]backend.Producer, 0, len(settings))
	for _, item := range settings {
		producers = append(producers, newBackend(item))
	}
	session, err := openSession(engine.Config{WorkspaceRoot: absoluteWorkspace}, producers...)
	if err != nil {
		return err
	}
	defer session.Close()
	schema, err := session.Schema(ctx)
	if err != nil {
		return err
	}
	_, err = fmt.Fprintln(stdout, schema)
	return err
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
	configPath := flags.String("config", "", "lintai.toml file (relative to the invocation directory)")
	var excludes stringList
	flags.Var(&excludes, "exclude", "glob of workspace files to leave out of the snapshot (repeatable)")
	var sources stringList
	flags.Var(&sources, "source", "exact workspace source file to select (repeatable)")
	files := flags.String("files", "", "files in the snapshot: tsconfig (files that tsc compiles) or walk (every walked file)")
	sourceLinks := flags.String("source-links", "", "JSON source-entry associations, relative to --workspace-root")
	var relations stringList
	flags.Var(&relations, "relation", "qualified base relation to print (repeatable; default: all)")
	if err := flags.Parse(args); err != nil {
		return err
	}
	absoluteWorkspace, err := filepath.Abs(*workspaceRoot)
	if err != nil {
		return err
	}
	settings, err := loadTypeScriptSettings(absoluteWorkspace, *configPath, *files, flagProvided(flags, "files"), *sourceLinks, flagProvided(flags, "source-links"), excludes, sources)
	if err != nil {
		return err
	}
	producers := make([]backend.Producer, 0, len(settings))
	for _, item := range settings {
		producers = append(producers, newBackend(item))
	}
	session, err := openSession(engine.Config{WorkspaceRoot: absoluteWorkspace}, producers...)
	if err != nil {
		return err
	}
	defer session.Close()
	output, err := session.Facts(ctx, relations)
	if err != nil {
		return err
	}
	if output != "" {
		fmt.Fprintln(stdout, output)
	}
	return nil
}

func runServe(ctx context.Context) error {
	instance := server.NewWithFactory(func(params server.InitializeParams) server.Analyzer {
		return &serveSession{params: params}
	})
	instance.Log = os.Stderr
	return instance.Serve(ctx, os.Stdin, os.Stdout)
}

// serveSession owns the configuration receipt and the producers made from it.
type serveSession struct {
	params   server.InitializeParams
	config   workspace.WorkspaceConfig
	receipt  bool
	settings []typescript.Options
	session  *engine.Session
}

func (s *serveSession) configure(ctx context.Context) (err error) {
	defer func() {
		if err != nil {
			_ = s.Close()
		}
	}()
	if err = ctx.Err(); err != nil {
		return err
	}
	if s.receipt && s.session != nil && len(s.config.Observations.Records()) != 0 {
		var changed *observe.Record
		changed, err = s.config.Observations.Changed(ctx, observe.Quick)
		if err != nil {
			return fmt.Errorf("validate workspace configuration: %w", err)
		}
		if changed == nil {
			return ctx.Err()
		}
	}
	var config workspace.WorkspaceConfig
	var settings []typescript.Options
	settings, config, err = captureTypeScriptSettings(s.params.WorkspaceRoot, "", "", false, "", false, nil, nil)
	if err != nil {
		return fmt.Errorf("load workspace configuration: %w", err)
	}
	if err = ctx.Err(); err != nil {
		return err
	}
	if s.session == nil || !reflect.DeepEqual(settings, s.settings) || !reflect.DeepEqual(config.Rule, s.config.Rule) {
		if s.session != nil {
			err = s.session.Close()
			s.session = nil
			if err != nil {
				return err
			}
		}
		producers := make([]backend.Producer, 0, len(settings))
		for _, item := range settings {
			producers = append(producers, newBackend(item))
		}
		s.session, err = openSession(engine.Config{
			WorkspaceRoot: s.params.WorkspaceRoot, Rule: config.Rule, FactLimit: s.params.FactLimit,
			RuleWorkers: s.params.RuleWorkers, RulesLock: s.params.RulesLock, SuppressionsLocation: s.params.SuppressionsLocation,
		}, producers...)
		if err != nil {
			return err
		}
		s.settings = settings
	}
	s.config, s.receipt = config, true
	return nil
}

func (s *serveSession) RefreshFiles(ctx context.Context, files []string) (engine.FilesResult, error) {
	if err := s.configure(ctx); err != nil {
		return engine.FilesResult{}, err
	}
	if err := ctx.Err(); err != nil {
		_ = s.Close()
		return engine.FilesResult{}, err
	}
	return s.session.RefreshFiles(ctx, files)
}

func (s *serveSession) RefreshAll(ctx context.Context) (engine.FilesResult, error) {
	if err := s.configure(ctx); err != nil {
		return engine.FilesResult{}, err
	}
	return s.session.RefreshAll(ctx)
}

func (s *serveSession) Close() error {
	s.receipt = false
	s.config = workspace.WorkspaceConfig{}
	s.settings = nil
	if s.session != nil {
		session := s.session
		s.session = nil
		return session.Close()
	}
	return nil
}

func flagProvided(flags *flag.FlagSet, name string) bool {
	provided := false
	flags.Visit(func(item *flag.Flag) {
		if item.Name == name {
			provided = true
		}
	})
	return provided
}

func loadTypeScriptSettings(workspaceRoot, explicitConfig, filesOverride string, filesSet bool, sourceLinksOverride string, sourceLinksSet bool, invocationExcludes, invocationSources []string) ([]typescript.Options, error) {
	settings, _, err := captureTypeScriptSettings(workspaceRoot, explicitConfig, filesOverride, filesSet, sourceLinksOverride, sourceLinksSet, invocationExcludes, invocationSources)
	return settings, err
}

func captureTypeScriptSettings(workspaceRoot, explicitConfig, filesOverride string, filesSet bool, sourceLinksOverride string, sourceLinksSet bool, invocationExcludes, invocationSources []string) ([]typescript.Options, workspace.WorkspaceConfig, error) {
	invocationDir, err := os.Getwd()
	if err != nil {
		return nil, workspace.WorkspaceConfig{}, err
	}
	configPath, optional, err := workspace.ResolveConfigPath(workspaceRoot, invocationDir, explicitConfig)
	if err != nil {
		return nil, workspace.WorkspaceConfig{}, err
	}
	config, err := workspace.LoadWorkspaceConfig(configPath, optional)
	if err != nil {
		return nil, workspace.WorkspaceConfig{}, err
	}
	producers := config.Producers
	if len(producers) == 0 {
		producers = map[string]workspace.ProducerConfig{"ts": {}}
	}
	namespaces := make([]string, 0, len(producers))
	for namespace := range producers {
		namespaces = append(namespaces, namespace)
	}
	sort.Strings(namespaces)
	if len(namespaces) > 1 && (filesSet || sourceLinksSet) {
		return nil, workspace.WorkspaceConfig{}, fmt.Errorf("TypeScript command-line scope flags are ambiguous with multiple TypeScript producers; configure them in lintai.toml")
	}
	settings := make([]typescript.Options, 0, len(namespaces))
	for _, namespace := range namespaces {
		producerConfig := producers[namespace]
		implementation := producerConfig.Implementation
		if implementation == "" && namespace == "ts" {
			implementation = "lintai.typescript"
		}
		if implementation != "lintai.typescript" {
			return nil, workspace.WorkspaceConfig{}, fmt.Errorf("unknown implementation %q for producer %q", implementation, namespace)
		}
		fileMode := producerConfig.Files
		if fileMode == "" {
			fileMode = string(typescript.FileScopeTsconfig)
		}
		if filesSet {
			fileMode = filesOverride
		}
		scope, err := typescript.ParseFileScope(fileMode)
		if err != nil {
			return nil, workspace.WorkspaceConfig{}, err
		}
		sourceLinks := producerConfig.SourceLinks
		if sourceLinksSet {
			sourceLinks = sourceLinksOverride
		}
		sources := append(slices.Clone(config.Sources), invocationSources...)
		settings = append(settings, typescript.Options{
			Namespace: namespace, WorkspaceExcludes: config.Exclude,
			Includes: config.Include, Sources: sources,
			Excludes: invocationExcludes, Files: scope, SourceLinks: sourceLinks,
		})
	}
	return settings, config, nil
}

func runScope(ctx context.Context, args []string, stdout io.Writer) error {
	flags := flag.NewFlagSet("lintai scope", flag.ContinueOnError)
	flags.SetOutput(os.Stderr)
	workspaceRoot := flags.String("workspace-root", ".", "workspace root to inspect")
	configPath := flags.String("config", "", "lintai.toml file (relative to the invocation directory)")
	explain := flags.Bool("explain", false, "explain the production source and compiler scope")
	jsonOutput := flags.Bool("json", false, "print the scope explanation as JSON")
	var excludes stringList
	flags.Var(&excludes, "exclude", "exclude a workspace pattern at invocation priority (repeatable)")
	var sources stringList
	flags.Var(&sources, "source", "select an exact workspace source file (repeatable)")
	files := flags.String("files", "", "files in scope: tsconfig or walk")
	sourceLinks := flags.String("source-links", "", "JSON source-entry associations, relative to --workspace-root")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if !*explain {
		return fmt.Errorf("lintai scope requires --explain")
	}
	root, err := filepath.Abs(*workspaceRoot)
	if err != nil {
		return err
	}
	settings, err := loadTypeScriptSettings(root, *configPath, *files, flagProvided(flags, "files"), *sourceLinks, flagProvided(flags, "source-links"), excludes, sources)
	if err != nil {
		return err
	}
	type producerScope struct {
		Producer string                      `json:"producer"`
		Files    string                      `json:"files"`
		Scope    typescript.ScopeExplanation `json:"scope"`
	}
	reports := make([]producerScope, 0, len(settings))
	for _, item := range settings {
		report, err := typescript.ExplainScope(ctx, root, item)
		if err != nil {
			return err
		}
		reports = append(reports, producerScope{Producer: item.Namespace, Files: string(item.Files), Scope: report})
	}
	if *jsonOutput {
		encoder := json.NewEncoder(stdout)
		encoder.SetIndent("", "  ")
		return encoder.Encode(reports)
	}
	for index, report := range reports {
		if index > 0 {
			fmt.Fprintln(stdout)
		}
		fmt.Fprintf(stdout, "producer: %s\nfiles: %s\n", report.Producer, report.Files)
		fmt.Fprintf(stdout, "exclude: %v\ninclude: %v\n", report.Scope.WorkspaceExclude, report.Scope.WorkspaceInclude)
		fmt.Fprintf(stdout, "exact sources: %v\n", report.Scope.ExactSources)
		fmt.Fprintf(stdout, "selected files: %d\n", len(report.Scope.Selected))
		for _, item := range report.Scope.Selected {
			fmt.Fprintf(stdout, "  %s", item.Path)
			if item.Exact {
				fmt.Fprint(stdout, " [exact]")
			}
			if len(item.IncludePatterns) > 0 {
				fmt.Fprintf(stdout, " [include: %s]", strings.Join(item.IncludePatterns, ", "))
			}
			if len(item.ExcludePatterns) > 0 {
				fmt.Fprintf(stdout, " [excluded then included: %s]", strings.Join(item.ExcludePatterns, ", "))
			}
			fmt.Fprintln(stdout)
		}
		fmt.Fprintf(stdout, "compiler eligible: %v\ncompiler excluded: %v\nconfigurations: %v\npruned boundaries: %v\n",
			report.Scope.CompilerEligible, report.Scope.CompilerExcluded, report.Scope.Configurations, report.Scope.PrunedBoundaries)
		for _, file := range report.Scope.CompilerFiles {
			selected, owner, kind := "none", "none", "none"
			if file.SelectedConfig != nil {
				selected = *file.SelectedConfig
			}
			if file.OwnerConfig != nil {
				owner = *file.OwnerConfig
			}
			if file.OwnerKind != nil {
				kind = *file.OwnerKind
			}
			fmt.Fprintf(stdout, "  %s: eligible=%t selected=%s owner=%s (%s) roots=%v\n", file.Path, file.Eligible, selected, owner, kind, file.RootConfigs)
		}
	}
	return nil
}

type projectFlags struct {
	flags                                          *flag.FlagSet
	root, config, files, sourceLinks, suppressions *string
	factLimit                                      *int
	excludes, sources                              stringList
}

func newProjectFlags(flags *flag.FlagSet) *projectFlags {
	p := &projectFlags{flags: flags}
	p.root = flags.String("workspace-root", ".", "workspace to analyze")
	p.config = flags.String("config", "", "lintai.toml file relative to invocation directory")
	p.files = flags.String("files", "", "source selection mode: tsconfig or walk")
	p.sourceLinks = flags.String("source-links", "", "source-entry associations relative to workspace")
	p.suppressions = flags.String("suppressions-location", "", "suppressions file relative to workspace")
	p.factLimit = flags.Int("fact-limit", engine.DefaultFactLimit, "maximum derived facts per rule")
	flags.Var(&p.excludes, "exclude", "exclude a workspace pattern (repeatable)")
	flags.Var(&p.sources, "source", "select an exact workspace source file (repeatable)")
	return p
}

// open binds every parsed setting into the session configuration. The caller
// supplies execution settings such as workers, explanations and lock selection.
func (p *projectFlags) open(options engine.Config) (*engine.Session, engine.Config, error) {
	if *p.factLimit <= 0 {
		return nil, engine.Config{}, fmt.Errorf("--fact-limit must be positive")
	}
	root, err := filepath.Abs(*p.root)
	if err != nil {
		return nil, engine.Config{}, err
	}
	settings, config, err := captureTypeScriptSettings(root, *p.config, *p.files, flagProvided(p.flags, "files"), *p.sourceLinks, flagProvided(p.flags, "source-links"), p.excludes, p.sources)
	if err != nil {
		return nil, engine.Config{}, err
	}
	options.WorkspaceRoot, options.Rule, options.FactLimit, options.SuppressionsLocation = root, config.Rule, *p.factLimit, *p.suppressions
	producers := make([]backend.Producer, 0, len(settings))
	for _, setting := range settings {
		producers = append(producers, newBackend(setting))
	}
	session, err := openSession(options, producers...)
	return session, options, err
}

func runOnce(ctx context.Context, args []string, stdout io.Writer) error {
	flags := flag.NewFlagSet("lintai", flag.ContinueOnError)
	project := newProjectFlags(flags)
	jsonOutput := flags.Bool("json", false, "emit diagnostics as JSON")
	explain := flags.Bool("explain", false, "attach the proof of each violation to JSON diagnostics (requires --json)")
	ruleWorkers := flags.Int("rule-workers", 0, "maximum concurrent rule jobs (0 uses GOMAXPROCS)")
	rulesLock := flags.String("rules-lock", "", "rules lock file to enforce (see lintai lock); a changed, new or removed rule is a rule error with phase integrity")
	suppressAll := flags.Bool("suppress-all", false, "record every error-severity violation in the suppressions file")
	var suppressRules stringList
	flags.Var(&suppressRules, "suppress-rule", "record the error-severity violations of this rule in the suppressions file (repeatable)")
	pruneSuppressions := flags.Bool("prune-suppressions", false, "lower or remove the suppressions that allow more violations than the run found")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if flags.NArg() != 0 {
		return fmt.Errorf("unexpected arguments: %v", flags.Args())
	}
	if *explain && !*jsonOutput {
		return fmt.Errorf("--explain requires --json")
	}
	runner, _, err := project.open(engine.Config{RuleWorkers: *ruleWorkers, Explain: *explain, RulesLock: *rulesLock})
	if err != nil {
		return err
	}
	defer runner.Close()
	update := engine.SuppressionUpdate{All: *suppressAll, Rules: suppressRules, Prune: *pruneSuppressions}
	var result engine.Result
	if update.All || len(update.Rules) > 0 || update.Prune {
		result, err = runner.UpdateSuppressions(ctx, update)
	} else {
		result, err = runner.Check(ctx)
	}
	if err != nil {
		return err
	}
	for _, warning := range result.Warnings {
		fmt.Fprintln(os.Stderr, warning)
	}
	if result.SuppressionsWritten {
		fmt.Fprintf(os.Stderr, "lintai: wrote %s\n", result.SuppressionsPath)
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
			fmt.Fprintf(stdout, "%s%s [%s/%s] %s\n", location, item.Severity, item.RuleID, item.AssertionID, item.Message)
		}
	}
	if err := diagnosticsOutcome(result.Diagnostics); err != nil {
		return err
	}
	return suppressionsOutcome(result)
}

// diagnosticsOutcome returns a rule error when a rule could not run, and a
// violation error when a rule reported an error-severity violation. Warnings
// do not change the exit code.
func diagnosticsOutcome(items []diagnostics.Diagnostic) error {
	ruleErrors, violations := 0, 0
	for _, item := range items {
		switch {
		case item.DiagnosticKind == diagnostics.KindRuleExecutionError:
			ruleErrors++
		case item.DiagnosticKind == diagnostics.KindUnusedSuppression:
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

// suppressionsOutcome returns a policy failure (exit 1) when the
// suppressions file allows more violations than the run found.
// diagnosticsOutcome runs first so execution failures take priority.
func suppressionsOutcome(result engine.Result) error {
	if len(result.UnusedSuppressions) == 0 {
		return nil
	}
	lines := make([]string, 0, len(result.UnusedSuppressions)+1)
	lines = append(lines, fmt.Sprintf("lintai: %d unused suppressions in %s. The run found fewer violations than these entries allow:", len(result.UnusedSuppressions), result.SuppressionsPath))
	for _, unused := range result.UnusedSuppressions {
		lines = append(lines, fmt.Sprintf("  %s %s/%s (%s): allows %d, found %d", unused.File, unused.RuleID, unused.Assertion.Name, unused.Description, unused.Allowed, unused.Found))
	}
	lines = append(lines, "Run lintai --prune-suppressions to remove them.")
	return &exitError{code: exitViolations, message: strings.Join(lines, "\n")}
}

func runLock(ctx context.Context, args []string, stdout io.Writer) error {
	flags := flag.NewFlagSet("lintai lock", flag.ContinueOnError)
	project := newProjectFlags(flags)
	path := flags.String("lock-file", engine.DefaultRulesLockFile, "approval file relative to the workspace root")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if flags.NArg() != 0 {
		return fmt.Errorf("unexpected arguments: %v", flags.Args())
	}
	session, options, err := project.open(engine.Config{})
	if err != nil {
		return err
	}
	defer session.Close()
	lock, err := session.BuildRulesLock(ctx, *path)
	if err != nil {
		return err
	}
	if _, err := fmt.Fprintf(stdout, "pack %s\npolicy %s\n", lock.PackDigest, lock.PolicyDigest); err != nil {
		return err
	}
	return engine.WriteRulesLock(ctx, engine.ResolveRulesLockPath(options.WorkspaceRoot, *path), lock)
}

// newBackend returns the TypeScript backend that reports adjusted tsconfigs
// on stderr.
func newBackend(settings ...typescript.Options) *typescript.Backend {
	producer := typescript.New(settings...)
	producer.Log = os.Stderr
	return producer
}

// runRules prints the metadata of the published pack.
func runRules(ctx context.Context, args []string, stdout io.Writer) error {
	flags := flag.NewFlagSet("lintai rules", flag.ContinueOnError)
	root := flags.String("workspace-root", ".", "workspace containing the built pack")
	jsonOutput := flags.Bool("json", false, "print rules and errors as JSON")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if flags.NArg() != 0 {
		return fmt.Errorf("unexpected arguments: %v", flags.Args())
	}
	list, err := engine.ListRules(ctx, *root)
	if err != nil {
		return err
	}
	if *jsonOutput {
		encoder := json.NewEncoder(stdout)
		encoder.SetIndent("", "  ")
		return encoder.Encode(list)
	}
	for _, item := range list.Rules {
		fmt.Fprintf(stdout, "%s %s\n", item.ID, item.Path)
	}
	for _, item := range list.Errors {
		fmt.Fprintf(os.Stderr, "%s: %s\n", item.Path, item.Message)
	}
	return nil
}
