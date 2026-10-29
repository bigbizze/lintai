package lintaiapi

import (
	"bytes"
	"encoding/hex"
	"encoding/json"
	"math"
	"path/filepath"
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
}

// TypeFact is one type entity. Its ID is derived from the canonical key of
// the type, so the same type has the same ID in every config group and in
// every run. The fields that refer to other types hold their IDs.
type TypeFact struct {
	ID     string
	Kind   string
	Named  bool
	Origin string
	Name   string
	// Args are the type arguments of a named type or the elements of a
	// tuple.
	Args       []string
	Members    []string
	Literal    *TypeLiteral
	ParamName  string
	Props      []TypeProp
	SigParams  []string
	SigReturns string
	Display    string
	Location   *SourceLocation
}

// typeShape is the classification of one checker type.
type typeShape struct {
	kind     string
	named    bool
	origin   string
	name     string
	nameDecl *ast.Node
	// localPos is the position of a type declared in a function body. It
	// is part of the key only.
	localPos string
	args     []*checker.Type
	argMarks []string
	members  []*checker.Type
	literal  *TypeLiteral
	litKey   string
	// paramName and paramOwner are the name of a type parameter and the
	// qualified name of the declaration that declares it.
	paramName  string
	paramOwner string
	props      []shapeProp
	sigParams  []*checker.Type
	sigMarks   []string
	sigReturn  *checker.Type
	callSigs   []*checker.Signature
	ctorSigs   []*checker.Signature
	indexInfos []*checker.IndexInfo
	otherText  string
}

