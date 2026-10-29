package lintaiapi

import (
	"context"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/bigbizze/lintai/internal/tsgo/internal/ast"
	"github.com/bigbizze/lintai/internal/tsgo/internal/bundled"
	"github.com/bigbizze/lintai/internal/tsgo/internal/checker"
	"github.com/bigbizze/lintai/internal/tsgo/internal/compiler"
	"github.com/bigbizze/lintai/internal/tsgo/internal/core"
	"github.com/bigbizze/lintai/internal/tsgo/internal/module"
	"github.com/bigbizze/lintai/internal/tsgo/internal/scanner"
	"github.com/bigbizze/lintai/internal/tsgo/internal/tsoptions"
	"github.com/bigbizze/lintai/internal/tsgo/internal/tspath"
	"github.com/bigbizze/lintai/internal/tsgo/internal/vfs/osvfs"
)

const (
	ImportFormStatic       = "static"
	ImportFormReexport     = "reexport"
	ImportFormDynamic      = "dynamic"
	ImportFormRequire      = "require"
	ImportFormImportEquals = "import_equals"
	ImportFormImportType   = "import_type"
)

type BuildSnapshotRequest struct {
	WorkspaceRoot   string
	Files           []string
	AmbientMatchers []AmbientMatcher
	// TsconfigFiles limits the modules to the files that tsc compiles. A
	// file under a tsconfig.json is a module only when the program of that
	// tsconfig contains it, as a root file or as an imported file. When it
	// is false, every file in Files is a module, and a file that its
	// tsconfig does not contain is analyzed with a synthetic tsconfig.
	// Files with no tsconfig.json are always analyzed with the synthetic
	// tsconfig.
	TsconfigFiles bool
}

type SourceLocation struct {
	File        string
	StartLine   int
	StartColumn int
	EndLine     int
	EndColumn   int
}

type Module struct {
	EntityID    string
	SemanticKey string
	Path        string
	Range       SourceLocation
}

type Function struct {
	EntityID       string
	SemanticKey    string
	Name           string
	Kind           string
	FilePath       string
	ContainerName  string
	ContainsAwait  bool
	IsExported     bool
	IsAsync        bool
	ParameterCount int
	// ReturnType and ParameterTypes are type entity IDs. Snapshot.Types
	// holds the facts of each type.
	ReturnType     string
	ParameterTypes []string
	Range          SourceLocation
	BodyStart      int
	BodyEnd        int
}

type ImportedSymbol struct {
	Name       string
	Kind       string
	IsTypeOnly bool
}

type ImportEdge struct {
	EntityID           string
	SemanticKey        string
	Specifier          string
	FromPath           string
	ToPath             string
	Form               string
	Internal           bool
	Unresolved         bool
	Package            string
	ImportedSymbols    []ImportedSymbol
	HasDefaultImport   bool
	HasNamespaceImport bool
	HasNamedImports    bool
	IsTypeOnly         bool
	// Retained is set on an import or re-export declaration that is not
	// type-only and whose bindings are all type-only, as in
	// import { type X } from "m" or export { type X } from "m", when
	// verbatimModuleSyntax is on. tsc then emits import {} from "m" or
	// export {} from "m", so the module still loads at runtime.
	Retained bool
	Range    SourceLocation
}

// CallEdge is a call from a collected function, or from module scope when
// FromModule is set, to a collected function. For a module-scope call,
// FromSemanticKey is the workspace-relative path of the module.
type CallEdge struct {
	EntityID        string
	SemanticKey     string
	FromSemanticKey string
	FromModule      bool
	ToSemanticKey   string
	FromName        string
	ToName          string
	FromPath        string
	ToPath          string
	Range           SourceLocation
}

// ExternalCall is a call whose resolved declaration is in a declaration file
// or outside the workspace: the default library, a package or a .d.ts file.
type ExternalCall struct {
	EntityID        string
	SemanticKey     string
	FromSemanticKey string
	FromModule      bool
	FromPath        string
	Module          string
	Name            string
	Range           SourceLocation
}

// CallUnresolved is a call or new expression whose target the checker does
// not resolve to a collected function or to an external declaration, for
// example a call through a callback parameter or an any-typed value.
type CallUnresolved struct {
	EntityID        string
	SemanticKey     string
	FromSemanticKey string
	FromModule      bool
	FromPath        string
	Range           SourceLocation
}

// ImportOpaque is an import() or require() whose specifier is not a string
// literal. It has no ImportEdge.
type ImportOpaque struct {
	EntityID    string
	SemanticKey string
	FromPath    string
	Range       SourceLocation
}

// FnRef is a reference to a collected function as a value, outside the
// callee position of a call.
type FnRef struct {
	EntityID        string
	SemanticKey     string
	FromSemanticKey string
	FromModule      bool
	FromPath        string
	ToSemanticKey   string
	ToPath          string
	Range           SourceLocation
}

type ModuleDirective struct {
	EntityID    string
	SemanticKey string
	FilePath    string
	Text        string
	Range       SourceLocation
}

type ParseError struct {
	EntityID    string
	SemanticKey string
	FilePath    string
	Message     string
	Range       SourceLocation
}

type TypeRef struct {
	EntityID    string
	SemanticKey string
	Name        string
	FilePath    string
	TargetPath  string
	Range       SourceLocation
}

type PendingDtsCall struct {
	DtsPath         string
	TargetName      string
	FromSemanticKey string
	FromModule      bool
	FromName        string
	FromPath        string
	Range           SourceLocation
	// Fallback is the external call that the call becomes when no source
	// function matches the declaration.
	Fallback ExternalCall
}

type Snapshot struct {
	Modules         []Module
	Functions       []Function
	ImportEdges     []ImportEdge
	CallEdges       []CallEdge
	ExternalCalls   []ExternalCall
	CallsUnresolved []CallUnresolved
	ImportsOpaque   []ImportOpaque
	FnRefs          []FnRef
	TypeRefs        []TypeRef
	AmbientUses     []AmbientUse
	GlobalRefs      []GlobalRef
	Directives      []ModuleDirective
	ParseErrors     []ParseError
	PendingDtsCalls []PendingDtsCall
	// Types are the type entities that function parameter and return types
	// refer to, sorted by ID.
	Types []TypeFact
	// ConfigFiles are the absolute paths of the tsconfig files that the
	// snapshot read, including extended and referenced configs.
	ConfigFiles []string
	// ConfigAdjustments are the compiler options that lintai changed because
	// TypeScript 7 rejects them, sorted by config path and option.
	ConfigAdjustments []ConfigAdjustment
}

type configGroup struct {
	ConfigPath   string
	Config       *tsoptions.ParsedCommandLine
	PrimaryFiles []string
}

type extractor struct {
	workspaceRoot   string
	program         *compiler.Program
	resolver        module.Resolver
	checker         *checker.Checker
	requestedFiles  map[string]struct{}
	primaryFiles    map[string]struct{}
	filesByPath     map[string]*ast.SourceFile
	functionNodes   map[*ast.Node]*Function
	functionKeys    map[string]*Function
	functionSyms    map[*ast.Symbol]*Function
	functionCount   map[string]int
	importCount     map[string]int
	callCount       map[string]int
	externalCount   map[string]int
	unresolvedCount map[string]int
	opaqueCount     map[string]int
	refCount        map[string]int
	typeRefCount    map[string]int
	ambientCount    map[string]int
	globalRefCount  map[string]int
	functions       []Function
	imports         []ImportEdge
	calls           []CallEdge
	externalCalls   []ExternalCall
	unresolved      []CallUnresolved
	opaque          []ImportOpaque
	refs            []FnRef
	typeRefs        []TypeRef
	ambientUses     []AmbientUse
	globalRefs      []GlobalRef
	pendingDtsCalls []PendingDtsCall
	ambient         *ambientIndex
	ambientFile     *ambientFileState
	ambientMembers  map[*ast.Symbol][]*AmbientMatcher
	ambientDone     map[*ast.Node]struct{}
	globalSyms      map[string]*ast.Symbol
	typeFacts       map[string]*TypeFact
	typeIDs         map[*checker.Type]string
	typeKeys        map[*checker.Type]cachedTypeKey
	typeShapes      map[*checker.Type]*typeShape
	typeTruncated   map[*checker.Type]struct{}
	keyTruncated    bool
	timing          *extractTiming
}

type functionMeta struct {
	localName    string
	kind         string
	nameNode     *ast.Node
	namingSource *ast.Node
	lookupNodes  []*ast.Node
	exported     bool
}

type functionState struct {
	record *Function
}

// callSource is the caller of a call: a collected function, or module scope.
type callSource struct {
	key    string
	name   string
	path   string
	module bool
}

// extractTiming records where extraction spends its time. BuildSnapshot
// prints it to stderr when LINTAI_EXTRACT_TIMING=1.
type extractTiming struct {
	programs int
	config   time.Duration
	program  time.Duration
	walk     time.Duration
	// types is the part of walk that builds type facts.
	types time.Duration
}

func BuildSnapshot(ctx context.Context, req BuildSnapshotRequest) (*Snapshot, error) {
	started := time.Now()
	timing := &extractTiming{}
	workspaceRoot, err := normalizeAbsPath(req.WorkspaceRoot)
	if err != nil {
		return nil, err
	}
	files, err := normalizeFiles(req.Files)
	if err != nil {
		return nil, err
	}
	if len(files) == 0 {
		return &Snapshot{}, nil
	}

	host := compiler.NewCompilerHost(
		bundled.WrapFS(osvfs.FS()),
		bundled.LibPath(),
		nil,
		nil,
		nil,
	)
	configs := newConfigCache(host)
	configStarted := time.Now()
	groups, err := discoverConfigGroups(workspaceRoot, files, configs)
	if err != nil {
		return nil, err
	}
	timing.config = time.Since(configStarted)
	merged := &Snapshot{}
	missing := make(map[string]struct{})
	for _, group := range groups {
		partial, groupMissing, err := buildSnapshotForGroup(ctx, host, workspaceRoot, files, group, req.AmbientMatchers, timing)
		if err != nil {
			return nil, err
		}
		mergeSnapshot(merged, partial)
		if req.TsconfigFiles && group.Config != nil {
			continue
		}
		for _, file := range groupMissing {
			missing[file] = struct{}{}
		}
	}
	if len(missing) > 0 {
		fallbackFiles := make([]string, 0, len(missing))
		for file := range missing {
			fallbackFiles = append(fallbackFiles, file)
		}
		slices.Sort(fallbackFiles)
		partial, _, err := buildSnapshotForGroup(ctx, host, workspaceRoot, files, configGroup{PrimaryFiles: fallbackFiles}, req.AmbientMatchers, timing)
		if err != nil {
			return nil, err
		}
		mergeSnapshot(merged, partial)
	}
	resolveDtsCalls(merged)
	if req.TsconfigFiles {
		keepModuleFiles(merged)
		keepReachableTypes(merged)
	}
	merged.ConfigFiles = configs.files()
	merged.ConfigAdjustments = configs.adjustments()
	sortSnapshot(merged)
	if os.Getenv("LINTAI_EXTRACT_TIMING") == "1" {
		fmt.Fprintf(os.Stderr, "lintai: extract files=%d programs=%d config_ms=%d program_ms=%d walk_ms=%d types_ms=%d type_entities=%d total_ms=%d\n",
			len(files), timing.programs, timing.config.Milliseconds(), timing.program.Milliseconds(), timing.walk.Milliseconds(), timing.types.Milliseconds(), len(merged.Types), time.Since(started).Milliseconds())
	}
	return merged, nil
}

