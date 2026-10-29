package engine

import (
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"sort"
	"strings"

	mangleanalysis "codeberg.org/TauCeti/mangle-go/analysis"
	"codeberg.org/TauCeti/mangle-go/ast"
	mangleengine "codeberg.org/TauCeti/mangle-go/engine"
	"codeberg.org/TauCeti/mangle-go/factstore"
	"codeberg.org/TauCeti/mangle-go/parse"
	"codeberg.org/TauCeti/mangle-go/provenance"
	"codeberg.org/TauCeti/mangle-go/symbols"
	"github.com/bigbizze/lintai/diagnostics"
	"github.com/bigbizze/lintai/facts"
	"github.com/bigbizze/lintai/runtime"
	"github.com/bmatcuk/doublestar/v4"
)

const DefaultFactLimit = 5_000_000

var globSym = ast.PredicateSym{Symbol: "glob", Arity: 2}

// notGlobSym is the internal predicate that a negated glob premise becomes.
// Mangle cannot negate an external predicate, so !glob(P, X) is evaluated as
// a positive premise that holds when P does not match X.
var notGlobSym = ast.PredicateSym{Symbol: "lintai:not_glob", Arity: 2}

type phaseError struct {
	phase string
	err   error
}

func (e *phaseError) Error() string {
	return e.err.Error()
}

func failPhase(phase string, format string, args ...any) *phaseError {
	return &phaseError{phase: phase, err: fmt.Errorf(format, args...)}
}

type violation struct {
	predicate   string
	anchor      string
	location    diagnostics.SourceLocation
	row         map[string]any
	explanation []string
}

type evalOptions struct {
	factLimit int
	explain   bool
}

func evaluateProgram(base factstore.ReadOnlyFactStore, prelude parse.SourceUnit, schemaDecls map[ast.PredicateSym]ast.Decl, output runtime.ProgramOutput, report, expectNonEmpty []string, options evalOptions) ([]violation, *phaseError) {
	unit, err := parse.Unit(strings.NewReader(splitConstantTerminators(output.Text)))
	if err != nil {
		return nil, failPhase("program_parse", "%v", err)
	}
	if perr := checkReservedPredicates(unit, prelude, schemaDecls); perr != nil {
		return nil, perr
	}
	ruleFacts, perr := ruleFactStore(unit, output.Facts)
	if perr != nil {
		return nil, perr
	}
	if perr := checkGlobUses(unit); perr != nil {
		return nil, perr
	}
	rewriteNegatedGlobs(&unit)
	if perr := checkNegatedWildcards(unit); perr != nil {
		return nil, perr
	}
	prelude = preludeFor(prelude, unit, append(append([]string(nil), report...), expectNonEmpty...))
	info, err := mangleanalysis.Analyze([]parse.SourceUnit{prelude, unit}, schemaDecls)
	if err != nil {
		return nil, failPhase("program_analysis", "%v", err)
	}
	reportDecls := make([]ast.Decl, 0, len(report))
	for _, name := range report {
		decl, ok := findDecl(unit.Decls, name)
		if !ok {
			return nil, failPhase("program_analysis", "reported predicate %q has no Decl in the program", name)
		}
		reportDecls = append(reportDecls, decl)
	}
	expectSyms := make([]ast.PredicateSym, 0, len(expectNonEmpty))
	for _, name := range expectNonEmpty {
		sym, ok := findSym(info.Decls, name)
		if !ok {
			return nil, failPhase("program_analysis", "expectNonEmpty predicate %q does not exist", name)
		}
		expectSyms = append(expectSyms, sym)
	}
	strata, predToStratum, err := mangleanalysis.Stratify(mangleanalysis.Program{
		EdbPredicates: info.EdbPredicates,
		IdbPredicates: info.IdbPredicates,
		Rules:         info.Rules,
	})
	if err != nil {
		return nil, failPhase("program_analysis", "%v", err)
	}

	derived := factstore.NewMultiIndexedArrayInMemoryStore()
	for _, fact := range info.InitialFacts {
		derived.Add(fact)
	}
	store := factstore.NewMergedStore([]factstore.ReadOnlyFactStore{base, ruleFacts}, derived)
	if err := runEvaluation(info, strata, predToStratum, store, options.factLimit); err != nil {
		return nil, failPhase("evaluation", "%v", err)
	}

	empty := make([]string, 0)
	for _, sym := range expectSyms {
		if !hasFacts(store, sym) {
			empty = append(empty, sym.Symbol)
		}
	}
	if len(empty) > 0 {
		return nil, failPhase("premise", "expectNonEmpty: these predicates have no facts: %s", strings.Join(empty, ", "))
	}

	violations := make([]violation, 0)
	for _, decl := range reportDecls {
		sym := decl.DeclaredAtom.Predicate
		argNames := make([]string, 0, sym.Arity)
		for _, arg := range decl.DeclaredAtom.Args {
			argNames = append(argNames, fmt.Sprint(arg))
		}
		rows := make([]ast.Atom, 0)
		if err := store.GetFacts(ast.NewQuery(sym), func(atom ast.Atom) error {
			rows = append(rows, atom)
			return nil
		}); err != nil {
			return nil, failPhase("report", "%v", err)
		}
		for _, atom := range rows {
			if sym.Arity == 0 {
				return nil, failPhase("report", "reported predicate %s has no anchor argument", sym.Symbol)
			}
			anchorConstant, _ := atom.Args[0].(ast.Constant)
			anchor := anchorString(anchorConstant)
			location, ok := lookupLocation(base, anchorConstant)
			if !ok {
				return nil, failPhase("report", "anchor %s of %s has no location fact", anchorConstant, atom)
			}
			row := make(map[string]any, len(argNames))
			for index, argName := range argNames {
				constant, _ := atom.Args[index].(ast.Constant)
				row[argName] = decodeConstant(constant)
			}
			item := violation{predicate: sym.Symbol, anchor: anchor, location: location, row: row}
			if options.explain {
				item.explanation = explain(info, store, atom)
			}
			violations = append(violations, item)
		}
	}
	return violations, nil
}

