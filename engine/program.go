package engine

import (
	"cmp"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"slices"
	"sort"
	"strconv"
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
	"github.com/bigbizze/lintai/internal/canonical"
	"github.com/bigbizze/lintai/runtime"
	"github.com/bmatcuk/doublestar/v4"
)

const DefaultFactLimit = 5_000_000

var globSym = ast.PredicateSym{Symbol: "lintai.glob", Arity: 2}

// notGlobSym is the internal predicate that a negated glob premise becomes.
// Mangle cannot negate an external predicate, so !lintai.glob(P, X) is evaluated as
// a positive premise that holds when P does not match X.
var notGlobSym = ast.PredicateSym{Symbol: "lintai.not_glob", Arity: 2}

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
	ruleID      string
	predicate   string
	anchor      string
	location    diagnostics.SourceLocation
	row         map[string]any
	typedRow    []facts.Value
	columns     []string
	explanation []string
	by          []string
	diagnostic  diagnostics.Diagnostic
}

type evalOptions struct {
	factLimit    int
	explain      bool
	createdFacts *int64
	// beforeEvaluation is an in-package synchronization point for tests. It is
	// called from the production evaluator immediately before Mangle runs.
	beforeEvaluation func()
	onDerivedFact    func()
}

type suppressionPolicy struct {
	assertion string
	by        []string
}

// A retained plan contains immutable compilation results and read-only facts;
// evaluation stores and accounting are created separately for each run.
type rulePlan struct {
	info             *mangleanalysis.ProgramInfo
	strata           []mangleanalysis.Nodeset
	predToStratum    map[ast.PredicateSym]int
	ruleFacts        factstore.ReadOnlyFactStore
	basePredicates   map[ast.PredicateSym]struct{}
	reportDecls      []ast.Decl
	expectSyms       []ast.PredicateSym
	suppressionRules []suppressionPolicy
}

type restrictedFactStore struct {
	base  factstore.ReadOnlyFactStore
	allow map[ast.PredicateSym]struct{}
	list  []ast.PredicateSym
	count int
}

func newRestrictedFactStore(base factstore.ReadOnlyFactStore, allowed map[ast.PredicateSym]struct{}, counts map[ast.PredicateSym]int) restrictedFactStore {
	list := make([]ast.PredicateSym, 0, len(allowed))
	count := 0
	for predicate := range allowed {
		list = append(list, predicate)
		if value, known := counts[predicate]; known {
			count += value
			continue
		}
		_ = base.GetFacts(ast.NewQuery(predicate), func(ast.Atom) error {
			count++
			return nil
		})
	}
	sort.Slice(list, func(i, j int) bool {
		if list[i].Symbol != list[j].Symbol {
			return list[i].Symbol < list[j].Symbol
		}
		return list[i].Arity < list[j].Arity
	})
	return restrictedFactStore{base: base, allow: allowed, list: list, count: count}
}

func (view restrictedFactStore) GetFacts(query ast.Atom, callback func(ast.Atom) error) error {
	if _, allowed := view.allow[query.Predicate]; !allowed {
		return nil
	}
	return view.base.GetFacts(query, callback)
}

func (view restrictedFactStore) Contains(atom ast.Atom) bool {
	if _, allowed := view.allow[atom.Predicate]; !allowed {
		return false
	}
	return view.base.Contains(atom)
}

func (view restrictedFactStore) ListPredicates() []ast.PredicateSym {
	return slices.Clone(view.list)
}

func (view restrictedFactStore) EstimateFactCount() int {
	return view.count
}

type createdFactStore struct {
	factstore.FactStore
	count         int64
	onDerivedFact func()
}

func (store *createdFactStore) Add(atom ast.Atom) bool {
	added := store.FactStore.Add(atom)
	if added {
		store.count++
		if hook := store.onDerivedFact; hook != nil {
			store.onDerivedFact = nil
			hook()
		}
	}
	return added
}

