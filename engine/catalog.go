package engine

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"slices"
	"sort"
	"strings"

	"codeberg.org/TauCeti/mangle-go/ast"
	"codeberg.org/TauCeti/mangle-go/factstore"
	"codeberg.org/TauCeti/mangle-go/parse"
	"codeberg.org/TauCeti/mangle-go/symbols"
	"github.com/bigbizze/lintai/backend"
	"github.com/bigbizze/lintai/facts"
	"github.com/bigbizze/lintai/internal/canonical"
	"github.com/bigbizze/lintai/suppressions"
)

type composedFacts struct {
	base          factstore.ReadOnlyFactStore
	sources       []parse.SourceUnit
	schemaDecls   map[ast.PredicateSym]ast.Decl
	anchors       map[string]facts.AnchorSupport
	reportable    map[facts.Predicate]map[string]struct{}
	analyzedFiles map[string]struct{}
	namespaces    []string
	snapshotID    string
}

type producerEntry struct {
	producer    backend.Producer
	description facts.ProducerDescription
}

func (e *Engine) collect(ctx context.Context, options Options) (composedFacts, error) {
	entries := make([]producerEntry, 0, len(e.producers))
	owned := make(map[string]struct{}, len(e.producers))
	for _, producer := range e.producers {
		description, err := producer.Describe()
		if err != nil {
			return composedFacts{}, fmt.Errorf("describe producer: %w", err)
		}
		if _, duplicate := owned[description.Namespace]; duplicate {
			return composedFacts{}, fmt.Errorf("producer namespace %q is registered more than once", description.Namespace)
		}
		owned[description.Namespace] = struct{}{}
		if _, err := facts.ValidateBatch(description, nil, facts.Batch{Relations: map[facts.Predicate][]facts.Row{}}); err != nil {
			return composedFacts{}, fmt.Errorf("invalid producer description %q: %w", description.Namespace, err)
		}
		entries = append(entries, producerEntry{producer: producer, description: description})
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].description.Namespace < entries[j].description.Namespace })

	var sourceUnits []parse.SourceUnit
	schemaDecls := make(map[ast.PredicateSym]ast.Decl)
	for _, entry := range entries {
		description := entry.description
		declarations, err := namespacedUnit(description.Schema.Declarations, description.Namespace)
		if err != nil {
			return composedFacts{}, fmt.Errorf("producer %q declarations: %w", description.Namespace, err)
		}
		prelude, err := namespacedUnit(description.Schema.Prelude, description.Namespace)
		if err != nil {
			return composedFacts{}, fmt.Errorf("producer %q prelude: %w", description.Namespace, err)
		}
		sourceUnits = append(sourceUnits, declarations, prelude)
		for _, unit := range []parse.SourceUnit{declarations, prelude} {
			for _, declaration := range unit.Decls {
				if isPackageDecl(declaration) {
					continue
				}
				predicate := declaration.DeclaredAtom.Predicate
				key := ast.PredicateSym{Symbol: description.Namespace + "." + predicate.Symbol, Arity: predicate.Arity}
				copy := declaration
				copy.DeclaredAtom.Predicate = key
				if _, duplicate := schemaDecls[key]; duplicate {
					return composedFacts{}, fmt.Errorf("catalog declares %s more than once", key)
				}
				schemaDecls[key] = copy
			}
		}
	}
	engineUnit, err := engineSchemaUnit()
	if err != nil {
		return composedFacts{}, fmt.Errorf("engine helper schema: %w", err)
	}
	sourceUnits = append(sourceUnits, engineUnit)
	for _, declaration := range engineUnit.Decls {
		if isPackageDecl(declaration) {
			continue
		}
		key := ast.PredicateSym{Symbol: "lintai." + declaration.DeclaredAtom.Predicate.Symbol, Arity: declaration.DeclaredAtom.Predicate.Arity}
		copy := declaration
		copy.DeclaredAtom.Predicate = key
		schemaDecls[key] = copy
	}

	allFacts := make([]facts.Fact, 0)
	anchors := make(map[string]facts.AnchorSupport)
	reportable := make(map[facts.Predicate]map[string]struct{})
	analyzedFiles := make(map[string]struct{})
	producerRecords := make([]any, 0, len(entries))
	namespaces := make([]string, 0, len(entries))
	for _, entry := range entries {
		producer := entry.producer
		description := entry.description
		requested := make([]facts.Predicate, len(description.BaseRelations))
		for i, relation := range description.BaseRelations {
			requested[i] = relation.Predicate
		}
		sort.Slice(requested, func(i, j int) bool {
			if requested[i].Name != requested[j].Name {
				return requested[i].Name < requested[j].Name
			}
			return requested[i].Arity < requested[j].Arity
		})
		batch, err := producer.Collect(ctx, backend.CollectionRequest{RepoRoot: options.RepoRoot, WorkspaceRoot: options.WorkspaceRoot, Predicates: slices.Clone(requested)})
		if err != nil {
			return composedFacts{}, fmt.Errorf("collect producer %q: %w", description.Namespace, err)
		}
		validated, err := facts.ValidateBatch(description, requested, batch)
		if err != nil {
			return composedFacts{}, fmt.Errorf("producer %q returned an invalid batch: %w", description.Namespace, err)
		}
		allFacts = append(allFacts, validated.Facts()...)
		producerAnchors := validated.Anchors()
		for id, support := range producerAnchors {
			if current, exists := anchors[id]; exists && !sameAnchor(current, support) {
				return composedFacts{}, fmt.Errorf("entity %q has conflicting producer-owned reporting support", id)
			}
			anchors[id] = support
		}
		for _, relation := range description.BaseRelations {
			for _, row := range validated.Rows(relation.Predicate) {
				for index, argument := range relation.Arguments {
					if argument.Role != facts.EntityRole || argument.ReportSupport == facts.ReportNone || row[index].Kind != facts.StringKind {
						continue
					}
					id := row[index].String
					if _, hasSupport := producerAnchors[id]; !hasSupport {
						continue
					}
					if reportable[relation.Predicate] == nil {
						reportable[relation.Predicate] = make(map[string]struct{})
					}
					reportable[relation.Predicate][id] = struct{}{}
				}
			}
		}
		for _, file := range validated.AnalyzedFiles() {
			analyzedFiles[suppressions.NormalizePath(file)] = struct{}{}
		}
		namespaces = append(namespaces, description.Namespace)
		requestedNames := make([]string, len(requested))
		for i, predicate := range requested {
			requestedNames[i] = predicate.Name
		}
		producerRecords = append(producerRecords, []any{
			"producer-result-v2", description.Namespace, description.ImplementationID,
			description.ImplementationStamp, description.ContractVersion, description.EffectiveConfiguration,
			validated.InputRevision(), validated.AnalyzedFiles(), requestedNames,
		})
	}
	base, err := facts.Store(allFacts)
	if err != nil {
		return composedFacts{}, fmt.Errorf("assembled facts: %w", err)
	}
	record := []any{"lintai-facts-v1", producerRecords}
	encoded, err := canonical.Marshal(record)
	if err != nil {
		return composedFacts{}, fmt.Errorf("snapshot identity: %w", err)
	}
	digest := sha256.Sum256(encoded)
	files := make([]string, 0, len(analyzedFiles))
	for file := range analyzedFiles {
		files = append(files, file)
	}
	sort.Strings(files)
	fileSet := make(map[string]struct{}, len(files))
	for _, file := range files {
		fileSet[file] = struct{}{}
	}
	return composedFacts{
		base: base, sources: sourceUnits, schemaDecls: schemaDecls, anchors: anchors, reportable: reportable,
		analyzedFiles: fileSet, namespaces: namespaces, snapshotID: "sha256:" + hex.EncodeToString(digest[:]),
	}, nil
}