func runEvaluation(info *mangleanalysis.ProgramInfo, strata []mangleanalysis.Nodeset, predToStratum map[ast.PredicateSym]int, store factstore.FactStore, factLimit int) (err error) {
	defer func() {
		if recovered := recover(); recovered != nil {
			err = fmt.Errorf("evaluation failed: %v", recovered)
		}
	}()
	if factLimit <= 0 {
		factLimit = DefaultFactLimit
	}
	externalPredicates := make(map[ast.PredicateSym]mangleengine.ExternalPredicateCallback)
	if decl := info.Decls[globSym]; decl != nil && decl.IsExternal() {
		externalPredicates[globSym] = globPredicate{}
	}
	if decl := info.Decls[notGlobSym]; decl != nil && decl.IsExternal() {
		externalPredicates[notGlobSym] = globPredicate{negated: true}
	}
	_, err = mangleengine.EvalStratifiedProgramWithStats(info, strata, predToStratum, store,
		mangleengine.WithDeterministicOrder(),
		mangleengine.WithCreatedFactLimit(factLimit),
		mangleengine.WithExternalPredicates(externalPredicates),
	)
	return err
}

type globPredicate struct {
	negated bool
}

func (globPredicate) ShouldPushdown() bool {
	return false
}

func (globPredicate) ShouldQuery([]ast.Constant, []ast.BaseTerm, []ast.Term) bool {
	return true
}

func (g globPredicate) ExecuteQuery(inputs []ast.Constant, _ []ast.BaseTerm, _ []ast.Term, cb func([]ast.BaseTerm)) error {
	path, err := inputs[0].StringValue()
	if err != nil {
		if g.negated {
			cb(nil)
		}
		return nil
	}
	pattern, err := inputs[1].StringValue()
	if err != nil {
		return fmt.Errorf("glob pattern must be a string, got %v", inputs[1])
	}
	matched, err := doublestar.Match(pattern, path)
	if err != nil {
		return fmt.Errorf("glob pattern %q: %w", pattern, err)
	}
	if matched != g.negated {
		cb(nil)
	}
	return nil
}