// keepModuleFiles removes the functions in files that are not modules, and
// the call edges to them. A group can collect the functions of a file that
// it imports from another group, and that group can leave the file out.
// Import edges to such a file stay: the import is in the source.
func keepModuleFiles(snap *Snapshot) {
	modules := make(map[string]struct{}, len(snap.Modules))
	for _, item := range snap.Modules {
		modules[item.Path] = struct{}{}
	}
	isModule := func(path string) bool {
		_, ok := modules[path]
		return ok
	}
	snap.Functions = slices.DeleteFunc(snap.Functions, func(item Function) bool { return !isModule(item.FilePath) })
	snap.CallEdges = slices.DeleteFunc(snap.CallEdges, func(item CallEdge) bool { return !isModule(item.ToPath) })
}

// resolveDtsCalls resolves unresolved calls where the target declaration is
// in a .d.ts file but the source function exists in the snapshot under a
// different path (dist/ → src/ mapping). This happens in monorepos where
// packages consume each other's compiled output.
func resolveDtsCalls(snap *Snapshot) {
	// Build index of functions by (name, path)
	type fnKey struct{ name, path string }
	fnIndex := make(map[fnKey]int, len(snap.Functions))
	for i, fn := range snap.Functions {
		fnIndex[fnKey{fn.Name, fn.FilePath}] = i
	}

	for _, pending := range snap.PendingDtsCalls {
		srcPath := mapDtsToSource(pending.DtsPath)
		idx, ok := fnIndex[fnKey{pending.TargetName, srcPath}]
		if srcPath == "" || !ok {
			snap.ExternalCalls = append(snap.ExternalCalls, pending.Fallback)
			continue
		}
		target := &snap.Functions[idx]
		baseKey := fmt.Sprintf("%s::calls::%s", pending.FromSemanticKey, target.SemanticKey)
		snap.CallEdges = append(snap.CallEdges, CallEdge{
			EntityID:        "call:" + baseKey,
			SemanticKey:     baseKey,
			FromSemanticKey: pending.FromSemanticKey,
			FromModule:      pending.FromModule,
			ToSemanticKey:   target.SemanticKey,
			FromName:        pending.FromName,
			ToName:          target.Name,
			FromPath:        pending.FromPath,
			ToPath:          target.FilePath,
			Range:           pending.Range,
		})
	}
	snap.PendingDtsCalls = nil
}

func buildSnapshotForGroup(ctx context.Context, host compiler.CompilerHost, workspaceRoot string, requestedFiles []string, group configGroup, ambientMatchers []AmbientMatcher, timing *extractTiming) (*Snapshot, []string, error) {
	programStarted := time.Now()
	timing.programs++
	config := group.Config
	if config == nil {
		synthetic, err := syntheticConfig(host, workspaceRoot, group.PrimaryFiles)
		if err != nil {
			return nil, nil, err
		}
		config = synthetic
	}
	var resolver module.Resolver
	program := compiler.NewProgram(compiler.ProgramOptions{
		ProgramConfig: compiler.ProgramConfig{Config: config},
		ProgramHosts:  compiler.ProgramHosts{Host: host},
		ProgramFactories: compiler.ProgramFactories{
			CreateModuleResolver: func(options module.ResolverOptions) module.Resolver {
				resolver = module.NewResolver(options)
				return resolver
			},
		},
	})
	if diags := program.GetConfigFileParsingDiagnostics(); len(diags) > 0 {
		return nil, nil, fmt.Errorf("tsgo config diagnostics: %s", joinDiagnostics(diags))
	}
	program.BindSourceFiles()
	typeChecker, done := program.GetTypeChecker(ctx)
	defer done()
	timing.program += time.Since(programStarted)
	walkStarted := time.Now()
	defer func() { timing.walk += time.Since(walkStarted) }()

	ex := &extractor{
		workspaceRoot:   workspaceRoot,
		program:         program,
		resolver:        resolver,
		checker:         typeChecker,
		requestedFiles:  make(map[string]struct{}, len(requestedFiles)),
		primaryFiles:    make(map[string]struct{}, len(group.PrimaryFiles)),
		filesByPath:     make(map[string]*ast.SourceFile, len(requestedFiles)),
		functionNodes:   make(map[*ast.Node]*Function),
		functionKeys:    make(map[string]*Function),
		functionSyms:    make(map[*ast.Symbol]*Function),
		functionCount:   make(map[string]int),
		importCount:     make(map[string]int),
		callCount:       make(map[string]int),
		externalCount:   make(map[string]int),
		unresolvedCount: make(map[string]int),
		opaqueCount:     make(map[string]int),
		refCount:        make(map[string]int),
		typeRefCount:    make(map[string]int),
		ambientCount:    make(map[string]int),
		globalRefCount:  make(map[string]int),
		ambientDone:     make(map[*ast.Node]struct{}),
		globalSyms:      make(map[string]*ast.Symbol),
		ambient:         newAmbientIndex(ambientMatchers),
		typeFacts:       make(map[string]*TypeFact),
		typeIDs:         make(map[*checker.Type]string),
		typeKeys:        make(map[*checker.Type]cachedTypeKey),
		typeShapes:      make(map[*checker.Type]*typeShape),
		typeTruncated:   make(map[*checker.Type]struct{}),
		timing:          timing,
	}
	for _, file := range requestedFiles {
		ex.requestedFiles[file] = struct{}{}
		if sourceFile := program.GetSourceFile(tspath.RootedFilePathFromAbsolute(file)); sourceFile != nil {
			ex.filesByPath[file] = sourceFile
		}
	}
	for _, file := range group.PrimaryFiles {
		ex.primaryFiles[file] = struct{}{}
	}

	modules := make([]Module, 0, len(group.PrimaryFiles))
	directives := make([]ModuleDirective, 0)
	parseErrors := make([]ParseError, 0)
	missingPrimary := make([]string, 0)
	for _, file := range group.PrimaryFiles {
		sourceFile := ex.filesByPath[file]
		if sourceFile == nil {
			missingPrimary = append(missingPrimary, file)
			continue
		}
		relative := ex.relativePath(file)
		modules = append(modules, Module{
			EntityID:    "module:" + relative,
			SemanticKey: fmt.Sprintf("%s::module", relative),
			Path:        relative,
			Range:       ex.moduleRange(sourceFile),
		})
		directives = append(directives, ex.moduleDirectives(sourceFile, relative)...)
		if parseError, ok := ex.moduleParseError(sourceFile, relative); ok {
			parseErrors = append(parseErrors, parseError)
		}
	}

	for _, file := range requestedFiles {
		sourceFile := ex.filesByPath[file]
		if sourceFile == nil {
			continue
		}
		ex.walkFunctions(sourceFile.AsNode())
	}

	for _, file := range group.PrimaryFiles {
		sourceFile := ex.filesByPath[file]
		if sourceFile == nil {
			continue
		}
		ex.beginAmbientFile(sourceFile)
		ex.walkFacts(sourceFile.AsNode(), nil)
	}
	ex.ambientFile = nil

	partial := &Snapshot{
		Modules:         modules,
		Functions:       ex.functions,
		ImportEdges:     ex.imports,
		CallEdges:       ex.calls,
		ExternalCalls:   ex.externalCalls,
		CallsUnresolved: ex.unresolved,
		ImportsOpaque:   ex.opaque,
		FnRefs:          ex.refs,
		TypeRefs:        ex.typeRefs,
		AmbientUses:     ex.ambientUses,
		GlobalRefs:      ex.globalRefs,
		Directives:      directives,
		ParseErrors:     parseErrors,
		PendingDtsCalls: ex.pendingDtsCalls,
		Types:           ex.sortedTypeFacts(),
	}
	sortSnapshot(partial)
	return partial, missingPrimary, nil
}

// parsedConfig is a parsed tsconfig file, the set of its root files and the
// paths of its project references that exist.
type parsedConfig struct {
	// original is the tsconfig as the project wrote it. command is the
	// command line to build the program with: the original, or an adjusted
	// copy when TypeScript 7 rejects options of the original. command is nil
	// until program is called for the config.
	original    *tsoptions.ParsedCommandLine
	command     *tsoptions.ParsedCommandLine
	adjustments []ConfigAdjustment
	rootFiles   map[string]struct{}
	references  []string
}

type configCache struct {
	host    compiler.CompilerHost
	configs map[string]*parsedConfig
	errors  map[string]error
}

func newConfigCache(host compiler.CompilerHost) *configCache {
	return &configCache{host: host, configs: make(map[string]*parsedConfig), errors: make(map[string]error)}
}

func (c *configCache) load(configPath string) (*parsedConfig, error) {
	if parsed, ok := c.configs[configPath]; ok {
		return parsed, nil
	}
	if err, ok := c.errors[configPath]; ok {
		return nil, err
	}
	command, diags := tsoptions.GetParsedCommandLineOfConfigFile(tspath.RootedFilePathFromAbsolute(configPath), &core.CompilerOptions{}, nil, c.host.FS(), nil)
	if command != nil {
		diags = append(diags, command.GetConfigFileParsingDiagnostics()...)
	}
	if len(diags) > 0 {
		err := fmt.Errorf("tsconfig diagnostics: %s", joinDiagnostics(diags))
		c.errors[configPath] = err
		return nil, err
	}
	rootFiles := make(map[string]struct{}, len(command.FileNames()))
	for _, fileName := range command.FileNames() {
		rootFiles[normalizeSlash(fileName.AsString())] = struct{}{}
	}
	references := make([]string, 0, len(command.ProjectReferences()))
	for _, reference := range command.ResolvedProjectReferencePaths() {
		referencePath := normalizeSlash(reference.AsString())
		if c.host.FS().FileExists(reference) {
			references = append(references, referencePath)
		}
	}
	parsed := &parsedConfig{original: command, rootFiles: rootFiles, references: references}
	c.configs[configPath] = parsed
	return parsed, nil
}

// program returns the config that the program of a group uses: the loaded
// config, adjusted when TypeScript 7 rejects some of its options. Only
// program configs are adjusted, so a referenced config that cannot be
// adjusted does not stop the groups of other configs.
func (c *configCache) program(configPath string) (*tsoptions.ParsedCommandLine, error) {
	parsed, err := c.load(configPath)
	if err != nil {
		return nil, err
	}
	if parsed.command != nil {
		return parsed.command, nil
	}
	adjusted, adjustments, err := adjustForTypeScript7(c.host, configPath, parsed.original)
	if err != nil {
		return nil, err
	}
	parsed.command = adjusted
	parsed.adjustments = adjustments
	return adjusted, nil
}

