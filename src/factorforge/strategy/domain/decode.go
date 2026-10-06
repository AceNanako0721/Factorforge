package domain

import (
	"bytes"
	"encoding/json"

	"io"
	"reflect"
	"regexp"
	"time"
	"unicode/utf8"
)

type shape map[string]json.RawMessage

var shapes = func() map[string]shape {
	var s map[string]shape
	if json.Unmarshal([]byte(recordSchema), &s) != nil {
		panic("invalid frozen request schema")
	}
	return s
}()

func invalid() error { return &Error{Code: "INVALID_REQUEST", Status: 422} }

// Decode validates presence and nullability before Go can replace either with
// zero values. It rejects duplicate/unknown keys at every nested model and
// applies only the frozen default factories, never production calibration.
func Decode(reader io.Reader, target any) error {
	data, err := io.ReadAll(io.LimitReader(reader, 4<<20+1))
	if err != nil || len(data) > 4<<20 {
		return invalid()
	}
	t := reflect.TypeOf(target)
	if t == nil || t.Kind() != reflect.Pointer {
		return invalid()
	}
	schema, ok := shapes[t.Elem().Name()]
	if !ok {
		return invalid()
	}
	normalized, err := admit(data, schema)
	if err != nil {
		return invalid()
	}
	decoder := json.NewDecoder(bytes.NewReader(normalized))
	decoder.DisallowUnknownFields()
	decoder.UseNumber()
	if decoder.Decode(target) != nil || Validate(target) != nil {
		return invalid()
	}
	if err := Validate(target); err != nil {
		return invalid()
	}
	return nil
}
func admit(data []byte, s shape) ([]byte, error) {
	if ref := s["$ref"]; ref != nil {
		var name string
		json.Unmarshal(ref, &name)
		return admit(data, shapes[name[len("#/$defs/"):]])
	}
	if alternatives := s["anyOf"]; alternatives != nil {
		var options []shape
		json.Unmarshal(alternatives, &options)
		for _, option := range options {
			if v, err := admit(data, option); err == nil {
				return v, nil
			}
		}
		return nil, invalid()
	}
	var kind string
	json.Unmarshal(s["type"], &kind)
	data = bytes.TrimSpace(data)
	if bytes.Equal(data, []byte("null")) {
		if kind == "null" {
			return data, nil
		}
		return nil, invalid()
	}
	switch kind {
	case "object":
		var fields Ordered[json.RawMessage]
		if json.Unmarshal(data, &fields) != nil {
			return nil, invalid()
		}
		var properties map[string]shape
		json.Unmarshal(s["properties"], &properties)
		var required []string
		json.Unmarshal(s["required"], &required)
		if properties != nil {
			for _, key := range fields.Keys() {
				child, ok := properties[key]
				if !ok {
					return nil, invalid()
				}
				v, err := admit(fields.Value(key), child)
				if err != nil {
					return nil, err
				}
				fields.Set(key, v)
			}
			for key, child := range properties {
				if _, exists := fields.Get(key); exists {
					continue
				}
				if Has(required, key) {
					return nil, invalid()
				}
				if value, ok := child["default"]; ok {
					v, err := admit(value, child)
					if err != nil {
						return nil, err
					}
					fields.Set(key, v)
				}
			}
		} else if extra := s["additionalProperties"]; extra != nil && string(extra) != "true" {
			var child shape
			if json.Unmarshal(extra, &child) != nil {
				return nil, invalid()
			}
			for _, key := range fields.Keys() {
				v, err := admit(fields.Value(key), child)
				if err != nil {
					return nil, err
				}
				fields.Set(key, v)
			}
		}
		return json.Marshal(fields)
	case "array":
		if len(data) == 0 || data[0] != '[' {
			return nil, invalid()
		}
		var items []json.RawMessage
		if json.Unmarshal(data, &items) != nil {
			return nil, invalid()
		}
		var min int
		json.Unmarshal(s["minItems"], &min)
		if len(items) < min {
			return nil, invalid()
		}
		var child shape
		json.Unmarshal(s["items"], &child)
		var tuple []shape
		json.Unmarshal(s["prefixItems"], &tuple)
		if len(tuple) > 0 && len(items) != len(tuple) {
			return nil, invalid()
		}
		unique := string(s["uniqueItems"]) == "true"
		seen := map[string]bool{}
		normalized := []json.RawMessage{}
		for i, item := range items {
			schema := child
			if len(tuple) > 0 {
				schema = tuple[i]
			}
			v, err := admit(item, schema)
			if err != nil {
				return nil, err
			}
			if !unique || !seen[string(v)] {
				normalized = append(normalized, v)
				seen[string(v)] = true
			}
		}
		items = normalized
		return json.Marshal(items)
	case "":
		return data, nil
	case "string":
		if len(data) == 0 || data[0] != '"' {
			return nil, invalid()
		}
		var value string
		if json.Unmarshal(data, &value) != nil {
			return nil, invalid()
		}
		var min, max int
		json.Unmarshal(s["minLength"], &min)
		json.Unmarshal(s["maxLength"], &max)
		size := utf8.RuneCountInString(value)
		if size < min || (max > 0 && size > max) {
			return nil, invalid()
		}
		var pattern string
		json.Unmarshal(s["pattern"], &pattern)
		if pattern != "" {
			match, err := regexp.MatchString(pattern, value)
			if err != nil || !match {
				return nil, invalid()
			}
		}
		var format string
		json.Unmarshal(s["format"], &format)
		if format == "date-time" {
			at, err := time.Parse(time.RFC3339Nano, value)
			if err != nil {
				return nil, invalid()
			}
			_, offset := at.Zone()
			if offset != 0 {
				return nil, invalid()
			}
			data, _ = json.Marshal(at.UTC().Truncate(time.Microsecond).Format(time.RFC3339Nano))
		}
	case "integer":
		var n int64
		if json.Unmarshal(data, &n) != nil {
			return nil, invalid()
		}
		for _, constraint := range []string{"minimum", "exclusiveMinimum", "maximum", "exclusiveMaximum"} {
			if limit, ok := s[constraint]; ok {
				var bound int64
				if json.Unmarshal(limit, &bound) != nil {
					return nil, invalid()
				}
				if (constraint == "minimum" && n < bound) || (constraint == "exclusiveMinimum" && n <= bound) || (constraint == "maximum" && n > bound) || (constraint == "exclusiveMaximum" && n >= bound) {
					return nil, invalid()
				}
			}
		}
	case "boolean":
		var b bool
		if json.Unmarshal(data, &b) != nil {
			return nil, invalid()
		}
	case "null":
		return nil, invalid()
	default:
		return nil, invalid()
	}
	if constant := s["const"]; constant != nil {
		var a, b any
		json.Unmarshal(data, &a)
		json.Unmarshal(constant, &b)
		if !reflect.DeepEqual(a, b) {
			return nil, invalid()
		}
	}
	if enumeration := s["enum"]; enumeration != nil {
		var value any
		var values []any
		json.Unmarshal(data, &value)
		json.Unmarshal(enumeration, &values)
		found := false
		for _, v := range values {
			found = found || reflect.DeepEqual(value, v)
		}
		if !found {
			return nil, invalid()
		}
	}
	if string(s["x-decimal"]) == "true" || s["ge"] != nil || s["le"] != nil || s["gt"] != nil || s["lt"] != nil {
		if bytes.Equal(data, []byte("null")) {
			return data, nil
		}
		var text string
		if json.Unmarshal(data, &text) == nil {
			value, err := Parse(text)
			if err != nil {
				return nil, invalid()
			}
			for _, c := range []string{"ge", "le", "gt", "lt"} {
				if raw, ok := s[c]; ok {
					var bound string
					if json.Unmarshal(raw, &bound) != nil {
						bound = string(raw)
					}
					d, err := Parse(bound)
					if err != nil {
						return nil, invalid()
					}
					cmp := value.Cmp(d)
					if c == "ge" && cmp < 0 || c == "gt" && cmp <= 0 || c == "le" && cmp > 0 || c == "lt" && cmp >= 0 {
						return nil, invalid()
					}
				}
			}
		}
	}
	return data, nil
}
