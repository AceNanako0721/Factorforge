// Package jev classifies the price-only market state (S3-035) with a
// System One model: typed questions over a JSON state, no generated text.
package jev

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"time"

	d "github.com/AceNanako0721/Factorforge/src/factorforge/applications/soxl/domain"
)

// Client posts one state and two questions per call and never retries; the
// position manager asks again on its next cycle.
type Client struct {
	URL, Key, Model string
	Timeout         time.Duration
	HTTP            *http.Client
}

const maxResponseBytes = 64 << 10

// The state names no instrument and no time, so the answer rests on the
// features alone.
const instrument = "anonymized leveraged equity perpetual"

var questions = map[string]any{
	"state": map[string]any{"type": "choice", "criteria": map[string]string{
		"UPTREND":   "Price is in a sustained rise across the recent windows.",
		"DOWNTREND": "Price is in a sustained decline across the recent windows.",
		"RANGE":     "No clear sustained direction; mixed or sideways."},
		"instructions": "Classify the current market state from the price features only."},
	"strength": map[string]any{"type": "score", "criteria": []string{"No trend", "Weak", "Moderate", "Strong", "Very strong"},
		"instructions": "How strong is the current trend?"},
}

// Classify returns the state, its probabilities and the trend strength.
// HourAt, AskedAt and Features are left to the caller.
func (c Client) Classify(ctx context.Context, f d.PriceFeatures) (d.PriceState, error) {
	var out d.PriceState
	body, err := json.Marshal(map[string]any{"model": c.Model, "state": map[string]any{"instrument": instrument, "features": f}, "questions": questions})
	if err != nil {
		return out, d.Fail("PRICE_STATE_REQUEST_INVALID", 422)
	}
	ctx, cancel := context.WithTimeout(ctx, c.Timeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.URL, bytes.NewReader(body))
	if err != nil {
		return out, d.Fail("PRICE_STATE_REQUEST_INVALID", 422)
	}
	req.Header.Set("Authorization", "Bearer "+c.Key)
	req.Header.Set("Content-Type", "application/json")
	client := c.HTTP
	if client == nil {
		client = http.DefaultClient
	}
	resp, err := client.Do(req)
	if err != nil {
		return out, d.Fail("PRICE_STATE_UNREACHABLE", 503)
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseBytes+1))
	if err != nil || len(raw) > maxResponseBytes {
		return out, d.Fail("PRICE_STATE_RESPONSE_INVALID", 503)
	}
	if resp.StatusCode != http.StatusOK {
		return out, d.Fail("PRICE_STATE_REJECTED", 503)
	}
	var reply struct {
		Model   string `json:"model"`
		Answers struct {
			State struct {
				Choice        string             `json:"choice"`
				Probabilities map[string]float64 `json:"probabilities"`
			} `json:"state"`
			Strength struct {
				Score *float64 `json:"score"`
			} `json:"strength"`
		} `json:"answers"`
	}
	if json.Unmarshal(raw, &reply) != nil || !d.Has(d.PriceStates, reply.Answers.State.Choice) || reply.Answers.Strength.Score == nil || reply.Model == "" {
		return out, d.Fail("PRICE_STATE_RESPONSE_INVALID", 503)
	}
	probabilities := map[string]float64{}
	for _, s := range d.PriceStates {
		p, ok := reply.Answers.State.Probabilities[s]
		if !ok || p < 0 || p > 1 {
			return out, d.Fail("PRICE_STATE_RESPONSE_INVALID", 503)
		}
		probabilities[s] = p
	}
	return d.PriceState{State: reply.Answers.State.Choice, Probabilities: probabilities, Strength: *reply.Answers.Strength.Score, Model: reply.Model}, nil
}
