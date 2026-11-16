// Package suppressions defines the current counted suppression file format.
package suppressions

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"unicode/utf8"

	"codeberg.org/TauCeti/mangle-go/ast"

	"github.com/bigbizze/lintai/internal/canonical"
)

const DefaultFile = "lintai-suppressions.json"
const Format = "lintai.suppressions/4"

type Assertion struct {
	Name  string `json:"name"`
	Arity int    `json:"arity"`
}
type Key struct {
	Scheme string `json:"scheme"`
	Data   any    `json:"data"`
}
type Reason struct {
	Name  string
	Value [2]string
}

func (r Reason) MarshalJSON() ([]byte, error) { return json.Marshal([]any{r.Name, r.Value}) }
func (r *Reason) UnmarshalJSON(data []byte) error {
	var fields []json.RawMessage
	if err := json.Unmarshal(data, &fields); err != nil || len(fields) != 2 {
		return fmt.Errorf("reason must be [name, typed value]")
	}
	if err := json.Unmarshal(fields[0], &r.Name); err != nil {
		return err
	}
	var value []string
	if err := json.Unmarshal(fields[1], &value); err != nil || len(value) != 2 {
		return fmt.Errorf("reason value must be a two-string typed value")
	}
	copy(r.Value[:], value)
	return nil
}

type Group struct {
	Namespace   string   `json:"namespace"`
	Key         *Key     `json:"key"`
	Reasons     []Reason `json:"reasons"`
	Description string   `json:"description"`
	Count       int      `json:"count"`
}
type Entry struct {
	File      string    `json:"file"`
	Rule      string    `json:"rule"`
	Assertion Assertion `json:"assertion"`
	Groups    []Group   `json:"groups"`
}
type File []Entry

type UsageError struct{ Path, Message string }

func (e *UsageError) Error() string {
	return fmt.Sprintf("suppressions file %s: %s", e.Path, e.Message)
}

var ErrNotExist = errors.New("suppressions file does not exist")

func Load(name string) (File, error) {
	data, err := os.ReadFile(name)
	if errors.Is(err, os.ErrNotExist) {
		return File{}, fmt.Errorf("%w: %s", ErrNotExist, name)
	}
	if err != nil {
		return nil, err
	}
	return Parse(name, data)
}
func Parse(name string, data []byte) (File, error) {
	fail := func(err error) (File, error) { return nil, &UsageError{Path: name, Message: err.Error()} }
	if !utf8.Valid(data) {
		return fail(fmt.Errorf("invalid UTF-8"))
	}
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	dec.UseNumber()
	var raw struct {
		Format  string `json:"format"`
		Entries File   `json:"entries"`
	}
	if err := dec.Decode(&raw); err != nil {
		return fail(err)
	}
	if _, err := dec.Token(); err != io.EOF {
		return fail(fmt.Errorf("data follows the JSON object"))
	}
	if err := requiredFields(data); err != nil {
		return fail(err)
	}
	if raw.Format != Format {
		return fail(fmt.Errorf("unsupported format %q; expected %s", raw.Format, Format))
	}
	if raw.Entries == nil {
		return fail(fmt.Errorf("entries must be an array"))
	}
	if err := Validate(raw.Entries); err != nil {
		return fail(err)
	}
	return raw.Entries, nil
}

// Required members cannot be inferred from zero values: a null key is
// meaningful and must be explicit, as must the data of a non-null key.
func requiredFields(data []byte) error {
	require := func(object map[string]json.RawMessage, names ...string) error {
		if object == nil {
			return fmt.Errorf("expected an object")
		}
		for _, name := range names {
			if _, ok := object[name]; !ok {
				return fmt.Errorf("missing field %q", name)
			}
		}
		return nil
	}
	var root map[string]json.RawMessage
	if err := json.Unmarshal(data, &root); err != nil {
		return err
	}
	if err := require(root, "format", "entries"); err != nil {
		return err
	}
	var entries []map[string]json.RawMessage
	if err := json.Unmarshal(root["entries"], &entries); err != nil {
		return err
	}
	for _, entry := range entries {
		if err := require(entry, "file", "rule", "assertion", "groups"); err != nil {
			return err
		}
		var assertion map[string]json.RawMessage
		if err := json.Unmarshal(entry["assertion"], &assertion); err != nil {
			return err
		}
		if err := require(assertion, "name", "arity"); err != nil {
			return err
		}
		var groups []map[string]json.RawMessage
		if err := json.Unmarshal(entry["groups"], &groups); err != nil {
			return err
		}
		for _, group := range groups {
			if err := require(group, "namespace", "key", "reasons", "description", "count"); err != nil {
				return err
			}
			if !bytes.Equal(bytes.TrimSpace(group["key"]), []byte("null")) {
				var key map[string]json.RawMessage
				if err := json.Unmarshal(group["key"], &key); err != nil {
					return err
				}
				if err := require(key, "scheme", "data"); err != nil {
					return err
				}
			}
		}
	}
	return nil
}

