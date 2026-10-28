package facts

import (
	_ "embed"
	"fmt"
	"slices"
	"sort"
	"strings"
	"sync"

	"codeberg.org/TauCeti/mangle-go/ast"
	"codeberg.org/TauCeti/mangle-go/factstore"
	"codeberg.org/TauCeti/mangle-go/parse"
	"codeberg.org/TauCeti/mangle-go/symbols"
	"github.com/bigbizze/lintai/internal/analysis"
	"github.com/bigbizze/lintai/internal/diagnostics"
)

//go:embed schema.mg
var Schema string

//go:embed prelude.mg
var Prelude string

type parsedSources struct {
	schemaDecls []ast.Decl
	prelude     parse.SourceUnit
	err         error
}

var parsed = sync.OnceValue(func() parsedSources {
	schema, err := parse.Unit(strings.NewReader(Schema))
	if err != nil {
		return parsedSources{err: fmt.Errorf("schema.mg: %w", err)}
	}
	prelude, err := parse.Unit(strings.NewReader(Prelude))
	if err != nil {
		return parsedSources{err: fmt.Errorf("prelude.mg: %w", err)}
	}
	return parsedSources{schemaDecls: schema.Decls, prelude: prelude}
})

// SchemaDecls returns a fresh map of the base relation declarations.
func SchemaDecls() (map[ast.PredicateSym]ast.Decl, error) {
	sources := parsed()
	if sources.err != nil {
		return nil, sources.err
	}
	result := make(map[ast.PredicateSym]ast.Decl, len(sources.schemaDecls))
	for _, decl := range sources.schemaDecls {
		if decl.DeclaredAtom.Predicate == symbols.Package || decl.DeclaredAtom.Predicate == symbols.Use {
			continue
		}
		result[decl.DeclaredAtom.Predicate] = decl
	}
	return result, nil
}

// PreludeUnit returns the parsed prelude.
func PreludeUnit() (parse.SourceUnit, error) {
	sources := parsed()
	if sources.err != nil {
		return parse.SourceUnit{}, sources.err
	}
	return sources.prelude, nil
}

// BaseRelations lists the base relation names in schema order.
func BaseRelations() []string {
	sources := parsed()
	names := make([]string, 0, len(sources.schemaDecls))
	for _, decl := range sources.schemaDecls {
		if decl.DeclaredAtom.Predicate == symbols.Package || decl.DeclaredAtom.Predicate == symbols.Use {
			continue
		}
		names = append(names, decl.DeclaredAtom.Predicate.Symbol)
	}
	return names
}

// FromSnapshot converts a snapshot into the base relations.
func FromSnapshot(snapshot *analysis.Snapshot) (factstore.ReadOnlyFactStore, error) {
	b := builder{store: factstore.NewMultiIndexedArrayInMemoryStore()}
	for _, module := range snapshot.Modules {
		b.add("module", str(module.Path))
		b.location(module.Path, module.Range)
	}
	for _, fn := range snapshot.Functions {
		id := str(fn.SemanticKey)
		b.add("fn", id, str(fn.FilePath), str(fn.Name))
		b.add("fn_kind", id, str(fn.Kind))
		if fn.ContainerName != "" {
			b.add("fn_container", id, str(fn.ContainerName))
		}
		if fn.IsExported {
			b.add("fn_exported", id)
		}
		if fn.IsAsync {
			b.add("fn_async", id)
		}
		if fn.ContainsAwait {
			b.add("fn_awaits", id)
		}
		for index, typeText := range fn.ParameterTypeTexts {
			b.add("fn_param", id, ast.Number(int64(index)), str(typeText))
		}
		b.add("fn_returns", id, str(fn.ReturnTypeText))
		b.location(fn.SemanticKey, fn.Range)
	}
	for _, edge := range snapshot.ImportEdges {
		id := str(edge.SemanticKey)
		b.add("import_edge", id, str(edge.FromPath), str(edge.ToPath), str(edge.Specifier))
		if edge.ToPath != edge.Specifier {
			b.add("import_internal", id)
		}
		if edge.IsTypeOnly {
			b.add("import_type_only", id)
		}
		for _, symbol := range edge.ImportedSymbols {
			kind, err := name(symbol.Kind)
			if err != nil {
				return nil, err
			}
			b.add("import_symbol", id, str(symbol.Name), kind)
			if symbol.IsTypeOnly {
				b.add("import_symbol_type_only", id, str(symbol.Name))
			}
		}
		b.location(edge.SemanticKey, edge.Range)
	}
	for _, edge := range snapshot.CallEdges {
		id := str(edge.SemanticKey)
		b.add("call_edge", id, str(edge.FromSemanticKey), str(edge.ToSemanticKey))
		b.add("site_in_fn", id, str(edge.FromSemanticKey))
		b.location(edge.SemanticKey, edge.Range)
	}
	for _, ref := range snapshot.TypeRefs {
		id := str(ref.SemanticKey)
		b.add("type_ref", id, str(ref.FilePath), str(ref.Name))
		if ref.TargetPath != "" {
			b.add("type_ref_target", id, str(ref.TargetPath))
		}
		b.location(ref.SemanticKey, ref.Range)
	}
	bodies := functionBodiesByFile(snapshot.Functions)
	for _, access := range snapshot.Accesses {
		id := str(access.SemanticKey)
		b.add("meta_access", id, str(access.FilePath), str(access.AccessPath))
		if fn := innermostFunction(bodies[access.FilePath], access.Start, access.End); fn != "" {
			b.add("site_in_fn", id, str(fn))
		}
		b.location(access.SemanticKey, access.Range)
	}
	for _, source := range snapshot.AmbientSources {
		sourceName, err := name(source.ID)
		if err != nil {
			return nil, err
		}
		for _, capability := range source.Capabilities {
			capabilityName, err := name(capability)
			if err != nil {
				return nil, err
			}
			b.add("ambient_source", sourceName, capabilityName)
		}
	}
	for _, use := range snapshot.AmbientUses {
		id := str(use.SemanticKey)
		sourceName, err := name(use.Source)
		if err != nil {
			return nil, err
		}
		capabilityName, err := name(use.Capability)
		if err != nil {
			return nil, err
		}
		b.add("ambient_use", id, sourceName, capabilityName, str(use.FilePath))
		if use.HasKey {
			b.add("ambient_key", id, str(use.Key))
		}
		if use.Dynamic {
			b.add("ambient_dynamic", id)
		}
		if use.Write {
			b.add("ambient_write", id)
		}
		if use.FunctionSemanticKey != "" {
			b.add("site_in_fn", id, str(use.FunctionSemanticKey))
		}
		b.location(use.SemanticKey, use.Range)
	}
	return b.store, nil
}

