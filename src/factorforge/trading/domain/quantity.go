package domain

import "github.com/AceNanako0721/Factorforge/src/factorforge/trading/domain/decimal"

// FloorStep uses ROUND_DOWN (toward zero, including negative quantities).
func FloorStep(value, step decimal.Value) (decimal.Value, error) {
	m := decimal.NewMath(28)
	result := m.Step(value, step, decimal.TowardZero)
	return result, m.Err()
}
