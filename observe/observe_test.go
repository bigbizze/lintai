package observe

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

func hash(value string) Value {
	sum := sha256.Sum256([]byte(value))
	return Value(hex.EncodeToString(sum[:]))
}
func mustSet(t *testing.T, records ...Record) Set {
	t.Helper()
	s, err := New(records)
	if err != nil {
		t.Fatal(err)
	}
	return s
}
func changed(t *testing.T, s Set, mode Mode) bool {
	t.Helper()
	record, err := s.Changed(context.Background(), mode)
	if err != nil {
		t.Fatal(err)
	}
	return record != nil
}
func write(t *testing.T, path, value string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(value), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestKindsAndIndependentOperations(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "file")
	write(t, file, "a")
	sub := filepath.Join(dir, "sub")
	if err := os.Mkdir(sub, 0o700); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(dir, "link")
	if err := os.Symlink("file", link); err != nil {
		t.Fatal(err)
	}
	missing := filepath.Join(dir, "missing")
	resolvedFile, err := filepath.EvalSymlinks(file)
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("LINTAI_OBSERVE_TEST", "")
	cases := []struct {
		name   string
		record Record
		mutate func()
	}{
		{"content", Record{Kind: Content, Path: file, Value: hash("a")}, func() { write(t, file, "b") }},
		{"compiler-file false for directory", Record{Kind: State, Path: sub, Op: CompilerFile, Value: False}, func() {
			if err := os.Remove(sub); err != nil {
				t.Fatal(err)
			}
			write(t, sub, "x")
		}},
		{"regular-file", Record{Kind: State, Path: missing, Op: RegularFile, Value: False}, func() { write(t, missing, "x") }},
		{"directory", Record{Kind: State, Path: missing, Op: Directory, Value: False}, func() {
			if err := os.Mkdir(missing, 0o700); err != nil {
				t.Fatal(err)
			}
		}},
		{"stat", Record{Kind: State, Path: missing, Op: Stat, Value: Missing}, func() { write(t, missing, "x") }},
		{"read-file", Record{Kind: State, Path: missing, Op: ReadFile, Value: Missing}, func() { write(t, missing, "x") }},
		{"read-directory", Record{Kind: State, Path: missing, Op: ReadDirectory, Value: Missing}, func() {
			if err := os.Mkdir(missing, 0o700); err != nil {
				t.Fatal(err)
			}
		}},
		{"link", Record{Kind: Link, Path: link, Value: "file"}, func() {
			_ = os.Remove(link)
			if err := os.Symlink("sub", link); err != nil {
				t.Fatal(err)
			}
		}},
		{"resolved", Record{Kind: Resolved, Path: link, Value: Value(resolvedFile)}, func() {
			_ = os.Remove(link)
			if err := os.Symlink("sub", link); err != nil {
				t.Fatal(err)
			}
		}},
		{"environment empty is present", Record{Kind: Env, Path: "LINTAI_OBSERVE_TEST", Present: true}, func() { _ = os.Unsetenv("LINTAI_OBSERVE_TEST") }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			// Restore the common fixture before each case.
			_ = os.RemoveAll(missing)
			_ = os.RemoveAll(sub)
			if err := os.Mkdir(sub, 0o700); err != nil {
				t.Fatal(err)
			}
			write(t, file, "a")
			_ = os.Remove(link)
			if err := os.Symlink("file", link); err != nil {
				t.Fatal(err)
			}
			t.Setenv("LINTAI_OBSERVE_TEST", "")
			set := mustSet(t, tc.record)
			if changed(t, set, Full) {
				t.Fatal("initial state did not match")
			}
			tc.mutate()
			if !changed(t, set, Full) {
				t.Fatal("change was missed")
			}
		})
	}
	physical := mustSet(t, Record{Kind: Listing, Path: dir, Op: Physical, Entries: [][2]string{{"file", "file"}, {"sub", "directory"}, {"link", "symlink"}}})
	accessible := mustSet(t, Record{Kind: Listing, Path: dir, Op: CompilerAccessible, Entries: [][2]string{{"file", "file"}, {"sub", "directory"}, {"link", "file"}, {"link", "symlink"}}})
	if changed(t, physical, Full) || changed(t, accessible, Full) {
		t.Fatal("listing mismatch")
	}
	_ = os.Remove(link)
	if err := os.Symlink("missing", link); err != nil {
		t.Fatal(err)
	}
	if changed(t, physical, Full) || !changed(t, accessible, Full) {
		t.Fatal("physical and accessible link views differ")
	}
}