// splitConstantTerminators puts a space between a name constant and the
// clause-ending dot, so that Layer != /composition. parses. The Mangle lexer
// allows "." inside a name constant, so it reads "/composition." as one
// constant. A dot is split off only when whitespace, a comment or the end of
// the text follows it. Strings and comments are not changed. When a space or
// a tab follows, the dot and that character change places, so the columns of
// later tokens do not change.
func splitConstantTerminators(text string) string {
	var out strings.Builder
	out.Grow(len(text) + 8)
	isConstantChar := func(c byte) bool {
		return c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || strings.IndexByte(".-_~%/", c) >= 0
	}
	for i := 0; i < len(text); {
		c := text[i]
		switch {
		case c == '#':
			end := strings.IndexByte(text[i:], '\n')
			if end < 0 {
				end = len(text) - i
			}
			out.WriteString(text[i : i+end])
			i += end
		case c == '"' || c == '\'' || c == '`':
			j := i + 1
			for j < len(text) && text[j] != c {
				if text[j] == '\\' {
					j++
				}
				j++
			}
			j = min(j+1, len(text))
			out.WriteString(text[i:j])
			i = j
		case c == '/':
			j := i + 1
			for j < len(text) && isConstantChar(text[j]) {
				j++
			}
			token := text[i:j]
			switch {
			case len(token) <= 2 || token[len(token)-1] != '.':
				out.WriteString(token)
			case j < len(text) && (text[j] == ' ' || text[j] == '\t'):
				out.WriteString(token[:len(token)-1])
				out.WriteByte(text[j])
				out.WriteByte('.')
				j++
			case j == len(text) || strings.IndexByte("\r\n\f#", text[j]) >= 0:
				out.WriteString(token[:len(token)-1])
				out.WriteString(" .")
			default:
				out.WriteString(token)
			}
			i = j
		default:
			out.WriteByte(c)
			i++
		}
	}
	return out.String()
}

const notGlobDecl = `Decl lintai:not_glob(Path, Pattern) descr [external(), mode('+', '+')].`

// rewriteNegatedGlobs replaces each !glob(P, X) premise with the internal
// positive premise lintai:not_glob(P, X), and declares that predicate when
// the program uses it.
func rewriteNegatedGlobs(unit *parse.SourceUnit) {
	rewritten := false
	for ci := range unit.Clauses {
		premises := unit.Clauses[ci].Premises
		for pi, premise := range premises {
			if negated, ok := premise.(ast.NegAtom); ok && negated.Atom.Predicate == globSym {
				premises[pi] = ast.Atom{Predicate: notGlobSym, Args: negated.Atom.Args}
				rewritten = true
			}
		}
	}
	if !rewritten {
		return
	}
	decls, err := parse.Unit(strings.NewReader(notGlobDecl))
	if err != nil {
		panic(err)
	}
	unit.Decls = append(unit.Decls, decls.Decls...)
}

// preludeFor keeps the prelude declarations and only the prelude clauses that
// the rule program depends on, so a rule does not derive prelude relations
// that it never reads.
func preludeFor(prelude, unit parse.SourceUnit, roots []string) parse.SourceUnit {
	clausesByHead := make(map[string][]ast.Clause)
	for _, clause := range prelude.Clauses {
		clausesByHead[clause.Head.Predicate.Symbol] = append(clausesByHead[clause.Head.Predicate.Symbol], clause)
	}
	needed := make(map[string]struct{})
	pending := append([]string(nil), roots...)
	addPremises := func(clause ast.Clause) {
		for _, premise := range clause.Premises {
			switch typed := premise.(type) {
			case ast.Atom:
				pending = append(pending, typed.Predicate.Symbol)
			case ast.NegAtom:
				pending = append(pending, typed.Atom.Predicate.Symbol)
			}
		}
	}
	for _, clause := range unit.Clauses {
		addPremises(clause)
	}
	for len(pending) > 0 {
		symbol := pending[len(pending)-1]
		pending = pending[:len(pending)-1]
		if _, seen := needed[symbol]; seen {
			continue
		}
		needed[symbol] = struct{}{}
		for _, clause := range clausesByHead[symbol] {
			addPremises(clause)
		}
	}
	clauses := make([]ast.Clause, 0, len(prelude.Clauses))
	for _, clause := range prelude.Clauses {
		if _, ok := needed[clause.Head.Predicate.Symbol]; ok {
			clauses = append(clauses, clause)
		}
	}
	return parse.SourceUnit{Decls: prelude.Decls, Clauses: clauses}
}

// checkReservedPredicates rejects rule declarations and clauses for base and
// prelude predicates. mangle-go accepts facts for declared extensional
// predicates, so the check is done here.
func checkReservedPredicates(unit, prelude parse.SourceUnit, schemaDecls map[ast.PredicateSym]ast.Decl) *phaseError {
	reserved := make(map[string]string)
	for sym := range schemaDecls {
		reserved[sym.Symbol] = "base relation"
	}
	for _, decl := range prelude.Decls {
		if isPackageDecl(decl) {
			continue
		}
		reserved[decl.DeclaredAtom.Predicate.Symbol] = "prelude predicate"
	}
	extensional := make(map[string]struct{})
	for _, decl := range unit.Decls {
		if isPackageDecl(decl) {
			continue
		}
		symbol := decl.DeclaredAtom.Predicate.Symbol
		if kind, ok := reserved[symbol]; ok {
			return failPhase("program_analysis", "cannot redeclare %s %s", kind, symbol)
		}
		if decl.IsExtensional() {
			extensional[symbol] = struct{}{}
		}
	}
	for _, clause := range unit.Clauses {
		symbol := clause.Head.Predicate.Symbol
		if kind, ok := reserved[symbol]; ok {
			return failPhase("program_analysis", "cannot redefine %s %s in clause %v", kind, symbol, clause)
		}
		if _, ok := extensional[symbol]; ok {
			return failPhase("program_analysis", "extensional predicate %s cannot have clauses; supply its facts with .facts()", symbol)
		}
	}
	return nil
}

