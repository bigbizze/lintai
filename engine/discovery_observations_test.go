package engine

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/bigbizze/lintai/observe"
)

func TestPolicyReadersCaptureParsedBytesAndMissingDefault(t *testing.T) {
	root := t.TempDir()
	state, err := loadSuppressions(Config{WorkspaceRoot: root}, SuppressionUpdate{})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(state.path, []byte(`{"format":"lintai.suppressions/4","entries":[]}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if changed, err := state.observations.Changed(context.Background(), observe.Full); err != nil || changed == nil {
		t.Fatalf("created suppressions: %v %v", changed, err)
	}
	state, err = loadSuppressions(Config{WorkspaceRoot: root}, SuppressionUpdate{})
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
	if err := os.WriteFile(path, []byte(`{"format":"lintai.rules-lock/5","abi":"lintai-pure-v1","pack_digest":"sha256:1111111111111111111111111111111111111111111111111111111111111111","policy_digest":"sha256:2222222222222222222222222222222222222222222222222222222222222222","approved_suppressions":[]}`), 0o600); err != nil {
		t.Fatal(err)
	}
	lock, err := ReadRulesLock(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(`{"format":"lintai.rules-lock/5","abi":"lintai-pure-v1","pack_digest":"sha256:1111111111111111111111111111111111111111111111111111111111111111","policy_digest":"sha256:2222222222222222222222222222222222222222222222222222222222222222","approved_suppressions":[]} `), 0o600); err != nil {
		t.Fatal(err)
	}
	if changed, err := lock.observations.Changed(context.Background(), observe.Full); err != nil || changed == nil {
		t.Fatalf("changed lock: %v %v", changed, err)
	}
	if _, err := ReadRulesLock(filepath.Join(root, "missing.lock")); err == nil {
		t.Fatal("explicit missing lock accepted")
	}
}
