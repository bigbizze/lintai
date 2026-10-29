package lintaiapi

import (
	"bytes"
	"encoding/hex"
	"encoding/json"
	"maps"
	"math"
	"path/filepath"
	"reflect"
	"slices"
	"strconv"
	"strings"

	"github.com/bigbizze/lintai/internal/tsgo/internal/ast"
	"github.com/bigbizze/lintai/internal/tsgo/internal/checker"
	"github.com/bigbizze/lintai/internal/tsgo/internal/jsnum"
	"github.com/zeebo/xxh3"
)

const (
	TypeKindAny           = "any"
	TypeKindUnknown       = "unknown"
	TypeKindNever         = "never"
	TypeKindVoid          = "void"
	TypeKindUndefined     = "undefined"
	TypeKindNull          = "null"
	TypeKindString        = "string"
	TypeKindNumber        = "number"
	TypeKindBigInt        = "bigint"
	TypeKindBoolean       = "boolean"
	TypeKindSymbol        = "symbol"
	TypeKindObjectKeyword = "object_keyword"
	TypeKindLiteral       = "literal"
	TypeKindTypeParam     = "type_param"
	TypeKindUnion         = "union"
	TypeKindIntersection  = "intersection"
	TypeKindTuple         = "tuple"
	TypeKindFunction      = "function"
	TypeKindObject        = "object"
	TypeKindOther         = "other"
)

// Reasons of type_incomplete.
const (
	IncompleteTruncated   = "truncated"
	IncompleteUnsupported = "unsupported"
	IncompleteSignatures  = "signatures"
	IncompleteConstraint  = "constraint"
	IncompleteConflict    = "conflict"
)

// Marks of a tuple element or a signature parameter.
const (
	MarkOptional = "optional"
	MarkRest     = "rest"
)

const (
	LiteralString  = "string"
	LiteralNumber  = "number"
	LiteralBigInt  = "bigint"
	LiteralBoolean = "boolean"
)

// maxTypeKeyDepth is the height of the highest canonical key that has
// structure facts. A higher type is /other.
const maxTypeKeyDepth = 16

// TypeLiteral is the value of a literal type. Text is the value of a string
// literal and the decimal digits of a bigint literal.
type TypeLiteral struct {
	Kind   string
	Text   string
	Number float64
	Bool   bool
}

type TypeProp struct {
	Name     string
	Type     string
	Optional bool
	Readonly bool
}

// TypeFact is one type entity. Its ID is derived from the canonical key of
// the type, so the same type has the same ID in every config group and in
// every run. The fields that refer to other types hold their IDs.
type TypeFact struct {
	ID         string
	Kind       string
	Named      bool
	OriginKind string
	Origin     string
	Name       string
	// Args are the type arguments of a named type or the elements of a
	// tuple. ArgMarks, when not nil, has a mark for each tuple element:
	// MarkOptional, MarkRest or "".
	Args       []string
	ArgMarks   []string
	Readonly   bool
	Members    []string
	Literal    *TypeLiteral
	ParamName  string
	Props      []TypeProp
	SigParams  []string
	SigMarks   []string
	SigReturns string
	// Incomplete holds the sorted type_incomplete reasons.
	Incomplete []string
	Display    string
	Location   *SourceLocation
}

// typeShape is the classification of one checker type.
type typeShape struct {
	kind       string
	named      bool
	originKind string
	origin     string
	name       string
	nameDecl   *ast.Node
	// localPos is the position of a type declared in a function body. It
	// is part of the key only.
	localPos string
	args     []*checker.Type
	argMarks []string
	readonly bool
	members  []*checker.Type
	literal  *TypeLiteral
	litKey   string
	// paramName is the name of a type parameter, and paramPos is the
	// position of its declaration in the file of origin.
	paramName  string
	paramPos   string
	props      []shapeProp
	sigParams  []*checker.Type
	sigMarks   []string
	sigReturn  *checker.Type
	callSigs   []*checker.Signature
	ctorSigs   []*checker.Signature
	indexInfos []*checker.IndexInfo
	// otherKey is the key of an /other type.
	otherKey string
	// otherParts are types inside an /other type, such as the object and
	// index of an indexed access type. Their keys are part of the key of
	// the /other type, so Db[K] from two files gets two IDs. They have no
	// facts.
	otherParts []*checker.Type
	// incomplete holds the type_incomplete reasons of the shape.
	incomplete []string
}

type shapeProp struct {
	name     string
	t        *checker.Type
	optional bool
	readonly bool
}

