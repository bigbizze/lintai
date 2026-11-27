package runtime

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"regexp"

	"github.com/dop251/goja"
)

type LoadedRule struct {
	stop         func() bool
	runtime      *goja.Runtime
	programValue goja.Value
	factsFn      goja.Callable
	messageFn    goja.Callable
	config       goja.Value
	parseConfig  goja.Callable
	detach       goja.Callable
	env          goja.Value
	strictRowFn  goja.Callable
}

// SuppressionDeclaration is raw rule metadata. Its option shape is validated
// against the generated report declaration during program preparation.
type SuppressionDeclaration struct {
	Assertion string
	Options   any
}

// RuleMetadata is the declarative part of a rule catalog entry.
type RuleMetadata struct {
	ID             string
	Version        int
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

// RuleIDPattern defines the semantic IDs used by configuration and diagnostics.
var RuleIDPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9._/-]*$`)

// ValidateRuleID returns an error when id does not match RuleIDPattern.
func ValidateRuleID(id string) error {
	if !RuleIDPattern.MatchString(id) {
		return fmt.Errorf("rule id %q is not valid: it must match %s", id, RuleIDPattern)
	}
	return nil
}

// RuleInspection retains partial metadata on errors and a runtime only for a
// valid rule.
type RuleInspection struct {
	Metadata RuleMetadata
	Rule     *LoadedRule
}

type decodedRule struct {
	config       goja.Value
	parseConfig  goja.Callable
	metadata     RuleMetadata
	programValue goja.Value
	factsFn      goja.Callable
	messageFn    goja.Callable
}

// Getters, value conversion, and even exception formatting can invoke rule code.
// Detach its error message while the VM is still interruptible.
func inVM(vm *goja.Runtime, run func() error) (err error) {
	defer func() {
		switch failure := recover().(type) {
		case nil:
		case *goja.InterruptedError:
			err = failure
		case *goja.StackOverflowError:
			err = errors.New("rule exceeded the JavaScript call stack")
		default:
			panic(failure)
		}
	}()
	if ex := vm.Try(func() { err = run() }); ex != nil {
		err = ex
	}
	if ex, ok := err.(*goja.Exception); ok {
		message := "rule threw an exception whose message could not be read"
		vm.Try(func() { message = ex.Error() })
		return errors.New(message)
	}
	return err
}

func decodeRuleMetadata(vm *goja.Runtime, decoded *decodedRule) (err error) {
	metadata := &decoded.metadata
	moduleValue := vm.Get("LintAIPureModule")
	if moduleValue == nil || goja.IsUndefined(moduleValue) || goja.IsNull(moduleValue) {
		return fmt.Errorf("pure bundle did not initialize LintAIPureModule")
	}
	ruleValue := moduleValue.ToObject(vm).Get("default")
	if ruleValue == nil || goja.IsUndefined(ruleValue) || goja.IsNull(ruleValue) {
		return fmt.Errorf("entry must default-export a lintai rule")
	}
	ruleObject := ruleValue.ToObject(vm)
	idValue := ruleObject.Get("id")
	if idValue == nil {
		return fmt.Errorf("the default export is not a lintai rule; create it with rule(id) from @lintai/sdk")
	}
	id, validID := idValue.Export().(string)
	if !validID || id == "" {
		return fmt.Errorf("the default export is not a lintai rule; create it with rule(id) from @lintai/sdk")
	}
	metadata.ID = id
	if err := ValidateRuleID(id); err != nil {
		return err
	}
	versionValue := ruleObject.Get("versionValue")
	if !goja.IsNumber(versionValue) {
		return fmt.Errorf("rule %s must call .version(n) with a positive integer", id)
	}
	version := versionValue.ToFloat()
	if math.IsNaN(version) || version < 1 || version > 9007199254740991 || math.Trunc(version) != version {
		return fmt.Errorf("rule %s must call .version(n) with a positive integer", id)
	}
	metadata.Version = int(version)
	decoded.config = moduleValue.ToObject(vm).Get("config")
	if decoded.config != nil && !goja.IsUndefined(decoded.config) {
		obj, ok := decoded.config.(*goja.Object)
		if !ok {
			return fmt.Errorf("config must export a synchronous parse(value) method")
		}
		decoded.parseConfig, ok = goja.AssertFunction(obj.Get("parse"))
		if !ok {
			return fmt.Errorf("config must export a synchronous parse(value) method")
		}
	}
	decoded.programValue = ruleObject.Get("programValue")
	if decoded.programValue == nil || goja.IsUndefined(decoded.programValue) || goja.IsNull(decoded.programValue) {
		return fmt.Errorf("rule is missing program()")
	}
	var valid bool
	decoded.messageFn, valid = goja.AssertFunction(ruleObject.Get("messageFn"))
	if !valid {
		return fmt.Errorf("rule is missing message()")
	}
	if value := ruleObject.Get("factsFn"); value != nil && !goja.IsUndefined(value) && !goja.IsNull(value) {
		decoded.factsFn, valid = goja.AssertFunction(value)
		if !valid {
			return fmt.Errorf("facts() must be given a function")
		}
	}
	metadata.Report, err = stringList(ruleObject.Get("reportPredicates"))
	if err != nil {
		return fmt.Errorf("report(): %w", err)
	}
	if len(metadata.Report) == 0 {
		return fmt.Errorf("rule is missing report()")
	}
	metadata.ExpectNonEmpty, err = stringList(ruleObject.Get("expectNonEmptyPredicates"))
	if err != nil {
		return fmt.Errorf("expectNonEmpty(): %w", err)
	}
	metadata.Suppressions, err = suppressionDeclarations(vm, ruleObject.Get("suppressionDeclarations"))
	if err != nil {
		return fmt.Errorf("suppression(): %w", err)
	}
	return nil
}

// Inspect initializes one entry. Close the returned rule after its last callback.
func Inspect(ctx context.Context, code string) (RuleInspection, error) {
	vm := goja.New()
	keepRuntime := false
	stop := context.AfterFunc(ctx, func() { vm.Interrupt(ctx.Err()) })
	defer func() {
		if !keepRuntime {
			stop()
		}
	}()
	disableAmbientCapabilities(vm)
	if _, err := vm.RunString(`
		const lintaiDetach = (() => {
			const {freeze, getPrototypeOf, getOwnPropertyDescriptor, defineProperty, prototype} = Object;
			const {isArray} = Array, {isFinite} = Number, {ownKeys} = Reflect;
			return function copy(v, stack = []) {
				if (v === null || typeof v === "string" || typeof v === "boolean" || (typeof v === "number" && isFinite(v))) return v;
				if (typeof v !== "object" || stack.includes(v)) throw new TypeError("config.parse must return finite, acyclic JSON data");
				const array = isArray(v);
				if (!array && getPrototypeOf(v) !== prototype && getPrototypeOf(v) !== null) throw new TypeError("config.parse must return synchronous JSON data");
				const out = array ? [] : {};
				const keys = ownKeys(v);
				if (array && keys.length !== v.length + 1) throw new TypeError("config.parse returned a sparse or extended array");
				for (const k of keys) {
					if (array && k === "length") continue;
					const d = getOwnPropertyDescriptor(v, k);
					if (typeof k !== "string" || !d.enumerable || !("value" in d) || (array && (!/^(0|[1-9][0-9]*)$/.test(k) || +k >= v.length))) throw new TypeError("config.parse returned a non-JSON property");
					defineProperty(out, k, {value:copy(d.value, [...stack, v]), enumerable:true});
				}
				return freeze(out);
			}
		})();
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
	detach, _ := goja.AssertFunction(vm.Get("lintaiDetach"))
	strictRowFn, _ := goja.AssertFunction(vm.Get("lintaiStrictRow"))
	var decoded decodedRule
	err := inVM(vm, func() error {
		if _, err := vm.RunString(code); err != nil {
			return err
		}
		return decodeRuleMetadata(vm, &decoded)
	})
	result := RuleInspection{Metadata: decoded.metadata}
	if err != nil {
		return result, err
	}
	result.Rule = &LoadedRule{
		runtime:      vm,
		programValue: decoded.programValue,
		factsFn:      decoded.factsFn,
		messageFn:    decoded.messageFn,
		parseConfig:  decoded.parseConfig,
		config:       decoded.config,
		detach:       detach,
		stop:         stop,
		strictRowFn:  strictRowFn,
	}
	keepRuntime = true
	return result, nil
}

