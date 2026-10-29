package lintaiapi

import (
	"fmt"
	"slices"

	"github.com/bigbizze/lintai/internal/tsgo/internal/ast"
	"github.com/bigbizze/lintai/internal/tsgo/internal/collections"
	"github.com/bigbizze/lintai/internal/tsgo/internal/compiler"
	"github.com/bigbizze/lintai/internal/tsgo/internal/core"
	"github.com/bigbizze/lintai/internal/tsgo/internal/diagnostics"
	"github.com/bigbizze/lintai/internal/tsgo/internal/tsoptions"
	"github.com/bigbizze/lintai/internal/tsgo/internal/tspath"
)

const (
	AdjustRemoved   = "removed"
	AdjustRewritten = "rewritten"
	AdjustReplaced  = "replaced"
)

// ConfigAdjustment is a compiler option that lintai changed before it loaded
// a tsconfig, because TypeScript 7 rejects the option.
type ConfigAdjustment struct {
	// Config is the absolute path of the original tsconfig.
	Config string
	Option string
	Action string
}

var (
	removedOptionCode      = diagnostics.Option_0_has_been_removed_Please_remove_it_from_your_configuration.Code()
	removedOptionValueCode = diagnostics.Option_0_1_has_been_removed_Please_remove_it_from_your_configuration.Code()
	nonRelativePathCode    = diagnostics.Non_relative_paths_are_not_allowed_Did_you_forget_a_leading_Slash.Code()
)

// removedOptionDiagnostics returns the diagnostics for options that
// TypeScript 7 removed. The compiler reports them when it creates a program,
// so this creates a program with no files, which only checks the options.
func removedOptionDiagnostics(host compiler.CompilerHost, command *tsoptions.ParsedCommandLine) []*ast.Diagnostic {
	program := compiler.NewProgram(compiler.ProgramOptions{
		ProgramConfig: compiler.ProgramConfig{Config: command.WithFileNames(nil)},
		ProgramHosts:  compiler.ProgramHosts{Host: host},
	})
	return slices.DeleteFunc(program.GetProgramDiagnostics(), func(diag *ast.Diagnostic) bool {
		switch diag.Code() {
		case removedOptionCode, removedOptionValueCode, nonRelativePathCode:
			return false
		}
		return true
	})
}

// adjustForTypeScript7 returns the command line to build the program of a
// tsconfig with. When TypeScript 7 rejects options of the tsconfig, it parses
// an in-memory tsconfig that extends the original, overrides those options,
// and lists the exact files of the original. The in-memory tsconfig has a
// path next to the original, so the compiler resolves inherited relative
// options and @types directories as it does for the original. Nothing is
// written to disk.
func adjustForTypeScript7(host compiler.CompilerHost, configPath string, command *tsoptions.ParsedCommandLine) (*tsoptions.ParsedCommandLine, []ConfigAdjustment, error) {
	diags := removedOptionDiagnostics(host, command)
	if len(diags) == 0 {
		return command, nil, nil
	}
	options := command.CompilerOptions()
	overrides := collections.NewOrderedMapWithSizeHint[string, any](len(diags))
	actions := make(map[string]string)
	rewritePaths := false
	for _, diag := range diags {
		if diag.Code() == nonRelativePathCode {
			rewritePaths = true
			continue
		}
		args := diag.MessageArgs()
		if len(args) == 0 {
			continue
		}
		option := args[0]
		switch option {
		case "baseUrl":
			rewritePaths = true
			overrides.Set(option, nil)
			actions[option] = AdjustRewritten
		case "moduleResolution":
			overrides.Set(option, replacementModuleResolution(options))
			actions[option] = AdjustReplaced
		default:
			overrides.Set(option, nil)
			actions[option] = AdjustRemoved
		}
	}
	if rewritePaths {
		base := options.BaseUrl.AsString()
		if base == "" {
			base = options.PathsBasePath.AsString()
		}
		paths := collections.NewOrderedMapWithSizeHint[string, any](options.Paths.Size() + 1)
		hasWildcard := false
		for key, targets := range options.Paths.Entries() {
			absolute := make([]any, 0, len(targets))
			for _, target := range targets {
				absolute = append(absolute, tspath.GetNormalizedAbsolutePath(target, tspath.RootedDirectoryPathFromAbsolute(base)))
			}
			paths.Set(key, absolute)
			hasWildcard = hasWildcard || key == "*"
		}
		if options.BaseUrl != "" && !hasWildcard {
			paths.Set("*", []any{tspath.CombinePaths(options.BaseUrl.AsString(), "*")})
		}
		if paths.Size() > 0 {
			overrides.Set("paths", paths)
			if options.Paths.Size() > 0 {
				actions["paths"] = AdjustRewritten
			}
		}
	}

	files := make([]any, 0, len(command.FileNames()))
	for _, file := range command.FileNames() {
		files = append(files, file.AsString())
	}
	references := make([]any, 0, len(command.ProjectReferences()))
	for _, reference := range command.ProjectReferences() {
		item := collections.NewOrderedMapWithSizeHint[string, any](2)
		item.Set("path", reference.Path.AsString())
		if reference.Circular {
			item.Set("circular", true)
		}
		references = append(references, item)
	}
	json := collections.NewOrderedMapWithSizeHint[string, any](5)
	json.Set("extends", configPath)
	json.Set("compilerOptions", overrides)
	json.Set("files", files)
	json.Set("include", []any{})
	if len(references) > 0 {
		json.Set("references", references)
	}
	configFile := tspath.RootedFilePathFromAbsolute(configPath)
	adjustedPath := configFile.Directory().ResolveFile("tsconfig.lintai.adjusted.json")
	adjusted := tsoptions.ParseJsonConfigFileContent(json, host.FS(), configFile.Directory(), nil, adjustedPath, nil, nil)
	if parseDiags := adjusted.GetConfigFileParsingDiagnostics(); len(parseDiags) > 0 {
		return nil, nil, fmt.Errorf("%s: adjusting the tsconfig for TypeScript 7 failed: %s", configPath, joinDiagnostics(parseDiags))
	}
	if remaining := removedOptionDiagnostics(host, adjusted); len(remaining) > 0 {
		return nil, nil, fmt.Errorf("%s: TypeScript 7 rejects the tsconfig after adjustment: %s", configPath, joinDiagnostics(remaining))
	}

	result := make([]ConfigAdjustment, 0, len(actions))
	for option, action := range actions {
		result = append(result, ConfigAdjustment{Config: configPath, Option: option, Action: action})
	}
	slices.SortFunc(result, func(a, b ConfigAdjustment) int {
		if a.Option < b.Option {
			return -1
		}
		if a.Option > b.Option {
			return 1
		}
		return 0
	})
	return adjusted, result, nil
}

// replacementModuleResolution returns the module resolution that TypeScript 7
// uses when moduleResolution is not set: node16 or nodenext for those module
// kinds, otherwise bundler.
func replacementModuleResolution(options *core.CompilerOptions) string {
	unset := options.Clone()
	unset.ModuleResolution = core.ModuleResolutionKindUnknown
	switch unset.GetModuleResolutionKind() {
	case core.ModuleResolutionKindNode16:
		return "node16"
	case core.ModuleResolutionKindNodeNext:
		return "nodenext"
	default:
		return "bundler"
	}
}
