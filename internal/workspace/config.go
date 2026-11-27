package workspace

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"syscall"

	"github.com/BurntSushi/toml"
	"github.com/bigbizze/lintai/internal/canonical"
	"github.com/bigbizze/lintai/observe"
)

// WorkspaceConfig is the supported lintai.toml input. Producer-specific
// settings remain keyed by namespace so the command composition root can
// select the appropriate implementation.
type WorkspaceConfig struct {
	Rule         map[string]RulePolicy
	Entries      []string
	Exclude      []string
	Include      []string
	Sources      []string
	Producers    map[string]ProducerConfig
	Observations observe.Set
}

type RulePolicy struct {
	Severity string         `toml:"severity"`
	Options  map[string]any `toml:"options"`
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
	absolute, err := filepath.Abs(path)
	if err != nil {
		return WorkspaceConfig{}, fmt.Errorf("resolve config %s: %w", path, err)
	}
	absolute = filepath.Clean(absolute)
	var records []observe.Record
	resolved, resolveErr := filepath.EvalSymlinks(absolute)
	if resolveErr == nil {
		records = append(records, observe.Record{Kind: observe.Resolved, Path: absolute, Value: observe.Value(filepath.Clean(resolved))})
	} else if !configMissing(resolveErr) {
		return WorkspaceConfig{}, fmt.Errorf("resolve config %s: %w", path, resolveErr)
	}
	// The closest existing parent covers an alias even when the default file
	// (or one of its parent directories) is absent.
	for parent := filepath.Dir(absolute); ; parent = filepath.Dir(parent) {
		info, statErr := os.Stat(parent)
		if statErr == nil && info.IsDir() {
			resolvedParent, err := filepath.EvalSymlinks(parent)
			if err != nil {
				return WorkspaceConfig{}, err
			}
			records = append(records, observe.Record{Kind: observe.State, Path: parent, Op: observe.Directory, Value: observe.True}, observe.Record{Kind: observe.Resolved, Path: parent, Value: observe.Value(filepath.Clean(resolvedParent))})
			break
		}
		if statErr != nil && !configMissing(statErr) {
			return WorkspaceConfig{}, fmt.Errorf("stat config parent %s: %w", parent, statErr)
		}
		records = append(records, observe.Record{Kind: observe.State, Path: parent, Op: observe.Directory, Value: observe.False})
		if parent == filepath.Dir(parent) {
			break
		}
	}
	contents, err := os.ReadFile(absolute)
	if err != nil {
		if optional && configMissing(err) {
			records = append(records, observe.Record{Kind: observe.State, Path: absolute, Op: observe.ReadFile, Value: observe.Missing})
			result.Observations, err = observe.New(records)
			return result, err
		}
		return WorkspaceConfig{}, fmt.Errorf("read config %s: %w", path, err)
	}
	digest := sha256.Sum256(contents)
	records = append(records, observe.Record{Kind: observe.Content, Path: absolute, Value: observe.Value(hex.EncodeToString(digest[:]))})
	result.Observations, err = observe.New(records)
	if err != nil {
		return WorkspaceConfig{}, err
	}
	if err := decodeWorkspaceTOML(string(contents), &result); err != nil {
		return WorkspaceConfig{}, fmt.Errorf("config %s: %w", path, err)
	}
	return result, nil
}

func configMissing(err error) bool {
	return errors.Is(err, os.ErrNotExist) || errors.Is(err, syscall.ENOTDIR)
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
		Rule  map[string]RulePolicy `toml:"rule"`
		Rules struct {
			Entries []string `toml:"entries"`
		} `toml:"rules"`
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
		case len(key) == 1 && key[0] == "rule" && metadata.Type(key...) == "Hash":
		case len(key) == 2 && key[0] == "rule" && metadata.Type(key...) == "Hash":
		case len(key) == 3 && key[0] == "rule" && key[2] == "severity":
		case len(key) >= 3 && key[0] == "rule" && key[2] == "options":
		case len(key) == 1 && key[0] == "rules" && metadata.Type(key...) == "Hash":
		case len(key) == 2 && key[0] == "rules" && key[1] == "entries":
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
	for id, policy := range document.Rule {
		if id == "" {
			return fmt.Errorf("rule ID must not be empty")
		}
		if !metadata.IsDefined("rule", id, "severity") {
			policy.Severity = "error"
		}
		switch policy.Severity {
		case "off", "warning", "error":
		default:
			return fmt.Errorf("rule %q: expected severity error, warning or off", id)
		}
		if policy.Options == nil {
			policy.Options = map[string]any{}
		}
		if _, err := canonical.MarshalStructured(policy.Options); err != nil {
			return fmt.Errorf("rule %q options: %w", id, err)
		}
		document.Rule[id] = policy
	}
	result.Rule = document.Rule
	result.Entries = document.Rules.Entries
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
