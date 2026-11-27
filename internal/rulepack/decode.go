package rulepack

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"slices"
	"strings"
	"unicode/utf8"
)

// Decode checks the entire envelope before any entry can be inspected.
func Decode(data []byte) (Pack, error) {
	if !utf8.Valid(data) {
		return Pack{}, fmt.Errorf("rule pack is not UTF-8")
	}
	d := json.NewDecoder(bytes.NewReader(data))
	var pack Pack
	err := object(d, func(key string) error {
		switch key {
		case "format":
			return d.Decode(&pack.Format)
		case "abi":
			return d.Decode(&pack.ABI)
		case "entries":
			if err := delimiter(d, '['); err != nil {
				return err
			}
			pack.Entries = []Entry{}
			seen := make(map[string]bool)
			for d.More() {
				var record Entry
				err := object(d, func(key string) error {
					switch key {
					case "entry":
						return d.Decode(&record.Entry)
					case "code":
						return d.Decode(&record.Code)
					default:
						return fmt.Errorf("unknown entry field %q", key)
					}
				})
				if err != nil {
					return err
				}
				if err := validateName(record.Entry); err != nil {
					return err
				}
				if record.Code == "" {
					return fmt.Errorf("missing executable code for %s", record.Entry)
				}
				if seen[strings.ToLower(record.Entry)] {
					return fmt.Errorf("duplicate rule entry %q", record.Entry)
				}
				seen[strings.ToLower(record.Entry)] = true
				pack.Entries = append(pack.Entries, record)
			}
			return delimiter(d, ']')
		default:
			return fmt.Errorf("unknown pack field %q", key)
		}
	})
	if err != nil {
		return Pack{}, fmt.Errorf("invalid rule pack: %w", err)
	}
	if _, err := d.Token(); err != io.EOF {
		return Pack{}, fmt.Errorf("trailing content in rule pack")
	}
	if pack.Format != Format || pack.ABI != ABI || pack.Entries == nil {
		return Pack{}, fmt.Errorf("invalid rule pack: expected %q, ABI %q and an entries array", Format, ABI)
	}
	if !slices.IsSortedFunc(pack.Entries, func(a, b Entry) int { return strings.Compare(a.Entry, b.Entry) }) {
		return Pack{}, fmt.Errorf("rule pack entries are out of order")
	}
	return pack, nil
}

func object(d *json.Decoder, field func(string) error) error {
	if err := delimiter(d, '{'); err != nil {
		return err
	}
	seen := make(map[string]bool)
	for d.More() {
		token, err := d.Token()
		if err != nil {
			return err
		}
		key, ok := token.(string)
		if !ok {
			return fmt.Errorf("expected object key")
		}
		if seen[key] {
			return fmt.Errorf("duplicate key %q", key)
		}
		seen[key] = true
		if err := field(key); err != nil {
			return err
		}
	}
	return delimiter(d, '}')
}

func delimiter(d *json.Decoder, want json.Delim) error {
	token, err := d.Token()
	if err != nil {
		return err
	}
	if token != want {
		return fmt.Errorf("expected %q", want)
	}
	return nil
}
