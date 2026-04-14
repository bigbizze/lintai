package query

import (
	"testing"

	"github.com/bigbizze/lintai/internal/analysis"
	"github.com/bigbizze/lintai/internal/backend"
	"github.com/bigbizze/lintai/internal/diagnostics"
	"github.com/dop251/goja"
)

func TestMatchPatternNormalizesLeadingSlashes(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name    string
		pattern string
		path    string
		want    bool
	}{
		{
			name:    "relative pattern",
			pattern: "src/pure/**",
			path:    "src/pure/math.ts",
			want:    true,
		},
		{
			name:    "leading slash pattern",
			pattern: "/src/pure/**",
			path:    "src/pure/math.ts",
			want:    true,
		},
		{
			name:    "leading slash path",
			pattern: "src/pure/**",
			path:    "/src/pure/math.ts",
			want:    true,
		},
		{
			name:    "non match",
			pattern: "/src/services/**",
			path:    "src/pure/math.ts",
			want:    false,
		},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			got, err := matchPattern(tc.pattern, tc.path)
			if err != nil {
				t.Fatal(err)
			}
			if got != tc.want {
				t.Fatalf("matchPattern(%q, %q) = %v, want %v", tc.pattern, tc.path, got, tc.want)
			}
		})
	}
}

func TestFunctionViewIncludesExpandedMetadata(t *testing.T) {
	t.Parallel()

	view := functionView(analysis.Function{
		Name:               "Repository.save",
		Kind:               "method",
		FilePath:           "src/service/repository.ts",
		ContainerName:      "Repository",
		SemanticKey:        "repo::save",
		ContainsAwait:      true,
		IsExported:         false,
		IsAsync:            true,
		ParameterCount:     2,
		ReturnTypeText:     "Promise<number>",
		ParameterTypeTexts: []string{"DbConfig", "string"},
		Range: diagnostics.SourceLocation{
			File:        "src/service/repository.ts",
			StartLine:   4,
			StartColumn: 2,
			EndLine:     7,
			EndColumn:   3,
		},
	})

	if view["containerName"] != "Repository" {
		t.Fatalf("expected containerName in function view, got %+v", view)
	}
	if view["returnTypeText"] != "Promise<number>" {
		t.Fatalf("expected returnTypeText in function view, got %+v", view)
	}
	if view["parameterCount"] != 2 {
		t.Fatalf("expected parameterCount in function view, got %+v", view)
	}
}

