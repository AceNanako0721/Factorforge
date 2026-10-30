package submission

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"time"

	d "github.com/AceNanako0721/Factorforge/src/factorforge/applications/soxl_jev/domain"
)

// position manager reads (S3-027～030). Read-only GETs against the
// bound run; no trading write is reachable from this client.

type AccountRead struct {
	Currency   string    `json:"currency"`
	Equity     string    `json:"equity"`
	ObservedAt time.Time `json:"observed_at"`
}
type PositionRead struct {
	InstrumentKey   map[string]any `json:"instrument_key"`
	OwnerID         string         `json:"owner_id"`
	Quantity        string         `json:"quantity"`
	AverageEntry    *string        `json:"average_entry"`
	PositionCycleID string         `json:"position_cycle_id"`
	EffectiveMode   string         `json:"effective_mode"`
}
type ProtectionRead struct {
	ProtectionID      string         `json:"protection_id"`
	OwnerID           string         `json:"owner_id"`
	Role              string         `json:"role"`
	State             string         `json:"state"`
	PositionDirection string         `json:"position_direction"`
	PositionCycleID   string         `json:"position_cycle_id"`
	Plan              map[string]any `json:"plan"`
}
type CandleRead struct {
	CloseAt     time.Time `json:"close_at"`
	AvailableAt time.Time `json:"available_at"`
	High        string    `json:"high"`
	Low         string    `json:"low"`
	Close       string    `json:"close"`
	Final       bool      `json:"final"`
}

func (c *TradingReadHTTP) read(ctx context.Context, resource string, extra url.Values, target any) error {
	if !d.Has([]string{"/account", "/positions", "/protections", "/market/candles"}, resource) {
		return d.Fail("TRADING_READ_OPERATION_FORBIDDEN", 403)
	}
	q := url.Values{"environment": {c.options.Run.Environment}, "account_id": {c.options.Run.AccountID}, "run_id": {c.options.Run.RunID}}
	for k, v := range extra {
		q[k] = v
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.options.BaseURL+"/api/v2/trading"+resource+"?"+q.Encode(), nil)
	if err != nil {
		return d.Fail("TRADING_READ_REQUEST_INVALID", 422)
	}
	req.Header.Set("Authorization", "Bearer "+c.options.Token)
	response, err := c.options.Client.Do(req)
	if err != nil {
		return d.Fail("TRADING_READ_UNAVAILABLE", 503)
	}
	defer response.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(response.Body, int64(c.options.MaxResponseBytes)+1))
	if err != nil || len(raw) > c.options.MaxResponseBytes {
		return d.Fail("TRADING_READ_BUDGET_EXCEEDED", 503)
	}
	if response.StatusCode != 200 {
		return d.Fail("TRADING_READ_REJECTED", response.StatusCode)
	}
	if json.Unmarshal(raw, target) != nil {
		return d.Fail("TRADING_READ_RESPONSE_INVALID", 503)
	}
	return nil
}

func (c *TradingReadHTTP) Account(ctx context.Context) (AccountRead, error) {
	var v AccountRead
	err := c.read(ctx, "/account", nil, &v)
	return v, err
}
func (c *TradingReadHTTP) Positions(ctx context.Context) ([]PositionRead, error) {
	var page struct {
		Items []PositionRead `json:"items"`
	}
	err := c.read(ctx, "/positions", nil, &page)
	return page.Items, err
}
func (c *TradingReadHTTP) Protections(ctx context.Context) ([]ProtectionRead, error) {
	var page struct {
		Items []ProtectionRead `json:"items"`
	}
	err := c.read(ctx, "/protections", nil, &page)
	return page.Items, err
}

// Candles reads completed candles in [start, end] for one instrument.
func (c *TradingReadHTTP) Candles(ctx context.Context, venue, instrument, interval string, start, end time.Time) ([]CandleRead, error) {
	var page struct {
		Items []CandleRead `json:"items"`
	}
	err := c.read(ctx, "/market/candles", url.Values{"venue": {venue}, "instrument_id": {instrument}, "interval": {interval}, "start": {start.UTC().Format(time.RFC3339)}, "end": {end.UTC().Format(time.RFC3339)}}, &page)
	return page.Items, err
}
