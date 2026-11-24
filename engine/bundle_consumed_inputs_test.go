package engine

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/bigbizze/lintai/bundle"
	"github.com/bigbizze/lintai/runtime"
)

// bundleEditAsset copies the real helper and injects an edit after both builds.
// The control file is created before any builds and is not a bundle input.
func bundleEditAsset(t *testing.T, repoRoot, control string) string {
	t.Helper()
	asset := t.TempDir()
	original := filepath.Join(repoRoot, "packages", "client")
	entries, err := os.ReadDir(filepath.Join(original, "scripts"))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(asset, "scripts"), 0o755); err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		data, err := os.ReadFile(filepath.Join(original, "scripts", entry.Name()))
		if err != nil {
			t.Fatal(err)
		}
		if entry.Name() == "bundle-rule.mjs" {
			anchor := "\t\tconst { hash, inputs, probes } = await ruleHash("
			if strings.Count(string(data), anchor) != 1 {
				t.Fatalf("bundle edit anchor occurs %d times", strings.Count(string(data), anchor))
			}
			edit := fmt.Sprintf(`		if ((await fs.readFile(%q, "utf8")).trim() === "armed") {
			await fs.writeFile(resolvedRulePath, (await fs.readFile(resolvedRulePath, "utf8")).replaceAll("diagnostic-A", "diagnostic-B"));
		}
		if ((await fs.readFile(%q, "utf8")).trim() === "config-aba") {
			const configPath = path.join(path.dirname(resolvedRulePath), "tsconfig.json");
			const config = await fs.readFile(configPath, "utf8");
			await fs.writeFile(configPath, config.replace("rules-support/b.ts", "rules-support/a.ts"));
		}
		if ((await fs.readFile(%q, "utf8")).trim() === "delete-after") {
			await fs.unlink(resolvedRulePath);
		}
		const externalAfter = (await fs.readFile(%q, "utf8")).trim();
		if (externalAfter.startsWith("config-external-aba:")) {
			await fs.writeFile(externalAfter.slice("config-external-aba:".length), JSON.stringify({compilerOptions: {useDefineForClassFields: false}}));
		}
		if (externalAfter.startsWith("config-path-base-aba:")) {
			const file = externalAfter.slice("config-path-base-aba:".length);
			await fs.writeFile(file, (await fs.readFile(file, "utf8")).replace("rules-support/b/nested", "rules-support/a/nested"));
		}
`, control, control, control, control)
			data = []byte(strings.Replace(string(data), anchor, edit+anchor, 1))
			between := "\t\tconst pureBuild = await build({"
			if strings.Count(string(data), between) != 1 {
				t.Fatalf("between-build edit anchor occurs %d times", strings.Count(string(data), between))
			}
			editBetween := fmt.Sprintf(`		if ((await fs.readFile(%q, "utf8")).trim() === "between") {
			await fs.writeFile(resolvedRulePath, (await fs.readFile(resolvedRulePath, "utf8")).replaceAll("diagnostic-A", "diagnostic-B"));
		}
		if (["config-between", "config-aba"].includes((await fs.readFile(%q, "utf8")).trim())) {
			const configPath = path.join(path.dirname(resolvedRulePath), "tsconfig.json");
			const config = await fs.readFile(configPath, "utf8");
			const changed = config.replace("rules-support/a.ts", "rules-support/b.ts");
			if (changed === config) throw new Error("config edit anchor missing");
			await fs.writeFile(configPath, changed);
		}
		const externalBetween = (await fs.readFile(%q, "utf8")).trim();
		if (externalBetween.startsWith("config-external-aba:")) {
			await fs.writeFile(externalBetween.slice("config-external-aba:".length), JSON.stringify({compilerOptions: {useDefineForClassFields: true}}));
			await fs.writeFile(%q + ".hook", "reached");
		}
		if (externalBetween.startsWith("config-path-base-aba:")) {
			const file = externalBetween.slice("config-path-base-aba:".length);
			const contents = await fs.readFile(file, "utf8");
			const changed = contents.replace("rules-support/a/nested", "rules-support/b/nested");
			if (changed === contents) throw new Error("base config edit anchor missing");
			await fs.writeFile(file, changed);
			await fs.writeFile(%q + ".hook", "reached");
		}
`, control, control, control, control, control)
			data = []byte(strings.Replace(string(data), between, editBetween+between, 1))
			beforeView := "\t\tview = await createInputView("
			if strings.Count(string(data), beforeView) != 1 {
				t.Fatalf("view edit anchor occurs %d times", strings.Count(string(data), beforeView))
			}
			editBeforeView := fmt.Sprintf(`		const lookupEdit = (await fs.readFile(%q, "utf8")).trim();
		if (lookupEdit.startsWith("config-path-lookup:")) {
			const contents = await fs.readFile(resolvedRulePath, "utf8");
			const changed = contents.replace("@fixture/safe", lookupEdit.slice("config-path-lookup:".length));
			if (changed === contents) throw new Error("lookup edit anchor missing");
			await fs.writeFile(resolvedRulePath, changed);
			await fs.writeFile(%q + ".hook", "reached");
		}
`, control, control)
			data = []byte(strings.Replace(string(data), beforeView, editBeforeView+beforeView, 1))
		}
		if err := os.WriteFile(filepath.Join(asset, "scripts", entry.Name()), data, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	metadata, err := os.ReadFile(filepath.Join(original, "package.json"))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(asset, "package.json"), metadata, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(asset, "node_modules", "@lintai"), 0o755); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"esbuild", "typescript", "@lintai/sdk"} {
		if err := os.Symlink(filepath.Join(original, "node_modules", name), filepath.Join(asset, "node_modules", name)); err != nil {
			t.Fatal(err)
		}
	}
	return asset
}

