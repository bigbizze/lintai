package lintaiapi

import (
	"cmp"
	"encoding/json"
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
	// End offsets distinguish nested invocations that share a start token.
	key, _ := json.Marshal([]any{e.canonicalFileIdentity(file.FileName().AsString()), scanner.GetTokenPosOfNode(node, file, false), node.End(), node.Kind})
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
		if member == nil || !(member.Kind == ast.KindPropertySignature || ast.IsPropertyDeclaration(member)) {
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
		if member == nil || !(member.Kind == ast.KindPropertySignature || ast.IsPropertyDeclaration(member)) {
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
	callee := ast.SkipOuterExpressions(calleeOf(node), ast.OEKAll)
	var symbol *ast.Symbol
	switch {
	case ast.IsPropertyAccessExpression(callee):
		symbol = e.checker.GetSymbolAtLocation(callee)
	case ast.IsElementAccessExpression(callee):
		argument := callee.AsElementAccessExpression().ArgumentExpression
		if argument == nil || !(ast.IsStringLiteralLike(argument) || ast.IsNumericLiteral(argument)) {
			return false
		}
		// The checker resolves static element members at their argument node.
		symbol = e.checker.GetSymbolAtLocation(argument)
	default:
		return false
	}
	if symbol != nil {
		for _, declaration := range symbol.Declarations() {
			if declaration.Kind == ast.KindPropertySignature || ast.IsPropertyDeclaration(declaration) {
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

// Filtering appends this observation only when it materializes the
// callback's unresolved site. Never query a signature on the
// escaped value, and never group it with real invocation observations.
func (callback escapingCallback) callDeclarationObservation() callDeclarationObservation {
	return escapedCallDeclarationObservation(callback.call.SemanticKey)
}

// Compare the original program observations before unioning any evidence. A
// program that did not analyze an invocation contributes no observation. Compare
// even observations whose emitted sites were filtered out, then emit only facts
// attached to surviving runtime sites. Escaped values have no invocation key and
// therefore never enter the disagreement groups.
func finalizeCallDeclarationObservations(snapshot *Snapshot) {
	first := make(map[string]string)
	conflicts := make(map[string]bool)
	for _, observation := range snapshot.callDeclarationObservations {
		if observation.InvocationKey == "" {
			continue
		}
		key, _ := json.Marshal([]any{observation.Site, observation.Declaration, observation.Member,
			strings.Join(sortedUnique(observation.Incomplete), "\x00")})
		if previous, exists := first[observation.InvocationKey]; exists {
			if previous != string(key) {
				conflicts[observation.InvocationKey] = true
			}
		} else {
			first[observation.InvocationKey] = string(key)
		}
	}
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
		if observation.InvocationKey != "" && conflicts[observation.InvocationKey] {
			incomplete[CallDeclarationIncompleteFact{Site: observation.Site, Reason: "conflict"}] = struct{}{}
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
