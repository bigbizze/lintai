package canonical

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
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
		raw, err := json.Marshal(typed)
		if err != nil {
			return err
		}
		buffer.Write(raw)
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
		keys := make([]string, 0, len(typed))
		for key := range typed {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		buffer.WriteByte('{')
		for index, key := range keys {
			if index > 0 {
				buffer.WriteByte(',')
			}
			rawKey, err := json.Marshal(key)
			if err != nil {
				return err
			}
			buffer.Write(rawKey)
			buffer.WriteByte(':')
			if err := writeCanonicalJSON(buffer, typed[key]); err != nil {
				return err
			}
		}
		buffer.WriteByte('}')
	default:
		return errors.New("encountered unsupported canonical value")
	}
	return nil
}
