package engine

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/bigbizze/lintai/observe"
)

func TestRuleDiscoveryObservations(t *testing.T) {
	root := t.TempDir()
	patterns := []string{"rules/**/*.ts"}
	paths, set, err := discoverRulePaths(root, patterns, true)
	if err != nil || len(paths) != 0 {
		t.Fatalf("empty discovery: %v %v", paths, err)
	}
	if err := os.Mkdir(filepath.Join(root, "rules"), 0o700); err != nil {
		t.Fatal(err)
	}
	if changed, err := set.Changed(context.Background(), observe.Full); err != nil || changed == nil {
		t.Fatalf("created prefix not detected: %v %v", changed, err)
	}
	file := filepath.Join(root, "rules", "first.ts")
	if err := os.WriteFile(file, []byte("first"), 0o600); err != nil {
		t.Fatal(err)
	}
	paths, set, err = discoverRulePaths(root, patterns, false)
	if err != nil || len(paths) != 1 {
		t.Fatalf("discovery: %v %v", paths, err)
	}
	if err := os.WriteFile(filepath.Join(root, "rules", "second.ts"), []byte("second"), 0o600); err != nil {
		t.Fatal(err)
	}
	if changed, err := set.Changed(context.Background(), observe.Full); err != nil || changed == nil {
		t.Fatalf("new rule not detected: %v %v", changed, err)
	}
	_, set, err = discoverRulePaths(root, []string{"rules/first.ts"}, false)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(file); err != nil {
		t.Fatal(err)
	}
	if changed, err := set.Changed(context.Background(), observe.Full); err != nil || changed == nil {
		t.Fatalf("deleted exact rule not detected: %v %v", changed, err)
	}
}

func TestPolicyReadersCaptureParsedBytesAndMissingDefault(t *testing.T) {
	root := t.TempDir()
	state, err := loadSuppressions(Options{WorkspaceRoot: root})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(state.path, []byte(`{"format":"lintai.suppressions/4","entries":[]}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if changed, err := state.observations.Changed(context.Background(), observe.Full); err != nil || changed == nil {
		t.Fatalf("created suppressions: %v %v", changed, err)
	}
	state, err = loadSuppressions(Options{WorkspaceRoot: root})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(state.path, []byte(`{"format":"lintai.suppressions/4","entries":[]} `), 0o600); err != nil {
		t.Fatal(err)
	}
	if changed, err := state.observations.Changed(context.Background(), observe.Full); err != nil || changed == nil {
		t.Fatalf("changed suppressions: %v %v", changed, err)
	}
	path := filepath.Join(root, "rules.lock")
	if err := os.WriteFile(path, []byte(`{"format":"lintai.rules-lock/4","rules":[],"approved_suppressions":[]}`), 0o600); err != nil {
		t.Fatal(err)
	}
	lock, err := ReadRulesLock(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(`{"format":"lintai.rules-lock/4","rules":[],"approved_suppressions":[]} `), 0o600); err != nil {
		t.Fatal(err)
	}
	if changed, err := lock.observations.Changed(context.Background(), observe.Full); err != nil || changed == nil {
		t.Fatalf("changed lock: %v %v", changed, err)
	}
	if _, err := ReadRulesLock(filepath.Join(root, "missing.lock")); err == nil {
		t.Fatal("explicit missing lock accepted")
	}
}
