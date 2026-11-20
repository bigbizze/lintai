package runtime

import (
	"reflect"
	"strings"
	"testing"
)

func TestMetadataLoaders(t *testing.T) {
	base := `({
		id: "fixture.rule", versionValue: 1, hasSetup: false, hasConfig: true,
		programValue: {__lintaiKind: "fragment", text: "Decl violation(Entity)."},
		factsFn: () => { throw new Error("facts called during inspection") },
		messageFn: () => { throw new Error("message called during inspection") },
		reportPredicates: ["violation"], expectNonEmptyPredicates: ["violation"],
		suppressionDeclarations: [{assertion: "violation", options: {}}]
	})`
	tests := []struct {
		name, setup, wantError string
		helper, knownID        bool
	}{
		{name: "fragment", setup: `globalThis.LintAIPureModule = {rule: base};`},
		{name: "callback", setup: `base.programValue = () => { throw new Error("program called during inspection") }; globalThis.LintAIPureModule = {rule: base};`},
		{name: "each property read once", setup: `let seen = {}; globalThis.LintAIPureModule = {rule: new Proxy(base, {get(target, key) { if (seen[key]) throw new Error("property read twice: " + key); seen[key] = true; return target[key] }})};`},
		{name: "helper", setup: `globalThis.LintAIPureModule = {};`, helper: true},
		{name: "invalid id", setup: `base.id = "Invalid ID"; globalThis.LintAIPureModule = {rule: base};`, wantError: "not valid"},
		{name: "invalid version", setup: `base.versionValue = 9007199254740992; globalThis.LintAIPureModule = {rule: base};`, wantError: "positive integer", knownID: true},
		{name: "invalid flags", setup: `base.hasSetup = 1; globalThis.LintAIPureModule = {rule: base};`, wantError: "invalid setup or config", knownID: true},
		{name: "missing program", setup: `delete base.programValue; globalThis.LintAIPureModule = {rule: base};`, wantError: "missing program", knownID: true},
		{name: "missing message", setup: `delete base.messageFn; globalThis.LintAIPureModule = {rule: base};`, wantError: "missing message", knownID: true},
		{name: "invalid facts", setup: `base.factsFn = 2; globalThis.LintAIPureModule = {rule: base};`, wantError: "facts()", knownID: true},
		{name: "empty report", setup: `base.reportPredicates = []; globalThis.LintAIPureModule = {rule: base};`, wantError: "missing report", knownID: true},
		{name: "invalid report", setup: `base.reportPredicates = {}; globalThis.LintAIPureModule = {rule: base};`, wantError: "report()", knownID: true},
		{name: "non-string list item", setup: `base.reportPredicates = [1]; globalThis.LintAIPureModule = {rule: base};`, wantError: "item 0 must be a string", knownID: true},
		{name: "invalid expectation", setup: `base.expectNonEmptyPredicates = {}; globalThis.LintAIPureModule = {rule: base};`, wantError: "expectNonEmpty()", knownID: true},
		{name: "invalid suppression", setup: `base.suppressionDeclarations = "bad"; globalThis.LintAIPureModule = {rule: base};`, wantError: "suppression()", knownID: true},
		{name: "invalid suppression item", setup: `base.suppressionDeclarations = [null]; globalThis.LintAIPureModule = {rule: base};`, wantError: "assertion string", knownID: true},
		{name: "throwing rule getter", setup: `Object.defineProperty(base, "versionValue", {get() {throw new Error("getter failed")}}); globalThis.LintAIPureModule = {rule: base};`, wantError: "getter failed", knownID: true},
		{name: "throwing export", setup: `base.suppressionDeclarations = [{get assertion() {throw new Error("export failed")}}]; globalThis.LintAIPureModule = {rule: base};`, wantError: "export failed", knownID: true},
		{name: "throwing module getter", setup: `Object.defineProperty(globalThis, "LintAIPureModule", {get() {throw new Error("module getter failed")}});`, wantError: "module getter failed"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			code := "const base = " + base + ";\n" + test.setup
			metadata, ok, metadataErr := ReadRuleMetadata(code)
			loaded, loadErr := LoadPureBundle(code)
			if test.helper {
				if ok || metadataErr != nil || loadErr == nil {
					t.Fatalf("helper listing/execution: ok=%v metadataErr=%v loadErr=%v", ok, metadataErr, loadErr)
				}
				return
			}
			if test.wantError != "" {
				if !ok || metadataErr == nil || loadErr == nil || !strings.Contains(metadataErr.Error(), test.wantError) || metadataErr.Error() != loadErr.Error() {
					t.Fatalf("loaders disagree: ok=%v metadataErr=%v loadErr=%v", ok, metadataErr, loadErr)
				}
				if test.knownID && metadata.ID != "fixture.rule" {
					t.Fatalf("known rule ID lost: %+v", metadata)
				}
				return
			}
			if !ok || metadataErr != nil || loadErr != nil {
				t.Fatalf("valid rule rejected: ok=%v metadataErr=%v loadErr=%v", ok, metadataErr, loadErr)
			}
			if loaded.RuleID() != metadata.ID || loaded.RuleVersion() != metadata.Version || !reflect.DeepEqual(loaded.Report(), metadata.Report) || !reflect.DeepEqual(loaded.ExpectNonEmpty(), metadata.ExpectNonEmpty) || !reflect.DeepEqual(loaded.SuppressionDeclarations(), metadata.Suppressions) {
				t.Fatalf("metadata differs from execution rule: %+v %+v", metadata, loaded)
			}
		})
	}
}

func TestPureExecutionStateAndExceptions(t *testing.T) {
	code := `let state = "fresh";
	globalThis.LintAIPureModule = {rule: {
		id: "fixture.state", versionValue: 1, hasSetup: false, hasConfig: false,
		programValue: () => { state = "ready"; return {__lintaiKind: "fragment", text: "Decl violation(Entity)."} },
		messageFn: () => state, reportPredicates: ["violation"], expectNonEmptyPredicates: [], suppressionDeclarations: []
	}};`
	first, err := LoadPureBundle(code)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := first.Program(nil, nil); err != nil {
		t.Fatal(err)
	}
	if got, err := first.Message(nil, "violation"); err != nil || got != "ready" {
		t.Fatalf("same-run state: %q %v", got, err)
	}
	second, err := LoadPureBundle(code)
	if err != nil {
		t.Fatal(err)
	}
	if got, err := second.Message(nil, "violation"); err != nil || got != "fresh" {
		t.Fatalf("next-run state: %q %v", got, err)
	}

	throwing := strings.Replace(code, `return {__lintaiKind: "fragment", text: "Decl violation(Entity)."}`, `return {get __lintaiKind() { throw new Error("fragment getter failed") }}`, 1)
	rule, err := LoadPureBundle(throwing)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := rule.Program(nil, nil); err == nil || !strings.Contains(err.Error(), "fragment getter failed") {
		t.Fatalf("program getter error: %v", err)
	}
	throwingMessage := strings.Replace(code, `messageFn: () => state`, `messageFn: () => ({toString() {throw new Error("message conversion failed")}})`, 1)
	rule, err = LoadPureBundle(throwingMessage)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := rule.Message(nil, "violation"); err == nil || !strings.Contains(err.Error(), "message conversion failed") {
		t.Fatalf("message conversion error: %v", err)
	}
}
