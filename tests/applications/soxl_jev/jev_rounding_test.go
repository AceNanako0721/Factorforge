package soxl_jev_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	a "github.com/AceNanako0721/Factorforge/src/factorforge/applications/soxl_jev/analysis"
	dec "github.com/AceNanako0721/Factorforge/src/factorforge/trading/api/dto"
)

// : real JEV responses round probabilities, so the exact
// decimal sum may differ from 1 by at most 0.01; larger deviations still fail.
func TestJevProbabilityRoundingTolerance(t *testing.T) {
	_, r, _, now := pipelineFixture()
	var result map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		json.NewEncoder(w).Encode(result)
	}))
	defer server.Close()
	o := a.JevOptions{Endpoint: server.URL + "/v1/systemone", Token: "fixture-only-token", ModelVersion: r.ModelVersion, Prompt: fixturePrompt(), Client: &http.Client{Timeout: time.Second}, MaxRequestBytes: 10000, MaxResponseBytes: 10000, FixtureOnly: true, Clock: func() time.Time { return now },
		Mapping: a.CalibrationMapping{Version: r.CalibrationVersion, RubricVersion: r.RubricVersion, ProducerVersion: "fixture-producer", Impact: []dec.Decimal{number("0"), number("5")}, Relevance: []dec.Decimal{number("0"), number("1")}, Expectation: []dec.Decimal{number("0"), number("1")}, HalfLife: []dec.Decimal{number("60"), number("300")}, Credibility: number("1"), Quality: number("1"), Novelty: number("1"), Prepricing: number("0"), ClaimSupportMinimum: number("0.9"), Verified: true}}
	provider, err := a.NewJev(o)
	if err != nil {
		t.Fatal(err)
	}
	direction := func(p, n, z, u string) {
		result["answers"].(map[string]any)["direction"] = map[string]any{"type": "choice", "choice": "POSITIVE", "confidence": json.Number("0.6"), "probabilities": map[string]any{"POSITIVE": json.Number(p), "NEGATIVE": json.Number(n), "NEUTRAL": json.Number(z), "UNKNOWN": json.Number(u)}}
	}
	impact := func(low, high string) {
		result["answers"].(map[string]any)["impact"] = map[string]any{"type": "score", "score": json.Number("1"), "confidence": json.Number("0.5"), "probabilities": map[string]any{"0": json.Number(low), "1": json.Number(high)}, "legend": map[string]string{"0": "fixture low", "1": "fixture high"}}
	}
	for name, values := range map[string][4]string{
		"rounded-low":  {"0.6", "0.2", "0.19", "0"},
		"rounded-high": {"0.6", "0.2", "0.21", "0"},
		"float-noise":  {"0.30000000000000004", "0.6", "0.1", "0"},
	} {
		result = jevResult()
		direction(values[0], values[1], values[2], values[3])
		if c, err := provider.Analyze(context.Background(), r); err != nil || a.Validate(r, c, now, true) != nil {
			t.Fatal("observed rounding rejected", name, err)
		}
	}
	for name, values := range map[string][4]string{
		"low":      {"0.6", "0.2", "0.18", "0"},
		"high":     {"0.6", "0.2", "0.22", "0"},
		"negative": {"0.61", "0.4", "-0.01", "0"},
		"over-one": {"1.01", "0", "0", "0"},
	} {
		result = jevResult()
		direction(values[0], values[1], values[2], values[3])
		if _, err := provider.Analyze(context.Background(), r); err == nil {
			t.Fatal("distorted distribution accepted", name)
		}
	}
	result = jevResult()
	delete(result["answers"].(map[string]any)["direction"].(map[string]any)["probabilities"].(map[string]any), "UNKNOWN")
	if _, err := provider.Analyze(context.Background(), r); err == nil {
		t.Fatal("missing option accepted")
	}
	// Score expectation is normalised by the validated sum.
	result = jevResult()
	impact("0.5", "0.5")
	c, err := provider.Analyze(context.Background(), r)
	if err != nil || c.Vector.ImpactPoints.Cmp(number("2.5")) != 0 {
		t.Fatal("exact distribution mapping changed", err)
	}
	result = jevResult()
	impact("0.5", "0.49")
	c, err = provider.Analyze(context.Background(), r)
	m := dec.NewMath(28)
	want := m.Div(m.Mul(number("0.49"), number("5")), number("0.99"))
	if err != nil || m.Err() != nil || c.Vector.ImpactPoints.Cmp(want) != 0 {
		t.Fatal("rounded score not normalised", err, c.Vector.ImpactPoints, want)
	}
}
