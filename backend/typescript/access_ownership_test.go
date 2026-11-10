package typescript

import (
	"context"
	"testing"

	"github.com/bigbizze/lintai/backend"
	"github.com/bigbizze/lintai/facts"
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
	snapshot, err := New().buildAnalysisSnapshot(context.Background(), root, root, backend.SnapshotOptions{Files: backend.FileScopeWalk})
	if err != nil {
		t.Fatal(err)
	}
	items, err := factsFromAnalysis(snapshot)
	if err != nil {
		t.Fatal(err)
	}
	if err := facts.Validate(facts.Snapshot{Version: snapshot.Version, Schema: Schema(), Facts: items}); err != nil {
		t.Fatal(err)
	}
	rows := make(map[string]map[string]facts.Fact)
	for _, item := range items {
		if rows[item.Relation] == nil {
			rows[item.Relation] = make(map[string]facts.Fact)
		}
		if item.Relation == "meta_access" {
			if _, exists := rows[item.Relation][item.Values[0].String]; exists {
				t.Fatalf("duplicate meta access: %v", item)
			}
		}
		rows[item.Relation][item.Values[0].String] = item
	}
	owner := "src/main.ts::function::read"
	constructor := "src/main.ts::constructor::Present.constructor"
	want := []struct {
		path, suffix, owner, reason string
		line, start, end            int64
	}{
		{"env", "", owner, "", 1, 34, 49},
		{"env", "#2", owner, "", 3, 23, 38},
		{"env", "#3", owner, "", 4, 18, 49},
		{"env", "#4", owner, "", 5, 22, 37},
		{"env", "#5", "", "instance_construction", 10, 11, 26},
		{"env", "#6", constructor, "", 15, 11, 26},
		{"env", "#7", "", "unsupported_context", 21, 11, 26},
		{"url", "", constructor, "", 16, 12, 27},
		{"url", "#2", "", "unsupported_context", 22, 12, 27},
		{"url", "#3", "", "", 26, 1, 16},
	}
	if len(rows["meta_access"]) != len(want) {
		t.Fatalf("meta accesses: got %v, want %d", rows["meta_access"], len(want))
	}
	for _, expected := range want {
		path := "import.meta." + expected.path
		id := "src/main.ts::access::" + path + expected.suffix
		access, ok := rows["meta_access"][id]
		if !ok || access.Values[1].String != "src/main.ts" || access.Values[2].String != path {
			t.Errorf("access %s: %v", id, access)
			continue
		}
		location, ok := rows["location"][id]
		if !ok || location.Values[1].String != "src/main.ts" || location.Values[2].Number != expected.line ||
			location.Values[3].Number != expected.start || location.Values[4].Number != expected.line || location.Values[5].Number != expected.end {
			t.Errorf("access %s location: %v", id, location)
		}
		actualOwner, hasOwner := rows["site_in_fn"][id]
		if hasOwner != (expected.owner != "") || hasOwner && actualOwner.Values[1].String != expected.owner {
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
		25: {"for_await", "src/main.ts::function::iterate", "", false},
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
		fn, hasFn := rows["suspension_in_fn"][site.EntityID]
		if hasFn != (expected.owner != "") || hasFn && fn.Values[1].String != expected.owner {
			t.Errorf("suspension %s owner: %v", site.EntityID, fn)
		}
		_, atModule := rows["suspension_at_module"][site.EntityID]
		marker, hasMarker := rows["site_attribution_unsupported"][site.EntityID]
		if atModule != expected.module || hasMarker != (expected.reason != "") || hasMarker && marker.Values[1].String != expected.reason {
			t.Errorf("suspension %s module=%v, unsupported=%v", site.EntityID, atModule, marker)
		}
	}
	if len(rows["fn_awaits"]) != 1 || rows["fn_awaits"][owner].Relation != "fn_awaits" {
		t.Errorf("ordinary await owners: %v", rows["fn_awaits"])
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
