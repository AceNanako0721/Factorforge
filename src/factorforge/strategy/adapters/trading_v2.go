// TradingV2 uses only P1's public HTTP contract and DTO validation. The framework
// never receives venue signing credentials or imports an execution implementation.
package adapters

import (
	"bytes"
	"context"
	"encoding/json"
	d "github.com/AceNanako0721/Factorforge/src/factorforge/strategy/domain"
	"github.com/AceNanako0721/Factorforge/src/factorforge/trading/api/dto"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"sort"
	"strings"
	"time"
)

const TradingPrefix = "/api/v2/trading"

type TradingV2 struct {
	URL, Token, CandleInterval string
	HistorySeconds             int
	Client                     *http.Client
}

func NewTradingV2(address, token string, timeout time.Duration, interval string, history int) (*TradingV2, error) {
	u, err := url.Parse(address)
	if err != nil || u.Host == "" || !d.Has([]string{"http", "https"}, u.Scheme) || u.User != nil || u.RawQuery != "" || u.Fragment != "" || token == "" || timeout <= 0 || interval == "" || history <= 0 {
		return nil, &d.Error{Code: "TRADING_CONFIGURATION_REQUIRED", Status: 503}
	}
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.Proxy = nil
	client := &http.Client{Transport: transport, Timeout: timeout, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	return &TradingV2{URL: strings.TrimRight(address, "/"), Token: token, CandleInterval: interval, HistorySeconds: history, Client: client}, nil
}
func SafeCode(code string) string {
	if regexp.MustCompile(`^[A-Z][A-Z0-9_]{1,79}$`).MatchString(code) {
		return code
	}
	return "TRADING_UNAVAILABLE"
}
func (t *TradingV2) Request(ctx context.Context, method, path string, query url.Values, body any) (map[string]any, error) {
	address := t.URL + TradingPrefix + path
	if len(query) > 0 {
		address += "?" + query.Encode()
	}
	var reader io.Reader
	if body != nil {
		data, err := d.Marshal(body)
		if err != nil {
			return nil, &d.Error{Code: "TRADING_REQUEST_INVALID", Status: 422}
		}
		reader = bytes.NewReader(data)
	}
	request, err := http.NewRequestWithContext(ctx, method, address, reader)
	if err != nil {
		return nil, &d.Error{Code: "TRADING_DELIVERY_UNKNOWN", Status: 503}
	}
	request.Header.Set("Authorization", "Bearer "+t.Token)
	request.Header.Set("Content-Type", "application/json")
	response, err := t.Client.Do(request)
	if err != nil {
		return nil, &d.Error{Code: "TRADING_DELIVERY_UNKNOWN", Status: 503}
	}
	defer response.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(response.Body, 4<<20+1))
	if err != nil || len(raw) > 4<<20 {
		return nil, &d.Error{Code: "TRADING_UNAVAILABLE", Status: 503}
	}
	var result map[string]any
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	if dec.Decode(&result) != nil {
		return nil, &d.Error{Code: "TRADING_UNAVAILABLE", Status: 503}
	}
	if response.StatusCode >= 300 {
		return nil, &d.Error{Code: SafeCode(d.Text(result["code"])), Status: response.StatusCode}
	}
	return result, nil
}
func queryBinding(b d.RunBinding) url.Values {
	return url.Values{"environment": {b.Environment}, "account_id": {b.AccountID}, "run_id": {b.RunID}}
}
func (t *TradingV2) Snapshot(ctx context.Context, objects []*d.ObservedObject) (snapshot *d.TradingSnapshot, err error) {
	err = d.Guard(func() error {
		if len(objects) == 0 {
			return &d.Error{Code: "MIXED_ACCOUNT_SNAPSHOT", Status: 422}
		}
		binding := objects[0].TradingRunKey
		for _, o := range objects {
			if o.TradingRunKey != binding {
				return &d.Error{Code: "MIXED_ACCOUNT_SNAPSHOT", Status: 422}
			}
		}
		query := queryBinding(binding)
		runQuery := url.Values{"environment": {binding.Environment}, "account_id": {binding.AccountID}}
		var run, account map[string]any
		pages := map[string]map[string]any{}
		consistent := false
		for attempt := 0; attempt < 3; attempt++ {
			var e error
			run, e = t.Request(ctx, "GET", "/runs/"+url.PathEscape(binding.RunID), runQuery, nil)
			if e != nil {
				return e
			}
			account, e = t.Request(ctx, "GET", "/account", query, nil)
			if e != nil {
				return e
			}
			for _, name := range []string{"instruments", "positions", "orders", "targets", "owners", "protections", "market/points", "fills", "income", "external-facts", "audit"} {
				pages[name], e = t.Request(ctx, "GET", "/"+name, query, nil)
				if e != nil {
					return e
				}
			}
			end, e := t.Request(ctx, "GET", "/runs/"+url.PathEscape(binding.RunID), runQuery, nil)
			if e != nil {
				return e
			}
			version := d.Number(run["aggregate_version"])
			consistent = version == d.Number(end["aggregate_version"])
			for _, p := range pages {
				consistent = consistent && d.Number(p["snapshot_version"]) == version
			}
			if consistent {
				break
			}
		}
		if !consistent {
			return &d.Error{Code: "TRADING_SNAPSHOT_CONFLICT", Status: 409}
		}
		at := d.At(account["observed_at"])
		snap := &d.TradingSnapshot{RunKey: binding, Version: d.Number(run["aggregate_version"]), At: at, Equity: d.Amount(account["equity"]), PolicyVersion: d.Text(account["policy_version"]), RiskLocks: d.Strings(account["risk_locks"]), RunState: d.Text(run["state"]), Actual: map[string]d.Decimal{}, Pending: map[string]d.Decimal{}, OwnerEpochs: map[string]int{}, TargetVersions: map[string]int{}, Specs: map[string]map[string]any{}, Samples: map[string]d.MarketSample{}, Bars: map[string][]d.Bar{}, Protections: map[string][]map[string]any{}, AverageEntries: map[string]*d.Decimal{}, SourceDecisions: []string{}, Fills: []map[string]any{}, Incomes: d.Rows(pages["income"]["items"]), OtherExposures: []d.ExposureRisk{}, ExternalChange: d.Text(run["state"]) != "NORMAL"}
		m := d.Math()
		orders := map[string]map[string]any{}
		terminal := []string{"FILLED", "CANCELED", "CANCELLED", "REJECTED", "EXPIRED"}
		for _, o := range d.Rows(pages["orders"]["items"]) {
			if id := d.Text(o["external_order_id"]); id != "" {
				orders[id] = o
			}
		}
		managed := []string{}
		for _, obj := range objects {
			identity := d.Digest(obj.InstrumentKey)
			managed = append(managed, identity)
			var spec map[string]any
			for _, s := range d.Rows(pages["instruments"]["items"]) {
				if d.Digest(s["key"]) == identity {
					spec = s
					break
				}
			}
			if spec == nil {
				return &d.Error{Code: "INSTRUMENT_NOT_REGISTERED", Status: 409}
			}
			id := obj.ObjectID
			snap.Specs[id] = spec
			actual, pending := d.Zero(), d.Zero()
			snap.AverageEntries[id] = nil
			for _, p := range d.Rows(pages["positions"]["items"]) {
				if d.Digest(p["instrument_key"]) != identity {
					continue
				}
				q := d.Amount(p["quantity"])
				if d.Text(p["owner_id"]) != obj.OwnerID && q.Sign() != 0 {
					return &d.Error{Code: "OWNER_CONFLICT", Status: 409}
				}
				if d.Text(p["owner_id"]) == obj.OwnerID {
					actual = m.Add(actual, q)
					if p["average_entry"] != nil {
						snap.AverageEntries[id] = d.Ptr(d.Amount(p["average_entry"]))
					}
				}
			}
			for _, o := range d.Rows(pages["orders"]["items"]) {
				if d.Digest(o["instrument_key"]) == identity && d.Text(o["owner_id"]) == obj.OwnerID && !d.Has(terminal, d.Text(o["state"])) {
					q := d.Amount(o["remaining_quantity"])
					if d.Text(o["side"]) != "BUY" {
						q = q.Neg()
					}
					pending = m.Add(pending, q)
				}
			}
			snap.Actual[id] = actual
			snap.Pending[id] = pending
			ownerFound := false
			for _, owner := range d.Rows(pages["owners"]["items"]) {
				if d.Digest(owner["instrument_key"]) == identity {
					if owner["owner_id"] != nil && d.Text(owner["owner_id"]) != obj.OwnerID {
						return &d.Error{Code: "OWNER_CONFLICT", Status: 409}
					}
					snap.OwnerEpochs[id] = d.Number(owner["owner_epoch"])
					ownerFound = true
					break
				}
			}
			if !ownerFound {
				return &d.Error{Code: "OWNER_SNAPSHOT_MISSING", Status: 423}
			}
			snap.TargetVersions[id] = 0
			for _, target := range d.Rows(pages["targets"]["items"]) {
				if d.Digest(target["instrument_key"]) == identity && d.Number(target["target_version"]) > snap.TargetVersions[id] {
					snap.TargetVersions[id] = d.Number(target["target_version"])
				}
			}
			snap.Protections[id] = []map[string]any{}
			for _, p := range d.Rows(pages["protections"]["items"]) {
				if d.Digest(p["instrument_key"]) == identity && d.Text(p["owner_id"]) == obj.OwnerID {
					snap.Protections[id] = append(snap.Protections[id], p)
				}
			}
			prices := []map[string]any{}
			for _, p := range d.Rows(pages["market/points"]["items"]) {
				if d.Digest(p["instrument_key"]) == identity && d.Text(p["kind"]) == "MARK" && !d.At(p["available_at"]).After(at) {
					prices = append(prices, p)
				}
			}
			sort.SliceStable(prices, func(i, j int) bool { return d.Text(prices[i]["available_at"]) < d.Text(prices[j]["available_at"]) })
			if len(prices) > 0 {
				p := prices[len(prices)-1]
				quality := d.Text(p["quality"])
				if !d.Has([]string{"VALID", "STALE"}, quality) {
					quality = "UNKNOWN"
				}
				snap.Samples[id] = d.MarketSample{AvailableAt: d.At(p["available_at"]), Price: d.Amount(p["value"]), Quality: quality}
			}
			candleQuery := queryBinding(binding)
			candleQuery.Set("venue", obj.InstrumentKey.Venue)
			candleQuery.Set("instrument_id", obj.InstrumentKey.InstrumentID)
			candleQuery.Set("interval", t.CandleInterval)
			candleQuery.Set("start", d.ISO(at.Add(-time.Duration(t.HistorySeconds)*time.Second)))
			candleQuery.Set("end", d.ISO(at))
			page, e := t.Request(ctx, "GET", "/market/candles", candleQuery, nil)
			if e != nil {
				return e
			}
			if d.Number(page["snapshot_version"]) != snap.Version {
				return &d.Error{Code: "TRADING_SNAPSHOT_CONFLICT", Status: 409}
			}
			snap.Bars[id] = []d.Bar{}
			for _, c := range d.Rows(page["items"]) {
				snap.Bars[id] = append(snap.Bars[id], d.Bar{CloseAt: d.At(c["close_at"]), AvailableAt: d.At(c["available_at"]), High: d.Amount(c["high"]), Low: d.Amount(c["low"]), Close: d.Amount(c["close"]), Final: d.Flag(c["final"]), Quality: "VALID"})
			}
		}
		exits := []string{}
		for _, p := range d.Rows(pages["protections"]["items"]) {
			if p["exit_order_id"] != nil {
				exits = append(exits, d.Text(p["exit_order_id"]))
			}
		}
		for _, fact := range d.Rows(pages["fills"]["items"]) {
			order := orders[d.Text(fact["external_order_id"])]
			instrument := d.Object(fact["instrument_key"])
			code := d.Text(instrument["venue"]) + ":" + d.Text(instrument["product"]) + ":" + d.Text(instrument["instrument_id"])
			var origin any
			for _, a := range d.Rows(pages["audit"]["items"]) {
				detail := d.Object(a["detail"])
				if d.Text(a["action"]) == "TARGET_SET" && d.Text(detail["resource_id"]) == code && d.Digest(detail["target_version"]) == d.Digest(order["target_version"]) {
					origin = a["request_id"]
					break
				}
			}
			copy := d.Clone(fact)
			copy["owner_id"] = order["owner_id"]
			copy["protection_exit"] = d.Has(exits, d.Text(order["order_id"]))
			copy["source_decision_id"] = origin
			snap.Fills = append(snap.Fills, copy)
		}
		for _, spec := range d.Rows(pages["instruments"]["items"]) {
			identity := d.Digest(spec["key"])
			if d.Has(managed, identity) {
				continue
			}
			held := d.Zero()
			queued := []d.Decimal{}
			for _, p := range d.Rows(pages["positions"]["items"]) {
				if d.Digest(p["instrument_key"]) == identity {
					held = m.Add(held, d.Amount(p["quantity"]))
				}
			}
			for _, o := range d.Rows(pages["orders"]["items"]) {
				if d.Digest(o["instrument_key"]) == identity && !d.Has(terminal, d.Text(o["state"])) {
					q := d.Amount(o["remaining_quantity"])
					if d.Text(o["side"]) != "BUY" {
						q = q.Neg()
					}
					queued = append(queued, q)
				}
			}
			var price *d.Decimal
			for _, p := range d.Rows(pages["market/points"]["items"]) {
				if d.Digest(p["instrument_key"]) == identity && d.Text(p["kind"]) == "MARK" && d.Text(p["quality"]) == "VALID" {
					price = d.Ptr(d.Amount(p["value"]))
					break
				}
			}
			if held.Sign() != 0 || len(queued) > 0 {
				if price == nil || d.Text(spec["settlement_currency"]) != d.Text(account["currency"]) {
					snap.RiskLocks = append(snap.RiskLocks, "UNPRICED_EXTERNAL_EXPOSURE")
				} else {
					for _, q := range append([]d.Decimal{held}, queued...) {
						v := m.Mul(m.Mul(q, *price), d.Amount(spec["contract_multiplier"]))
						group := d.Text(spec["risk_group"])
						if group == "" {
							group = "DEFAULT"
						}
						snap.OtherExposures = append(snap.OtherExposures, d.ExposureRisk{Value: v, Stress: v.Abs(), Group: group})
					}
				}
			}
		}
		for _, target := range d.Rows(pages["targets"]["items"]) {
			if v := d.Text(target["source_decision_id"]); v != "" {
				snap.SourceDecisions = append(snap.SourceDecisions, v)
			}
		}
		for _, a := range d.Rows(pages["audit"]["items"]) {
			if d.Text(a["action"]) == "TARGET_SET" {
				snap.SourceDecisions = append(snap.SourceDecisions, d.Text(a["request_id"]))
			}
		}
		snap.SourceDecisions = d.Unique(snap.SourceDecisions)
		if err := m.Err(); err != nil {
			return err
		}
		snapshot = snap
		return nil
	})
	return
}
func (t *TradingV2) Prepare(obj *d.ObservedObject, item *d.TargetOutbox, snapshot *d.TradingSnapshot) (map[string]any, error) {
	decision := item.Decision
	body := map[string]any{"schema_version": "trading-2.0", "request_id": decision.DecisionID, "idempotency_key": decision.DecisionID, "run_key": d.Map(obj.TradingRunKey), "expected_version": snapshot.Version, "reason": "strategy cycle " + decision.CycleID, "expires_at_utc": item.ExpiresAt, "owner_id": obj.OwnerID, "instrument_key": d.Map(obj.InstrumentKey), "target_version": item.TargetVersion, "target_quantity": decision.TargetQuantity, "owner_epoch": item.OwnerEpoch, "policy_version": snapshot.PolicyVersion, "spec_version": snapshot.Specs[obj.ObjectID]["version"], "source_decision_id": decision.DecisionID, "protection_plan": d.JSONValue(decision.StopPlan)}
	raw, err := d.Marshal(body)
	if err != nil {
		return nil, err
	}
	var request dto.TargetRequest
	if dto.Decode(bytes.NewReader(raw), &request) != nil {
		return nil, &d.Error{Code: "TRADING_TARGET_INVALID", Status: 422}
	}
	return d.Map(body), nil
}
func (t *TradingV2) Deliver(ctx context.Context, obj *d.ObservedObject, item *d.TargetOutbox, snap *d.TradingSnapshot) (map[string]any, error) {
	var body map[string]any
	if item.CommandPayload != nil {
		body = *item.CommandPayload
	} else {
		v, err := t.Prepare(obj, item, snap)
		if err != nil {
			return nil, err
		}
		body = v
	}
	return t.Request(ctx, "PUT", "/owners/"+url.PathEscape(obj.OwnerID)+"/targets/"+url.PathEscape(obj.InstrumentKey.InstrumentID), nil, body)
}
func (t *TradingV2) Simulate(ctx context.Context, scenario map[string]any) (map[string]any, error) {
	create := d.Object(scenario["create_run"])
	binding := d.Object(create["run_key"])
	if d.Text(binding["environment"]) != "SIM" {
		return nil, &d.Error{Code: "COUNTERFACTUAL_MUST_BE_SIM", Status: 403}
	}
	result, err := t.Request(ctx, "POST", "/runs", nil, create)
	if err != nil {
		return nil, err
	}
	query := url.Values{"environment": {"SIM"}, "account_id": {d.Text(binding["account_id"])}, "run_id": {d.Text(binding["run_id"])}}
	for _, op := range d.Rows(scenario["operations"]) {
		body := d.Clone(d.Object(op["body"]))
		if d.Digest(body["run_key"]) != d.Digest(binding) {
			return nil, &d.Error{Code: "COUNTERFACTUAL_OPERATION_RUN_MISMATCH", Status: 403}
		}
		run, e := t.Request(ctx, "GET", "/runs/"+url.PathEscape(d.Text(binding["run_id"])), query, nil)
		if e != nil {
			return nil, e
		}
		body["expected_version"] = run["aggregate_version"]
		method, path := d.Text(op["method"]), d.Text(op["path"])
		result, e = t.Request(ctx, method, path, nil, body)
		if e != nil {
			return nil, e
		}
		if method == "PUT" && strings.Contains(path, "/targets/") {
			wait := t.Client.Timeout
			if wait <= 0 {
				wait = 5 * time.Second
			}
			if wait > 60*time.Second {
				wait = 60 * time.Second
			}
			deadline := time.Now().Add(wait)
			for {
				orders, e := t.Request(ctx, "GET", "/orders", query, nil)
				if e != nil {
					return nil, e
				}
				unfinished := false
				for _, o := range d.Rows(orders["items"]) {
					unfinished = unfinished || d.Has([]string{"RESERVED", "DISPATCHING", "UNKNOWN"}, d.Text(o["state"]))
				}
				if !unfinished {
					break
				}
				if !time.Now().Before(deadline) {
					return nil, &d.Error{Code: "COUNTERFACTUAL_EXECUTION_UNCONFIRMED", Status: 503}
				}
				timer := time.NewTimer(50 * time.Millisecond)
				select {
				case <-ctx.Done():
					timer.Stop()
					return nil, ctx.Err()
				case <-timer.C:
				}
			}
		}
	}
	account, err := t.Request(ctx, "GET", "/account", query, nil)
	if err != nil {
		return nil, err
	}
	return map[string]any{"run": binding, "last_receipt": result, "equity": account["equity"], "fees": account["fees"], "realized_pnl": account["realized_pnl"], "risk_locks": account["risk_locks"]}, nil
}