func TestResolveImportsSupportsWherePredicates(t *testing.T) {
	t.Parallel()

	vm := goja.New()
	handler, err := vm.RunString(`(edge) => edge.importedSymbols.some((symbol) => symbol.name === "db" && symbol.kind === "default" && !symbol.isTypeOnly)`)
	if err != nil {
		t.Fatal(err)
	}

	evaluator := NewEvaluator(vm, &analysis.Snapshot{
		ImportEdges: []analysis.ImportEdge{
			{
				SemanticKey: "import:bad",
				FromPath:    "src/pure/bad.ts",
				ToPath:      "src/data/db.ts",
				ImportedSymbols: []analysis.ImportedSymbol{
					{Name: "db", Kind: "default", IsTypeOnly: false},
				},
			},
			{
				SemanticKey: "import:good",
				FromPath:    "src/pure/good.ts",
				ToPath:      "src/data/db.ts",
				ImportedSymbols: []analysis.ImportedSymbol{
					{Name: "DbConfig", Kind: "named", IsTypeOnly: true},
				},
			},
		},
	}, backend.CapabilityManifest{
		QueryKinds: []string{"imports"},
		Operators:  []string{"where"},
	})

	values, err := evaluator.Resolve(Plan{
		Entity: "imports",
		Ops: []Operation{
			{Type: "where", Handler: handler},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(values) != 1 {
		t.Fatalf("expected 1 matching import edge, got %d (%+v)", len(values), values)
	}
	if values[0].(analysis.ImportEdge).SemanticKey != "import:bad" {
		t.Fatalf("unexpected import edge %+v", values[0])
	}
}

func TestResolveCallsSupportsWherePredicates(t *testing.T) {
	t.Parallel()

	vm := goja.New()
	handler, err := vm.RunString(`(edge) => edge.fromPath === "src/services/service.ts" && edge.toName === "db"`)
	if err != nil {
		t.Fatal(err)
	}

	evaluator := NewEvaluator(vm, &analysis.Snapshot{
		CallEdges: []analysis.CallEdge{
			{
				SemanticKey: "call:bad",
				FromPath:    "src/services/service.ts",
				ToName:      "db",
			},
			{
				SemanticKey: "call:good",
				FromPath:    "src/pure/helper.ts",
				ToName:      "helper",
			},
		},
	}, backend.CapabilityManifest{
		QueryKinds: []string{"calls"},
		Operators:  []string{"where"},
	})

	values, err := evaluator.Resolve(Plan{
		Entity: "calls",
		Ops: []Operation{
			{Type: "where", Handler: handler},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(values) != 1 {
		t.Fatalf("expected 1 matching call edge, got %d (%+v)", len(values), values)
	}
	if values[0].(analysis.CallEdge).SemanticKey != "call:bad" {
		t.Fatalf("unexpected call edge %+v", values[0])
	}
}

func TestResolveTypeRefsSupportsInAndWherePredicates(t *testing.T) {
	t.Parallel()

	vm := goja.New()
	handler, err := vm.RunString(`(ref) => ref.targetPath.startsWith("src/services/")`)
	if err != nil {
		t.Fatal(err)
	}

	evaluator := NewEvaluator(vm, &analysis.Snapshot{
		TypeRefs: []analysis.TypeRef{
			{
				SemanticKey: "type:bad",
				Name:        "ServiceConfig",
				FilePath:    "src/pure/bad.ts",
				TargetPath:  "src/services/types.ts",
				Range: diagnostics.SourceLocation{
					File: "src/pure/bad.ts",
				},
			},
			{
				SemanticKey: "type:good",
				Name:        "DbConfig",
				FilePath:    "src/pure/good.ts",
				TargetPath:  "src/data/db.ts",
				Range: diagnostics.SourceLocation{
					File: "src/pure/good.ts",
				},
			},
		},
	}, backend.CapabilityManifest{
		QueryKinds: []string{"typeRefs"},
		Operators:  []string{"in", "where"},
	})

	values, err := evaluator.Resolve(Plan{
		Entity: "typeRefs",
		Ops: []Operation{
			{Type: "in", Value: "src/pure/**"},
			{Type: "where", Handler: handler},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(values) != 1 {
		t.Fatalf("expected 1 matching type ref, got %d (%+v)", len(values), values)
	}
	ref := values[0].(analysis.TypeRef)
	if ref.SemanticKey != "type:bad" {
		t.Fatalf("unexpected type ref %+v", ref)
	}
	if location := DiagnosticLocation(ref); location == nil || location.File != "src/pure/bad.ts" {
		t.Fatalf("expected diagnostic location for type ref, got %+v", location)
	}
	if identity := DiagnosticIdentity(ref); identity == nil || identity.SemanticKey != "type:bad" {
		t.Fatalf("expected diagnostic identity for type ref, got %+v", identity)
	}
}

func TestResolveAccessesSupportsInAndWherePredicates(t *testing.T) {
	t.Parallel()

	vm := goja.New()
	handler, err := vm.RunString(`(access) => access.origin === "special_form" && access.accessPath === "import.meta.env"`)
	if err != nil {
		t.Fatal(err)
	}

	evaluator := NewEvaluator(vm, &analysis.Snapshot{
		Accesses: []analysis.Access{
			{
				SemanticKey: "access:env",
				Root:        "import.meta",
				AccessPath:  "import.meta.env",
				Origin:      "special_form",
				FilePath:    "src/env.ts",
				Range: diagnostics.SourceLocation{
					File:      "src/env.ts",
					StartLine: 2,
					EndLine:   2,
				},
			},
			{
				SemanticKey: "access:url",
				Root:        "import.meta",
				AccessPath:  "import.meta.url",
				Origin:      "special_form",
				FilePath:    "src/url.ts",
				Range: diagnostics.SourceLocation{
					File:      "src/url.ts",
					StartLine: 2,
					EndLine:   2,
				},
			},
			{
				SemanticKey: "access:other",
				Root:        "import.meta",
				AccessPath:  "import.meta.glob",
				Origin:      "special_form",
				FilePath:    "src/server.ts",
				Range: diagnostics.SourceLocation{
					File:      "src/server.ts",
					StartLine: 2,
					EndLine:   2,
				},
			},
		},
	}, backend.CapabilityManifest{
		QueryKinds: []string{"accesses"},
		Operators:  []string{"in", "where"},
	})

	values, err := evaluator.Resolve(Plan{
		Entity: "accesses",
		Ops: []Operation{
			{Type: "in", Value: "src/**"},
			{Type: "where", Handler: handler},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(values) != 1 {
		t.Fatalf("expected 1 matching access, got %d (%+v)", len(values), values)
	}
	access := values[0].(analysis.Access)
	if location := DiagnosticLocation(access); location == nil || location.File != "src/env.ts" {
		t.Fatalf("expected diagnostic location for access, got %+v", location)
	}
	if identity := DiagnosticIdentity(access); identity == nil || identity.SemanticKey != "access:env" {
		t.Fatalf("expected diagnostic identity for access, got %+v", identity)
	}
}
