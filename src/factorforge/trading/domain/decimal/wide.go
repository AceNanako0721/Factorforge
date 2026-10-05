package decimal

import (
	"github.com/cockroachdb/apd/v3"
	"math/big"
	"regexp"
	"strconv"
	"strings"
	"unicode"
)

// apd has an internal +/-100000 exponent bound. Scale-independent coefficient
// operations bridge it to the frozen Decimal context (+/-999999), including
// half-even subnormal rounding. This does not narrow the trading contract.
const pythonMaxExponent = int64(999999999999999999)

var finitePattern = regexp.MustCompile(`^([+-]?)([0-9]*)(?:\.([0-9]*))?(?:[eE]([+-]?[0-9]+))?$`)

func parseFinite(input string) (Value, error) {
	input = strings.Map(func(r rune) rune {
		if r < '0' || r > '9' {
			for _, row := range unicode.Digit.R16 {
				if uint32(r) >= uint32(row.Lo) && uint32(r) <= uint32(row.Hi) && (uint32(r)-uint32(row.Lo))%uint32(row.Stride) == 0 {
					return '0' + rune((uint32(r)-uint32(row.Lo))/uint32(row.Stride)%10)
				}
			}
			for _, row := range unicode.Digit.R32 {
				if uint32(r) >= row.Lo && uint32(r) <= row.Hi && (uint32(r)-row.Lo)%row.Stride == 0 {
					return '0' + rune((uint32(r)-row.Lo)/row.Stride%10)
				}
			}
		}
		return r
	}, input)
	match := finitePattern.FindStringSubmatch(input)
	if match == nil || match[2]+match[3] == "" {
		return Value{}, ErrValue
	}
	exp := int64(0)
	if match[4] != "" {
		var err error
		exp, err = strconv.ParseInt(match[4], 10, 64)
		if err != nil {
			return Value{}, ErrValue
		}
	}
	exp -= int64(len(match[3]))
	if exp > pythonMaxExponent || exp < -pythonMaxExponent {
		return Value{}, ErrValue
	}
	coefficient := new(big.Int)
	if _, ok := coefficient.SetString(match[2]+match[3], 10); !ok {
		return Value{}, ErrValue
	}
	return fromCoefficient(coefficient, exp, match[1] == "-"), nil
}
func fromCoefficient(c *big.Int, exp int64, negative bool) Value {
	d := new(apd.Decimal)
	d.Coeff.SetMathBigInt(c)
	d.Negative = negative
	if exp >= -2147483648 && exp <= 2147483647 {
		d.Exponent = int32(exp)
		return Value{value: d}
	}
	e := exp
	return Value{value: d, extendedExponent: &e}
}
func (v Value) exponent() int64 {
	if v.extendedExponent != nil {
		return *v.extendedExponent
	}
	return int64(v.raw().Exponent)
}
func (v Value) coefficient() *big.Int { return new(big.Int).Set(v.raw().Coeff.MathBigInt()) }
func (v Value) adjusted() int64       { return v.exponent() + int64(len(v.raw().Coeff.String())) - 1 }
func (v Value) wide() bool {
	return v.exponent() > 90000 || v.exponent() < -90000 || v.adjusted() > 90000 || v.adjusted() < -90000
}
func valueString(v Value) string {
	digits := v.raw().Coeff.String()
	exp := v.exponent()
	adjusted := exp + int64(len(digits)) - 1
	sign := ""
	if v.raw().Negative {
		sign = "-"
	}
	if exp == 0 {
		return sign + digits
	}
	if exp < 0 && adjusted >= -6 {
		point := int64(len(digits)) + exp
		if point > 0 {
			return sign + digits[:int(point)] + "." + digits[int(point):]
		}
		return sign + "0." + strings.Repeat("0", int(-point)) + digits
	}
	mantissa := digits
	if len(digits) > 1 {
		mantissa = digits[:1] + "." + digits[1:]
	}
	suffix := strconv.FormatInt(adjusted, 10)
	if adjusted >= 0 {
		suffix = "+" + suffix
	}
	return sign + mantissa + "E" + suffix
}
func compare(a, b Value) int {
	as, bs := a.Sign(), b.Sign()
	if as < bs {
		return -1
	}
	if as > bs {
		return 1
	}
	if as == 0 {
		return 0
	}
	left, right := a.adjusted(), b.adjusted()
	cmp := 0
	if left < right {
		cmp = -1
	} else if left > right {
		cmp = 1
	} else {
		x, y := a.raw().Coeff.String(), b.raw().Coeff.String()
		n := len(x)
		if len(y) > n {
			n = len(y)
		}
		for i := 0; i < n; i++ {
			p, q := byte('0'), byte('0')
			if i < len(x) {
				p = x[i]
			}
			if i < len(y) {
				q = y[i]
			}
			if p < q {
				cmp = -1
				break
			}
			if p > q {
				cmp = 1
				break
			}
		}
	}
	if as < 0 {
		cmp = -cmp
	}
	return cmp
}
func power10(n int64) *big.Int { return new(big.Int).Exp(big.NewInt(10), big.NewInt(n), nil) }
func roundedCoefficient(c *big.Int, cut int64) (*big.Int, bool) {
	if cut <= 0 {
		return new(big.Int).Set(c), false
	}
	if cut > int64(len(c.String())) {
		return new(big.Int), c.Sign() != 0
	}
	denominator := power10(cut)
	q, r := new(big.Int), new(big.Int)
	q.QuoRem(c, denominator, r)
	twice := new(big.Int).Lsh(r, 1)
	cmp := twice.Cmp(denominator)
	if cmp > 0 || (cmp == 0 && q.Bit(0) == 1) {
		q.Add(q, big.NewInt(1))
	}
	return q, r.Sign() != 0
}
func (m *Math) finish(c *big.Int, exp int64, negative bool) Value {
	if m.err != nil {
		return Value{}
	}
	precision := int64(m.context.Precision)
	if precision <= 0 {
		m.err = ErrArithmetic
		return Value{}
	}
	etiny := int64(-999999) - precision + 1
	digits := int64(len(c.String()))
	cut := digits - precision
	if cut < 0 {
		cut = 0
	}
	if etiny-exp > cut {
		cut = etiny - exp
	}
	if cut > 0 {
		c, _ = roundedCoefficient(c, cut)
		exp += cut
	}
	if c.Sign() != 0 && int64(len(c.String())) > precision {
		c.Quo(c, big.NewInt(10))
		exp++
	}
	adjusted := exp + int64(len(c.String())) - 1
	if c.Sign() != 0 && adjusted > 999999 {
		m.err = ErrArithmetic
		return Value{}
	}
	if c.Sign() == 0 {
		if exp > 999999 {
			exp = 999999
		}
		if exp < etiny {
			exp = etiny
		}
	}
	return fromCoefficient(c, exp, negative)
}
func (m *Math) wideBinary(a, b Value, operation string) Value {
	if m.err != nil {
		return Value{}
	}
	negative := a.raw().Negative != b.raw().Negative
	switch operation {
	case "mul":
		return m.finish(new(big.Int).Mul(a.coefficient(), b.coefficient()), a.exponent()+b.exponent(), negative)
	case "div":
		if b.Sign() == 0 {
			m.err = ErrArithmetic
			return Value{}
		}
		if a.Sign() == 0 {
			return m.finish(new(big.Int), a.exponent()-b.exponent(), negative)
		}
		scale := int64(m.context.Precision) + int64(len(b.raw().Coeff.String())) - int64(len(a.raw().Coeff.String())) + 3
		if scale < 0 {
			scale = 0
		}
		numerator := new(big.Int).Mul(a.coefficient(), power10(scale))
		q, r := new(big.Int), new(big.Int)
		q.QuoRem(numerator, b.coefficient(), r)
		exp := a.exponent() - b.exponent() - scale
		if r.Sign() != 0 {
			if new(big.Int).Mod(q, big.NewInt(10)).Sign() == 0 {
				q.Add(q, big.NewInt(1))
			}
		} else {
			preferred := a.exponent() - b.exponent()
			for exp < preferred && q.Sign() != 0 {
				next, remainder := new(big.Int), new(big.Int)
				next.QuoRem(q, big.NewInt(10), remainder)
				if remainder.Sign() != 0 {
					break
				}
				q = next
				exp++
			}
		}
		return m.finish(q, exp, negative)
	default:
		exp := a.exponent()
		if b.exponent() < exp {
			exp = b.exponent()
		}
		maxAdjusted := a.adjusted()
		if a.Sign() == 0 {
			maxAdjusted = b.adjusted()
		}
		if b.Sign() != 0 && b.adjusted() > maxAdjusted {
			maxAdjusted = b.adjusted()
		}
		guard := maxAdjusted - int64(m.context.Precision) - 3
		if exp < guard {
			exp = guard
		}
		scaled := func(v Value) *big.Int {
			c := v.coefficient()
			if c.Sign() == 0 {
				return c
			}
			shift := v.exponent() - exp
			if shift >= 0 {
				c.Mul(c, power10(shift))
			} else if -shift > int64(len(c.String())) {
				if c.Sign() != 0 {
					c.SetInt64(1)
				}
			} else {
				q, r := new(big.Int), new(big.Int)
				q.QuoRem(c, power10(-shift), r)
				c = q
				if r.Sign() != 0 && new(big.Int).Mod(c, big.NewInt(10)).Sign() == 0 {
					c.Add(c, big.NewInt(1))
				}
			}
			if v.raw().Negative {
				c.Neg(c)
			}
			return c
		}
		left, right := scaled(a), scaled(b)
		if operation == "sub" {
			right.Neg(right)
		}
		sum := new(big.Int).Add(left, right)
		neg := sum.Sign() < 0
		if sum.Sign() == 0 {
			neg = a.raw().Negative && ((operation == "add" && b.raw().Negative) || (operation == "sub" && !b.raw().Negative))
		}
		sum.Abs(sum)
		return m.finish(sum, exp, neg)
	}
}
func (m *Math) wideIntegral(v Value, mode Rounding) Value {
	c := v.coefficient()
	exp := v.exponent()
	if exp >= 0 {
		return fromCoefficient(c, exp, v.raw().Negative)
	}
	q, r := new(big.Int), new(big.Int)
	if -exp > int64(len(c.String())) {
		r = c
	} else {
		q.QuoRem(c, power10(-exp), r)
	}
	if r.Sign() != 0 && ((mode == Ceiling && !v.raw().Negative) || (mode == Floor && v.raw().Negative)) {
		q.Add(q, big.NewInt(1))
	}
	if mode != TowardZero && mode != Ceiling && mode != Floor {
		m.err = ErrArithmetic
		return Value{}
	}
	return fromCoefficient(q, 0, v.raw().Negative)
}
func (m *Math) wideSqrt(v Value) Value {
	if v.Sign() < 0 {
		m.err = ErrArithmetic
		return Value{}
	}
	exp := v.exponent()
	half := exp / 2
	if exp < 0 && exp%2 != 0 {
		half--
	}
	normalized := new(apd.Decimal).Set(v.raw())
	normalized.Exponent = int32(exp - 2*half)
	work := apd.BaseContext.WithPrecision(m.context.Precision * 3)
	result := new(apd.Decimal)
	_, err := work.Sqrt(result, normalized)
	if err != nil {
		m.err = ErrArithmetic
		return Value{}
	}
	return m.finish(new(big.Int).Set(result.Coeff.MathBigInt()), int64(result.Exponent)+half, result.Negative)
}
func (m *Math) wideLn(v Value) Value {
	if v.Sign() <= 0 {
		m.err = ErrArithmetic
		return Value{}
	}
	adjusted := v.adjusted()
	normalized := new(apd.Decimal).Set(v.raw())
	normalized.Exponent = int32(1 - len(normalized.Coeff.String()))
	var values [2]Value
	for i, factor := range []uint32{3, 4} {
		work := apd.BaseContext.WithPrecision(m.context.Precision * factor)
		ln, ten := new(apd.Decimal), new(apd.Decimal)
		_, err := work.Ln(ln, normalized)
		if err == nil {
			_, err = work.Ln(ten, apd.New(10, 0))
		}
		if err == nil {
			_, err = work.Mul(ten, ten, apd.New(adjusted, 0))
		}
		if err == nil {
			_, err = work.Add(ln, ln, ten)
		}
		if err != nil {
			m.err = ErrArithmetic
			return Value{}
		}
		values[i] = m.finish(new(big.Int).Set(ln.Coeff.MathBigInt()), int64(ln.Exponent), ln.Negative)
	}
	if values[0].Cmp(values[1]) != 0 {
		m.err = ErrArithmetic
		return Value{}
	}
	return values[0]
}
func (m *Math) rangeExp(v Value) Value {
	if v.adjusted() > 7 {
		if v.Sign() < 0 {
			return m.finish(new(big.Int), -999999-int64(m.context.Precision)+1, false)
		}
		m.err = ErrArithmetic
		return Value{}
	}
	if v.adjusted() < -int64(m.context.Precision)-2 {
		return fromCoefficient(big.NewInt(1), 0, false)
	}
	var values [2]Value
	for i, factor := range []uint32{3, 4} {
		work := apd.BaseContext.WithPrecision(m.context.Precision*factor + 16)
		ln10 := new(apd.Decimal)
		_, err := work.Ln(ln10, apd.New(10, 0))
		q, n := new(apd.Decimal), new(apd.Decimal)
		if err == nil {
			_, err = work.Quo(q, v.raw(), ln10)
		}
		floor := *work
		floor.Rounding = apd.RoundFloor
		if err == nil {
			_, err = floor.RoundToIntegralValue(n, q)
		}
		exponent, e2 := n.Int64()
		if err != nil || e2 != nil {
			m.err = ErrArithmetic
			return Value{}
		}
		if exponent > 999999 {
			m.err = ErrArithmetic
			return Value{}
		}
		if exponent < -999999-int64(m.context.Precision)-2 {
			return m.finish(new(big.Int), -999999-int64(m.context.Precision)+1, false)
		}
		remainder, product := new(apd.Decimal), new(apd.Decimal)
		_, err = work.Mul(product, n, ln10)
		if err == nil {
			_, err = work.Sub(remainder, v.raw(), product)
		}
		result := new(apd.Decimal)
		if err == nil {
			_, err = work.Exp(result, remainder)
		}
		if err != nil {
			m.err = ErrArithmetic
			return Value{}
		}
		values[i] = m.finish(new(big.Int).Set(result.Coeff.MathBigInt()), int64(result.Exponent)+exponent, false)
	}
	if values[0].Cmp(values[1]) != 0 {
		m.err = ErrArithmetic
		return Value{}
	}
	return values[0]
}