// files returns the paths of every loaded config and of the configs that
// they extend or reference, sorted.
func (c *configCache) files() []string {
	result := make([]string, 0, len(c.configs))
	for configPath, parsed := range c.configs {
		result = append(result, configPath)
		for _, extended := range parsed.original.ExtendedSourceFiles() {
			result = append(result, normalizeSlash(extended.AsString()))
		}
		result = append(result, parsed.references...)
	}
	slices.Sort(result)
	return slices.Compact(result)
}

// adjustments returns the option changes of every loaded config, sorted by
// config path.
func (c *configCache) adjustments() []ConfigAdjustment {
	paths := slices.Sorted(maps.Keys(c.configs))
	var result []ConfigAdjustment
	for _, configPath := range paths {
		result = append(result, c.configs[configPath].adjustments...)
	}
	return result
}

// owningConfig returns the config that lists file as a root file: the
// nearest config, or else the first config in its project references, in
// depth-first order. A solution-style tsconfig.json, with "files": [] and
// references, lists no files itself. When no config lists the file, it
// returns the nearest config. A reference to a config that does not exist
// is skipped.
func (c *configCache) owningConfig(nearestPath string, file string) (string, error) {
	visited := make(map[string]struct{})
	var search func(configPath string) (string, error)
	search = func(configPath string) (string, error) {
		if _, seen := visited[configPath]; seen {
			return "", nil
		}
		visited[configPath] = struct{}{}
		parsed, err := c.load(configPath)
		if err != nil {
			return "", err
		}
		if _, ok := parsed.rootFiles[file]; ok {
			return configPath, nil
		}
		for _, reference := range parsed.references {
			owner, err := search(reference)
			if err != nil || owner != "" {
				return owner, err
			}
		}
		return "", nil
	}
	owner, err := search(nearestPath)
	if err != nil || owner != "" {
		return owner, err
	}
	return nearestPath, nil
}

func syntheticConfig(host compiler.CompilerHost, workspaceRoot string, files []string) (*tsoptions.ParsedCommandLine, error) {
	syntheticPath := filepath.Join(workspaceRoot, "tsconfig.lintai.synthetic.json")
	relativeFiles := make([]any, 0, len(files))
	for _, file := range files {
		relative, err := filepath.Rel(workspaceRoot, filepath.FromSlash(file))
		if err != nil {
			return nil, err
		}
		relativeFiles = append(relativeFiles, filepath.ToSlash(relative))
	}
	jsonConfig := map[string]any{
		"files": relativeFiles,
		"compilerOptions": map[string]any{
			"allowJs":          true,
			"checkJs":          false,
			"noEmit":           true,
			"jsx":              "preserve",
			"target":           "es2022",
			"module":           "preserve",
			"moduleResolution": "bundler",
		},
	}
	parsed := tsoptions.ParseJsonConfigFileContent(
		jsonConfig,
		host.FS(),
		tspath.RootedDirectoryPathFromAbsolute(workspaceRoot),
		nil,
		tspath.RootedFilePathFromAbsolute(syntheticPath),
		nil,
		nil,
	)
	if diags := parsed.GetConfigFileParsingDiagnostics(); len(diags) > 0 {
		return nil, fmt.Errorf("synthetic tsconfig diagnostics: %s", joinDiagnostics(diags))
	}
	return parsed, nil
}

func discoverConfigGroups(workspaceRoot string, files []string, configs *configCache) ([]configGroup, error) {
	grouped := make(map[string][]string)
	for _, file := range files {
		configPath, err := findNearestConfigPath(workspaceRoot, file)
		if err != nil {
			return nil, err
		}
		if configPath != "" {
			configPath, err = configs.owningConfig(configPath, file)
			if err != nil {
				return nil, err
			}
		}
		grouped[configPath] = append(grouped[configPath], file)
	}
	keys := make([]string, 0, len(grouped))
	for key := range grouped {
		keys = append(keys, key)
	}
	slices.Sort(keys)
	groups := make([]configGroup, 0, len(keys))
	for _, key := range keys {
		primaryFiles := grouped[key]
		slices.Sort(primaryFiles)
		group := configGroup{ConfigPath: key, PrimaryFiles: primaryFiles}
		if key != "" {
			command, err := configs.program(key)
			if err != nil {
				return nil, err
			}
			group.Config = command
		}
		groups = append(groups, group)
	}
	return groups, nil
}

func findNearestConfigPath(workspaceRoot string, file string) (string, error) {
	rootPath := filepath.Clean(filepath.FromSlash(workspaceRoot))
	current := filepath.Dir(filepath.FromSlash(file))
	for {
		candidate := filepath.Join(current, "tsconfig.json")
		if _, err := os.Stat(candidate); err == nil {
			return normalizeSlash(candidate), nil
		} else if !os.IsNotExist(err) {
			return "", err
		}
		if current == rootPath {
			return "", nil
		}
		parent := filepath.Dir(current)
		if parent == current {
			return "", nil
		}
		relative, err := filepath.Rel(rootPath, parent)
		if err != nil {
			return "", err
		}
		if strings.HasPrefix(relative, "..") {
			return "", nil
		}
		current = parent
	}
}

func mergeSnapshot(into *Snapshot, partial *Snapshot) {
	into.Modules = mergeByEntityID(into.Modules, partial.Modules, func(item Module) string { return item.EntityID })
	into.Functions = mergeByEntityID(into.Functions, partial.Functions, func(item Function) string { return item.EntityID })
	into.ImportEdges = mergeByEntityID(into.ImportEdges, partial.ImportEdges, func(item ImportEdge) string { return item.EntityID })
	into.CallEdges = mergeByEntityID(into.CallEdges, partial.CallEdges, func(item CallEdge) string { return item.EntityID })
	into.ExternalCalls = mergeByEntityID(into.ExternalCalls, partial.ExternalCalls, func(item ExternalCall) string { return item.EntityID })
	into.CallsUnresolved = mergeByEntityID(into.CallsUnresolved, partial.CallsUnresolved, func(item CallUnresolved) string { return item.EntityID })
	into.ImportsOpaque = mergeByEntityID(into.ImportsOpaque, partial.ImportsOpaque, func(item ImportOpaque) string { return item.EntityID })
	into.FnRefs = mergeByEntityID(into.FnRefs, partial.FnRefs, func(item FnRef) string { return item.EntityID })
	into.TypeRefs = mergeByEntityID(into.TypeRefs, partial.TypeRefs, func(item TypeRef) string { return item.EntityID })
	into.AmbientUses = mergeByEntityID(into.AmbientUses, partial.AmbientUses, func(item AmbientUse) string { return item.EntityID })
	into.GlobalRefs = mergeByEntityID(into.GlobalRefs, partial.GlobalRefs, func(item GlobalRef) string { return item.EntityID })
	into.Directives = mergeByEntityID(into.Directives, partial.Directives, func(item ModuleDirective) string { return item.EntityID })
	into.ParseErrors = mergeByEntityID(into.ParseErrors, partial.ParseErrors, func(item ParseError) string { return item.EntityID })
	into.PendingDtsCalls = append(into.PendingDtsCalls, partial.PendingDtsCalls...)
	into.Types = mergeTypes(into.Types, partial.Types)
}

func sortSnapshot(snapshot *Snapshot) {
	slices.SortFunc(snapshot.Modules, func(a, b Module) int { return strings.Compare(a.SemanticKey, b.SemanticKey) })
	slices.SortFunc(snapshot.Functions, func(a, b Function) int { return strings.Compare(a.SemanticKey, b.SemanticKey) })
	slices.SortFunc(snapshot.ImportEdges, func(a, b ImportEdge) int { return strings.Compare(a.SemanticKey, b.SemanticKey) })
	slices.SortFunc(snapshot.CallEdges, func(a, b CallEdge) int { return strings.Compare(a.SemanticKey, b.SemanticKey) })
	slices.SortFunc(snapshot.ExternalCalls, func(a, b ExternalCall) int { return strings.Compare(a.SemanticKey, b.SemanticKey) })
	slices.SortFunc(snapshot.CallsUnresolved, func(a, b CallUnresolved) int { return strings.Compare(a.SemanticKey, b.SemanticKey) })
	slices.SortFunc(snapshot.ImportsOpaque, func(a, b ImportOpaque) int { return strings.Compare(a.SemanticKey, b.SemanticKey) })
	slices.SortFunc(snapshot.FnRefs, func(a, b FnRef) int { return strings.Compare(a.SemanticKey, b.SemanticKey) })
	slices.SortFunc(snapshot.TypeRefs, func(a, b TypeRef) int { return strings.Compare(a.SemanticKey, b.SemanticKey) })
	slices.SortFunc(snapshot.AmbientUses, func(a, b AmbientUse) int { return strings.Compare(a.SemanticKey, b.SemanticKey) })
	slices.SortFunc(snapshot.GlobalRefs, func(a, b GlobalRef) int { return strings.Compare(a.SemanticKey, b.SemanticKey) })
	slices.SortFunc(snapshot.Directives, func(a, b ModuleDirective) int { return strings.Compare(a.SemanticKey, b.SemanticKey) })
	slices.SortFunc(snapshot.ParseErrors, func(a, b ParseError) int { return strings.Compare(a.SemanticKey, b.SemanticKey) })
	slices.SortFunc(snapshot.Types, func(a, b TypeFact) int { return strings.Compare(a.ID, b.ID) })
}

func mergeByEntityID[T any](existing []T, incoming []T, entityID func(T) string) []T {
	seen := make(map[string]struct{}, len(existing))
	for _, item := range existing {
		seen[entityID(item)] = struct{}{}
	}
	for _, item := range incoming {
		id := entityID(item)
		if _, ok := seen[id]; ok {
			continue
		}
		existing = append(existing, item)
		seen[id] = struct{}{}
	}
	return existing
}

func (e *extractor) walkFunctions(node *ast.Node) {
	if node == nil {
		return
	}
	if ast.IsFunctionLike(node) {
		e.collectFunction(node)
	}
	node.ForEachChild(func(child *ast.Node) bool {
		e.walkFunctions(child)
		return false
	})
}

