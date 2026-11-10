package lintaiapi

import (
	"fmt"
	"path/filepath"
	"strings"
	"testing"

	"github.com/bigbizze/lintai/internal/tsgo/internal/bundled"
	"github.com/bigbizze/lintai/internal/tsgo/internal/compiler"
	"github.com/bigbizze/lintai/internal/tsgo/internal/module"
	"github.com/bigbizze/lintai/internal/tsgo/internal/vfs/osvfs"
)

// This API probe protects the program-construction decision PR3 depends on:
// the vendored compiler can redirect a project reference to source without
// built output, and the project option can disable that behavior.
func TestProjectReferenceSourceRedirectProbe(t *testing.T) {
	for _, disabled := range []bool{false, true} {
		name := "enabled"
		if disabled {
			name = "disabled"
		}
		t.Run(name, func(t *testing.T) {
			root := t.TempDir()
			writeWorkspace(t, root, map[string]string{
				"contracts/tsconfig.json": `{"compilerOptions":{"composite":true,"declaration":true,"rootDir":"src","outDir":"dist"},"include":["src/**/*.ts"]}`,
				"contracts/src/index.ts":  "export interface Contract { id: string }\nexport function load(): Contract { return { id: \"x\" }; }\n",
				"app/tsconfig.json":       fmt.Sprintf(`{"compilerOptions":{"target":"es2022","module":"nodenext","moduleResolution":"nodenext","noEmit":true,"baseUrl":".","paths":{"@contracts":["../contracts/dist/index.d.ts"]},"disableSourceOfProjectReferenceRedirect":%t},"references":[{"path":"../contracts"}],"include":["src/**/*.ts"]}`, disabled),
				"app/src/consumer.ts":     "import { load } from \"@contracts\";\nexport function use() { return load(); }\n",
			})
			host := compiler.NewCompilerHost(bundled.WrapFS(osvfs.FS()), bundled.LibPath(), nil, nil, nil)
			configPath := filepath.Join(root, "app/tsconfig.json")
			config, err := newConfigCache(host).program(normalizeSlash(configPath))
			if err != nil {
				t.Fatal(err)
			}
			var resolver module.Resolver
			program := compiler.NewProgram(compiler.ProgramOptions{
				ProgramConfig: compiler.ProgramConfig{
					Config:                      config,
					UseSourceOfProjectReference: true,
				},
				ProgramHosts: compiler.ProgramHosts{Host: host},
				ProgramFactories: compiler.ProgramFactories{
					CreateModuleResolver: func(options module.ResolverOptions) module.Resolver {
						resolver = module.NewResolver(options)
						return resolver
					},
				},
			})
			if diagnostics := program.GetConfigFileParsingDiagnostics(); len(diagnostics) > 0 {
				t.Fatalf("config diagnostics: %s", joinDiagnostics(diagnostics))
			}
			program.BindSourceFiles()

			foundSource := false
			foundGenerated := false
			for _, file := range program.GetSourceFiles() {
				path := normalizeSlash(file.FileName().AsString())
				foundSource = foundSource || strings.HasSuffix(path, "/contracts/src/index.ts")
				foundGenerated = foundGenerated || strings.HasSuffix(path, "/contracts/dist/index.d.ts")
			}
			if foundGenerated {
				t.Fatal("the generated declaration unexpectedly exists in the checked program")
			}
			if foundSource == disabled {
				t.Fatalf("source presence=%t, disableSourceOfProjectReferenceRedirect=%t", foundSource, disabled)
			}
		})
	}
}
