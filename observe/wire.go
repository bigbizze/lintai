package observe

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"unicode/utf8"
)

const Format = "lintai.observations/1"

// MarshalJSON emits the fields required for the record's kind, including
// false and empty values that are meaningful on the wire.
func (r Record) MarshalJSON() ([]byte, error) {
	switch r.Kind {
	case Content:
		return json.Marshal(struct {
			Kind  Kind   `json:"kind"`
			Path  string `json:"path"`
			Value Value  `json:"value"`
			Stamp *Stamp `json:"stamp,omitempty"`
		}{r.Kind, r.Path, r.Value, r.Stamp})
	case State:
		return json.Marshal(struct {
			Kind  Kind      `json:"kind"`
			Path  string    `json:"path"`
			Op    Operation `json:"op"`
			Value Value     `json:"value"`
		}{r.Kind, r.Path, r.Op, r.Value})
	case Listing:
		return json.Marshal(struct {
			Kind    Kind        `json:"kind"`
			Path    string      `json:"path"`
			Op      Operation   `json:"op"`
			Entries [][2]string `json:"entries"`
			Ignore  []string    `json:"ignore"`
		}{r.Kind, r.Path, r.Op, r.Entries, r.Ignore})
	case Link, Resolved:
		return json.Marshal(struct {
			Kind  Kind   `json:"kind"`
			Path  string `json:"path"`
			Value Value  `json:"value"`
		}{r.Kind, r.Path, r.Value})
	case Env:
		return json.Marshal(struct {
			Kind    Kind   `json:"kind"`
			Path    string `json:"path"`
			Present bool   `json:"present"`
			Value   Value  `json:"value"`
		}{r.Kind, r.Path, r.Present, r.Value})
	default:
		return nil, fmt.Errorf("unknown observation kind %q", r.Kind)
	}
}