func (e *extractor) walkFacts(node *ast.Node, currentFunction *functionState) {
	if node == nil {
		return
	}
	switch {
	case ast.IsImportDeclaration(node):
		e.collectImport(node, node.ModuleSpecifier(), ImportFormStatic, node.ImportClause() != nil && node.ImportClause().IsTypeOnly())
	case ast.IsExportDeclaration(node) && node.ModuleSpecifier() != nil:
		e.collectImport(node, node.ModuleSpecifier(), ImportFormReexport, node.IsTypeOnly())
	case ast.IsExternalModuleImportEqualsDeclaration(node):
		e.collectImport(node, node.AsImportEqualsDeclaration().ModuleReference.Expression(), ImportFormImportEquals, node.IsTypeOnly())
	case ast.IsLiteralImportTypeNode(node):
		e.collectImport(node, node.AsImportTypeNode().Argument.AsLiteralTypeNode().Literal, ImportFormImportType, true)
	case ast.IsImportCall(node):
		if arguments := node.Arguments(); len(arguments) > 0 {
			if ast.IsStringLiteralLike(arguments[0]) {
				e.collectImport(node, arguments[0], ImportFormDynamic, false)
			} else {
				e.collectOpaqueImport(node)
			}
		}
	case ast.IsRequireCall(node, true):
		e.collectImport(node, node.Arguments()[0], ImportFormRequire, false)
	case ast.IsRequireCall(node, false):
		e.collectOpaqueImport(node)
	case ast.IsTypeReferenceNode(node):
		e.collectTypeRef(node.AsTypeReferenceNode().TypeName, node.AsTypeReferenceNode().TypeName.AsNode())
	case ast.IsExpressionWithTypeArguments(node):
		expression := node.AsExpressionWithTypeArguments().Expression
		e.collectTypeRef(expression.AsNode(), expression.AsNode())
	case ast.IsCallExpression(node), ast.IsNewExpression(node), ast.IsTaggedTemplateExpression(node):
		e.collectCall(e.callSourceFor(node, currentFunction), node)
	case ast.IsIdentifier(node):
		if e.ambientFile != nil {
			e.matchAmbient(node, currentFunction)
			e.matchGlobalRef(node, currentFunction)
			e.matchFnRef(node, currentFunction)
		}
	}
	if e.ambientFile != nil {
		switch node.Kind {
		case ast.KindPropertyAccessExpression, ast.KindElementAccessExpression, ast.KindBindingElement, ast.KindShorthandPropertyAssignment, ast.KindPropertyAssignment:
			e.matchAmbientMember(node, currentFunction)
		}
	}

	nextFunction := currentFunction
	if ast.IsFunctionLike(node) {
		if record := e.lookupFunction(node); record != nil {
			if e.ambientFile != nil && createsFunctionValue(node) {
				e.recordFnRef(e.callSourceFor(node, currentFunction), record, record.Range)
			}
			nextFunction = &functionState{record: record}
		}
	}
	if ast.IsPropertyDeclaration(node) && !ast.IsStatic(node) && node.Initializer() != nil && ast.IsClassLike(node.Parent) {
		if constructor := ast.GetFirstConstructorWithBody(node.Parent); constructor != nil {
			if record := e.lookupFunction(constructor); record != nil {
				nextFunction = &functionState{record: record}
			}
		}
	}

	node.ForEachChild(func(child *ast.Node) bool {
		e.walkFacts(child, nextFunction)
		return false
	})
}

func (e *extractor) collectFunction(node *ast.Node) *Function {
	if node.Body() == nil {
		return nil
	}
	meta := functionMetaForNode(node)
	if meta == nil || meta.localName == "" || meta.nameNode == nil {
		return nil
	}

	file := ast.GetSourceFileOfNode(node)
	if file == nil {
		return nil
	}
	fileName := normalizeSlash(file.FileName().AsString())
	if _, ok := e.requestedFiles[fileName]; !ok {
		return nil
	}

	containerName := containerNameForNode(node, meta.namingSource)
	displayName := meta.localName
	if containerName != "" {
		displayName = containerName + "." + meta.localName
	}

	nameRange := e.nodeLocation(file, meta.nameNode)
	returnType, parameterTypes := e.functionTypes(node)
	baseKey := fmt.Sprintf("%s::%s::%s", nameRange.File, meta.kind, displayName)
	semanticKey := withOrdinal(baseKey, nextOrdinal(e.functionCount, baseKey))
	record := Function{
		EntityID:       "function:" + semanticKey,
		SemanticKey:    semanticKey,
		Name:           displayName,
		Kind:           meta.kind,
		FilePath:       nameRange.File,
		ContainerName:  containerName,
		ContainsAwait:  containsAwait(node),
		IsExported:     meta.exported || e.isExportedFunction(node, meta),
		IsAsync:        ast.HasSyntacticModifier(node, ast.ModifierFlagsAsync),
		ParameterCount: len(node.Parameters()),
		ReturnType:     returnType,
		ParameterTypes: parameterTypes,
		Range:          nameRange,
		BodyStart:      node.Body().Pos(),
		BodyEnd:        node.Body().End(),
	}
	e.functions = append(e.functions, record)
	stored := &e.functions[len(e.functions)-1]
	for _, lookupNode := range meta.lookupNodes {
		if lookupNode == nil {
			continue
		}
		e.functionNodes[lookupNode] = stored
		if key := nodeKey(lookupNode); key != "" {
			e.functionKeys[key] = stored
		}
		if symbol := lookupNode.Symbol(); symbol != nil {
			e.functionSyms[symbol] = stored
		}
	}
	if key := nodeKey(node); key != "" {
		e.functionKeys[key] = stored
	}
	if symbol := node.Symbol(); symbol != nil {
		e.functionSyms[symbol] = stored
	}
	return stored
}

func (e *extractor) collectImport(node *ast.Node, moduleSpecifier *ast.Node, form string, typeOnly bool) {
	file := ast.GetSourceFileOfNode(node)
	if file == nil {
		return
	}
	fileName := normalizeSlash(file.FileName().AsString())
	if _, ok := e.primaryFiles[fileName]; !ok {
		return
	}
	if moduleSpecifier == nil || !ast.IsStringLiteralLike(moduleSpecifier) {
		return
	}
	specifier := moduleSpecifier.Text()
	fromPath := e.relativePath(fileName)
	toPath := specifier
	internal := false
	unresolved := false
	packageName := ""
	if resolved := e.resolveModuleSpecifier(file, moduleSpecifier); resolved.IsResolved() {
		resolvedPath := normalizeSlash(resolved.ResolvedFileName.AsString())
		if isWithinRoot(e.workspaceRoot, resolvedPath) && !strings.Contains(resolvedPath, "/node_modules/") {
			internal = true
			toPath = e.relativePath(resolvedPath)
			packageName = resolved.PackageId.Name
		} else {
			packageName = packageNameFromSpecifier(specifier)
		}
	} else if e.checker.ResolveExternalModuleName(moduleSpecifier, nil) != nil {
		packageName = packageNameFromSpecifier(specifier)
	} else {
		unresolved = true
	}
	location := e.nodeLocation(file, moduleSpecifier)
	baseKey := fmt.Sprintf("%s::import::%s", fromPath, specifier)
	semanticKey := withOrdinal(baseKey, nextOrdinal(e.importCount, baseKey))
	importedSymbols, hasDefaultImport, hasNamespaceImport, hasNamedImports := extractImportedSymbols(node, typeOnly)
	e.imports = append(e.imports, ImportEdge{
		EntityID:           "import:" + semanticKey,
		SemanticKey:        semanticKey,
		Specifier:          specifier,
		FromPath:           fromPath,
		ToPath:             toPath,
		Form:               form,
		Internal:           internal,
		Unresolved:         unresolved,
		Package:            packageName,
		ImportedSymbols:    importedSymbols,
		HasDefaultImport:   hasDefaultImport,
		HasNamespaceImport: hasNamespaceImport,
		HasNamedImports:    hasNamedImports,
		IsTypeOnly:         typeOnly,
		Retained:           e.isRetainedTypeImport(node, typeOnly, importedSymbols),
		Range:              location,
	})
}

// isRetainedTypeImport reports an import or re-export declaration that tsc
// keeps in the output although every binding is type-only. With
// verbatimModuleSyntax, import { type X } from "m" becomes import {} from "m",
// and export { type X } from "m" becomes export {} from "m". A default or
// namespace binding is a value binding, so it does not need the flag.
func (e *extractor) isRetainedTypeImport(node *ast.Node, typeOnly bool, symbols []ImportedSymbol) bool {
	if typeOnly || !(ast.IsImportDeclaration(node) || ast.IsExportDeclaration(node)) || len(symbols) == 0 || !e.program.Options().VerbatimModuleSyntax.IsTrue() {
		return false
	}
	for _, symbol := range symbols {
		if !symbol.IsTypeOnly {
			return false
		}
	}
	return true
}

// resolveModuleSpecifier returns the program's resolution of a specifier. The
// program does not resolve some specifiers, for example require() calls in
// TypeScript files, so it falls back to the program's resolver.
func (e *extractor) resolveModuleSpecifier(file *ast.SourceFile, moduleSpecifier *ast.Node) *module.ResolvedModule {
	if resolved := e.program.GetResolvedModuleFromModuleSpecifier(file, moduleSpecifier); resolved.IsResolved() {
		return resolved
	}
	mode := e.program.GetModeForUsageLocation(file, moduleSpecifier)
	resolved, _, _ := e.resolver.ResolveModuleName(moduleSpecifier.Text(), file.FileName(), mode, nil)
	return resolved
}

// packageNameFromSpecifier returns the package part of a bare specifier:
// "next" for "next/server" and "@scope/pkg" for "@scope/pkg/sub".
func packageNameFromSpecifier(specifier string) string {
	if specifier == "" || tspath.IsExternalModuleNameRelative(specifier) {
		return ""
	}
	parts := strings.Split(specifier, "/")
	if strings.HasPrefix(specifier, "@") && len(parts) > 1 {
		return parts[0] + "/" + parts[1]
	}
	return parts[0]
}

func (e *extractor) callSourceFor(node *ast.Node, currentFunction *functionState) callSource {
	if currentFunction != nil {
		record := currentFunction.record
		return callSource{key: record.SemanticKey, name: record.Name, path: record.FilePath}
	}
	relative := e.relativePath(normalizeSlash(ast.GetSourceFileOfNode(node).FileName().AsString()))
	return callSource{key: relative, name: relative, path: relative, module: true}
}

// collectOpaqueImport records an import() or require() whose specifier is
// not a string literal.
func (e *extractor) collectOpaqueImport(node *ast.Node) {
	file := ast.GetSourceFileOfNode(node)
	if file == nil {
		return
	}
	fileName := normalizeSlash(file.FileName().AsString())
	if _, ok := e.primaryFiles[fileName]; !ok {
		return
	}
	fromPath := e.relativePath(fileName)
	baseKey := fromPath + "::import_opaque"
	semanticKey := withOrdinal(baseKey, nextOrdinal(e.opaqueCount, baseKey))
	e.opaque = append(e.opaque, ImportOpaque{
		EntityID:    "import_opaque:" + semanticKey,
		SemanticKey: semanticKey,
		FromPath:    fromPath,
		Range:       e.nodeLocation(file, node),
	})
}

