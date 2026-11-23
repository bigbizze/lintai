package typescript

import (
	"context"
	"fmt"
	"io"
	"path/filepath"
	"slices"
	"strings"
	"sync"

	"github.com/bigbizze/lintai/backend"
	"github.com/bigbizze/lintai/facts"
	"github.com/bigbizze/lintai/internal/ambient"
	"github.com/bigbizze/lintai/internal/tsgo/lintaiapi"
	"github.com/bigbizze/lintai/internal/workspace"
	"github.com/bigbizze/lintai/observe"
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
	plan                    *workspace.ScopePlan
	observations            observe.Set
	inputRevision           string
	scope                   map[string]any
	description             facts.ProducerDescription
	sources                 []ambient.Source
	adjustments             []lintaiapi.ConfigAdjustment
}

var _ backend.Producer = (*Backend)(nil)

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
	description, err := facts.DescribeSchema(b.namespace, "lintai.typescript", "typescript-producer-owner-context-1", "ts-vocabulary/3", configuration, Schema())
	if err != nil {
		return facts.ProducerDescription{}, err
	}
	// Ambient names come from the same registry used to extract ambient facts.
	var ambientSources, ambientCapabilities []string
	for _, source := range ambient.Builtin() {
		ambientSources = append(ambientSources, source.ID)
		for _, matcher := range source.Matchers {
			ambientCapabilities = append(ambientCapabilities, matcher.Capability)
		}
	}
	ambientSources = sortedUnique(ambientSources)
	ambientCapabilities = sortedUnique(ambientCapabilities)
	// Package is a reserved Mangle token, so the declaration uses Pkg as its
	// variable while the public catalog retains Appendix A's argument name.
	for index := range description.BaseRelations {
		relation := &description.BaseRelations[index]
		switch relation.Predicate.Name {
		case b.namespace + ".import_package":
			relation.Arguments[1].Name = "Package"
		case b.namespace + ".ambient_source", b.namespace + ".ambient_use":
			for index := range relation.Arguments {
				argument := &relation.Arguments[index]
				switch argument.Name {
				case "Source":
					argument.AllowedNames = slices.Clone(ambientSources)
				case "Capability":
					argument.AllowedNames = slices.Clone(ambientCapabilities)
				}
			}
		}
	}
	return description, nil
}

func (b *Backend) Prepare(ctx context.Context, request backend.CollectionRequest, mode observe.Mode) (backend.Generation, error) {
	b.collectMu.Lock()
	defer b.collectMu.Unlock()
	if mode != observe.Full && mode != observe.Quick {
		return nil, fmt.Errorf("unknown observation mode %d", mode)
	}
	description, err := b.Describe()
	if err != nil {
		return nil, err
	}
	available := make(map[facts.Predicate]struct{}, len(description.BaseRelations))
	for _, relation := range description.BaseRelations {
		available[relation.Predicate] = struct{}{}
	}
	requested := make(map[facts.Predicate]struct{}, len(request.Predicates))
	relations := make([]string, 0, len(request.Predicates))
	for _, predicate := range request.Predicates {
		if _, ok := available[predicate]; !ok {
			return nil, fmt.Errorf("unknown TypeScript base predicate %s", predicate)
		}
		if _, ok := requested[predicate]; ok {
			return nil, fmt.Errorf("duplicate requested TypeScript predicate %s", predicate)
		}
		requested[predicate] = struct{}{}
		relations = append(relations, strings.TrimPrefix(predicate.Name, b.namespace+"."))
	}
	slices.Sort(relations)
	repoRoot, err := filepath.Abs(request.RepoRoot)
	if err != nil {
		return nil, err
	}
	workspaceRoot, err := filepath.Abs(request.WorkspaceRoot)
	if err != nil {
		return nil, err
	}
	return b.generation(ctx, filepath.Clean(repoRoot), filepath.Clean(workspaceRoot), relations, description, mode)
}

func (g *producerGeneration) Metadata() backend.GenerationMetadata {
	files := make([]string, 0, len(g.snapshot.Modules))
	for _, module := range g.snapshot.Modules {
		files = append(files, module.Path)
	}
	return backend.GenerationMetadata{InputRevision: g.inputRevision, Scope: cloneProducerValue(g.scope), AnalyzedFiles: files, Observations: g.observations, ReusableFromObservations: true}
}

func (g *producerGeneration) Emit(ctx context.Context, builder *facts.Builder) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	return emitSnapshot(ctx, builder, g.description, g.snapshot, g.sources, g.adjustments, g.description.Namespace)
}

func (b *Backend) Close() error {
	b.collectMu.Lock()
	defer b.collectMu.Unlock()
	b.current = nil
	return nil
}

// generation is called with collectMu held. A changed relation demand needs a
// new extraction. The prior
// snapshot only supplies tracking keys whose captured source bytes still match.
func (b *Backend) generation(ctx context.Context, repoRoot, workspaceRoot string, relations []string, description facts.ProducerDescription, mode observe.Mode) (*producerGeneration, error) {
	var previousSnapshot *lintaiapi.Snapshot
	if b.current != nil && b.current.repoRoot == repoRoot && b.current.workspaceRoot == workspaceRoot {
		previousSnapshot = b.current.snapshot
		if slices.Equal(relations, b.current.relations) {
			valid, err := b.validateGeneration(ctx, b.current, mode)
			if err != nil {
				b.current = nil
				return nil, err
			}
			if valid {
				return b.current, nil
			}
		}
	}
	b.current = nil
	generation, sources, err := b.extractSnapshot(ctx, workspaceRoot, relations, previousSnapshot)
	if err != nil {
		return nil, err
	}
	generation.sources = sources
	generation.description = description
	generation.adjustments = convertAdjustments(generation.plan.Root, generation.snapshot.ConfigAdjustments)
	b.reportAdjustments(generation.adjustments)
	generation.repoRoot, generation.workspaceRoot = repoRoot, workspaceRoot
	generation.relations = slices.Clone(relations)
	b.current = generation
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

func sortedUnique(values []string) []string {
	result := slices.Clone(values)
	slices.Sort(result)
	return slices.Compact(result)
}

func convertAdjustments(workspaceRoot string, items []lintaiapi.ConfigAdjustment) []lintaiapi.ConfigAdjustment {
	result := slices.Clone(items)
	for index := range result {
		result[index].Config = lintaiapi.PublicConfigPath(workspaceRoot, result[index].Config)
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
