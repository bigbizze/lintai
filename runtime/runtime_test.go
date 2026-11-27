package runtime

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

func TestInspectPureBundle(t *testing.T) {
	base := `({
		id: "fixture.rule", versionValue: 1,
		programValue: {__lintaiKind: "fragment", text: "Decl violation(Entity)."},
		factsFn: () => { throw new Error("facts called during inspection") },
		messageFn: () => { throw new Error("message called during inspection") },
		reportPredicates: ["violation"], expectNonEmptyPredicates: ["violation"],
		suppressionDeclarations: [{assertion: "violation", options: {}}]
	})`
	tests := []struct {
		name, script, wantError string
		knownID                 bool
	}{
		{name: "fragment", script: `globalThis.LintAIPureModule = {default: base};`},
		{name: "callback", script: `base.programValue = () => { throw new Error("program called during inspection") }; globalThis.LintAIPureModule = {default: base};`},
		{name: "each property read once", script: `let seen = {}; globalThis.LintAIPureModule = {default: new Proxy(base, {get(target, key) { if (seen[key]) throw new Error("property read twice: " + key); seen[key] = true; return target[key] }})};`},
		{name: "helper", script: `globalThis.LintAIPureModule = {};`, wantError: "default-export"},
		{name: "invalid id", script: `base.id = "Invalid ID"; globalThis.LintAIPureModule = {default: base};`, wantError: "not valid"},
		{name: "invalid version", script: `base.versionValue = 9007199254740992; globalThis.LintAIPureModule = {default: base};`, wantError: "positive integer", knownID: true},
		{name: "missing program", script: `delete base.programValue; globalThis.LintAIPureModule = {default: base};`, wantError: "missing program", knownID: true},
		{name: "missing message", script: `delete base.messageFn; globalThis.LintAIPureModule = {default: base};`, wantError: "missing message", knownID: true},
		{name: "invalid facts", script: `base.factsFn = 2; globalThis.LintAIPureModule = {default: base};`, wantError: "facts()", knownID: true},
		{name: "empty report", script: `base.reportPredicates = []; globalThis.LintAIPureModule = {default: base};`, wantError: "missing report", knownID: true},
		{name: "invalid report", script: `base.reportPredicates = {}; globalThis.LintAIPureModule = {default: base};`, wantError: "report()", knownID: true},
		{name: "non-string list item", script: `base.reportPredicates = [1]; globalThis.LintAIPureModule = {default: base};`, wantError: "item 0 must be a string", knownID: true},
		{name: "invalid expectation", script: `base.expectNonEmptyPredicates = {}; globalThis.LintAIPureModule = {default: base};`, wantError: "expectNonEmpty()", knownID: true},
		{name: "invalid suppression", script: `base.suppressionDeclarations = "bad"; globalThis.LintAIPureModule = {default: base};`, wantError: "suppression()", knownID: true},
		{name: "invalid suppression item", script: `base.suppressionDeclarations = [null]; globalThis.LintAIPureModule = {default: base};`, wantError: "assertion string", knownID: true},
		{name: "throwing rule getter", script: `Object.defineProperty(base, "versionValue", {get() {throw new Error("getter failed")}}); globalThis.LintAIPureModule = {default: base};`, wantError: "getter failed", knownID: true},
		{name: "throwing export", script: `base.suppressionDeclarations = [{get assertion() {throw new Error("export failed")}}]; globalThis.LintAIPureModule = {default: base};`, wantError: "export failed", knownID: true},
		{name: "throwing module getter", script: `Object.defineProperty(globalThis, "LintAIPureModule", {get() {throw new Error("module getter failed")}});`, wantError: "module getter failed"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			code := "const base = " + base + ";\n" + test.script
			inspection, err := Inspect(context.Background(), code)
			if test.wantError != "" {
				if inspection.Rule != nil || err == nil || !strings.Contains(err.Error(), test.wantError) {
					t.Fatalf("invalid rule inspection: %+v %v", inspection, err)
				}
				if test.knownID && inspection.Metadata.ID != "fixture.rule" {
					t.Fatalf("known rule ID lost: %+v", inspection.Metadata)
				}
				return
			}
			if inspection.Rule == nil || err != nil {
				t.Fatalf("valid rule rejected: %+v %v", inspection, err)
			}
			metadata := inspection.Metadata
			if metadata.ID != "fixture.rule" || metadata.Version != 1 ||
				len(metadata.Report) != 1 || metadata.Report[0] != "violation" || len(metadata.ExpectNonEmpty) != 1 || metadata.ExpectNonEmpty[0] != "violation" || len(metadata.Suppressions) != 1 || metadata.Suppressions[0].Assertion != "violation" {
				t.Fatalf("incorrect metadata: %+v", metadata)
			}
		})
	}
}