type shapeProp struct {
	name     string
	t        *checker.Type
	optional bool
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
		if existing := e.typeFacts[id]; existing != nil {
			if display < existing.Display {
				existing.Display = display
			}
			if existing.Location == nil {
				existing.Location = e.typeLocation(t)
			}
		} else {
			e.typeFacts[id] = e.newTypeFact(t, id, display)
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
	if e.isTruncated(t) {
		return &TypeFact{ID: id, Kind: TypeKindOther, Named: shape.named, Origin: shape.origin, Name: shape.name, Display: display}
	}
	fact := &TypeFact{
		ID:        id,
		Kind:      shape.kind,
		Named:     shape.named,
		Origin:    shape.origin,
		Name:      shape.name,
		Literal:   shape.literal,
		ParamName: shape.paramName,
		Display:   display,
	}
	for _, arg := range shape.args {
		fact.Args = append(fact.Args, e.typeID(arg))
	}
	for _, member := range shape.members {
		fact.Members = append(fact.Members, e.typeID(member))
	}
	slices.Sort(fact.Members)
	fact.Members = slices.Compact(fact.Members)
	for _, prop := range shape.props {
		fact.Props = append(fact.Props, TypeProp{Name: prop.name, Type: e.typeID(prop.t), Optional: prop.optional})
	}
	if shape.kind == TypeKindFunction {
		for _, param := range shape.sigParams {
			fact.SigParams = append(fact.SigParams, e.typeID(param))
		}
		fact.SigReturns = e.typeID(shape.sigReturn)
	}
	fact.Location = e.typeLocation(t)
	return fact
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
// a type.
func (e *extractor) typeID(t *checker.Type) string {
	if id, ok := e.typeIDs[t]; ok {
		return id
	}
	key, height, _ := e.typeKey(t, nil)
	if height > maxTypeKeyDepth {
		e.typeTruncated[t] = struct{}{}
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

// cachedTypeKey is a canonical key that does not refer to the stack. height
// is the number of nested levels that the key describes.
type cachedTypeKey struct {
	key    string
	height int
}

// typeKey returns the canonical key of a type. A key contains the hash of
// the key of each child, not the child key itself, so a key has a bounded
// length and each type is keyed once. stack holds the types whose keys are
// being built. A type that is already on the stack is written as ^n, where
// n is its distance from the top of the stack. A position deeper than
// maxTypeKeyDepth is written as trunc. The key is a function of
// the type and the stack only, so it does not depend on the order in which
// types are keyed. The second result is the height of the key. The third
// result is true when the key has no ^n. Such a key depends only on the
// type and its depth, and is cached for that depth.
func (e *extractor) typeKey(t *checker.Type, stack []*checker.Type) (string, int, bool) {
	at := typeKeyAt{t: t, depth: len(stack)}
	if cached, ok := e.typeKeys[at]; ok {
		return cached.key, cached.height, true
	}
	for index := len(stack) - 1; index >= 0; index-- {
		if stack[index] == t {
			return "^" + strconv.Itoa(len(stack)-index), 0, false
		}
	}
	if len(stack) > maxTypeKeyDepth {
		// The parent of this position is already too high for structure
		// facts, so the rest of the key does not change any fact.
		return "trunc", 0, true
	}
	shape := e.typeShapeOf(t)
	stack = append(stack, t)
	free := true
	height := 1
	key := func(item *checker.Type) string {
		text, itemHeight, itemFree := e.typeKey(item, stack)
		free = free && itemFree
		height = max(height, itemHeight+1)
		if strings.HasPrefix(text, "^") || strings.HasPrefix(text, "/") {
			return text
		}
		return "#" + hashKey(text)
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
		for index, mark := range marks {
			switch mark {
			case "?":
				parts[index] += "?"
			case "...":
				parts[index] = "..." + parts[index]
			}
		}
		return strings.Join(parts, ",")
	}
	signature := func(sig *checker.Signature) string {
		params, marks, ret := e.signatureParts(sig)
		return "(" + marked(params, marks) + ")=>" + key(ret)
	}
	var result string
	switch {
	case shape.named:
		result = "named(" + shape.origin + "," + shape.name + shape.localPos + ")<" + strings.Join(keys(shape.args), ",") + ">"
	case shape.kind == TypeKindLiteral:
		result = shape.litKey
	case shape.kind == TypeKindTypeParam:
		result = "tparam(" + shape.origin + "," + shape.paramOwner + "," + shape.paramName + ")"
	case shape.kind == TypeKindUnion || shape.kind == TypeKindIntersection:
		parts := keys(shape.members)
		slices.Sort(parts)
		parts = slices.Compact(parts)
		prefix := "union{"
		if shape.kind == TypeKindIntersection {
			prefix = "inter{"
		}
		result = prefix + strings.Join(parts, ",") + "}"
	case shape.kind == TypeKindTuple:
		result = "tuple[" + marked(shape.args, shape.argMarks) + "]"
	case shape.kind == TypeKindFunction:
		result = "fn(" + marked(shape.sigParams, shape.sigMarks) + ")=>" + key(shape.sigReturn)
	case shape.kind == TypeKindObject:
		entries := make([]string, 0, len(shape.props))
		for _, prop := range shape.props {
			name := jsonQuote(prop.name)
			if prop.optional {
				name += "?"
			}
			entries = append(entries, name+":"+key(prop.t))
		}
		slices.Sort(entries)
		for _, sig := range shape.callSigs {
			entries = append(entries, "call"+signature(sig))
		}
		for _, sig := range shape.ctorSigs {
			entries = append(entries, "new"+signature(sig))
		}
		for _, info := range shape.indexInfos {
			entries = append(entries, "index("+key(info.KeyType())+")=>"+key(info.ValueType()))
		}
		result = "obj{" + strings.Join(entries, ",") + "}"
	case shape.kind == TypeKindOther:
		result = "other(" + shape.otherText + ")"
	default:
		result = "/" + shape.kind
	}
	if free {
		e.typeKeys[at] = cachedTypeKey{key: result, height: height}
	}
	return result, height, free
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
		return &typeShape{kind: TypeKindLiteral, literal: &TypeLiteral{Kind: LiteralString, Text: value}, litKey: "lit:s:" + jsonQuote(value)}
	case jsnum.Number:
		number := float64(value)
		if math.IsNaN(number) || math.IsInf(number, 0) {
			return nil
		}
		return &typeShape{kind: TypeKindLiteral, literal: &TypeLiteral{Kind: LiteralNumber, Number: number}, litKey: "lit:n:" + value.String()}
	case jsnum.PseudoBigInt:
		digits := value.String()
		return &typeShape{kind: TypeKindLiteral, literal: &TypeLiteral{Kind: LiteralBigInt, Text: digits}, litKey: "lit:big:" + digits}
	case bool:
		return &typeShape{kind: TypeKindLiteral, literal: &TypeLiteral{Kind: LiteralBoolean, Bool: value}, litKey: "lit:b:" + strconv.FormatBool(value)}
	}
	return nil
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

func (e *extractor) typeParamShape(shape *typeShape, t *checker.Type) {
	symbol := t.Symbol()
	if symbol == nil {
		return
	}
	declarations := symbol.Declarations()
	if t.AsTypeParameter().IsThisType() {
		shape.paramName = "this"
		shape.paramOwner = qualifiedSymbolName(symbol)
		if len(declarations) > 0 {
			shape.origin = e.typeOrigin(declarations[0])
		}
		return
	}
	shape.paramName = symbol.Name()
	if len(declarations) == 0 {
		return
	}
	shape.origin = e.typeOrigin(declarations[0])
	if parent := declarations[0].Parent; parent != nil {
		shape.paramOwner = qualifiedSymbolName(parent.Symbol())
	}
	if shape.paramOwner == "" {
		// An anonymous declaration, such as a generic arrow function, has
		// no name. Its position keeps its type parameters apart from those
		// of other anonymous declarations in the file.
		shape.paramOwner = "@" + strconv.Itoa(declarations[0].Pos())
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
		shape.props = append(shape.props, shapeProp{name: name, t: propertyType, optional: optional})
	}
	slices.SortFunc(shape.props, func(a, b shapeProp) int { return strings.Compare(a.name, b.name) })
	shape.callSigs = calls
	shape.ctorSigs = constructs
	shape.indexInfos = indexInfos
}

func (e *extractor) setOther(shape *typeShape, t *checker.Type) {
	shape.kind = TypeKindOther
	shape.otherText = e.checker.TypeToStringEx(t, nil, checker.TypeFormatFlagsNoTruncation, nil)
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
	shape.origin = e.typeOrigin(decl)
	return true
}

// typeOrigin returns the origin of a type declaration: the module name that
// external_call uses, or the workspace-relative path of the declaring
// source file. A declaration file under dist/ whose source file is a
// workspace file has the origin of the source file, as resolveDtsCalls
// does for calls.
func (e *extractor) typeOrigin(decl *ast.Node) string {
	if origin := e.externalModuleName(decl); origin != "" {
		if strings.HasSuffix(origin, ".d.ts") {
			if mapped := mapDtsToSource(origin); mapped != "" {
				absolute := normalizeSlash(filepath.Join(filepath.FromSlash(e.workspaceRoot), filepath.FromSlash(mapped)))
				if _, ok := e.requestedFiles[absolute]; ok {
					return mapped
				}
			}
		}
		return origin
	}
	file := ast.GetSourceFileOfNode(decl)
	if file == nil {
		return ""
	}
	return e.relativePath(file.FileName().AsString())
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

// mergeTypes merges the type facts of a config group into a snapshot. The
// same ID describes the same type. The smallest display text is kept, and
// a location from any group is kept.
func mergeTypes(existing []TypeFact, incoming []TypeFact) []TypeFact {
	index := make(map[string]int, len(existing))
	for i, item := range existing {
		index[item.ID] = i
	}
	for _, item := range incoming {
		if i, ok := index[item.ID]; ok {
			if item.Display < existing[i].Display {
				existing[i].Display = item.Display
			}
			if existing[i].Location == nil {
				existing[i].Location = item.Location
			}
			continue
		}
		index[item.ID] = len(existing)
		existing = append(existing, item)
	}
	return existing
}