// emitFunctionType records the facts of a parameter or return type and of
// every type that those facts refer to, and returns the type ID.
func (e *extractor) emitFunctionType(root *checker.Type, enclosing *ast.Node) string {
	if root == nil {
		return ""
	}
	visited := make(map[*checker.Type]struct{})
	var visit func(t *checker.Type)
	visit = func(t *checker.Type) {
		if _, ok := visited[t]; ok {
			return
		}
		visited[t] = struct{}{}
		id := e.typeID(t)
		display := e.typeDisplay(t, enclosing)
		existing := e.typeFacts[id]
		switch {
		case existing == nil:
			e.typeFacts[id] = e.newTypeFact(t, id, display)
			e.typeFactSeen[t] = struct{}{}
		case !hasKey(e.typeFactSeen, t):
			// Another checker type has this ID. Its facts must agree.
			*existing = mergeTypeFact(*existing, *e.newTypeFact(t, id, display))
			e.typeFactSeen[t] = struct{}{}
		default:
			if display < existing.Display {
				existing.Display = display
			}
		}
		if e.isTruncated(t) {
			return
		}
		for _, child := range e.typeShapeOf(t).children() {
			visit(child)
		}
	}
	visit(root)
	return e.typeID(root)
}

// maxTypeDisplayLength is the approximate length at which the checker
// stops printing the display text of a type. The text of a type that
// nests the same subtype several times grows exponentially with depth.
const maxTypeDisplayLength = 2000

// typeDisplay returns the type_display text of a type. It is the
// renderTypeText text, truncated with "..." by the checker when it is
// longer than maxTypeDisplayLength.
func (e *extractor) typeDisplay(t *checker.Type, enclosing *ast.Node) string {
	return e.checker.TypeToStringEx(
		t,
		enclosing,
		checker.TypeFormatFlagsUseAliasDefinedOutsideCurrentScope,
		&checker.VerbosityContext{Level: -1, MaxTruncationLength: maxTypeDisplayLength},
	)
}

func (s *typeShape) children() []*checker.Type {
	result := make([]*checker.Type, 0, len(s.args)+len(s.members)+len(s.props)+len(s.sigParams)+1)
	result = append(result, s.args...)
	result = append(result, s.members...)
	for _, prop := range s.props {
		result = append(result, prop.t)
	}
	if s.kind == TypeKindFunction {
		result = append(result, s.sigParams...)
		result = append(result, s.sigReturn)
	}
	return result
}

func (e *extractor) newTypeFact(t *checker.Type, id string, display string) *TypeFact {
	shape := e.typeShapeOf(t)
	fact := &TypeFact{
		ID:      id,
		Named:   shape.named,
		Origin:  shape.origin,
		Name:    shape.name,
		Display: display,
	}
	if shape.named {
		fact.OriginKind = shape.originKind
	}
	if e.isTruncated(t) {
		fact.Kind = TypeKindOther
		fact.Incomplete = []string{IncompleteTruncated}
		return fact
	}
	fact.Kind = shape.kind
	fact.Literal = shape.literal
	fact.ParamName = shape.paramName
	fact.Readonly = shape.readonly
	fact.Incomplete = slices.Clone(shape.incomplete)
	for _, arg := range shape.args {
		fact.Args = append(fact.Args, e.typeID(arg))
	}
	fact.ArgMarks = factMarks(shape.argMarks)
	for _, member := range shape.members {
		fact.Members = append(fact.Members, e.typeID(member))
	}
	slices.Sort(fact.Members)
	fact.Members = slices.Compact(fact.Members)
	for _, prop := range shape.props {
		fact.Props = append(fact.Props, TypeProp{Name: prop.name, Type: e.typeID(prop.t), Optional: prop.optional, Readonly: prop.readonly})
	}
	if shape.kind == TypeKindFunction {
		for _, param := range shape.sigParams {
			fact.SigParams = append(fact.SigParams, e.typeID(param))
		}
		fact.SigMarks = factMarks(shape.sigMarks)
		fact.SigReturns = e.typeID(shape.sigReturn)
	}
	fact.Location = e.typeLocation(t)
	return fact
}

// factMarks converts the key marks of tuple elements or signature
// parameters to TypeFact marks. It returns nil when no item is marked.
func factMarks(marks []string) []string {
	if !slices.ContainsFunc(marks, func(mark string) bool { return mark != "" }) {
		return nil
	}
	result := make([]string, len(marks))
	for index, mark := range marks {
		switch mark {
		case "?":
			result[index] = MarkOptional
		case "...":
			result[index] = MarkRest
		}
	}
	return result
}

func hasKey[K comparable, V any](m map[K]V, key K) bool {
	_, ok := m[key]
	return ok
}

