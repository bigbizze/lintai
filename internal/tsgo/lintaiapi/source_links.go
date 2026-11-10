package lintaiapi

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strings"

	"github.com/bigbizze/lintai/internal/tsgo/internal/tspath"
)

type configuredSourceEntry struct {
	ID           string
	CheckedEntry string
	SourceEntry  string
	SourceConfig string
	CheckedPath  string
	SourcePath   string
	ConfigPath   string
}

// Entries may be spelled through different aliases. Equivalence is learned
// from each checked program, not from filesystem realpaths outside it.
type sourceEntryResolutions struct {
	byChecked map[string]configuredSourceEntry
	byEntry   map[string]map[string]bool
}

func newSourceEntryResolutions() *sourceEntryResolutions {
	return &sourceEntryResolutions{byChecked: make(map[string]configuredSourceEntry), byEntry: make(map[string]map[string]bool)}
}

func (r *sourceEntryResolutions) record(identity string, entry configuredSourceEntry) error {
	previous, found := r.byChecked[identity]
	if found && (previous.SourceEntry != entry.SourceEntry || previous.SourceConfig != entry.SourceConfig) {
		return fmt.Errorf("invalid --source-links: contradictory entries for resolved checkedEntry %q", identity)
	}
	if !found || entry.ID < previous.ID {
		r.byChecked[identity] = entry
	}
	if r.byEntry[entry.ID] == nil {
		r.byEntry[entry.ID] = make(map[string]bool)
	}
	r.byEntry[entry.ID][identity] = true
	return nil
}

func (r *sourceEntryResolutions) canonicalize(snapshot *Snapshot, entries []configuredSourceEntry) {
	byID := make(map[string]configuredSourceEntry, len(entries))
	for _, entry := range entries {
		byID[entry.ID] = entry
	}
	var facts []SourceEntryFact
	for _, item := range snapshot.SourceEntries {
		representatives := make(map[string]bool)
		for identity := range r.byEntry[item.ID] {
			representatives[r.byChecked[identity].ID] = true
		}
		if len(representatives) == 0 {
			representatives[item.ID] = true
		}
		for id := range representatives {
			fact := item
			fact.ID = id
			if entry, ok := byID[id]; ok {
				fact.CheckedEntry, fact.SourceEntry, fact.Tsconfig = entry.CheckedEntry, entry.SourceEntry, entry.SourceConfig
			}
			facts = append(facts, fact)
		}
	}
	snapshot.SourceEntries = facts
	for index := range snapshot.SourceBindings {
		item := &snapshot.SourceBindings[index]
		if entry, ok := r.byChecked[item.CheckedFile]; ok {
			item.Entry = entry.ID
		}
		item.ID = sourceBindingID(item.Entry, item.CheckedFile, item.Name, item.Space)
	}
}

type sourceLinksDocument struct {
	Version int                        `json:"version"`
	Entries []sourceLinksDocumentEntry `json:"entries"`
}

type sourceLinksDocumentEntry struct {
	CheckedEntry   string          `json:"checkedEntry"`
	SourceEntry    string          `json:"sourceEntry"`
	SourceTsconfig json.RawMessage `json:"sourceTsconfig,omitempty"`
}

