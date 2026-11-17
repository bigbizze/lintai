package lintaiapi

import (
	"context"
	"fmt"
	"slices"

	"github.com/bigbizze/lintai/internal/tsgo/internal/bundled"
	"github.com/bigbizze/lintai/internal/tsgo/internal/compiler"
	"github.com/bigbizze/lintai/internal/tsgo/internal/vfs/osvfs"
)

// CompilerEligibility contains only compiler membership evidence. The producer
// supplies the selected paths and assembles the scope explanation.
type CompilerEligibility struct {
	Files          []string
	Configurations []string
}

func ExplainCompilerEligibility(ctx context.Context, req BuildSnapshotRequest) (CompilerEligibility, error) {
	root := req.WorkspaceRoot
	trackedFS := trackResolutionInputs(osvfs.FS(), root)
	host := compiler.NewCompilerHost(bundled.WrapFS(trackedFS), bundled.LibPath(), nil, nil, nil)
	configs := newConfigCache(host)
	groups, err := discoverConfigGroups(root, req.Files, req.ExactFiles, configs)
	if err != nil {
		return CompilerEligibility{}, err
	}
	report := CompilerEligibility{}
	eligible := make(map[string]struct{})
	var missingWalkFiles []string
	for _, group := range groups {
		if err := ctx.Err(); err != nil {
			return CompilerEligibility{}, err
		}
		configuration := "synthetic"
		programConfig := group.Config
		if programConfig == nil {
			programConfig, err = syntheticConfig(host, root, group.PrimaryFiles)
			if err != nil {
				return CompilerEligibility{}, err
			}
		} else {
			configuration = group.ConfigPath
		}
		report.Configurations = append(report.Configurations, configuration)
		program := compiler.NewProgram(compiler.ProgramOptions{
			ProgramConfig: compiler.ProgramConfig{Config: programConfig, UseSourceOfProjectReference: true},
			ProgramHosts:  compiler.ProgramHosts{Host: host},
		})
		if diagnostics := program.GetConfigFileParsingDiagnostics(); len(diagnostics) > 0 {
			return CompilerEligibility{}, fmt.Errorf("tsgo config diagnostics: %s", joinDiagnostics(diagnostics))
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
			return CompilerEligibility{}, err
		}
		program := compiler.NewProgram(compiler.ProgramOptions{
			ProgramConfig: compiler.ProgramConfig{Config: synthetic, UseSourceOfProjectReference: true},
			ProgramHosts:  compiler.ProgramHosts{Host: host},
		})
		if diagnostics := program.GetConfigFileParsingDiagnostics(); len(diagnostics) > 0 {
			return CompilerEligibility{}, fmt.Errorf("synthetic tsconfig diagnostics: %s", joinDiagnostics(diagnostics))
		}
		program.BindSourceFiles()
		available := make(map[string]struct{}, len(program.SourceFiles()))
		for _, source := range program.SourceFiles() {
			available[normalizeSlash(source.FileName().AsString())] = struct{}{}
		}
		for _, file := range missingWalkFiles {
			if _, ok := available[normalizeSlash(file)]; !ok {
				return CompilerEligibility{}, fmt.Errorf("compiler membership is incomplete for %s", file)
			}
			eligible[file] = struct{}{}
		}
		report.Configurations = append(report.Configurations, "synthetic")
	}
	slices.Sort(report.Configurations)
	report.Configurations = slices.Compact(report.Configurations)
	for _, file := range req.Files {
		if _, ok := eligible[file]; ok {
			report.Files = append(report.Files, file)
		}
	}
	return report, nil
}
