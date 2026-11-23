package lintaiapi

import (
	"context"
	"path/filepath"
	"slices"

	"github.com/bigbizze/lintai/internal/tsgo/internal/bundled"
	"github.com/bigbizze/lintai/internal/tsgo/internal/compiler"
	"github.com/bigbizze/lintai/internal/tsgo/internal/tspath"
	"github.com/bigbizze/lintai/internal/tsgo/internal/vfs/osvfs"
)

// CompilerFile describes one selected file's planned compiler ownership and
// original root membership. A nil root list means membership was not collected.
type CompilerFile struct {
	Path           string   `json:"path"`
	Eligible       bool     `json:"eligible"`
	OwnerKind      *string  `json:"ownerKind"`
	OwnerConfig    *string  `json:"ownerConfig"`
	SelectedConfig *string  `json:"selectedConfig"`
	RootConfigs    []string `json:"rootConfigs"`
}

type CompilerEligibility struct {
	Files          []string
	Configurations []string
	CompilerFiles  []CompilerFile
}

// PublicConfigPath applies the compiler's filesystem case policy and exposes
// in-workspace paths relative to the workspace, with outside paths absolute.
func PublicConfigPath(root, path string) string {
	return publicIdentityPath(root, path, osvfs.FS().CaseSensitivity())
}

func publicIdentityPath(root, path string, sensitivity tspath.CaseSensitivity) string {
	if path == "" {
		return ""
	}
	key := normalizeSlash(sensitivity.PathKey(tspath.RootedPath(path)).AsString())
	rootKey := normalizeSlash(sensitivity.PathKey(tspath.RootedPath(root)).AsString())
	if isWithinRoot(rootKey, key) {
		if relative, err := filepath.Rel(filepath.FromSlash(rootKey), filepath.FromSlash(key)); err == nil {
			return filepath.ToSlash(relative)
		}
	}
	return key
}

func (plan *ordinaryPlan) compilerFiles(root string, host compiler.CompilerHost) []CompilerFile {
	result := make([]CompilerFile, 0, len(plan.files))
	configPath := func(path string) *string {
		if path == "" {
			return nil
		}
		value := publicIdentityPath(root, path, host.FS().CaseSensitivity())
		return &value
	}
	for _, file := range plan.files {
		path, err := filepath.Rel(filepath.FromSlash(root), filepath.FromSlash(file.path))
		if err != nil {
			path = file.path
		}
		item := CompilerFile{Path: filepath.ToSlash(path), Eligible: file.owner != nil, SelectedConfig: configPath(file.selectedConfig)}
		if file.owner != nil {
			kind := "synthetic"
			if file.owner.group.ConfigPath != "" {
				kind = "configured"
				item.OwnerConfig = configPath(file.owner.group.ConfigPath)
			}
			item.OwnerKind = &kind
		}
		if plan.membershipCollected {
			item.RootConfigs = make([]string, 0, len(file.rootConfigs))
			for _, config := range file.rootConfigs {
				item.RootConfigs = append(item.RootConfigs, *configPath(config))
			}
			slices.Sort(item.RootConfigs)
			item.RootConfigs = slices.Compact(item.RootConfigs)
		}
		result = append(result, item)
	}
	return result
}

// ExplainCompilerEligibility uses the same final ordinary plan as extraction.
func ExplainCompilerEligibility(ctx context.Context, req BuildSnapshotRequest) (CompilerEligibility, error) {
	root, err := normalizeAbsPath(req.WorkspaceRoot)
	if err != nil {
		return CompilerEligibility{}, err
	}
	files, err := normalizeFiles(req.Files)
	if err != nil {
		return CompilerEligibility{}, err
	}
	exact, err := normalizeFiles(req.ExactFiles)
	if err != nil {
		return CompilerEligibility{}, err
	}
	trackedFS := trackResolutionInputs(osvfs.FS())
	host := compiler.NewCompilerHost(bundled.WrapFS(trackedFS), bundled.LibPath(), nil, nil, nil)
	plan, err := planOrdinary(ctx, host, root, files, exact, req.TsconfigFiles, true, newConfigCache(host), &extractTiming{})
	if err != nil {
		return CompilerEligibility{}, err
	}
	report := CompilerEligibility{CompilerFiles: plan.compilerFiles(root, host)}
	for i, file := range plan.files {
		if file.owner != nil {
			report.Files = append(report.Files, file.path)
		}
		item := report.CompilerFiles[i]
		if item.SelectedConfig != nil {
			report.Configurations = append(report.Configurations, *item.SelectedConfig)
		}
		if item.OwnerConfig != nil {
			report.Configurations = append(report.Configurations, *item.OwnerConfig)
		}
	}
	slices.Sort(report.Configurations)
	report.Configurations = slices.Compact(report.Configurations)
	return report, nil
}
