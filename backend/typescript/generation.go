package typescript

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"path/filepath"
	"slices"
	"strings"

	"github.com/bigbizze/lintai/internal/ambient"
	"github.com/bigbizze/lintai/internal/canonical"
	"github.com/bigbizze/lintai/internal/tsgo/lintaiapi"
	"github.com/bigbizze/lintai/internal/workspace"
	"github.com/bigbizze/lintai/observe"
)

// validateGeneration checks exactly the observations captured for this generation.
func (b *Backend) validateGeneration(ctx context.Context, generation *producerGeneration, mode observe.Mode) (bool, error) {
	changed, err := generation.observations.Changed(ctx, mode)
	return changed == nil && err == nil, err
}

func (b *Backend) extractSnapshot(ctx context.Context, workspaceRoot string, relations []string, previous *lintaiapi.Snapshot) (*producerGeneration, []ambient.Source, error) {
	scope, err := ParseFileScope(string(b.settings.Files))
	if err != nil {
		return nil, nil, err
	}
	sources := ambient.Builtin()
	options := workspace.ScopeOptions{Exclude: b.settings.WorkspaceExcludes, Include: b.settings.Includes, Sources: b.settings.Sources, InvocationExclude: b.settings.Excludes}
	for attempt := 0; attempt < 2; attempt++ {
		plan, err := workspace.PlanSources(workspaceRoot, options)
		if err != nil {
			return nil, nil, err
		}
		sourceLinks := b.settings.SourceLinks
		if filepath.IsAbs(sourceLinks) {
			requestedRoot, err := filepath.Abs(workspaceRoot)
			if err != nil {
				return nil, nil, err
			}
			relative := relativeScopePath(requestedRoot, sourceLinks)
			if !filepath.IsAbs(filepath.FromSlash(relative)) {
				sourceLinks = filepath.Join(plan.Root, filepath.FromSlash(relative))
			}
		}
		snapshot, err := lintaiapi.BuildSnapshotReusingTracking(ctx, lintaiapi.BuildSnapshotRequest{
			WorkspaceRoot: plan.Root, Files: plan.Files, ExactFiles: plan.ExactFiles,
			SourceLinks: sourceLinks, AmbientMatchers: convertAmbientMatchers(sources),
			RequestedRelations: relations, TsconfigFiles: scope == FileScopeTsconfig,
		}, previous)
		if err != nil {
			return nil, nil, err
		}
		generation := &producerGeneration{snapshot: snapshot, plan: plan}
		if err := b.finishGeneration(generation, workspaceRoot, scope == FileScopeTsconfig); err != nil {
			return nil, nil, err
		}
		if b.beforeValidation != nil {
			b.beforeValidation()
		}
		stable, err := b.validateGeneration(ctx, generation, observe.Full)
		if err != nil {
			return nil, nil, err
		}
		if stable {
			return generation, sources, nil
		}
	}
	return nil, nil, fmt.Errorf("inputs_changed: compiler inputs or source membership changed during collection")
}

func compilerObservations(inputs []lintaiapi.CompilerObservation) (observe.Set, error) {
	records := make([]observe.Record, 0, len(inputs))
	for _, input := range inputs {
		record := observe.Record{Kind: observe.Kind(input.Kind), Path: filepath.Clean(input.Path), Op: observe.Operation(input.Op), Value: observe.Value(input.Value), Entries: input.Entries}
		if input.Stamp != nil {
			record.Stamp = &observe.Stamp{Size: fmt.Sprint(input.Stamp.Size), MtimeNs: fmt.Sprint(input.Stamp.MtimeNs), ObservedAtNs: fmt.Sprint(input.Stamp.ObservedAtNs)}
		}
		records = append(records, record)
	}
	return observe.New(records)
}

