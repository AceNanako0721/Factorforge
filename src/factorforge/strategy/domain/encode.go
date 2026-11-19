package domain

import (
	"encoding/json"
	"reflect"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"
)

// The encoder below writes json.Marshal(JSONValue(v)) directly, byte for byte,
// without building the intermediate maps and interface values: the whole
// aggregate is encoded on every store write, and the map-of-any detour was
// most of that cost. JSONValue stays the reference definition; any change to
// it must be mirrored here (TestEncodeMatchesJSONValue compares the two).

var (
	timeType     = reflect.TypeOf(time.Time{})
	decimalType  = reflect.TypeOf(Decimal{})
	exposureType = reflect.TypeOf(ExposureRisk{})
	stringType   = reflect.TypeOf("")
	boolType     = reflect.TypeOf(false)
	intType      = reflect.TypeOf(0)
	int64Type    = reflect.TypeOf(int64(0))
	int32Type    = reflect.TypeOf(int32(0))
	comparable   = reflect.TypeOf((*interface{ comparisonEntries() map[string]any })(nil)).Elem()
	uniqueKeys   = []string{"scopes", "capabilities", "object_ids", "fill_ids", "income_ids", "consumed_evidence", "source_decisions", "calibrations", "rubrics", "verification_manifests"}
)

// EncodeFields encodes each top-level key of value's JSONValue form, except
// the skipped keys, which are never encoded.
func EncodeFields(value any, skip ...string) (map[string][]byte, error) {
	v := reflect.ValueOf(value)
	for v.IsValid() && (v.Kind() == reflect.Interface || v.Kind() == reflect.Pointer) && !v.IsNil() {
		v = v.Elem()
	}
	out := map[string][]byte{}
	if !v.IsValid() || v.Kind() != reflect.Struct || special(v) {
		return out, nil
	}
	fields, err := structFields(v)
	if err != nil {
		return nil, err
	}
	for _, f := range fields {
		if Has(skip, f.key) {
			continue
		}
		b, err := f.encode(nil)
		if err != nil {
			return nil, err
		}
		out[f.key] = b
	}
	return out, nil
}

// EncodeObject writes encoded fields as one JSON object with sorted keys, as
// json.Marshal does for a map.
func EncodeObject(fields map[string][]byte) []byte {
	keys := make([]string, 0, len(fields))
	size := 2
	for k, v := range fields {
		keys = append(keys, k)
		size += len(k) + len(v) + 4
	}
	sort.Strings(keys)
	b := make([]byte, 0, size)
	b = append(b, '{')
	for i, k := range keys {
		if i > 0 {
			b = append(b, ',')
		}
		b = appendString(b, k)
		b = append(b, ':')
		b = append(b, fields[k]...)
	}
	return append(b, '}')
}

type field struct {
	key   string
	value reflect.Value
	plain any
	raw   bool // plain holds an already-converted JSONValue result
}

func (f field) encode(b []byte) ([]byte, error) {
	if f.raw {
		return appendAny(b, f.plain)
	}
	return appendValue(b, f.value)
}

func special(v reflect.Value) bool {
	t := v.Type()
	return t == timeType || t == decimalType || t == exposureType || t.Implements(comparable)
}

// structFields lists the keys JSONValue gives a struct, later fields
// overriding earlier ones as its map assignments do.
func structFields(v reflect.Value) ([]field, error) {
	t := v.Type()
	index := map[string]int{}
	fields := []field{}
	put := func(f field) {
		if i, ok := index[f.key]; ok {
			fields[i] = f
			return
		}
		index[f.key] = len(fields)
		fields = append(fields, f)
	}
	for i := 0; i < v.NumField(); i++ {
		f := t.Field(i)
		if !f.IsExported() {
			continue
		}
		tag := f.Tag.Get("json")
		if (strings.Contains(tag, ",omitempty") || strings.Contains(tag, ",omitzero")) && v.Field(i).IsZero() {
			continue
		}
		key := strings.Split(tag, ",")[0]
		if key == "-" {
			continue
		}
		if f.Anonymous {
			for k, x := range Object(jsonValue(v.Field(i))) {
				put(field{key: k, plain: x, raw: true})
			}
			continue
		}
		if key == "" {
			key = f.Name
		}
		if Has(uniqueKeys, key) {
			value := jsonValue(v.Field(i))
			if values, ok := value.([]any); ok {
				text := []string{}
				for _, item := range values {
					text = append(text, Text(item))
				}
				value = JSONValue(Unique(text))
			}
			put(field{key: key, plain: value, raw: true})
			continue
		}
		put(field{key: key, value: v.Field(i)})
	}
	return fields, nil
}