// typeLocation returns the location of the declaration name of a named
// type that is declared in a workspace source file. A type that is reached
// through a dist/ declaration file has the origin of its source file but
// no declaration in it, so it has no location from that path; another
// path to the same type can supply it.
func (e *extractor) typeLocation(t *checker.Type) *SourceLocation {
	shape := e.typeShapeOf(t)
	if !shape.named || shape.nameDecl == nil || e.isTruncated(t) {
		return nil
	}
	file := ast.GetSourceFileOfNode(shape.nameDecl)
	if file == nil || file.IsDeclarationFile || !isWithinRoot(e.workspaceRoot, file.FileName().AsString()) || e.relativePath(file.FileName().AsString()) != shape.origin {
		return nil
	}
	nameNode := ast.GetNameOfDeclaration(shape.nameDecl)
	if nameNode == nil {
		nameNode = shape.nameDecl
	}
	location := e.nodeLocation(file, nameNode)
	return &location
}

// typeID returns "type:" and the hex xxh3-128 hash of the canonical key of
// a type. The key of a truncated type also has its display text, so two
// types that differ only below the depth limit get two IDs when their
// display texts differ.
func (e *extractor) typeID(t *checker.Type) string {
	if id, ok := e.typeIDs[t]; ok {
		return id
	}
	result := e.typeKey(t, nil)
	key := result.key
	if result.height > maxTypeKeyDepth {
		e.typeTruncated[t] = struct{}{}
		key = jsonList(jsonQuote("truncated"), jsonQuote(key), jsonQuote(e.typeDisplay(t, nil)))
	}
	id := "type:" + hashKey(key)
	e.typeIDs[t] = id
	return id
}

func hashKey(key string) string {
	sum := xxh3.HashString128(key).Bytes()
	return hex.EncodeToString(sum[:])
}

// isTruncated reports whether the canonical key of a type is more than
// maxTypeKeyDepth levels high. Such a type is /other and has no structure
// facts. This bounds the facts of a type that makes a new type at each
// level, such as type Deep<T> = { next: Deep<T[]> }.
func (e *extractor) isTruncated(t *checker.Type) bool {
	e.typeID(t)
	_, ok := e.typeTruncated[t]
	return ok
}

// typeKeyAt is a type at a depth in the key of another type.
type typeKeyAt struct {
	t     *checker.Type
	depth int
}

// typeKeyResult is a canonical key. height is the number of nested levels
// that the key describes. free is true when the key has no ^n. reach is
// nil when the key has no trunc, and otherwise holds the types that the
// walk visited.
type typeKeyResult struct {
	key    string
	height int
	free   bool
	reach  map[*checker.Type]struct{}
}

