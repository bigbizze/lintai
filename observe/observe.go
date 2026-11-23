// Package observe records and validates the inputs consumed by an analysis.
package observe

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"unicode/utf8"
)

type Mode uint8

const (
	Full Mode = iota
	Quick
)

type Kind string
type Operation string
type Value string

const (
	Content  Kind = "content"
	State    Kind = "state"
	Listing  Kind = "listing"
	Link     Kind = "link"
	Resolved Kind = "resolved"
	Env      Kind = "env"

	CompilerFile       Operation = "compiler-file"
	RegularFile        Operation = "regular-file"
	Directory          Operation = "directory"
	Stat               Operation = "stat"
	ReadFile           Operation = "read-file"
	ReadDirectory      Operation = "read-directory"
	Physical           Operation = "physical"
	CompilerAccessible Operation = "compiler-accessible"

	True           Value = "true"
	False          Value = "false"
	Missing        Value = "missing"
	Unreadable     Value = "unreadable"
	File           Value = "file"
	DirectoryValue Value = "directory"
	Symlink        Value = "symlink"
	Other          Value = "other"
)

// Stamp certifies the metadata immediately before the bytes were read.
type Stamp struct {
	Size         string `json:"size"`
	MtimeNs      string `json:"mtimeNs"`
	ObservedAtNs string `json:"observedAtNs"`
}

// Record describes one operation and its captured result. Entries are pairs of
// immediate child name and kind. Fields not applicable to Kind must be zero.
type Record struct {
	Kind    Kind        `json:"kind"`
	Path    string      `json:"path"`
	Op      Operation   `json:"op,omitempty"`
	Value   Value       `json:"value,omitempty"`
	Entries [][2]string `json:"entries,omitempty"`
	Ignore  []string    `json:"ignore,omitempty"`
	Present bool        `json:"present,omitempty"`
	Stamp   *Stamp      `json:"stamp,omitempty"`
}

type setData struct {
	records     []Record
	conflicting [2]int
	hasConflict bool
	mu          sync.Mutex
	hints       []*Stamp
}

// Set owns immutable semantic records and synchronized, renewable hints.
// Its zero value is an empty set.
type Set struct{ data *setData }

func validText(s string) bool { return utf8.ValidString(s) && !strings.ContainsRune(s, 0) }
func validPath(s string) bool { return validText(s) && filepath.IsAbs(s) && filepath.Clean(s) == s }
func validChild(s string) bool {
	return validText(s) && s != "" && s != "." && s != ".." && filepath.Base(s) == s && !strings.ContainsRune(s, '/')
}
func validStamp(s *Stamp) bool {
	if s == nil {
		return true
	}
	size, e1 := strconv.ParseInt(s.Size, 10, 64)
	_, e2 := strconv.ParseInt(s.MtimeNs, 10, 64)
	at, e3 := strconv.ParseInt(s.ObservedAtNs, 10, 64)
	return e1 == nil && e2 == nil && e3 == nil && size >= 0 && at >= 0 && decimal(s.Size) && decimal(s.MtimeNs) && decimal(s.ObservedAtNs)
}
func decimal(s string) bool {
	if s == "" {
		return false
	}
	if s[0] == '-' {
		s = s[1:]
	}
	if s == "" {
		return false
	}
	for _, c := range s {
		if c < '0' || c > '9' {
			return false
		}
	}
	return true
}
func copyRecord(r Record) Record {
	r.Entries = slices.Clone(r.Entries)
	r.Ignore = slices.Clone(r.Ignore)
	if r.Stamp != nil {
		stamp := *r.Stamp
		r.Stamp = &stamp
	}
	return r
}
func normalize(r Record) (Record, error) {
	r = copyRecord(r)
	if !validText(r.Path) || r.Path == "" || (r.Kind != Env && !validPath(r.Path)) {
		return r, fmt.Errorf("invalid observation path %q", r.Path)
	}
	if !validText(string(r.Value)) || !validStamp(r.Stamp) {
		return r, fmt.Errorf("invalid observation value or stamp for %s", r.Path)
	}
	noListing := r.Entries == nil && r.Ignore == nil
	switch r.Kind {
	case Content:
		if r.Op != "" || !noListing || r.Present || len(r.Value) != 64 || string(r.Value) != strings.ToLower(string(r.Value)) {
			return r, fmt.Errorf("invalid content observation %s", r.Path)
		}
		if _, err := hex.DecodeString(string(r.Value)); err != nil {
			return r, fmt.Errorf("invalid content digest: %w", err)
		}
	case State:
		if !noListing || r.Present || r.Stamp != nil {
			return r, fmt.Errorf("invalid state observation %s", r.Path)
		}
		switch r.Op {
		case CompilerFile, RegularFile, Directory:
			if r.Value != True && r.Value != False {
				return r, fmt.Errorf("invalid predicate value")
			}
		case Stat:
			if r.Value != Missing && r.Value != File && r.Value != DirectoryValue {
				return r, fmt.Errorf("invalid stat value")
			}
		case ReadFile, ReadDirectory:
			if r.Value != Missing && r.Value != Unreadable {
				return r, fmt.Errorf("invalid read value")
			}
		default:
			return r, fmt.Errorf("invalid state operation %q", r.Op)
		}
	case Listing:
		if r.Op != Physical && r.Op != CompilerAccessible || r.Value != "" || r.Present || r.Stamp != nil {
			return r, fmt.Errorf("invalid listing observation %s", r.Path)
		}
		for _, name := range r.Ignore {
			if !validChild(name) {
				return r, fmt.Errorf("invalid ignored child %q", name)
			}
		}
		slices.Sort(r.Ignore)
		r.Ignore = slices.Compact(r.Ignore)
		for _, entry := range r.Entries {
			if !validChild(entry[0]) || (entry[1] != string(File) && entry[1] != string(DirectoryValue) && entry[1] != string(Symlink) && (r.Op != Physical || entry[1] != string(Other))) {
				return r, fmt.Errorf("invalid listing entry %q", entry)
			}
		}
		r.Entries = slices.DeleteFunc(r.Entries, func(e [2]string) bool { _, ok := slices.BinarySearch(r.Ignore, e[0]); return ok })
		slices.SortFunc(r.Entries, func(a, b [2]string) int {
			if c := strings.Compare(a[0], b[0]); c != 0 {
				return c
			}
			return strings.Compare(a[1], b[1])
		})
		r.Entries = slices.Compact(r.Entries)
		if r.Entries == nil {
			r.Entries = [][2]string{}
		}
		if r.Ignore == nil {
			r.Ignore = []string{}
		}
	case Link:
		if r.Op != "" || !noListing || r.Present || r.Stamp != nil || r.Value == "" {
			return r, fmt.Errorf("invalid link observation %s", r.Path)
		}
	case Resolved:
		if r.Op != "" || !noListing || r.Present || r.Stamp != nil || !validPath(string(r.Value)) {
			return r, fmt.Errorf("invalid resolved observation %s", r.Path)
		}
	case Env:
		if r.Op != "" || !noListing || r.Stamp != nil || (!r.Present && r.Value != "") || strings.Contains(r.Path, "=") {
			return r, fmt.Errorf("invalid environment observation %s", r.Path)
		}
	default:
		return r, fmt.Errorf("unknown observation kind %q", r.Kind)
	}
	return r, nil
}
func semanticKey(r Record) string { r.Stamp = nil; b, _ := json.Marshal(r); return string(b) }

