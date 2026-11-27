package engine

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"path/filepath"
	"reflect"
	"slices"
	"sort"
	"strings"
	"unicode/utf8"

	"codeberg.org/TauCeti/mangle-go/ast"
	"codeberg.org/TauCeti/mangle-go/factstore"
	"codeberg.org/TauCeti/mangle-go/parse"
	"codeberg.org/TauCeti/mangle-go/symbols"
	"github.com/bigbizze/lintai/backend"
	"github.com/bigbizze/lintai/facts"
	"github.com/bigbizze/lintai/internal/canonical"
	"github.com/bigbizze/lintai/observe"
)

type composedFacts struct {
	sourceDigests            map[string]string
	digestOwners             map[string]map[string]struct{}
	fileAliases              map[string]string
	base                     factstore.ReadOnlyFactStore
	anchors                  map[string]facts.AnchorSupport
	anchorOwners             map[string]string
	producerFiles            map[string]map[string]struct{}
	reportable               map[facts.Predicate]map[string]struct{}
	factCounts               map[ast.PredicateSym]int
	analyzedFiles            map[string]struct{}
	namespaces               []string
	snapshotID               string
	observations             observe.Set
	reusableFromObservations bool
}

type producerEntry struct {
	producer    backend.Producer
	description facts.ProducerDescription
	descriptor  facts.Descriptor
}

type producerCatalog struct {
	entries     []producerEntry
	sources     []parse.SourceUnit
	schemaDecls map[ast.PredicateSym]ast.Decl
	namespaces  []string
	identity    string
}

// An interface holding a nil producer or generation pointer is non-nil.
func nilImplementation(value any) bool {
	v := reflect.ValueOf(value)
	return !v.IsValid() || v.Kind() == reflect.Pointer && v.IsNil()
}

// bindProducers constructs the validated catalog without collecting any
// source facts. Rule programs use this view to determine the exact producer
// relations they need before extraction begins.
func bindProducers(producers []backend.Producer) (producerCatalog, error) {
	entries := make([]producerEntry, 0, len(producers))
	owned := make(map[string]struct{}, len(producers))
	for _, producer := range producers {
		if nilImplementation(producer) {
			return producerCatalog{}, fmt.Errorf("nil producer")
		}
		description, err := producer.Describe()
		if err != nil {
			return producerCatalog{}, fmt.Errorf("describe producer: %w", err)
		}
		if _, duplicate := owned[description.Namespace]; duplicate {
			return producerCatalog{}, fmt.Errorf("producer namespace %q is registered more than once", description.Namespace)
		}
		owned[description.Namespace] = struct{}{}
		descriptor, err := facts.ValidateDescription(description)
		if err != nil {
			return producerCatalog{}, fmt.Errorf("invalid producer description %q: %w", description.Namespace, err)
		}
		entries = append(entries, producerEntry{producer: producer, description: descriptor.Description(), descriptor: descriptor})
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].description.Namespace < entries[j].description.Namespace })

	var sourceUnits []parse.SourceUnit
	schemaDecls := make(map[ast.PredicateSym]ast.Decl)
	for _, entry := range entries {
		description := entry.description
		declarations, err := namespacedUnit(description.Schema.Declarations, description.Namespace)
		if err != nil {
			return producerCatalog{}, fmt.Errorf("producer %q declarations: %w", description.Namespace, err)
		}
		prelude, err := namespacedUnit(description.Schema.Prelude, description.Namespace)
		if err != nil {
			return producerCatalog{}, fmt.Errorf("producer %q prelude: %w", description.Namespace, err)
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
					return producerCatalog{}, fmt.Errorf("catalog declares %s more than once", key)
				}
				schemaDecls[key] = copy
			}
		}
	}
	engineUnit, err := engineSchemaUnit()
	if err != nil {
		return producerCatalog{}, fmt.Errorf("engine helper schema: %w", err)
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
	identity, err := catalogIdentity(entries, "")
	if err != nil {
		return producerCatalog{}, err
	}
	namespaces := make([]string, 0, len(entries))
	for _, entry := range entries {
		namespaces = append(namespaces, entry.description.Namespace)
	}
	return producerCatalog{
		entries: entries, sources: sourceUnits, schemaDecls: schemaDecls, namespaces: namespaces,
		identity: identity,
	}, nil
}