// collectCall records one fact for a call or new expression: a call edge to
// a collected function, an external call, or an unresolved call.
func (e *extractor) collectCall(source callSource, node *ast.Node) {
	file := ast.GetSourceFileOfNode(node)
	if file == nil {
		return
	}
	if _, ok := e.primaryFiles[normalizeSlash(file.FileName().AsString())]; !ok {
		return
	}
	target, declaration := e.resolveCallTarget(node)
	location := e.nodeLocation(file, calleeNameNode(node))
	workspaceClass := e.constructedWorkspaceClass(node)
	if workspaceClass != nil && hasInstanceInitializer(workspaceClass) {
		e.recordUnresolvedCall(source, location)
		return
	}
	if target == nil {
		callee := e.calleeSymbol(node)
		declarationExternal := declaration != nil && e.isExternalDeclaration(declaration)
		if workspaceClass != nil && declarationExternal {
			callee = e.externalBaseClass(workspaceClass)
		}
		calleeExternal := callee != nil && e.isExternalSymbol(callee)
		if (declarationExternal && (calleeExternal || !isValueSignature(declaration))) || (declaration == nil && calleeExternal) {
			if external, ok := e.externalCallFor(source, callee, declaration, location); ok {
				if e.invokesFunctionValue(node, external) {
					e.recordUnresolvedCall(source, location)
					return
				}
				if !e.recordPendingDtsCall(source, declaration, location, external) {
					e.externalCalls = append(e.externalCalls, external)
				}
				e.collectEscapingCallbacks(source, file, node)
				return
			}
		}
		if declaration == nil && workspaceClass != nil {
			return
		}
		e.recordUnresolvedCall(source, location)
		return
	}
	baseKey := fmt.Sprintf("%s::calls::%s", source.key, target.SemanticKey)
	semanticKey := withOrdinal(baseKey, nextOrdinal(e.callCount, baseKey))
	e.calls = append(e.calls, CallEdge{
		EntityID:        "call:" + semanticKey,
		SemanticKey:     semanticKey,
		FromSemanticKey: source.key,
		FromModule:      source.module,
		ToSemanticKey:   target.SemanticKey,
		FromName:        source.name,
		ToName:          target.Name,
		FromPath:        source.path,
		ToPath:          target.FilePath,
		Range:           location,
	})
}

// invokesFunctionValue reports f.call(...) or f.apply(...) where f is not a
// collected function. The default library declares call and apply, but the
// function that runs is f, and lintai does not know it. When f is a
// collected function, the reference to f is an fn_ref.
func (e *extractor) invokesFunctionValue(node *ast.Node, external ExternalCall) bool {
	if external.Module != "lib" {
		return false
	}
	switch external.Name {
	case "CallableFunction.call", "CallableFunction.apply", "NewableFunction.call", "NewableFunction.apply", "Function.call", "Function.apply":
	default:
		return false
	}
	callee := ast.SkipParentheses(calleeOf(node))
	if !ast.IsPropertyAccessExpression(callee) {
		return true
	}
	receiver := ast.SkipParentheses(callee.Expression())
	if !ast.IsIdentifier(receiver) && !ast.IsPropertyAccessExpression(receiver) {
		return true
	}
	return e.lookupFunction(e.checker.GetSymbolAtLocation(receiver)) == nil
}

// collectEscapingCallbacks records a call_unresolved row for each argument
// of an external call that is a function value that lintai cannot follow,
// for example a callback parameter passed to Array.map. Code outside the
// workspace can call that value. An argument that is a collected function
// is an fn_ref, an inline function is collected with its body, and a
// function from outside the workspace is not workspace code.
func (e *extractor) collectEscapingCallbacks(source callSource, file *ast.SourceFile, node *ast.Node) {
	if ast.IsTaggedTemplateExpression(node) {
		return
	}
	for _, argument := range node.Arguments() {
		value := ast.SkipOuterExpressions(argument, ast.OEKAll)
		if ast.IsSpreadElement(value) || ast.IsFunctionExpressionOrArrowFunction(value) || isPlainValue(value) {
			continue
		}
		if ast.IsIdentifier(value) || ast.IsPropertyAccessExpression(value) {
			symbol := e.checker.GetSymbolAtLocation(value)
			if symbol != nil && symbol.Flags()&ast.SymbolFlagsAlias != 0 {
				symbol = e.checker.SkipAlias(symbol)
			}
			if symbol != nil && (e.lookupFunction(symbol) != nil || e.isExternalSymbol(symbol)) {
				continue
			}
		}
		valueType := e.checker.GetTypeAtLocation(value)
		if valueType == nil || len(e.checker.GetSignaturesOfType(e.checker.GetNonNullableType(valueType), checker.SignatureKindCall)) == 0 {
			continue
		}
		e.recordUnresolvedCall(source, e.nodeLocation(file, value))
	}
}

// isPlainValue reports a literal, which is never a function.
func isPlainValue(node *ast.Node) bool {
	return isLiteralValue(node) || ast.IsObjectLiteralExpression(node) || ast.IsArrayLiteralExpression(node)
}

// isLiteralValue reports a literal that runs no code.
func isLiteralValue(node *ast.Node) bool {
	switch node.Kind {
	case ast.KindTrueKeyword, ast.KindFalseKeyword, ast.KindNullKeyword:
		return true
	}
	return ast.IsLiteralExpression(node)
}

// createsFunctionValue reports a collected function that its definition
// passes on as a value: an arrow function or function expression in an
// argument, an array or a return, and a method or function in an object
// literal. A function that is stored in a variable, a class property or a
// default export is a value only where its name is used, and matchFnRef
// records those uses.
func createsFunctionValue(node *ast.Node) bool {
	parent := node.Parent
	if parent == nil {
		return false
	}
	switch {
	case ast.IsMethodDeclaration(node), ast.IsGetAccessorDeclaration(node), ast.IsSetAccessorDeclaration(node):
		return ast.IsObjectLiteralExpression(parent)
	case ast.IsFunctionExpressionOrArrowFunction(node):
		return !ast.IsVariableDeclaration(parent) && !ast.IsPropertyDeclaration(parent) && !ast.IsExportAssignment(parent)
	}
	return false
}

// recordFnRef records a reference from source to a collected function.
func (e *extractor) recordFnRef(source callSource, target *Function, location SourceLocation) {
	baseKey := fmt.Sprintf("%s::refs::%s", source.key, target.SemanticKey)
	semanticKey := withOrdinal(baseKey, nextOrdinal(e.refCount, baseKey))
	e.refs = append(e.refs, FnRef{
		EntityID:        "fn_ref:" + semanticKey,
		SemanticKey:     semanticKey,
		FromSemanticKey: source.key,
		FromModule:      source.module,
		FromPath:        source.path,
		ToSemanticKey:   target.SemanticKey,
		ToPath:          target.FilePath,
		Range:           location,
	})
}

// recordUnresolvedCall records a call whose target is not known: a call
// through a callback, a function-typed value or an any-typed value.
func (e *extractor) recordUnresolvedCall(source callSource, location SourceLocation) {
	baseKey := source.key + "::calls_unresolved"
	semanticKey := withOrdinal(baseKey, nextOrdinal(e.unresolvedCount, baseKey))
	e.unresolved = append(e.unresolved, CallUnresolved{
		EntityID:        "call_unresolved:" + semanticKey,
		SemanticKey:     semanticKey,
		FromSemanticKey: source.key,
		FromModule:      source.module,
		FromPath:        source.path,
		Range:           location,
	})
}

// calleeSymbol returns the symbol of the value that a call calls: the
// symbol of an identifier or a property access, after alias resolution, or
// the base class of a super() call. It returns nil for other callees, for
// example an element access or the result of another call.
func (e *extractor) calleeSymbol(node *ast.Node) *ast.Symbol {
	callee := ast.SkipParentheses(calleeOf(node))
	var symbol *ast.Symbol
	switch {
	case callee.Kind == ast.KindSuperKeyword:
		if t := e.checker.GetTypeAtLocation(callee); t != nil {
			symbol = t.Symbol()
		}
	case ast.IsIdentifier(callee), ast.IsPropertyAccessExpression(callee):
		symbol = e.checker.GetSymbolAtLocation(callee)
	}
	if symbol != nil && symbol.Flags()&ast.SymbolFlagsAlias != 0 {
		symbol = e.checker.SkipAlias(symbol)
	}
	return symbol
}

// isValueSignature reports a signature that a type declares for a function
// value: a function type, a constructor type, or a call or construct
// signature of an interface or type literal. A call with such a signature
// calls a value of that type, and the type does not tell which function.
func isValueSignature(declaration *ast.Node) bool {
	switch declaration.Kind {
	case ast.KindFunctionType, ast.KindConstructorType, ast.KindCallSignature, ast.KindConstructSignature:
		return true
	}
	return false
}

// isExternalDeclaration reports a declaration outside the workspace source
// files: in the default library, in a package or in a declaration file.
func (e *extractor) isExternalDeclaration(declaration *ast.Node) bool {
	file := ast.GetSourceFileOfNode(declaration)
	if file == nil {
		return false
	}
	fileName := normalizeSlash(file.FileName().AsString())
	return file.IsDeclarationFile || !isWithinRoot(e.workspaceRoot, fileName) || strings.Contains(fileName, "/node_modules/")
}

// isExternalSymbol reports a symbol whose declarations are all external.
func (e *extractor) isExternalSymbol(symbol *ast.Symbol) bool {
	declarations := symbol.Declarations()
	if len(declarations) == 0 {
		return false
	}
	for _, declaration := range declarations {
		if !e.isExternalDeclaration(declaration) {
			return false
		}
	}
	return true
}

// constructedWorkspaceClass returns the class declaration for a new
// expression or super() call of a workspace class that has no constructor
// of its own. The checker resolves such a call to the constructor of a
// base class, or to no declaration when no class in the chain has one.
func (e *extractor) constructedWorkspaceClass(node *ast.Node) *ast.Node {
	if !ast.IsNewExpression(node) && ast.SkipParentheses(calleeOf(node)).Kind != ast.KindSuperKeyword {
		return nil
	}
	symbol := e.calleeSymbol(node)
	if symbol == nil || symbol.Flags()&ast.SymbolFlagsClass == 0 {
		return nil
	}
	var class *ast.Node
	for _, declaration := range symbol.Declarations() {
		if ast.GetSourceFileOfNode(declaration) == nil || e.isExternalDeclaration(declaration) {
			return nil
		}
		if ast.IsClassLike(declaration) {
			class = declaration
		}
	}
	if class == nil || ast.GetFirstConstructorWithBody(class) != nil {
		return nil
	}
	return class
}

// externalBaseClass returns the first class outside the workspace in the
// base class chain of a workspace class, or nil.
func (e *extractor) externalBaseClass(class *ast.Node) *ast.Symbol {
	for range 32 {
		heritage := ast.GetClassExtendsHeritageElement(class)
		if heritage == nil {
			return nil
		}
		symbol := e.checker.GetSymbolAtLocation(heritage.Expression())
		if symbol != nil && symbol.Flags()&ast.SymbolFlagsAlias != 0 {
			symbol = e.checker.SkipAlias(symbol)
		}
		if symbol == nil {
			return nil
		}
		if e.isExternalSymbol(symbol) {
			return symbol
		}
		class = nil
		for _, declaration := range symbol.Declarations() {
			if ast.IsClassLike(declaration) {
				class = declaration
			}
		}
		if class == nil {
			return nil
		}
	}
	return nil
}