// Program runs facts() and program() and returns the program text and the
// rule's extra extensional tuples.
func (r *LoadedRule) Program() (output ProgramOutput, err error) {
	err = inVM(r.runtime, func() error { var callErr error; output, callErr = r.program(); return callErr })
	return output, err
}

func (r *LoadedRule) program() (ProgramOutput, error) {
	contextObject := r.runtime.NewObject()
	if err := contextObject.Set("env", r.env); err != nil {
		return ProgramOutput{}, err
	}
	var err error
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
	err = inVM(r.runtime, func() error { var callErr error; message, callErr = r.message(row, assertionID); return callErr })
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
	if isPromise(result) {
		return "", fmt.Errorf("message() returned a Promise; message callbacks must be synchronous")
	}
	text, ok := result.Export().(string)
	if !ok {
		return "", fmt.Errorf("message() must return a string")
	}
	return text, nil
}

func isPromise(value goja.Value) bool {
	if value == nil {
		return false
	}
	_, ok := value.Export().(*goja.Promise)
	return ok
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

func (r *LoadedRule) Close() { r.stop() }

// Configure validates options in the rule's VM, then detaches and freezes the result.
func (r *LoadedRule) Configure(options map[string]any) error {
	if options == nil {
		options = map[string]any{}
	}
	return inVM(r.runtime, func() error {
		input, err := toJSValue(r.runtime, options)
		if err != nil {
			return err
		}
		if r.parseConfig != nil {
			input, err = r.parseConfig(r.config, input)
			if err != nil {
				return err
			}
		}
		r.env, err = r.detach(goja.Undefined(), input)
		return err
	})
}

func fragmentText(vm *goja.Runtime, value goja.Value) (string, bool) {
	if value == nil || goja.IsUndefined(value) || goja.IsNull(value) {
		return "", false
	}
	object, ok := value.(*goja.Object)
	if !ok {
		return "", false
	}
	kind := object.Get("__lintaiKind")
	if kind == nil || kind.String() != "fragment" {
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
	if isPromise(value) {
		return nil, fmt.Errorf("returned a Promise; facts callbacks must be synchronous")
	}
	if object, ok := value.(*goja.Object); !ok || object.ClassName() != "Object" {
		return nil, fmt.Errorf("must return a plain object of relations")
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
			function Date() { throw new TypeError(message); }
			Date.now = Date.parse = Date.UTC = Date;
			globalThis.Date = Date;
		})();
	`)
}

func toJSValue(vm *goja.Runtime, value any) (goja.Value, error) {
	raw, err := json.Marshal(value)
	if err != nil {
		return nil, err
	}
	return vm.RunString(fmt.Sprintf("JSON.parse(%q)", string(raw)))
}
