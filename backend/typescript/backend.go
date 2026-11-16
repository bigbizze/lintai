package typescript

import (
	"context"
	"fmt"
	"io"
	"slices"
	"strings"
	"sync"

	"github.com/bigbizze/lintai/backend"
	"github.com/bigbizze/lintai/diagnostics"
	"github.com/bigbizze/lintai/facts"
	"github.com/bigbizze/lintai/internal/ambient"
	"github.com/bigbizze/lintai/internal/tsgo/lintaiapi"
	"github.com/bigbizze/lintai/internal/workspace"
)

// Backend extracts facts from TypeScript and JavaScript files.
type Backend struct {
	settings  Options
	namespace string

	// Log receives one line for each tsconfig that lintai adjusted for
	// TypeScript 7, once per config and adjustment set. Nil discards them.
	Log io.Writer

	reportedMu sync.Mutex
	reported   map[string]string
	collectMu  sync.Mutex
	current    *producerGeneration
}

type producerGeneration struct {
	repoRoot, workspaceRoot string
	relations               []string
	snapshot                *lintaiapi.Snapshot
	items                   []facts.Fact
}

var _ backend.Producer = (*Backend)(nil)

func producerEntityID(localID string) string { return producerEntityIDIn("ts", localID) }

func producerEntityIDIn(namespace, localID string) string { return namespace + ":" + localID }

func New(settings ...Options) *Backend {
	selected := Options{Files: FileScopeTsconfig}
	if len(settings) > 0 {
		selected = settings[0]
		if scope, err := ParseFileScope(string(selected.Files)); err == nil {
			selected.Files = scope
		}
		selected.WorkspaceExcludes = slices.Clone(selected.WorkspaceExcludes)
		selected.Includes = slices.Clone(selected.Includes)
		selected.Sources = slices.Clone(selected.Sources)
		selected.Excludes = slices.Clone(selected.Excludes)
	}
	if selected.Namespace == "" {
		selected.Namespace = "ts"
	}
	return &Backend{settings: selected, namespace: selected.Namespace}
}

func (b *Backend) Describe() (facts.ProducerDescription, error) {
	configuration := map[string]any{
		"workspaceExclude": sortedUnique(b.settings.WorkspaceExcludes),
		"include":          sortedUnique(b.settings.Includes),
		"sources":          sortedUnique(b.settings.Sources),
		"exclude":          sortedUnique(b.settings.Excludes),
		"files":            string(b.settings.Files),
		"sourceLinks":      b.settings.SourceLinks,
	}
	description, err := facts.DescribeSchema(b.namespace, "lintai.typescript", "typescript-producer-semantic-ownership-1", "ts-vocabulary/2", configuration, Schema())
	if err != nil {
		return facts.ProducerDescription{}, err
	}
	// Package is a reserved Mangle token, so the declaration uses Pkg as its
	// variable while the public catalog retains Appendix A's argument name.
	for index := range description.BaseRelations {
		if description.BaseRelations[index].Predicate.Name == b.namespace+".import_package" {
			description.BaseRelations[index].Arguments[1].Name = "Package"
		}
	}
	return description, nil
}