func TestPureExecutionStateAndExceptions(t *testing.T) {
	code := `let state = "fresh";
	globalThis.LintAIPureModule = {default: {
		get id() { state += ":inspected"; return "fixture.state" }, versionValue: 1,
		factsFn: () => { state += ":facts"; return {} },
		programValue: () => { state += ":program"; return {__lintaiKind: "fragment", text: "Decl violation(Entity)."} },
		messageFn: () => state, reportPredicates: ["violation"], expectNonEmptyPredicates: [], suppressionDeclarations: []
	}};`
	first, err := Inspect(context.Background(), code)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := first.Rule.Program(); err != nil {
		t.Fatal(err)
	}
	if got, err := first.Rule.Message(nil, "violation"); err != nil || got != "fresh:inspected:facts:program" {
		t.Fatalf("same-run state: %q %v", got, err)
	}
	second, err := Inspect(context.Background(), code)
	if err != nil {
		t.Fatal(err)
	}
	if got, err := second.Rule.Message(nil, "violation"); err != nil || got != "fresh:inspected" {
		t.Fatalf("next-run state: %q %v", got, err)
	}

	throwing := strings.Replace(code, `return {__lintaiKind: "fragment", text: "Decl violation(Entity)."}`, `return {get __lintaiKind() { throw new Error("fragment getter failed") }}`, 1)
	rule, err := Inspect(context.Background(), throwing)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := rule.Rule.Program(); err == nil || !strings.Contains(err.Error(), "fragment getter failed") {
		t.Fatalf("program getter error: %v", err)
	}
	throwingMessage := strings.Replace(code, `messageFn: () => state`, `messageFn: () => ({toString() {throw new Error("message conversion failed")}})`, 1)
	rule, err = Inspect(context.Background(), throwingMessage)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := rule.Rule.Message(nil, "violation"); err == nil || !strings.Contains(err.Error(), "message conversion failed") {
		t.Fatalf("message conversion error: %v", err)
	}
}

func TestPureConfiguration(t *testing.T) {
	for _, tc := range []struct {
		name, parse string
		valid       bool
	}{
		{"plain JSON", `v => ({items:[v,null,1]})`, true},
		{"date check", `v => ({isDate:v instanceof Date})`, true},
		{"date call", `() => Date()`, false}, {"date constructor", `() => new Date()`, false},
		{"clock", `() => Date.now()`, false}, {"random", `() => Math.random()`, false},
		{"promise", `async () => ({})`, false}, {"undefined", `() => ({a:undefined})`, false},
		{"cycle", `() => { const a={}; a.a=a; return a }`, false},
		{"sparse", `() => Array(2)`, false}, {"symbol", `() => ({[Symbol()]: 1})`, false},
		{"infinity", `() => [Infinity]`, false}, {"nan", `() => NaN`, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			code := `var LintAIPureModule={config:{parse:` + tc.parse + `},default:{
				id:"pure",versionValue:1,reportPredicates:["v"],messageFn:()=>"ok",
				programValue:({env})=>({__lintaiKind:"fragment",text:String(Object.isFrozen(env) && (!env.items || Object.isFrozen(env.items)))})
			}};`
			inspection, err := Inspect(context.Background(), code)
			if err != nil {
				t.Fatal(err)
			}
			defer inspection.Rule.Close()
			err = inspection.Rule.Configure(map[string]any{"key": "value"})
			if (err == nil) != tc.valid {
				t.Fatalf("config.parse returned %v", err)
			}
			if tc.valid {
				output, err := inspection.Rule.Program()
				if err != nil || output.Text != "true" {
					t.Fatalf("configuration freeze: %+v %v", output, err)
				}
			}
		})
	}
}

func TestInspectionCancellationInterruptsMetadataGetter(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	_, err := Inspect(ctx, `var LintAIPureModule={default:{get id(){ for (;;) {} }}};`)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("getter did not stop on cancellation: %v", err)
	}
}
