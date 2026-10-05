// Package dto is the trading layer's public, infrastructure-free value surface.
// Upper layers do not import trading domain or execution implementation packages.
package dto

import "github.com/AceNanako0721/Factorforge/src/factorforge/trading/domain/decimal"

type Decimal = decimal.Value
type Math = decimal.Math
type Rounding = decimal.Rounding

const TowardZero = decimal.TowardZero
const Ceiling = decimal.Ceiling
const Floor = decimal.Floor

func ParseDecimal(value string) (Decimal, error) { return decimal.Parse(value) }
func NewMath(precision uint32) *Math             { return decimal.NewMath(precision) }
func Min(values ...Decimal) Decimal              { return decimal.Min(values...) }
func Max(values ...Decimal) Decimal              { return decimal.Max(values...) }
