package workspace

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/bigbizze/lintai/observe"
)

func TestWorkspaceConfigStrictSchemaAndResolution(t *testing.T) {
	root := t.TempDir()
	configPath := filepath.Join(root, "lintai.toml")
	contents := `[workspace]
exclude = ["tmp/**"]
include = ["dist/contracts/**"]
sources = ["generated/contracts.d.ts"]

[producers.ts]
implementation = "lintai.typescript"
files = "walk"
source_links = ""
`
	if err := os.WriteFile(configPath, []byte(contents), 0o644); err != nil {
		t.Fatal(err)
	}
	config, err := LoadWorkspaceConfig(configPath, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(config.Exclude) != 1 || config.Exclude[0] != "tmp/**" || len(config.Include) != 1 || config.Include[0] != "dist/contracts/**" ||
		len(config.Sources) != 1 || config.Sources[0] != "generated/contracts.d.ts" || config.Producers["ts"].Files != "walk" || len(config.Observations.Records()) == 0 {
		t.Fatalf("unexpected effective config: %+v", config)
	}
	invocationDir := t.TempDir()
	resolved, optional, err := ResolveConfigPath(root, invocationDir, "custom.toml")
	if err != nil || optional || resolved != filepath.Join(invocationDir, "custom.toml") {
		t.Fatalf("explicit config path was not invocation-relative: %q optional=%v err=%v", resolved, optional, err)
	}
	defaultPath, optional, err := ResolveConfigPath(root, t.TempDir(), "")
	if err != nil || !optional || defaultPath != filepath.Join(root, "lintai.toml") {
		t.Fatalf("default config path was not workspace-local: %q optional=%v err=%v", defaultPath, optional, err)
	}
	if _, err := LoadWorkspaceConfig(filepath.Join(root, "absent.toml"), true); err != nil {
		t.Fatalf("missing default config should select defaults: %v", err)
	}
	if _, err := LoadWorkspaceConfig(filepath.Join(root, "absent.toml"), false); err == nil {
		t.Fatal("explicitly named missing config was treated as defaults")
	}
	if err := os.WriteFile(configPath, []byte("[workspace]\nexclude = [\n  'tmp/**', # literal TOML string\n  \"dist/#literal/**\",\n]\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	multiline, err := LoadWorkspaceConfig(configPath, false)
	if err != nil || len(multiline.Exclude) != 2 || multiline.Exclude[0] != "tmp/**" || multiline.Exclude[1] != "dist/#literal/**" {
		t.Fatalf("valid multiline TOML array or quoted comment marker failed: %+v err=%v", multiline, err)
	}
	for name, invalid := range map[string]string{
		"duplicate key":          "[workspace]\nexclude = []\nexclude = []\n",
		"empty array element":    "[workspace]\nexclude = [\"tmp/**\",,]\n",
		"missing array comma":    "[workspace]\nexclude = ['src/a.ts' 'src/b.ts']\n",
		"wrong value type":       "[workspace]\nexclude = \"tmp/**\"\n",
		"unknown workspace key":  "[workspace]\nextend = []\n",
		"incorrect key case":     "[workspace]\nEXCLUDE = []\n",
		"unknown producer key":   "[producers.ts]\nextra = \"value\"\n",
		"missing implementation": "[producers.catalog]\nfiles = \"walk\"\n",
		"empty producers":        "[producers]\n",
		"unknown table":          "[workspace.extra]\n",
	} {
		t.Run(name, func(t *testing.T) {
			if err := os.WriteFile(configPath, []byte(invalid), 0o644); err != nil {
				t.Fatal(err)
			}
			_, err := LoadWorkspaceConfig(configPath, false)
			if err == nil {
				t.Fatal("invalid TOML configuration was accepted")
			}
			if want := map[string]string{
				"unknown workspace key": "unknown workspace key",
				"incorrect key case":    "unknown workspace key",
				"unknown producer key":  "unknown producer key",
				"unknown table":         "unsupported table",
			}[name]; want != "" && !strings.Contains(err.Error(), want) {
				t.Fatalf("error %q does not identify %s", err, want)
			}
		})
	}
}

func TestConfigObservationsCaptureBytesAndMissingDefault(t *testing.T) {
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(root, "lintai.toml")
	config, err := LoadWorkspaceConfig(path, true)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("[workspace]\nexclude = []\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if changed, err := config.Observations.Changed(context.Background(), observe.Full); err != nil || changed == nil {
		t.Fatalf("missing config creation: %v %v", changed, err)
	}
	config, err = LoadWorkspaceConfig(path, true)
	if err != nil {
		t.Fatal(err)
	}
	if changed, err := config.Observations.Changed(context.Background(), observe.Full); err != nil || changed != nil {
		t.Fatalf("unchanged config: %v %v", changed, err)
	}
	if err := os.WriteFile(path, []byte("[workspace]\nexclude = [\"a\"]\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if changed, err := config.Observations.Changed(context.Background(), observe.Full); err != nil || changed == nil {
		t.Fatalf("changed config: %v %v", changed, err)
	}
	other := filepath.Join(root, "other.toml")
	if err := os.WriteFile(other, []byte("[workspace]\nexclude = [\"a\"]\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	alias := filepath.Join(root, "selected.toml")
	if err := os.Symlink(path, alias); err != nil {
		t.Skip(err)
	}
	config, err = LoadWorkspaceConfig(alias, false)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(alias); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(other, alias); err != nil {
		t.Fatal(err)
	}
	if changed, err := config.Observations.Changed(context.Background(), observe.Full); err != nil || changed == nil {
		t.Fatalf("retargeted config alias: %v %v", changed, err)
	}
}
