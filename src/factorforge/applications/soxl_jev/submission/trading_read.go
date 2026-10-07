package submission

import (
	"context"
	"encoding/json"
	d "github.com/AceNanako0721/Factorforge/src/factorforge/applications/soxl_jev/domain"
	dto "github.com/AceNanako0721/Factorforge/src/factorforge/trading/api/dto"
	"io"
	"net/http"
	"net/url"
	"strings"
)

type TradingReadOptions struct {
	BaseURL, Token   string
	Run              dto.RunKey
	Client           *http.Client
	MaxResponseBytes int
}
type TradingReadHTTP struct{ options TradingReadOptions }

func NewTradingRead(o TradingReadOptions) (*TradingReadHTTP, error) {
	u, err := url.Parse(o.BaseURL)
	if err != nil || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || strings.TrimRight(u.Path, "/") != "" || !d.Has([]string{"http", "https"}, u.Scheme) ||
		!d.Has([]string{"SIM", "LIVE"}, o.Run.Environment) || !d.ValidID(o.Run.AccountID) || !d.ValidID(o.Run.RunID) || o.Token == "" || o.Client == nil || o.Client.Timeout <= 0 || o.MaxResponseBytes <= 0 {
		return nil, d.Fail("TRADING_READ_CONFIGURATION_REQUIRED", 503)
	}
	client := *o.Client
	client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	o.Client = &client
	o.BaseURL = strings.TrimRight(o.BaseURL, "/")
	return &TradingReadHTTP{o}, nil
}
func (c *TradingReadHTTP) get(ctx context.Context, resource string, target any) error {
	// These are the only allowed transport paths. No write method or caller URL.
	if !d.Has([]string{"/health", "/runs/" + c.options.Run.RunID, "/instruments"}, resource) {
		return d.Fail("TRADING_READ_OPERATION_FORBIDDEN", 403)
	}
	endpoint := c.options.BaseURL + "/api/v2/trading" + resource
	if resource != "/health" {
		endpoint += "?" + url.Values{"environment": {c.options.Run.Environment}, "account_id": {c.options.Run.AccountID}, "run_id": {c.options.Run.RunID}}.Encode()
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
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
	// All output is subsequently projected into closed binding value records.
	if json.Unmarshal(raw, target) != nil {
		return d.Fail("TRADING_READ_RESPONSE_INVALID", 503)
	}
	return nil
}
func (c *TradingReadHTTP) BindingSnapshot(ctx context.Context) (d.TradingBindingSnapshot, error) {
	var result d.TradingBindingSnapshot
	var health struct{ Environment string }
	if err := c.get(ctx, "/health", &health); err != nil {
		return result, err
	}
	if health.Environment != c.options.Run.Environment {
		return result, d.Fail("TRADING_BINDING_MISMATCH", 403)
	}
	var run struct {
		RunKey           dto.RunKey `json:"run_key"`
		AggregateVersion int        `json:"aggregate_version"`
		State            string     `json:"state"`
		ExecutionMode    string     `json:"execution_mode"`
		PolicyVersion    string     `json:"policy_version"`
	}
	if err := c.get(ctx, "/runs/"+c.options.Run.RunID, &run); err != nil {
		return result, err
	}
	if run.RunKey != c.options.Run || run.AggregateVersion < 0 || !d.ValidID(run.PolicyVersion) {
		return result, d.Fail("TRADING_BINDING_MISMATCH", 403)
	}
	var page struct {
		Items           []dto.InstrumentSpec `json:"items"`
		Cursor          *string              `json:"cursor"`
		SnapshotVersion int                  `json:"snapshot_version"`
	}
	if err := c.get(ctx, "/instruments", &page); err != nil {
		return result, err
	}
	if page.Items == nil || page.Cursor != nil || page.SnapshotVersion != run.AggregateVersion {
		return result, d.Fail("TRADING_BINDING_SNAPSHOT_CHANGED", 409)
	}
	return d.TradingBindingSnapshot{Run: run.RunKey, State: run.State, ExecutionMode: run.ExecutionMode, PolicyVersion: run.PolicyVersion, AggregateVersion: run.AggregateVersion, Instruments: page.Items}, nil
}
