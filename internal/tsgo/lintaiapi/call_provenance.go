package lintaiapi

import (
	"cmp"
	"maps"
	"slices"
	"strings"

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

// An observation belongs to the invocation's checked program.
type callDeclarationObservation struct {
	Site        string
	Declaration string
	Member      *DeclarationMemberFact
	Incomplete  []string
}

func (e *extractor) collectCallDeclaration(node *ast.Node, site string, origin observationOrigin) {
	if !e.validateOrigin(origin, "call declaration", node) {
		return
	}
	observation := callDeclarationObservation{Site: site}
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
	id, supported := e.addCallDeclarationMetadata(origin, declaration)
	observation.Declaration = id
	if id == "" || !supported {
		observation.Incomplete = []string{"unsupported"}
		return
	}
	member, lexicalMember, unsupported := e.callDeclarationMember(origin, declaration, id)
	observation.Member = member
	if unsupported || e.callUsesUnassociatedProperty(node, declaration, lexicalMember) {
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

func (e *extractor) addCallDeclarationMetadata(origin observationOrigin, node *ast.Node) (string, bool) {
	if !concreteCallDeclaration(node) {
		return "", false
	}
	name := ""
	if node.Name() != nil {
		name, _ = ast.TryGetTextOfPropertyName(node.Name())
	}
	id, fact, supported := e.addDeclarationMetadata(origin, node, node.Symbol(), name)
	if fact != nil {
		// A symbol fallback can select a different overload or implementation.
		if function := e.functionNodes[node]; function != nil {
			e.linkDeclarationFunction(fact, function.SemanticKey)
		}
	}
	return id, supported
}

func (e *extractor) callDeclarationMember(origin observationOrigin, declaration *ast.Node, id string) (*DeclarationMemberFact, *ast.Node, bool) {
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
		if member == nil || !(member.Kind == ast.KindPropertySignature || ast.IsPropertyDeclaration(member)) {
			return nil, nil, false // Anonymous signature, alias, or returned callable.
		}
	case ast.KindCallSignature:
		literal := declaration.Parent
		if literal == nil || !ast.IsTypeLiteralNode(literal) {
			return nil, nil, false // An anonymous signature directly on an interface.
		}
		for literal.Parent != nil && ast.IsParenthesizedTypeNode(literal.Parent) {
			literal = literal.Parent
		}
		member = literal.Parent
		if member == nil || !(member.Kind == ast.KindPropertySignature || ast.IsPropertyDeclaration(member)) {
			return nil, nil, false
		}
	default:
		return nil, nil, false
	}
	owner := member.Parent
	if owner == nil || !(ast.IsInterfaceDeclaration(owner) || ast.IsClassLike(owner)) || member.Name() == nil {
		return nil, member, true
	}
	name, ok := ast.TryGetTextOfPropertyName(member.Name())
	if !ok {
		return nil, member, true
	}
	ownerID, supported := e.addCallDeclarationMetadata(origin, owner)
	if ownerID == "" || !supported {
		return nil, member, true
	}
	return &DeclarationMemberFact{Declaration: id, OwnerDeclaration: ownerID, Name: name}, member, false
}

// The selected signature must belong to a declaration of the resolved property.
// One matching declaration suffices when an intersection supplies overloads.
func (e *extractor) callUsesUnassociatedProperty(node, signature, lexicalMember *ast.Node) bool {
	callee := ast.SkipOuterExpressions(calleeOf(node), ast.OEKAll)
	if !ast.IsPropertyAccessExpression(callee) && !ast.IsElementAccessExpression(callee) {
		return false
	}
	symbol := e.checker.GetResolvedPropertySymbol(callee)
	if symbol == nil || len(symbol.Declarations()) == 0 {
		return true // No single checked property establishes the association.
	}
	if lexicalMember != nil {
		return !slices.Contains(symbol.Declarations(), lexicalMember)
	}
	for ancestor := signature; ancestor != nil; ancestor = ancestor.Parent {
		if slices.Contains(symbol.Declarations(), ancestor) {
			return false
		}
	}
	return true
}

// Compare the original program observations before unioning any evidence. A
// program that did not analyze an invocation contributes no observation. Compare
// even observations whose emitted sites were filtered out, then emit only facts
// attached to surviving runtime sites. Escaped values have no invocation key and
// therefore never enter the disagreement groups.
func finalizeCallDeclarationObservations(snapshot *Snapshot) {
	sites := make(map[string]bool)
	for _, call := range snapshot.CallEdges {
		sites[call.SemanticKey] = true
	}
	for _, call := range snapshot.ExternalCalls {
		sites[call.SemanticKey] = true
	}
	for _, call := range snapshot.CallsUnresolved {
		sites[call.SemanticKey] = true
	}
	declarations := make(map[CallDeclarationFact]struct{})
	members := make(map[DeclarationMemberFact]struct{})
	incomplete := make(map[CallDeclarationIncompleteFact]struct{})
	for _, observation := range snapshot.callDeclarationObservations {
		if !sites[observation.Site] {
			continue
		}
		if observation.Declaration != "" {
			declarations[CallDeclarationFact{Site: observation.Site, Declaration: observation.Declaration}] = struct{}{}
			if observation.Member != nil {
				members[*observation.Member] = struct{}{}
			}
		}
		for _, reason := range observation.Incomplete {
			incomplete[CallDeclarationIncompleteFact{Site: observation.Site, Reason: reason}] = struct{}{}
		}
	}
	snapshot.CallDeclarations = slices.Collect(maps.Keys(declarations))
	slices.SortFunc(snapshot.CallDeclarations, func(a, b CallDeclarationFact) int {
		return cmp.Or(strings.Compare(a.Site, b.Site), strings.Compare(a.Declaration, b.Declaration))
	})
	snapshot.DeclarationMembers = slices.Collect(maps.Keys(members))
	slices.SortFunc(snapshot.DeclarationMembers, func(a, b DeclarationMemberFact) int {
		return cmp.Or(strings.Compare(a.Declaration, b.Declaration), strings.Compare(a.OwnerDeclaration, b.OwnerDeclaration), strings.Compare(a.Name, b.Name))
	})
	snapshot.CallDeclarationIncomplete = slices.Collect(maps.Keys(incomplete))
	slices.SortFunc(snapshot.CallDeclarationIncomplete, func(a, b CallDeclarationIncompleteFact) int {
		return cmp.Or(strings.Compare(a.Site, b.Site), strings.Compare(a.Reason, b.Reason))
	})
}
