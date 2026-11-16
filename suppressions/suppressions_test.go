package suppressions

import (
	"strings"
	"testing"
)

// V25, V35, V37, V39: current format validation, typed group identity and approval.
func TestCurrentFormatAndApproval(t *testing.T) {
	source := `{"format":"lintai.suppressions/4","entries":[{"file":"src/a.ts","rule":"r","assertion":{"name":"v","arity":2},"groups":[{"namespace":"sql","key":{"scheme":"catalog/1","data":{"name":"x"}},"reasons":[["Reason",["n","clock"]]],"description":"item","count":2}]}]}`
	file, err := Parse("fixture", []byte(source))
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := Encode(file)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = Parse("roundtrip", encoded); err != nil {
		t.Fatal(err)
	}
	for _, bad := range []string{
		`{}`, strings.Replace(source, `lintai.suppressions/4`, `lintai.suppressions/3`, 1),
		strings.Replace(source, `"count":2`, `"count":0`, 1),
		strings.Replace(source, `"key":{"scheme":"catalog/1","data":{"name":"x"}},`, ``, 1), strings.Replace(source, `"description":"item"`, `"description":"item","extra":true`, 1),
		strings.Replace(source, `src/a.ts`, ``, 1), strings.Replace(source, `src/a.ts`, `.`, 1),
		strings.Replace(source, `src/a.ts`, `..`, 1), strings.Replace(source, `src/a.ts`, `src/../a.ts`, 1),
		strings.Replace(source, `src/a.ts`, `src\\a.ts`, 1),
		strings.Replace(source, `"groups":[{`, `"groups":[{"namespace":"sql","key":{"scheme":"catalog/1","data":{"name":"x"}},"reasons":[["Reason",["n","clock"]]],"description":"other","count":1},{`, 1),
	} {
		if _, err := Parse("bad", []byte(bad)); err == nil {
			t.Fatalf("accepted %s", bad)
		}
	}
	for _, acceptedPath := range []string{"../a.ts", "/x/a.ts"} {
		candidate := strings.Replace(source, `src/a.ts`, acceptedPath, 1)
		if _, err := Parse("path", []byte(candidate)); err != nil {
			t.Fatalf("rejected normalized path %q: %v", acceptedPath, err)
		}
	}
	if got := NormalizePath("/x/a.ts"); got != "/x/a.ts" {
		t.Fatalf("absolute path lost its leading slash: %q", got)
	}
	if NormalizePath("/x/a.ts") == NormalizePath("x/a.ts") {
		t.Fatal("absolute and relative paths were conflated")
	}
	approved := file
	changed := append(File(nil), file...)
	changed[0].Groups = append([]Group(nil), file[0].Groups...)
	changed[0].Groups[0].Description = "new preview"
	if _, fail := Limit(changed, approved); len(fail) != 0 {
		t.Fatal(fail)
	}
	changed[0].Groups[0].Count = 3
	if _, fail := Limit(changed, approved); len(fail) != 1 {
		t.Fatalf("missing increased-count failure: %v", fail)
	}
	changed[0].Groups[0].Count = 2
	changed[0].Groups[0].Reasons = []Reason{{Name: "Reason", Value: [2]string{"s", "clock"}}}
	if _, fail := Limit(changed, approved); len(fail) != 1 {
		t.Fatal("name and string must differ")
	}
	changed[0].Groups[0].Reasons = []Reason{{Name: "Reason", Value: [2]string{"i", "1"}}}
	integerID, err := GroupID(changed[0], changed[0].Groups[0])
	if err != nil {
		t.Fatal(err)
	}
	changed[0].Groups[0].Reasons[0].Value = [2]string{"f", "3ff0000000000000"}
	floatID, err := GroupID(changed[0], changed[0].Groups[0])
	if err != nil {
		t.Fatal(err)
	}
	if integerID == floatID {
		t.Fatal("integer and float reasons must differ")
	}
	changed[0].Groups[0].Reasons[0].Value = [2]string{"s", "<tag>&\u2028"}
	unicodeID, err := GroupID(changed[0], changed[0].Groups[0])
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(unicodeID, "<tag>&\u2028") || strings.Contains(unicodeID, `\u003c`) {
		t.Fatalf("group identity is not canonical UTF-8: %s", unicodeID)
	}
	changed[0].Groups[0].Key = &Key{Scheme: "test/1", Data: map[string]any{"z": 1, "a": 2}}
	objectID, err := GroupID(changed[0], changed[0].Groups[0])
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(objectID, `[["a",2],["z",1]]`) {
		t.Fatalf("tracking map is not encoded as sorted pairs: %s", objectID)
	}
}
