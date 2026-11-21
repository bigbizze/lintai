package bundle

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestNodeExecutable(t *testing.T) {
	cases := []struct {
		name, version string
		ok            bool
	}{
		{"minimum stable", "v26.13.1", true},
		{"later stable", "v26.14.0", true},
		{"below minimum", "v26.13.0", false},
		{"next major", "v27.0.0", false},
		{"prerelease", "v26.14.0-rc.1", false},
		{"malformed", "not node", false},
		{"missing", "", false},
	}
	for _, item := range cases {
		t.Run(item.name, func(t *testing.T) {
			dir := t.TempDir()
			t.Setenv("PATH", dir)
			path := filepath.Join(dir, "node")
			if item.version != "" {
				if err := os.WriteFile(path, []byte("#!/bin/sh\nprintf '%s\\n' '"+item.version+"'\n"), 0o755); err != nil {
					t.Fatal(err)
				}
			}
			got, err := NodeExecutable(context.Background())
			if item.ok {
				if err != nil || got != path {
					t.Fatalf("got path %q, error %v; want %q", got, err, path)
				}
			} else if err == nil || !strings.Contains(err.Error(), "Node >=26.13.1") {
				t.Fatalf("got path %q, error %v; want actionable runtime error", got, err)
			}
		})
	}
}

func TestSetupSandboxModes(t *testing.T) {
	for _, item := range []struct {
		input string
		want  SetupSandbox
	}{
		{"", SetupSandboxOn}, {"on", SetupSandboxOn}, {"off", SetupSandboxOff},
	} {
		got, err := ParseSetupSandbox(item.input)
		if err != nil || got != item.want {
			t.Fatalf("ParseSetupSandbox(%q) = %q, %v; want %q", item.input, got, err, item.want)
		}
	}
	if _, err := ParseSetupSandbox("auto"); err == nil {
		t.Fatal("auto must be rejected")
	}
}

func TestDirectBundleBoundariesRequireNode(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	ctx := context.Background()
	if _, _, err := BuildAll(ctx, "", "", "", "", []string{"rule.ts"}); err == nil || !strings.Contains(err.Error(), "could not find node") {
		t.Fatalf("BuildAll: %v", err)
	}
	if _, _, err := PrepareAll(ctx, "", "", "", map[string]Artifacts{"rule.ts": {}}, nil, PrepareOptions{}); err == nil || !strings.Contains(err.Error(), "could not find node") {
		t.Fatalf("PrepareAll with sandbox off: %v", err)
	}
}
