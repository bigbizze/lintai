package typescript

import (
	"context"
	"testing"
)

func TestImportMetaSyntaxAndOwnership(t *testing.T) {
	const source = "export async function read(arg = import.meta.env) {\n" +
		"  const text = /import.meta.env/; const literal = \"import.meta.env\"; // import.meta.url\n" +
		"  const template = `${import.meta.env.API_URL}`;\n" +
		"  const spaced = import /*x*/ . meta /*y*/ . env;\n" +
		"  const icon = \"😀\"; import.meta.env.MODE;\n" +
		"  await work();\n" +
		"}\n" +
		"class Absent {\n" +
		"  pattern = /import.meta.env/;\n" +
		"  field = import.meta.env;\n" +
		"  pending = await work();\n" +
		"  missing = work();\n" +
		"}\n" +
		"class Present {\n" +
		"  first = import.meta.env;\n" +
		"  second = import.meta.url;\n" +
		"  constructor(x: number);\n" +
		"  constructor(x?: number) {}\n" +
		"}\n" +
		"class Ambiguous {\n" +
		"  first = import.meta.env;\n" +
		"  second = import.meta.url;\n" +
		"  constructor() {} constructor(x: number) {}\n" +
		"}\n" +
		"async function iterate() { for await (const x of stream()) {} }\n" +
		"import.meta.url;\n" +
		"await start();\n" +
		"const other = [import.meta?.env, import.meta[\"env\"], (import.meta).env];\n"
	root := t.TempDir()
	writeWorkspaceFile(t, root, "src/main.ts", source)
	snapshot, sources, _, err := New(Options{Files: FileScopeWalk}).buildLintaiSnapshot(context.Background(), root, root)
	if err != nil {
		t.Fatal(err)
	}
	description, err := New().Describe()
	if err != nil {
		t.Fatal(err)
	}
	items := inspectLocalSnapshot(t, snapshot, sources, convertAdjustments(root, snapshot.ConfigAdjustments), description, nil, false)
	rows := make(map[string]map[string]testFact)
	for _, item := range items {
		if rows[item.Relation] == nil {
			rows[item.Relation] = make(map[string]testFact)
		}
		if item.Relation == "meta_access" {
			if _, exists := rows[item.Relation][item.Values[0].String]; exists {
				t.Fatalf("duplicate meta access: %v", item)
			}
		}
		rows[item.Relation][item.Values[0].String] = item
	}
	functionID := func(name string) string {
		for _, fn := range snapshot.Functions {
			if fn.Name == name {
				return fn.SemanticKey
			}
		}
		t.Fatalf("missing function %s", name)
		return ""
	}
	owner := functionID("read")
	constructor := functionID("Present.constructor")
	want := []struct {
		path, owner, reason string
		line, start, end    int64
	}{
		{"env", owner, "", 1, 34, 49},
		{"env", owner, "", 3, 23, 38},
		{"env", owner, "", 4, 18, 49},
		{"env", owner, "", 5, 22, 37},
		{"env", "", "instance_construction", 10, 11, 26},
		{"env", constructor, "", 15, 11, 26},
		{"env", "", "unsupported_context", 21, 11, 26},
		{"url", constructor, "", 16, 12, 27},
		{"url", "", "unsupported_context", 22, 12, 27},
		{"url", "", "", 26, 1, 16},
	}
	if len(rows["meta_access"]) != len(want) {
		t.Fatalf("meta accesses: got %v, want %d", rows["meta_access"], len(want))
	}
	for _, expected := range want {
		path := "import.meta." + expected.path
		id := ""
		for candidate := range rows["meta_access"] {
			if location := rows["location"][candidate]; len(location.Values) > 2 && location.Values[2].Number == expected.line {
				id = candidate
				break
			}
		}
		access, ok := rows["meta_access"][id]
		if !ok || access.Values[1].String != path {
			t.Errorf("access %s: %v", id, access)
			continue
		}
		location, ok := rows["location"][id]
		if !ok || location.Values[1].String != "src/main.ts" || location.Values[2].Number != expected.line ||
			location.Values[3].Number != expected.start || location.Values[4].Number != expected.line || location.Values[5].Number != expected.end {
			t.Errorf("access %s location: %v", id, location)
		}
		actualOwner, hasOwner := rows["site_owner"][id]
		expectedOwner := expected.owner
		if expectedOwner == "" && expected.reason == "" {
			expectedOwner = "module:src/main.ts"
		}
		if hasOwner != (expectedOwner != "") || hasOwner && actualOwner.Values[1].String != expectedOwner {
			t.Errorf("access %s owner: %v", id, actualOwner)
		}
		marker, hasMarker := rows["site_attribution_unsupported"][id]
		if hasMarker != (expected.reason != "") || hasMarker && marker.Values[1].String != expected.reason {
			t.Errorf("access %s unsupported marker: %v", id, marker)
		}
	}

	wantSuspension := map[int]struct {
		kind, owner, reason string
		module              bool
	}{
		6:  {"await", owner, "", false},
		11: {"await", "", "instance_construction", false},
		25: {"for_await", functionID("iterate"), "", false},
		27: {"await", "", "", true},
	}
	if len(snapshot.SuspensionSites) != len(wantSuspension) {
		t.Fatalf("suspension sites: %v", snapshot.SuspensionSites)
	}
	for _, site := range snapshot.SuspensionSites {
		expected, ok := wantSuspension[site.Range.StartLine]
		if !ok || site.Kind != expected.kind {
			t.Errorf("unexpected suspension: %+v", site)
			continue
		}
		ownerFact, hasOwner := rows["site_owner"][site.EntityID]
		ownerID := expected.owner
		if expected.module {
			ownerID = "module:src/main.ts"
		}
		marker, hasMarker := rows["site_attribution_unsupported"][site.EntityID]
		if hasOwner != (ownerID != "") || hasOwner && ownerFact.Values[1].String != ownerID || hasMarker != (expected.reason != "") || hasMarker && marker.Values[1].String != expected.reason {
			t.Errorf("suspension owner or attribution: %s %v %v", site.EntityID, ownerFact, marker)
		}
	}
	var missingCall string
	for _, call := range snapshot.CallsUnresolved {
		if call.Range.StartLine == 12 {
			missingCall = call.SemanticKey
		}
	}
	if marker, ok := rows["site_attribution_unsupported"][missingCall]; missingCall == "" || !ok || marker.Values[1].String != "instance_construction" {
		t.Errorf("other site's unsupported marker: %s %v", missingCall, marker)
	}
}