func (b *Backend) finishGeneration(generation *producerGeneration, workspaceRoot string, tsconfigFiles bool) error {
	snapshot, plan := generation.snapshot, generation.plan
	compilerSet, err := compilerObservations(snapshot.CompilerInputs)
	if err != nil {
		return fmt.Errorf("compiler observations: %w", err)
	}
	generation.observations = observe.Union(compilerSet, plan.Observations)
	if b.settings.ConfigPath != "" {
		configPath, err := filepath.Abs(b.settings.ConfigPath)
		if err != nil {
			return err
		}
		configRecord := observe.Record{Kind: observe.State, Path: filepath.Clean(configPath), Op: observe.ReadFile, Value: observe.Missing}
		if b.settings.ConfigPresent {
			configRecord = observe.Record{Kind: observe.Content, Path: filepath.Clean(configPath), Value: observe.Value(b.settings.ConfigDigest)}
		}
		configSet, err := observe.New([]observe.Record{configRecord})
		if err != nil {
			return err
		}
		generation.observations = observe.Union(generation.observations, configSet)
	}
	requestedRoot, err := filepath.Abs(workspaceRoot)
	if err != nil {
		return err
	}
	revision, err := generation.observations.Digest(filepath.Clean(requestedRoot))
	if err != nil {
		return fmt.Errorf("input revision: %w", err)
	}
	// Compiler observations use the resolved plan root. Keep in-workspace
	// input paths in the spelling supplied by the caller, including symlinked
	// workspace roots.
	for i := range snapshot.Inputs {
		relative := relativeScopePath(plan.Root, snapshot.Inputs[i].Path)
		if !filepath.IsAbs(filepath.FromSlash(relative)) {
			snapshot.Inputs[i].Path = filepath.ToSlash(filepath.Join(requestedRoot, filepath.FromSlash(relative)))
		}
	}
	snapshot.Inputs = uniqueSnapshotInputs(snapshot.Inputs)
	description, err := b.Describe()
	if err != nil {
		return err
	}
	revision = combineInputRevisions(revision, plan.Revision, description.EffectiveConfiguration)
	generation.scope = scopeIdentity(plan, snapshot, tsconfigFiles)
	generation.inputRevision = revision
	return nil
}

func uniqueSnapshotInputs(items []lintaiapi.SnapshotInput) []lintaiapi.SnapshotInput {
	byPath := make(map[string]lintaiapi.SnapshotInput, len(items))
	for _, item := range items {
		current := byPath[item.Path]
		current.Path = item.Path
		current.Exists = current.Exists || item.Exists
		current.OptionalMap = current.OptionalMap || item.OptionalMap
		byPath[item.Path] = current
	}
	result := make([]lintaiapi.SnapshotInput, 0, len(byPath))
	for _, item := range byPath {
		result = append(result, item)
	}
	slices.SortFunc(result, func(left, right lintaiapi.SnapshotInput) int { return strings.Compare(left.Path, right.Path) })
	return result
}

func combineInputRevisions(values ...any) string {
	encoded, err := canonical.MarshalStructured(append([]any{"typescript-input-revision-v3"}, values...))
	if err != nil {
		return ""
	}
	digest := sha256.Sum256(encoded)
	return "sha256:" + hex.EncodeToString(digest[:])
}

func scopeIdentity(plan *workspace.ScopePlan, snapshot *lintaiapi.Snapshot, tsconfigFiles bool) map[string]any {
	primarySet := make(map[string]struct{}, len(snapshot.Modules))
	primary := make([]string, 0, len(snapshot.Modules))
	for _, module := range snapshot.Modules {
		primarySet[module.Path] = struct{}{}
		primary = append(primary, module.Path)
	}
	slices.Sort(primary)
	primary = slices.Compact(primary)
	dependencies := make([]string, 0)
	for _, input := range snapshot.CompilerInputs {
		if input.Kind != "content" {
			continue
		}
		relative := relativeScopePath(plan.Root, input.Path)
		if _, isPrimary := primarySet[relative]; !isPrimary {
			dependencies = append(dependencies, relative)
		}
	}
	slices.Sort(dependencies)
	dependencies = slices.Compact(dependencies)
	contexts := make([]string, len(snapshot.ContextPaths))
	for i, contextPath := range snapshot.ContextPaths {
		if contextPath == "synthetic" {
			contexts[i] = contextPath
		} else {
			contexts[i] = lintaiapi.PublicConfigPath(plan.Root, contextPath)
		}
	}
	slices.Sort(contexts)
	contexts = slices.Compact(contexts)
	exact := make([]string, len(plan.ExactFiles))
	for i, file := range plan.ExactFiles {
		exact[i] = relativeScopePath(plan.Root, file)
	}
	candidates := make([]string, len(plan.Files))
	for i, file := range plan.Files {
		candidates[i] = relativeScopePath(plan.Root, file)
	}
	mode := "walk"
	if tsconfigFiles {
		mode = "tsconfig"
	}
	return map[string]any{
		"mode": mode, "candidates": candidates, "exact": exact,
		"primary": primary, "dependencies": dependencies, "contexts": contexts,
	}
}

func relativeScopePath(root, path string) string {
	path = filepath.Clean(filepath.FromSlash(path))
	root = filepath.Clean(filepath.FromSlash(root))
	if !filepath.IsAbs(path) {
		return filepath.ToSlash(path)
	}
	relative, err := filepath.Rel(root, path)
	if err != nil || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		return filepath.ToSlash(path)
	}
	return filepath.ToSlash(relative)
}