func readSourceLinks(workspaceRoot, option string, caseSensitivity tspath.CaseSensitivity) ([]configuredSourceEntry, string, error) {
	if option == "" {
		return nil, "", nil
	}
	file := option
	if !filepath.IsAbs(file) {
		file = filepath.Join(workspaceRoot, filepath.FromSlash(file))
	}
	absolute, err := filepath.Abs(file)
	if err != nil {
		return nil, "", fmt.Errorf("invalid --source-links: resolve path: %w", err)
	}
	absolute = filepath.Clean(absolute)
	contents, err := os.ReadFile(absolute)
	if err != nil {
		return nil, "", fmt.Errorf("invalid --source-links: read %s: %w", absolute, err)
	}
	decoder := json.NewDecoder(bytes.NewReader(contents))
	decoder.DisallowUnknownFields()
	var document sourceLinksDocument
	if err := decoder.Decode(&document); err != nil {
		return nil, "", fmt.Errorf("invalid --source-links: decode JSON: %w", err)
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		if err == nil {
			return nil, "", fmt.Errorf("invalid --source-links: expected one JSON object")
		}
		return nil, "", fmt.Errorf("invalid --source-links: trailing JSON: %w", err)
	}
	if document.Version != 1 {
		return nil, "", fmt.Errorf("invalid --source-links: unsupported version %d", document.Version)
	}
	if document.Entries == nil {
		return nil, "", fmt.Errorf("invalid --source-links: entries must be an array")
	}
	rootKey := caseSensitivity.PathKey(tspath.RootedPathFromAbsolute(workspaceRoot)).AsString()
	byChecked := make(map[string]configuredSourceEntry, len(document.Entries))
	for index, raw := range document.Entries {
		checked, checkedPath, checkedKey, err := canonicalEntryPath(workspaceRoot, rootKey, raw.CheckedEntry, caseSensitivity)
		if err != nil {
			return nil, "", fmt.Errorf("invalid --source-links: entries[%d].checkedEntry: %w", index, err)
		}
		source, sourcePath, _, err := canonicalEntryPath(workspaceRoot, rootKey, raw.SourceEntry, caseSensitivity)
		if err != nil {
			return nil, "", fmt.Errorf("invalid --source-links: entries[%d].sourceEntry: %w", index, err)
		}
		config, configPath := "", ""
		if raw.SourceTsconfig != nil {
			var selector string
			if bytes.Equal(bytes.TrimSpace(raw.SourceTsconfig), []byte("null")) {
				return nil, "", fmt.Errorf("invalid --source-links: entries[%d].sourceTsconfig must be a string", index)
			}
			if err := json.Unmarshal(raw.SourceTsconfig, &selector); err != nil {
				return nil, "", fmt.Errorf("invalid --source-links: entries[%d].sourceTsconfig must be a string: %w", index, err)
			}
			config, configPath, _, err = canonicalEntryPath(workspaceRoot, rootKey, selector, caseSensitivity)
			if err != nil {
				return nil, "", fmt.Errorf("invalid --source-links: entries[%d].sourceTsconfig: %w", index, err)
			}
			if !strings.HasSuffix(strings.ToLower(config), ".json") {
				return nil, "", fmt.Errorf("invalid --source-links: entries[%d].sourceTsconfig must name a JSON config", index)
			}
			if _, err := os.Stat(configPath); err != nil {
				return nil, "", fmt.Errorf("invalid --source-links: entries[%d].sourceTsconfig does not exist: %w", index, err)
			}
		}
		entry := configuredSourceEntry{
			CheckedEntry: checked, SourceEntry: source, SourceConfig: config,
			CheckedPath: checkedPath, SourcePath: sourcePath, ConfigPath: configPath,
		}
		if previous, exists := byChecked[checkedKey]; exists {
			if previous.SourceEntry != entry.SourceEntry || previous.SourceConfig != entry.SourceConfig {
				return nil, "", fmt.Errorf("invalid --source-links: contradictory entries for checkedEntry %q", checked)
			}
			continue
		}
		entry.ID = sourceEntryID(entry.CheckedEntry, entry.SourceEntry, entry.SourceConfig)
		byChecked[checkedKey] = entry
	}
	entries := make([]configuredSourceEntry, 0, len(byChecked))
	for _, entry := range byChecked {
		entries = append(entries, entry)
	}
	slicesSortSourceEntries(entries)
	return entries, absolute, nil
}

func canonicalEntryPath(root, rootKey, value string, caseSensitivity tspath.CaseSensitivity) (relative, absolute, key string, err error) {
	value = strings.ReplaceAll(value, "\\", "/")
	if value == "" || tspath.PathIsAbsolute(value) || strings.HasPrefix(value, "//") {
		return "", "", "", fmt.Errorf("path must be workspace-relative")
	}
	clean := path.Clean(value)
	if clean == "." || clean == ".." || strings.HasPrefix(clean, "../") {
		return "", "", "", fmt.Errorf("path escapes the workspace root or is empty")
	}
	abs, err := filepath.Abs(filepath.Join(root, filepath.FromSlash(clean)))
	if err != nil {
		return "", "", "", err
	}
	abs = filepath.Clean(abs)
	rootFS, err := filepath.Rel(root, abs)
	if err != nil || rootFS == ".." || strings.HasPrefix(rootFS, ".."+string(filepath.Separator)) {
		return "", "", "", fmt.Errorf("path escapes the workspace root")
	}
	absolute = normalizeSlash(abs)
	relative = normalizeSlash(rootFS)
	rooted, ok := tspath.TryRootedPathFromAbsolute(absolute)
	if !ok {
		return "", "", "", fmt.Errorf("path is not a normalized rooted file path")
	}
	key = caseSensitivity.PathKey(rooted).AsString()
	rootPrefix := strings.TrimSuffix(rootKey, "/") + "/"
	if !strings.HasPrefix(key, rootPrefix) {
		return "", "", "", fmt.Errorf("path escapes the workspace root")
	}
	relative = strings.TrimPrefix(key, rootPrefix)
	return relative, absolute, key, nil
}

func slicesSortSourceEntries(entries []configuredSourceEntry) {
	slices.SortFunc(entries, func(left, right configuredSourceEntry) int {
		return strings.Compare(left.ID, right.ID)
	})
}

func sourceEntryID(checkedEntry, sourceEntry, sourceConfig string) string {
	parts := []string{jsonQuote(checkedEntry), jsonQuote(sourceEntry)}
	if sourceConfig != "" {
		parts = append(parts, jsonQuote(sourceConfig))
	}
	key := jsonList(parts...)
	return "source-entry:" + hashKey(key)
}

func sourceBindingID(entryID, checkedFile, name, space string) string {
	key := jsonList(jsonQuote("source-binding-v1"), jsonQuote(entryID), jsonQuote(checkedFile), jsonQuote(name), jsonQuote(space))
	return "source-binding:" + hashKey(key)
}
