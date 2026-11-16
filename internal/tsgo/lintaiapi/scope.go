package lintaiapi

import (
	"context"
	"fmt"
	"slices"

	"github.com/bigbizze/lintai/internal/tsgo/internal/bundled"
	"github.com/bigbizze/lintai/internal/tsgo/internal/compiler"
	"github.com/bigbizze/lintai/internal/tsgo/internal/vfs/osvfs"
	"github.com/bigbizze/lintai/internal/workspace"
)

type ScopeExplanation struct {
	WorkspaceExclude []string               `json:"exclude"`
	WorkspaceInclude []string               `json:"include"`
	ExactSources     []string               `json:"sources"`
	Selected         []workspace.ScopeMatch `json:"selected"`
	CompilerEligible []string               `json:"compilerEligible"`
	CompilerExcluded []string               `json:"compilerExcluded"`
	Configurations   []string               `json:"configurations"`
	PrunedBoundaries []string               `json:"prunedBoundaries"`
}

// ExplainScope uses the same production selector and configuration grouping
// as extraction. It builds compiler programs only to determine membership;
// it does not bind/check source files or collect semantic facts.
func ExplainScope(ctx context.Context, req BuildSnapshotRequest) (ScopeExplanation, error) {
	if req.ScopeOptions == nil {
		return ScopeExplanation{}, fmt.Errorf("scope explanation requires the production selector options")
	}
	plan, err := workspace.PlanSources(req.WorkspaceRoot, *req.ScopeOptions)
	if err != nil {
		return ScopeExplanation{}, err
	}
	root, err := normalizeAbsPath(req.WorkspaceRoot)
	if err != nil {
		return ScopeExplanation{}, err
	}
	trackedFS := trackResolutionInputs(osvfs.FS(), root)
	host := compiler.NewCompilerHost(bundled.WrapFS(trackedFS), bundled.LibPath(), nil, nil, nil)
	configs := newConfigCache(host)
	groups, err := discoverConfigGroups(root, plan.Files, plan.ExactFiles, configs)
	if err != nil {
		return ScopeExplanation{}, err
	}
	report := ScopeExplanation{
		WorkspaceExclude: slices.Clone(req.ScopeOptions.Exclude),
		WorkspaceInclude: slices.Clone(req.ScopeOptions.Include),
		Selected:         slices.Clone(plan.Matches),
		PrunedBoundaries: slices.Clone(plan.Pruned),
	}
	for _, file := range plan.ExactFiles {
		report.ExactSources = append(report.ExactSources, workspace.RelativePath(root, file))
	}
	slices.Sort(report.ExactSources)
	report.ExactSources = slices.Compact(report.ExactSources)
	eligible := make(map[string]struct{})
	var missingWalkFiles []string
	for _, group := range groups {
		if err := ctx.Err(); err != nil {
			return ScopeExplanation{}, err
		}
		configuration := "synthetic"
		programConfig := group.Config
		if programConfig == nil {
			programConfig, err = syntheticConfig(host, root, group.PrimaryFiles)
			if err != nil {
				return ScopeExplanation{}, err
			}
		} else {
			configuration = workspace.RelativePath(root, group.ConfigPath)
		}
		report.Configurations = append(report.Configurations, configuration)
		program := compiler.NewProgram(compiler.ProgramOptions{
			ProgramConfig: compiler.ProgramConfig{Config: programConfig, UseSourceOfProjectReference: true},
			ProgramHosts:  compiler.ProgramHosts{Host: host},
		})
		if diagnostics := program.GetConfigFileParsingDiagnostics(); len(diagnostics) > 0 {
			return ScopeExplanation{}, fmt.Errorf("tsgo config diagnostics: %s", joinDiagnostics(diagnostics))
		}
		program.BindSourceFiles()
		available := make(map[string]struct{}, len(program.SourceFiles()))
		for _, source := range program.SourceFiles() {
			available[normalizeSlash(source.FileName().AsString())] = struct{}{}
		}
		for _, file := range group.PrimaryFiles {
			if _, ok := available[normalizeSlash(file)]; ok {
				eligible[file] = struct{}{}
			} else if !req.TsconfigFiles {
				missingWalkFiles = append(missingWalkFiles, file)
			}
		}
	}
	if len(missingWalkFiles) > 0 {
		slices.Sort(missingWalkFiles)
		synthetic, err := syntheticConfig(host, root, missingWalkFiles)
		if err != nil {
			return ScopeExplanation{}, err
		}
		program := compiler.NewProgram(compiler.ProgramOptions{
			ProgramConfig: compiler.ProgramConfig{Config: synthetic, UseSourceOfProjectReference: true},
			ProgramHosts:  compiler.ProgramHosts{Host: host},
		})
		if diagnostics := program.GetConfigFileParsingDiagnostics(); len(diagnostics) > 0 {
			return ScopeExplanation{}, fmt.Errorf("synthetic tsconfig diagnostics: %s", joinDiagnostics(diagnostics))
		}
		program.BindSourceFiles()
		available := make(map[string]struct{}, len(program.SourceFiles()))
		for _, source := range program.SourceFiles() {
			available[normalizeSlash(source.FileName().AsString())] = struct{}{}
		}
		for _, file := range missingWalkFiles {
			if _, ok := available[normalizeSlash(file)]; !ok {
				return ScopeExplanation{}, fmt.Errorf("compiler membership is incomplete for %s", file)
			}
			eligible[file] = struct{}{}
		}
		report.Configurations = append(report.Configurations, "synthetic")
	}
	slices.Sort(report.Configurations)
	report.Configurations = slices.Compact(report.Configurations)
	for _, file := range plan.Files {
		relative := workspace.RelativePath(root, file)
		if _, ok := eligible[file]; ok {
			report.CompilerEligible = append(report.CompilerEligible, relative)
		} else {
			report.CompilerExcluded = append(report.CompilerExcluded, relative)
		}
	}
	return report, nil
}
