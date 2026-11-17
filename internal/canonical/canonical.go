package canonical

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"unicode/utf8"
)

func Normalize(value any) (any, error) {
	switch typed := value.(type) {
	case nil, bool, string:
		return typed, nil
	case float64, float32, int, int32, int64, uint, uint32, uint64:
		return typed, nil
	case []any:
		items := make([]any, 0, len(typed))
		for _, item := range typed {
			normalized, err := Normalize(item)
			if err != nil {
				return nil, err
			}
			items = append(items, normalized)
		}
		return items, nil
	case []string:
		items := make([]any, 0, len(typed))
		for _, item := range typed {
			items = append(items, item)
		}
		return items, nil
	case map[string]any:
		result := make(map[string]any, len(typed))
		for key, item := range typed {
			normalized, err := Normalize(item)
			if err != nil {
				return nil, err
			}
			result[key] = normalized
		}
		return result, nil
	case json.Number:
		return typed, nil
	default:
		return nil, fmt.Errorf("setup value contains unsupported type %T", value)
	}
}

func Marshal(value any) ([]byte, error) {
	normalized, err := Normalize(value)
	if err != nil {
		return nil, err
	}
	buffer := &bytes.Buffer{}
	if err := writeCanonicalJSON(buffer, normalized); err != nil {
		return nil, err
	}
	return buffer.Bytes(), nil
}

func writeCanonicalJSON(buffer *bytes.Buffer, value any) error {
	switch typed := value.(type) {
	case nil:
		buffer.WriteString("null")
	case bool:
		if typed {
			buffer.WriteString("true")
		} else {
			buffer.WriteString("false")
		}
	case string:
		if err := writeJSONString(buffer, typed); err != nil {
			return err
		}
	case float64, float32, int, int32, int64, uint, uint32, uint64, json.Number:
		raw, err := json.Marshal(typed)
		if err != nil {
			return err
		}
		buffer.Write(raw)
	case []any:
		buffer.WriteByte('[')
		for index, item := range typed {
			if index > 0 {
				buffer.WriteByte(',')
			}
			if err := writeCanonicalJSON(buffer, item); err != nil {
				return err
			}
		}
		buffer.WriteByte(']')
	case map[string]any:
		type encodedKey struct {
			key     string
			encoded string
		}
		keys := make([]encodedKey, 0, len(typed))
		for key := range typed {
			encoded := &bytes.Buffer{}
			if err := writeJSONString(encoded, key); err != nil {
				return err
			}
			keys = append(keys, encodedKey{key: key, encoded: encoded.String()})
		}
		sort.Slice(keys, func(i, j int) bool { return keys[i].encoded < keys[j].encoded })
		buffer.WriteByte('[')
		for index, key := range keys {
			if index > 0 {
				buffer.WriteByte(',')
			}
			buffer.WriteByte('[')
			buffer.WriteString(key.encoded)
			buffer.WriteByte(',')
			if err := writeCanonicalJSON(buffer, typed[key.key]); err != nil {
				return err
			}
			buffer.WriteByte(']')
		}
		buffer.WriteByte(']')
	default:
		return errors.New("encountered unsupported canonical value")
	}
	return nil
}

// writeJSONString emits the canonical JSON string form without HTML or
// Unicode separator escaping. Invalid UTF-8 has no canonical representation.
func writeJSONString(buffer *bytes.Buffer, value string) error {
	if !utf8.ValidString(value) {
		return errors.New("canonical string is not valid UTF-8")
	}
	buffer.WriteByte('"')
	const hex = "0123456789abcdef"
	for _, char := range value {
		switch char {
		case '"':
			buffer.WriteString(`\"`)
		case '\\':
			buffer.WriteString(`\\`)
		case '\b':
			buffer.WriteString(`\b`)
		case '\f':
			buffer.WriteString(`\f`)
		case '\n':
			buffer.WriteString(`\n`)
		case '\r':
			buffer.WriteString(`\r`)
		case '\t':
			buffer.WriteString(`\t`)
		default:
			if char < 0x20 {
				buffer.WriteString(`\u00`)
				buffer.WriteByte(hex[byte(char)>>4])
				buffer.WriteByte(hex[byte(char)&15])
			} else {
				buffer.WriteRune(char)
			}
		}
	}
	buffer.WriteByte('"')
	return nil
}
