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
	"github.com/bigbizze/lintai/bundle"
	"github.com/bigbizze/lintai/diagnostics"
	"github.com/bigbizze/lintai/engine"
	"github.com/bigbizze/lintai/internal/workspace"
	"github.com/bigbizze/lintai/observe"
	"github.com/bigbizze/lintai/server"
	"github.com/bigbizze/lintai/suppressions"
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
		case "check":
			return runOnce(ctx, args[1:], stdout)
		case "serve":
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

func runSchema(ctx context.Context, args []string, stdout io.Writer) error {
	flags := flag.NewFlagSet("lintai schema", flag.ContinueOnError)
	flags.SetOutput(os.Stderr)
	workspaceRoot := flags.String("workspace-root", ".", "workspace root whose configured producer catalog to print")
	configPath := flags.String("config", "", "lintai.toml file (relative to the invocation directory)")
	flags.String("asset-root", "", "ignored; the npm launcher passes it to every command")
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
	session := engine.Open(engine.Options{RepoRoot: absoluteWorkspace, WorkspaceRoot: absoluteWorkspace}, producers...)
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

// parseRuleSeverity parses --severity values of the form rule-id=level.
func parseRuleSeverity(values []string) (map[string]diagnostics.Severity, error) {
	result := make(map[string]diagnostics.Severity, len(values))
	for _, value := range values {
		ruleID, level, ok := strings.Cut(value, "=")
		ruleID = strings.TrimSpace(ruleID)
		if !ok || ruleID == "" {
			return nil, fmt.Errorf("invalid --severity value %q: use <rule-id>=error|warning|off", value)
		}
		severity, err := diagnostics.ParseSeverity(strings.TrimSpace(level))
		if err != nil {
			return nil, fmt.Errorf("invalid --severity value %q: %w", value, err)
		}
		result[ruleID] = severity
	}
	return result, nil
}

func runFacts(ctx context.Context, args []string, stdout io.Writer) error {
	flags := flag.NewFlagSet("lintai facts", flag.ContinueOnError)
	flags.SetOutput(os.Stderr)
	workspaceRoot := flags.String("workspace-root", ".", "workspace root to analyze")
	flags.String("asset-root", "", "ignored; the npm launcher passes it to every command")
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
	session := engine.Open(engine.Options{RepoRoot: absoluteWorkspace, WorkspaceRoot: absoluteWorkspace}, producers...)
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

func (s *serveSession) configure(ctx context.Context, force bool) (err error) {
	defer func() {
		if err != nil {
			_ = s.Close()
		}
	}()
	if err = ctx.Err(); err != nil {
		return err
	}
	if !force && s.receipt && s.session != nil && len(s.config.Observations.Records()) != 0 {
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
	settings, config, err = captureTypeScriptSettings(s.params.WorkspaceRoot, "", s.params.Files, s.params.Files != "", s.params.SourceLinks, s.params.SourceLinks != "", s.params.Excludes, nil)
	if err != nil {
		return fmt.Errorf("load workspace configuration: %w", err)
	}
	if err = ctx.Err(); err != nil {
		return err
	}
	if s.session == nil || !reflect.DeepEqual(settings, s.settings) {
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
		s.session = engine.Open(engine.Options{}, producers...)
		s.settings = settings
	}
	s.config, s.receipt = config, true
	return nil
}

func (s *serveSession) Check(ctx context.Context, requests ...engine.Options) (engine.Result, error) {
	if err := s.configure(ctx, true); err != nil {
		return engine.Result{}, err
	}
	if err := ctx.Err(); err != nil {
		_ = s.Close()
		return engine.Result{}, err
	}
	return s.session.Check(ctx, requests...)
}

func (s *serveSession) RefreshFiles(ctx context.Context, options engine.Options, files []string) (engine.FilesResult, error) {
	if err := s.configure(ctx, false); err != nil {
		return engine.FilesResult{}, err
	}
	if err := ctx.Err(); err != nil {
		_ = s.Close()
		return engine.FilesResult{}, err
	}
	return s.session.RefreshFiles(ctx, options, files)
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
			Namespace: namespace, WorkspaceExcludes: slices.Clone(config.Exclude),
			Includes: slices.Clone(config.Include), Sources: sources,
			Excludes: slices.Clone(invocationExcludes), Files: scope, SourceLinks: sourceLinks,
		})
	}
	return settings, config, nil
}

