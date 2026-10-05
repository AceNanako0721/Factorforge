// Package decimal provides the trading layer's immutable, finite decimal value.
// Rounding belongs to an operation-local Math, never a mutable global context.
package decimal

import (
	"encoding/json"
	"errors"
	"strings"

	"github.com/cockroachdb/apd/v3"
)

var ErrValue = errors.New("DECIMAL_STRING_REQUIRED")
var ErrArithmetic = errors.New("DECIMAL_ARITHMETIC_FAILED")

// Value's zero value is decimal zero. Its coefficient cannot be mutated by users.
type Value struct {
	value            *apd.Decimal
	extendedExponent *int64
}

func Parse(text string) (Value, error) {
	// Python Decimal accepts surrounding whitespace and digit separators.
	text = strings.ReplaceAll(strings.TrimSpace(text), "_", "")
	return parseFinite(text)
}

func (v Value) raw() *apd.Decimal {
	if v.value == nil {
		return apd.New(0, 0)
	}
	return v.value
}
func (v Value) String() string      { return valueString(v) }
func (v Value) Sign() int           { return v.raw().Sign() }
func (v Value) Cmp(other Value) int { return compare(v, other) }
func (v Value) Abs() Value {
	return Value{value: new(apd.Decimal).Abs(v.raw()), extendedExponent: v.extendedExponent}
}
func (v Value) Neg() Value {
	return Value{value: new(apd.Decimal).Neg(v.raw()), extendedExponent: v.extendedExponent}
}
func (v Value) MarshalJSON() ([]byte, error) { return json.Marshal(v.String()) }
func (v *Value) UnmarshalJSON(data []byte) error {
	// json.Unmarshal(null, &string) succeeds; explicitly exclude null and numbers.
	if len(data) == 0 || data[0] != '"' {
		return ErrValue
	}
	var text string
	if json.Unmarshal(data, &text) != nil {
		return ErrValue
	}
	parsed, err := Parse(text)
	if err != nil {
		return err
	}
	*v = parsed
	return nil
}

func Min(values ...Value) Value {
	if len(values) == 0 {
		return Value{}
	}
	result := values[0]
	for _, v := range values[1:] {
		if v.Cmp(result) < 0 {
			result = v
		}
	}
	return result
}
func Max(values ...Value) Value {
	if len(values) == 0 {
		return Value{}
	}
	result := values[0]
	for _, v := range values[1:] {
		if v.Cmp(result) > 0 {
			result = v
		}
	}
	return result
}

// Math records the first error. Call Err before committing any computed state.
// An instance belongs to one calculation; it must not be shared by goroutines.
type Math struct {
	context apd.Context
	err     error
}