func evaluateProgram(base factstore.ReadOnlyFactStore, catalog []parse.SourceUnit, schemaDecls map[ast.PredicateSym]ast.Decl, anchors map[string]facts.AnchorSupport, reportable map[facts.Predicate]map[string]struct{}, namespaces []string, output runtime.ProgramOutput, report, expectNonEmpty []string, options evalOptions) ([]violation, *phaseError) {
	plan, perr := prepareProgram(catalog, schemaDecls, namespaces, output, report, expectNonEmpty, nil)
	if perr != nil {
		return nil, perr
	}
	return plan.evaluate(base, nil, anchors, reportable, options)
}

func prepareProgram(catalog []parse.SourceUnit, schemaDecls map[ast.PredicateSym]ast.Decl, namespaces []string, output runtime.ProgramOutput, report, expectNonEmpty []string, suppressionDeclarations []runtime.SuppressionDeclaration) (*rulePlan, *phaseError) {
	unit, err := parse.Unit(strings.NewReader(splitConstantTerminators(output.Text)))
	if err != nil {
		return nil, failPhase("program_parse", "%v", err)
	}
	if perr := bindRuleUnit(&unit, output.Text, namespaces); perr != nil {
		return nil, perr
	}
	if perr := checkReservedPredicates(unit, schemaDecls); perr != nil {
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
	catalogView, requiredPredicates := catalogForRule(catalog, unit, append(slices.Clone(report), expectNonEmpty...))
	program := append(catalogView, unit)
	if perr := checkPureTimeFunctions(program); perr != nil {
		return nil, perr
	}
	info, err := mangleanalysis.Analyze(program, nil)
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
	sort.Slice(reportDecls, func(i, j int) bool {
		if reportDecls[i].DeclaredAtom.Predicate.Symbol != reportDecls[j].DeclaredAtom.Predicate.Symbol {
			return reportDecls[i].DeclaredAtom.Predicate.Symbol < reportDecls[j].DeclaredAtom.Predicate.Symbol
		}
		return reportDecls[i].DeclaredAtom.Predicate.Arity < reportDecls[j].DeclaredAtom.Predicate.Arity
	})
	policies, perr := validateSuppressionPolicies(suppressionDeclarations, reportDecls)
	if perr != nil {
		return nil, perr
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
	basePredicates := make(map[ast.PredicateSym]struct{})
	for predicate, declaration := range schemaDecls {
		if declaration.IsExtensional() && !strings.HasPrefix(predicate.Symbol, "lintai.") {
			if _, needed := requiredPredicates[predicate.Symbol]; needed {
				basePredicates[predicate] = struct{}{}
			}
		}
	}
	return &rulePlan{
		info: info, strata: strata, predToStratum: predToStratum, ruleFacts: ruleFacts,
		basePredicates: basePredicates, reportDecls: reportDecls, expectSyms: expectSyms,
		suppressionRules: policies,
	}, nil
}

func generatedRulePlanKey(catalogIdentity string, output runtime.ProgramOutput, report, expectNonEmpty []string, suppressions []runtime.SuppressionDeclaration) (string, error) {
	relationNames := make([]string, 0, len(output.Facts))
	for name := range output.Facts {
		relationNames = append(relationNames, name)
	}
	sort.Strings(relationNames)
	factRecords := make([]any, 0, len(relationNames))
	for _, name := range relationNames {
		rows := make([]string, 0, len(output.Facts[name]))
		for _, row := range output.Facts[name] {
			values := make([]any, len(row))
			for index, cell := range row {
				constant, err := encodeCell(cell)
				if err != nil {
					return "", err
				}
				values[index] = canonicalFactValue(constant)
			}
			encoded, err := canonical.Marshal(values)
			if err != nil {
				return "", err
			}
			rows = append(rows, string(encoded))
		}
		sort.Strings(rows)
		rowRecords := make([]any, len(rows))
		for index, row := range rows {
			rowRecords[index] = row
		}
		factRecords = append(factRecords, []any{name, rowRecords})
	}
	policyRecords := make([]any, len(suppressions))
	for index, suppression := range suppressions {
		options, err := canonical.NormalizeSuppressionOptions(suppression.Options)
		if err != nil {
			return "", err
		}
		policyRecords[index] = []any{suppression.Assertion, options}
	}
	encoded, err := canonical.MarshalStructured([]any{
		"rule-plan-v1", catalogIdentity, output.Text, factRecords,
		report, expectNonEmpty, policyRecords,
	})
	if err != nil {
		return "", err
	}
	digest := sha256.Sum256(encoded)
	return hex.EncodeToString(digest[:]), nil
}

func (plan *rulePlan) evaluate(base factstore.ReadOnlyFactStore, factCounts map[ast.PredicateSym]int, anchors map[string]facts.AnchorSupport, reportable map[facts.Predicate]map[string]struct{}, options evalOptions) ([]violation, *phaseError) {
	derived := &createdFactStore{FactStore: factstore.NewMultiIndexedArrayInMemoryStore()}
	for _, fact := range plan.info.InitialFacts {
		derived.Add(fact)
	}
	derived.onDerivedFact = options.onDerivedFact
	baseView := newRestrictedFactStore(base, plan.basePredicates, factCounts)
	store := factstore.NewMergedStore([]factstore.ReadOnlyFactStore{baseView, plan.ruleFacts}, derived)
	evaluationErr := runEvaluation(plan.info, plan.strata, plan.predToStratum, store, options.factLimit, options.beforeEvaluation)
	if options.createdFacts != nil {
		*options.createdFacts = derived.count
	}
	if evaluationErr != nil {
		return nil, failPhase("evaluation", "%v", evaluationErr)
	}

	empty := make([]string, 0)
	for _, sym := range plan.expectSyms {
		if !hasFacts(store, sym) {
			empty = append(empty, sym.Symbol)
		}
	}
	if len(empty) > 0 {
		return nil, failPhase("premise", "expectNonEmpty: these predicates have no facts: %s", strings.Join(empty, ", "))
	}
	eligibleAnchors := make(map[string]struct{})
	for predicate, eligible := range reportable {
		if _, required := plan.basePredicates[ast.PredicateSym{Symbol: predicate.Name, Arity: predicate.Arity}]; !required {
			continue
		}
		for id := range eligible {
			if _, exists := anchors[id]; exists {
				eligibleAnchors[id] = struct{}{}
			}
		}
	}

	violations := make([]violation, 0)
	for _, decl := range plan.reportDecls {
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
		if err := sortReportRows(rows); err != nil {
			return nil, failPhase("report", "%v", err)
		}
		for _, atom := range rows {
			if sym.Arity == 0 {
				return nil, failPhase("report", "reported predicate %s has no anchor argument", sym.Symbol)
			}
			anchorConstant, _ := atom.Args[0].(ast.Constant)
			anchor := anchorString(anchorConstant)
			if _, eligible := eligibleAnchors[anchor]; !eligible {
				return nil, failPhase("report", "anchor %s of %s has no eligible producer-owned location", anchorConstant, atom)
			}
			support := anchors[anchor]
			row := make(map[string]any, len(argNames))
			typedRow := make([]facts.Value, len(atom.Args))
			for index, argName := range argNames {
				constant, ok := atom.Args[index].(ast.Constant)
				if !ok {
					return nil, failPhase("report", "reported row %s contains a non-constant argument", atom)
				}
				value, err := factValue(constant)
				if err != nil {
					return nil, failPhase("report", "%v", err)
				}
				typedRow[index] = value
				row[argName] = decodeConstant(constant)
			}
			location := support.Location
			var by []string
			for _, policy := range plan.suppressionRules {
				if policy.assertion == sym.Symbol {
					by = append([]string(nil), policy.by...)
					break
				}
			}
			for _, column := range by {
				for i, name := range argNames {
					if name == column {
						if _, err := typedReason(typedRow[i]); err != nil {
							return nil, failPhase("report", "%s: %v", column, err)
						}
						break
					}
				}
			}
			item := violation{predicate: sym.Symbol, anchor: anchor, location: location, row: row, typedRow: typedRow, columns: append([]string(nil), argNames...), by: by}
			if options.explain {
				item.explanation = explain(plan.info, store, atom)
			}
			violations = append(violations, item)
		}
	}
	return violations, nil
}

func runEvaluation(info *mangleanalysis.ProgramInfo, strata []mangleanalysis.Nodeset, predToStratum map[ast.PredicateSym]int, store factstore.FactStore, factLimit int, beforeEvaluation func()) (err error) {
	defer func() {
		if recovered := recover(); recovered != nil {
			err = fmt.Errorf("evaluation failed: %v", recovered)
		}
	}()
	if factLimit <= 0 {
		factLimit = DefaultFactLimit
	}
	if beforeEvaluation != nil {
		beforeEvaluation()
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

func checkPureTimeFunctions(units []parse.SourceUnit) *phaseError {
	forbidden := map[string]struct{}{
		"fn:time:now": {}, "fn:time:parse_civil": {}, "fn:time:format_civil": {},
		"fn:time:trunc_civil": {}, "fn:time:add_civil": {}, "fn:time:weekday_civil": {},
	}
	var visitTerm func(ast.BaseTerm) *phaseError
	visitTerm = func(term ast.BaseTerm) *phaseError {
		call, ok := term.(ast.ApplyFn)
		if !ok {
			return nil
		}
		if _, denied := forbidden[call.Function.Symbol]; denied {
			return failPhase("program_analysis", "%s is unavailable in the pure runtime; supply time as explicit data", call.Function.Symbol)
		}
		for _, argument := range call.Args {
			if perr := visitTerm(argument); perr != nil {
				return perr
			}
		}
		return nil
	}
	visitAtom := func(atom ast.Atom) *phaseError {
		for _, argument := range atom.Args {
			if perr := visitTerm(argument); perr != nil {
				return perr
			}
		}
		return nil
	}
	var visitTransform func(*ast.Transform) *phaseError
	visitTransform = func(transform *ast.Transform) *phaseError {
		for _, statement := range transform.Statements {
			if _, denied := forbidden[statement.Fn.Function.Symbol]; denied {
				return failPhase("program_analysis", "%s is unavailable in the pure runtime; supply time as explicit data", statement.Fn.Function.Symbol)
			}
			for _, argument := range statement.Fn.Args {
				if perr := visitTerm(argument); perr != nil {
					return perr
				}
			}
		}
		if transform.Next != nil {
			return visitTransform(transform.Next)
		}
		return nil
	}
	for _, unit := range units {
		for _, clause := range unit.Clauses {
			if perr := visitAtom(clause.Head); perr != nil {
				return perr
			}
			for _, premise := range clause.Premises {
				switch typed := premise.(type) {
				case ast.Atom:
					if perr := visitAtom(typed); perr != nil {
						return perr
					}
				case ast.NegAtom:
					if perr := visitAtom(typed.Atom); perr != nil {
						return perr
					}
				case ast.Eq:
					if perr := visitTerm(typed.Left); perr != nil {
						return perr
					}
					if perr := visitTerm(typed.Right); perr != nil {
						return perr
					}
				case ast.Ineq:
					if perr := visitTerm(typed.Left); perr != nil {
						return perr
					}
					if perr := visitTerm(typed.Right); perr != nil {
						return perr
					}
				}
			}
			if clause.Transform != nil {
				if perr := visitTransform(clause.Transform); perr != nil {
					return perr
				}
			}
		}
	}
	return nil
}

func validateSuppressionPolicies(declarations []runtime.SuppressionDeclaration, reportDecls []ast.Decl) ([]suppressionPolicy, *phaseError) {
	reports := make(map[string]ast.Decl, len(reportDecls))
	for _, declaration := range reportDecls {
		name := declaration.DeclaredAtom.Predicate.Symbol
		if _, duplicate := reports[name]; duplicate {
			return nil, failPhase("program_analysis", "reported predicate %q is declared more than once", name)
		}
		reports[name] = declaration
	}
	byAssertion := make(map[string][]string, len(declarations))
	for _, declaration := range declarations {
		if declaration.Assertion == "" {
			return nil, failPhase("program_analysis", "suppression declaration has an empty assertion")
		}
		if _, duplicate := byAssertion[declaration.Assertion]; duplicate {
			return nil, failPhase("program_analysis", "suppression declaration for %q is duplicated", declaration.Assertion)
		}
		report, reported := reports[declaration.Assertion]
		if !reported {
			return nil, failPhase("program_analysis", "suppression declaration names unknown or unreported predicate %q", declaration.Assertion)
		}
		options, ok := declaration.Options.(map[string]any)
		if !ok {
			return nil, failPhase("program_analysis", "suppression declaration for %q must be an options object", declaration.Assertion)
		}
		for key := range options {
			if key != "by" {
				return nil, failPhase("program_analysis", "suppression declaration for %q has unknown option %q", declaration.Assertion, key)
			}
		}
		var columns []string
		if raw, exists := options["by"]; exists {
			values, ok := raw.([]any)
			if !ok {
				return nil, failPhase("program_analysis", "suppression declaration for %q: by must be an array of column names", declaration.Assertion)
			}
			columns = make([]string, len(values))
			seen := make(map[string]struct{}, len(values))
			argNames := make([]string, len(report.DeclaredAtom.Args))
			for index, arg := range report.DeclaredAtom.Args {
				argNames[index] = fmt.Sprint(arg)
			}
			for index, value := range values {
				column, ok := value.(string)
				if !ok || column == "" {
					return nil, failPhase("program_analysis", "suppression declaration for %q: by entries must be nonempty column names", declaration.Assertion)
				}
				if _, duplicate := seen[column]; duplicate {
					return nil, failPhase("program_analysis", "suppression declaration for %q selects column %q more than once", declaration.Assertion, column)
				}
				seen[column] = struct{}{}
				if len(argNames) > 0 && column == argNames[0] {
					return nil, failPhase("program_analysis", "suppression declaration for %q cannot select its anchor column %q", declaration.Assertion, column)
				}
				matches := 0
				for _, name := range argNames {
					if name == column {
						matches++
					}
				}
				if matches == 0 {
					return nil, failPhase("program_analysis", "suppression declaration for %q selects unknown column %q", declaration.Assertion, column)
				}
				if matches > 1 {
					return nil, failPhase("program_analysis", "suppression declaration for %q selects ambiguous column %q", declaration.Assertion, column)
				}
				columns[index] = column
			}
		}
		byAssertion[declaration.Assertion] = columns
	}
	policies := make([]suppressionPolicy, 0, len(reportDecls))
	for _, declaration := range reportDecls {
		name := declaration.DeclaredAtom.Predicate.Symbol
		policies = append(policies, suppressionPolicy{assertion: name, by: append([]string(nil), byAssertion[name]...)})
	}
	return policies, nil
}

func sortReportRows(rows []ast.Atom) error {
	keys := make([]string, len(rows))
	for index, row := range rows {
		values := make([]any, len(row.Args))
		for argIndex, arg := range row.Args {
			constant, ok := arg.(ast.Constant)
			if !ok {
				return fmt.Errorf("reported row %s contains a non-constant argument", row)
			}
			values[argIndex] = canonicalFactValue(constant)
		}
		encoded, err := canonical.Marshal(values)
		if err != nil {
			return fmt.Errorf("canonicalize report row %s: %w", row, err)
		}
		keys[index] = string(encoded)
	}
	order := make([]int, len(rows))
	for index := range order {
		order[index] = index
	}
	sort.Slice(order, func(i, j int) bool { return keys[order[i]] < keys[order[j]] })
	sorted := make([]ast.Atom, len(rows))
	for index, source := range order {
		sorted[index] = rows[source]
	}
	copy(rows, sorted)
	return nil
}

func canonicalFactValue(constant ast.Constant) any {
	switch constant.Type {
	case ast.StringType:
		value, _ := constant.StringValue()
		return []any{"s", value}
	case ast.NameType:
		value, _ := constant.NameValue()
		return []any{"n", strings.TrimPrefix(value, "/")}
	case ast.NumberType:
		value, _ := constant.NumberValue()
		return []any{"i", strconv.FormatInt(value, 10)}
	case ast.Float64Type:
		value, _ := constant.Float64Value()
		if value == 0 {
			value = 0
		}
		return []any{"f", fmt.Sprintf("%016x", math.Float64bits(value))}
	default:
		return []any{"?", constant.String()}
	}
}

func factValue(constant ast.Constant) (facts.Value, error) {
	switch constant.Type {
	case ast.StringType:
		value, err := constant.StringValue()
		return facts.String(value), err
	case ast.NameType:
		value, err := constant.NameValue()
		return facts.Name(strings.TrimPrefix(value, "/")), err
	case ast.NumberType:
		value, err := constant.NumberValue()
		return facts.Number(value), err
	case ast.Float64Type:
		value, err := constant.Float64Value()
		return facts.Float(value), err
	default:
		return facts.Value{}, fmt.Errorf("unsupported reported Mangle value %s", constant)
	}
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

// rewriteNegatedGlobs replaces each !lintai.glob(P, X) premise with the internal
// positive premise lintai.not_glob(P, X). Its declaration is part of the
// engine package and is always available through catalog binding.
func rewriteNegatedGlobs(unit *parse.SourceUnit) {
	for ci := range unit.Clauses {
		premises := unit.Clauses[ci].Premises
		for pi, premise := range premises {
			if negated, ok := premise.(ast.NegAtom); ok && negated.Atom.Predicate == globSym {
				premises[pi] = ast.Atom{Predicate: notGlobSym, Args: negated.Atom.Args}
			}
		}
	}
}

func bindRuleUnit(unit *parse.SourceUnit, source string, namespaces []string) *phaseError {
	if containsMangleKeyword(source, "Package") || containsMangleKeyword(source, "Use") {
		return failPhase("program_analysis", "Package and Use declarations are owned by the producer catalog")
	}
	uses := append(slices.Clone(namespaces), "lintai")
	sort.Strings(uses)
	for _, namespace := range slices.Compact(uses) {
		useUnit, err := parse.Unit(strings.NewReader("Use " + namespace + "!"))
		if err != nil {
			return failPhase("program_analysis", "cannot bind namespace %q: %v", namespace, err)
		}
		for _, declaration := range useUnit.Decls {
			if declaration.DeclaredAtom.Predicate == symbols.Use {
				unit.Decls = append(unit.Decls, declaration)
			}
		}
	}
	return nil
}

func containsMangleKeyword(source, keyword string) bool {
	for index := 0; index < len(source); {
		current := source[index]
		if current == '#' {
			for index < len(source) && source[index] != '\n' {
				index++
			}
			continue
		}
		if current == '"' || current == '\'' || current == '`' {
			quote := current
			index++
			for index < len(source) {
				if source[index] == '\\' {
					index += 2
					continue
				}
				if source[index] == quote {
					index++
					break
				}
				index++
			}
			continue
		}
		if current >= 'A' && current <= 'Z' || current >= 'a' && current <= 'z' || current == '_' {
			start := index
			for index < len(source) {
				char := source[index]
				if !(char >= 'A' && char <= 'Z' || char >= 'a' && char <= 'z' || char >= '0' && char <= '9' || char == '_') {
					break
				}
				index++
			}
			if source[start:index] == keyword {
				return true
			}
			continue
		}
		index++
	}
	return false
}

// checkReservedPredicates rejects rule declarations and clauses for base and
// prelude predicates. mangle-go accepts facts for declared extensional
// predicates, so the check is done here.
func checkReservedPredicates(unit parse.SourceUnit, schemaDecls map[ast.PredicateSym]ast.Decl) *phaseError {
	reserved := make(map[string]string)
	for sym := range schemaDecls {
		reserved[sym.Symbol] = "base relation"
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
		return runtime.Name{Value: strings.TrimPrefix(value, "/")}
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

// explanationFactStore gives provenance a stable order for each fact query.
// Provenance chooses the first proof of a nested goal, even when it is asked
// for several proofs of the reported goal.
type explanationFactStore struct {
	factstore.ReadOnlyFactStore
}

func (store explanationFactStore) GetFacts(query ast.Atom, callback func(ast.Atom) error) error {
	var matches []ast.Atom
	if err := store.ReadOnlyFactStore.GetFacts(query, func(atom ast.Atom) error {
		matches = append(matches, atom)
		return nil
	}); err != nil {
		return err
	}
	slices.SortFunc(matches, func(a, b ast.Atom) int {
		if order := strings.Compare(a.String(), b.String()); order != 0 {
			return order
		}
		// Distinct Mangle values can have the same printed form, such as 0
		// and 0.0. Break those ties by their actual values.
		for i := range a.Args {
			left, leftOK := a.Args[i].(ast.Constant)
			right, rightOK := b.Args[i].(ast.Constant)
			if leftOK && rightOK {
				if order := compareExplanationConstants(left, right); order != 0 {
					return order
				}
			}
		}
		return 0
	})
	for _, atom := range matches {
		if err := callback(atom); err != nil {
			return err
		}
	}
	return nil
}

func compareExplanationConstants(a, b ast.Constant) int {
	if order := cmp.Compare(a.Type, b.Type); order != 0 {
		return order
	}
	if order := cmp.Compare(a.Symbol, b.Symbol); order != 0 {
		return order
	}
	if order := cmp.Compare(a.NumValue, b.NumValue); order != 0 {
		return order
	}
	// Composite constants can also contain values with identical renderings
	// and hashes, so compare their contents when the outer fields tie.
	switch a.Type {
	case ast.PairShape:
		leftFirst, leftSecond, _ := a.PairValue()
		rightFirst, rightSecond, _ := b.PairValue()
		if order := compareExplanationConstants(leftFirst, rightFirst); order != 0 {
			return order
		}
		return compareExplanationConstants(leftSecond, rightSecond)
	case ast.ListShape:
		left, _ := a.ListSeq()
		right, _ := b.ListSeq()
		return slices.CompareFunc(slices.Collect(left), slices.Collect(right), compareExplanationConstants)
	case ast.MapShape, ast.StructShape:
		return slices.CompareFunc(explanationEntries(a), explanationEntries(b), compareExplanationConstants)
	}
	return 0
}

func explanationEntries(value ast.Constant) []ast.Constant {
	var entries []ast.Constant
	collect := func(key, item ast.Constant) error {
		entries = append(entries, key, item)
		return nil
	}
	empty := func() error { return nil }
	if value.Type == ast.MapShape {
		_, _ = value.MapValues(collect, empty)
	} else {
		_, _ = value.StructValues(collect, empty)
	}
	return entries
}

// explain renders a proof as ground atoms in depth-first rule-premise order.
// Of the proofs provenance returns, the smallest rendering is kept.
func explain(info *mangleanalysis.ProgramInfo, store factstore.ReadOnlyFactStore, goal ast.Atom) []string {
	proofs, err := provenance.Explain(info, explanationFactStore{store}, goal, provenance.Options{MaxProofs: 16})
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
