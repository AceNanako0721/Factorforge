package application

import (
	"context"

	d "github.com/AceNanako0721/Factorforge/src/factorforge/trading/domain"
	"github.com/AceNanako0721/Factorforge/src/factorforge/trading/domain/decimal"
	"sort"
)

func cancelForTarget(run *d.Aggregate, target *d.Target, orders []*d.Order) error {
	if target.ExpiresAt == nil {
		return problem("TARGET_EXPIRED")
	}
	for _, order := range orders {
		if order.State == "CANCEL_PENDING" {
			continue
		}
		order.State = "CANCEL_PENDING"
		id := "target-cancel-" + RequestHash(Response{"target": target, "order": order.OrderID})[:24]
		run.Outbox = append(run.Outbox, d.OutboxItem{CommandID: id, Kind: "CANCEL", OrderID: order.OrderID, PrincipalID: target.OwnerID, ExpiresAt: *target.ExpiresAt, ExecutorEpoch: run.ExecutorEpoch, State: "PENDING"})
	}
	return nil
}
func AdvanceTargets(e *d.Engine, strict bool) error {
	run := e.Run
	codes := run.Targets.Keys()
	sort.Strings(codes)
	for _, code := range codes {
		target := run.Targets.Value(code)
		if target.State == "BLOCKED" {
			continue
		}
		block := func(reason string) { target.State = "BLOCKED"; target.Reasons = []string{reason} }
		if target.OwnerEpoch != run.OwnerEpochs.Value(code) {
			block("EXTERNAL_OWNER_EPOCH_CHANGED")
			continue
		}
		if target.ExpiresAt == nil || target.ExpiresAt.Before(run.Clock) {
			block("TARGET_EXPIRED")
			continue
		}
		spec := run.Specs.Value(code)
		if spec == nil || target.PolicyVersion == nil || *target.PolicyVersion != run.Policy.Version || target.SpecVersion == nil || *target.SpecVersion != spec.Version {
			block("TARGET_RULE_VERSION_MISMATCH")
			continue
		}
		open, older := []*d.Order{}, []*d.Order{}
		for _, o := range run.Orders.Values() {
			if o.Request.InstrumentKey.Code() == code && !d.Terminal(o.State) {
				open = append(open, o)
				if o.TargetVersion == nil || *o.TargetVersion != target.TargetVersion {
					older = append(older, o)
				}
			}
		}
		if len(older) > 0 {
			if err := cancelForTarget(run, target, older); err != nil {
				return err
			}
			target.State = "RECONCILING"
			target.Reasons = []string{"WAIT_PREVIOUS_CANCEL_CONFIRMATION"}
			continue
		}
		if len(open) > 0 {
			target.State = "ACTIVE"
			for _, o := range open {
				if o.State == "UNKNOWN" {
					target.State = "RECONCILING"
				}
			}
			continue
		}
		rejected := false
		for _, o := range run.Orders.Values() {
			if o.TargetVersion != nil && *o.TargetVersion == target.TargetVersion && o.Request.InstrumentKey.Code() == code && o.State == "REJECTED" {
				rejected = true
			}
		}
		if rejected {
			block("TARGET_ORDER_REJECTED")
			continue
		}
		actual := zero
		if position := run.Positions.Value(code); position != nil {
			actual = position.Quantity
		}
		delta := e.Math.Sub(target.TargetQuantity, actual)
		target.Reasons = []string{}
		if e.Math.Mul(actual, target.TargetQuantity).Sign() < 0 {
			delta = e.Math.Neg(actual)
			target.Reasons = append(target.Reasons, "FLATTEN_BEFORE_REVERSE")
		}
		if delta.Sign() == 0 {
			target.State = "ACTIVE"
			continue
		}
		reducing := e.Math.Mul(actual, delta).Sign() < 0
		quantity := e.Math.Abs(delta)
		if reducing {
			quantity = decimal.Min(quantity, e.Math.Abs(actual))
		}
		side := "BUY"
		if delta.Sign() < 0 {
			side = "SELL"
		}
		request := d.OrderRequest{OwnerID: target.OwnerID, InstrumentKey: target.InstrumentKey, Side: side, OrderType: "MARKET", Quantity: quantity, TimeInForce: "GTC", PositionSide: "BOTH", ReduceOnly: reducing, SpecVersion: *target.SpecVersion, ProtectionPlan: target.ProtectionPlan}
		check := d.NewEngine(run)
		reserved := check.AuthorizeOrder(request)
		if err := check.Err(); err != nil {
			if strict {
				return err
			}
			failure, ok := err.(*d.Error)
			if !ok {
				return err
			}
			block(failure.Code)
			continue
		}
		target.Generation++
		id := "ord-" + RequestHash(Response{"run": run.RunKey, "target": target, "generation": target.Generation})[:28]
		version := target.TargetVersion
		run.Orders.Set(id, &d.Order{OrderID: id, ClientOrderID: "ff-" + id[4:], Request: request, State: "RESERVED", ReservedNotional: reserved, CreatedAt: run.Clock, TargetVersion: &version})
		run.Outbox = append(run.Outbox, d.OutboxItem{CommandID: "cmd-" + id[4:], Kind: "SUBMIT", OrderID: id, PrincipalID: target.OwnerID, ExpiresAt: *target.ExpiresAt, ExecutorEpoch: run.ExecutorEpoch, State: "PENDING"})
		target.State = "ACTIVE"
	}
	return e.Err()
}
func (s *Service) SetTarget(ctx context.Context, p d.Principal, request d.TargetRequest) (Response, error) {
	return s.Command(ctx, p, request, "TARGET_SET", request, "target:write", func(e *d.Engine, r Response) error {
		run := e.Run
		code := request.InstrumentKey.Code()
		spec := run.Specs.Value(code)
		if spec == nil || request.SpecVersion != spec.Version || request.PolicyVersion != run.Policy.Version {
			return problem("TARGET_RULE_VERSION_MISMATCH", 409)
		}
		if request.OwnerEpoch != run.OwnerEpochs.Value(code) {
			return problem("OWNER_EPOCH_MISMATCH", 409)
		}
		if owner, ok := run.Owners.Get(code); ok && owner != request.OwnerID {
			return problem("OWNER_CONFLICT", 409)
		}
		if old := run.Targets.Value(code); old != nil && request.TargetVersion <= old.TargetVersion {
			return problem("TARGET_VERSION_CONFLICT", 409)
		}
		quantity := e.Math.Step(request.TargetQuantity, spec.QuantityStep, decimal.TowardZero)
		actual, pending := zero, zero
		if position := run.Positions.Value(code); position != nil {
			actual = position.Quantity
		}
		for _, o := range run.Orders.Values() {
			if o.Request.InstrumentKey == request.InstrumentKey && !d.Terminal(o.State) {
				value := e.Remaining(o)
				if o.Request.Side != "BUY" {
					value = e.Math.Neg(value)
				}
				pending = e.Math.Add(pending, value)
			}
		}
		delta := e.Math.Sub(e.Math.Sub(quantity, actual), pending)
		if e.Math.Mul(actual, quantity).Sign() < 0 {
			delta = e.Math.Neg(actual)
		}
		target := &d.Target{OwnerID: request.OwnerID, InstrumentKey: request.InstrumentKey, TargetVersion: request.TargetVersion, TargetQuantity: quantity, OwnerEpoch: request.OwnerEpoch, State: "PENDING", ExpiresAt: &request.ExpiresAtUTC, PolicyVersion: &request.PolicyVersion, SpecVersion: &request.SpecVersion, ProtectionPlan: request.ProtectionPlan, SourceDecisionID: &request.SourceDecisionID, Reasons: []string{}}
		run.Targets.Set(code, target)
		run.Owners.Set(code, request.OwnerID)
		if err := AdvanceTargets(e, true); err != nil {
			return err
		}
		merge(r, Response{"resource_id": code, "state": target.State, "target_version": request.TargetVersion, "target_quantity": quantity, "actual_quantity": actual, "pending_quantity": pending, "delta_quantity": delta, "reasons": target.Reasons})
		return e.Err()
	})
}