func TestBundleConsumedInputEdit(t *testing.T) {
	repoRoot, err := filepath.Abs("..")
	if err != nil {
		t.Fatal(err)
	}
	workspace := t.TempDir()
	control := filepath.Join(t.TempDir(), "edit-control")
	setControl := func(value string) {
		t.Helper()
		if err := os.WriteFile(control, []byte(value), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	setControl("disarmed")
	asset := bundleEditAsset(t, repoRoot, control)
	program := "Decl violation(Entity, Code). violation(Entity, Code) :- catalog.usable(Entity, Code)."
	source := parallelRuleSource("fixture.bundle-input", program, `() => "diagnostic-A"`)
	source = strings.Replace(source, ".program(", `.setup(() => ({marker: "diagnostic-A"})).program(`, 1)
	rulePath := writeTestFile(t, workspace, "lintai-rules/input.ts", strings.ReplaceAll(source, "diagnostic-A", "diagnostic-B"))
	realRulePath, err := filepath.EvalSymlinks(rulePath)
	if err != nil {
		t.Fatal(err)
	}
	writeA := func() {
		t.Helper()
		if err := os.WriteFile(rulePath, []byte(source), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	build := func() (bundle.Artifacts, error) {
		t.Helper()
		artifacts, failures, err := bundle.BuildAll(context.Background(), asset, repoRoot, workspace, t.TempDir(), []string{rulePath})
		if err != nil {
			return bundle.Artifacts{}, err
		}
		if err := failures[rulePath]; err != nil {
			return bundle.Artifacts{}, err
		}
		return artifacts[rulePath], nil
	}
	behavior := func(artifact bundle.Artifacts) string {
		t.Helper()
		code, err := os.ReadFile(artifact.PureBundlePath)
		if err != nil {
			t.Fatal(err)
		}
		loaded, err := runtime.InspectPureBundle(string(code))
		if err != nil {
			t.Fatal(err)
		}
		message, err := loaded.Rule.Message(map[string]any{}, "violation")
		if err != nil {
			t.Fatal(err)
		}
		return message
	}
	stable, err := build()
	if err != nil {
		t.Fatal(err)
	}
	stableAgain, err := build()
	if err != nil {
		t.Fatal(err)
	}
	if stable.Hash != stableAgain.Hash || behavior(stable) != "diagnostic-B" || behavior(stableAgain) != "diagnostic-B" {
		t.Fatalf("stable B must reproduce identity and behavior: %s %s", stable.Hash, stableAgain.Hash)
	}
	for _, input := range stable.Observations.Records() {
		if input.Path == control {
			t.Fatal("control file appeared in bundle inputs")
		}
	}
	t.Logf("stable B: hash=%s behavior=diagnostic-B; different output directories agree", stable.Hash)

	writeA()
	stableA, err := build()
	if err != nil {
		t.Fatal(err)
	}
	setControl("armed")
	edited, editErr := build()
	if editErr != nil || behavior(edited) != "diagnostic-A" || edited.Hash != stableA.Hash || bundleUnchanged(edited.Observations) {
		t.Fatalf("post-capture edit did not publish captured A with stale evidence: artifact=%+v err=%v", edited, editErr)
	}
	setControl("disarmed")
	writeA()
	setControl("between")
	betweenArtifact, betweenErr := build()
	if betweenErr != nil {
		t.Fatalf("between-build capture: %v", betweenErr)
	}
	prepared, failures, err := bundle.PrepareAll(context.Background(), asset, repoRoot, workspace, map[string]bundle.Artifacts{rulePath: betweenArtifact}, map[string]any{}, bundle.PrepareOptions{})
	if err != nil || len(failures) != 0 {
		t.Fatalf("prepare between-build artifact: %v %+v", err, failures)
	}
	setup, ok := prepared[rulePath].Setup.(map[string]any)
	if !ok || setup["marker"] != "diagnostic-A" || behavior(betweenArtifact) != "diagnostic-A" || betweenArtifact.Hash != stableA.Hash || bundleUnchanged(betweenArtifact.Observations) {
		t.Fatalf("prepare and pure did not retain captured A: setup=%+v pure=%s", prepared[rulePath].Setup, behavior(betweenArtifact))
	}
	setControl("disarmed")

	options := parallelFixtureOptions(repoRoot, workspace)
	options.AssetRoot = asset
	writeA()
	setControl("armed")
	warm := Open(Options{}, newParallelFixtureProducer())
	defer warm.Close()
	first, err := warm.Check(context.Background(), options)
	if err != nil {
		t.Fatal(err)
	}
	before := warm.executionObservation().bundleArtifacts
	setControl("disarmed")
	second, err := warm.Check(context.Background(), options)
	if err != nil {
		t.Fatal(err)
	}
	after := warm.executionObservation().bundleArtifacts
	fresh := Open(Options{}, newParallelFixtureProducer())
	defer fresh.Close()
	freshResult, err := fresh.Check(context.Background(), options)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("retained A-to-B then unchanged B: first=%+v second=%+v fresh=%+v bundle builds=%d->%d", first.Diagnostics, second.Diagnostics, freshResult.Diagnostics, before, after)
	if !reflect.DeepEqual(second, freshResult) {
		t.Errorf("unchanged B retained result differs from fresh B")
	}
	if len(first.Diagnostics) != 3 || first.Diagnostics[0].Message != "diagnostic-A" || before != 1 || after != 2 {
		t.Fatalf("captured A was not replaced by B on the next check: first=%+v builds=%d->%d", first.Diagnostics, before, after)
	}
	if len(second.Diagnostics) != 3 || second.Diagnostics[0].Message != "diagnostic-B" {
		t.Fatalf("retained B did not run: %+v", second.Diagnostics)
	}
	third, err := warm.Check(context.Background(), options)
	if err != nil || !reflect.DeepEqual(third, second) || warm.executionObservation().bundleArtifacts != after {
		t.Fatalf("unchanged B was not reused: %v %+v", err, third.Diagnostics)
	}

	lock, _, err := BuildRulesLock(context.Background(), options)
	if err != nil {
		t.Fatal(err)
	}
	if err := WriteRulesLock(context.Background(), filepath.Join(workspace, DefaultRulesLockFile), lock); err != nil {
		t.Fatal(err)
	}
	options.RulesLock = DefaultRulesLockFile
	locked := Open(Options{}, newParallelFixtureProducer())
	defer locked.Close()
	approved, err := locked.Check(context.Background(), options)
	if err != nil {
		t.Fatal(err)
	}
	if len(approved.Diagnostics) != 3 || approved.Diagnostics[0].Message != "diagnostic-B" {
		t.Fatalf("stable B lock did not authorize B: %+v", approved.Diagnostics)
	}
	writeA()
	setControl("armed")
	lockedEdit := Open(Options{}, newParallelFixtureProducer())
	defer lockedEdit.Close()
	lockedResult, err := lockedEdit.Check(context.Background(), options)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("B lock with A-to-B edit: %+v", lockedResult.Diagnostics)
	if len(lockedResult.Diagnostics) != 1 || lockedResult.Diagnostics[0].Phase != "integrity" || !strings.Contains(lockedResult.Diagnostics[0].Message, "bundle changed since the rules lock was written") {
		t.Fatalf("B lock authorized captured A: %+v", lockedResult.Diagnostics)
	}

	writeA()
	setControl("delete-after")
	if artifact, err := build(); err == nil {
		if behavior(artifact) != "diagnostic-A" || artifact.Hash != stableA.Hash || bundleUnchanged(artifact.Observations) {
			t.Errorf("deleted source lost captured evidence: %+v", artifact)
		}
	} else if !strings.Contains(err.Error(), realRulePath) {
		t.Errorf("unexpected deleted-source rejection: %v", err)
	}

}

func TestBundleHelperInputEdit(t *testing.T) {
	repo, err := filepath.Abs("..")
	if err != nil {
		t.Fatal(err)
	}
	workspace := t.TempDir()
	control := filepath.Join(t.TempDir(), "control")
	if err := os.WriteFile(control, []byte("disarmed"), 0o600); err != nil {
		t.Fatal(err)
	}
	asset := bundleEditAsset(t, repo, control)
	helper := filepath.Join(asset, "scripts", "bundle-rule.mjs")
	source, err := os.ReadFile(helper)
	if err != nil {
		t.Fatal(err)
	}
	entryAnchor := "\t\tconst prepareBundlePath = path.join(outDir, \"prepare.cjs\");"
	captureAnchor := "\t\tawait captureTools(initialInputs, workspaceRoot, repoRoot);"
	if strings.Count(string(source), entryAnchor) != 1 || strings.Count(string(source), captureAnchor) != 1 {
		t.Fatal("helper edit anchors are not unique")
	}
	entryEdit := `		const marker = "A";
		await fs.writeFile(pureEntry, (await fs.readFile(pureEntry, "utf8")).replace("messageFn: rule.messageFn", ` + "`" + `messageFn: () => ${JSON.stringify(marker)}` + "`" + `));
`
	captureEdit := fmt.Sprintf(`		if ((await fs.readFile(%q, "utf8")).trim() === "tool-armed") {
			const original = path.join(process.argv[2], "scripts", "bundle-rule.mjs");
			await fs.writeFile(original, (await fs.readFile(original, "utf8")).replace('const marker = "A";', 'const marker = "B";'));
		}
`, control)
	a := strings.Replace(string(source), entryAnchor, entryEdit+entryAnchor, 1)
	a = strings.Replace(a, captureAnchor, captureEdit+captureAnchor, 1)
	b := strings.Replace(a, `const marker = "A";`, `const marker = "B";`, 1)
	rule := writeTestFile(t, workspace, "lintai-rules/rule.ts", parallelRuleSource("fixture.tool-input", "Decl violation(Entity).", `() => "source"`))
	build := func() (map[string]bundle.Artifacts, map[string]error) {
		t.Helper()
		artifacts, failures, err := bundle.BuildAll(context.Background(), asset, repo, workspace, t.TempDir(), []string{rule})
		if err != nil {
			t.Fatal(err)
		}
		return artifacts, failures
	}
	if err := os.WriteFile(helper, []byte(b), 0o600); err != nil {
		t.Fatal(err)
	}
	stable, failures := build()
	if failures[rule] != nil || len(stable) != 1 {
		t.Fatalf("stable helper B: %v", failures[rule])
	}
	code, err := os.ReadFile(stable[rule].PureBundlePath)
	if err != nil {
		t.Fatal(err)
	}
	loaded, err := runtime.InspectPureBundle(string(code))
	if err != nil {
		t.Fatal(err)
	}
	if message, err := loaded.Rule.Message(nil, "violation"); err != nil || message != "B" {
		t.Fatalf("stable helper B behavior: %q %v", message, err)
	}
	if err := os.WriteFile(helper, []byte(a), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(control, []byte("tool-armed"), 0o600); err != nil {
		t.Fatal(err)
	}
	edited, failures := build()
	if failures[rule] != nil || len(edited) != 1 || edited[rule].Hash == stable[rule].Hash || bundleUnchanged(edited[rule].Observations) {
		t.Fatalf("helper A-to-B must retain A identity and stale evidence: artifacts=%+v error=%v", edited, failures[rule])
	}
	captured, err := os.ReadFile(edited[rule].PureBundlePath)
	if err != nil {
		t.Fatal(err)
	}
	capturedRule, err := runtime.InspectPureBundle(string(captured))
	if err != nil {
		t.Fatal(err)
	}
	if message, err := capturedRule.Rule.Message(nil, "violation"); err != nil || message != "A" {
		t.Fatalf("captured helper behavior: %q %v", message, err)
	}

	if err := os.WriteFile(control, []byte("disarmed"), 0o600); err != nil {
		t.Fatal(err)
	}
	oldHelper := filepath.Join(asset, "helper-a.mjs")
	newHelper := filepath.Join(asset, "helper-b.mjs")
	if err := os.WriteFile(oldHelper, []byte(a), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(newHelper, []byte(b), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(helper); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(oldHelper, helper); err != nil {
		t.Fatal(err)
	}
	linked, failures := build()
	if failures[rule] != nil || len(linked) != 1 {
		t.Fatalf("linked helper A: %v", failures[rule])
	}
	if err := os.Remove(helper); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(newHelper, helper); err != nil {
		t.Fatal(err)
	}
	if bundleUnchanged(linked[rule].Observations) {
		t.Error("retargeting the helper link did not invalidate the artifact")
	}
	rebuilt, failures := build()
	if failures[rule] != nil || len(rebuilt) != 1 || rebuilt[rule].Hash == linked[rule].Hash {
		t.Fatalf("retargeted helper B: artifacts=%+v failures=%v", rebuilt, failures[rule])
	}
}

// Tool code is loaded before the builds, so hashing the installed file only
// after loading it must not relabel a bundle with a later version of that code.
func TestBundleToolInputEdit(t *testing.T) {
	repo, err := filepath.Abs("..")
	if err != nil {
		t.Fatal(err)
	}
	workspace := t.TempDir()
	control := filepath.Join(t.TempDir(), "control")
	if err := os.WriteFile(control, []byte("disarmed"), 0o600); err != nil {
		t.Fatal(err)
	}
	asset := bundleEditAsset(t, repo, control)
	original, err := filepath.EvalSymlinks(filepath.Join(repo, "packages", "client", "node_modules", "esbuild"))
	if err != nil {
		t.Fatal(err)
	}
	toolDir := filepath.Join(asset, "node_modules", "esbuild")
	if err := os.Remove(toolDir); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(toolDir, "lib"), 0o755); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"package.json", "lib/main.js"} {
		data, err := os.ReadFile(filepath.Join(original, name))
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(toolDir, name), data, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Symlink(filepath.Join(filepath.Dir(original), "@esbuild"), filepath.Join(asset, "node_modules", "@esbuild")); err != nil {
		t.Fatal(err)
	}
	tool := filepath.Join(toolDir, "lib", "main.js")
	code, err := os.ReadFile(tool)
	if err != nil {
		t.Fatal(err)
	}
	anchor := `var build = (options) => ensureServiceIsRunning().build(options);`
	if strings.Count(string(code), anchor) != 1 {
		t.Fatal("esbuild test anchor is not unique")
	}
	toolA := `var build = (options) => ensureServiceIsRunning().build({...options, define: {...options.define, __ESBUILD_TOOL_MARKER__: '"A"'}});`
	toolB := strings.Replace(toolA, `'"A"'`, `'"B"'`, 1)
	a := strings.Replace(string(code), anchor, toolA, 1)
	b := strings.Replace(string(code), anchor, toolB, 1)
	helper := filepath.Join(asset, "scripts", "bundle-rule.mjs")
	helperCode, err := os.ReadFile(helper)
	if err != nil {
		t.Fatal(err)
	}
	editAnchor := "\t\tconst initialInputs = new Map(view.inputs);"
	if strings.Count(string(helperCode), editAnchor) != 1 {
		t.Fatal("tool edit anchor is not unique")
	}
	edit := fmt.Sprintf(`		if ((await fs.readFile(%q, "utf8")).trim() === "esbuild-armed") {
			const tool = createRequire(import.meta.url).resolve("esbuild");
			await fs.writeFile(tool, (await fs.readFile(tool, "utf8")).replace(%q, %q));
		}
`, control, toolA, toolB)
	if err := os.WriteFile(helper, []byte(strings.Replace(string(helperCode), editAnchor, edit+editAnchor, 1)), 0o600); err != nil {
		t.Fatal(err)
	}
	rule := writeTestFile(t, workspace, "lintai-rules/rule.ts", parallelRuleSource("fixture.esbuild-input", "Decl violation(Entity).", `() => __ESBUILD_TOOL_MARKER__`))
	build := func() (map[string]bundle.Artifacts, map[string]error) {
		t.Helper()
		artifacts, failures, err := bundle.BuildAll(context.Background(), asset, repo, workspace, t.TempDir(), []string{rule})
		if err != nil {
			t.Fatal(err)
		}
		return artifacts, failures
	}
	message := func(artifact bundle.Artifacts) string {
		t.Helper()
		data, err := os.ReadFile(artifact.PureBundlePath)
		if err != nil {
			t.Fatal(err)
		}
		loaded, err := runtime.InspectPureBundle(string(data))
		if err != nil {
			t.Fatal(err)
		}
		result, err := loaded.Rule.Message(nil, "violation")
		if err != nil {
			t.Fatal(err)
		}
		return result
	}
	if err := os.WriteFile(tool, []byte(b), 0o600); err != nil {
		t.Fatal(err)
	}
	stable, failures := build()
	if failures[rule] != nil || len(stable) != 1 || message(stable[rule]) != "B" {
		t.Fatalf("stable tool B: artifacts=%+v failures=%v", stable, failures[rule])
	}
	if err := os.WriteFile(tool, []byte(a), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(control, []byte("esbuild-armed"), 0o600); err != nil {
		t.Fatal(err)
	}
	edited, failures := build()
	if failures[rule] == nil {
		got := message(edited[rule])
		t.Logf("tool A-to-B: message=%s identity equals stable B=%v", got, edited[rule].Hash == stable[rule].Hash)
		if got == "A" && edited[rule].Hash == stable[rule].Hash {
			t.Error("tool A artifact claimed stable B identity")
		}
	}

	// Retargeting the dependency link changes the tool selected by the next
	// build even when the previous physical file remains intact.
	if err := os.WriteFile(control, []byte("disarmed"), 0o600); err != nil {
		t.Fatal(err)
	}
	oldDir := filepath.Join(asset, "node_modules", "esbuild-old")
	newDir := filepath.Join(asset, "node_modules", "esbuild-new")
	if err := os.Rename(toolDir, oldDir); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(newDir, "lib"), 0o755); err != nil {
		t.Fatal(err)
	}
	metadata, err := os.ReadFile(filepath.Join(oldDir, "package.json"))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(newDir, "package.json"), metadata, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(newDir, "lib", "main.js"), []byte(a), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(oldDir, toolDir); err != nil {
		t.Fatal(err)
	}
	selected, failures := build()
	if failures[rule] != nil || len(selected) != 1 || message(selected[rule]) != "B" {
		t.Fatalf("linked tool B: %v", failures[rule])
	}
	if err := os.Remove(toolDir); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(newDir, toolDir); err != nil {
		t.Fatal(err)
	}
	if bundleUnchanged(selected[rule].Observations) {
		t.Error("retargeting the esbuild dependency link did not invalidate the artifact")
	}
	rebuilt, failures := build()
	if failures[rule] != nil || len(rebuilt) != 1 || message(rebuilt[rule]) != "A" || rebuilt[rule].Hash == selected[rule].Hash {
		t.Fatalf("retargeted tool A: artifacts=%+v failures=%v", rebuilt, failures[rule])
	}
}