// hasInstanceInitializer reports a class with an instance property whose
// initializer can run code. The initializer runs on construction, but a
// class without a constructor has no function to attribute it to, so the
// construction is a call that lintai cannot follow. A function or a literal
// initializer runs no code.
func hasInstanceInitializer(class *ast.Node) bool {
	for _, member := range class.Members() {
		if !ast.IsPropertyDeclaration(member) || ast.IsStatic(member) || member.Initializer() == nil {
			continue
		}
		value := ast.SkipOuterExpressions(member.Initializer(), ast.OEKAll)
		if ast.IsFunctionExpressionOrArrowFunction(value) || isLiteralValue(value) {
			continue
		}
		return true
	}
	return false
}

// matchFnRef records a reference to a collected function as a value: an
// identifier or a property name that resolves to the function, outside the
// callee of a call or new expression, a declaration name, an import or
// export, an assignment target and a type position.
func (e *extractor) matchFnRef(node *ast.Node, currentFunction *functionState) {
	parent := node.Parent
	if parent == nil {
		return
	}
	var symbol *ast.Symbol
	reference := node
	switch {
	case ast.IsShorthandPropertyAssignment(parent) && parent.Name() == node:
		symbol = e.checker.GetShorthandAssignmentValueSymbol(parent)
	case ast.IsPropertyAccessExpression(parent) && parent.Name() == node:
		reference = parent
		symbol = e.checker.GetSymbolAtLocation(node)
	case ast.IsJsxTagName(node):
		symbol = e.checker.GetSymbolAtLocation(node)
	default:
		if !ast.IsExpressionNode(node) || ast.IsDeclarationName(node) {
			return
		}
		symbol = e.checker.GetSymbolAtLocation(node)
	}
	if symbol == nil || ast.IsPartOfTypeQuery(node) || ast.IsPartOfTypeNode(node) || isCallee(reference) || ast.IsAssignmentTarget(reference) {
		return
	}
	for ancestor := parent; ancestor != nil; ancestor = ancestor.Parent {
		switch ancestor.Kind {
		case ast.KindImportDeclaration, ast.KindExportDeclaration, ast.KindImportEqualsDeclaration, ast.KindExportAssignment:
			return
		}
		if ast.IsStatement(ancestor) || ast.IsFunctionLike(ancestor) {
			break
		}
	}
	target := e.lookupFunction(symbol)
	if target == nil {
		return
	}
	e.recordFnRef(e.callSourceFor(node, currentFunction), target, e.nodeLocation(e.ambientFile.file, node))
}

// isCallee reports the callee of a call or new expression, through
// parentheses.
func isCallee(node *ast.Node) bool {
	for node.Parent != nil && node.Parent.Kind == ast.KindParenthesizedExpression {
		node = node.Parent
	}
	parent := node.Parent
	return parent != nil && (ast.IsCallExpression(parent) || ast.IsNewExpression(parent) || ast.IsTaggedTemplateExpression(parent)) && calleeOf(parent) == node
}

// calleeOf returns the callee of a call, new or tagged template expression.
func calleeOf(node *ast.Node) *ast.Node {
	if ast.IsTaggedTemplateExpression(node) {
		return node.AsTaggedTemplateExpression().Tag
	}
	return node.Expression()
}

func calleeNameNode(node *ast.Node) *ast.Node {
	callee := ast.SkipParentheses(calleeOf(node))
	if ast.IsPropertyAccessExpression(callee) {
		return callee.Name().AsNode()
	}
	return callee
}

// resolveCallTarget returns the collected function that a call resolves to.
// When it finds none, it returns the declaration of the checker's resolved
// signature, if there is one. A call links to a function only through the
// checker's signature or through the callee's own symbol, never through a
// name lookup, so seen.add() on a Set does not link to a local add.
func (e *extractor) resolveCallTarget(node *ast.Node) (*Function, *ast.Node) {
	var declaration *ast.Node
	if signature := e.checker.GetResolvedSignature(node); signature != nil {
		declaration = signature.Declaration()
		if target := e.lookupFunction(declaration); target != nil {
			return target, nil
		}
		if declaration != nil {
			if target := e.lookupFunction(declaration.Symbol()); target != nil {
				return target, nil
			}
		}
	}
	expression := ast.SkipParentheses(calleeOf(node))
	if expression != nil && (ast.IsIdentifier(expression) || ast.IsPropertyAccessExpression(expression)) {
		if target := e.lookupFunction(e.checker.GetSymbolAtLocation(expression)); target != nil {
			return target, nil
		}
	}
	return nil, declaration
}

// recordPendingDtsCall records a call to a declaration in a workspace dist/
// .d.ts file. resolveDtsCalls later links it to the matching source
// function, or records external when there is none.
func (e *extractor) recordPendingDtsCall(source callSource, decl *ast.Node, location SourceLocation, external ExternalCall) bool {
	if decl == nil {
		return false
	}
	sf := ast.GetSourceFileOfNode(decl)
	if sf == nil {
		return false
	}
	fileName := normalizeSlash(sf.FileName().AsString())
	if !strings.HasSuffix(fileName, ".d.ts") || !isWithinRoot(e.workspaceRoot, fileName) || strings.Contains(fileName, "/node_modules/") {
		return false
	}
	name := ""
	if decl.Symbol() != nil {
		name = decl.Symbol().Name()
	}
	if name == "" {
		return false
	}
	relPath := e.relativePath(fileName)
	if !strings.Contains(relPath, "/dist/") {
		return false
	}
	e.pendingDtsCalls = append(e.pendingDtsCalls, PendingDtsCall{
		DtsPath:         relPath,
		TargetName:      name,
		FromSemanticKey: source.key,
		FromModule:      source.module,
		FromName:        source.name,
		FromPath:        source.path,
		Range:           location,
		Fallback:        external,
	})
	return true
}

// externalCallFor returns the external call for a call of callee, a symbol
// that is declared in the default library, in a package or in a declaration
// file. decl is the declaration of the resolved signature, or else of the
// callee.
func (e *extractor) externalCallFor(source callSource, callee *ast.Symbol, decl *ast.Node, location SourceLocation) (ExternalCall, bool) {
	if decl == nil {
		decl = callee.Declarations()[0]
	}
	moduleName := e.externalModuleName(decl)
	name := qualifiedSymbolName(decl.Symbol())
	if name == "" && callee != nil {
		name = qualifiedSymbolName(callee)
	}
	if moduleName == "" || name == "" {
		return ExternalCall{}, false
	}
	baseKey := fmt.Sprintf("%s::external::%s::%s", source.key, moduleName, name)
	semanticKey := withOrdinal(baseKey, nextOrdinal(e.externalCount, baseKey))
	return ExternalCall{
		EntityID:        "external_call:" + semanticKey,
		SemanticKey:     semanticKey,
		FromSemanticKey: source.key,
		FromModule:      source.module,
		FromPath:        source.path,
		Module:          moduleName,
		Name:            name,
		Range:           location,
	}, true
}

// externalModuleName names the module that declares an external
// declaration: the name of an enclosing declare module "x" block, "lib" for
// the default library, the package name for node_modules, or the
// workspace-relative path of a declaration file. It returns "" for
// declarations in workspace source files.
func (e *extractor) externalModuleName(decl *ast.Node) string {
	for parent := decl.Parent; parent != nil; parent = parent.Parent {
		if ast.IsModuleDeclaration(parent) && ast.IsStringLiteral(parent.Name()) {
			return parent.Name().Text()
		}
	}
	file := ast.GetSourceFileOfNode(decl)
	if file == nil {
		return ""
	}
	if e.program.IsSourceFileDefaultLibrary(file.PathKey()) {
		return "lib"
	}
	fileName := normalizeSlash(file.FileName().AsString())
	if index := strings.LastIndex(fileName, "/node_modules/"); index >= 0 {
		packageName := packageNameFromSpecifier(fileName[index+len("/node_modules/"):])
		if typed, ok := strings.CutPrefix(packageName, "@types/"); ok {
			if scope, name, scoped := strings.Cut(typed, "__"); scoped {
				return "@" + scope + "/" + name
			}
			return typed
		}
		return packageName
	}
	if !file.IsDeclarationFile && isWithinRoot(e.workspaceRoot, fileName) {
		return ""
	}
	return e.relativePath(fileName)
}

// qualifiedSymbolName returns the name of a symbol with the names of its
// containing classes, interfaces and namespaces, for example "JSON.parse"
// or "Set.add". It returns "" for a symbol with an internal name, such as a
// call signature.
func qualifiedSymbolName(symbol *ast.Symbol) string {
	if symbol == nil || isInternalSymbolName(symbol.Name()) {
		return ""
	}
	parts := []string{symbol.Name()}
	for parent := symbol.Parent(); parent != nil; parent = parent.Parent() {
		name := parent.Name()
		if name == "" || strings.HasPrefix(name, "\"") || isInternalSymbolName(name) {
			break
		}
		parts = append([]string{name}, parts...)
	}
	return strings.Join(parts, ".")
}

func isInternalSymbolName(name string) bool {
	return strings.HasPrefix(name, ast.InternalSymbolNamePrefix)
}

// moduleDirectives returns the directive prologue of a file, for example
// "use client".
func (e *extractor) moduleDirectives(file *ast.SourceFile, relative string) []ModuleDirective {
	result := make([]ModuleDirective, 0)
	counts := make(map[string]int)
	for _, statement := range file.Statements.Nodes {
		if !ast.IsPrologueDirective(statement) {
			break
		}
		expression := statement.Expression()
		text := expression.Text()
		baseKey := fmt.Sprintf("%s::directive::%s", relative, text)
		semanticKey := withOrdinal(baseKey, nextOrdinal(counts, baseKey))
		result = append(result, ModuleDirective{
			EntityID:    "directive:" + semanticKey,
			SemanticKey: semanticKey,
			FilePath:    relative,
			Text:        text,
			Range:       e.nodeLocation(file, expression),
		})
	}
	return result
}

// moduleParseError returns the first syntax error of a file.
func (e *extractor) moduleParseError(file *ast.SourceFile, relative string) (ParseError, bool) {
	diagnostics := file.Diagnostics()
	if len(diagnostics) == 0 {
		return ParseError{}, false
	}
	first := diagnostics[0]
	semanticKey := relative + "::parse_error"
	return ParseError{
		EntityID:    "parse_error:" + semanticKey,
		SemanticKey: semanticKey,
		FilePath:    relative,
		Message:     first.String(),
		Range:       e.location(file, first.Pos(), first.End()),
	}, true
}