// typeKey returns the canonical key of a type. Each node of a key is a JSON
// array, so names, origins and property names cannot run into the
// delimiters. A key contains the hash of the key of each child, not the
// child key itself, so a key has a bounded length and each type is keyed
// once. stack holds the types whose keys are being built. A type that is
// already on the stack is written as ^n, where n is its distance from the
// top of the stack. A position deeper than maxTypeKeyDepth is written as
// trunc.
//
// The key is a function of the type and the stack only, so it does not
// depend on the order in which types are keyed. A key without ^n is cached
// for its depth. If the key has no trunc, no type that it describes can be
// on the stack: the walk would reach the type itself and write ^n. A key
// with trunc stopped where it did only because the types that it visited
// were not on the stack, so it is reused only if none of them is.
func (e *extractor) typeKey(t *checker.Type, stack []*checker.Type) typeKeyResult {
	for index := len(stack) - 1; index >= 0; index-- {
		if stack[index] == t {
			return typeKeyResult{key: "^" + strconv.Itoa(len(stack)-index)}
		}
	}
	at := typeKeyAt{t: t, depth: len(stack)}
	if cached, ok := e.typeKeys[at]; ok && !slices.ContainsFunc(stack, func(item *checker.Type) bool { return hasKey(cached.reach, item) }) {
		return cached
	}
	if len(stack) > maxTypeKeyDepth {
		// The parent of this position is already too high for structure
		// facts, so the rest of the key does not change any fact.
		return typeKeyResult{key: "trunc", free: true, reach: map[*checker.Type]struct{}{t: {}}}
	}
	shape := e.typeShapeOf(t)
	stack = append(stack, t)
	result := typeKeyResult{free: true, height: 1}
	key := func(item *checker.Type) string {
		child := e.typeKey(item, stack)
		result.free = result.free && child.free
		result.height = max(result.height, child.height+1)
		if child.reach != nil {
			if result.reach == nil {
				result.reach = map[*checker.Type]struct{}{t: {}}
			}
			maps.Copy(result.reach, child.reach)
		}
		if strings.HasPrefix(child.key, "^") || strings.HasPrefix(child.key, "/") {
			return jsonQuote(child.key)
		}
		return jsonQuote("#" + hashKey(child.key))
	}
	keys := func(types []*checker.Type) []string {
		result := make([]string, len(types))
		for index, item := range types {
			result[index] = key(item)
		}
		return result
	}
	marked := func(types []*checker.Type, marks []string) string {
		parts := keys(types)
		for index := range parts {
			parts[index] = jsonList(jsonQuote(marks[index]), parts[index])
		}
		return jsonList(parts...)
	}
	signature := func(sig *checker.Signature) string {
		params, marks, ret := e.signatureParts(sig)
		return jsonList(marked(params, marks), key(ret))
	}
	q := jsonQuote
	switch {
	case shape.named:
		result.key = jsonList(q("named"), q(shape.originKind), q(shape.origin), q(shape.name), q(shape.localPos), jsonList(keys(shape.args)...))
	case shape.kind == TypeKindLiteral:
		result.key = shape.litKey
	case shape.kind == TypeKindTypeParam:
		result.key = jsonList(q("tparam"), q(shape.originKind), q(shape.origin), q(shape.paramPos), q(shape.paramName))
	case shape.kind == TypeKindUnion || shape.kind == TypeKindIntersection:
		parts := keys(shape.members)
		slices.Sort(parts)
		parts = slices.Compact(parts)
		prefix := "union"
		if shape.kind == TypeKindIntersection {
			prefix = "inter"
		}
		result.key = jsonList(q(prefix), jsonList(parts...))
	case shape.kind == TypeKindTuple:
		readonly := ""
		if shape.readonly {
			readonly = "readonly"
		}
		result.key = jsonList(q("tuple"), q(readonly), marked(shape.args, shape.argMarks))
	case shape.kind == TypeKindFunction:
		result.key = jsonList(q("fn"), marked(shape.sigParams, shape.sigMarks), key(shape.sigReturn))
	case shape.kind == TypeKindObject:
		props := make([]string, 0, len(shape.props))
		for _, prop := range shape.props {
			flags := ""
			if prop.optional {
				flags += "?"
			}
			if prop.readonly {
				flags += "r"
			}
			props = append(props, jsonList(q(prop.name), q(flags), key(prop.t)))
		}
		calls := make([]string, 0, len(shape.callSigs))
		for _, sig := range shape.callSigs {
			calls = append(calls, signature(sig))
		}
		ctors := make([]string, 0, len(shape.ctorSigs))
		for _, sig := range shape.ctorSigs {
			ctors = append(ctors, signature(sig))
		}
		indexes := make([]string, 0, len(shape.indexInfos))
		for _, info := range shape.indexInfos {
			readonly := ""
			if info.IsReadonly() {
				readonly = "readonly"
			}
			indexes = append(indexes, jsonList(key(info.KeyType()), key(info.ValueType()), q(readonly)))
		}
		result.key = jsonList(q("obj"), jsonList(props...), jsonList(calls...), jsonList(ctors...), jsonList(indexes...))
	case shape.kind == TypeKindOther:
		result.key = jsonList(shape.otherKey, jsonList(keys(shape.otherParts)...))
	default:
		result.key = "/" + shape.kind
	}
	if result.free {
		e.typeKeys[at] = result
	}
	return result
}

func jsonList(items ...string) string {
	return "[" + strings.Join(items, ",") + "]"
}

func (e *extractor) typeShapeOf(t *checker.Type) *typeShape {
	if shape, ok := e.typeShapes[t]; ok {
		return shape
	}
	shape := e.classifyType(t)
	e.typeShapes[t] = shape
	return shape
}

var keywordTypeKinds = []struct {
	flag checker.TypeFlags
	kind string
}{
	{checker.TypeFlagsAny, TypeKindAny},
	{checker.TypeFlagsUnknown, TypeKindUnknown},
	{checker.TypeFlagsNever, TypeKindNever},
	{checker.TypeFlagsVoid, TypeKindVoid},
	{checker.TypeFlagsUndefined, TypeKindUndefined},
	{checker.TypeFlagsNull, TypeKindNull},
	{checker.TypeFlagsString, TypeKindString},
	{checker.TypeFlagsNumber, TypeKindNumber},
	{checker.TypeFlagsBigInt, TypeKindBigInt},
	{checker.TypeFlagsESSymbol, TypeKindSymbol},
	{checker.TypeFlagsNonPrimitive, TypeKindObjectKeyword},
}