func appendValue(b []byte, v reflect.Value) ([]byte, error) {
	if !v.IsValid() {
		return append(b, "null"...), nil
	}
	if v.Kind() == reflect.Interface || v.Kind() == reflect.Pointer {
		if v.IsNil() {
			return append(b, "null"...), nil
		}
		return appendValue(b, v.Elem())
	}
	switch t := v.Type(); {
	case t == timeType:
		return appendString(b, strings.TrimSuffix(ISO(v.Interface().(time.Time)), "+00:00")+"Z"), nil
	case t == decimalType:
		return appendString(b, v.Interface().(Decimal).String()), nil
	case t == exposureType:
		e := v.Interface().(ExposureRisk)
		b = append(b, '[')
		b = appendString(b, e.Value.String())
		b = append(b, ',')
		b = appendString(b, e.Stress.String())
		b = append(b, ',')
		b = appendString(b, e.Group)
		return append(b, ']'), nil
	case t.Implements(comparable):
		return appendValue(b, reflect.ValueOf(v.Interface().(interface{ comparisonEntries() map[string]any }).comparisonEntries()))
	}
	switch v.Kind() {
	case reflect.Struct:
		fields, err := structFields(v)
		if err != nil {
			return nil, err
		}
		sort.Slice(fields, func(i, j int) bool { return fields[i].key < fields[j].key })
		b = append(b, '{')
		for i, f := range fields {
			if i > 0 {
				b = append(b, ',')
			}
			b = appendString(b, f.key)
			b = append(b, ':')
			if b, err = f.encode(b); err != nil {
				return nil, err
			}
		}
		return append(b, '}'), nil
	case reflect.Slice, reflect.Array:
		var err error
		b = append(b, '[')
		for i := 0; i < v.Len(); i++ {
			if i > 0 {
				b = append(b, ',')
			}
			if b, err = appendValue(b, v.Index(i)); err != nil {
				return nil, err
			}
		}
		return append(b, ']'), nil
	case reflect.Map:
		type entry struct {
			key   string
			value reflect.Value
		}
		entries := make([]entry, 0, v.Len())
		seen := map[string]int{}
		it := v.MapRange()
		for it.Next() {
			key := Text(it.Key().Interface())
			if i, ok := seen[key]; ok {
				entries[i].value = it.Value()
				continue
			}
			seen[key] = len(entries)
			entries = append(entries, entry{key, it.Value()})
		}
		sort.Slice(entries, func(i, j int) bool { return entries[i].key < entries[j].key })
		var err error
		b = append(b, '{')
		for i, e := range entries {
			if i > 0 {
				b = append(b, ',')
			}
			b = appendString(b, e.key)
			b = append(b, ':')
			if b, err = appendValue(b, e.value); err != nil {
				return nil, err
			}
		}
		return append(b, '}'), nil
	case reflect.String:
		if v.Type() == stringType {
			return appendString(b, v.String()), nil
		}
	case reflect.Bool:
		if v.Type() == boolType {
			return strconv.AppendBool(b, v.Bool()), nil
		}
	case reflect.Int, reflect.Int64, reflect.Int32:
		if t := v.Type(); t == intType || t == int64Type || t == int32Type {
			return strconv.AppendInt(b, v.Int(), 10), nil
		}
	}
	return appendPrimitive(b, v.Interface())
}

// appendAny encodes a value JSONValue already produced (maps, slices and
// primitives only).
func appendAny(b []byte, value any) ([]byte, error) {
	switch x := value.(type) {
	case nil:
		return append(b, "null"...), nil
	case string:
		return appendString(b, x), nil
	case bool:
		return strconv.AppendBool(b, x), nil
	case []any:
		var err error
		b = append(b, '[')
		for i, item := range x {
			if i > 0 {
				b = append(b, ',')
			}
			if b, err = appendAny(b, item); err != nil {
				return nil, err
			}
		}
		return append(b, ']'), nil
	case map[string]any:
		keys := make([]string, 0, len(x))
		for k := range x {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		var err error
		b = append(b, '{')
		for i, k := range keys {
			if i > 0 {
				b = append(b, ',')
			}
			b = appendString(b, k)
			b = append(b, ':')
			if b, err = appendAny(b, x[k]); err != nil {
				return nil, err
			}
		}
		return append(b, '}'), nil
	}
	return appendPrimitive(b, value)
}

func appendPrimitive(b []byte, value any) ([]byte, error) {
	data, err := json.Marshal(value)
	if err != nil {
		return nil, err
	}
	return append(b, data...), nil
}

// appendString quotes s as encoding/json does (HTML-safe escaping); strings
// that need any escaping go through encoding/json itself.
func appendString(b []byte, s string) []byte {
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c < 0x20 || c >= utf8.RuneSelf || c == '"' || c == '\\' || c == '<' || c == '>' || c == '&' {
			data, _ := json.Marshal(s)
			return append(b, data...)
		}
	}
	b = append(b, '"')
	b = append(b, s...)
	return append(b, '"')
}