// The compiled plan uses the bound descriptions. Approval additionally resolves
// project path settings relative to the workspace owning the operation.
func catalogIdentity(entries []producerEntry, approvalRoot string) (string, error) {
	identityRecords := make([]any, 0, len(entries)+1)
	for _, entry := range entries {
		description := entry.description
		if approvalRoot != "" && description.ImplementationID == "lintai.typescript" {
			config, err := typeScriptPolicyConfig(approvalRoot, description.EffectiveConfiguration)
			if err != nil {
				return "", err
			}
			description.EffectiveConfiguration = config
		}
		identityRecords = append(identityRecords, description)
	}
	identityRecords = append(identityRecords, engineSchemaSource)
	identityRaw, err := json.Marshal(identityRecords)
	if err != nil {
		return "", fmt.Errorf("producer catalog identity: %w", err)
	}
	identityHash := sha256.Sum256(identityRaw)

	return hex.EncodeToString(identityHash[:]), nil
}

// Schema renders the configured catalog descriptions and engine helpers
// without collecting producer source facts.
func (e *Session) Schema(ctx context.Context) (string, error) {
	checkCtx, finish, err := e.beginCheck(ctx)
	if err != nil {
		return "", err
	}
	defer finish()
	if err := checkCtx.Err(); err != nil {
		return "", err
	}
	return e.catalog.schema(), nil
}

func (catalog producerCatalog) schema() string {
	var output strings.Builder
	for _, entry := range catalog.entries {
		if output.Len() > 0 {
			output.WriteString("\n\n")
		}
		fmt.Fprintf(&output, "Package %s!", entry.description.Namespace)
		for _, source := range []string{entry.description.Schema.Declarations, entry.description.Schema.Prelude} {
			if source = withoutPackageDeclaration(source); source != "" {
				output.WriteString("\n\n")
				output.WriteString(source)
			}
		}
	}
	if output.Len() > 0 {
		output.WriteString("\n\n")
	}
	output.WriteString(engineSchemaSource)
	return strings.TrimSpace(output.String())
}

func withoutPackageDeclaration(source string) string {
	lines := strings.Split(source, "\n")
	for index, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || strings.HasPrefix(trimmed, "#") || strings.HasPrefix(trimmed, "//") {
			continue
		}
		if strings.HasPrefix(trimmed, "Package ") && strings.HasSuffix(trimmed, "!") {
			lines = append(lines[:index], lines[index+1:]...)
		}
		break
	}
	return strings.TrimSpace(strings.Join(lines, "\n"))
}