// Format renders the facts of the named relations (all base relations when
// relations is empty) in Mangle syntax, one sorted fact per line.
func Format(store factstore.ReadOnlyFactStore, relations []string) (string, error) {
	decls, err := SchemaDecls()
	if err != nil {
		return "", err
	}
	selected := BaseRelations()
	if len(relations) > 0 {
		for _, relation := range relations {
			if !slices.Contains(selected, relation) {
				return "", fmt.Errorf("unknown base relation %q", relation)
			}
		}
		selected = relations
	}
	lines := make([]string, 0)
	for sym := range decls {
		if !slices.Contains(selected, sym.Symbol) {
			continue
		}
		if err := store.GetFacts(ast.NewQuery(sym), func(atom ast.Atom) error {
			lines = append(lines, atom.String()+".")
			return nil
		}); err != nil {
			return "", err
		}
	}
	sort.Strings(lines)
	return strings.Join(lines, "\n"), nil
}

type builder struct {
	store *factstore.MultiIndexedArrayInMemoryStore
}

func (b builder) add(predicate string, args ...ast.BaseTerm) {
	b.store.Add(ast.NewAtom(predicate, args...))
}

func (b builder) location(entity string, location diagnostics.SourceLocation) {
	b.add("location",
		str(entity),
		str(location.File),
		ast.Number(int64(location.StartLine)),
		ast.Number(int64(location.StartColumn)),
		ast.Number(int64(location.EndLine)),
		ast.Number(int64(location.EndColumn)),
	)
}

func str(value string) ast.Constant {
	return ast.String(value)
}

func name(value string) (ast.Constant, error) {
	constant, err := ast.Name("/" + value)
	if err != nil {
		return ast.Constant{}, fmt.Errorf("invalid name constant %q: %w", value, err)
	}
	return constant, nil
}

type functionBody struct {
	key   string
	start int
	end   int
}

func functionBodiesByFile(functions []analysis.Function) map[string][]functionBody {
	result := make(map[string][]functionBody)
	for _, fn := range functions {
		if fn.BodyEnd <= fn.BodyStart {
			continue
		}
		result[fn.FilePath] = append(result[fn.FilePath], functionBody{key: fn.SemanticKey, start: fn.BodyStart, end: fn.BodyEnd})
	}
	return result
}

func innermostFunction(bodies []functionBody, start, end int) string {
	best := ""
	bestSize := -1
	for _, body := range bodies {
		if start < body.start || end > body.end {
			continue
		}
		size := body.end - body.start
		if bestSize == -1 || size < bestSize {
			best = body.key
			bestSize = size
		}
	}
	return best
}