func NewMath(precision uint32) *Math {
	context := apd.BaseContext.WithPrecision(precision)
	context.Rounding = apd.RoundHalfEven
	context.Traps = apd.DivisionByZero | apd.DivisionUndefined | apd.InvalidOperation | apd.Overflow
	context.MaxExponent = 999999
	context.MinExponent = -999999
	return &Math{context: *context}
}
func (m *Math) Err() error { return m.err }
func (m *Math) result(d *apd.Decimal, err error) Value {
	if err != nil || d.Form != apd.Finite {
		m.err = ErrArithmetic
		return Value{}
	}
	return Value{value: d}
}
func (m *Math) binary(a, b Value, op func(*apd.Decimal, *apd.Decimal, *apd.Decimal) (apd.Condition, error)) Value {
	if m.err != nil {
		return Value{}
	}
	d := new(apd.Decimal)
	_, err := op(d, a.raw(), b.raw())
	return m.result(d, err)
}
func (m *Math) Add(a, b Value) Value {
	if a.wide() || b.wide() {
		return m.wideBinary(a, b, "add")
	}
	return m.binary(a, b, m.context.Add)
}
func (m *Math) Sub(a, b Value) Value {
	if a.wide() || b.wide() {
		return m.wideBinary(a, b, "sub")
	}
	return m.binary(a, b, m.context.Sub)
}
func (m *Math) Mul(a, b Value) Value {
	if a.wide() || b.wide() {
		return m.wideBinary(a, b, "mul")
	}
	return m.binary(a, b, m.context.Mul)
}
func (m *Math) Div(a, b Value) Value {
	if m.err != nil {
		return Value{}
	}
	if a.wide() || b.wide() {
		return m.wideBinary(a, b, "div")
	}
	d := new(apd.Decimal)
	conditions, err := m.context.Quo(d, a.raw(), b.raw())
	if err == nil && !conditions.Inexact() {
		preferred := a.raw().Exponent - b.raw().Exponent
		ten := apd.NewBigInt(10)
		remainder := new(apd.BigInt)
		for d.Exponent < preferred && d.Coeff.Sign() != 0 {
			coefficient := new(apd.BigInt)
			coefficient.QuoRem(&d.Coeff, ten, remainder)
			if remainder.Sign() != 0 {
				break
			}
			d.Coeff.Set(coefficient)
			d.Exponent++
		}
	}
	return m.result(d, err)
}
func (m *Math) Sum(values ...Value) Value {
	result := Value{}
	for _, v := range values {
		result = m.Add(result, v)
	}
	return result
}
func (m *Math) Ln(v Value) Value {
	if v.wide() {
		return m.wideLn(v)
	}
	if m.err != nil {
		return Value{}
	}
	d := new(apd.Decimal)
	_, err := m.context.Ln(d, v.raw())
	return m.result(d, err)
}
func (m *Math) Abs(v Value) Value {
	if v.wide() {
		return m.finish(v.coefficient(), v.exponent(), false)
	}
	if m.err != nil {
		return Value{}
	}
	d := new(apd.Decimal)
	_, err := m.context.Abs(d, v.raw())
	return m.result(d, err)
}
func (m *Math) Neg(v Value) Value {
	if v.wide() {
		return m.finish(v.coefficient(), v.exponent(), !v.raw().Negative)
	}
	if m.err != nil {
		return Value{}
	}
	d := new(apd.Decimal)
	_, err := m.context.Neg(d, v.raw())
	return m.result(d, err)
}
func (m *Math) Sqrt(v Value) Value {
	if v.wide() {
		return m.wideSqrt(v)
	}
	if m.err != nil {
		return Value{}
	}
	d := new(apd.Decimal)
	_, err := m.context.Sqrt(d, v.raw())
	return m.result(d, err)
}

// Exp rounds an extended-precision result back to the original Python context.
// Two work precisions must agree after rounding; uncertainty fails the calculation.
func (m *Math) Exp(v Value) Value {
	limit, _ := Parse("500")
	if v.wide() || v.Abs().Cmp(limit) > 0 {
		return m.rangeExp(v)
	}
	if m.err != nil {
		return Value{}
	}
	var rounded [2]*apd.Decimal
	for i, factor := range []uint32{2, 3} {
		work := m.context.WithPrecision(m.context.Precision * factor)
		d := new(apd.Decimal)
		if _, err := work.Exp(d, v.raw()); err != nil {
			m.err = ErrArithmetic
			return Value{}
		}
		rounded[i] = new(apd.Decimal)
		if _, err := m.context.Round(rounded[i], d); err != nil {
			m.err = ErrArithmetic
			return Value{}
		}
	}
	if rounded[0].Cmp(rounded[1]) != 0 {
		m.err = ErrArithmetic
		return Value{}
	}
	return Value{value: rounded[0]}
}

type Rounding string

const (
	TowardZero Rounding = "down"
	Ceiling    Rounding = "ceiling"
	Floor      Rounding = "floor"
)

func (m *Math) Integral(v Value, mode Rounding) Value {
	if v.wide() {
		return m.wideIntegral(v, mode)
	}
	if m.err != nil {
		return Value{}
	}
	if v.raw().Exponent >= 0 {
		return Value{value: new(apd.Decimal).Set(v.raw()), extendedExponent: v.extendedExponent}
	}
	context := m.context
	switch mode {
	case TowardZero:
		context.Rounding = apd.RoundDown
	case Ceiling:
		context.Rounding = apd.RoundCeiling
	case Floor:
		context.Rounding = apd.RoundFloor
	default:
		m.err = ErrArithmetic
		return Value{}
	}
	d := new(apd.Decimal)
	_, err := context.RoundToIntegralValue(d, v.raw())
	return m.result(d, err)
}

// Step preserves the original division -> integral rounding -> multiplication.
func (m *Math) Step(v, step Value, mode Rounding) Value {
	if step.Sign() <= 0 {
		m.err = ErrArithmetic
		return Value{}
	}
	return m.Mul(m.Integral(m.Div(v, step), mode), step)
}
