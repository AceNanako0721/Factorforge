package domain

import (
	"bytes"
	"encoding/json"
	"reflect"
	"time"

	"github.com/AceNanako0721/Factorforge/src/factorforge/trading/domain/decimal"
)

var zero decimal.Value
var one, _ = decimal.Parse("1")
var two, _ = decimal.Parse("2")
var tenThousand, _ = decimal.Parse("10000")

// Engine requires a validated snapshot held under its account transaction lock.
// Use Mutate for rollback; query callers must validate before constructing it.
// Calculations use the original 28-digit
// half-even context; every arithmetic error prevents committing the snapshot.
type Engine struct {
	Run  *Aggregate
	Math *decimal.Math
	err  error
}

func NewEngine(run *Aggregate) *Engine { return &Engine{Run: run, Math: decimal.NewMath(28)} }
func (e *Engine) Err() error {
	if e.err != nil {
		return e.err
	}
	return e.Math.Err()
}
func (e *Engine) Fail(code string, status ...int) {
	if e.Err() == nil {
		s := 422
		if len(status) > 0 {
			s = status[0]
		}
		e.err = &Error{code, s}
	}
}
func (e *Engine) Remaining(order *Order) decimal.Value {
	return decimal.Max(zero, e.Math.Sub(order.Request.Quantity, order.FilledQuantity))
}
func Terminal(state string) bool {
	return state == "FILLED" || state == "CANCELED" || state == "REJECTED"
}
func Has(values []string, value string) bool {
	for _, v := range values {
		if v == value {
			return true
		}
	}
	return false
}
func (k InstrumentKey) Code() string { return k.Venue + ":" + k.Product + ":" + k.InstrumentID }
func PythonTime(at time.Time) string {
	at = at.UTC()
	format := "2006-01-02T15:04:05"
	if at.Nanosecond() != 0 {
		format += ".000000"
	}
	return at.Format(format) + "+00:00"
}
func Alert(code string, at time.Time, details ...string) Object {
	result := Object{}
	result["code"], _ = json.Marshal(code)
	result["at"], _ = json.Marshal(PythonTime(at))
	for i := 0; i+1 < len(details); i += 2 {
		result[details[i]], _ = json.Marshal(details[i+1])
	}
	return result
}

// Clone round-trips typed state without float conversion and preserves ordered
// facts. Store adapters must perform this operation under their account lock.
func (run *Aggregate) Clone() (*Aggregate, error) {
	data, err := json.Marshal(run)
	if err != nil {
		return nil, err
	}
	var copy Aggregate
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err = decoder.Decode(&copy); err != nil {
		return nil, err
	}
	return &copy, nil
}
func Mutate(run *Aggregate, action func(*Engine)) error {
	if err := Validate(run); err != nil {
		return err
	}
	copy, err := run.Clone()
	if err != nil {
		return &Error{Code: "SNAPSHOT_INVALID", Status: 422}
	}
	engine := NewEngine(copy)
	action(engine)
	if err = engine.Err(); err != nil {
		return err
	}
	if err = Validate(copy); err != nil {
		return err
	}
	*run = *copy
	return nil
}

func (e *Engine) validate(value any) bool {
	if e.Err() != nil {
		return false
	}
	if err := Validate(value); err != nil {
		e.err = err
		return false
	}
	return true
}
func sameJSON(a, b any) bool {
	return sameValue(reflect.ValueOf(a), reflect.ValueOf(b))
}

// Immutable fact equality follows typed values, not JSON spelling: decimal
// scales, UTC offset spelling, sets and object key order are not new facts.
func sameValue(a, b reflect.Value) bool {
	if !a.IsValid() || !b.IsValid() {
		return a.IsValid() == b.IsValid()
	}
	if a.Type() != b.Type() {
		return false
	}
	if a.Kind() == reflect.Pointer {
		if a.IsNil() || b.IsNil() {
			return a.IsNil() == b.IsNil()
		}
		return sameValue(a.Elem(), b.Elem())
	}
	if a.Type() == decimalType {
		return a.Interface().(decimal.Value).Cmp(b.Interface().(decimal.Value)) == 0
	}
	if a.Type() == timeType {
		return a.Interface().(time.Time).Equal(b.Interface().(time.Time))
	}
	if left, ok := a.Interface().(interface{ comparisonEntries() map[string]any }); ok {
		x := left.comparisonEntries()
		y := b.Interface().(interface{ comparisonEntries() map[string]any }).comparisonEntries()
		if len(x) != len(y) {
			return false
		}
		for k, v := range x {
			other, ok := y[k]
			if !ok || !sameValue(reflect.ValueOf(v), reflect.ValueOf(other)) {
				return false
			}
		}
		return true
	}
	switch a.Kind() {
	case reflect.Struct:
		for i := 0; i < a.NumField(); i++ {
			field := a.Type().Field(i)
			if field.Name == "Capabilities" || field.Name == "PriceRoles" || field.Name == "Permissions" {
				x := a.Field(i).Interface().([]string)
				y := b.Field(i).Interface().([]string)
				for _, v := range x {
					if !Has(y, v) {
						return false
					}
				}
				for _, v := range y {
					if !Has(x, v) {
						return false
					}
				}
				continue
			}
			if !sameValue(a.Field(i), b.Field(i)) {
				return false
			}
		}
		return true
	case reflect.Slice, reflect.Array:
		if a.Len() != b.Len() {
			return false
		}
		for i := 0; i < a.Len(); i++ {
			if !sameValue(a.Index(i), b.Index(i)) {
				return false
			}
		}
		return true
	default:
		return reflect.DeepEqual(a.Interface(), b.Interface())
	}
}
func sameFill(a, b Fill) bool {
	a.ReceivedAt = b.ReceivedAt
	// Decimal equality follows Python Decimal, including equivalent scales.
	if a.Quantity.Cmp(b.Quantity) != 0 || a.Price.Cmp(b.Price) != 0 || a.Fee.Cmp(b.Fee) != 0 {
		return false
	}
	a.Quantity = b.Quantity
	a.Price = b.Price
	a.Fee = b.Fee
	return sameJSON(a, b)
}
