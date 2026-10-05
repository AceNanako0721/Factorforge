// Package api exposes only explicit trading projections and authenticated
// application commands. Exchange credentials never enter this process.
package api

import (
	"crypto/subtle"
	"encoding/json"
	"errors"
	"github.com/AceNanako0721/Factorforge/src/factorforge/trading/api/dto"
	a "github.com/AceNanako0721/Factorforge/src/factorforge/trading/application"
	d "github.com/AceNanako0721/Factorforge/src/factorforge/trading/domain"
	"mime"
	"net/http"
	"strings"
	"time"
)

const Prefix = "/api/v2/trading"

type Handler struct {
	Service *a.Service
	Tokens  map[string]d.Principal
}

func New(service *a.Service, tokens map[string]d.Principal) http.Handler {
	return &Handler{service, tokens}
}
func fail(code string, status int) error { return &d.Error{Code: code, Status: status} }
func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	if r.Method == "GET" && r.URL.Path == "/openapi.json" {
		w.Write([]byte(OpenAPI))
		return
	}
	if r.Method == "GET" && r.URL.Path == Prefix+"/health" {
		write(w, 200, a.Response{"schema_version": "trading-2.0", "environment": h.Service.Store.Environment(), "live_ready": false, "upper_layers_required": false})
		return
	}
	p, err := h.authenticate(r)
	if err != nil {
		h.problem(w, r, nil, err)
		return
	}
	var value any
	status := 200
	if r.Method == "GET" {
		value, err = h.query(r, p)
	} else {
		value, status, err = h.command(r, p)
	}
	if err != nil {
		h.problem(w, r, &p, err)
		return
	}
	write(w, status, value)
}
func (h *Handler) authenticate(r *http.Request) (d.Principal, error) {
	parts := strings.SplitN(r.Header.Get("Authorization"), " ", 2)
	if len(parts) == 2 && strings.EqualFold(parts[0], "Bearer") {
		for token, p := range h.Tokens {
			if token != "" && subtle.ConstantTimeCompare([]byte(token), []byte(parts[1])) == 1 {
				return p, nil
			}
		}
	}
	return d.Principal{}, fail("AUTHENTICATION_REQUIRED", 401)
}
func (h *Handler) problem(w http.ResponseWriter, r *http.Request, p *d.Principal, err error) {
	code, status := "INTERNAL_ERROR", 503
	var problem *d.Error
	var field any
	if errors.As(err, &problem) {
		code, status = problem.Code, problem.Status
	}
	if p != nil {
		_ = h.Service.RecordRejection(r.Context(), *p, code, r.Method)
	}
	var correlation any
	if id := r.Header.Get("x-request-id"); id != "" {
		correlation = id
	}
	write(w, status, a.Response{"code": code, "message": code, "field": field, "correlation_id": correlation, "retryable": false})
}
func write(w http.ResponseWriter, status int, value any) {
	data, err := json.Marshal(a.Wire(value))
	if err != nil {
		w.WriteHeader(503)
		w.Write([]byte(`{"code":"RESPONSE_UNAVAILABLE","message":"RESPONSE_UNAVAILABLE","field":null,"correlation_id":null,"retryable":false}`))
		return
	}
	w.WriteHeader(status)
	w.Write(data)
}
func path(r *http.Request) []string {
	if !strings.HasPrefix(r.URL.Path, Prefix+"/") {
		return nil
	}
	return strings.Split(strings.TrimPrefix(r.URL.Path, Prefix+"/"), "/")
}
func decode[T any](r *http.Request) (T, error) {
	var value T
	if header := r.Header.Get("Content-Type"); header != "" {
		media, _, err := mime.ParseMediaType(header)
		if err != nil || (media != "application/json" && !(strings.HasPrefix(media, "application/") && strings.HasSuffix(media, "+json"))) {
			return value, fail("INVALID_REQUEST", 422)
		}
	}
	err := dto.Decode(r.Body, &value)
	return value, err
}
func (h *Handler) command(r *http.Request, p d.Principal) (any, int, error) {
	s, ctx := h.Service, r.Context()
	parts := path(r)
	if len(parts) == 0 {
		return nil, 404, fail("NOT_FOUND", 404)
	}
	route := strings.Join(parts, "/")
	if r.Method == "POST" {
		switch route {
		case "runs":
			v, err := decode[dto.CreateRun](r)
			if err != nil {
				return nil, 0, err
			}
			result, err := s.CreateRun(ctx, p, v)
			return result, 201, err
		case "orders":
			v, err := decode[dto.SubmitOrder](r)
			if err != nil {
				return nil, 0, err
			}
			result, err := s.SubmitOrder(ctx, p, v, v.Order, nil)
			if result != nil {
				result = copyResponse(result)
				result["target_version"] = nil
			}
			return result, 202, err
		case "instruments":
			v, err := decode[dto.RegisterSpec](r)
			if err != nil {
				return nil, 0, err
			}
			result, err := s.RegisterSpec(ctx, p, v, v.Spec)
			return result, 201, err
		case "protections":
			v, err := decode[dto.SetProtection](r)
			if err != nil {
				return nil, 0, err
			}
			result, err := s.MaintainProtection(ctx, p, v, v.InstrumentKey, v.Plan, nil)
			return result, 201, err
		case "income":
			v, err := decode[dto.RecordIncome](r)
			if err != nil {
				return nil, 0, err
			}
			result, err := s.RecordIncome(ctx, p, v, v.Income)
			return result, 202, err
		case "simulation/frames":
			v, err := decode[dto.AdvanceReplay](r)
			if err != nil {
				return nil, 0, err
			}
			result, err := s.AdvanceReplay(ctx, p, v, v.Frame)
			return result, 202, err
		case "market/snapshots":
			v, err := decode[dto.MarketSnapshot](r)
			if err != nil {
				return nil, 0, err
			}
			result, err := s.IngestSnapshot(ctx, p, v)
			return result, 202, err
		case "external-facts":
			v, err := decode[dto.ImportExternal](r)
			if err != nil {
				return nil, 0, err
			}
			result, err := s.ImportExternal(ctx, p, v, v.Fact)
			return result, 202, err
		case "external-facts/resolve":
			v, err := decode[dto.ResolveExternal](r)
			if err != nil {
				return nil, 0, err
			}
			result, err := s.ResolveExternal(ctx, p, v)
			return result, 202, err
		case "fx":
			v, err := decode[dto.RegisterFX](r)
			if err != nil {
				return nil, 0, err
			}
			result, err := s.RegisterFX(ctx, p, v, v.Rate)
			return result, 201, err
		case "executors/fence":
			v, err := decode[dto.FenceExecutor](r)
			if err != nil {
				return nil, 0, err
			}
			result, err := s.FenceExecutor(ctx, p, v)
			return result, 202, err
		}
		if len(parts) == 3 {
			switch parts[0] {
			case "orders":
				if parts[2] == "cancel" {
					v, err := decode[d.Command](r)
					if err != nil {
						return nil, 0, err
					}
					result, err := s.CancelOrder(ctx, p, v, parts[1])
					return result, 202, err
				}
			case "protections":
				if parts[2] == "replace" {
					v, err := decode[dto.SetProtection](r)
					if err != nil {
						return nil, 0, err
					}
					result, err := s.MaintainProtection(ctx, p, v, v.InstrumentKey, v.Plan, &parts[1])
					return result, 202, err
				}
				if parts[2] == "cancel" {
					v, err := decode[d.Command](r)
					if err != nil {
						return nil, 0, err
					}
					result, err := s.CancelProtection(ctx, p, v, parts[1])
					return result, 202, err
				}
			case "runs":
				v, err := decode[d.Command](r)
				if err != nil {
					return nil, 0, err
				}
				if v.RunKey.RunID != parts[1] || !d.Has([]string{"stop", "resume", "reconcile"}, parts[2]) {
					return nil, 0, fail("RUN_ACTION_UNSUPPORTED", 422)
				}
				result, err := s.RunAction(ctx, p, v, parts[2])
				return result, 202, err
			}
		}
	}
	if r.Method == "PUT" && len(parts) == 4 && parts[0] == "owners" && parts[2] == "targets" {
		v, err := decode[dto.TargetRequest](r)
		if err != nil {
			return nil, 0, err
		}
		if parts[1] != v.OwnerID || parts[3] != v.InstrumentKey.InstrumentID {
			return nil, 0, fail("TARGET_PATH_MISMATCH", 422)
		}
		result, err := s.SetTarget(ctx, p, v)
		return result, 202, err
	}
	return nil, 404, fail("NOT_FOUND", 404)
}
func copyResponse(v a.Response) a.Response {
	result := a.Response{}
	for k, x := range v {
		result[k] = x
	}
	return result
}
func (h *Handler) query(r *http.Request, p d.Principal) (any, error) {
	parts := path(r)
	if len(parts) == 0 {
		return nil, fail("NOT_FOUND", 404)
	}
	q := r.URL.Query()
	key := d.RunKey{Environment: q.Get("environment"), AccountID: q.Get("account_id"), RunID: q.Get("run_id")}
	if len(parts) == 2 && parts[0] == "runs" {
		key.RunID = parts[1]
	}
	if d.Validate(key) != nil {
		return nil, fail("INVALID_REQUEST", 422)
	}
	run, err := h.Service.Read(r.Context(), p, key)
	if err != nil {
		return nil, err
	}
	items := []any{}
	page := func() any { return a.Response{"items": items, "cursor": nil, "snapshot_version": run.Version} }
	switch parts[0] {
	case "runs":
		if len(parts) == 2 {
			return a.RunView(run), nil
		}
	case "orders":
		if len(parts) == 2 {
			order := run.Orders.Value(parts[1])
			if order == nil {
				return nil, fail("ORDER_NOT_FOUND", 404)
			}
			view := a.OrderView(order)
			view["target_version"] = order.TargetVersion
			return view, nil
		}
		for _, order := range run.Orders.Values() {
			view := a.OrderView(order)
			view["target_version"] = order.TargetVersion
			items = append(items, view)
		}
		return page(), nil
	case "instruments":
		for _, x := range run.Specs.Values() {
			items = append(items, x)
		}
		return page(), nil
	case "positions":
		for _, x := range run.Positions.Values() {
			items = append(items, a.Response{"instrument_key": x.InstrumentKey, "owner_id": x.OwnerID, "quantity": x.Quantity, "average_entry": x.AverageEntry, "protection_state": x.ProtectionState, "reconciliation_state": run.State, "observed_at": run.Clock})
		}
		return page(), nil
	case "owners":
		for _, code := range run.Specs.Keys() {
			var owner any
			if x, ok := run.Owners.Get(code); ok {
				owner = x
			}
			items = append(items, a.Response{"instrument_key": run.Specs.Value(code).Key, "owner_id": owner, "owner_epoch": run.OwnerEpochs.Value(code)})
		}
		return page(), nil
	case "protections":
		if len(parts) == 2 {
			x := run.Protections.Value(parts[1])
			if x == nil {
				return nil, fail("PROTECTION_NOT_FOUND", 404)
			}
			return x, nil
		}
		for _, x := range run.Protections.Values() {
			items = append(items, x)
		}
		return page(), nil
	case "account":
		engine := d.NewEngine(run)
		value := engine.AccountView()
		return value, engine.Err()
	case "market":
		if len(parts) == 2 {
			switch parts[1] {
			case "points":
				for _, x := range run.Points.Values() {
					point := *x
					if run.Clock.Sub(point.ObservedAt) > time.Duration(run.Policy.MaxMarketAgeSeconds)*time.Second {
						point.Quality = "STALE"
					}
					items = append(items, point)
				}
				return page(), nil
			case "candles", "trades":
				key := d.InstrumentKey{Venue: q.Get("venue"), Product: "LINEAR_PERPETUAL", InstrumentID: q.Get("instrument_id")}
				if d.Validate(key) != nil {
					return nil, fail("INVALID_REQUEST", 422)
				}
				if parts[1] == "trades" {
					for _, x := range run.MarketTrades.Values() {
						if x.InstrumentKey == key {
							items = append(items, x)
						}
					}
					return page(), nil
				}
				start, e1 := time.Parse(time.RFC3339Nano, q.Get("start"))
				end, e2 := time.Parse(time.RFC3339Nano, q.Get("end"))
				if e1 != nil || e2 != nil || q.Get("interval") == "" {
					return nil, fail("INVALID_REQUEST", 422)
				}
				for _, x := range run.Candles {
					if x.InstrumentKey == key && x.Interval == q.Get("interval") && x.Final && !x.AvailableAt.After(run.Clock) && !x.OpenAt.Before(start) && !x.CloseAt.After(end) {
						items = append(items, x)
					}
				}
				return page(), nil
			}
		}
	case "fills":
		for _, x := range run.Fills.Values() {
			items = append(items, x)
		}
		return page(), nil
	case "income":
		for _, x := range run.Incomes.Values() {
			items = append(items, x)
		}
		return page(), nil
	case "external-facts":
		for _, x := range run.ExternalFacts.Values() {
			items = append(items, x)
		}
		return page(), nil
	case "targets":
		for _, x := range run.Targets.Values() {
			items = append(items, x)
		}
		return page(), nil
	case "alerts":
		for _, x := range run.Alerts {
			value := map[string]any{"instrument": nil, "external_id": nil, "id": nil}
			for k, v := range x {
				if k == "at" || k == "code" || k == "instrument" || k == "external_id" || k == "id" {
					value[k] = v
				}
			}
			items = append(items, value)
		}
		return page(), nil
	case "audit":
		for _, x := range run.Audit {
			items = append(items, x)
		}
		return page(), nil
	case "operational-health":
		return a.Response{"issues": run.HealthIssues, "checked_at": run.HealthCheckedAt, "executor_epoch": run.ExecutorEpoch, "lease_epoch": run.LeaseEpoch, "lease_until": run.LeaseUntil}, nil
	}
	return nil, fail("NOT_FOUND", 404)
}