// classifyType applies the classification of the structural type facts
// spec, section 4.2. The first matching case wins.
func (e *extractor) classifyType(t *checker.Type) *typeShape {
	flags := t.Flags()
	for _, item := range keywordTypeKinds {
		if flags&item.flag != 0 {
			return &typeShape{kind: item.kind}
		}
	}
	if flags&checker.TypeFlagsBoolean != 0 {
		return &typeShape{kind: TypeKindBoolean}
	}
	if flags&checker.TypeFlagsLiteral != 0 && flags&checker.TypeFlagsEnumLiteral == 0 {
		if shape := literalShape(t); shape != nil {
			return shape
		}
		return e.otherShape(t)
	}
	shape := &typeShape{}
	if alias := t.Alias(); alias != nil && e.nameShape(shape, alias.Symbol()) {
		shape.args = alias.TypeArguments()
	}
	e.structureShape(shape, t)
	return shape
}

func literalShape(t *checker.Type) *typeShape {
	switch value := t.AsLiteralType().Value().(type) {
	case string:
		return &typeShape{kind: TypeKindLiteral, literal: &TypeLiteral{Kind: LiteralString, Text: value}, litKey: literalKey("s", value)}
	case jsnum.Number:
		number := float64(value)
		if math.IsNaN(number) || math.IsInf(number, 0) {
			return nil
		}
		return &typeShape{kind: TypeKindLiteral, literal: &TypeLiteral{Kind: LiteralNumber, Number: number}, litKey: literalKey("n", value.String())}
	case jsnum.PseudoBigInt:
		digits := value.String()
		return &typeShape{kind: TypeKindLiteral, literal: &TypeLiteral{Kind: LiteralBigInt, Text: digits}, litKey: literalKey("big", digits)}
	case bool:
		return &typeShape{kind: TypeKindLiteral, literal: &TypeLiteral{Kind: LiteralBoolean, Bool: value}, litKey: literalKey("b", strconv.FormatBool(value))}
	}
	return nil
}

func literalKey(kind string, value string) string {
	return jsonList(jsonQuote("lit"), jsonQuote(kind), jsonQuote(value))
}

func jsonQuote(value string) string {
	var buffer bytes.Buffer
	encoder := json.NewEncoder(&buffer)
	encoder.SetEscapeHTML(false)
	_ = encoder.Encode(value)
	return strings.TrimSuffix(buffer.String(), "\n")
}

// structureShape sets the kind and the structure of a type that is not a
// keyword, boolean or literal type. A shape that a type alias names keeps
// the alias name and type arguments.
func (e *extractor) structureShape(shape *typeShape, t *checker.Type) {
	flags := t.Flags()
	objectFlags := t.ObjectFlags()
	symbol := t.Symbol()
	switch {
	case flags&checker.TypeFlagsTypeParameter != 0:
		shape.kind = TypeKindTypeParam
		e.typeParamShape(shape, t)
	case flags&(checker.TypeFlagsEnumLiteral|checker.TypeFlagsEnum) != 0 && symbol != nil && symbol.Flags()&(ast.SymbolFlagsEnum|ast.SymbolFlagsEnumMember) != 0:
		shape.kind = TypeKindObject
		if !shape.named && !e.nameShape(shape, symbol) {
			e.setOther(shape, t)
		}
	case flags&checker.TypeFlagsUnion != 0:
		shape.kind = TypeKindUnion
		shape.members = e.unionMembers(t.Types())
	case flags&checker.TypeFlagsIntersection != 0:
		shape.kind = TypeKindIntersection
		shape.members = t.Types()
	case flags&checker.TypeFlagsObject != 0 && checker.IsTupleType(t):
		shape.kind = TypeKindTuple
		// A generic tuple alias keeps its type arguments in args, as
		// other named types do. A tuple alias without type parameters
		// gets its elements.
		if shape.named && len(shape.args) > 0 {
			return
		}
		shape.readonly = t.TargetTupleType().IsReadonly()
		elementFlags := t.TargetTupleType().ElementFlags()
		elements := slices.Clone(e.checker.GetTypeArguments(t))
		if len(elements) > len(elementFlags) {
			elements = elements[:len(elementFlags)]
		}
		marks := make([]string, len(elements))
		for index := range elements {
			switch {
			case elementFlags[index]&checker.ElementFlagsOptional != 0:
				marks[index] = "?"
				elements[index] = e.checker.RemoveMissingOrUndefinedType(elements[index])
			case elementFlags[index]&checker.ElementFlagsVariable != 0:
				marks[index] = "..."
			}
		}
		shape.args = elements
		shape.argMarks = marks
	case flags&checker.TypeFlagsObject != 0 && isClassOrInterface(t):
		shape.kind = TypeKindObject
		if shape.named {
			return
		}
		target := t
		if objectFlags&checker.ObjectFlagsReference != 0 && t.Target() != nil {
			target = t.Target()
		}
		if !e.nameShape(shape, target.Symbol()) {
			e.setOther(shape, t)
			return
		}
		if objectFlags&checker.ObjectFlagsReference != 0 {
			args := e.checker.GetTypeArguments(t)
			if declared := len(target.AsInterfaceType().TypeParameters()); len(args) > declared {
				args = args[:declared]
			}
			shape.args = args
		}
	case flags&checker.TypeFlagsObject != 0 && objectFlags&checker.ObjectFlagsAnonymous != 0 && objectFlags&(checker.ObjectFlagsMapped|checker.ObjectFlagsReverseMapped) == 0:
		e.anonymousObjectShape(shape, t)
	default:
		e.setOther(shape, t)
	}
}

