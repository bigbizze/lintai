package typescript

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/bigbizze/lintai/internal/ambient"
	"github.com/bigbizze/lintai/internal/canonical"
	"github.com/bigbizze/lintai/internal/tsgo/lintaiapi"
	"github.com/bigbizze/lintai/internal/workspace"
)

type validationStats struct {
	files   int
	bytes   int64
	retries int
	elapsed time.Duration
}

func printValidationTrace(stats validationStats) {
	fmt.Fprintf(os.Stderr, "lintai: validation files=%d bytes=%d retries=%d ms=%d\n", stats.files, stats.bytes, stats.retries, stats.elapsed.Milliseconds())
}

// validateGeneration is the single validity decision for publication and reuse.
// Compiler observations, configuration and the retained selection plan all
// contribute, even when an earlier component has already changed.
func (b *Backend) validateGeneration(ctx context.Context, generation *producerGeneration, total *validationStats) (bool, error) {
	started := time.Now()
	defer func() { total.elapsed += time.Since(started) }()
	stable, trace, err := generation.snapshot.CompilerInputs.Validate(ctx)
	total.files += trace.Files
	total.bytes += trace.Bytes
	if err != nil {
		return false, err
	}
	if b.settings.ConfigPath != "" {
		valid, bytesRead, fileRead, err := validateConfigInput(b.settings.ConfigPath, b.settings.ConfigPresent, b.settings.ConfigDigest)
		if fileRead {
			total.files++
		}
		total.bytes += bytesRead
		if err != nil {
			return false, err
		}
		stable = stable && valid
	}
	valid, err := workspace.ValidateScope(generation.plan)
	if err != nil {
		return false, err
	}
	if err := ctx.Err(); err != nil {
		return false, err
	}
	return stable && valid, nil
}

func (b *Backend) extractSnapshot(ctx context.Context, workspaceRoot string, relations []string, previous *lintaiapi.Snapshot, total *validationStats) (*producerGeneration, []ambient.Source, error) {
	scope, err := ParseFileScope(string(b.settings.Files))
	if err != nil {
		return nil, nil, err
	}
	sources := ambient.Builtin()
	options := workspace.ScopeOptions{
		Exclude: b.settings.WorkspaceExcludes, Include: b.settings.Includes,
		Sources: b.settings.Sources, InvocationExclude: b.settings.Excludes,
	}
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
		if b.beforeValidation != nil {
			b.beforeValidation()
		}
		stable, err := b.validateGeneration(ctx, generation, total)
		if err != nil {
			return nil, nil, err
		}
		if stable {
			if err := b.finishGeneration(generation, workspaceRoot, scope == FileScopeTsconfig); err != nil {
				return nil, nil, err
			}
			return generation, sources, nil
		}
		if attempt == 1 {
			return nil, nil, fmt.Errorf("inputs_changed: compiler inputs or source membership changed during collection")
		}
		total.retries++
	}
	return nil, nil, fmt.Errorf("inputs_changed: compiler inputs or source membership changed during collection")
}

func (b *Backend) finishGeneration(generation *producerGeneration, workspaceRoot string, tsconfigFiles bool) error {
	snapshot, plan := generation.snapshot, generation.plan
	encoded, err := canonical.Marshal(snapshot.CompilerInputs.IdentityRecords)
	if err != nil {
		return fmt.Errorf("input revision: %w", err)
	}
	digest := sha256.Sum256(encoded)
	revision := "sha256:" + hex.EncodeToString(digest[:])
	requestedRoot, err := filepath.Abs(workspaceRoot)
	if err != nil {
		return err
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
	addWatchHint := func(path string) {
		relative := relativeScopePath(plan.Root, path)
		if !filepath.IsAbs(filepath.FromSlash(relative)) {
			path = filepath.ToSlash(filepath.Join(requestedRoot, filepath.FromSlash(relative)))
		}
		generation.watchHints = append(generation.watchHints, path)
	}
	for _, input := range snapshot.Inputs {
		addWatchHint(input.Path)
	}
	if b.settings.ConfigPath != "" {
		addWatchHint(filepath.ToSlash(b.settings.ConfigPath))
	}
	for _, directory := range plan.Membership {
		generation.watchHints = append(generation.watchHints, directory.Path)
	}
	slices.Sort(generation.watchHints)
	generation.watchHints = slices.Compact(generation.watchHints)
	revision = combineInputRevisions(revision, plan.Revision)
	generation.scope = scopeIdentity(plan, snapshot, tsconfigFiles)
	if b.settings.ConfigPath != "" {
		configPath := normalizeIdentityPath(workspaceRoot, b.settings.ConfigPath)
		revision = combineInputRevisions(revision, configPath, b.settings.ConfigPresent, b.settings.ConfigDigest)
	}
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

func validateConfigInput(path string, expectedPresent bool, expectedDigest string) (bool, int64, bool, error) {
	info, err := os.Stat(path)
	if err != nil {
		if os.IsNotExist(err) {
			return !expectedPresent, 0, false, nil
		}
		return false, 0, false, fmt.Errorf("validate config %s: %w", path, err)
	}
	if !info.Mode().IsRegular() {
		return false, 0, false, fmt.Errorf("validate config %s: not a regular file", path)
	}
	contents, err := os.ReadFile(path)
	if err != nil {
		return false, 0, false, fmt.Errorf("validate config %s: %w", path, err)
	}
	digest := sha256.Sum256(contents)
	return expectedPresent && hex.EncodeToString(digest[:]) == expectedDigest, int64(len(contents)), true, nil
}

func combineInputRevisions(values ...any) string {
	encoded, err := canonical.Marshal(append([]any{"typescript-input-revision-v1"}, values...))
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
	for _, file := range snapshot.CompilerInputs.ReadPaths {
		relative := relativeScopePath(plan.Root, file)
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
			contexts[i] = relativeScopePath(plan.Root, contextPath)
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

func normalizeIdentityPath(root, path string) string {
	absoluteRoot, rootErr := filepath.Abs(root)
	if rootErr == nil {
		if resolvedRoot, err := filepath.EvalSymlinks(absoluteRoot); err == nil {
			absoluteRoot = resolvedRoot
		}
	}
	absolutePath, pathErr := filepath.Abs(path)
	if pathErr == nil {
		if resolvedPath, err := filepath.EvalSymlinks(absolutePath); err == nil {
			absolutePath = resolvedPath
		}
	}
	if rootErr != nil || pathErr != nil {
		return filepath.ToSlash(filepath.Clean(path))
	}
	return relativeScopePath(absoluteRoot, absolutePath)
}