func mapDtsToSource(dtsPath string) string {
	// packages/foo/dist/bar.d.ts → packages/foo/src/bar.ts
	if !strings.Contains(dtsPath, "/dist/") {
		return ""
	}
	srcPath := strings.Replace(dtsPath, "/dist/", "/src/", 1)
	srcPath = strings.TrimSuffix(srcPath, ".d.ts") + ".ts"
	return srcPath
}

func (e *extractor) lookupFunction(value any) *Function {
	switch typed := value.(type) {
	case *ast.Node:
		if typed == nil {
			return nil
		}
		if record := e.functionNodes[typed]; record != nil {
			return record
		}
		if record := e.functionKeys[nodeKey(typed)]; record != nil {
			return record
		}
		if symbol := typed.Symbol(); symbol != nil {
			return e.functionSyms[symbol]
		}
	case *ast.Symbol:
		if typed == nil {
			return nil
		}
		// Try the symbol as-is and then its fully-resolved alias.
		candidates := []*ast.Symbol{typed}
		if typed.Flags()&ast.SymbolFlagsAlias != 0 {
			if resolved := e.checker.SkipAlias(typed); resolved != nil && resolved != typed {
				candidates = append(candidates, resolved)
			}
		}
		for _, symbol := range candidates {
			if record := e.functionSyms[symbol]; record != nil {
				return record
			}
			if symbol.ValueDeclaration() != nil {
				if record := e.functionNodes[symbol.ValueDeclaration()]; record != nil {
					return record
				}
				if record := e.functionKeys[nodeKey(symbol.ValueDeclaration())]; record != nil {
					return record
				}
			}
			for _, decl := range symbol.Declarations() {
				if record := e.functionNodes[decl]; record != nil {
					return record
				}
				if record := e.functionKeys[nodeKey(decl)]; record != nil {
					return record
				}
			}
		}
	}
	return nil
}

func (e *extractor) collectTypeRef(nameNode *ast.Node, rangeNode *ast.Node) {
	if nameNode == nil || rangeNode == nil {
		return
	}
	file := ast.GetSourceFileOfNode(nameNode)
	if file == nil {
		return
	}
	fileName := normalizeSlash(file.FileName().AsString())
	if _, ok := e.primaryFiles[fileName]; !ok {
		return
	}
	name := qualifiedName(nameNode)
	if name == "" {
		return
	}
	location := e.nodeLocation(file, rangeNode)
	baseKey := fmt.Sprintf("%s::type_ref::%s", location.File, name)
	semanticKey := withOrdinal(baseKey, nextOrdinal(e.typeRefCount, baseKey))
	e.typeRefs = append(e.typeRefs, TypeRef{
		EntityID:    "type_ref:" + semanticKey,
		SemanticKey: semanticKey,
		Name:        name,
		FilePath:    location.File,
		TargetPath:  e.resolveTypeTargetPath(nameNode),
		Range:       location,
	})
}

func (e *extractor) isExportedFunction(node *ast.Node, meta *functionMeta) bool {
	if ast.GetCombinedModifierFlags(meta.namingSource)&ast.ModifierFlagsExport != 0 {
		return true
	}
	for _, lookupNode := range meta.lookupNodes {
		if lookupNode == nil {
			continue
		}
		if symbol := lookupNode.Symbol(); e.isExportedSymbol(symbol) {
			return true
		}
	}
	return e.isExportedSymbol(node.Symbol())
}

func (e *extractor) isExportedSymbol(symbol *ast.Symbol) bool {
	if symbol == nil {
		return false
	}
	if symbol.ExportSymbol() != nil {
		return true
	}
	exportSymbol := e.checker.GetExportSymbolOfSymbol(symbol)
	return exportSymbol != nil && exportSymbol != symbol
}

// functionTypes records the type facts of the return type and the
// parameter types of a function and returns their type IDs.
func (e *extractor) functionTypes(node *ast.Node) (string, []string) {
	started := time.Now()
	defer func() { e.timing.types += time.Since(started) }()
	returnType := ""
	if signature := e.checker.GetSignatureFromDeclaration(node); signature != nil {
		returnType = e.emitFunctionType(e.checker.GetReturnTypeOfSignature(signature), node)
	}
	parameters := node.Parameters()
	parameterTypes := make([]string, 0, len(parameters))
	for _, parameter := range parameters {
		if parameter == nil {
			parameterTypes = append(parameterTypes, "")
			continue
		}
		parameterTypes = append(parameterTypes, e.emitFunctionType(e.checker.GetTypeAtLocation(parameter.AsNode()), parameter.AsNode()))
	}
	return returnType, parameterTypes
}

func (e *extractor) renderTypeText(t *checker.Type, enclosing *ast.Node) string {
	if t == nil {
		return ""
	}
	return e.checker.TypeToStringEx(
		t,
		enclosing,
		checker.TypeFormatFlagsNoTruncation|checker.TypeFormatFlagsUseAliasDefinedOutsideCurrentScope,
		nil,
	)
}

// extractImportedSymbols returns the bindings that an import declaration,
// a re-export or an import-equals declaration introduces. For a re-export,
// the names are the names that the target module exports.
func extractImportedSymbols(node *ast.Node, isTypeOnly bool) ([]ImportedSymbol, bool, bool, bool) {
	switch {
	case ast.IsExportDeclaration(node):
		return extractReexportedSymbols(node, isTypeOnly)
	case ast.IsImportEqualsDeclaration(node):
		return []ImportedSymbol{{Name: node.Name().Text(), Kind: "namespace", IsTypeOnly: isTypeOnly}}, false, true, false
	case !ast.IsImportDeclaration(node):
		return nil, false, false, false
	}
	decl := node.AsImportDeclaration()
	if decl.ImportClause == nil {
		return nil, false, false, false
	}
	clause := decl.ImportClause.AsImportClause()
	if clause == nil {
		return nil, false, false, false
	}

	symbols := make([]ImportedSymbol, 0, 4)
	hasDefaultImport := false
	hasNamespaceImport := false
	hasNamedImports := false

	if name := clause.Name(); name != nil {
		hasDefaultImport = true
		symbols = append(symbols, ImportedSymbol{
			Name:       name.Text(),
			Kind:       "default",
			IsTypeOnly: isTypeOnly,
		})
	}
	if clause.NamedBindings == nil {
		return symbols, hasDefaultImport, hasNamespaceImport, hasNamedImports
	}

	switch clause.NamedBindings.Kind {
	case ast.KindNamespaceImport:
		hasNamespaceImport = true
		if name := clause.NamedBindings.Name(); name != nil {
			symbols = append(symbols, ImportedSymbol{
				Name:       name.Text(),
				Kind:       "namespace",
				IsTypeOnly: clause.NamedBindings.IsTypeOnly(),
			})
		}
	case ast.KindNamedImports:
		hasNamedImports = true
		for _, specifier := range clause.NamedBindings.Elements() {
			if specifier == nil || specifier.Name() == nil {
				continue
			}
			symbols = append(symbols, ImportedSymbol{
				Name:       specifier.Name().Text(),
				Kind:       "named",
				IsTypeOnly: specifier.IsTypeOnly() || isTypeOnly,
			})
		}
	}
	return symbols, hasDefaultImport, hasNamespaceImport, hasNamedImports
}

func extractReexportedSymbols(node *ast.Node, isTypeOnly bool) ([]ImportedSymbol, bool, bool, bool) {
	clause := node.AsExportDeclaration().ExportClause
	if clause == nil {
		return nil, false, false, false
	}
	if clause.Kind == ast.KindNamespaceExport {
		return []ImportedSymbol{{Name: clause.Name().Text(), Kind: "namespace", IsTypeOnly: isTypeOnly}}, false, true, false
	}
	symbols := make([]ImportedSymbol, 0, 4)
	for _, specifier := range clause.Elements() {
		if specifier == nil || specifier.Name() == nil {
			continue
		}
		nameNode := specifier.Name()
		if property := specifier.PropertyName(); property != nil {
			nameNode = property
		}
		symbols = append(symbols, ImportedSymbol{
			Name:       nameNode.Text(),
			Kind:       "named",
			IsTypeOnly: specifier.IsTypeOnly() || isTypeOnly,
		})
	}
	return symbols, false, false, len(symbols) > 0
}

func (e *extractor) resolveTypeTargetPath(nameNode *ast.Node) string {
	seen := map[*ast.Symbol]struct{}{}
	queue := make([]*ast.Symbol, 0, 4)
	enqueue := func(symbol *ast.Symbol) {
		if symbol == nil {
			return
		}
		if _, ok := seen[symbol]; ok {
			return
		}
		seen[symbol] = struct{}{}
		queue = append(queue, symbol)
	}

	e.enqueueTypeReferenceSymbols(nameNode, enqueue)
	if typ := e.checker.GetTypeAtLocation(nameNode); typ != nil {
		enqueue(typ.Symbol())
	}

	for len(queue) > 0 {
		symbol := queue[0]
		queue = queue[1:]

		if symbol.Flags()&ast.SymbolFlagsAlias != 0 {
			if aliased := e.checker.GetAliasedSymbol(symbol); aliased != nil && aliased != symbol {
				if path := e.symbolTargetPath(aliased); path != "" {
					return path
				}
				enqueue(aliased)
			}
			if immediate := e.checker.GetImmediateAliasedSymbol(symbol); immediate != nil && immediate != symbol {
				if path := e.symbolTargetPath(immediate); path != "" {
					return path
				}
				enqueue(immediate)
			}
		}
		exportSymbol := e.checker.GetExportSymbolOfSymbol(symbol)
		if exportSymbol != nil && exportSymbol != symbol {
			enqueue(exportSymbol)
		}
		if declaredType := e.checker.GetDeclaredTypeOfSymbol(symbol); declaredType != nil {
			enqueue(declaredType.Symbol())
		}
		if path := e.symbolTargetPath(symbol); path != "" {
			return path
		}
	}

	return ""
}

func (e *extractor) enqueueTypeReferenceSymbols(nameNode *ast.Node, enqueue func(*ast.Symbol)) {
	if nameNode == nil {
		return
	}

	enqueue(e.checker.GetSymbolAtLocation(nameNode))

	switch {
	case ast.IsIdentifier(nameNode), ast.IsPrivateIdentifier(nameNode):
		enqueue(e.checker.GetResolvedSymbol(nameNode))
	case ast.IsQualifiedName(nameNode):
		right := nameNode.AsQualifiedName().Right.AsNode()
		enqueue(e.checker.GetSymbolAtLocation(right))
		enqueue(e.checker.GetResolvedSymbol(right))
	case ast.IsPropertyAccessExpression(nameNode):
		right := nameNode.AsPropertyAccessExpression().Name().AsNode()
		enqueue(e.checker.GetSymbolAtLocation(right))
		enqueue(e.checker.GetResolvedSymbol(right))
	}
}