// operationKey groups expectations that cannot both hold when their results
// differ. Listings with different ignore sets are not assumed to conflict.
func operationKey(r Record) string {
	r.Value = ""
	r.Present = false
	if r.Kind == Listing {
		r.Entries = nil
	}
	return semanticKey(r)
}

// New validates and detaches records, retaining conflicting expectations.
func New(records []Record) (Set, error) {
	unique := make(map[string]Record, len(records))
	for _, raw := range records {
		r, err := normalize(raw)
		if err != nil {
			return Set{}, err
		}
		k := semanticKey(r)
		if old, ok := unique[k]; !ok || old.Stamp == nil && r.Stamp != nil {
			unique[k] = r
		}
	}
	keys := make([]string, 0, len(unique))
	for k := range unique {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	if len(keys) == 0 {
		return Set{}, nil
	}
	d := &setData{records: make([]Record, 0, len(keys)), hints: make([]*Stamp, 0, len(keys))}
	operations := make(map[string]int, len(keys))
	for _, k := range keys {
		r := unique[k]
		op := operationKey(r)
		if first, ok := operations[op]; ok {
			if !d.hasConflict {
				d.conflicting = [2]int{first, len(d.records)}
				d.hasConflict = true
			}
		} else {
			operations[op] = len(d.records)
		}
		d.records = append(d.records, r)
		if r.Stamp == nil {
			d.hints = append(d.hints, nil)
		} else {
			s := *r.Stamp
			d.hints = append(d.hints, &s)
		}
	}
	return Set{d}, nil
}
func Union(sets ...Set) Set {
	var records []Record
	for _, s := range sets {
		records = append(records, s.Records()...)
	}
	result, err := New(records)
	if err != nil {
		panic("observe: invalid internal set: " + err.Error())
	}
	return result
}
func (s Set) Records() []Record {
	if s.data == nil {
		return []Record{}
	}
	r := make([]Record, len(s.data.records))
	for i, v := range s.data.records {
		r[i] = copyRecord(v)
	}
	return r
}

// Digest hashes only semantic data. It does not inspect the filesystem.
func (s Set) Digest(root string) (string, error) {
	if !validPath(root) {
		return "", fmt.Errorf("invalid workspace root %q", root)
	}
	pathValue := func(p string) [2]string {
		if rel, err := filepath.Rel(root, p); err == nil {
			return [2]string{"relative", filepath.ToSlash(rel)}
		}
		return [2]string{"absolute", filepath.ToSlash(filepath.Clean(p))}
	}
	type item struct {
		Kind    Kind        `json:"kind"`
		Path    any         `json:"path"`
		Op      Operation   `json:"op"`
		Value   any         `json:"value"`
		Entries [][2]string `json:"entries"`
		Ignore  []string    `json:"ignore"`
		Present bool        `json:"present"`
	}
	values := make([][]byte, 0)
	for _, r := range s.Records() {
		var path any = pathValue(r.Path)
		if r.Kind == Env {
			path = r.Path
		}
		var value any = string(r.Value)
		if r.Kind == Resolved || r.Kind == Link && filepath.IsAbs(string(r.Value)) {
			value = pathValue(string(r.Value))
		}
		b, err := json.Marshal(item{r.Kind, path, r.Op, value, r.Entries, r.Ignore, r.Present})
		if err != nil {
			return "", err
		}
		values = append(values, b)
	}
	slices.SortFunc(values, bytes.Compare)
	values = slices.CompactFunc(values, bytes.Equal)
	hash := sha256.New()
	hash.Write([]byte("lintai.observation-identity/1\x00"))
	for _, v := range values {
		fmt.Fprintf(hash, "%d:", len(v))
		hash.Write(v)
	}
	return "sha256:" + hex.EncodeToString(hash.Sum(nil)), nil
}
