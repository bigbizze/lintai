package typescript

import (
	"context"
	"fmt"
	"path/filepath"
	"slices"
	"strings"

	"github.com/bigbizze/lintai/internal/ambient"
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
	sources := ambient.Builtin()
	options := workspace.ScopeOptions{Exclude: b.settings.WorkspaceExcludes, Include: b.settings.Includes, Sources: b.settings.Sources, InvocationExclude: b.settings.Excludes}
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
		RequestedRelations: relations, TsconfigFiles: b.settings.Files == FileScopeTsconfig,
	}, previous)
	if err != nil {
		return nil, nil, err
	}
	generation := &producerGeneration{snapshot: snapshot, adjustments: convertAdjustments(plan.Root, snapshot.ConfigAdjustments)}
	if err := b.finishGeneration(generation, plan, workspaceRoot, b.settings.Files == FileScopeTsconfig); err != nil {
		return nil, nil, err
	}
	return generation, sources, nil
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

func (b *Backend) finishGeneration(generation *producerGeneration, plan *workspace.ScopePlan, workspaceRoot string, tsconfigFiles bool) error {
	snapshot := generation.snapshot
	compilerSet, err := compilerObservations(snapshot.CompilerInputs)
	if err != nil {
		return fmt.Errorf("compiler observations: %w", err)
	}
	generation.observations = observe.Union(compilerSet, plan.Observations)
	generation.aliases = plan.ExactAliases
	requestedRoot, err := filepath.Abs(workspaceRoot)
	if err != nil {
		return err
	}
	generation.inputRevision, err = generation.observations.Digest(filepath.Clean(requestedRoot))
	if err != nil {
		return fmt.Errorf("input revision: %w", err)
	}
	generation.scope = scopeIdentity(plan, snapshot, tsconfigFiles)
	return nil
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