func runScope(ctx context.Context, args []string, stdout io.Writer) error {
	flags := flag.NewFlagSet("lintai scope", flag.ContinueOnError)
	flags.SetOutput(os.Stderr)
	workspaceRoot := flags.String("workspace-root", ".", "workspace root to inspect")
	configPath := flags.String("config", "", "lintai.toml file (relative to the invocation directory)")
	flags.String("asset-root", "", "ignored; the npm launcher passes it to every command")
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

func runOnce(ctx context.Context, args []string, stdout io.Writer) error {
	flags := flag.NewFlagSet("lintai", flag.ContinueOnError)
	flags.SetOutput(os.Stderr)

	workspaceRoot := flags.String("workspace-root", ".", "workspace root to analyze")
	repoRoot := flags.String("repo-root", ".", "repository root containing scripts and packages")
	assetRoot := flags.String("asset-root", "", "asset root containing lintai helper scripts")
	configPath := flags.String("config", "", "lintai.toml file (relative to the invocation directory)")
	rules := flags.String("rules", "lintai-rules/**/*.ts", "comma-separated list of rule globs, relative to --workspace-root")
	envJSON := flags.String("env-json", "{}", "JSON object passed to every rule as env")
	jsonOutput := flags.Bool("json", false, "emit diagnostics as JSON")
	explain := flags.Bool("explain", false, "attach the proof of each violation to JSON diagnostics (requires --json)")
	factLimit := flags.Int("fact-limit", engine.DefaultFactLimit, "maximum number of facts a rule may derive")
	ruleWorkers := flags.Int("rule-workers", 0, "maximum concurrent rule jobs (0 uses GOMAXPROCS)")
	var excludes stringList
	flags.Var(&excludes, "exclude", "glob of workspace files to leave out of the snapshot (repeatable)")
	var sources stringList
	flags.Var(&sources, "source", "exact workspace source file to select (repeatable)")
	files := flags.String("files", "", "files in the snapshot: tsconfig (files that tsc compiles) or walk (every walked file)")
	sourceLinks := flags.String("source-links", "", "JSON source-entry associations, relative to --workspace-root")
	var severities stringList
	flags.Var(&severities, "severity", "rule severity as <rule-id>=error|warning|off (repeatable; default: error)")
	rulesLock := flags.String("rules-lock", "", "rules lock file to enforce (see lintai lock); a changed, new or removed rule is a rule error with phase integrity")
	setupSandbox := flags.String("setup-sandbox", string(bundle.SetupSandboxOn), "run setup() under the Node permission model: on or off (default on)")
	suppressAll := flags.Bool("suppress-all", false, "record every error-severity violation in the suppressions file")
	var suppressRules stringList
	flags.Var(&suppressRules, "suppress-rule", "record the error-severity violations of this rule in the suppressions file (repeatable)")
	pruneSuppressions := flags.Bool("prune-suppressions", false, "lower or remove the suppressions that allow more violations than the run found")
	suppressionsLocation := flags.String("suppressions-location", "", "suppressions file, relative to --workspace-root (default "+suppressions.DefaultFile+")")
	if err := flags.Parse(args); err != nil {
		return err
	}
	ruleSeverity, err := parseRuleSeverity(severities)
	if err != nil {
		return err
	}
	sandbox, err := bundle.ParseSetupSandbox(*setupSandbox)
	if err != nil {
		return err
	}
	if *explain && !*jsonOutput {
		return fmt.Errorf("--explain requires --json")
	}
	if *factLimit <= 0 {
		return fmt.Errorf("--fact-limit must be positive")
	}
	if *ruleWorkers < 0 {
		return fmt.Errorf("--rule-workers must be zero or a positive integer")
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
	producerSettings, err := loadTypeScriptSettings(absoluteWorkspace, *configPath, *files, flagProvided(flags, "files"), *sourceLinks, flagProvided(flags, "source-links"), excludes, sources)
	if err != nil {
		return err
	}
	producers := make([]backend.Producer, 0, len(producerSettings))
	for _, settings := range producerSettings {
		producers = append(producers, newBackend(settings))
	}
	runner := engine.Open(engine.Options{}, producers...)
	defer runner.Close()
	result, err := runner.Check(ctx, engine.Options{
		RepoRoot:      absoluteRepo,
		AssetRoot:     absoluteAssetRoot,
		WorkspaceRoot: absoluteWorkspace,
		RuleGlobs:     ruleGlobs,
		Env:           env,
		Severity:      diagnostics.SeverityError,
		RuleSeverity:  ruleSeverity,
		FactLimit:     *factLimit,
		RuleWorkers:   *ruleWorkers,
		Explain:       *explain,
		RulesLock:     *rulesLock,
		SetupSandbox:  sandbox,

		SuppressionsLocation: *suppressionsLocation,
		SuppressAll:          *suppressAll,
		SuppressRules:        suppressRules,
		PruneSuppressions:    *pruneSuppressions,
	})
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

func resolveAssetRoot(explicit, repoRoot string) (string, error) {
	if explicit != "" {
		return filepath.Abs(explicit)
	}
	candidates := []string{
		filepath.Join(repoRoot, "node_modules", "@lintai", "client"),
		// pnpm does not link transitive dependencies into the root
		// node_modules. It links them here.
		filepath.Join(repoRoot, "node_modules", ".pnpm", "node_modules", "@lintai", "client"),
		filepath.Join(repoRoot, "packages", "client"),
		repoRoot,
	}
	for _, candidate := range candidates {
		if hasHelperScripts(candidate) {
			return candidate, nil
		}
	}
	return "", fmt.Errorf("could not find lintai asset root under %s", strings.Join(candidates[:len(candidates)-1], ", "))
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

// runLock bundles the rules and writes the rules lock.
func runLock(ctx context.Context, args []string, stdout io.Writer) error {
	flags := flag.NewFlagSet("lintai lock", flag.ContinueOnError)
	flags.SetOutput(os.Stderr)
	workspaceRoot := flags.String("workspace-root", ".", "workspace root that contains the rules")
	repoRoot := flags.String("repo-root", ".", "repository root containing scripts and packages")
	assetRoot := flags.String("asset-root", "", "asset root containing lintai helper scripts")
	rules := flags.String("rules", "lintai-rules/**/*.ts", "comma-separated list of rule globs, relative to --workspace-root")
	lockFile := flags.String("lock-file", engine.DefaultRulesLockFile, "rules lock file to write, relative to --workspace-root")
	suppressionsLocation := flags.String("suppressions-location", "", "suppressions file to lock, relative to --workspace-root (default "+suppressions.DefaultFile+")")
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
	lock, locked, err := engine.BuildRulesLock(ctx, engine.Options{
		RepoRoot:             absoluteRepo,
		AssetRoot:            absoluteAssetRoot,
		WorkspaceRoot:        absoluteWorkspace,
		RuleGlobs:            strings.Split(*rules, ","),
		SuppressionsLocation: *suppressionsLocation,
		RulesLock:            *lockFile,
	})
	if err != nil {
		return err
	}
	path := engine.ResolveRulesLockPath(absoluteWorkspace, *lockFile)
	if err := engine.WriteRulesLock(ctx, path, lock); err != nil {
		return err
	}
	for _, item := range locked {
		fmt.Fprintf(stdout, "%s %s\n", item.Hash, item.Path)
	}
	if entries := lock.Suppressions.Entries(); entries > 0 {
		fmt.Fprintf(stdout, "%d suppressions entries\n", entries)
	}
	return nil
}

// newBackend returns the TypeScript backend that reports adjusted tsconfigs
// on stderr.
func newBackend(settings ...typescript.Options) *typescript.Backend {
	producer := typescript.New(settings...)
	producer.Log = os.Stderr
	return producer
}

// runRules prints the id and path of each rule. A rule that does not bundle
// or load is in the errors, and the exit code is still 0, so that a linter
// plugin can create the other rules.
func runRules(ctx context.Context, args []string, stdout io.Writer) error {
	flags := flag.NewFlagSet("lintai rules", flag.ContinueOnError)
	flags.SetOutput(os.Stderr)
	workspaceRoot := flags.String("workspace-root", ".", "workspace root that contains the rules")
	repoRoot := flags.String("repo-root", ".", "repository root containing scripts and packages")
	assetRoot := flags.String("asset-root", "", "asset root containing lintai helper scripts")
	rules := flags.String("rules", "lintai-rules/**/*.ts", "comma-separated list of rule globs, relative to --workspace-root")
	jsonOutput := flags.Bool("json", false, "print the rules and the errors as JSON")
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
	list, err := engine.ListRules(ctx, engine.Options{
		RepoRoot:      absoluteRepo,
		AssetRoot:     absoluteAssetRoot,
		WorkspaceRoot: absoluteWorkspace,
		RuleGlobs:     strings.Split(*rules, ","),
	})
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
