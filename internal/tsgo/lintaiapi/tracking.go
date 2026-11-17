package lintaiapi

import (
	"context"
	"crypto/sha256"
	"fmt"
	"slices"
	"strings"

	"github.com/bigbizze/lintai/internal/tsgo/internal/ast"
	"github.com/bigbizze/lintai/internal/tsgo/internal/scanner"
)

// TrackingSupport is source-derived reporting support. Data is nil for an
// anchor that has no named subject; otherwise it is the immutable JSON-shaped
// data for lintai.typescript.subject/1.
type TrackingSupport struct {
	Description string
	Data        map[string]any
}

func syntaxKindName(node *ast.Node) string {
	return strings.TrimPrefix(node.Kind.String(), "Kind")
}

func trackingName(node *ast.Node) []any {
	if node == nil {
		return nil
	}
	if ast.IsConstructorDeclaration(node) {
		return []any{"name", "constructor"}
	}
	name := node.Name()
	if name == nil && (ast.IsFunctionExpressionOrArrowFunction(node) || ast.IsClassExpression(node) || ast.IsObjectLiteralExpression(node)) {
		expression := node
		for expression.Parent != nil && ast.IsParenthesizedExpression(expression.Parent) && expression.Parent.Expression() == expression {
			expression = expression.Parent
		}
		parent := expression.Parent
		if parent != nil && (ast.IsVariableDeclaration(parent) || ast.IsPropertyDeclaration(parent) || ast.IsPropertyAssignment(parent)) && parent.Initializer() == expression {
			name = parent.Name()
			if ast.IsVariableDeclaration(parent) && name != nil && !ast.IsIdentifier(name) {
				name = nil
			}
		}
	}
	if name == nil {
		return nil
	}
	switch {
	case ast.IsPrivateIdentifier(name):
		return []any{"private", strings.TrimPrefix(name.Text(), "#")}
	case ast.IsComputedPropertyName(name):
		file := ast.GetSourceFileOfNode(name)
		if file == nil {
			return nil
		}
		start := scanner.GetTokenPosOfNode(name, file, false)
		if start < 0 || name.End() < start || name.End() > len(file.Text()) {
			return nil
		}
		return []any{"computed", file.Text()[start:name.End()]}
	case ast.IsIdentifier(name), ast.IsStringLiteralLike(name), ast.IsNumericLiteral(name):
		return []any{"name", name.Text()}
	default:
		return nil
	}
}

func trackingPlacement(node *ast.Node) string {
	if node == nil {
		return "none"
	}
	member := node
	for member.Parent != nil && ast.IsParenthesizedExpression(member.Parent) && member.Parent.Expression() == member {
		member = member.Parent
	}
	if parent := member.Parent; parent != nil && ast.IsPropertyDeclaration(parent) && parent.Initializer() == member {
		member = parent
	}
	if member.Parent == nil || !ast.IsClassLike(member.Parent) {
		return "none"
	}
	if ast.IsStatic(member) {
		return "static"
	}
	return "instance"
}

func trackingScope(node *ast.Node) []any {
	var reversed []any
	for parent := node.Parent; parent != nil && parent.Kind != ast.KindSourceFile; parent = parent.Parent {
		switch {
		case ast.IsModuleDeclaration(parent), ast.IsClassLike(parent), ast.IsFunctionLike(parent), ast.IsObjectLiteralExpression(parent), ast.IsBlock(parent):
			var name any
			if descriptor := trackingName(parent); descriptor != nil {
				name = descriptor
			}
			reversed = append(reversed, []any{syntaxKindName(parent), name, trackingPlacement(parent)})
		}
	}
	slices.Reverse(reversed)
	if reversed == nil {
		return []any{}
	}
	return reversed
}

func trackingPreview(node *ast.Node) string {
	if node == nil {
		return ""
	}
	file := ast.GetSourceFileOfNode(node)
	if file == nil {
		return ""
	}
	start := scanner.GetTokenPosOfNode(node, file, false)
	if start < 0 || node.End() < start || node.End() > len(file.Text()) {
		return ""
	}
	preview := file.Text()[start:node.End()]
	if line, _, found := strings.Cut(preview, "\n"); found {
		preview = line
	}
	preview = strings.TrimSpace(preview)
	if len(preview) > 120 {
		preview = preview[:120]
	}
	return preview
}

func trackingDescription(category, name string, scope []any, preview string) string {
	var names []string
	for _, raw := range scope {
		part := raw.([]any)
		if descriptor, ok := part[1].([]any); ok {
			text := descriptor[1].(string)
			if descriptor[0] == "private" {
				text = "#" + text
			}
			names = append(names, text)
		}
	}
	names = append(names, name)
	description := fmt.Sprintf("%s %q", category, strings.Join(names, "."))
	if preview != "" {
		description += fmt.Sprintf(": %q", preview)
	}
	return description
}

