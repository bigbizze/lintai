package runtime

import (
	"bytes"
	"encoding/json"
	"fmt"
	"math"
	"regexp"

	"github.com/dop251/goja"
)

type LoadedRule struct {
	runtime      *goja.Runtime
	programValue goja.Value
	factsFn      goja.Callable
	messageFn    goja.Callable
	freezeFn     goja.Callable
	strictRowFn  goja.Callable
}

// SuppressionDeclaration is raw rule metadata. Its option shape is validated
// against the generated report declaration during program preparation.
type SuppressionDeclaration struct {
	Assertion string
	Options   any
}

// RuleMetadata is the pure, setup-free part of a rule catalog entry.
type RuleMetadata struct {
	ID             string
	Version        int
	HasSetup       bool
	HasConfig      bool
	Report         []string
	ExpectNonEmpty []string
	Suppressions   []SuppressionDeclaration
}

// ProgramOutput is what the pure phase of a rule produces before evaluation.
type ProgramOutput struct {
	Text  string
	Facts map[string][][]any
}

// Name is a Mangle name constant value, without its leading slash.
type Name struct {
	Value string
}

// RuleIDPattern is the format of a rule id. Linters use the id in a rule
// name, lintai/<id>, so it has no upper case letters, spaces or other
// characters that need quotes in a config or a disable comment.
var RuleIDPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9._/-]*$`)

// ValidateRuleID returns an error when id does not match RuleIDPattern.
func ValidateRuleID(id string) error {
	if !RuleIDPattern.MatchString(id) {
		return fmt.Errorf("rule id %q is not valid: it must match %s", id, RuleIDPattern)
	}
	return nil
}

// RuleInspection retains partial metadata on errors and a runtime only for a
// valid rule. Helpers have IsRule false and no runtime.
type RuleInspection struct {
	Metadata RuleMetadata
	IsRule   bool
	Rule     *LoadedRule
}

type decodedRule struct {
	metadata     RuleMetadata
	programValue goja.Value
	factsFn      goja.Callable
	messageFn    goja.Callable
}

// Property access and export can invoke JavaScript getters outside RunString.
// Keep the partially decoded ID if a getter throws after it was read.
func readRuleMetadata(vm *goja.Runtime) (decoded decodedRule, ok bool, err error) {
	if exception := vm.Try(func() {
		ok, err = decodeRuleMetadata(vm, &decoded)
	}); exception != nil {
		return decoded, true, fmt.Errorf("read rule metadata: %w", exception)
	}
	return decoded, ok, err
}

func decodeRuleMetadata(vm *goja.Runtime, decoded *decodedRule) (ok bool, err error) {
	metadata := &decoded.metadata
	moduleValue := vm.Get("LintAIPureModule")
	if moduleValue == nil || goja.IsUndefined(moduleValue) || goja.IsNull(moduleValue) {
		return false, fmt.Errorf("pure bundle did not initialize LintAIPureModule")
	}
	ruleValue := moduleValue.ToObject(vm).Get("rule")
	if ruleValue == nil || goja.IsUndefined(ruleValue) || goja.IsNull(ruleValue) {
		return false, nil
	}
	ruleObject := ruleValue.ToObject(vm)
	idValue := ruleObject.Get("id")
	if idValue == nil {
		return true, fmt.Errorf("the default export is not a lintai rule; create it with rule(id) from @lintai/sdk")
	}
	id, validID := idValue.Export().(string)
	if !validID || id == "" {
		return true, fmt.Errorf("the default export is not a lintai rule; create it with rule(id) from @lintai/sdk")
	}
	metadata.ID = id
	if err := ValidateRuleID(id); err != nil {
		return true, err
	}
	versionValue := ruleObject.Get("versionValue")
	if !goja.IsNumber(versionValue) {
		return true, fmt.Errorf("rule %s must call .version(n) with a positive integer", id)
	}
	version := versionValue.ToFloat()
	if math.IsNaN(version) || version < 1 || version > 9007199254740991 || math.Trunc(version) != version {
		return true, fmt.Errorf("rule %s must call .version(n) with a positive integer", id)
	}
	metadata.Version = int(version)
	setupValue, configValue := ruleObject.Get("hasSetup"), ruleObject.Get("hasConfig")
	if setupValue == nil || configValue == nil {
		return true, fmt.Errorf("rule %s has invalid setup or config metadata", id)
	}
	var validSetup, validConfig bool
	metadata.HasSetup, validSetup = setupValue.Export().(bool)
	metadata.HasConfig, validConfig = configValue.Export().(bool)
	if !validSetup || !validConfig {
		return true, fmt.Errorf("rule %s has invalid setup or config metadata", id)
	}
	decoded.programValue = ruleObject.Get("programValue")
	if decoded.programValue == nil || goja.IsUndefined(decoded.programValue) || goja.IsNull(decoded.programValue) {
		return true, fmt.Errorf("rule is missing program()")
	}
	var valid bool
	decoded.messageFn, valid = goja.AssertFunction(ruleObject.Get("messageFn"))
	if !valid {
		return true, fmt.Errorf("rule is missing message()")
	}
	if value := ruleObject.Get("factsFn"); value != nil && !goja.IsUndefined(value) && !goja.IsNull(value) {
		decoded.factsFn, valid = goja.AssertFunction(value)
		if !valid {
			return true, fmt.Errorf("facts() must be given a function")
		}
	}
	metadata.Report, err = stringList(ruleObject.Get("reportPredicates"))
	if err != nil {
		return true, fmt.Errorf("report(): %w", err)
	}
	if len(metadata.Report) == 0 {
		return true, fmt.Errorf("rule is missing report()")
	}
	metadata.ExpectNonEmpty, err = stringList(ruleObject.Get("expectNonEmptyPredicates"))
	if err != nil {
		return true, fmt.Errorf("expectNonEmpty(): %w", err)
	}
	metadata.Suppressions, err = suppressionDeclarations(vm, ruleObject.Get("suppressionDeclarations"))
	if err != nil {
		return true, fmt.Errorf("suppression(): %w", err)
	}
	return true, nil
}

// InspectPureBundle loads metadata without calling setup, config, facts,
// program or message. A valid rule's runtime is shared for that operation's
// program and messages; callers inspect again for a new operation.
func InspectPureBundle(code string) (RuleInspection, error) {
	vm := goja.New()
	disableAmbientCapabilities(vm)
	if _, err := vm.RunString(`
		function lintaiDeepFreeze(value) {
			if (!value || typeof value !== "object") return value;
			Object.freeze(value);
			for (const key of Object.keys(value)) {
				lintaiDeepFreeze(value[key]);
			}
			return value;
		}
		function lintaiStrictRow(row) {
			return new Proxy(row, {
				get(target, key) {
					if (typeof key === "string" && key !== "toJSON" && key !== "then" && !Object.prototype.hasOwnProperty.call(target, key) && !(key in Object.prototype)) {
						throw new TypeError("message() read row." + key + ", but the reported predicate has no argument " + key + ". Its arguments are: " + Object.keys(target).join(", "));
					}
					return target[key];
				},
			});
		}
	`); err != nil {
		return RuleInspection{}, err
	}
	freezeFn, _ := goja.AssertFunction(vm.Get("lintaiDeepFreeze"))
	strictRowFn, _ := goja.AssertFunction(vm.Get("lintaiStrictRow"))
	if _, err := vm.RunString(code); err != nil {
		return RuleInspection{}, err
	}
	decoded, ok, err := readRuleMetadata(vm)
	result := RuleInspection{Metadata: decoded.metadata, IsRule: ok}
	if err != nil || !ok {
		return result, err
	}
	result.Rule = &LoadedRule{
		runtime:      vm,
		programValue: decoded.programValue,
		factsFn:      decoded.factsFn,
		messageFn:    decoded.messageFn,
		freezeFn:     freezeFn,
		strictRowFn:  strictRowFn,
	}
	return result, nil
}

// Program runs facts() and program() and returns the program text and the
// rule's extra extensional tuples.
func (r *LoadedRule) Program(env, setup any) (output ProgramOutput, err error) {
	if exception := r.runtime.Try(func() {
		output, err = r.program(env, setup)
	}); exception != nil {
		return ProgramOutput{}, exception
	}
	return output, err
}

func (r *LoadedRule) program(env, setup any) (ProgramOutput, error) {
	contextObject, err := r.ruleContext(env, setup)
	if err != nil {
		return ProgramOutput{}, err
	}
	output := ProgramOutput{Facts: map[string][][]any{}}
	if r.factsFn != nil {
		result, err := r.factsFn(goja.Undefined(), contextObject)
		if err != nil {
			return ProgramOutput{}, err
		}
		output.Facts, err = decodeFacts(r.runtime, result)
		if err != nil {
			return ProgramOutput{}, fmt.Errorf("facts(): %w", err)
		}
	}
	fragment := r.programValue
	if fn, ok := goja.AssertFunction(fragment); ok {
		fragment, err = fn(goja.Undefined(), contextObject)
		if err != nil {
			return ProgramOutput{}, err
		}
	}
	text, ok := fragmentText(r.runtime, fragment)
	if !ok {
		return ProgramOutput{}, fmt.Errorf("program() must be a dl fragment or a function that returns one")
	}
	output.Text = text
	return output, nil
}

func (r *LoadedRule) Message(row map[string]any, assertionID string) (message string, err error) {
	if exception := r.runtime.Try(func() {
		message, err = r.message(row, assertionID)
	}); exception != nil {
		return "", exception
	}
	return message, err
}

func (r *LoadedRule) message(row map[string]any, assertionID string) (string, error) {
	messageRow := make(map[string]any, len(row))
	for key, value := range row {
		if name, ok := value.(Name); ok {
			messageRow[key] = "/" + name.Value
		} else {
			messageRow[key] = value
		}
	}
	strictRow, err := r.strictRowFn(goja.Undefined(), r.runtime.ToValue(messageRow))
	if err != nil {
		return "", err
	}
	result, err := r.messageFn(
		goja.Undefined(),
		strictRow,
		r.runtime.ToValue(map[string]any{"assertion_id": assertionID}),
	)
	if err != nil {
		return "", err
	}
	return result.String(), nil
}

func suppressionDeclarations(vm *goja.Runtime, value goja.Value) ([]SuppressionDeclaration, error) {
	if value == nil || goja.IsUndefined(value) || goja.IsNull(value) {
		return nil, nil
	}
	raw, err := json.Marshal(value.Export())
	if err != nil {
		return nil, err
	}
	var items []*struct {
		Assertion *string `json:"assertion"`
		Options   any     `json:"options"`
	}
	if err := json.Unmarshal(raw, &items); err != nil {
		return nil, fmt.Errorf("must be an array of assertion policies")
	}
	result := make([]SuppressionDeclaration, len(items))
	for index, item := range items {
		if item == nil || item.Assertion == nil {
			return nil, fmt.Errorf("item %d must declare an assertion string", index)
		}
		result[index] = SuppressionDeclaration{Assertion: *item.Assertion, Options: item.Options}
	}
	return result, nil
}

func (r *LoadedRule) ruleContext(env, setup any) (*goja.Object, error) {
	envValue, err := toFrozenValue(r.runtime, env)
	if err != nil {
		return nil, err
	}
	setupValue, err := toFrozenValue(r.runtime, setup)
	if err != nil {
		return nil, err
	}
	if r.freezeFn != nil {
		if _, err := r.freezeFn(goja.Undefined(), envValue); err != nil {
			return nil, err
		}
		if _, err := r.freezeFn(goja.Undefined(), setupValue); err != nil {
			return nil, err
		}
	}
	contextObject := r.runtime.NewObject()
	if err := contextObject.Set("env", envValue); err != nil {
		return nil, err
	}
	if err := contextObject.Set("setup", setupValue); err != nil {
		return nil, err
	}
	return contextObject, nil
}

func fragmentText(vm *goja.Runtime, value goja.Value) (string, bool) {
	if value == nil || goja.IsUndefined(value) || goja.IsNull(value) {
		return "", false
	}
	object, ok := value.(*goja.Object)
	if !ok || object.Get("__lintaiKind").String() != "fragment" {
		return "", false
	}
	text := object.Get("text")
	if text == nil || goja.IsUndefined(text) {
		return "", false
	}
	return text.String(), true
}

func decodeFacts(vm *goja.Runtime, value goja.Value) (map[string][][]any, error) {
	if value == nil || goja.IsUndefined(value) || goja.IsNull(value) {
		return nil, fmt.Errorf("must return an object of relations")
	}
	raw, err := json.Marshal(value.Export())
	if err != nil {
		return nil, err
	}
	var relations map[string][]json.RawMessage
	if err := json.Unmarshal(raw, &relations); err != nil {
		return nil, fmt.Errorf("must return Record<string, Array<Array<string | number | Name>>>")
	}
	result := make(map[string][][]any, len(relations))
	for relation, rows := range relations {
		decodedRows := make([][]any, 0, len(rows))
		for rowIndex, rawRow := range rows {
			var cells []json.RawMessage
			if err := json.Unmarshal(rawRow, &cells); err != nil {
				return nil, fmt.Errorf("%s[%d] is not an array", relation, rowIndex)
			}
			row := make([]any, 0, len(cells))
			for cellIndex, rawCell := range cells {
				cell, err := decodeCell(rawCell)
				if err != nil {
					return nil, fmt.Errorf("%s[%d][%d]: %w", relation, rowIndex, cellIndex, err)
				}
				row = append(row, cell)
			}
			decodedRows = append(decodedRows, row)
		}
		result[relation] = decodedRows
	}
	return result, nil
}

func decodeCell(raw json.RawMessage) (any, error) {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	var value any
	if err := decoder.Decode(&value); err != nil {
		return nil, err
	}
	switch typed := value.(type) {
	case string:
		return typed, nil
	case json.Number:
		return typed, nil
	case map[string]any:
		if typed["__lintaiKind"] == "name" {
			if text, ok := typed["value"].(string); ok {
				return Name{Value: text}, nil
			}
		}
	}
	return nil, fmt.Errorf("unsupported value %s: use a string, a number or dl.name()", string(raw))
}

func stringList(value goja.Value) ([]string, error) {
	if value == nil || goja.IsUndefined(value) || goja.IsNull(value) {
		return nil, nil
	}
	raw, ok := value.Export().([]any)
	if !ok {
		return nil, fmt.Errorf("must be an array of strings")
	}
	items := make([]string, len(raw))
	for index, item := range raw {
		text, ok := item.(string)
		if !ok {
			return nil, fmt.Errorf("item %d must be a string", index)
		}
		items[index] = text
	}
	return items, nil
}

func disableAmbientCapabilities(vm *goja.Runtime) {
	_ = vm.Set("process", goja.Undefined())
	_ = vm.Set("require", goja.Undefined())
	_ = vm.Set("fetch", goja.Undefined())
	_ = vm.Set("setTimeout", goja.Undefined())
	_ = vm.Set("setInterval", goja.Undefined())
	if mathValue := vm.Get("Math"); !goja.IsUndefined(mathValue) && !goja.IsNull(mathValue) {
		mathObject := mathValue.ToObject(vm)
		_ = mathObject.Set("random", func(goja.FunctionCall) goja.Value {
			panic(vm.NewTypeError("Math.random is disabled in the pure runtime"))
		})
	}
	_, _ = vm.RunString(`
		(() => {
			const message = "Date is disabled in the pure runtime; supply time as explicit data";
			const denied = () => { throw new TypeError(message); };
			globalThis.Date = new Proxy(function lintaiDisabledDate() {}, {
				apply: denied,
				construct: denied,
				get(target, key, receiver) {
					if (key === "now" || key === "parse" || key === "UTC") return denied;
					return Reflect.get(target, key, receiver);
				},
			});
		})();
	`)
}

func toFrozenValue(vm *goja.Runtime, value any) (goja.Value, error) {
	raw, err := json.Marshal(value)
	if err != nil {
		return nil, err
	}
	return vm.RunString(fmt.Sprintf("JSON.parse(%q)", string(raw)))
}