// typeParamShape keys a type parameter by the position of its declaration.
// The name of the declaration that declares it is not unique: the generic
// methods of two object literals in one file have the same name.
func (e *extractor) typeParamShape(shape *typeShape, t *checker.Type) {
	symbol := t.Symbol()
	var declarations []*ast.Node
	if symbol != nil {
		declarations = symbol.Declarations()
	}
	if len(declarations) == 0 {
		shape.incomplete = []string{IncompleteUnsupported}
		if symbol != nil {
			shape.paramName = symbol.Name()
		}
		return
	}
	shape.originKind, shape.origin = e.declarationOrigin(declarations[0], false)
	shape.paramPos = strconv.Itoa(declarations[0].Pos())
	if t.AsTypeParameter().IsThisType() {
		shape.paramName = "this"
		return
	}
	shape.paramName = symbol.Name()
	if e.checker.GetConstraintOfTypeParameter(t) != nil {
		shape.incomplete = []string{IncompleteConstraint}
	}
}

func (e *extractor) anonymousObjectShape(shape *typeShape, t *checker.Type) {
	calls := e.checker.GetSignaturesOfType(t, checker.SignatureKindCall)
	constructs := e.checker.GetSignaturesOfType(t, checker.SignatureKindConstruct)
	properties := e.checker.GetPropertiesOfType(t)
	indexInfos := e.checker.GetIndexInfosOfType(t)
	if len(calls) == 1 && len(constructs) == 0 && len(properties) == 0 && len(indexInfos) == 0 {
		shape.kind = TypeKindFunction
		shape.sigParams, shape.sigMarks, shape.sigReturn = e.signatureParts(calls[0])
		return
	}
	shape.kind = TypeKindObject
	for _, property := range properties {
		name := property.Name()
		if isInternalSymbolName(name) {
			continue
		}
		optional := property.Flags()&ast.SymbolFlagsOptional != 0
		propertyType := e.checker.GetTypeOfSymbol(property)
		if optional {
			propertyType = e.checker.RemoveMissingOrUndefinedType(propertyType)
		}
		shape.props = append(shape.props, shapeProp{name: name, t: propertyType, optional: optional, readonly: e.checker.IsReadonlySymbol(property)})
	}
	slices.SortFunc(shape.props, func(a, b shapeProp) int { return strings.Compare(a.name, b.name) })
	shape.callSigs = calls
	shape.ctorSigs = constructs
	shape.indexInfos = indexInfos
	if len(calls) > 0 || len(constructs) > 0 || len(indexInfos) > 0 {
		shape.incomplete = append(shape.incomplete, IncompleteSignatures)
	}
}

// setOther makes a shape /other. The key has the text of the type and the
// declaration of its symbol, if it has one, so two unique symbol types
// with one name in two files get two IDs. An alias of an /other type keeps
// its name and arguments.
func (e *extractor) setOther(shape *typeShape, t *checker.Type) {
	shape.kind = TypeKindOther
	shape.incomplete = []string{IncompleteUnsupported}
	text := e.checker.TypeToStringEx(t, nil, checker.TypeFormatFlagsNoTruncation, nil)
	originKind, origin, position := "", "", ""
	if symbol := t.Symbol(); symbol != nil && len(symbol.Declarations()) > 0 {
		decl := symbol.Declarations()[0]
		originKind, origin = e.declarationOrigin(decl, false)
		position = strconv.Itoa(decl.Pos())
	}
	shape.otherKey = jsonList(jsonQuote("other"), jsonQuote(text), jsonQuote(originKind), jsonQuote(origin), jsonQuote(position))
	flags := t.Flags()
	switch {
	case flags&checker.TypeFlagsIndexedAccess != 0:
		shape.otherParts = []*checker.Type{t.AsIndexedAccessType().ObjectType(), t.AsIndexedAccessType().IndexType()}
	case flags&checker.TypeFlagsIndex != 0:
		shape.otherParts = []*checker.Type{t.AsIndexType().Target()}
	case flags&checker.TypeFlagsConditional != 0:
		shape.otherParts = []*checker.Type{t.AsConditionalType().CheckType(), t.AsConditionalType().ExtendsType()}
	case flags&checker.TypeFlagsTemplateLiteral != 0:
		shape.otherParts = t.AsTemplateLiteralType().Types()
	case flags&checker.TypeFlagsStringMapping != 0:
		shape.otherParts = []*checker.Type{t.AsStringMappingType().Target()}
	case flags&checker.TypeFlagsSubstitution != 0:
		shape.otherParts = []*checker.Type{t.AsSubstitutionType().BaseType()}
	}
	shape.otherParts = slices.DeleteFunc(shape.otherParts, func(part *checker.Type) bool { return part == nil })
}

