package engine

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Exercise isolation and deterministic ordering across initialization, configuration,
// duplicate IDs, disabled callbacks and successful producer-backed reports.
func TestMixedRuleOutcomes(t *testing.T) {
	repoRoot, _ := filepath.Abs("..")
	workspace := t.TempDir()
	program := "Decl violation(Entity, Code). violation(Entity, Code) :- catalog.usable(Entity, Code)."
	source := func(id string) string { return parallelRuleSource(id, program, "(row) => row.Code") }
	badConfig := "export const config = {parse(){ throw 'invalid options' }};\n"
	for name, code := range map[string]string{
		"healthy": source("fixture.healthy"), "disabled": badConfig + source("fixture.disabled"),
		"config": badConfig + source("fixture.config"), "duplicate": source("fixture.duplicate"),
		"partial": `export default {id:"fixture.duplicate",versionValue:1};`,
		"broken":  `throw "initialization failed"; export default {};`,
	} {
		writeTestFile(t, workspace, "lintai-rules/"+name+".ts", code)
	}
	options := parallelFixtureOptions(t, repoRoot, workspace)
	options.Rule = map[string]RulePolicy{"fixture.disabled": {Severity: "off"}}
	session := openTestSession(t, options, newParallelFixtureProducer())
	result, err := session.Check(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if result.SnapshotVersion == "" {
		t.Fatal("analysis has no snapshot identity")
	}
	for _, item := range result.Diagnostics {
		if item.Provenance.SnapshotVersion != result.SnapshotVersion || len(item.Provenance.ProducerNamespaces) != 1 || item.Provenance.ProducerNamespaces[0] != "catalog" {
			t.Errorf("diagnostic did not receive final analysis provenance: %+v", item)
		}
	}
	data, err := json.Marshal(result.Diagnostics)
	if err != nil {
		t.Fatal(err)
	}
	var items []map[string]any
	if err := json.Unmarshal(data, &items); err != nil {
		t.Fatal(err)
	}
	for _, item := range items {
		// Generated bundle coordinates can change when the SDK is rebuilt.
		item["message"], _, _ = strings.Cut(item["message"].(string), " at ")
		delete(item["provenance"].(map[string]any), "snapshot_version")
	}
	got, err := json.MarshalIndent(items, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	got = append(got, '\n')
	want, err := os.ReadFile("testdata/mixed-rule-diagnostics.json")
	if err != nil {
		t.Fatalf("read diagnostic golden: %v\n%s", err, got)
	}
	if !bytes.Equal(got, want) {
		t.Fatalf("ordered diagnostics changed:\n--- got\n%s\n--- want\n%s", got, want)
	}
}

func TestSuppressedFindingsRetainFinalProvenance(t *testing.T) {
	repoRoot, _ := filepath.Abs("..")
	workspace := t.TempDir()
	program := "Decl violation(Entity, Code). violation(Entity, Code) :- catalog.usable(Entity, Code)."
	writeTestFile(t, workspace, "lintai-rules/rule.ts", parallelRuleSource("fixture.suppressed", program, "(row) => row.Code"))
	session := openTestSession(t, parallelFixtureOptions(t, repoRoot, workspace), newParallelFixtureProducer())
	result, err := session.UpdateSuppressions(context.Background(), SuppressionUpdate{All: true})
	if err != nil {
		t.Fatal(err)
	}
	if !result.SuppressionsWritten || len(result.Suppressed) != 3 {
		t.Fatalf("expected three recorded and suppressed findings: %+v", result)
	}
	for _, item := range result.Suppressed {
		if item.Provenance.SnapshotVersion != result.SnapshotVersion || item.Provenance.RuleVersion != 1 || len(item.Provenance.ProducerNamespaces) != 1 || item.Provenance.ProducerNamespaces[0] != "catalog" {
			t.Errorf("suppressed copy lost final analysis provenance: %+v", item)
		}
	}
}
