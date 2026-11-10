package lintaiapi

import (
	"encoding/json"

	"github.com/bigbizze/lintai/internal/tsgo/internal/ast"
	"github.com/bigbizze/lintai/internal/tsgo/internal/scanner"
)

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

// An observation belongs to one checked program. Do not flatten or reconcile
// these until the program merge has compared the original observations.
// InvocationKey is empty for escaped values, which are not invocations.
type callDeclarationObservation struct {
	InvocationKey string
	Site          string
	Declaration   string
	Member        *DeclarationMemberFact
	Incomplete    []string
}

func (e *extractor) collectCallDeclaration(node *ast.Node, site string) {
	file := ast.GetSourceFileOfNode(node)
	kind := "call"
	if ast.IsNewExpression(node) {
		kind = "new"
	} else if ast.IsTaggedTemplateExpression(node) {
		kind = "tagged_template"
	}
	key, _ := json.Marshal([]any{"invocation-v1", e.canonicalFileIdentity(file.FileName().AsString()), scanner.GetTokenPosOfNode(node, file, false), kind})
	observation := callDeclarationObservation{InvocationKey: string(key), Site: site}
	defer func() {
		e.callDeclarationObservations = append(e.callDeclarationObservations, observation)
	}()

	evidence := e.checker.GetResolvedSignatureEvidence(node)
	switch {
	case evidence.Synthetic:
		observation.Incomplete = []string{"synthetic"}
		return
	case evidence.Recovery || evidence.Signature == nil:
		observation.Incomplete = []string{"unresolved"}
		return
	}
	declaration := evidence.Signature.Declaration()
	if !concreteCallDeclaration(declaration) {
		observation.Incomplete = []string{"unresolved"}
		return
	}
	if ast.IsGetAccessorDeclaration(declaration) || ast.IsSetAccessorDeclaration(declaration) {
		// An accessor signature describes the property access, never the
		// returned callable that this invocation executes.
		observation.Incomplete = []string{"unsupported"}
		return
	}
	id, supported := e.addCallDeclarationMetadata(declaration)
	observation.Declaration = id
	if id == "" || !supported {
		observation.Incomplete = []string{"unsupported"}
		return
	}
	member, unsupported := e.callDeclarationMember(declaration, id)
	observation.Member = member
	if unsupported || member == nil && e.callUsesUnassociatedProperty(node) {
		observation.Incomplete = []string{"unsupported"}
	}
}

// Reject missing, recovery, and out-of-source nodes before metadata machinery
// assigns declaration identities. The checker evidence API rejects synthesized
// combinations independently of their potentially concrete component nodes.
func concreteCallDeclaration(node *ast.Node) bool {
	if node == nil {
		return false
	}
	file := ast.GetSourceFileOfNode(node)
	if file == nil || node.Pos() < 0 || node.End() > len(file.Text()) || node.End() <= node.Pos() {
		return false
	}
	start := scanner.GetTokenPosOfNode(node, file, false)
	return start >= node.Pos() && start < node.End()
}

func (e *extractor) addCallDeclarationMetadata(node *ast.Node) (string, bool) {
	if !concreteCallDeclaration(node) {
		return "", false
	}
	name := ""
	if node.Name() != nil {
		name, _ = ast.TryGetTextOfPropertyName(node.Name())
	}
	id, fact, supported := e.addDeclarationMetadata(node, node.Symbol(), name)
	if fact != nil {
		// A symbol fallback can select a different overload or implementation.
		if function := e.functionNodes[node]; function != nil {
			fact.Function = function.SemanticKey
			fact.FunctionObservations = append(fact.FunctionObservations, function.SemanticKey)
		}
	}
	return id, supported
}

func (e *extractor) callDeclarationMember(declaration *ast.Node, id string) (*DeclarationMemberFact, bool) {
	member := declaration
	switch declaration.Kind {
	case ast.KindMethodDeclaration, ast.KindMethodSignature:
		// The parent is the actual declaring component, including inheritance.
	case ast.KindFunctionType:
		member = declaration
		for member.Parent != nil && ast.IsParenthesizedTypeNode(member.Parent) {
			member = member.Parent
		}
		member = member.Parent
		if member == nil || !(ast.IsPropertySignature(member) || ast.IsPropertyDeclaration(member)) {
			return nil, false // Anonymous signature, alias, or returned callable.
		}
	case ast.KindCallSignature:
		literal := declaration.Parent
		if literal == nil || !ast.IsTypeLiteralNode(literal) {
			return nil, false // An anonymous signature directly on an interface.
		}
		for literal.Parent != nil && ast.IsParenthesizedTypeNode(literal.Parent) {
			literal = literal.Parent
		}
		member = literal.Parent
		if member == nil || !(ast.IsPropertySignature(member) || ast.IsPropertyDeclaration(member)) {
			return nil, false
		}
	default:
		return nil, false
	}
	owner := member.Parent
	if owner == nil || !(ast.IsInterfaceDeclaration(owner) || ast.IsClassLike(owner)) || member.Name() == nil {
		return nil, true
	}
	name, ok := ast.TryGetTextOfPropertyName(member.Name())
	if !ok {
		return nil, true
	}
	ownerID, supported := e.addCallDeclarationMetadata(owner)
	if ownerID == "" || !supported {
		return nil, true
	}
	return &DeclarationMemberFact{Declaration: id, OwnerDeclaration: ownerID, Name: name}, false
}

// A selected signature shared through an alias does not lexically belong to
// the observed property. Inspect only that property's declarations, never its
// receiver or the members of its type. Accessors returning callables are not
// property signature declarations and must not become the callable endpoint.
func (e *extractor) callUsesUnassociatedProperty(node *ast.Node) bool {
	callee := ast.SkipParentheses(calleeOf(node))
	if !ast.IsPropertyAccessExpression(callee) && !ast.IsElementAccessExpression(callee) {
		return false
	}
	symbol := e.checker.GetSymbolAtLocation(callee)
	if symbol != nil {
		for _, declaration := range symbol.Declarations() {
			if ast.IsPropertySignature(declaration) || ast.IsPropertyDeclaration(declaration) {
				return true
			}
		}
	}
	return false
}

func escapedCallDeclarationObservation(site string) callDeclarationObservation {
	return callDeclarationObservation{Site: site, Incomplete: []string{"unsupported"}}
}

func (e *extractor) recordEscapedCallDeclaration(source callSource, location SourceLocation) {
	site := e.recordUnresolvedCall(source, location)
	e.callDeclarationObservations = append(e.callDeclarationObservations, escapedCallDeclarationObservation(site))
}

// The filtering/finalization task must append this observation only when it
// materializes the callback's unresolved site. Never query a signature on the
// escaped value, and never group it with real invocation observations.
func (callback escapingCallback) callDeclarationObservation() callDeclarationObservation {
	return escapedCallDeclarationObservation(callback.call.SemanticKey)
}