func (e *extractor) symbolTargetPath(symbol *ast.Symbol) string {
	if symbol == nil {
		return ""
	}
	if path := e.nodeTargetPath(symbol.ValueDeclaration()); path != "" {
		return path
	}
	for _, decl := range symbol.Declarations() {
		if path := e.nodeTargetPath(decl); path != "" {
			return path
		}
	}
	return ""
}

func (e *extractor) nodeTargetPath(node *ast.Node) string {
	if node == nil {
		return ""
	}
	file := ast.GetSourceFileOfNode(node)
	if file == nil {
		return ""
	}
	fileName := normalizeSlash(file.FileName().AsString())
	if !isWithinRoot(e.workspaceRoot, fileName) {
		return ""
	}
	return e.relativePath(fileName)
}

func (e *extractor) moduleRange(file *ast.SourceFile) SourceLocation {
	return e.location(file, 0, len(file.Text()))
}

// nodeLocation returns the range of a node from its first token, without
// the leading trivia.
func (e *extractor) nodeLocation(file *ast.SourceFile, node *ast.Node) SourceLocation {
	return e.location(file, scanner.GetTokenPosOfNode(node, file, false), node.End())
}

func (e *extractor) location(file *ast.SourceFile, start int, end int) SourceLocation {
	relative := e.relativePath(file.FileName().AsString())
	if start < 0 || end < 0 {
		return SourceLocation{
			File:        relative,
			StartLine:   1,
			StartColumn: 1,
			EndLine:     1,
			EndColumn:   1,
		}
	}
	if end < start {
		end = start
	}
	lineStarts := file.ECMALineMap()
	startLine, startOffset := core.PositionToLineAndByteOffset(start, lineStarts)
	endLine, endOffset := core.PositionToLineAndByteOffset(end, lineStarts)
	return SourceLocation{
		File:        relative,
		StartLine:   startLine + 1,
		StartColumn: utf16Column(file.Text(), lineStarts, startLine, startOffset),
		EndLine:     endLine + 1,
		EndColumn:   utf16Column(file.Text(), lineStarts, endLine, endOffset),
	}
}

func (e *extractor) relativePath(fileName string) string {
	relative, err := filepath.Rel(filepath.FromSlash(e.workspaceRoot), filepath.FromSlash(fileName))
	if err != nil {
		return normalizeSlash(fileName)
	}
	return filepath.ToSlash(relative)
}

func containsAwait(node *ast.Node) bool {
	body := node.Body()
	if body == nil {
		return false
	}
	found := false
	var visit func(current *ast.Node, root bool)
	visit = func(current *ast.Node, root bool) {
		if current == nil || found {
			return
		}
		if !root && ast.IsFunctionLike(current) {
			return
		}
		if current.Kind == ast.KindAwaitExpression {
			found = true
			return
		}
		current.ForEachChild(func(child *ast.Node) bool {
			visit(child, false)
			return found
		})
	}
	visit(body, true)
	return found
}

func functionMetaForNode(node *ast.Node) *functionMeta {
	switch {
	case ast.IsFunctionDeclaration(node):
		name := node.Name()
		if name == nil {
			if !ast.HasSyntacticModifier(node, ast.ModifierFlagsDefault) {
				return nil
			}
			return &functionMeta{
				localName:    "default",
				kind:         "function",
				nameNode:     node,
				lookupNodes:  []*ast.Node{node},
				namingSource: node,
				exported:     true,
			}
		}
		nameNode := name.AsNode()
		return &functionMeta{
			localName:    name.Text(),
			kind:         "function",
			nameNode:     nameNode,
			lookupNodes:  []*ast.Node{node, nameNode},
			namingSource: node,
		}
	case ast.IsConstructorDeclaration(node):
		return &functionMeta{
			localName:    "constructor",
			kind:         "constructor",
			nameNode:     node,
			lookupNodes:  []*ast.Node{node},
			namingSource: node,
		}
	case ast.IsMethodDeclaration(node):
		name := node.Name()
		if name == nil {
			return nil
		}
		nameNode := name.AsNode()
		return &functionMeta{
			localName:    propertyNameText(nameNode),
			kind:         "method",
			nameNode:     nameNode,
			lookupNodes:  []*ast.Node{node, nameNode},
			namingSource: node,
		}
	case ast.IsGetAccessorDeclaration(node), ast.IsSetAccessorDeclaration(node):
		name := node.Name()
		if name == nil {
			return nil
		}
		kind := "get_accessor"
		if ast.IsSetAccessorDeclaration(node) {
			kind = "set_accessor"
		}
		nameNode := name.AsNode()
		return &functionMeta{
			localName:    propertyNameText(nameNode),
			kind:         kind,
			nameNode:     nameNode,
			lookupNodes:  []*ast.Node{node},
			namingSource: node,
		}
	case ast.IsFunctionExpression(node):
		return expressionFunctionMeta(node, "function_expression")
	case ast.IsArrowFunction(node):
		return expressionFunctionMeta(node, "arrow_function")
	default:
		return nil
	}
}

func expressionFunctionMeta(node *ast.Node, kind string) *functionMeta {
	parent := node.Parent
	if parent == nil {
		return nil
	}
	if ast.IsVariableDeclaration(parent) && parent.Name() != nil {
		nameNode := parent.Name().AsNode()
		return &functionMeta{
			localName:    parent.Name().Text(),
			kind:         kind,
			nameNode:     nameNode,
			namingSource: parent,
			lookupNodes:  []*ast.Node{node, parent, nameNode},
		}
	}
	if ast.IsPropertyAssignment(parent) && parent.Name() != nil {
		nameNode := parent.Name().AsNode()
		return &functionMeta{
			localName:    propertyNameText(nameNode),
			kind:         kind,
			nameNode:     nameNode,
			namingSource: parent,
			lookupNodes:  []*ast.Node{node, parent, nameNode},
		}
	}
	if ast.IsPropertyDeclaration(parent) && parent.Initializer() == node && parent.Name() != nil {
		nameNode := parent.Name().AsNode()
		return &functionMeta{
			localName:    propertyNameText(nameNode),
			kind:         kind,
			nameNode:     nameNode,
			namingSource: parent,
			lookupNodes:  []*ast.Node{node, parent, nameNode},
		}
	}
	if ast.IsExportAssignment(parent) && !parent.AsExportAssignment().IsExportEquals && node.Name() == nil {
		return &functionMeta{
			localName:    "default",
			kind:         kind,
			nameNode:     node,
			namingSource: parent,
			lookupNodes:  []*ast.Node{node, parent},
			exported:     true,
		}
	}
	name := node.Name()
	if name == nil {
		return nil
	}
	nameNode := name.AsNode()
	return &functionMeta{
		localName:    name.Text(),
		kind:         kind,
		nameNode:     nameNode,
		namingSource: node,
		lookupNodes:  []*ast.Node{node, nameNode},
	}
}

func containerNameForNode(node *ast.Node, namingSource *ast.Node) string {
	parts := make([]string, 0, 4)
	for child, parent := node, node.Parent; parent != nil; child, parent = parent, parent.Parent {
		if parent == namingSource {
			continue
		}
		switch {
		case ast.IsClassDeclaration(parent):
			if parent.Name() != nil {
				parts = append([]string{parent.Name().Text()}, parts...)
			}
		case ast.IsFunctionDeclaration(parent):
			if parent.Name() != nil {
				parts = append([]string{parent.Name().Text()}, parts...)
			}
		case ast.IsMethodDeclaration(parent):
			if parent.Name() != nil {
				parts = append([]string{propertyNameText(parent.Name().AsNode())}, parts...)
			}
		case ast.IsConstructorDeclaration(parent):
			parts = append([]string{"constructor"}, parts...)
		case ast.IsVariableDeclaration(parent):
			if parent.Name() != nil && parent.Initializer() == child && (ast.IsObjectLiteralExpression(child) || child.Kind == ast.KindClassExpression) {
				parts = append([]string{parent.Name().Text()}, parts...)
			}
		case ast.IsPropertyAssignment(parent):
			if parent.Name() != nil && parent.Initializer() == child && (ast.IsObjectLiteralExpression(child) || child.Kind == ast.KindClassExpression) {
				parts = append([]string{propertyNameText(parent.Name().AsNode())}, parts...)
			}
		}
	}
	return strings.Join(parts, ".")
}

// propertyNameText returns the text of a property name. A computed name that
// is not a literal, such as [Symbol.iterator], gives its source text in
// brackets.
func propertyNameText(name *ast.Node) string {
	if text, ok := ast.TryGetTextOfPropertyName(name); ok {
		return text
	}
	return "[" + scanner.GetTextOfNode(name.Expression()) + "]"
}

func qualifiedName(node *ast.Node) string {
	switch {
	case node == nil:
		return ""
	case ast.IsIdentifier(node), ast.IsPrivateIdentifier(node):
		return node.Text()
	case ast.IsQualifiedName(node), ast.IsPropertyAccessExpression(node):
		return ast.EntityNameToString(node, nil)
	default:
		return ""
	}
}

func normalizeFiles(files []string) ([]string, error) {
	result := make([]string, 0, len(files))
	for _, file := range files {
		normalized, err := normalizeAbsPath(file)
		if err != nil {
			return nil, err
		}
		result = append(result, normalized)
	}
	slices.Sort(result)
	return slices.Compact(result), nil
}

func normalizeAbsPath(path string) (string, error) {
	absolute, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	return normalizeSlash(filepath.Clean(absolute)), nil
}

func normalizeSlash(path string) string {
	return filepath.ToSlash(path)
}

func isWithinRoot(root string, path string) bool {
	root = strings.TrimSuffix(root, "/")
	path = normalizeSlash(path)
	return path == root || strings.HasPrefix(path, root+"/")
}

func utf16Column(text string, lineStarts []core.TextPos, line int, byteOffset int) int {
	if line < 0 || line >= len(lineStarts) {
		return 1
	}
	start := int(lineStarts[line])
	end := start + byteOffset
	if start < 0 {
		start = 0
	}
	if end < start {
		end = start
	}
	if end > len(text) {
		end = len(text)
	}
	return int(core.UTF16Len(text[start:end])) + 1
}

func joinDiagnostics(diags []*ast.Diagnostic) string {
	parts := make([]string, 0, len(diags))
	for _, diag := range diags {
		if diag == nil {
			continue
		}
		parts = append(parts, diag.String())
	}
	return strings.Join(parts, "; ")
}

func nodeKey(node *ast.Node) string {
	if node == nil {
		return ""
	}
	file := ast.GetSourceFileOfNode(node)
	if file == nil {
		return ""
	}
	return fmt.Sprintf("%s:%d:%d:%d", normalizeSlash(file.FileName().AsString()), node.Kind, node.Pos(), node.End())
}

func nextOrdinal(counts map[string]int, base string) int {
	counts[base]++
	return counts[base]
}

func withOrdinal(base string, ordinal int) string {
	if ordinal <= 1 {
		return base
	}
	return fmt.Sprintf("%s#%d", base, ordinal)
}