// Decode accepts exactly one versioned observation envelope. Unknown and
// duplicate fields, malformed payloads, and trailing data are rejected.
func Decode(data []byte) (Set, error) {
	if !utf8.Valid(data) {
		return Set{}, fmt.Errorf("observation JSON is not UTF-8")
	}
	if err := validUnicodeEscapes(data); err != nil {
		return Set{}, err
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	if err := scanJSON(decoder); err != nil {
		return Set{}, err
	}
	if _, err := decoder.Token(); err != io.EOF {
		if err == nil {
			return Set{}, fmt.Errorf("trailing JSON")
		}
		return Set{}, err
	}
	var envelope map[string]json.RawMessage
	if err := json.Unmarshal(data, &envelope); err != nil || envelope == nil {
		return Set{}, fmt.Errorf("invalid observation envelope")
	}
	if len(envelope) != 2 || !isString(envelope["format"]) || !isArray(envelope["observations"]) {
		return Set{}, fmt.Errorf("invalid observation envelope fields")
	}
	var format string
	_ = json.Unmarshal(envelope["format"], &format)
	if format != Format {
		return Set{}, fmt.Errorf("unsupported observation format %q", format)
	}
	var rows []json.RawMessage
	if err := json.Unmarshal(envelope["observations"], &rows); err != nil {
		return Set{}, err
	}
	records := make([]Record, 0, len(rows))
	for _, row := range rows {
		r, err := decodeRecord(row)
		if err != nil {
			return Set{}, err
		}
		records = append(records, r)
	}
	return New(records)
}

// encoding/json replaces unpaired surrogate escapes with U+FFFD. Reject them
// instead of silently changing a captured path or value.
func validUnicodeEscapes(data []byte) error {
	inString := false
	for i := 0; i < len(data); i++ {
		if !inString {
			if data[i] == '"' {
				inString = true
			}
			continue
		}
		if data[i] == '"' {
			inString = false
			continue
		}
		if data[i] != '\\' {
			continue
		}
		i++
		if i >= len(data) {
			break
		}
		if data[i] != 'u' {
			continue
		}
		unit, ok := unicodeUnit(data, i+1)
		if !ok {
			continue
		} // The JSON decoder reports malformed escapes.
		i += 4
		if unit >= 0xdc00 && unit <= 0xdfff {
			return fmt.Errorf("unpaired JSON surrogate")
		}
		if unit < 0xd800 || unit > 0xdbff {
			continue
		}
		if i+6 >= len(data) || data[i+1] != '\\' || data[i+2] != 'u' {
			return fmt.Errorf("unpaired JSON surrogate")
		}
		low, ok := unicodeUnit(data, i+3)
		if !ok || low < 0xdc00 || low > 0xdfff {
			return fmt.Errorf("unpaired JSON surrogate")
		}
		i += 6
	}
	return nil
}

func unicodeUnit(data []byte, start int) (uint16, bool) {
	if start+4 > len(data) {
		return 0, false
	}
	var result uint16
	for _, c := range data[start : start+4] {
		result <<= 4
		switch {
		case c >= '0' && c <= '9':
			result += uint16(c - '0')
		case c >= 'a' && c <= 'f':
			result += uint16(c - 'a' + 10)
		case c >= 'A' && c <= 'F':
			result += uint16(c - 'A' + 10)
		default:
			return 0, false
		}
	}
	return result, true
}
func isString(v json.RawMessage) bool { return len(v) > 0 && v[0] == '"' }
func isArray(v json.RawMessage) bool  { return len(v) > 0 && v[0] == '[' }
func decodeRecord(raw json.RawMessage) (Record, error) {
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(raw, &fields); err != nil || fields == nil {
		return Record{}, fmt.Errorf("observation must be an object")
	}
	if !isString(fields["kind"]) || !isString(fields["path"]) {
		return Record{}, fmt.Errorf("observation requires kind and path strings")
	}
	var r Record
	if err := json.Unmarshal(raw, &r); err != nil {
		return Record{}, err
	}
	required := map[string]bool{"kind": true, "path": true}
	optional := map[string]bool{}
	switch r.Kind {
	case Content:
		required["value"] = true
		optional["stamp"] = true
	case State:
		required["op"] = true
		required["value"] = true
	case Listing:
		required["op"] = true
		required["entries"] = true
		required["ignore"] = true
	case Link, Resolved:
		required["value"] = true
	case Env:
		required["present"] = true
		required["value"] = true
	default:
		return Record{}, fmt.Errorf("unknown observation kind %q", r.Kind)
	}
	for name := range required {
		v, ok := fields[name]
		if !ok {
			return Record{}, fmt.Errorf("missing observation field %q", name)
		}
		switch name {
		case "kind", "path", "op", "value":
			if !isString(v) {
				return Record{}, fmt.Errorf("observation field %q must be a string", name)
			}
		case "present":
			if string(v) != "true" && string(v) != "false" {
				return Record{}, fmt.Errorf("present must be a boolean")
			}
		case "entries", "ignore":
			if !isArray(v) {
				return Record{}, fmt.Errorf("observation field %q must be an array", name)
			}
		}
	}
	for name := range fields {
		if !required[name] && !optional[name] {
			return Record{}, fmt.Errorf("unexpected observation field %q", name)
		}
	}
	if v, ok := fields["stamp"]; ok && string(v) != "null" {
		var stamp map[string]json.RawMessage
		if err := json.Unmarshal(v, &stamp); err != nil || len(stamp) != 3 || !isString(stamp["size"]) || !isString(stamp["mtimeNs"]) || !isString(stamp["observedAtNs"]) {
			return Record{}, fmt.Errorf("invalid stamp fields")
		}
	}
	if r.Kind == Listing {
		var entries []json.RawMessage
		_ = json.Unmarshal(fields["entries"], &entries)
		for _, entry := range entries {
			var tuple []json.RawMessage
			if !isArray(entry) || json.Unmarshal(entry, &tuple) != nil || len(tuple) != 2 || !isString(tuple[0]) || !isString(tuple[1]) {
				return Record{}, fmt.Errorf("invalid listing tuple")
			}
		}
		var ignores []json.RawMessage
		_ = json.Unmarshal(fields["ignore"], &ignores)
		for _, v := range ignores {
			if !isString(v) {
				return Record{}, fmt.Errorf("invalid ignored name")
			}
		}
	}
	return r, nil
}
func scanJSON(d *json.Decoder) error {
	token, err := d.Token()
	if err != nil {
		return err
	}
	delim, ok := token.(json.Delim)
	if !ok {
		return nil
	}
	switch delim {
	case '{':
		keys := map[string]bool{}
		for d.More() {
			key, err := d.Token()
			if err != nil {
				return err
			}
			name, ok := key.(string)
			if !ok || keys[name] {
				return fmt.Errorf("duplicate or invalid JSON member %q", name)
			}
			keys[name] = true
			if err := scanJSON(d); err != nil {
				return err
			}
		}
		end, err := d.Token()
		if err != nil || end != json.Delim('}') {
			return fmt.Errorf("invalid JSON object")
		}
	case '[':
		for d.More() {
			if err := scanJSON(d); err != nil {
				return err
			}
		}
		end, err := d.Token()
		if err != nil || end != json.Delim(']') {
			return fmt.Errorf("invalid JSON array")
		}
	default:
		return fmt.Errorf("unexpected JSON delimiter")
	}
	return nil
}