func TestIdentityAndConflicts(t *testing.T) {
	root := t.TempDir()
	a := filepath.Join(root, "project")
	b := filepath.Join(root, "other", "project")
	externalA := filepath.Join(root, "shared")
	externalB := filepath.Join(root, "other", "shared")
	records := []Record{{Kind: Content, Path: filepath.Join(a, "a"), Value: hash("a"), Stamp: &Stamp{"1", "1", "3"}}, {Kind: Link, Path: filepath.Join(a, "link"), Value: Value(filepath.Join(a, "a"))}, {Kind: Resolved, Path: filepath.Join(a, "alias"), Value: Value(externalA)}, {Kind: Env, Path: "VAR", Present: true, Value: "/opaque/value"}}
	first := mustSet(t, records...)
	records[0].Stamp = nil
	reordered := mustSet(t, records[3], records[2], records[1], records[0], records[0])
	d1, err := first.Digest(a)
	if err != nil {
		t.Fatal(err)
	}
	d2, _ := reordered.Digest(a)
	if d1 != d2 {
		t.Fatal("order, duplicate or stamp changed identity")
	}
	relocated := []Record{{Kind: Content, Path: filepath.Join(b, "a"), Value: hash("a")}, {Kind: Link, Path: filepath.Join(b, "link"), Value: Value(filepath.Join(b, "a"))}, {Kind: Resolved, Path: filepath.Join(b, "alias"), Value: Value(externalB)}, {Kind: Env, Path: "VAR", Present: true, Value: "/opaque/value"}}
	d3, _ := mustSet(t, relocated...).Digest(b)
	if d1 != d3 {
		t.Fatal("equivalent relocation changed identity")
	}
	relocated[3].Value = "/opaque/other"
	d4, _ := mustSet(t, relocated...).Digest(b)
	if d4 == d3 {
		t.Fatal("environment value omitted")
	}
	file := filepath.Join(root, "file")
	write(t, file, "a")
	set := Union(mustSet(t, Record{Kind: Content, Path: file, Value: hash("a")}), mustSet(t, Record{Kind: Content, Path: file, Value: hash("b")}))
	if len(set.Records()) != 2 || !changed(t, set, Full) {
		t.Fatal("conflicting expectations must reject reuse")
	}
	detached := first.Records()
	detached[0].Path = "changed"
	if first.Records()[0].Path == "changed" {
		t.Fatal("records exposed mutable state")
	}
}

