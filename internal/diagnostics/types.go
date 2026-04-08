package diagnostics

type Kind string

const (
	KindArchitectureViolation Kind = "architecture_violation"
	KindRuleExecutionError    Kind = "rule_execution_error"
)

type Severity string

const (
	SeverityError   Severity = "error"
	SeverityWarning Severity = "warning"
)

type SourceLocation struct {
	File        string `json:"file"`
	StartLine   int    `json:"startLine"`
	StartColumn int    `json:"startColumn"`
	EndLine     int    `json:"endLine"`
	EndColumn   int    `json:"endColumn"`
}

type EntityIdentity struct {
	SemanticKey string `json:"semantic_key"`
}

type Provenance struct {
	SnapshotVersion string `json:"snapshot_version"`
	RuleVersion     int    `json:"rule_version"`
	BackendID       string `json:"backend_id"`
}

type Diagnostic struct {
	RuleID         string          `json:"rule_id"`
	AssertionID    string          `json:"assertion_id"`
	DiagnosticKind Kind            `json:"diagnostic_kind"`
	Severity       Severity        `json:"severity"`
	Message        string          `json:"message"`
	SourceLocation *SourceLocation `json:"source_location,omitempty"`
	EntityIdentity *EntityIdentity `json:"entity_identity,omitempty"`
	Provenance     Provenance      `json:"provenance"`
	Phase          string          `json:"phase,omitempty"`
}
