package lintaiapi

import (
	"strconv"

	"github.com/bigbizze/lintai/internal/tsgo/internal/ast"
	"github.com/bigbizze/lintai/internal/tsgo/internal/scanner"
)

// SourceSite is the syntax identity and its one lexical attribution, shared
// by all the observations made about that syntax node.
type SourceSite struct {
	ID, File, Owner, UnsupportedReason string
	Range                              SourceLocation
}

func moduleID(file string) string { return "module:" + file }

func (e *extractor) sourceNodeID(prefix, version string, node *ast.Node) string {
	if node == nil {
		return ""
	}
	file := ast.GetSourceFileOfNode(node)
	if file == nil {
		return ""
	}
	return e.sourceRangeID(prefix, version, file, syntaxKindName(node), scanner.GetTokenPosOfNode(node, file, false), node.End())
}

func (e *extractor) sourceRangeID(prefix, version string, file *ast.SourceFile, kind string, start, end int) string {
	key := jsonList(jsonQuote(version), jsonQuote(e.canonicalFileIdentity(file.FileName().AsString())), jsonQuote(kind), strconv.Itoa(start), strconv.Itoa(end))
	return prefix + ":" + hashKey(key)
}

func (e *extractor) registerSite(node *ast.Node, source callSource) string {
	id := e.sourceNodeID("site", "site-v1", node)
	if id == "" {
		return ""
	}
	file := ast.GetSourceFileOfNode(node)
	if _, ok := e.primaryFiles[normalizeSlash(file.FileName().AsString())]; !ok {
		return id
	}
	owner := source.key
	if source.module {
		owner = moduleID(source.path)
	}
	site := SourceSite{ID: id, File: source.path, Owner: owner, UnsupportedReason: source.reason, Range: e.nodeLocation(file, node)}
	if e.sites == nil {
		e.sites = make(map[string]SourceSite)
	}
	if previous, ok := e.sites[id]; ok && previous != site {
		e.siteConflict = id
	} else {
		e.sites[id] = site
	}
	e.trackNull(id, "site", node)
	return id
}

// finalizeCallTargets compares runtime classifications after filtering.
func finalizeCallTargets(snapshot *Snapshot) {
	observed := make(map[string]map[string]bool)
	add := func(site, evidence string) {
		if observed[site] == nil {
			observed[site] = make(map[string]bool)
		}
		observed[site][evidence] = true
	}
	for _, call := range snapshot.CallEdges {
		add(call.SemanticKey, "local\x00"+call.ToSemanticKey)
	}
	for _, call := range snapshot.ExternalCalls {
		add(call.SemanticKey, "external\x00"+call.OriginKind+"\x00"+call.Module+"\x00"+call.Name)
	}
	for _, call := range snapshot.CallsUnresolved {
		add(call.SemanticKey, "unresolved")
	}
	for site, outcomes := range observed {
		if len(outcomes) > 1 {
			snapshot.CallTargetConflicts = append(snapshot.CallTargetConflicts, site)
		}
	}
}

// SignatureID identifies an entire observation, including unknown slots.
func SignatureID(function string, observation FunctionSignatureObservation, namespaces ...string) string {
	namespace := "ts"
	if len(namespaces) != 0 {
		namespace = namespaces[0]
	}
	function = namespace + ":" + function
	params := make([]string, len(observation.ParameterTypes))
	for index, typ := range observation.ParameterTypes {
		typePart, optional, rest := "null", "null", "null"
		if typ != "" {
			typePart = jsonQuote(namespace + ":" + typ)
		}
		if index < len(observation.ParameterMarks) {
			switch observation.ParameterMarks[index] {
			case MarkOptional:
				optional = jsonQuote("optional")
			case MarkRest:
				rest = jsonQuote("rest")
			}
		}
		params[index] = jsonList(typePart, optional, rest)
	}
	returns := "null"
	if observation.ReturnType != "" {
		returns = jsonQuote(namespace + ":" + observation.ReturnType)
	}
	reasons := "[]"
	if !observation.Complete {
		reasons = jsonList(jsonQuote("unresolved"))
	}
	return "signature:" + hashKey(jsonList(jsonQuote("signature-v1"), jsonQuote(function), jsonList(params...), returns, reasons))
}