// collect prepares selected generations, establishes identity, then emits only on a miss.
func (catalog producerCatalog) collect(ctx context.Context, options Options, requestedByNamespace map[string]map[facts.Predicate]struct{}, current *composedFacts, mode observe.Mode) (composedFacts, error) {
	type prepared struct {
		entry      producerEntry
		requested  []facts.Predicate
		generation backend.Generation
		metadata   backend.GenerationMetadata
	}
	preparedGenerations := make([]prepared, 0, len(catalog.entries))
	records := make([]any, 0, len(catalog.entries))
	sourceDigests := make(map[string]string)
	digestOwners := make(map[string]map[string]struct{})
	fileAliases := make(map[string]string)
	var observations []observe.Set
	reusable := true
	for _, entry := range catalog.entries {
		description := entry.description
		wanted := requestedByNamespace[description.Namespace]
		if len(wanted) == 0 {
			continue
		}
		requested := make([]facts.Predicate, 0, len(wanted))
		for _, relation := range description.BaseRelations {
			if _, ok := wanted[relation.Predicate]; ok {
				requested = append(requested, relation.Predicate)
			}
		}
		if len(requested) != len(wanted) {
			return composedFacts{}, fmt.Errorf("requested facts include an unknown base relation for producer %q", description.Namespace)
		}
		sort.Slice(requested, func(i, j int) bool {
			if requested[i].Name != requested[j].Name {
				return requested[i].Name < requested[j].Name
			}
			return requested[i].Arity < requested[j].Arity
		})
		generation, err := entry.producer.Prepare(ctx, backend.CollectionRequest{RepoRoot: options.RepoRoot, WorkspaceRoot: options.WorkspaceRoot, Predicates: slices.Clone(requested)}, mode)
		if err != nil {
			return composedFacts{}, fmt.Errorf("prepare producer %q: %w", description.Namespace, err)
		}
		if nilImplementation(generation) {
			return composedFacts{}, fmt.Errorf("producer %q returned a nil generation", description.Namespace)
		}
		metadata, err := captureGenerationMetadata(generation.Metadata(), options.WorkspaceRoot, description.Namespace, sourceDigests, digestOwners)
		if err != nil {
			return composedFacts{}, fmt.Errorf("producer %q generation metadata: %w", description.Namespace, err)
		}
		observations = append(observations, metadata.Observations)
		for alias, file := range metadata.FileAliases {
			alias, file = publicationFileKey(options.WorkspaceRoot, alias), publicationFileKey(options.WorkspaceRoot, file)
			if previous, exists := fileAliases[alias]; exists && previous != file {
				return composedFacts{}, fmt.Errorf("producer %q names %q as both %q and %q", description.Namespace, alias, previous, file)
			}
			fileAliases[alias] = file
		}
		reusable = reusable && metadata.ReusableFromObservations
		predicates := make([]any, len(requested))
		for i, predicate := range requested {
			predicates[i] = []any{predicate.Name, predicate.Arity}
		}
		records = append(records, []any{description.Namespace, metadata.InputRevision, metadata.Scope, predicates})
		preparedGenerations = append(preparedGenerations, prepared{entry, requested, generation, metadata})
	}
	encoded, err := canonical.MarshalStructured([]any{"lintai-facts-v4", catalog.identity, records})
	if err != nil {
		return composedFacts{}, fmt.Errorf("snapshot identity: %w", err)
	}
	digest := sha256.Sum256(encoded)
	snapshotID := "sha256:" + hex.EncodeToString(digest[:])
	captured := observe.Union(observations...)
	if current != nil && current.snapshotID == snapshotID {
		reused := *current
		reused.sourceDigests, reused.digestOwners, reused.fileAliases = sourceDigests, digestOwners, fileAliases
		reused.observations, reused.reusableFromObservations = captured, reusable
		return reused, nil
	}
	base := factstore.NewMultiIndexedArrayInMemoryStore()
	anchors := make(map[string]facts.AnchorSupport)
	anchorOwners := make(map[string]string)
	producerFiles := make(map[string]map[string]struct{})
	reportable := make(map[facts.Predicate]map[string]struct{})
	factCounts := make(map[ast.PredicateSym]int)
	analyzedFiles := make(map[string]struct{})
	namespaces := make([]string, 0, len(preparedGenerations))
	for _, item := range preparedGenerations {
		namespace := item.entry.description.Namespace
		builder, err := facts.NewBuilder(item.entry.descriptor, item.requested)
		if err != nil {
			return composedFacts{}, fmt.Errorf("producer %q demand: %w", namespace, err)
		}
		if err := item.generation.Emit(ctx, builder); err != nil {
			return composedFacts{}, fmt.Errorf("emit producer %q: %w", namespace, err)
		}
		validated, err := builder.Finish()
		if err != nil {
			return composedFacts{}, fmt.Errorf("producer %q returned invalid facts: %w", namespace, err)
		}
		if err := ctx.Err(); err != nil {
			return composedFacts{}, err
		}
		namespaces = append(namespaces, namespace)
		for _, predicate := range item.requested {
			factCounts[ast.PredicateSym{Symbol: predicate.Name, Arity: predicate.Arity}] = validated.Count(predicate)
			reportable[predicate] = validated.Reportable(predicate)
			for _, atom := range validated.Atoms(predicate) {
				base.Add(atom)
			}
		}
		for id, support := range validated.Anchors() {
			anchors[id] = support
			anchorOwners[id] = namespace
		}
		producerFiles[namespace] = make(map[string]struct{})
		for _, file := range item.metadata.AnalyzedFiles {
			analyzedFiles[file] = struct{}{}
			producerFiles[namespace][file] = struct{}{}
		}
	}
	return composedFacts{sourceDigests: sourceDigests, digestOwners: digestOwners, fileAliases: fileAliases, base: base, anchors: anchors, anchorOwners: anchorOwners, producerFiles: producerFiles, reportable: reportable, factCounts: factCounts, analyzedFiles: analyzedFiles, namespaces: namespaces, snapshotID: snapshotID, observations: captured, reusableFromObservations: reusable}, nil
}

