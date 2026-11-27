package canonical

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"unicode/utf8"
)

// NormalizeSuppressionOptions retains the historical policy normalization in
// which typed nil maps and slices are treated as empty collections. Suppression
// policy identity deliberately keeps this behavior; JSON identities should use
// NormalizeStructured instead.
func NormalizeSuppressionOptions(value any) (any, error) {
	return detach(value, false)
}

// NormalizeStructured validates and detaches a JSON-shaped value while
// preserving null, array, and object distinctions, including typed nil maps
// and slices.
func NormalizeStructured(value any) (any, error) {
	return detach(value, true)
}

// detach applies one recursive JSON-shaped copy with an explicit nil-collection
// policy. Legacy suppression identity treats typed nil collections as empty;
// structured request and generation identities preserve them as JSON null.
func detach(value any, preserveNull bool) (any, error) {
	switch typed := value.(type) {
	case nil, bool, string:
		return typed, nil
	case float64, float32, int, int32, int64, uint, uint32, uint64:
		return typed, nil
	case []any:
		if preserveNull && typed == nil {
			return []any(nil), nil
		}
		items := make([]any, 0, len(typed))
		for _, item := range typed {
			normalized, err := detach(item, preserveNull)
			if err != nil {
				return nil, err
			}
			items = append(items, normalized)
		}
		return items, nil
	case []string:
		if preserveNull && typed == nil {
			return []any(nil), nil
		}
		items := make([]any, 0, len(typed))
		for _, item := range typed {
			items = append(items, item)
		}
		return items, nil
	case map[string]any:
		if preserveNull && typed == nil {
			return map[string]any(nil), nil
		}
		result := make(map[string]any, len(typed))
		for key, item := range typed {
			normalized, err := detach(item, preserveNull)
			if err != nil {
				return nil, err
			}
			result[key] = normalized
		}
		return result, nil
	case json.Number:
		return typed, nil
	default:
		return nil, fmt.Errorf("value contains unsupported type %T", value)
	}
}

func Marshal(value any) ([]byte, error) {
	normalized, err := detach(value, false)
	if err != nil {
		return nil, err
	}
	buffer := &bytes.Buffer{}
	if err := writeCanonicalJSON(buffer, normalized, false); err != nil {
		return nil, err
	}
	return buffer.Bytes(), nil
}

// MarshalStructured emits a canonical JSON identity, preserving null, arrays,
// and objects for arbitrary JSON-shaped values.
func MarshalStructured(value any) ([]byte, error) {
	normalized, err := detach(value, true)
	if err != nil {
		return nil, err
	}
	buffer := &bytes.Buffer{}
	if err := writeCanonicalJSON(buffer, normalized, true); err != nil {
		return nil, err
	}
	return buffer.Bytes(), nil
}
func writeCanonicalJSON(buffer *bytes.Buffer, value any, structured bool) error {
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
		if typed == nil {
			buffer.WriteString("null")
			break
		}
		buffer.WriteByte('[')
		for index, item := range typed {
			if index > 0 {
				buffer.WriteByte(',')
			}
			if err := writeCanonicalJSON(buffer, item, structured); err != nil {
				return err
			}
		}
		buffer.WriteByte(']')
	case map[string]any:
		if typed == nil {
			buffer.WriteString("null")
			break
		}
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
		if structured {
			buffer.WriteByte('{')
		} else {
			buffer.WriteByte('[')
		}
		for index, key := range keys {
			if index > 0 {
				buffer.WriteByte(',')
			}
			if !structured {
				buffer.WriteByte('[')
			}
			buffer.WriteString(key.encoded)
			if structured {
				buffer.WriteByte(':')
			} else {
				buffer.WriteByte(',')
			}
			if err := writeCanonicalJSON(buffer, typed[key.key], structured); err != nil {
				return err
			}
			if !structured {
				buffer.WriteByte(']')
			}
		}
		if structured {
			buffer.WriteByte('}')
		} else {
			buffer.WriteByte(']')
		}
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
