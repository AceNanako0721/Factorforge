package trading_test

import (
	"encoding/json"
	"testing"

	"github.com/AceNanako0721/Factorforge/src/factorforge/trading/domain/decimal"
)

// Published JSON decimals stay plain (contract pattern has no exponent) while
// keeping value and scale; String() keeps its frozen form.
func TestDecimalJSONIsPlainAndRoundTrips(t *testing.T) {
	for raw, want := range map[string]string{"0E-15": "0.000000000000000", "-1.23E-8": "-0.0000000123", "0.000001": "0.000001", "107.796075": "107.796075", "1E+3": "1E+3"} {
		v, err := decimal.Parse(raw)
		if err != nil {
			t.Fatal(raw, err)
		}
		out, _ := json.Marshal(v)
		if string(out) != `"`+want+`"` {
			t.Fatalf("%s -> %s want %s", raw, out, want)
		}
		var back decimal.Value
		if err = json.Unmarshal(out, &back); err != nil || back.String() != v.String() {
			t.Fatalf("round trip %s -> %s (%v)", v.String(), back.String(), err)
		}
	}
}
