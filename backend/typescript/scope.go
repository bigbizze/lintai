package typescript

import (
	"context"
	"slices"

	"github.com/bigbizze/lintai/internal/tsgo/lintaiapi"
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

// ExplainScope uses the production selector and compiler grouping without
// collecting semantic facts.
func ExplainScope(ctx context.Context, workspaceRoot string, options Options) (ScopeExplanation, error) {
	scope, err := ParseFileScope(string(options.Files))
	if err != nil {
		return ScopeExplanation{}, err
	}
	plan, err := workspace.PlanSources(workspaceRoot, workspace.ScopeOptions{
		Exclude: options.WorkspaceExcludes, Include: options.Includes,
		Sources: options.Sources, InvocationExclude: options.Excludes,
	})
	if err != nil {
		return ScopeExplanation{}, err
	}
	compiler, err := lintaiapi.ExplainCompilerEligibility(ctx, lintaiapi.BuildSnapshotRequest{
		WorkspaceRoot: plan.Root, Files: plan.Files, ExactFiles: plan.ExactFiles,
		TsconfigFiles: scope == FileScopeTsconfig,
	})
	if err != nil {
		return ScopeExplanation{}, err
	}
	report := ScopeExplanation{
		WorkspaceExclude: slices.Clone(options.WorkspaceExcludes),
		WorkspaceInclude: slices.Clone(options.Includes),
		Selected:         slices.Clone(plan.Matches), PrunedBoundaries: slices.Clone(plan.Pruned),
	}
	for _, file := range plan.ExactFiles {
		report.ExactSources = append(report.ExactSources, workspace.RelativePath(plan.Root, file))
	}
	slices.Sort(report.ExactSources)
	report.ExactSources = slices.Compact(report.ExactSources)
	for _, config := range compiler.Configurations {
		if config != "synthetic" {
			config = workspace.RelativePath(plan.Root, config)
		}
		report.Configurations = append(report.Configurations, config)
	}
	slices.Sort(report.Configurations)
	report.Configurations = slices.Compact(report.Configurations)
	eligible := make(map[string]struct{}, len(compiler.Files))
	for _, file := range compiler.Files {
		eligible[file] = struct{}{}
	}
	for _, file := range plan.Files {
		relative := workspace.RelativePath(plan.Root, file)
		if _, ok := eligible[file]; ok {
			report.CompilerEligible = append(report.CompilerEligible, relative)
		} else {
			report.CompilerExcluded = append(report.CompilerExcluded, relative)
		}
	}
	return report, nil
}
