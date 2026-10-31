package strategy_test

import (
	"bytes"
	"encoding/json"
	"os"
	"testing"

	sd "github.com/AceNanako0721/Factorforge/src/factorforge/strategy/domain"
)

// The public policy example is a complete P2 policy that the news-judgment
// and stop-management stack accepts. Without composition.sizing every
// INITIAL_DISTANCE carrying risk_fraction is rejected (STOP_DIRECTIVE_INVALID)
// and no order is ever produced.
func TestDynamicStopPolicyExampleIsComplete(t *testing.T) {
	data, err := os.ReadFile("../../src/factorforge/applications/soxl/config/dynamic_stop_policy.example.json")
	if err != nil {
		t.Fatal(err)
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	var p sd.Policy
	if err = decoder.Decode(&p); err != nil {
		t.Fatal("example is not a P2 policy:", err)
	}
	if err = sd.Validate(&p); err != nil {
		t.Fatal("example fails P2 validation:", err)
	}
	if p.Composition == nil || p.Composition.Sizing == nil || p.Composition.Sizing.Mode != "RISK_UNIT" || !p.Composition.Sizing.RequireInitialDistance {
		t.Fatal("example lacks RISK_UNIT sizing")
	}
	if p.LearningMode != "FROZEN" || len(p.JudgmentEventTypes) == 0 {
		t.Fatal("example lacks FROZEN learning or a judgment event type")
	}
}
