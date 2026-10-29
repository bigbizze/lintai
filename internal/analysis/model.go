package analysis

import "github.com/bigbizze/lintai/diagnostics"

type Module struct {
	EntityID    string
	SemanticKey string
	Path        string
	Range       diagnostics.SourceLocation
}

type Function struct {
	EntityID           string
	SemanticKey        string
	Name               string
	Kind               string
	FilePath           string
	ContainerName      string
	ContainsAwait      bool
	IsExported         bool
	IsAsync            bool
	ParameterCount     int
	ReturnTypeText     string
	ParameterTypeTexts []string
	Range              diagnostics.SourceLocation
	BodyStart          int
	BodyEnd            int
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
	Retained           bool
	Range              diagnostics.SourceLocation
}

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
	Range           diagnostics.SourceLocation
}

type ExternalCall struct {
	EntityID        string
	SemanticKey     string
	FromSemanticKey string
	FromModule      bool
	FromPath        string
	Module          string
	Name            string
	Range           diagnostics.SourceLocation
}

type ModuleDirective struct {
	EntityID    string
	SemanticKey string
	FilePath    string
	Text        string
	Range       diagnostics.SourceLocation
}

// CallUnresolved is a call whose target the checker does not resolve.
type CallUnresolved struct {
	EntityID        string
	SemanticKey     string
	FromSemanticKey string
	FromModule      bool
	FromPath        string
	Range           diagnostics.SourceLocation
}

// ImportOpaque is an import() or require() with a computed specifier.
type ImportOpaque struct {
	EntityID    string
	SemanticKey string
	FromPath    string
	Range       diagnostics.SourceLocation
}

// FnRef is a reference to a collected function as a value.
type FnRef struct {
	EntityID        string
	SemanticKey     string
	FromSemanticKey string
	FromModule      bool
	FromPath        string
	ToSemanticKey   string
	ToPath          string
	Range           diagnostics.SourceLocation
}

// ConfigAdjustment is a compiler option that the backend changed before it
// loaded a tsconfig, because its compiler rejects the option.
type ConfigAdjustment struct {
	// Config is the workspace-relative path of the original tsconfig.
	Config string
	Option string
	// Action is removed, rewritten or replaced.
	Action string
}

type ParseError struct {
	EntityID    string
	SemanticKey string
	FilePath    string
	Message     string
	Range       diagnostics.SourceLocation
}

type TypeRef struct {
	EntityID    string
	SemanticKey string
	Name        string
	FilePath    string
	TargetPath  string
	Range       diagnostics.SourceLocation
}

type Access struct {
	EntityID    string
	SemanticKey string
	Root        string
	AccessPath  string
	Origin      string
	FilePath    string
	Range       diagnostics.SourceLocation
	Start       int
	End         int
}

type AmbientUse struct {
	EntityID            string
	SemanticKey         string
	Source              string
	Capability          string
	FilePath            string
	Key                 string
	HasKey              bool
	Dynamic             bool
	Write               bool
	FunctionSemanticKey string
	Range               diagnostics.SourceLocation
}

type GlobalRef struct {
	EntityID            string
	SemanticKey         string
	Name                string
	FilePath            string
	FunctionSemanticKey string
	Range               diagnostics.SourceLocation
}

type Snapshot struct {
	Version         string
	BackendID       string
	WorkspaceRoot   string
	Modules         []Module
	Functions       []Function
	ImportEdges     []ImportEdge
	CallEdges       []CallEdge
	ExternalCalls   []ExternalCall
	CallsUnresolved []CallUnresolved
	ImportsOpaque   []ImportOpaque
	FnRefs          []FnRef
	TypeRefs        []TypeRef
	Accesses        []Access
	AmbientSources  []AmbientSource
	AmbientUses     []AmbientUse
	GlobalRefs      []GlobalRef
	Directives      []ModuleDirective
	ParseErrors     []ParseError
	Adjustments     []ConfigAdjustment
}

type AmbientSource struct {
	ID           string
	Capabilities []string
}
