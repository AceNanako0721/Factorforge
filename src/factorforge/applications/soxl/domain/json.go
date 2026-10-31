package domain

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
)

// DecodePrivate accepts one closed JSON record. Duplicate keys are ambiguous
// even when a decoder would silently keep the last value.
func DecodePrivate(raw []byte, target any) error {
	walk := json.NewDecoder(bytes.NewReader(raw))
	walk.UseNumber()
	var value func() error
	value = func() error {
		token, err := walk.Token()
		if err != nil {
			return err
		}
		delim, container := token.(json.Delim)
		if !container {
			return nil
		}
		switch delim {
		case '{':
			seen := map[string]bool{}
			for walk.More() {
				key, err := walk.Token()
				if err != nil {
					return err
				}
				text, ok := key.(string)
				if !ok || seen[text] {
					return fmt.Errorf("duplicate JSON key")
				}
				seen[text] = true
				if err = value(); err != nil {
					return err
				}
			}
		case '[':
			for walk.More() {
				if err = value(); err != nil {
					return err
				}
			}
		default:
			return fmt.Errorf("unexpected JSON delimiter")
		}
		end, err := walk.Token()
		if err != nil {
			return err
		}
		if delim == '{' && end != json.Delim('}') || delim == '[' && end != json.Delim(']') {
			return fmt.Errorf("JSON delimiter mismatch")
		}
		return nil
	}
	if err := value(); err != nil {
		return err
	}
	if _, err := walk.Token(); err != io.EOF {
		return fmt.Errorf("trailing JSON")
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return err
	}
	if decoder.Decode(new(any)) != io.EOF {
		return fmt.Errorf("trailing JSON")
	}
	return nil
}
