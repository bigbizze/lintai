package lintaiapi

// CallDeclarationFact links an invocation to its selected signature declaration.
// Site is the existing call site's SemanticKey, without an EntityID prefix.
type CallDeclarationFact struct {
	Site, Declaration string
}

// DeclarationMemberFact records a signature declaration's lexical named member.
type DeclarationMemberFact struct {
	Declaration, OwnerDeclaration, Name string
}

// CallDeclarationIncompleteFact records incomplete declaration or member provenance.
// Site is the existing call site's SemanticKey. Reason is unresolved, synthetic,
// unsupported, or conflict.
type CallDeclarationIncompleteFact struct {
	Site, Reason string
}
