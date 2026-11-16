package lintaiapi

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/bigbizze/lintai/internal/canonical"
	"github.com/bigbizze/lintai/internal/workspace"
)

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

func scopeIdentity(plan *workspace.ScopePlan, snapshot *Snapshot, tsconfigFiles bool) map[string]any {
	primarySet := make(map[string]struct{}, len(snapshot.Modules))
	primary := make([]string, 0, len(snapshot.Modules))
	for _, module := range snapshot.Modules {
		primarySet[module.Path] = struct{}{}
		primary = append(primary, module.Path)
	}
	slices.Sort(primary)
	primary = slices.Compact(primary)
	dependencies := make([]string, 0)
	if snapshot.capture != nil {
		for _, file := range snapshot.capture.readPaths() {
			relative := relativeScopePath(plan.Root, file)
			if _, isPrimary := primarySet[relative]; !isPrimary {
				dependencies = append(dependencies, relative)
			}
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

func printValidationTrace(previous, current validationStats) {
	stats := validationStats{
		files:   previous.files + current.files,
		bytes:   previous.bytes + current.bytes,
		retries: previous.retries + current.retries,
		elapsed: previous.elapsed + current.elapsed,
	}
	fmt.Fprintf(os.Stderr, "lintai: validation files=%d bytes=%d retries=%d ms=%d\n", stats.files, stats.bytes, stats.retries, stats.elapsed.Milliseconds())
}
