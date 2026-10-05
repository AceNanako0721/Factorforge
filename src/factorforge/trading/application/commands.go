package application

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode/utf16"

	d "github.com/AceNanako0721/Factorforge/src/factorforge/trading/domain"
	"github.com/AceNanako0721/Factorforge/src/factorforge/trading/domain/decimal"
	"github.com/AceNanako0721/Factorforge/src/factorforge/trading/ports"
)

type CommandInput interface{ Metadata() d.Command }
type Response map[string]any
type Service struct {
	Store     ports.Store
	Simulator ports.Simulation
	Health    ports.Health
}

func problem(code string, status ...int) error {
	s := 422
	if len(status) > 0 {
		s = status[0]
	}
	return &d.Error{Code: code, Status: s}
}
func Require(principal d.Principal, key d.RunKey, permission string) error {
	if principal.Environment != key.Environment || principal.AccountID != key.AccountID {
		return problem("ACCOUNT_SCOPE_FORBIDDEN", 403)
	}
	if !d.Has(principal.Permissions, permission) {
		return problem("PERMISSION_FORBIDDEN", 403)
	}
	return nil
}

// Wire preserves string amounts and the original canonical sorted set / UTC
// representation. Numbers in metadata are retained as json.Number, never float.
func Wire(value any) any {
	data, err := json.Marshal(value)
	if err != nil {
		return nil
	}
	var result any
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	if decoder.Decode(&result) != nil {
		return nil
	}
	var normalize func(any, string) any
	normalize = func(value any, key string) any {
		switch v := value.(type) {
		case map[string]any:
			for k, x := range v {
				v[k] = normalize(x, k)
			}
			return v
		case []any:
			for i, x := range v {
				v[i] = normalize(x, "")
			}
			if key == "capabilities" || key == "price_roles" || key == "permissions" {
				sort.Slice(v, func(i, j int) bool { return v[i].(string) < v[j].(string) })
			}
			return v
		case string:
			if key == "clock" || key == "at" || key == "valid_from" || key == "lease_until" || strings.HasSuffix(key, "_at") || strings.HasSuffix(key, "_at_utc") {
				if at, err := time.Parse(time.RFC3339Nano, v); err == nil {
					return strings.TrimSuffix(d.PythonTime(at.UTC().Truncate(time.Microsecond)), "+00:00") + "Z"
				}
			}
			return v
		}
		return value
	}
	return normalize(result, "")
}
func RequestHash(value any) string {
	var buffer bytes.Buffer
	encoder := json.NewEncoder(&buffer)
	encoder.SetEscapeHTML(false)
	_ = encoder.Encode(Wire(value))
	var ascii strings.Builder
	for _, r := range strings.TrimSuffix(buffer.String(), "\n") {
		if r < 128 {
			ascii.WriteRune(r)
		} else if r <= 0xffff {
			ascii.WriteString("\\u" + hex4(uint16(r)))
		} else {
			high, low := utf16.EncodeRune(r)
			ascii.WriteString("\\u" + hex4(uint16(high)) + "\\u" + hex4(uint16(low)))
		}
	}
	data := []byte(ascii.String())
	hash := sha256.Sum256(data)
	return hex.EncodeToString(hash[:])
}
func hex4(v uint16) string {
	const digits = "0123456789abcdef"
	return string([]byte{digits[v>>12], digits[v>>8&15], digits[v>>4&15], digits[v&15]})
}
func ResourceID(run *d.Aggregate, prefix, identity string) string {
	return prefix + "-" + RequestHash(Response{"run": run.RunKey, "version": run.Version, "clock": run.Clock, "seed": run.SimConfig.Seed, "identity": identity})[:28]
}
func Audit(run *d.Aggregate, action string, principal d.Principal, requestID string, detail any) {
	event := Response{"sequence": len(run.Audit) + 1, "action": action, "principal_id": principal.PrincipalID, "request_id": requestID, "at": run.Clock, "detail": Wire(detail)}
	data, _ := json.Marshal(Wire(event))
	var object d.Object
	json.Unmarshal(data, &object)
	run.Audit = append(run.Audit, object)
}
func responseObject(value d.Object) Response {
	data, _ := json.Marshal(value)
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	var result Response
	decoder.Decode(&result)
	return result
}
func merge(to Response, from Response) {
	for k, v := range from {
		to[k] = v
	}
}
func RunView(run *d.Aggregate) Response {
	owners := run.Owners.Values()
	sort.Strings(owners)
	unique := make([]string, 0)
	for _, v := range owners {
		if len(unique) == 0 || unique[len(unique)-1] != v {
			unique = append(unique, v)
		}
	}
	reasons := append([]string{}, run.RecoveryIssues...)
	reasons = append(reasons, run.RiskLocks...)
	return Wire(Response{"resource_id": run.RunKey.RunID, "run_key": run.RunKey, "aggregate_version": run.Version, "execution_mode": run.ExecutionMode, "state": run.State, "policy_version": run.Policy.Version, "executor_epoch": run.ExecutorEpoch, "active_owners": unique, "reasons": reasons}).(map[string]any)
}
func (s *Service) CreateRun(ctx context.Context, principal d.Principal, body d.CreateRun) (Response, error) {
	if err := Require(principal, body.RunKey, "run:create"); err != nil {
		return nil, err
	}
	if body.RunKey.Environment != "SIM" {
		return nil, problem("LIVE_CAPABILITIES_UNVERIFIED", 423)
	}
	if body.ExpiresAtUTC.Before(body.Clock) {
		return nil, problem("REQUEST_EXPIRED", 409)
	}
	if body.AccountPolicy.ValidFrom.After(body.Clock) {
		return nil, problem("POLICY_NOT_EFFECTIVE")
	}
	identity := principal.PrincipalID + ":" + body.IdempotencyKey
	fingerprint := RequestHash(body)
	old, err := s.Store.Read(ctx, body.RunKey)
	if err == nil {
		saved, ok := old.Dedup.Get(identity)
		if ok {
			prior := responseObject(saved)
			if prior["hash"] == fingerprint {
				return prior["response"].(map[string]any), nil
			}
		}
		return nil, problem("CREATE_IDEMPOTENCY_CONFLICT", 409)
	}
	var failure *d.Error
	if !errors.As(err, &failure) || failure.Code != "RUN_NOT_FOUND" {
		return nil, err
	}
	day, err := d.RiskDay(body.Clock, body.AccountPolicy.RiskDayZone)
	if err != nil {
		return nil, err
	}
	run := &d.Aggregate{RunKey: body.RunKey, ExecutionMode: body.ExecutionMode, State: "NORMAL", Policy: body.AccountPolicy, SimConfig: body.SimConfig, InitialCash: body.InitialCash, Currency: body.Currency, Cash: body.InitialCash, Clock: body.Clock, RiskDay: day, DayStartEquity: body.InitialCash, PeakEquity: body.InitialCash, ExecutorEpoch: 1}
	d.InitCollections(run)
	response := RunView(run)
	saved, _ := json.Marshal(Response{"hash": fingerprint, "response": response})
	var object d.Object
	json.Unmarshal(saved, &object)
	run.Dedup.Set(identity, object)
	Audit(run, "RUN_CREATED", principal, body.RequestID, response)
	if err = s.Store.Create(ctx, run); err != nil {
		return nil, err
	}
	return response, nil
}