func (b *Backend) Collect(ctx context.Context, request backend.CollectionRequest) (facts.Batch, error) {
	b.collectMu.Lock()
	defer b.collectMu.Unlock()
	batch := facts.Batch{Relations: make(map[facts.Predicate][]facts.Row), Anchors: make(map[string]facts.AnchorSupport)}
	if len(request.Predicates) == 0 {
		return batch, nil
	}
	description, err := b.Describe()
	if err != nil {
		return facts.Batch{}, err
	}
	requested := make(map[facts.Predicate]struct{}, len(request.Predicates))
	for _, predicate := range request.Predicates {
		found := false
		for _, relation := range description.BaseRelations {
			if relation.Predicate == predicate {
				found = true
				break
			}
		}
		if !found {
			return facts.Batch{}, fmt.Errorf("unknown TypeScript base predicate %s", predicate)
		}
		if _, exists := requested[predicate]; exists {
			return facts.Batch{}, fmt.Errorf("duplicate requested TypeScript predicate %s", predicate)
		}
		requested[predicate] = struct{}{}
	}
	relations := make([]string, 0, len(requested))
	for predicate := range requested {
		relations = append(relations, strings.TrimPrefix(predicate.Name, b.namespace+"."))
	}
	slices.Sort(relations)
	generation, err := b.generation(ctx, request.RepoRoot, request.WorkspaceRoot, relations)
	if err != nil {
		return facts.Batch{}, err
	}
	snapshot, items := generation.snapshot, generation.items
	metadata := make(map[facts.Predicate]facts.RelationDescription, len(description.BaseRelations))
	for _, relation := range description.BaseRelations {
		metadata[relation.Predicate] = relation
	}
	for predicate := range requested {
		batch.Relations[predicate] = []facts.Row{}
	}
	for _, item := range items {
		predicate := facts.Predicate{Name: b.namespace + "." + item.Relation, Arity: len(item.Values)}
		if _, wanted := requested[predicate]; !wanted {
			continue
		}
		values := slices.Clone(item.Values)
		for index, argument := range metadata[predicate].Arguments {
			if argument.Role != facts.EntityRole {
				continue
			}
			if values[index].Kind != facts.StringKind {
				return facts.Batch{}, fmt.Errorf("TypeScript entity %s argument %s is not a string", predicate, argument.Name)
			}
			id := values[index].String
			values[index].String = producerEntityIDIn(b.namespace, id)
		}
		batch.Relations[predicate] = append(batch.Relations[predicate], facts.Row(values))
	}
	locationPredicate := facts.Predicate{Name: b.namespace + ".location", Arity: 6}
	if _, ok := metadata[locationPredicate]; ok {
		descriptions := make(map[string]string)
		for _, fn := range snapshot.Functions {
			description := snapshot.Tracking[fn.SemanticKey].Description
			if strings.HasPrefix(description, "function") {
				description = "signature" + strings.TrimPrefix(description, "function")
			} else if description != "" {
				description = "signature " + description
			} else {
				description = "signature"
			}
			for _, observation := range fn.SignatureObservations {
				descriptions[lintaiapi.SignatureID(fn.SemanticKey, observation, b.namespace)] = description
			}
		}
		for _, item := range snapshot.Types {
			if item.Description != "" {
				descriptions[item.ID] = item.Description
			}
		}
		for _, item := range items {
			if item.Relation != facts.LocationRelation || len(item.Values) != 6 {
				continue
			}
			id := producerEntityIDIn(b.namespace, item.Values[0].String)
			file := item.Values[1].String
			if strings.HasPrefix(file, "bundled://") {
				continue
			}
			toInt := func(value facts.Value) int { return int(value.Number) }
			support := snapshot.Tracking[item.Values[0].String]
			description := support.Description
			if description == "" {
				description = descriptions[item.Values[0].String]
			}
			if description == "" {
				description = "TypeScript " + strings.SplitN(item.Values[0].String, ":", 2)[0]
			}
			anchor := facts.AnchorSupport{
				Location:    diagnostics.SourceLocation{File: file, StartLine: toInt(item.Values[2]), StartColumn: toInt(item.Values[3]), EndLine: toInt(item.Values[4]), EndColumn: toInt(item.Values[5])},
				Description: description,
			}
			if support.Data != nil {
				anchor.Tracking = &facts.TrackingKey{Scheme: "lintai.typescript.subject/1", Data: cloneProducerValue(support.Data)}
			}
			batch.Anchors[id] = anchor
		}
	}
	for _, module := range snapshot.Modules {
		batch.AnalyzedFiles = append(batch.AnalyzedFiles, module.Path)
	}
	batch.InputRevision = snapshot.InputRevision
	batch.Scope = cloneProducerValue(snapshot.Scope)
	batch.WatchHints = slices.Clone(snapshot.WatchHints)
	return batch, nil
}

func (b *Backend) Close() error {
	b.collectMu.Lock()
	defer b.collectMu.Unlock()
	b.current = nil
	return nil
}

