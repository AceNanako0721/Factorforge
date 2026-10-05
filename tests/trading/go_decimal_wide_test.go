package trading_test

import (
	"encoding/json"
	"fmt"
	"github.com/AceNanako0721/Factorforge/src/factorforge/trading/domain/decimal"
	"os"
	"testing"
)

func TestDecimalPythonExponentRange(t *testing.T) {
	data, err := os.ReadFile("fixtures/go_decimal_wide.json")
	if err != nil {
		t.Fatal(err)
	}
	var fixture struct {
		Precision     uint32 `json:"precision"`
		FixtureOnly   bool   `json:"fixture_only"`
		OracleCommit  string `json:"oracle_commit"`
		SchemaVersion int    `json:"schema_version"`
		Emax          int    `json:"emax"`
		Emin          int    `json:"emin"`
		Cases         []struct {
			Operation, A, B, Result string
			Error                   bool
		} `json:"cases"`
	}
	if json.Unmarshal(data, &fixture) != nil || fixture.SchemaVersion != 1 || !fixture.FixtureOnly || fixture.OracleCommit != "e97c9e9a380d105a7e96933bdd325e1d9af06d93" || fixture.Precision != 28 || fixture.Emax != 999999 || fixture.Emin != -999999 || len(fixture.Cases) != 360 {
		t.Fatal("invalid wide oracle")
	}
	for i, row := range fixture.Cases {
		t.Run(fmt.Sprintf("%03d-%s", i, row.Operation), func(t *testing.T) {
			a, err := decimal.Parse(row.A)
			if err != nil {
				t.Fatal(err)
			}
			b, err := decimal.Parse(row.B)
			if err != nil {
				t.Fatal(err)
			}
			m := decimal.NewMath(28)
			var result decimal.Value
			switch row.Operation {
			case "add":
				result = m.Add(a, b)
			case "sub":
				result = m.Sub(a, b)
			case "mul":
				result = m.Mul(a, b)
			case "div":
				result = m.Div(a, b)
			case "sqrt":
				result = m.Sqrt(a)
			case "ln":
				result = m.Ln(a)
			case "exp":
				result = m.Exp(a)
			case "integral":
				result = m.Integral(a, decimal.TowardZero)
			}
			if row.Error {
				if m.Err() == nil {
					t.Fatalf("overflow expected: %s %s %s", row.Operation, row.A, row.B)
				}
				return
			}
			if m.Err() != nil {
				t.Fatal(m.Err())
			}
			expected, err := decimal.Parse(row.Result)
			if err != nil || result.Cmp(expected) != 0 || result.String() != row.Result {
				t.Fatalf("%s: expected %s, got %s", row.A, row.Result, result.String())
			}
		})
	}
	for _, text := range []string{"1e999999999999999999", "1e-999999999999999999", "١٢.٣٤"} {
		if _, err := decimal.Parse(text); err != nil {
			t.Fatal(text, err)
		}
	}
}