func TestQuickStampRenewalAndRestoredMtime(t *testing.T) {
	realNow := now
	defer func() { now = realNow }()
	clock := time.Unix(2000, 0)
	now = func() time.Time { return clock }
	file := filepath.Join(t.TempDir(), "input")
	write(t, file, "aaa")
	mtime := clock.Add(-time.Second)
	if err := os.Chtimes(file, mtime, mtime); err != nil {
		t.Fatal(err)
	}
	stamp := &Stamp{"3", fmt.Sprint(mtime.UnixNano()), fmt.Sprint(clock.UnixNano())}
	set := mustSet(t, Record{Kind: Content, Path: file, Value: hash("aaa"), Stamp: stamp})
	before, _ := set.Digest(filepath.Dir(file))
	write(t, file, "bbb")
	_ = os.Chtimes(file, mtime, mtime)
	if !changed(t, set, Quick) {
		t.Fatal("racy restored timestamp missed")
	}
	write(t, file, "aaa")
	_ = os.Chtimes(file, mtime, mtime)
	clock = clock.Add(3 * time.Second)
	if changed(t, set, Quick) {
		t.Fatal("matching reread failed")
	}
	after, _ := set.Digest(filepath.Dir(file))
	if before != after {
		t.Fatal("hint renewal changed identity")
	}
	write(t, file, "bbb")
	_ = os.Chtimes(file, mtime, mtime)
	if changed(t, set, Quick) {
		t.Fatal("certified hint should skip a restored timestamp")
	}
	if !changed(t, set, Full) {
		t.Fatal("full validation missed content")
	}
	if _, err := set.Changed(context.Background(), Mode(9)); err == nil {
		t.Fatal("unknown mode accepted")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := set.Changed(ctx, Full); err != context.Canceled {
		t.Fatalf("canceled validation: %v", err)
	}
}

func TestWireStrictness(t *testing.T) {
	path := filepath.Join(t.TempDir(), "x")
	good := fmt.Sprintf(`{"format":"%s","observations":[{"kind":"content","path":%q,"value":"%s"}]}`, Format, path, hash("x"))
	if _, err := Decode([]byte(good)); err != nil {
		t.Fatal(err)
	}
	set := mustSet(t, Record{Kind: Env, Path: "ABSENT"}, Record{Kind: Listing, Path: filepath.Dir(path), Op: Physical})
	encoded, err := json.Marshal(struct {
		Format       string   `json:"format"`
		Observations []Record `json:"observations"`
	}{Format, set.Records()})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Decode(encoded); err != nil {
		t.Fatalf("encoded records did not round trip: %s: %v", encoded, err)
	}
	if _, err := Decode([]byte(`{"format":"lintai.observations/1","observations":[{"kind":"env","path":"EXAMPLE","present":true,"value":"\ud83d\ude00"}]}`)); err != nil {
		t.Fatalf("valid surrogate pair: %v", err)
	}
	bad := []string{
		strings.Replace(good, Format, "old/1", 1), good + " {}", strings.Replace(good, `"kind":"content"`, `"kind":"content","kind":"content"`, 1), strings.Replace(good, `"value":`, `"extra":1,"value":`, 1), strings.Replace(good, `"value":`, `"stamp":{"size":"1"},"value":`, 1), strings.Replace(good, `"kind":"content"`, `"kind":"unknown"`, 1), strings.Replace(good, string(hash("x")), strings.ToUpper(string(hash("x"))), 1), strings.Replace(good, fmt.Sprintf("%q", path), `"relative"`, 1), strings.Replace(good, `"observations":[`, `"observations":null,"unused":[`, 1),
		strings.Replace(good, fmt.Sprintf("%q", path), `"/tmp/\ud800"`, 1),
	}
	for _, input := range bad {
		if _, err := Decode([]byte(input)); err == nil {
			t.Errorf("accepted %s", input)
		}
	}
}

func TestFullReadsNonRegularContent(t *testing.T) {
	if !filepath.IsAbs(os.DevNull) {
		t.Skip("no absolute null device")
	}
	info, err := os.Stat(os.DevNull)
	if err != nil || info.Mode().IsRegular() {
		t.Skip("no non-regular null device")
	}
	set := mustSet(t, Record{Kind: Content, Path: os.DevNull, Value: hash("")})
	if changed(t, set, Full) || changed(t, set, Quick) {
		t.Fatal("unchanged non-regular content was rejected")
	}
}

func TestConcurrentHintRenewal(t *testing.T) {
	file := filepath.Join(t.TempDir(), "input")
	write(t, file, "content")
	old := time.Unix(100000, 0)
	if err := os.Chtimes(file, old, old); err != nil {
		t.Fatal(err)
	}
	set := mustSet(t, Record{Kind: Content, Path: file, Value: hash("content")})
	before, _ := set.Digest(filepath.Dir(file))
	var workers sync.WaitGroup
	errors := make(chan error, 16)
	for i := 0; i < cap(errors); i++ {
		workers.Add(1)
		go func(mode Mode) {
			defer workers.Done()
			for j := 0; j < 4; j++ {
				changed, err := set.Changed(context.Background(), mode)
				if err != nil || changed != nil {
					errors <- fmt.Errorf("changed=%v err=%v", changed, err)
					return
				}
			}
		}(Mode(i % 2))
	}
	workers.Wait()
	close(errors)
	for err := range errors {
		t.Error(err)
	}
	after, _ := set.Digest(filepath.Dir(file))
	if before != after {
		t.Fatal("hint renewal changed identity")
	}
}