// BuildSnapshot extracts the facts of the source files under workspaceRoot.
func (b *Backend) BuildSnapshot(ctx context.Context, repoRoot, workspaceRoot string) (facts.Snapshot, error) {
	b.collectMu.Lock()
	defer b.collectMu.Unlock()
	generation, err := b.generation(ctx, repoRoot, workspaceRoot, nil)
	if err != nil {
		return facts.Snapshot{}, err
	}
	snapshot := generation.snapshot
	items := make([]facts.Fact, len(generation.items))
	for index, item := range generation.items {
		items[index] = facts.Fact{Relation: item.Relation, Values: slices.Clone(item.Values)}
	}
	inputPaths := make([]string, 0, len(snapshot.Inputs))
	for _, input := range snapshot.Inputs {
		inputPaths = append(inputPaths, input.Path)
	}
	return facts.Snapshot{Version: snapshot.InputRevision, InputRevision: snapshot.InputRevision, Scope: cloneProducerValue(snapshot.Scope), Schema: Schema(), Facts: items, InputPaths: inputPaths, WatchHints: slices.Clone(snapshot.WatchHints)}, nil
}

// generation is called with collectMu held. A changed relation demand needs a
// new extraction, whose own validation covers the new inputs. The prior
// snapshot only supplies tracking keys whose captured source bytes still match.
func (b *Backend) generation(ctx context.Context, repoRoot, workspaceRoot string, relations []string) (*producerGeneration, error) {
	var previousSnapshot *lintaiapi.Snapshot
	var previousValidation *lintaiapi.InputValidation
	if b.current != nil && b.current.repoRoot == repoRoot && b.current.workspaceRoot == workspaceRoot {
		previousSnapshot = b.current.snapshot
		if (relations == nil) == (b.current.relations == nil) && slices.Equal(relations, b.current.relations) {
			valid, validation, err := previousSnapshot.Validity.Validate(ctx)
			if err != nil {
				b.current = nil
				return nil, err
			}
			if valid {
				return b.current, nil
			}
			previousValidation = validation
		}
	}
	b.current = nil
	snapshot, sources, _, err := b.extractSnapshot(ctx, workspaceRoot, relations, previousSnapshot, previousValidation)
	if err != nil {
		return nil, err
	}
	adjustments := convertAdjustments(workspaceRoot, snapshot.ConfigAdjustments)
	b.reportAdjustments(adjustments)
	items, err := factsFromSnapshot(snapshot, sources, adjustments, b.namespace)
	if err != nil {
		return nil, err
	}
	b.current = &producerGeneration{repoRoot: repoRoot, workspaceRoot: workspaceRoot, relations: slices.Clone(relations), snapshot: snapshot, items: items}
	return b.current, nil
}

// cloneProducerValue detaches the JSON-shaped scope and tracking data that
// callers receive from the producer's retained generation.
func cloneProducerValue(value any) any {
	switch value := value.(type) {
	case map[string]any:
		clone := make(map[string]any, len(value))
		for key, item := range value {
			clone[key] = cloneProducerValue(item)
		}
		return clone
	case []any:
		clone := make([]any, len(value))
		for index, item := range value {
			clone[index] = cloneProducerValue(item)
		}
		return clone
	case []string:
		return slices.Clone(value)
	default:
		return value
	}
}

func (b *Backend) buildLintaiSnapshot(ctx context.Context, repoRoot, workspaceRoot string) (*lintaiapi.Snapshot, []ambient.Source, string, error) {
	b.collectMu.Lock()
	defer b.collectMu.Unlock()
	return b.extractSnapshot(ctx, workspaceRoot, nil, nil, nil)
}

