package domain

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
)

// Ordered preserves receipt/insertion order, which determines queue priority and
// the original 28-digit summation order. Updating an entry never moves it.
type Ordered[T any] struct {
	keys   []string
	values map[string]T
}

func (o Ordered[T]) validationChildren() []any {
	children := make([]any, 0, len(o.keys))
	for _, key := range o.keys {
		children = append(children, o.values[key])
	}
	return children
}

func (o Ordered[T]) comparisonEntries() map[string]any {
	entries := make(map[string]any, len(o.keys))
	for _, key := range o.keys {
		entries[key] = o.values[key]
	}
	return entries
}

func (o *Ordered[T]) Set(key string, value T) {
	if o.values == nil {
		o.values = make(map[string]T)
	}
	if _, ok := o.values[key]; !ok {
		o.keys = append(o.keys, key)
	}
	o.values[key] = value
}
func (o Ordered[T]) Get(key string) (T, bool) { value, ok := o.values[key]; return value, ok }
func (o Ordered[T]) Value(key string) T       { return o.values[key] }
func (o Ordered[T]) Len() int                 { return len(o.keys) }
func (o Ordered[T]) Keys() []string           { return append([]string(nil), o.keys...) }
func (o Ordered[T]) Values() []T {
	result := make([]T, 0, len(o.keys))
	for _, k := range o.keys {
		result = append(result, o.values[k])
	}
	return result
}
func (o *Ordered[T]) Delete(key string) {
	if _, ok := o.values[key]; !ok {
		return
	}
	delete(o.values, key)
	for i, k := range o.keys {
		if k == key {
			o.keys = append(o.keys[:i], o.keys[i+1:]...)
			return
		}
	}
}
func (o Ordered[T]) MarshalJSON() ([]byte, error) {
	var out bytes.Buffer
	out.WriteByte('{')
	for i, k := range o.keys {
		if i > 0 {
			out.WriteByte(',')
		}
		key, _ := json.Marshal(k)
		value, err := json.Marshal(o.values[k])
		if err != nil {
			return nil, err
		}
		out.Write(key)
		out.WriteByte(':')
		out.Write(value)
	}
	out.WriteByte('}')
	return out.Bytes(), nil
}
func (o *Ordered[T]) UnmarshalJSON(data []byte) error {
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	token, err := decoder.Token()
	if err != nil || token != json.Delim('{') {
		return errors.New("OBJECT_REQUIRED")
	}
	var result Ordered[T]
	for decoder.More() {
		token, err = decoder.Token()
		if err != nil {
			return errors.New("INVALID_OBJECT")
		}
		key, ok := token.(string)
		if !ok {
			return errors.New("INVALID_OBJECT")
		}
		if _, exists := result.Get(key); exists {
			return errors.New("DUPLICATE_OBJECT_KEY")
		}
		var value T
		if err := decoder.Decode(&value); err != nil {
			return err
		}
		result.Set(key, value)
	}
	if _, err := decoder.Token(); err != nil {
		return err
	}
	if _, err := decoder.Token(); err != io.EOF {
		return errors.New("SINGLE_OBJECT_REQUIRED")
	}
	*o = result
	return nil
}