func (e *extractor) otherShape(t *checker.Type) *typeShape {
	shape := &typeShape{}
	e.setOther(shape, t)
	return shape
}

func isClassOrInterface(t *checker.Type) bool {
	if t.ObjectFlags()&checker.ObjectFlagsClassOrInterface != 0 {
		return true
	}
	return t.ObjectFlags()&checker.ObjectFlagsReference != 0 && t.Target() != nil && t.Target().ObjectFlags()&checker.ObjectFlagsClassOrInterface != 0
}

// unionMembers returns the members of a union. The checker stores boolean
// as the two members true and false. A union that has both gets one
// boolean member instead, as the source writes it.
func (e *extractor) unionMembers(members []*checker.Type) []*checker.Type {
	isBool := func(member *checker.Type, want bool) bool {
		if member.Flags()&checker.TypeFlagsBooleanLiteral == 0 {
			return false
		}
		value, ok := member.AsLiteralType().Value().(bool)
		return ok && value == want
	}
	if !slices.ContainsFunc(members, func(m *checker.Type) bool { return isBool(m, true) }) ||
		!slices.ContainsFunc(members, func(m *checker.Type) bool { return isBool(m, false) }) {
		return members
	}
	result := make([]*checker.Type, 0, len(members)-1)
	for _, member := range members {
		if member.Flags()&checker.TypeFlagsBooleanLiteral == 0 {
			result = append(result, member)
		}
	}
	return append(result, e.checker.GetBooleanType())
}

// signatureParts returns the parameter types of a signature, a mark for
// each parameter ("?" for an optional parameter, "..." for a rest
// parameter) and the return type.
func (e *extractor) signatureParts(sig *checker.Signature) ([]*checker.Type, []string, *checker.Type) {
	parameters := sig.Parameters()
	types := make([]*checker.Type, len(parameters))
	marks := make([]string, len(parameters))
	for index, parameter := range parameters {
		parameterType := e.checker.GetTypeOfSymbol(parameter)
		switch {
		case sig.HasRestParameter() && index == len(parameters)-1:
			marks[index] = "..."
		case index >= sig.MinArgumentCount():
			marks[index] = "?"
			parameterType = e.checker.RemoveMissingOrUndefinedType(parameterType)
		}
		types[index] = parameterType
	}
	return types, marks, e.checker.GetReturnTypeOfSignature(sig)
}

// nameShape names a shape after a declared symbol. It returns false when
// the symbol has no declaration or no name.
func (e *extractor) nameShape(shape *typeShape, symbol *ast.Symbol) bool {
	if symbol == nil || len(symbol.Declarations()) == 0 {
		return false
	}
	name := qualifiedSymbolName(symbol)
	if name == "" {
		return false
	}
	decl := symbol.Declarations()[0]
	if parent := decl.Parent; parent != nil && !ast.IsSourceFile(parent) && parent.Kind != ast.KindModuleBlock {
		// A type declared in a function body is not unique by name in its
		// file. Its position keeps it apart from other local types with
		// the same name.
		shape.localPos = "@" + strconv.Itoa(decl.Pos())
	}
	shape.named = true
	shape.name = name
	shape.nameDecl = decl
	shape.originKind, shape.origin = e.declarationOrigin(decl, true)
	return true
}