func (b *Backend) extractSnapshot(ctx context.Context, workspaceRoot string, relations []string, previousSnapshot *lintaiapi.Snapshot, previousValidation *lintaiapi.InputValidation) (*lintaiapi.Snapshot, []ambient.Source, string, error) {
	options := b.settings
	scope, err := ParseFileScope(string(options.Files))
	if err != nil {
		return nil, nil, "", err
	}
	sources := ambient.Builtin()
	scopeOptions := workspace.ScopeOptions{
		Exclude: options.WorkspaceExcludes, Include: options.Includes,
		Sources: options.Sources, InvocationExclude: options.Excludes,
	}
	extracted, err := lintaiapi.BuildSnapshotReusingTracking(ctx, lintaiapi.BuildSnapshotRequest{
		WorkspaceRoot:      workspaceRoot,
		ScopeOptions:       &scopeOptions,
		ConfigPath:         options.ConfigPath,
		ConfigDigest:       options.ConfigDigest,
		ConfigPresent:      options.ConfigPresent,
		SourceLinks:        options.SourceLinks,
		AmbientMatchers:    convertAmbientMatchers(sources),
		RequestedRelations: relations,
		PreviousValidation: previousValidation,
		TsconfigFiles:      scope == FileScopeTsconfig,
	}, previousSnapshot)
	if err != nil {
		return nil, nil, "", err
	}
	return extracted, sources, extracted.InputRevision, nil
}

func sortedUnique(values []string) []string {
	result := slices.Clone(values)
	slices.Sort(result)
	return slices.Compact(result)
}

// moduleFiles returns the walked files that are modules of the snapshot.
func moduleFiles(workspaceRoot string, walked []string, modules []lintaiapi.Module) []string {
	paths := make(map[string]struct{}, len(modules))
	for _, item := range modules {
		paths[item.Path] = struct{}{}
	}
	result := make([]string, 0, len(modules))
	for _, file := range walked {
		if _, ok := paths[workspace.RelativePath(workspaceRoot, file)]; ok {
			result = append(result, file)
		}
	}
	return result
}

func convertAdjustments(workspaceRoot string, items []lintaiapi.ConfigAdjustment) []lintaiapi.ConfigAdjustment {
	result := slices.Clone(items)
	for index := range result {
		result[index].Config = workspace.RelativePath(workspaceRoot, result[index].Config)
	}
	return result
}

// reportAdjustments writes one line per adjusted tsconfig to b.Log, for
// example "lintai: adjusted tsconfig.json for TypeScript 7 (baseUrl
// rewritten, target removed)". A line that b.Log already received for the
// config is not written again, so lintai serve does not repeat it on every
// refresh. The adjustments are sorted by config.
func (b *Backend) reportAdjustments(adjustments []lintaiapi.ConfigAdjustment) {
	if b.Log == nil {
		return
	}
	b.reportedMu.Lock()
	defer b.reportedMu.Unlock()
	if b.reported == nil {
		b.reported = make(map[string]string)
	}
	for start := 0; start < len(adjustments); {
		end := start
		changes := make([]string, 0)
		for end < len(adjustments) && adjustments[end].Config == adjustments[start].Config {
			changes = append(changes, adjustments[end].Option+" "+adjustments[end].Action)
			end++
		}
		config := adjustments[start].Config
		line := fmt.Sprintf("lintai: adjusted %s for TypeScript 7 (%s)\n", config, strings.Join(changes, ", "))
		if b.reported[config] != line {
			b.reported[config] = line
			_, _ = io.WriteString(b.Log, line)
		}
		start = end
	}
}

func convertAmbientMatchers(sources []ambient.Source) []lintaiapi.AmbientMatcher {
	result := make([]lintaiapi.AmbientMatcher, 0)
	for _, source := range sources {
		for _, matcher := range source.Matchers {
			modules := make([]lintaiapi.AmbientModuleBinding, 0, len(matcher.Modules))
			for _, binding := range matcher.Modules {
				modules = append(modules, lintaiapi.AmbientModuleBinding{
					Specifier: binding.Specifier,
					Export:    binding.Export,
					Path:      append([]string(nil), binding.Path...),
				})
			}
			result = append(result, lintaiapi.AmbientMatcher{
				Source:     source.ID,
				Capability: matcher.Capability,
				Root:       matcher.Root,
				Path:       append([]string(nil), matcher.Path...),
				Modules:    modules,
				Form:       string(matcher.Form),
				ArgCount:   matcher.ArgCount,
				KeyMember:  matcher.Key == ambient.KeyMember,
			})
		}
	}
	return result
}
