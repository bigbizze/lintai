package runtime

import (
	"bytes"
	"encoding/json"
	"fmt"

	"github.com/dop251/goja"
)

type LoadedRule struct {
	runtime        *goja.Runtime
	id             string
	version        int
	programValue   goja.Value
	factsFn        goja.Callable
	messageFn      goja.Callable
	freezeFn       goja.Callable
	strictRowFn    goja.Callable
	report         []string
	expectNonEmpty []string
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

func LoadPureBundle(code string) (*LoadedRule, error) {
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
		return nil, err
	}
	if _, err := vm.RunString(code); err != nil {
		return nil, err
	}
	moduleValue := vm.Get("LintAIPureModule")
	if goja.IsUndefined(moduleValue) || goja.IsNull(moduleValue) {
		return nil, fmt.Errorf("pure bundle did not initialize LintAIPureModule")
	}
	ruleObject := moduleValue.ToObject(vm).Get("rule").ToObject(vm)
	programValue := ruleObject.Get("programValue")
	if programValue == nil || goja.IsUndefined(programValue) || goja.IsNull(programValue) {
		return nil, fmt.Errorf("rule is missing program()")
	}
	messageFn, ok := goja.AssertFunction(ruleObject.Get("messageFn"))
	if !ok {
		return nil, fmt.Errorf("rule is missing message()")
	}
	var factsFn goja.Callable
	if value := ruleObject.Get("factsFn"); value != nil && !goja.IsUndefined(value) && !goja.IsNull(value) {
		factsFn, ok = goja.AssertFunction(value)
		if !ok {
			return nil, fmt.Errorf("facts() must be given a function")
		}
	}
	report, err := stringList(vm, ruleObject.Get("reportPredicates"))
	if err != nil {
		return nil, fmt.Errorf("report(): %w", err)
	}
	if len(report) == 0 {
		return nil, fmt.Errorf("rule is missing report()")
	}
	expectNonEmpty, err := stringList(vm, ruleObject.Get("expectNonEmptyPredicates"))
	if err != nil {
		return nil, fmt.Errorf("expectNonEmpty(): %w", err)
	}
	freezeFn, _ := goja.AssertFunction(vm.Get("lintaiDeepFreeze"))
	strictRowFn, _ := goja.AssertFunction(vm.Get("lintaiStrictRow"))
	return &LoadedRule{
		runtime:        vm,
		id:             ruleObject.Get("id").String(),
		version:        int(ruleObject.Get("versionValue").ToInteger()),
		programValue:   programValue,
		factsFn:        factsFn,
		messageFn:      messageFn,
		freezeFn:       freezeFn,
		strictRowFn:    strictRowFn,
		report:         report,
		expectNonEmpty: expectNonEmpty,
	}, nil
}

func (r *LoadedRule) RuleID() string {
	return r.id
}

func (r *LoadedRule) RuleVersion() int {
	return r.version
}

func (r *LoadedRule) Report() []string {
	return append([]string(nil), r.report...)
}

func (r *LoadedRule) ExpectNonEmpty() []string {
	return append([]string(nil), r.expectNonEmpty...)
}

// Program runs facts() and program() and returns the program text and the
// rule's extra extensional tuples.
func (r *LoadedRule) Program(env, setup any) (ProgramOutput, error) {
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

func (r *LoadedRule) Message(row map[string]any, assertionID string) (string, error) {
	strictRow, err := r.strictRowFn(goja.Undefined(), r.runtime.ToValue(row))
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

func stringList(vm *goja.Runtime, value goja.Value) ([]string, error) {
	if value == nil || goja.IsUndefined(value) || goja.IsNull(value) {
		return nil, nil
	}
	var items []string
	if err := vm.ExportTo(value, &items); err != nil {
		return nil, err
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
	if dateValue := vm.Get("Date"); !goja.IsUndefined(dateValue) && !goja.IsNull(dateValue) {
		dateObject := dateValue.ToObject(vm)
		_ = dateObject.Set("now", func(goja.FunctionCall) goja.Value {
			panic(vm.NewTypeError("Date.now is disabled in the pure runtime"))
		})
	}
}

func toFrozenValue(vm *goja.Runtime, value any) (goja.Value, error) {
	raw, err := json.Marshal(value)
	if err != nil {
		return nil, err
	}
	return vm.RunString(fmt.Sprintf("JSON.parse(%q)", string(raw)))
}