// captureGenerationMetadata records each source digest together with the
// producer that captured it, so projection can verify a range only against
// text its own producer observed.
func captureGenerationMetadata(metadata backend.GenerationMetadata, root, namespace string, sourceDigests map[string]string, digestOwners map[string]map[string]struct{}) (backend.GenerationMetadata, error) {
	if metadata.InputRevision == "" {
		return backend.GenerationMetadata{}, fmt.Errorf("input revision is empty")
	}
	scope, err := canonical.NormalizeStructured(metadata.Scope)
	if err != nil {
		return backend.GenerationMetadata{}, fmt.Errorf("scope: %w", err)
	}
	metadata.Scope = scope
	for file, digest := range metadata.SourceDigests {
		file, err := generationFilePath(file)
		if err != nil {
			return backend.GenerationMetadata{}, err
		}
		if _, err := hex.DecodeString(digest); err != nil || len(digest) != 2*sha256.Size || digest != strings.ToLower(digest) {
			return backend.GenerationMetadata{}, fmt.Errorf("invalid source digest for %q", file)
		}
		key := publicationFileKey(root, file)
		if previous, exists := sourceDigests[key]; exists && previous != digest {
			return backend.GenerationMetadata{}, fmt.Errorf("conflicting source identities for %q", key)
		}
		sourceDigests[key] = digest
		if digestOwners[key] == nil {
			digestOwners[key] = make(map[string]struct{})
		}
		digestOwners[key][namespace] = struct{}{}
	}
	// Digests now belong to the composition. Drop the producer-owned map.
	metadata.SourceDigests = nil
	files := make([]string, 0, len(metadata.AnalyzedFiles))
	for _, file := range metadata.AnalyzedFiles {
		file, err := generationFilePath(file)
		if err != nil {
			return backend.GenerationMetadata{}, err
		}
		files = append(files, file)
	}
	sort.Strings(files)
	metadata.AnalyzedFiles = slices.Compact(files)
	return metadata, nil
}

func generationFilePath(file string) (string, error) {
	clean := filepath.Clean(filepath.FromSlash(file))
	if !utf8.ValidString(file) || strings.ContainsRune(file, 0) || clean == filepath.Dir(clean) {
		return "", fmt.Errorf("invalid generation file path %q", file)
	}
	return filepath.ToSlash(clean), nil
}

const engineSchemaSource = `Package lintai!
Decl glob(Path, Pattern) descr [external(), mode('+', '+')].
Decl not_glob(Path, Pattern) descr [external(), mode('+', '+')].`

func engineSchemaUnit() (parse.SourceUnit, error) {
	return parse.Unit(strings.NewReader(engineSchemaSource))
}

func catalogForRule(catalog []parse.SourceUnit, rule parse.SourceUnit, roots []string) ([]parse.SourceUnit, map[string]struct{}) {
	type clauseRef struct {
		unitIndex   int
		clauseIndex int
		clause      ast.Clause
		pkg         string
	}
	byHead := make(map[string][]clauseRef)
	for unitIndex, unit := range catalog {
		pkg := sourcePackageName(unit)
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