var namespacePattern = regexp.MustCompile(`^[a-z][a-z0-9_]*$`)

func Validate(file File) error {
	outer := map[string]bool{}
	for _, e := range file {
		if !validPath(e.File) || strings.TrimSpace(e.Rule) == "" || strings.TrimSpace(e.Assertion.Name) == "" || e.Assertion.Arity < 1 {
			return fmt.Errorf("invalid file, rule, or assertion in entry %q %q", e.File, e.Rule)
		}
		id := EntryID(e)
		if outer[id] {
			return fmt.Errorf("duplicate entry %s", id)
		}
		outer[id] = true
		if len(e.Groups) == 0 {
			return fmt.Errorf("entry %s has no groups", id)
		}
		seen := map[string]bool{}
		var names string
		for i, g := range e.Groups {
			if !namespacePattern.MatchString(g.Namespace) || g.Namespace == "lintai" || strings.TrimSpace(g.Description) == "" || g.Count <= 0 || int64(g.Count) > 9007199254740991 || g.Reasons == nil {
				return fmt.Errorf("invalid group in entry %s", id)
			}
			if g.Key != nil {
				if g.Key.Scheme == "" {
					return fmt.Errorf("empty tracking scheme in entry %s", id)
				}
				if err := validateData(g.Key.Data); err != nil {
					return fmt.Errorf("invalid tracking data in entry %s: %w", id, err)
				}
			}
			selected := map[string]bool{}
			reasonNames := make([]string, 0, len(g.Reasons))
			for _, r := range g.Reasons {
				if r.Name == "" || selected[r.Name] {
					return fmt.Errorf("invalid reason name in entry %s", id)
				}
				selected[r.Name] = true
				reasonNames = append(reasonNames, r.Name)
			}
			encodedNames, _ := json.Marshal(reasonNames)
			if i > 0 && names != string(encodedNames) {
				return fmt.Errorf("inconsistent reason columns in entry %s", id)
			}
			names = string(encodedNames)
			key, err := GroupID(e, g)
			if err != nil {
				return err
			}
			if seen[key] {
				return fmt.Errorf("duplicate group in entry %s", id)
			}
			seen[key] = true
		}
	}
	return nil
}
func ValidateReason(v [2]string) error {
	switch v[0] {
	case "s":
		return nil
	case "n":
		if _, err := ast.Name("/" + v[1]); err == nil {
			return nil
		}
	case "i":
		n, err := strconv.ParseInt(v[1], 10, 64)
		if err == nil && strconv.FormatInt(n, 10) == v[1] {
			return nil
		}
	case "f":
		if len(v[1]) == 16 && strings.ToLower(v[1]) == v[1] {
			bits, err := strconv.ParseUint(v[1], 16, 64)
			f := math.Float64frombits(bits)
			if err == nil && !math.IsInf(f, 0) && !math.IsNaN(f) && !(f == 0 && bits != 0) {
				return nil
			}
		}
	}
	return fmt.Errorf("unsupported typed value %q", v)
}
func validateData(v any) error {
	switch x := v.(type) {
	case nil, bool, string:
		return nil
	case int:
		if int64(x) > 9007199254740991 || int64(x) < -9007199254740991 {
			return fmt.Errorf("integer outside safe range")
		}
		return nil
	case int64:
		if x > 9007199254740991 || x < -9007199254740991 {
			return fmt.Errorf("integer outside safe range")
		}
		return nil
	case json.Number:
		n, err := strconv.ParseInt(string(x), 10, 64)
		if err != nil || n > 9007199254740991 || n < -9007199254740991 {
			return fmt.Errorf("number must be a safe integer")
		}
		return nil
	case []any:
		for _, item := range x {
			if err := validateData(item); err != nil {
				return err
			}
		}
		return nil
	case map[string]any:
		for _, item := range x {
			if err := validateData(item); err != nil {
				return err
			}
		}
		return nil
	default:
		return fmt.Errorf("unsupported %T", v)
	}
}
func validPath(p string) bool {
	return p != "" && p != "." && p != ".." && !strings.Contains(p, `\`) && path.Clean(p) == p
}
func ResolvePath(root, name string) string {
	if name == "" {
		name = DefaultFile
	}
	if filepath.IsAbs(name) {
		return name
	}
	return filepath.Join(root, name)
}
func NormalizePath(p string) string { return filepath.ToSlash(p) }
func EntryID(e Entry) string {
	b, _ := canonical.Marshal([]any{e.File, e.Rule, e.Assertion.Name, e.Assertion.Arity})
	return string(b)
}
func GroupID(e Entry, g Group) (string, error) {
	var key any
	if g.Key != nil {
		key = []any{g.Key.Scheme, g.Key.Data}
	}
	reasons := make([]any, 0, len(g.Reasons))
	for _, r := range g.Reasons {
		reasons = append(reasons, []any{r.Name, []any{r.Value[0], r.Value[1]}})
	}
	b, err := canonical.Marshal([]any{"lintai-suppression-group-v3", e.File, e.Rule, []any{e.Assertion.Name, e.Assertion.Arity}, g.Namespace, key, reasons})
	return string(b), err
}
func Encode(file File) ([]byte, error) {
	if file == nil {
		file = File{}
	}
	if err := Validate(file); err != nil {
		return nil, err
	}
	for _, entry := range file {
		for _, group := range entry.Groups {
			for _, reason := range group.Reasons {
				if err := ValidateReason(reason.Value); err != nil {
					return nil, fmt.Errorf("suppression_policy_mismatch: %s/%s: %w", entry.Rule, entry.Assertion.Name, err)
				}
			}
		}
	}
	copyFile := append(File(nil), file...)
	sort.Slice(copyFile, func(i, j int) bool { return EntryID(copyFile[i]) < EntryID(copyFile[j]) })
	for i := range copyFile {
		copyFile[i].Groups = append([]Group(nil), copyFile[i].Groups...)
		e := copyFile[i]
		sort.Slice(copyFile[i].Groups, func(a, b int) bool { x, _ := GroupID(e, e.Groups[a]); y, _ := GroupID(e, e.Groups[b]); return x < y })
	}
	if copyFile == nil {
		copyFile = File{}
	}
	data, err := json.MarshalIndent(struct {
		Format  string `json:"format"`
		Entries File   `json:"entries"`
	}{Format, copyFile}, "", "  ")
	if err != nil {
		return nil, err
	}
	return append(data, '\n'), nil
}
func (f File) Entries() int { return len(f) }

type Unused struct {
	File        string    `json:"file"`
	RuleID      string    `json:"rule_id"`
	Assertion   Assertion `json:"assertion"`
	Description string    `json:"description"`
	Allowed     int       `json:"allowed"`
	Found       int       `json:"found"`
}
type LimitFailure struct{ File, RuleID, Message string }

func Limit(file, approved File) (File, []LimitFailure) {
	allowed := map[string]int{}
	for _, e := range approved {
		for _, g := range e.Groups {
			id, _ := GroupID(e, g)
			allowed[id] = g.Count
		}
	}
	out := File{}
	var failures []LimitFailure
	for _, e := range file {
		copyEntry := e
		copyEntry.Groups = nil
		for _, g := range e.Groups {
			id, _ := GroupID(e, g)
			if g.Count > allowed[id] {
				failures = append(failures, LimitFailure{e.File, e.Rule, fmt.Sprintf("group %s in %s allows %d, but the rules lock approves %d", e.Rule, e.File, g.Count, allowed[id])})
				continue
			}
			copyEntry.Groups = append(copyEntry.Groups, g)
		}
		if len(copyEntry.Groups) > 0 {
			out = append(out, copyEntry)
		}
	}
	return out, failures
}