// checkGlobUses rejects glob premises that mangle-go cannot evaluate: a glob
// or a negated glob whose arguments are not bound by an earlier premise.
func checkGlobUses(unit parse.SourceUnit) *phaseError {
	for _, clause := range unit.Clauses {
		bound := make(map[ast.Variable]struct{})
		checkBound := func(args []ast.BaseTerm) *phaseError {
			for _, arg := range args {
				if variable, ok := arg.(ast.Variable); ok {
					if _, isBound := bound[variable]; !isBound {
						return failPhase("program_analysis", "glob argument %v must be bound by an earlier premise in %v", variable, clause)
					}
				}
			}
			return nil
		}
		for _, premise := range clause.Premises {
			switch typed := premise.(type) {
			case ast.NegAtom:
				if typed.Atom.Predicate == globSym {
					if perr := checkBound(typed.Atom.Args); perr != nil {
						return perr
					}
				}
			case ast.Atom:
				if typed.Predicate == globSym {
					if perr := checkBound(typed.Args); perr != nil {
						return perr
					}
					continue
				}
				for _, arg := range typed.Args {
					if variable, ok := arg.(ast.Variable); ok {
						bound[variable] = struct{}{}
					}
				}
			case ast.Eq:
				if variable, ok := typed.Left.(ast.Variable); ok {
					bound[variable] = struct{}{}
				}
				if variable, ok := typed.Right.(ast.Variable); ok {
					bound[variable] = struct{}{}
				}
			}
		}
	}
	return nil
}

// checkNegatedWildcards rejects _ in a negated atom. Mangle replaces each _
// with a generated variable and then reports that variable as unbound, which
// does not point at the cause.
func checkNegatedWildcards(unit parse.SourceUnit) *phaseError {
	for _, clause := range unit.Clauses {
		for _, premise := range clause.Premises {
			negated, ok := premise.(ast.NegAtom)
			if !ok {
				continue
			}
			for _, arg := range negated.Atom.Args {
				if variable, ok := arg.(ast.Variable); ok && variable.Symbol == "_" {
					return failPhase("program_analysis", "_ cannot be used in the negated atom !%v in %v; every variable in a negated atom must be bound. Define a helper predicate without the unused argument and negate the helper", negated.Atom, clause)
				}
			}
		}
	}
	return nil
}

func ruleFactStore(unit parse.SourceUnit, relations map[string][][]any) (factstore.ReadOnlyFactStore, *phaseError) {
	store := factstore.NewMultiIndexedArrayInMemoryStore()
	names := make([]string, 0, len(relations))
	for name := range relations {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		decl, ok := findDecl(unit.Decls, name)
		if !ok || !decl.IsExtensional() {
			return nil, failPhase("program_analysis", "facts() relation %q must be declared in the program with descr [extensional()]", name)
		}
		arity := decl.DeclaredAtom.Predicate.Arity
		for rowIndex, row := range relations[name] {
			if len(row) != arity {
				return nil, failPhase("program_analysis", "facts() relation %s has arity %d, but row %d has %d values", name, arity, rowIndex, len(row))
			}
			args := make([]ast.BaseTerm, 0, arity)
			for _, cell := range row {
				constant, err := encodeCell(cell)
				if err != nil {
					return nil, failPhase("program_analysis", "facts() relation %s row %d: %v", name, rowIndex, err)
				}
				args = append(args, constant)
			}
			store.Add(ast.NewAtom(name, args...))
		}
	}
	return store, nil
}

func encodeCell(cell any) (ast.Constant, error) {
	switch typed := cell.(type) {
	case string:
		return ast.String(typed), nil
	case json.Number:
		if integer, err := typed.Int64(); err == nil {
			return ast.Number(integer), nil
		}
		floating, err := typed.Float64()
		if err != nil || math.IsInf(floating, 0) || math.IsNaN(floating) {
			return ast.Constant{}, fmt.Errorf("invalid number %s", typed)
		}
		return ast.Float64(floating), nil
	case runtime.Name:
		return ast.Name("/" + typed.Value)
	}
	return ast.Constant{}, fmt.Errorf("unsupported value %v", cell)
}

