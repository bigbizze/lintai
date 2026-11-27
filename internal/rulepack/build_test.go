package rulepack

import (
	"bytes"
	"context"
	"github.com/dop251/goja"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func write(t *testing.T, root, name, contents string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(root, name), []byte(contents), 0644); err != nil {
		t.Fatal(err)
	}
}
func read(t *testing.T, root string) []byte {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(root, Path))
	if err != nil {
		t.Fatal(err)
	}
	return b
}
func TestBuildPublishesCompletePortablePack(t *testing.T) {
	var first []byte
	for i := 0; i < 2; i++ {
		root := t.TempDir()
		write(t, root, "a.ts", "import config from './config.json'; export default config.message")
		write(t, root, "b.ts", "throw new Error('only during analysis'); export default {}")
		write(t, root, "config.json", `{"message":"a\r\nb"}`)
		entries := []string{"b.ts", "a.ts"}
		if err := Build(t.Context(), root, entries); err != nil {
			t.Fatal(err)
		}
		data := read(t, root)
		if i == 0 {
			first = data
		} else if !bytes.Equal(first, data) {
			t.Fatal("pack changed with checkout root")
		}
		pack, err := Decode(data)
		if err != nil || len(pack.Entries) != 2 || pack.Entries[0].Entry != "a.ts" {
			t.Fatalf("decode: %v %+v", err, pack)
		}
		vm := goja.New()
		v, err := vm.RunString(pack.Entries[0].Code + "; LintAIPureModule.default")
		if err != nil || v.String() != "a\r\nb" {
			t.Fatalf("JSON import %v %v", v, err)
		}
		for _, source := range []string{"import 'missing-dependency'", "export default name=>require(name)", "export default name=>import(name)", "export default () => { try {return require('missing')} catch { return {} } }", "import fs from 'node:fs'; export default fs"} {
			write(t, root, "a.ts", source)
			if err := Build(t.Context(), root, entries); err == nil || !strings.Contains(err.Error(), "a.ts") {
				t.Fatalf("expected named build failure: %s: %v", source, err)
			}
			if !bytes.Equal(data, read(t, root)) {
				t.Fatal("failed rebuild changed pack")
			}
		}
		ctx, cancel := context.WithCancel(t.Context())
		cancel()
		if err := Build(ctx, root, []string{}); err == nil || !bytes.Equal(data, read(t, root)) {
			t.Fatal("canceled build changed pack")
		}
		if err := Build(t.Context(), root, []string{}); err != nil {
			t.Fatal(err)
		}
		empty, err := Decode(read(t, root))
		if err != nil || len(empty.Entries) != 0 {
			t.Fatalf("empty pack: %v %v", empty, err)
		}
	}
}

func TestRejectsMalformedEnvelopes(t *testing.T) {
	for _, fields := range []string{`"entries":null`, `"entries":[],"entries":[]`, `"entries":[{"entry":"x","entry":"y","code":"1"}]`, `"entries":[{"entry":"x","code":""}]`, `"entries":[{"entry":"../x","code":"1"}]`, `"entries":[],"extra":1`, `"Entries":[]`, `"entries":[{"entry":"x","code":"1"},{"entry":"X","code":"2"}]`} {
		data := []byte(`{"format":"` + Format + `","abi":"` + ABI + `",` + fields + `}`)
		if _, err := Decode(data); err == nil {
			t.Errorf("accepted %s", data)
		}
	}
}

func TestBuildIgnoresApplicationResolutionSettings(t *testing.T) {
	root := t.TempDir()
	write(t, root, "tsconfig.json", `{"compilerOptions":{"baseUrl":".","paths":{"policy":["local.ts"]}}}`)
	write(t, root, "local.ts", "export default 42")
	write(t, root, "entry.ts", "export { default } from 'policy'")
	err := Build(t.Context(), root, []string{"entry.ts"})
	if err == nil || !strings.Contains(err.Error(), "policy") {
		t.Fatalf("ambient tsconfig alias was accepted: %v", err)
	}
}

func TestBuildResolvesLinkedPackageDependencies(t *testing.T) {
	root := t.TempDir()
	store := filepath.Join(t.TempDir(), "linked-package-store")
	for _, dir := range []string{filepath.Join(root, "node_modules"), filepath.Join(store, "node_modules", "policy"), filepath.Join(store, "node_modules", "shared")} {
		if err := os.MkdirAll(dir, 0755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Symlink(filepath.Join(store, "node_modules", "policy"), filepath.Join(root, "node_modules", "policy")); err != nil {
		t.Fatal(err)
	}
	write(t, root, "entry.ts", "import policy from 'policy'; export default policy")
	write(t, store, "node_modules/policy/index.js", "module.exports = require('shared')")
	write(t, store, "node_modules/shared/index.js", "module.exports = function allowed(){}")
	if err := Build(t.Context(), root, []string{"entry.ts"}); err != nil {
		t.Fatal(err)
	}
	pack, err := Decode(read(t, root))
	if err != nil {
		t.Fatal(err)
	}
	code := pack.Entries[0].Code
	if strings.Contains(code, filepath.Base(store)) {
		t.Fatal("executable contains package store path")
	}
	value, err := goja.New().RunString(code + "; LintAIPureModule.default.name")
	if err != nil || value.String() != "allowed" {
		t.Fatalf("expected exported function: %s (%v)", value, err)
	}
}
