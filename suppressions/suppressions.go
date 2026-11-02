// Package suppressions reads, writes and applies a bulk suppressions file.
// The file has the format of ESLint's eslint-suppressions.json, with an
// optional entities map in each rule entry:
//
//	{
//	  "src/db/users.ts": {
//	    "knex-confinement": {
//	      "count": 2,
//	      "entities": {
//	        "src/db/users.ts::import::knex": 1,
//	        "src/db/users.ts::arrow_function::load::calls::knex::raw": 1
//	      }
//	    }
//	  }
//	}
//
// The first key is the file of a diagnostic, relative to the workspace root,
// with forward slashes. The second key is the lintai rule id, without the
// lintai/ prefix of the linter plugins. The entities keys are the semantic
// keys of the diagnostics (EntityIdentity.SemanticKey).
package suppressions

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/bigbizze/lintai/diagnostics"
)

// DefaultFile is the name of the suppressions file at the workspace root.
const DefaultFile = "lintai-suppressions.json"

// File maps a file path to the suppressed rules of that file.
type File map[string]map[string]Entry

// Entry is the number of suppressed violations of one rule in one file.
// When Entities is not empty, the violations are counted for each semantic
// key, and Count is the sum of the entity counts.
type Entry struct {
	Count    int            `json:"count"`
	Entities map[string]int `json:"entities,omitempty"`
}

// UsageError is an invalid suppressions file. The CLI exits with the usage
// exit code for it.
type UsageError struct {
	Path    string
	Message string
}

func (e *UsageError) Error() string {
	return fmt.Sprintf("suppressions file %s: %s", e.Path, e.Message)
}

// ErrNotExist is returned by Load when the file does not exist.
var ErrNotExist = errors.New("suppressions file does not exist")

// Load reads and validates a suppressions file. A file that does not exist
// returns an empty File and an error that matches ErrNotExist.
func Load(path string) (File, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return File{}, fmt.Errorf("%w: %s", ErrNotExist, path)
		}
		return nil, err
	}
	return Parse(path, data)
}