func engineSchemaUnit() (parse.SourceUnit, error) {
	return parse.Unit(strings.NewReader(`Package lintai!
Decl glob(Path, Pattern) descr [external(), mode('+', '+')].
Decl not_glob(Path, Pattern) descr [external(), mode('+', '+')].`))
}

func catalogForRule(catalog []parse.SourceUnit, rule parse.SourceUnit, roots []string) ([]parse.SourceUnit, map[string]struct{}) {
	type clauseRef struct {
		unitIndex   int
		clauseIndex int
		clause      ast.Clause
		pkg         string
	}
	byHead := make(map[string][]clauseRef)
	pkgNames := make([]string, len(catalog))
	for unitIndex, unit := range catalog {
		pkg := sourcePackageName(unit)
		pkgNames[unitIndex] = pkg
		for clauseIndex, clause := range unit.Clauses {
			byHead[qualifyPredicate(pkg, clause.Head.Predicate.Symbol)] = append(byHead[qualifyPredicate(pkg, clause.Head.Predicate.Symbol)], clauseRef{unitIndex: unitIndex, clauseIndex: clauseIndex, clause: clause, pkg: pkg})
		}
	}
	needed := make(map[string]struct{})
	pending := make([]string, 0)
	for _, clause := range rule.Clauses {
		for _, premise := range clause.Premises {
			switch typed := premise.(type) {
			case ast.Atom:
				pending = append(pending, qualifyPredicate("", typed.Predicate.Symbol))
			case ast.NegAtom:
				pending = append(pending, qualifyPredicate("", typed.Atom.Predicate.Symbol))
			}
		}
	}
	pending = append(pending, roots...)
	selected := make(map[[2]int]struct{})
	for len(pending) > 0 {
		name := pending[len(pending)-1]
		pending = pending[:len(pending)-1]
		if _, seen := needed[name]; seen {
			continue
		}
		needed[name] = struct{}{}
		for _, ref := range byHead[name] {
			selected[[2]int{ref.unitIndex, ref.clauseIndex}] = struct{}{}
			for _, premise := range ref.clause.Premises {
				switch typed := premise.(type) {
				case ast.Atom:
					pending = append(pending, qualifyPredicate(ref.pkg, typed.Predicate.Symbol))
				case ast.NegAtom:
					pending = append(pending, qualifyPredicate(ref.pkg, typed.Atom.Predicate.Symbol))
				}
			}
		}
	}
	result := make([]parse.SourceUnit, len(catalog))
	for i, unit := range catalog {
		result[i] = parse.SourceUnit{Decls: slices.Clone(unit.Decls)}
		for clauseIndex, clause := range unit.Clauses {
			if _, keep := selected[[2]int{i, clauseIndex}]; keep {
				result[i].Clauses = append(result[i].Clauses, clause)
			}
		}
	}
	return result, needed
}