func decodeConstant(constant ast.Constant) any {
	switch constant.Type {
	case ast.StringType:
		value, _ := constant.StringValue()
		return value
	case ast.NumberType:
		value, _ := constant.NumberValue()
		return value
	case ast.Float64Type:
		value, _ := constant.Float64Value()
		return value
	case ast.NameType:
		value, _ := constant.NameValue()
		return value
	}
	return constant.String()
}

func anchorString(constant ast.Constant) string {
	if constant.Type == ast.StringType {
		value, _ := constant.StringValue()
		return value
	}
	return constant.String()
}

func lookupLocation(base factstore.ReadOnlyFactStore, anchor ast.Constant) (diagnostics.SourceLocation, bool) {
	if anchor.Type != ast.StringType {
		return diagnostics.SourceLocation{}, false
	}
	query := ast.NewQuery(ast.PredicateSym{Symbol: facts.LocationRelation, Arity: 6})
	query.Args[0] = anchor
	var found *diagnostics.SourceLocation
	_ = base.GetFacts(query, func(atom ast.Atom) error {
		file, _ := atom.Args[1].(ast.Constant).StringValue()
		number := func(index int) int {
			value, _ := atom.Args[index].(ast.Constant).NumberValue()
			return int(value)
		}
		location := diagnostics.SourceLocation{
			File:        file,
			StartLine:   number(2),
			StartColumn: number(3),
			EndLine:     number(4),
			EndColumn:   number(5),
		}
		if found == nil || compareLocations(location, *found) < 0 {
			found = &location
		}
		return nil
	})
	if found == nil {
		return diagnostics.SourceLocation{}, false
	}
	return *found, true
}

func compareLocations(left, right diagnostics.SourceLocation) int {
	if c := strings.Compare(left.File, right.File); c != 0 {
		return c
	}
	for _, pair := range [][2]int{
		{left.StartLine, right.StartLine},
		{left.StartColumn, right.StartColumn},
		{left.EndLine, right.EndLine},
		{left.EndColumn, right.EndColumn},
	} {
		if pair[0] != pair[1] {
			return pair[0] - pair[1]
		}
	}
	return 0
}

func hasFacts(store factstore.ReadOnlyFactStore, sym ast.PredicateSym) bool {
	errFound := errors.New("found")
	err := store.GetFacts(ast.NewQuery(sym), func(ast.Atom) error {
		return errFound
	})
	return errors.Is(err, errFound)
}

func isPackageDecl(decl ast.Decl) bool {
	return decl.DeclaredAtom.Predicate == symbols.Package || decl.DeclaredAtom.Predicate == symbols.Use
}

func findDecl(decls []ast.Decl, name string) (ast.Decl, bool) {
	for _, decl := range decls {
		if decl.DeclaredAtom.Predicate.Symbol == name {
			return decl, true
		}
	}
	return ast.Decl{}, false
}

func findSym(decls map[ast.PredicateSym]*ast.Decl, name string) (ast.PredicateSym, bool) {
	for sym := range decls {
		if sym.Symbol == name {
			return sym, true
		}
	}
	return ast.PredicateSym{}, false
}

// explain renders the proof of a reported atom as ground atoms in depth-first
// order. Several proofs are requested and the smallest rendering is kept, so
// the output does not depend on fact store iteration order.
func explain(info *mangleanalysis.ProgramInfo, store factstore.ReadOnlyFactStore, goal ast.Atom) []string {
	proofs, err := provenance.Explain(info, store, goal, provenance.Options{MaxProofs: 16})
	if err != nil || len(proofs) == 0 {
		return []string{goal.String()}
	}
	var best []string
	for _, proof := range proofs {
		lines := make([]string, 0)
		renderProof(proof, &lines)
		if best == nil || len(lines) < len(best) || (len(lines) == len(best) && strings.Join(lines, "\n") < strings.Join(best, "\n")) {
			best = lines
		}
	}
	return best
}

func renderProof(node *provenance.ProofNode, lines *[]string) {
	fact := node.Fact
	negated := node.Kind == provenance.KindAbsence
	if fact.Predicate == notGlobSym {
		fact = ast.Atom{Predicate: globSym, Args: fact.Args}
		negated = true
	}
	text := fact.String()
	if negated {
		text = "!" + text
	}
	*lines = append(*lines, text)
	for _, premise := range node.Premises {
		renderProof(premise, lines)
	}
}