// Command performs deduplication before optimistic version/expiry checks so a
// confirmed retry returns its original acceptance even after state advances.
func (s *Service) Command(ctx context.Context, principal d.Principal, commandInput CommandInput, action string, payload any, permission string, apply func(*d.Engine, Response) error) (Response, error) {
	command := commandInput.Metadata()
	if err := Require(principal, command.RunKey, permission); err != nil {
		return nil, err
	}
	var response Response
	err := s.Store.Transaction(ctx, command.RunKey, func(run *d.Aggregate) error {
		identity := principal.PrincipalID + ":" + command.IdempotencyKey
		fingerprint := RequestHash(Response{"action": action, "command": commandInput, "payload": payload})
		if saved, ok := run.Dedup.Get(identity); ok {
			prior := responseObject(saved)
			if prior["hash"] != fingerprint {
				return problem("IDEMPOTENCY_CONFLICT", 409)
			}
			response = prior["response"].(map[string]any)
			return nil
		}
		if command.ExpectedVersion != run.Version {
			return problem("VERSION_CONFLICT", 409)
		}
		if command.ExpiresAtUTC.Before(run.Clock) {
			return problem("REQUEST_EXPIRED", 409)
		}
		if s.Health != nil {
			issues, err := s.Health.Check(ctx, run)
			if err != nil {
				return err
			}
			run.HealthIssues = issues
			at := run.Clock
			run.HealthCheckedAt = &at
		}
		response = Response{}
		engine := d.NewEngine(run)
		if err := apply(engine, response); err != nil {
			return err
		}
		if err := engine.Err(); err != nil {
			return err
		}
		run.Version++
		response["aggregate_version"] = run.Version
		response["correlation_id"] = command.RequestID
		response["accepted_at_utc"] = run.Clock
		response["reason_codes"] = []string{}
		Audit(run, action, principal, command.RequestID, response)
		response = Wire(response).(map[string]any)
		saved, _ := json.Marshal(Response{"hash": fingerprint, "response": response})
		var object d.Object
		json.Unmarshal(saved, &object)
		run.Dedup.Set(identity, object)
		return nil
	})
	return response, err
}
func OrderView(order *d.Order) Response {
	e := d.NewEngine(nil)
	return Response{"order_id": order.OrderID, "external_order_id": order.ExternalOrderID, "client_order_id": order.ClientOrderID, "owner_id": order.Request.OwnerID, "instrument_key": order.Request.InstrumentKey, "side": order.Request.Side, "requested_quantity": order.Request.Quantity, "filled_quantity": order.FilledQuantity, "remaining_quantity": e.Remaining(order), "average_fill_price": order.AverageFillPrice, "state": order.State, "reduce_only": order.Request.ReduceOnly, "spec_version": order.Request.SpecVersion}
}
func (s *Service) SubmitOrder(ctx context.Context, p d.Principal, command CommandInput, request d.OrderRequest, targetVersion *int64) (Response, error) {
	c := command.Metadata()
	return s.Command(ctx, p, command, "ORDER_SUBMIT", request, "order:write", func(e *d.Engine, r Response) error {
		reservation := e.AuthorizeOrder(request)
		if e.Err() != nil {
			return e.Err()
		}
		run := e.Run
		id := ResourceID(run, "ord", p.PrincipalID+":"+c.IdempotencyKey)
		order := &d.Order{OrderID: id, ClientOrderID: d.StableID("ff-", id), Request: request, State: "RESERVED", ReservedNotional: reservation, CreatedAt: run.Clock, TargetVersion: targetVersion}
		run.Owners.Set(request.InstrumentKey.Code(), request.OwnerID)
		run.Orders.Set(id, order)
		run.Outbox = append(run.Outbox, d.OutboxItem{CommandID: ResourceID(run, "cmd", id+":submit"), Kind: "SUBMIT", OrderID: id, PrincipalID: p.PrincipalID, ExpiresAt: c.ExpiresAtUTC, ExecutorEpoch: run.ExecutorEpoch, State: "PENDING"})
		r["resource_id"] = id
		merge(r, OrderView(order))
		return nil
	})
}
func (s *Service) CancelOrder(ctx context.Context, p d.Principal, command CommandInput, id string) (Response, error) {
	c := command.Metadata()
	return s.Command(ctx, p, command, "ORDER_CANCEL", Response{"order_id": id}, "order:write", func(e *d.Engine, r Response) error {
		run := e.Run
		order := run.Orders.Value(id)
		if order == nil {
			return problem("ORDER_NOT_FOUND", 404)
		}
		if !d.Terminal(order.State) {
			order.State = "CANCEL_PENDING"
			run.Outbox = append(run.Outbox, d.OutboxItem{CommandID: ResourceID(run, "cmd", id+":cancel"), Kind: "CANCEL", OrderID: id, PrincipalID: p.PrincipalID, ExpiresAt: c.ExpiresAtUTC, ExecutorEpoch: run.ExecutorEpoch, State: "PENDING"})
		}
		r["resource_id"] = id
		r["state"] = order.State
		return nil
	})
}
func (s *Service) Read(ctx context.Context, p d.Principal, key d.RunKey) (*d.Aggregate, error) {
	if err := Require(p, key, "read"); err != nil {
		return nil, err
	}
	return s.Store.Read(ctx, key)
}
func (s *Service) RecordRejection(ctx context.Context, p d.Principal, code, action string) error {
	if p.Environment != s.Store.Environment() {
		return nil
	}
	key, err := s.Store.BoundRun(ctx, p.AccountID)
	if err != nil || key == nil {
		return err
	}
	return s.Store.Transaction(ctx, *key, func(run *d.Aggregate) error {
		detail := Response{"code": code, "action": action}
		data, _ := json.Marshal(detail)
		var object d.Object
		json.Unmarshal(data, &object)
		run.RejectedRequests = append(run.RejectedRequests, object)
		Audit(run, "REQUEST_REJECTED", p, "rejection-"+formatInt(int64(len(run.Audit))), detail)
		return nil
	})
}

var zero decimal.Value
var one, _ = decimal.Parse("1")

func formatInt(v int64) string { return strconv.FormatInt(v, 10) }