func sourcePackageName(unit parse.SourceUnit) string {
	for _, declaration := range unit.Decls {
		if declaration.DeclaredAtom.Predicate != symbols.Package {
			continue
		}
		for _, description := range declaration.Descr {
			if description.Predicate.Symbol == "name" && len(description.Args) == 1 {
				if value, ok := description.Args[0].(ast.Constant); ok {
					name, _ := value.StringValue()
					return name
				}
			}
		}
	}
	return ""
}

func qualifyPredicate(namespace, symbol string) string {
	if namespace == "" || strings.Contains(symbol, ".") {
		return symbol
	}
	return namespace + "." + symbol
}

func namespacedUnit(source, namespace string) (parse.SourceUnit, error) {
	unit, err := parse.Unit(strings.NewReader(source))
	if err != nil {
		return parse.SourceUnit{}, err
	}
	set := false
	for i := range unit.Decls {
		if unit.Decls[i].DeclaredAtom.Predicate != symbols.Package {
			continue
		}
		unit.Decls[i].Descr = []ast.Atom{ast.NewAtom("name", ast.String(namespace))}
		set = true
		break
	}
	if !set {
		return parse.SourceUnit{}, fmt.Errorf("Mangle parser did not provide package metadata")
	}
	return unit, nil
}

func sameAnchor(left, right facts.AnchorSupport) bool {
	if left.Location != right.Location || left.Description != right.Description {
		return false
	}
	if (left.Tracking == nil) != (right.Tracking == nil) {
		return false
	}
	if left.Tracking == nil {
		return true
	}
	leftBytes, leftErr := canonical.Marshal([]any{left.Tracking.Scheme, left.Tracking.Data})
	rightBytes, rightErr := canonical.Marshal([]any{right.Tracking.Scheme, right.Tracking.Data})
	return leftErr == nil && rightErr == nil && string(leftBytes) == string(rightBytes)
}
