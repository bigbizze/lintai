package analysis

import "github.com/bigbizze/lintai/internal/diagnostics"

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
	ImportedSymbols    []ImportedSymbol
	HasDefaultImport   bool
	HasNamespaceImport bool
	HasNamedImports    bool
	IsTypeOnly         bool
	Range              diagnostics.SourceLocation
}

type CallEdge struct {
	EntityID        string
	SemanticKey     string
	FromSemanticKey string
	ToSemanticKey   string
	FromName        string
	ToName          string
	FromPath        string
	ToPath          string
	Range           diagnostics.SourceLocation
}

type TypeRef struct {
	EntityID    string
	SemanticKey string
	Name        string
	FilePath    string
	TargetPath  string
	Range       diagnostics.SourceLocation
}

type Snapshot struct {
	Version          string
	BackendID        string
	WorkspaceRoot    string
	Modules          []Module
	Functions        []Function
	ImportEdges      []ImportEdge
	CallEdges        []CallEdge
	TypeRefs         []TypeRef
	FunctionsByName  map[string][]Function
	FunctionsByKey   map[string]Function
	TransitiveCalls  map[string]map[string]struct{}
	FileDiagnostics  map[string][]diagnostics.Diagnostic
	AvailableKindSet map[string]struct{}
}
