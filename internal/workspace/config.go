package workspace

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"

	"github.com/BurntSushi/toml"
)

// WorkspaceConfig is the supported lintai.toml input. Producer-specific
// settings remain keyed by namespace so the command composition root can
// select the appropriate implementation.
type WorkspaceConfig struct {
	Exclude   []string
	Include   []string
	Sources   []string
	Producers map[string]ProducerConfig
	Path      string
	Digest    string
}

type ProducerConfig struct {
	Implementation string `toml:"implementation"`
	Files          string `toml:"files"`
	SourceLinks    string `toml:"source_links"`
}

// LoadWorkspaceConfig reads path. A missing default file is represented by an
// empty config; an explicitly selected file must exist.
func LoadWorkspaceConfig(path string, optional bool) (WorkspaceConfig, error) {
	result := WorkspaceConfig{Producers: make(map[string]ProducerConfig)}
	contents, err := os.ReadFile(path)
	if err != nil {
		if optional && os.IsNotExist(err) {
			return result, nil
		}
		return WorkspaceConfig{}, fmt.Errorf("read config %s: %w", path, err)
	}
	result.Path = filepath.Clean(path)
	digest := sha256.Sum256(contents)
	result.Digest = hex.EncodeToString(digest[:])
	if err := decodeWorkspaceTOML(string(contents), &result); err != nil {
		return WorkspaceConfig{}, fmt.Errorf("config %s: %w", path, err)
	}
	return result, nil
}

// ResolveConfigPath applies the CLI rule: an explicit relative path is based
// on the invocation directory, while the default is at the resolved workspace
// root and is never searched for in a parent.
func ResolveConfigPath(workspaceRoot, invocationDir, explicit string) (string, bool, error) {
	if explicit == "" {
		return filepath.Join(workspaceRoot, "lintai.toml"), true, nil
	}
	path := explicit
	if !filepath.IsAbs(path) {
		path = filepath.Join(invocationDir, path)
	}
	absolute, err := filepath.Abs(path)
	if err != nil {
		return "", false, fmt.Errorf("resolve --config: %w", err)
	}
	return filepath.Clean(absolute), false, nil
}

func decodeWorkspaceTOML(input string, result *WorkspaceConfig) error {
	var document struct {
		Workspace struct {
			Exclude []string `toml:"exclude"`
			Include []string `toml:"include"`
			Sources []string `toml:"sources"`
		} `toml:"workspace"`
		Producers map[string]ProducerConfig `toml:"producers"`
	}
	metadata, err := toml.Decode(input, &document)
	if err != nil {
		return err
	}
	// Check exact keys as well as decoded types: the TOML decoder otherwise
	// accepts case-insensitive struct field matches and ignores unknown keys.
	for _, key := range metadata.Keys() {
		switch {
		case len(key) == 1 && (key[0] == "workspace" || key[0] == "producers"):
		case len(key) == 2 && key[0] == "workspace" && metadata.Type(key...) != "Hash":
			switch key[1] {
			case "exclude", "include", "sources":
			default:
				return fmt.Errorf("unknown workspace key %q", key[1])
			}
		case len(key) == 2 && key[0] == "producers" && metadata.Type(key...) == "Hash":
			if !validNamespace(key[1]) {
				return fmt.Errorf("invalid producer namespace %q", key[1])
			}
		case len(key) == 2 && key[0] == "producers":
			return fmt.Errorf("producer settings must be inside [producers.<namespace>]")
		case len(key) == 3 && key[0] == "producers" && metadata.Type(key...) != "Hash":
			switch key[2] {
			case "implementation", "files", "source_links":
			default:
				return fmt.Errorf("unknown producer key %q", key[2])
			}
		default:
			return fmt.Errorf("unsupported table %q", key.String())
		}
	}
	if metadata.IsDefined("producers") && len(document.Producers) == 0 {
		return fmt.Errorf("[producers] must contain at least one producer")
	}
	for namespace, producer := range document.Producers {
		if !validNamespace(namespace) {
			return fmt.Errorf("invalid producer namespace %q", namespace)
		}
		if namespace != "ts" && producer.Implementation == "" {
			return fmt.Errorf("producer %q must state implementation", namespace)
		}
	}
	result.Exclude = document.Workspace.Exclude
	result.Include = document.Workspace.Include
	result.Sources = document.Workspace.Sources
	if document.Producers != nil {
		result.Producers = document.Producers
	}
	return nil
}

func validNamespace(value string) bool {
	if value == "" || value[0] < 'a' || value[0] > 'z' {
		return false
	}
	for _, r := range value[1:] {
		if !(r >= 'a' && r <= 'z' || r >= '0' && r <= '9' || r == '_') {
			return false
		}
	}
	return true
}
