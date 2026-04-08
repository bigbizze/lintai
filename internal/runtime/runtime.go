package runtime

import (
	"encoding/json"
	"fmt"

	"github.com/bigbizze/lintai/internal/query"
	"github.com/dop251/goja"
)

type LoadedRule struct {
	runtime     *goja.Runtime
	id          string
	version     int
	assertFn    goja.Callable
	messageFn   goja.Callable
	freezeFn    goja.Callable
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
	moduleObject := moduleValue.ToObject(vm)
	ruleValue := moduleObject.Get("rule")
	ruleObject := ruleValue.ToObject(vm)
	assertFn, ok := goja.AssertFunction(ruleObject.Get("assertFn"))
	if !ok {
		return nil, fmt.Errorf("rule is missing assert()")
	}
	messageFn, ok := goja.AssertFunction(ruleObject.Get("messageFn"))
	if !ok {
		return nil, fmt.Errorf("rule is missing message()")
	}
	freezeFn, _ := goja.AssertFunction(vm.Get("lintaiDeepFreeze"))
	return &LoadedRule{
		runtime:   vm,
		id:        ruleObject.Get("id").String(),
		version:   int(ruleObject.Get("versionValue").ToInteger()),
		assertFn:  assertFn,
		messageFn: messageFn,
		freezeFn:  freezeFn,
	}, nil
}

func (r *LoadedRule) RuleID() string {
	return r.id
}

func (r *LoadedRule) RuleVersion() int {
	return r.version
}

func (r *LoadedRule) Runtime() *goja.Runtime {
	return r.runtime
}

func (r *LoadedRule) BuildAssertions(env, setup any) ([]query.Assertion, error) {
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
	result, err := r.assertFn(goja.Undefined(), contextObject)
	if err != nil {
		return nil, err
	}
	return extractAssertions(r.runtime, result)
}

func (r *LoadedRule) Message(entityView map[string]any, assertionID string) (string, error) {
	result, err := r.messageFn(
		goja.Undefined(),
		r.runtime.ToValue(entityView),
		r.runtime.ToValue(map[string]any{"assertion_id": assertionID}),
	)
	if err != nil {
		return "", err
	}
	return result.String(), nil
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

func extractAssertions(vm *goja.Runtime, value goja.Value) ([]query.Assertion, error) {
	if isAssertion(vm, value) {
		assertion, err := decodeAssertion(vm, "default", value)
		if err != nil {
			return nil, err
		}
		return []query.Assertion{assertion}, nil
	}
	object := value.ToObject(vm)
	keys := object.Keys()
	results := make([]query.Assertion, 0, len(keys))
	for _, key := range keys {
		assertion, err := decodeAssertion(vm, key, object.Get(key))
		if err != nil {
			return nil, err
		}
		results = append(results, assertion)
	}
	return results, nil
}

func decodeAssertion(vm *goja.Runtime, assertionID string, value goja.Value) (query.Assertion, error) {
	if value == nil || goja.IsUndefined(value) || goja.IsNull(value) {
		return query.Assertion{}, fmt.Errorf("assertion %s is empty", assertionID)
	}
	object := value.ToObject(vm)
	planValue := object.Get("query")
	plan, err := decodePlan(vm, planValue)
	if err != nil {
		return query.Assertion{}, err
	}
	return query.Assertion{
		AssertionID: assertionID,
		Terminal:    object.Get("terminal").String(),
		Query:       &plan,
	}, nil
}

func decodePlan(vm *goja.Runtime, value goja.Value) (query.Plan, error) {
	if value == nil || goja.IsUndefined(value) || goja.IsNull(value) {
		return query.Plan{}, fmt.Errorf("query plan is missing")
	}
	object := value.ToObject(vm)
	opsValue := object.Get("ops")
	opsObject := opsValue.ToObject(vm)
	ops := make([]query.Operation, 0, opsObject.Get("length").ToInteger())
	length := int(opsObject.Get("length").ToInteger())
	for index := 0; index < length; index++ {
		item := opsObject.Get(fmt.Sprintf("%d", index)).ToObject(vm)
		operation := query.Operation{
			Type: item.Get("type").String(),
		}
		if nested := item.Get("query"); nested != nil && !goja.IsUndefined(nested) && !goja.IsNull(nested) {
			plan, err := decodePlan(vm, nested)
			if err != nil {
				return query.Plan{}, err
			}
			operation.Query = &plan
		}
		if value := item.Get("value"); value != nil && !goja.IsUndefined(value) && !goja.IsNull(value) {
			operation.Value = value.String()
		}
		if handler := item.Get("handler"); handler != nil && !goja.IsUndefined(handler) && !goja.IsNull(handler) {
			operation.Handler = handler
		}
		ops = append(ops, operation)
	}
	return query.Plan{
		Entity: object.Get("entity").String(),
		Ops:    ops,
	}, nil
}

func isAssertion(vm *goja.Runtime, value goja.Value) bool {
	if value == nil || goja.IsUndefined(value) || goja.IsNull(value) {
		return false
	}
	object := value.ToObject(vm)
	return object.Get("__lintaiKind").String() == "assertion"
}