// Parse decodes and validates the content of a suppressions file. path is
// used in error messages only.
func Parse(path string, data []byte) (File, error) {
	invalid := func(format string, args ...any) error {
		return &UsageError{Path: path, Message: fmt.Sprintf(format, args...)}
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	var raw map[string]map[string]json.RawMessage
	if err := decoder.Decode(&raw); err != nil {
		return nil, invalid("is not valid: %v", err)
	}
	if _, err := decoder.Token(); err != io.EOF {
		return nil, invalid("has data after the JSON object")
	}
	if raw == nil {
		return nil, invalid("must be a JSON object")
	}
	file := make(File, len(raw))
	for filePath, rules := range raw {
		if rules == nil {
			return nil, invalid("file %q must contain a rule object", filePath)
		}
		file[filePath] = make(map[string]Entry, len(rules))
		for ruleID, data := range rules {
			var entry Entry
			entryDecoder := json.NewDecoder(bytes.NewReader(data))
			entryDecoder.DisallowUnknownFields()
			if err := entryDecoder.Decode(&entry); err != nil {
				return nil, invalid("rule %q in file %q: %v", ruleID, filePath, err)
			}
			var fields map[string]json.RawMessage
			if err := json.Unmarshal(data, &fields); err != nil {
				return nil, invalid("rule %q in file %q: %v", ruleID, filePath, err)
			}
			if entities, present := fields["entities"]; present && bytes.Equal(bytes.TrimSpace(entities), []byte("null")) {
				return nil, invalid("entities of rule %q in file %q must be an object", ruleID, filePath)
			}
			file[filePath][ruleID] = entry
		}
	}
	for _, filePath := range sortedKeys(file) {
		if file[filePath] == nil {
			return nil, invalid("file %q must contain a rule object", filePath)
		}
		for _, ruleID := range sortedKeys(file[filePath]) {
			entry := file[filePath][ruleID]
			if entry.Count <= 0 {
				return nil, invalid("count of rule %q in file %q must be a positive integer, got %d", ruleID, filePath, entry.Count)
			}
			if entry.Entities == nil {
				continue
			}
			sum := 0
			for _, key := range sortedKeys(entry.Entities) {
				value := entry.Entities[key]
				if key == "" {
					return nil, invalid("entity key of rule %q in file %q must not be empty", ruleID, filePath)
				}
				if value <= 0 {
					return nil, invalid("count of entity %q of rule %q in file %q must be a positive integer, got %d", key, ruleID, filePath, value)
				}
				if value > entry.Count-sum {
					return nil, invalid("entity counts of rule %q in file %q exceed count %d", ruleID, filePath, entry.Count)
				}
				sum += value
			}
			if sum != entry.Count {
				return nil, invalid("count of rule %q in file %q is %d, but its entity counts add up to %d", ruleID, filePath, entry.Count, sum)
			}
		}
	}
	return file, nil
}

// Write writes file as JSON with sorted keys, an indent of two spaces and a
// trailing newline, so that a change to the file has a small diff.
func Write(path string, file File) error {
	if file == nil {
		file = File{}
	}
	data, err := json.MarshalIndent(file, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, append(data, '\n'), 0o644)
}

// ResolvePath returns path, resolved from the workspace root when it is
// relative. An empty path is the default file at the workspace root.
func ResolvePath(workspaceRoot, path string) string {
	if path == "" {
		path = DefaultFile
	}
	if filepath.IsAbs(path) {
		return path
	}
	return filepath.Join(workspaceRoot, path)
}

// NormalizePath returns the key of a diagnostic file in a suppressions
// file: forward slashes and no leading slash. lintai serve uses the same
// key for its per-file diagnostics.
func NormalizePath(path string) string {
	return strings.TrimPrefix(filepath.ToSlash(path), "/")
}

// Clone returns a deep copy of file.
func (f File) Clone() File {
	result := make(File, len(f))
	for filePath, rules := range f {
		copied := make(map[string]Entry, len(rules))
		for ruleID, entry := range rules {
			copied[ruleID] = entry.clone()
		}
		result[filePath] = copied
	}
	return result
}

// Entries returns the number of rule entries in file.
func (f File) Entries() int {
	count := 0
	for _, rules := range f {
		count += len(rules)
	}
	return count
}

func (e Entry) clone() Entry {
	if e.Entities == nil {
		return Entry{Count: e.Count}
	}
	entities := make(map[string]int, len(e.Entities))
	for key, value := range e.Entities {
		entities[key] = value
	}
	return Entry{Count: e.Count, Entities: entities}
}

// Scope describes what a run checked. An entry is unused only when its file
// and its rule are in the scope of the run, so a run with a subset of the
// files or rules does not report the other entries as unused.
type Scope struct {
	// Files are the analyzed files, as NormalizePath keys.
	Files map[string]struct{}
	// Rules are the rules that were evaluated with severity error and
	// had no rule execution error.
	Rules map[string]struct{}
	// KnownRules are the ids of every loaded rule, including rules that
	// failed or that a severity setting turned off or down.
	KnownRules map[string]struct{}
	// AllRulesLoaded is true when the run loaded every rule of the
	// workspace (the default rule glob), with the default error severity,
	// and had no rule execution error. An entry for a rule id that is not
	// in KnownRules is then unused, because the rule was renamed or
	// deleted.
	AllRulesLoaded bool
}

// Covers reports whether the run checked rule ruleID in file filePath.
func (s Scope) Covers(filePath, ruleID string) bool {
	if _, ok := s.Files[filePath]; !ok {
		return false
	}
	return s.coversRule(ruleID)
}

func (s Scope) coversRule(ruleID string) bool {
	if _, ok := s.Rules[ruleID]; ok {
		return true
	}
	if _, known := s.KnownRules[ruleID]; known {
		return false
	}
	return s.AllRulesLoaded
}

// Unused is an entry, or one entity of an entry, that allows more
// violations than the run found. Entity is empty for an entry without
// entities.
type Unused struct {
	File    string `json:"file"`
	RuleID  string `json:"rule_id"`
	Entity  string `json:"entity,omitempty"`
	Allowed int    `json:"allowed"`
	Found   int    `json:"found"`
}

// Exceeded is an entry, or one entity of an entry, that allows fewer
// violations than the run found. All of its violations are reported.
// Allowed is 0 for an entity that the entry does not list.
type Exceeded struct {
	File    string `json:"file"`
	RuleID  string `json:"rule_id"`
	Entity  string `json:"entity,omitempty"`
	Allowed int    `json:"allowed"`
	Found   int    `json:"found"`
}

// Result is the outcome of Apply.
type Result struct {
	// Reported are the diagnostics that are not suppressed, in the order
	// of the input.
	Reported []diagnostics.Diagnostic
	// Suppressed are the suppressed diagnostics, in the order of the input.
	Suppressed []diagnostics.Diagnostic
	// Unused are sorted by file, rule and entity.
	Unused []Unused
	// Exceeded are sorted by file, rule and entity.
	Exceeded []Exceeded
}

// IsCandidate reports whether a diagnostic can be suppressed: an
// error-severity architecture violation with a source location. Rule
// execution errors, warnings and diagnostics without a location are never
// suppressed.
func IsCandidate(item diagnostics.Diagnostic) bool {
	return item.DiagnosticKind == diagnostics.KindArchitectureViolation &&
		item.Severity == diagnostics.SeverityError &&
		item.SourceLocation != nil
}

func semanticKey(item diagnostics.Diagnostic) string {
	if item.EntityIdentity == nil {
		return ""
	}
	return item.EntityIdentity.SemanticKey
}

type groupKey struct {
	file string
	rule string
}

// groupCandidates returns the indexes of the candidates in items for each
// file and rule.
func groupCandidates(items []diagnostics.Diagnostic) map[groupKey][]int {
	groups := make(map[groupKey][]int)
	for index, item := range items {
		if !IsCandidate(item) {
			continue
		}
		key := groupKey{file: NormalizePath(item.SourceLocation.File), rule: item.RuleID}
		groups[key] = append(groups[key], index)
	}
	return groups
}

// Apply suppresses the candidates in items that file allows.
//
// For an entry without entities, the candidates of the file and rule are
// suppressed when there are at most Count of them. When there are more,
// all of them are reported, as ESLint does.
//
// For an entry with entities, the candidates are grouped again by semantic
// key. The candidates of a key are suppressed when the key is listed and
// there are at most as many as its count. Otherwise all candidates of the
// key are reported. A candidate with an empty semantic key is never
// listed.
func Apply(file File, items []diagnostics.Diagnostic, scope Scope) Result {
	suppressed := make([]bool, len(items))
	result := Result{}
	groups := groupCandidates(items)
	for filePath, rules := range file {
		for ruleID, entry := range rules {
			indexes := groups[groupKey{file: filePath, rule: ruleID}]
			covered := scope.Covers(filePath, ruleID)
			if len(entry.Entities) == 0 {
				found := len(indexes)
				if found <= entry.Count {
					for _, index := range indexes {
						suppressed[index] = true
					}
					if found < entry.Count && covered {
						result.Unused = append(result.Unused, Unused{File: filePath, RuleID: ruleID, Allowed: entry.Count, Found: found})
					}
				} else {
					result.Exceeded = append(result.Exceeded, Exceeded{File: filePath, RuleID: ruleID, Allowed: entry.Count, Found: found})
				}
				continue
			}
			byKey := make(map[string][]int)
			for _, index := range indexes {
				key := semanticKey(items[index])
				byKey[key] = append(byKey[key], index)
			}
			for key, keyIndexes := range byKey {
				allowed := 0
				if key != "" {
					allowed = entry.Entities[key]
				}
				if allowed > 0 && len(keyIndexes) <= allowed {
					for _, index := range keyIndexes {
						suppressed[index] = true
					}
					continue
				}
				result.Exceeded = append(result.Exceeded, Exceeded{File: filePath, RuleID: ruleID, Entity: key, Allowed: allowed, Found: len(keyIndexes)})
			}
			if !covered {
				continue
			}
			for key, allowed := range entry.Entities {
				found := len(byKey[key])
				if found < allowed {
					result.Unused = append(result.Unused, Unused{File: filePath, RuleID: ruleID, Entity: key, Allowed: allowed, Found: found})
				}
			}
		}
	}
	result.Reported = make([]diagnostics.Diagnostic, 0, len(items))
	for index, item := range items {
		if suppressed[index] {
			result.Suppressed = append(result.Suppressed, item)
		} else {
			result.Reported = append(result.Reported, item)
		}
	}
	sort.Slice(result.Unused, func(left, right int) bool {
		return compareKeys(result.Unused[left].File, result.Unused[left].RuleID, result.Unused[left].Entity,
			result.Unused[right].File, result.Unused[right].RuleID, result.Unused[right].Entity) < 0
	})
	sort.Slice(result.Exceeded, func(left, right int) bool {
		return compareKeys(result.Exceeded[left].File, result.Exceeded[left].RuleID, result.Exceeded[left].Entity,
			result.Exceeded[right].File, result.Exceeded[right].RuleID, result.Exceeded[right].Entity) < 0
	})
	return result
}

func compareKeys(leftFile, leftRule, leftEntity, rightFile, rightRule, rightEntity string) int {
	if c := strings.Compare(leftFile, rightFile); c != 0 {
		return c
	}
	if c := strings.Compare(leftRule, rightRule); c != 0 {
		return c
	}
	return strings.Compare(leftEntity, rightEntity)
}

// Record returns a copy of file with an entry for each file and rule that
// has candidates in items. rules selects the rules to record; nil records
// every rule. An existing entry for a recorded file and rule is replaced
// by the current count, as ESLint's --suppress-all and --suppress-rule do.
// Entries for other files and rules stay. The entry has entities when
// every candidate of the file and rule has a semantic key.
func Record(file File, items []diagnostics.Diagnostic, rules []string) File {
	result := file.Clone()
	selected := make(map[string]struct{}, len(rules))
	for _, ruleID := range rules {
		selected[ruleID] = struct{}{}
	}
	for key, indexes := range groupCandidates(items) {
		if rules != nil {
			if _, ok := selected[key.rule]; !ok {
				continue
			}
		}
		entry := Entry{Count: len(indexes), Entities: make(map[string]int, len(indexes))}
		for _, index := range indexes {
			semantic := semanticKey(items[index])
			if semantic == "" {
				entry.Entities = nil
				break
			}
			entry.Entities[semantic]++
		}
		if result[key.file] == nil {
			result[key.file] = make(map[string]Entry)
		}
		result[key.file][key.rule] = entry
	}
	return result
}

// Prune returns a copy of file with the counts of unused entries lowered to
// the number of violations that the run found. A count is never raised.
// An entry, entity or file that is left with nothing is removed. An entry
// for a file that is not in the scope of the run and for which exists
// returns false is removed too, as ESLint removes entries of deleted files.
func Prune(file File, items []diagnostics.Diagnostic, scope Scope, exists func(filePath string) bool) File {
	result := file.Clone()
	for _, unused := range Apply(file, items, scope).Unused {
		entry := result[unused.File][unused.RuleID]
		if unused.Entity == "" {
			entry.Count = unused.Found
		} else {
			if unused.Found == 0 {
				delete(entry.Entities, unused.Entity)
			} else {
				entry.Entities[unused.Entity] = unused.Found
			}
			entry.Count = 0
			for _, value := range entry.Entities {
				entry.Count += value
			}
		}
		if entry.Count == 0 {
			delete(result[unused.File], unused.RuleID)
		} else {
			result[unused.File][unused.RuleID] = entry
		}
	}
	for filePath, rules := range result {
		_, analyzed := scope.Files[filePath]
		if !analyzed && exists != nil && !exists(filePath) {
			for ruleID := range rules {
				if scope.coversRule(ruleID) {
					delete(rules, ruleID)
				}
			}
		}
		if len(rules) == 0 {
			delete(result, filePath)
		}
	}
	return result
}

// LimitFailure is an entry of a suppressions file that allows more than the
// locked suppressions.
type LimitFailure struct {
	File    string
	RuleID  string
	Message string
}

// Limit compares file with the locked suppressions. An entry that is not
// locked, that has a higher count or a higher entity count than the locked
// entry, that lists an entity that the locked entry does not list, or that
// has no entities when the locked entry has them, allows more than the
// lock. Limit returns a copy of file in which each such entry is replaced
// by the locked entry, or removed when there is none, and one failure for
// each such entry. Lower counts and removed entries are not failures.
func Limit(file, locked File) (File, []LimitFailure) {
	result := file.Clone()
	failures := make([]LimitFailure, 0)
	for _, filePath := range sortedKeys(file) {
		for _, ruleID := range sortedKeys(file[filePath]) {
			entry := file[filePath][ruleID]
			lockedEntry, ok := locked[filePath][ruleID]
			message := ""
			switch {
			case !ok:
				message = fmt.Sprintf("suppresses %d violations of rule %s in %s, but the rules lock has no suppressions for this file and rule", entry.Count, ruleID, filePath)
			case entry.Count > lockedEntry.Count:
				message = fmt.Sprintf("suppresses %d violations of rule %s in %s, but the rules lock allows %d", entry.Count, ruleID, filePath, lockedEntry.Count)
			case len(entry.Entities) == 0 && len(lockedEntry.Entities) > 0:
				message = fmt.Sprintf("suppresses violations of rule %s in %s without entities, but the rules lock lists the entities", ruleID, filePath)
			case len(entry.Entities) > 0 && len(lockedEntry.Entities) > 0:
				for _, key := range sortedKeys(entry.Entities) {
					if entry.Entities[key] > lockedEntry.Entities[key] {
						message = fmt.Sprintf("suppresses %d violations of rule %s for entity %s in %s, but the rules lock allows %d", entry.Entities[key], ruleID, key, filePath, lockedEntry.Entities[key])
						break
					}
				}
			}
			if message == "" {
				continue
			}
			failures = append(failures, LimitFailure{File: filePath, RuleID: ruleID, Message: message})
			if ok {
				result[filePath][ruleID] = lockedEntry.clone()
			} else {
				delete(result[filePath], ruleID)
				if len(result[filePath]) == 0 {
					delete(result, filePath)
				}
			}
		}
	}
	return result, failures
}

func sortedKeys[V any](values map[string]V) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}
