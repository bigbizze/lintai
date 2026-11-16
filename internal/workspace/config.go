package workspace

import (
	"bufio"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
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
	Implementation string
	Files          string
	SourceLinks    string
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
	section := ""
	seenTables := make(map[string]bool)
	seenKeys := make(map[string]bool)
	producersTable := false
	scanner := bufio.NewScanner(strings.NewReader(input))
	lineNumber := 0
	for scanner.Scan() {
		lineNumber++
		line := stripTOMLComment(scanner.Text())
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		if strings.HasPrefix(line, "[") {
			if !strings.HasSuffix(line, "]") || strings.HasPrefix(line, "[[") {
				return fmt.Errorf("line %d: unsupported or malformed table header", lineNumber)
			}
			name := strings.TrimSpace(line[1 : len(line)-1])
			parts := strings.Split(name, ".")
			switch {
			case len(parts) == 1 && parts[0] == "workspace":
				section = "workspace"
			case len(parts) == 1 && parts[0] == "producers":
				section = "producers"
				producersTable = true
			case len(parts) == 2 && parts[0] == "producers":
				namespace := parts[1]
				if !validNamespace(namespace) {
					return fmt.Errorf("line %d: invalid producer namespace %q", lineNumber, namespace)
				}
				section = "producers." + namespace
				producersTable = true
				if _, ok := result.Producers[namespace]; !ok {
					result.Producers[namespace] = ProducerConfig{}
				}
			default:
				return fmt.Errorf("line %d: unsupported table %q", lineNumber, name)
			}
			if seenTables[section] {
				return fmt.Errorf("line %d: duplicate table %q", lineNumber, name)
			}
			seenTables[section] = true
			continue
		}
		key, value, ok := strings.Cut(line, "=")
		if !ok || section == "" {
			return fmt.Errorf("line %d: expected a key/value inside a supported table", lineNumber)
		}
		key = strings.TrimSpace(key)
		if !validKey(key) {
			return fmt.Errorf("line %d: invalid key %q", lineNumber, key)
		}
		fullKey := section + "." + key
		if seenKeys[fullKey] {
			return fmt.Errorf("line %d: duplicate key %q", lineNumber, fullKey)
		}
		seenKeys[fullKey] = true
		value = strings.TrimSpace(value)
		if strings.HasPrefix(value, "[") && !stringArrayComplete(value) {
			for scanner.Scan() {
				lineNumber++
				value += "\n" + strings.TrimSpace(stripTOMLComment(scanner.Text()))
				if stringArrayComplete(value) {
					break
				}
			}
			if !stringArrayComplete(value) {
				return fmt.Errorf("line %d: unterminated string array", lineNumber)
			}
		}
		switch section {
		case "workspace":
			var destination *[]string
			switch key {
			case "exclude":
				destination = &result.Exclude
			case "include":
				destination = &result.Include
			case "sources":
				destination = &result.Sources
			default:
				return fmt.Errorf("line %d: unknown workspace key %q", lineNumber, key)
			}
			items, err := parseStringArray(value)
			if err != nil {
				return fmt.Errorf("line %d: workspace.%s: %w", lineNumber, key, err)
			}
			*destination = items
		case "producers":
			return fmt.Errorf("line %d: producer settings must be inside [producers.<namespace>]", lineNumber)
		default:
			if !strings.HasPrefix(section, "producers.") {
				return fmt.Errorf("line %d: unsupported table %q", lineNumber, section)
			}
			namespace := strings.TrimPrefix(section, "producers.")
			producer := result.Producers[namespace]
			text, err := parseString(value)
			if err != nil {
				return fmt.Errorf("line %d: producers.%s.%s: %w", lineNumber, namespace, key, err)
			}
			switch key {
			case "implementation":
				producer.Implementation = text
			case "files":
				producer.Files = text
			case "source_links":
				producer.SourceLinks = text
			default:
				return fmt.Errorf("line %d: unknown producer key %q", lineNumber, key)
			}
			result.Producers[namespace] = producer
		}
	}
	if err := scanner.Err(); err != nil {
		return err
	}
	if producersTable && len(result.Producers) == 0 {
		return fmt.Errorf("[producers] must contain at least one producer")
	}
	for namespace, producer := range result.Producers {
		if namespace != "ts" && producer.Implementation == "" {
			return fmt.Errorf("producer %q must state implementation", namespace)
		}
	}
	return nil
}

func stripTOMLComment(line string) string {
	quote, escaped := rune(0), false
	for index, r := range line {
		if escaped {
			escaped = false
			continue
		}
		if quote == '"' && r == '\\' {
			escaped = true
			continue
		}
		if quote != 0 {
			if r == quote {
				quote = 0
			}
			continue
		}
		if r == '"' || r == '\'' {
			quote = r
			continue
		}
		if r == '#' {
			return line[:index]
		}
	}
	return line
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

func validKey(value string) bool {
	if value == "" {
		return false
	}
	for _, r := range value {
		if !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '_') {
			return false
		}
	}
	return true
}

func parseString(value string) (string, error) {
	if len(value) < 2 || value[len(value)-1] != value[0] || value[0] != '"' && value[0] != '\'' {
		return "", fmt.Errorf("expected a quoted string")
	}
	if value[0] == '\'' {
		return value[1 : len(value)-1], nil
	}
	decoded, err := strconv.Unquote(value)
	if err != nil {
		return "", fmt.Errorf("invalid string: %w", err)
	}
	return decoded, nil
}

func parseStringArray(value string) ([]string, error) {
	if len(value) < 2 || value[0] != '[' || value[len(value)-1] != ']' {
		return nil, fmt.Errorf("expected an array of strings")
	}
	body := strings.TrimSpace(value[1 : len(value)-1])
	if body == "" {
		return []string{}, nil
	}
	parts := make([]string, 0)
	start, quote, escaped := 0, rune(0), false
	for index, r := range body {
		if escaped {
			escaped = false
			continue
		}
		if quote == '"' && r == '\\' {
			escaped = true
			continue
		}
		if quote != 0 {
			if r == quote {
				quote = 0
			}
			continue
		}
		if r == '"' || r == '\'' {
			quote = r
			continue
		}
		if r == ',' {
			parts = append(parts, strings.TrimSpace(body[start:index]))
			start = index + 1
		}
	}
	parts = append(parts, strings.TrimSpace(body[start:]))
	items := make([]string, 0, len(parts))
	for index, part := range parts {
		if part == "" {
			if index == len(parts)-1 && strings.HasSuffix(body, ",") {
				continue
			}
			return nil, fmt.Errorf("array contains an empty element")
		}
		item, err := parseString(part)
		if err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, nil
}

func stringArrayComplete(value string) bool {
	depth := 0
	quote := rune(0)
	escaped := false
	for _, r := range value {
		if escaped {
			escaped = false
			continue
		}
		if quote == '"' && r == '\\' {
			escaped = true
			continue
		}
		if quote != 0 {
			if r == quote {
				quote = 0
			}
			continue
		}
		if r == '"' || r == '\'' {
			quote = r
			continue
		}
		switch r {
		case '[':
			depth++
		case ']':
			depth--
			if depth < 0 {
				return true
			}
		}
	}
	return depth == 0 && quote == 0 && strings.HasSuffix(strings.TrimSpace(value), "]")
}