func (e *extractor) putTracking(id, category, kind string, scope []any, subject []any, name, preview string) {
	if id == "" {
		return
	}
	if e.tracking == nil {
		e.tracking = make(map[string]TrackingSupport)
	}
	if existing, exists := e.tracking[id]; exists && existing.Data != nil {
		return
	}
	description := trackingDescription(category, name, scope, preview)
	data := map[string]any{"category": category, "kind": kind, "scope": scope, "subject": subject}
	e.tracking[id] = TrackingSupport{Description: description, Data: data}
	e.trackingKeyBuilds++
}

func (e *extractor) trackNamed(id, category string, node *ast.Node) {
	cacheKey := e.trackingCacheKey(category, id, node)
	if e.restoreTracking(cacheKey, id) {
		return
	}
	name := trackingName(node)
	if name == nil {
		return
	}
	display := name[1].(string)
	if name[0] == "private" {
		display = "#" + display
	}
	e.putTracking(id, category, syntaxKindName(node), trackingScope(node), []any{"named", name, trackingPlacement(node)}, display, trackingPreview(node))
	e.rememberTracking(cacheKey, id)
}

func (e *extractor) trackModule(id, path string) {
	cacheKey := "module\x00" + id
	if e.restoreTracking(cacheKey, id) {
		return
	}
	e.putTracking(id, "module", "module", []any{}, []any{"module"}, path, "")
	e.rememberTracking(cacheKey, id)
}

func (e *extractor) trackImport(id string, node *ast.Node, form, specifier string) {
	cacheKey := e.trackingCacheKey("site", id, node)
	if e.restoreTracking(cacheKey, id) {
		return
	}
	e.putTracking(id, "site", syntaxKindName(node), trackingScope(node), []any{"module_reference", form, specifier}, specifier, trackingPreview(node))
	e.rememberTracking(cacheKey, id)
}

func (e *extractor) trackExport(id, name, space string) {
	cacheKey := "export\x00" + id
	if e.restoreTracking(cacheKey, id) {
		return
	}
	e.putTracking(id, "export", "export_binding", []any{}, []any{"export_binding", name, space}, name, "")
	e.rememberTracking(cacheKey, id)
}

func (e *extractor) trackNull(id, category string, node *ast.Node) {
	if id == "" {
		return
	}
	if e.tracking == nil {
		e.tracking = make(map[string]TrackingSupport)
	}
	if _, exists := e.tracking[id]; exists {
		return
	}
	description := category
	if preview := trackingPreview(node); preview != "" {
		description += fmt.Sprintf(": %q", preview)
	}
	e.tracking[id] = TrackingSupport{Description: description}
}

func (e *extractor) trackingCacheKey(category, id string, node *ast.Node) string {
	if node == nil {
		return ""
	}
	file := ast.GetSourceFileOfNode(node)
	if file == nil {
		return ""
	}
	if e.trackingSourceDigests == nil {
		e.trackingSourceDigests = make(map[*ast.SourceFile]string)
	}
	digest, ok := e.trackingSourceDigests[file]
	if !ok {
		digest = fmt.Sprintf("%x", sha256.Sum256([]byte(file.Text())))
		e.trackingSourceDigests[file] = digest
	}
	return category + "\x00" + id + "\x00" + digest
}

func (e *extractor) restoreTracking(cacheKey, id string) bool {
	if cacheKey == "" {
		return false
	}
	support, ok := e.operations.trackingCache[cacheKey]
	if !ok {
		support, ok = e.operations.trackingPrevious[cacheKey]
	}
	if !ok {
		return false
	}
	if e.operations.trackingCache != nil {
		e.operations.trackingCache[cacheKey] = support
	}
	if e.tracking == nil {
		e.tracking = make(map[string]TrackingSupport)
	}
	e.tracking[id] = support
	return true
}

func (e *extractor) rememberTracking(cacheKey, id string) {
	if cacheKey == "" || e.operations.trackingCache == nil {
		return
	}
	if support, ok := e.tracking[id]; ok && support.Data != nil {
		e.operations.trackingCache[cacheKey] = support
	}
}

// BuildSnapshotReusingTracking reuses source-derived support only when the
// captured file bytes are identical; the ordinary input validation still runs.
func BuildSnapshotReusingTracking(ctx context.Context, request BuildSnapshotRequest, previous *Snapshot) (*Snapshot, error) {
	request.trackingCache = make(map[string]TrackingSupport)
	if previous != nil {
		request.trackingPrevious = previous.trackingCache
	}
	return BuildSnapshot(ctx, request)
}