// declarationOrigin returns the origin kind and the origin of a type
// declaration. The origin is the module name that external_call uses, or
// the workspace-relative path of the declaring source file. A declaration
// file under dist/ whose source file is a workspace file has the origin of
// the source file, as resolveDtsCalls does for calls, when mapDist is
// true. A type parameter is keyed by a position in its file, so its origin
// is not mapped.
func (e *extractor) declarationOrigin(decl *ast.Node, mapDist bool) (string, string) {
	if kind, origin := e.externalModule(decl); origin != "" {
		if mapDist && strings.HasSuffix(origin, ".d.ts") {
			if mapped := mapDtsToSource(origin); mapped != "" {
				absolute := normalizeSlash(filepath.Join(filepath.FromSlash(e.workspaceRoot), filepath.FromSlash(mapped)))
				if _, ok := e.requestedFiles[absolute]; ok {
					return OriginKindSourceFile, mapped
				}
			}
		}
		return kind, origin
	}
	file := ast.GetSourceFileOfNode(decl)
	if file == nil {
		return "", ""
	}
	return OriginKindSourceFile, e.relativePath(file.FileName().AsString())
}

// sortedTypeFacts returns the recorded type facts sorted by ID.
func (e *extractor) sortedTypeFacts() []TypeFact {
	result := make([]TypeFact, 0, len(e.typeFacts))
	for _, fact := range e.typeFacts {
		result = append(result, *fact)
	}
	slices.SortFunc(result, func(a, b TypeFact) int { return strings.Compare(a.ID, b.ID) })
	return result
}

// keepReachableTypes removes the type facts that no function parameter or
// return type reaches, after functions were removed from the snapshot.
func keepReachableTypes(snap *Snapshot) {
	index := make(map[string]*TypeFact, len(snap.Types))
	for i := range snap.Types {
		index[snap.Types[i].ID] = &snap.Types[i]
	}
	reached := make(map[string]struct{}, len(snap.Types))
	var visit func(id string)
	visit = func(id string) {
		if id == "" {
			return
		}
		if _, ok := reached[id]; ok {
			return
		}
		reached[id] = struct{}{}
		fact := index[id]
		if fact == nil {
			return
		}
		for _, child := range fact.children() {
			visit(child)
		}
	}
	for _, fn := range snap.Functions {
		visit(fn.ReturnType)
		for _, param := range fn.ParameterTypes {
			visit(param)
		}
	}
	snap.Types = slices.DeleteFunc(snap.Types, func(item TypeFact) bool {
		_, ok := reached[item.ID]
		return !ok
	})
}

func (f *TypeFact) children() []string {
	result := slices.Concat(f.Args, f.Members, f.SigParams)
	for _, prop := range f.Props {
		result = append(result, prop.Type)
	}
	if f.SigReturns != "" {
		result = append(result, f.SigReturns)
	}
	return result
}

// mergeTypes merges the type facts of a config group into a snapshot. Two
// facts with one ID are merged with mergeTypeFact.
func mergeTypes(existing []TypeFact, incoming []TypeFact) []TypeFact {
	index := make(map[string]int, len(existing))
	for i, item := range existing {
		index[item.ID] = i
	}
	for _, item := range incoming {
		if i, ok := index[item.ID]; ok {
			existing[i] = mergeTypeFact(existing[i], item)
			continue
		}
		index[item.ID] = len(existing)
		existing = append(existing, item)
	}
	return existing
}

// mergeTypeFact merges two facts with one ID: from two config groups, or
// from two checker types in one group. The smallest display text and the
// smallest location are kept. If the facts give different structure, for
// example one declaration under strict and non-strict compiler options,
// the result keeps only the fields that the key fixes (the name, the
// origin and the arguments of a named type), drops the other structure
// and is incomplete with IncompleteConflict. The result does not depend
// on the order of the two facts.
func mergeTypeFact(left TypeFact, right TypeFact) TypeFact {
	display := min(left.Display, right.Display)
	location := left.Location
	if location == nil || (right.Location != nil && locationLess(*right.Location, *location)) {
		location = right.Location
	}
	if !sameStructure(left, right) {
		kind := left.Kind
		if left.Kind != right.Kind {
			kind = TypeKindOther
		}
		merged := TypeFact{
			ID:         left.ID,
			Kind:       kind,
			Named:      left.Named,
			OriginKind: left.OriginKind,
			Origin:     left.Origin,
			Name:       left.Name,
			Incomplete: []string{IncompleteConflict},
		}
		if left.Named && slices.Equal(left.Args, right.Args) {
			merged.Args = left.Args
		}
		left = merged
	}
	left.Display = display
	left.Location = location
	return left
}

// sameStructure reports whether two facts with one ID agree on every
// field except the display text and the location.
func sameStructure(left TypeFact, right TypeFact) bool {
	left.Display, right.Display = "", ""
	left.Location, right.Location = nil, nil
	return reflect.DeepEqual(left, right)
}

func locationLess(left SourceLocation, right SourceLocation) bool {
	if left.File != right.File {
		return left.File < right.File
	}
	if left.StartLine != right.StartLine {
		return left.StartLine < right.StartLine
	}
	return left.StartColumn < right.StartColumn
}
