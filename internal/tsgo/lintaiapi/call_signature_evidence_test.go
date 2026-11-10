package lintaiapi

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/bigbizze/lintai/internal/tsgo/internal/ast"
	"github.com/bigbizze/lintai/internal/tsgo/internal/bundled"
	"github.com/bigbizze/lintai/internal/tsgo/internal/checker"
	"github.com/bigbizze/lintai/internal/tsgo/internal/compiler"
	"github.com/bigbizze/lintai/internal/tsgo/internal/core"
	"github.com/bigbizze/lintai/internal/tsgo/internal/module"
	"github.com/bigbizze/lintai/internal/tsgo/internal/tspath"
	"github.com/bigbizze/lintai/internal/tsgo/internal/vfs/osvfs"
)

// Uses production program/checker APIs without requesting whole-file diagnostics.
// Run after scripts/sync-tsgo.sh installs the evidence patch.
func TestCallSignatureEvidence(t *testing.T) {
	const source = `
declare function overloaded(x: string): string;
declare function overloaded(x: number): number;
declare function single(x: string): void;
declare function generic<T extends string>(x: T): T;
declare const optional: (<T>(x: T) => T) | undefined;
declare const union: ((x: string) => string) | ((x: string | number) => number);
declare const genericUnion: (<T>(x: T) => T) | (<T>(x: T) => T[]);
declare const optionalUnion: typeof genericUnion | undefined;
declare const anything: any;
declare const noncallable: number;
declare class Box { constructor(x: string); }
declare function tag(parts: TemplateStringsArray): string;
declare function flowStep(x: string): string | number;
declare function f(x: string): void;
f("ok",);
single(123);
single("ok");
single();
generic(123);
generic("ok");
overloaded("ok");
overloaded(1);
overloaded(true);
optional?.("ok");
union("ok");
genericUnion("ok");
optionalUnion?.("ok");
anything();
noncallable();
new Box("ok");
tag` + "`ok`" + `;
let flowValue: string | number = "start";
while (typeof flowValue === "string") { flowValue = flowStep(123); }
const afterFlow = flowValue;
`
	cases := []struct {
		call        string
		declaration string
		synthetic   bool
		recovery    bool
	}{
		{`single(123)`, `single(x: string)`, false, true},
		{`single("ok")`, `single(x: string)`, false, false},
		{`single()`, `single(x: string)`, false, true},
		{`generic(123)`, `generic<T extends string>`, false, true},
		{`generic("ok")`, `generic<T extends string>`, false, false},
		{`overloaded("ok")`, `overloaded(x: string)`, false, false},
		{`overloaded(1)`, `overloaded(x: number)`, false, false},
		{`overloaded(true)`, `overloaded(x: string)`, true, true},
		{`optional?.("ok")`, `<T>(x: T) => T`, false, false},
		{`union("ok")`, ``, true, false},
		{`genericUnion("ok")`, ``, true, false},
		{`optionalUnion?.("ok")`, ``, true, false},
		{`anything()`, ``, false, false},
		{`noncallable()`, ``, false, false},
		{`new Box("ok")`, `constructor(x: string)`, false, false},
		{"tag`ok`", `tag(parts: TemplateStringsArray)`, false, false},
		{`flowStep(123)`, `flowStep(x: string)`, false, true},
	}
	for _, legacyFirst := range []bool{true, false} {
		name := "evidence-first"
		if legacyFirst {
			name = "legacy-first"
		}
		t.Run(name, func(t *testing.T) {
			root := t.TempDir()
			writeWorkspace(t, root, map[string]string{"index.ts": source})
			path := filepath.Join(root, "index.ts")
			host := compiler.NewCompilerHost(bundled.WrapFS(osvfs.FS()), bundled.LibPath(), nil, nil, nil)
			config, err := syntheticConfig(host, root, []string{path})
			if err != nil {
				t.Fatal(err)
			}
			config.CompilerOptions().StrictNullChecks = core.TSTrue
			program := compiler.NewProgram(compiler.ProgramOptions{
				ProgramConfig: compiler.ProgramConfig{Config: config},
				ProgramHosts:  compiler.ProgramHosts{Host: host},
				ProgramFactories: compiler.ProgramFactories{CreateModuleResolver: func(options module.ResolverOptions) module.Resolver {
					return module.NewResolver(options)
				}},
			})
			program.BindSourceFiles()
			c, done := program.GetTypeChecker(context.Background())
			defer done()
			file := program.GetSourceFile(tspath.RootedFilePathFromAbsolute(path))
			if file == nil {
				t.Fatal("fixture source not loaded")
			}
			calls := make(map[string]*ast.Node)
			var flowRead *ast.Node
			var visit func(*ast.Node) bool
			visit = func(node *ast.Node) bool {
				switch node.Kind {
				case ast.KindCallExpression, ast.KindNewExpression, ast.KindTaggedTemplateExpression:
					calls[strings.TrimSpace(source[node.Pos():node.End()])] = node
				case ast.KindVariableDeclaration:
					if node.Name().Text() == "afterFlow" {
						flowRead = node.Initializer()
					}
				}
				return node.ForEachChild(visit)
			}
			visit(file.AsNode())
			if flowRead == nil {
				t.Fatal("missing flow read")
			}
			// Ask for the post-loop type before explicitly resolving its call.
			// This exercises the production control-flow entry point; the public
			// API does not expose whether a particular result was temporary.
			c.GetTypeAtLocation(flowRead)
			observed := make(map[string]checker.ResolvedSignatureEvidence)
			for _, tc := range cases {
				node := calls[tc.call]
				if node == nil {
					t.Fatalf("missing invocation %s", tc.call)
				}
				var legacy *checker.Signature
				if legacyFirst {
					legacy = c.GetResolvedSignature(node)
				}
				evidence := c.GetResolvedSignatureEvidence(node)
				if !legacyFirst {
					legacy = c.GetResolvedSignature(node)
				}
				if evidence.Signature != legacy || evidence.Synthetic != tc.synthetic || evidence.Recovery != tc.recovery {
					t.Errorf("%s: evidence=%+v, legacy=%p; want synthetic=%v recovery=%v", tc.call, evidence, legacy, tc.synthetic, tc.recovery)
				}
				if tc.declaration != "" {
					if evidence.Signature == nil || evidence.Signature.Declaration() == nil {
						t.Fatalf("%s: missing declaration", tc.call)
					}
					decl := evidence.Signature.Declaration()
					if text := source[decl.Pos():decl.End()]; !strings.Contains(text, tc.declaration) {
						t.Errorf("%s: selected %q, want %q", tc.call, text, tc.declaration)
					}
				}
				if tc.call == "anything()" || tc.call == "noncallable()" {
					if evidence.Signature != nil && evidence.Signature.Declaration() != nil {
						t.Errorf("%s: unexpected concrete declaration", tc.call)
					}
				}
				observed[tc.call] = evidence
			}
			// Bad and good invocations share a candidate, but not recovery status.
			if observed[`single(123)`].Signature != observed[`single("ok")`].Signature {
				t.Error("single-candidate fixture did not exercise a shared signature")
			}
			for _, call := range []string{`genericUnion("ok")`, `optionalUnion?.("ok")`} {
				if sig := observed[call].Signature; sig == nil || sig.Target() == nil {
					t.Errorf("%s: fixture did not exercise a composite target chain", call)
				}
			}
			for call, first := range observed {
				if again := c.GetResolvedSignatureEvidence(calls[call]); again != first {
					t.Errorf("%s: cached evidence changed: %+v -> %+v", call, first, again)
				}
			}
			// Signature help counts the trailing comma as another argument and
			// temporarily selects an arity-error fallback for this valid call.
			// Restoring its signature must also restore its complete evidence.
			node := calls[`f("ok",)`]
			if node == nil {
				t.Fatal("missing signature-help invocation")
			}
			before := c.GetResolvedSignatureEvidence(node)
			if before.Signature == nil || before.Signature.Declaration() == nil || before.Synthetic || before.Recovery {
				t.Fatalf("expected complete evidence before signature help, got %+v", before)
			}
			if sig, candidates := checker.GetResolvedSignatureForSignatureHelp(node, 2, c); sig == nil || len(candidates) != 1 {
				t.Fatalf("signature help did not resolve the expected candidate: %p, %d candidates", sig, len(candidates))
			}
			if after := c.GetResolvedSignatureEvidence(node); after != before {
				t.Errorf("signature help changed complete evidence: %+v -> %+v", before, after)
			}
		})
	}
}
