package application

import (
	"context"
	"errors"
	d "github.com/AceNanako0721/Factorforge/src/factorforge/strategy/domain"
	"github.com/AceNanako0721/Factorforge/src/factorforge/strategy/ports"
	"github.com/AceNanako0721/Factorforge/src/factorforge/trading/api/dto"
	"time"
)

type StopArchiveReader interface {
	ReadStopDirective(context.Context, string, string) (*d.StopDirectiveRecord, error)
	ListStopDirectives(context.Context, string, string, string, int) ([]*d.StopDirectiveRecord, error)
}

func directivePrincipal(identity d.Identity) string {
	if w := identity.Worker(); w != nil {
		return w.WorkloadID
	}
	if p, ok := identity.(d.PublicPrincipal); ok {
		return p.PrincipalID
	}
	return ""
}
func ValidateDirectiveSnapshot(v d.StopDirective, obj *d.ObservedObject, snap *d.TradingSnapshot, p *d.Policy, at time.Time) error {
	if snap == nil || !snap.LowerCompat {
		return &d.Error{Code: "P1_LOWER_COMPAT_REQUIRED", Status: 423}
	}
	spec := snap.Specs[obj.ObjectID]
	if !d.Has(d.Strings(spec["price_roles"]), v.TriggerKind) || !d.Has([]string{"MARK", "LAST"}, v.TriggerKind) {
		return &d.Error{Code: "STOP_DIRECTIVE_TRIGGER_UNSUPPORTED", Status: 422}
	}
	point, ok := snap.TriggerPrices[obj.ObjectID][v.TriggerKind]
	if !ok || point.Quality != "VALID" || point.AvailableAt.After(at) || at.Sub(point.AvailableAt).Seconds() > float64(p.MaxPriceAgeSeconds) {
		return &d.Error{Code: "STOP_DIRECTIVE_PRICE_UNUSABLE", Status: 423}
	}
	if v.Kind == "POSITION_STOP" {
		q := snap.Actual[obj.ObjectID]
		if q.Sign() == 0 || d.DirectionName(q) != v.PositionDirection {
			return &d.Error{Code: "STOP_DIRECTIVE_DIRECTION_MISMATCH", Status: 422}
		}
		if v.Mode == "VENUE" && d.Math().Step(*v.TriggerPrice, d.Amount(spec["price_tick"]), dto.TowardZero).Cmp(*v.TriggerPrice) != 0 {
			return &d.Error{Code: "STOP_DIRECTIVE_TICK_MISMATCH", Status: 422}
		}
		if q.Sign() > 0 && v.TriggerPrice.Cmp(point.Price) >= 0 || q.Sign() < 0 && v.TriggerPrice.Cmp(point.Price) <= 0 {
			return &d.Error{Code: "STOP_DIRECTIVE_ALREADY_CROSSED", Status: 423}
		}
	}
	return nil
}
func (s Service) SubmitStopDirective(ctx context.Context, identity d.Identity, request d.StopDirectiveCommand) (any, error) {
	if identity == nil {
		return nil, &d.Error{Code: "UNAUTHENTICATED", Status: 401}
	}
	if request.Mode == "" {
		request.Mode = "VENUE"
	}
	now := s.Clock.Now()
	value, err := s.Command(ctx, identity, request.Command, d.ObjectWrite, request, request.ObjectID, func(state *d.StrategyState) (any, error) {
		obj := state.Objects.Value(request.ObjectID)
		if obj == nil {
			return nil, &d.Error{Code: "OBJECT_NOT_FOUND", Status: 404}
		}
		if err := d.ValidateStopDirective(request.StopDirective); err != nil {
			return nil, err
		}
		principal := directivePrincipal(identity)
		executable := identity.Worker() != nil
		if !d.WriterAllowed(state, principal, request.StopDirective, now) {
			return nil, &d.Error{Code: "STOP_DIRECTIVE_WRITE_FORBIDDEN", Status: 403}
		}
		heads := &state.StopDirectiveVersions
		collection := &state.StopDirectives
		if !executable {
			collection = &state.ResearchStopDirectives
			heads = &state.ResearchStopVersions
		}
		key := d.StopDirectiveKey(request.StopDirective, principal, !executable)
		hash := d.Digest(request.StopDirective)
		var oldID *d.StopDirectiveRecord
		for _, v := range collection.Values() {
			if v.Directive.DirectiveID == request.DirectiveID {
				oldID = v
			}
		}
		if oldID == nil {
			if reader, ok := s.Store.(StopArchiveReader); ok {
				var err error
				oldID, err = reader.ReadStopDirective(ctx, state.InstanceID, request.DirectiveID)
				if err != nil {
					return nil, err
				}
			}
		}
		if oldID != nil {
			if oldID.ContentHash != hash || oldID.PrincipalID != principal {
				return nil, &d.Error{Code: "STOP_DIRECTIVE_CONTENT_CONFLICT", Status: 409}
			}
			return oldID.Receipt, nil
		}
		if request.Version <= heads.Value(key) {
			return nil, &d.Error{Code: "STOP_DIRECTIVE_VERSION_CONFLICT", Status: 409}
		}
		if old := collection.Value(key); old != nil && request.Version <= old.Directive.Version {
			return nil, &d.Error{Code: "STOP_DIRECTIVE_VERSION_CONFLICT", Status: 409}
		}
		expires := request.EffectiveAt.Add(time.Duration(request.ValidForSeconds) * time.Second)
		if request.EffectiveAt.After(now) {
			return nil, &d.Error{Code: "STOP_DIRECTIVE_NOT_EFFECTIVE", Status: 422}
		}
		if !now.Before(expires) {
			return nil, &d.Error{Code: "STOP_DIRECTIVE_EXPIRED", Status: 422}
		}
		var snap *d.TradingSnapshot
		if executable {
			if s.Trading == nil {
				return nil, &d.Error{Code: "STOP_DIRECTIVE_PRICE_UNUSABLE", Status: 423}
			}
			var err error
			snap, err = s.Trading.Snapshot(ctx, []*d.ObservedObject{obj})
			if err != nil {
				return nil, err
			}
			if err = ValidateDirectiveSnapshot(request.StopDirective, obj, snap, state.Policies.Value(obj.TimePolicyVersion), now); err != nil {
				return nil, err
			}
		}
		receipt := d.StopDirectiveReceipt{DirectiveID: request.DirectiveID, ObjectID: request.ObjectID, Version: request.Version, State: "RESEARCH_ONLY", ReceivedAt: now, EffectiveAt: request.EffectiveAt, ExpiresAt: expires, ReasonCodes: request.ReasonCodes, CurrentVersion: request.Version, ApplicationState: "NOT_APPLIED"}
		cycle := ""
		if executable {
			receipt.State = "ACCEPTED"
			receipt.AcceptedAt = &now
			cycle = d.Text(snap.Positions[obj.ObjectID]["position_cycle_id"])
		}
		heads.Set(key, request.Version)
		collection.Set(key, &d.StopDirectiveRecord{Directive: request.StopDirective, Receipt: receipt, ContentHash: hash, PrincipalID: principal, Executable: executable, PositionCycleID: cycle})
		state.AppendAudit(map[string]any{"action": "SUBMIT_STOP_DIRECTIVE", "directive_id": request.DirectiveID, "object_id": request.ObjectID, "at": d.ISO(now)})
		return receipt, nil
	})
	var failure *d.Error
	if errors.As(err, &failure) && failure.Status == 403 {
		return nil, &d.Error{Code: "STOP_DIRECTIVE_WRITE_FORBIDDEN", Status: 403}
	}
	return value, err
}
func LatestStopDirective(state *d.StrategyState, obj *d.ObservedObject, snap *d.TradingSnapshot, kind, direction string, at time.Time) *d.StopDirectiveRecord {
	var result *d.StopDirectiveRecord
	for _, r := range state.StopDirectives.Values() {
		v := r.Directive
		if !r.Executable || v.ObjectID != obj.ObjectID || v.Kind != kind || v.PositionDirection != direction || r.Receipt.AcceptedAt == nil || r.Receipt.AcceptedAt.After(at) || v.EffectiveAt.After(at) || !at.Before(r.Receipt.ExpiresAt) || !d.WriterAllowed(state, r.PrincipalID, v, at) {
			continue
		}
		if kind == "POSITION_STOP" && r.PositionCycleID != d.Text(snap.Positions[obj.ObjectID]["position_cycle_id"]) {
			continue
		}
		if ValidateDirectiveSnapshot(v, obj, snap, state.Policies.Value(obj.TimePolicyVersion), at) != nil {
			continue
		}
		if result == nil || v.Version > result.Directive.Version {
			result = r
		}
	}
	return result
}
func StopPlanForDirective(v d.StopDirective, trigger, quantity d.Decimal, rules d.ProductRules, p *d.Policy) *d.StopPlan {
	plan := &d.StopPlan{StrategyProtectionMode: v.Mode, TriggerKind: v.TriggerKind, MaxSlippageBPS: d.Math().Mul(p.SlippageFraction, d.Int(10000)), SpecVersion: rules.Version}
	if v.Mode == "UPPER_MANAGED" {
		plan.ManagedStopPrice = &trigger
		plan.OvershootAllowance = d.Clone(v.OvershootAllowance)
	} else {
		plan.TriggerPrice = trigger
		plan.CoveredQuantity = quantity.Abs()
		plan.ExitOrderType = "MARKET"
	}
	return plan
}
func SizeDirective(q, price d.Decimal, rules d.ProductRules, r *d.StopDirectiveRecord, p *d.Policy) (d.StopResult, error) {
	v := r.Directive
	m := d.Math()
	distance := *v.InitialStopDistance
	fraction := m.Div(distance, price)
	if fraction.Cmp(p.MicroDistance) < 0 || fraction.Cmp(p.MaxStopFraction) > 0 {
		return d.StopResult{Reason: "STOP_DISTANCE_UNAPPROVED"}, nil
	}
	sign := d.One()
	round := dto.Ceiling
	if q.Sign() < 0 {
		sign = sign.Neg()
		round = dto.Floor
	}
	trigger := m.Sub(price, m.Mul(sign, distance))
	if v.Mode == "VENUE" {
		trigger = m.Step(trigger, rules.PriceTick, round)
	}
	if trigger.Sign() <= 0 || m.Mul(sign, m.Sub(price, trigger)).Sign() <= 0 {
		return d.StopResult{Reason: "STOP_TICK_UNAVAILABLE"}, nil
	}
	roundedFraction := m.Div(m.Abs(m.Sub(price, trigger)), price)
	if roundedFraction.Cmp(p.MicroDistance) < 0 || roundedFraction.Cmp(p.MaxStopFraction) > 0 {
		return d.StopResult{Reason: "STOP_DISTANCE_UNAPPROVED"}, nil
	}
	allowance := d.Zero()
	if v.OvershootAllowance != nil {
		allowance = *v.OvershootAllowance
	}
	unit := m.Mul(rules.Multiplier, m.Add(m.Add(m.Abs(m.Sub(price, trigger)), allowance), m.Mul(price, m.Add(m.Add(p.FeeRate, p.SlippageFraction), p.GapFraction))))
	sized := m.Mul(sign, m.Step(d.Min(q.Abs(), m.Div(p.ObjectLossBudget, unit)), rules.QuantityStep, dto.TowardZero))
	if m.Mul(m.Mul(sized.Abs(), price), rules.Multiplier).Cmp(rules.MinNotional) < 0 {
		return d.StopResult{Reason: "MINIMUM_UNIT"}, nil
	}
	return d.StopResult{Quantity: sized, Plan: StopPlanForDirective(v, trigger, sized, rules, p), Reason: "STOP_DIRECTIVE_SIZED", Fraction: fraction}, m.Err()
}

func findDirective(state *d.StrategyState, id string) *d.StopDirectiveRecord {
	for _, r := range state.StopDirectives.Values() {
		if r.Directive.DirectiveID == id {
			return r
		}
	}
	for _, row := range state.Outbox.Values() {
		if row.StopDirective != nil && row.StopDirective.Directive.DirectiveID == id {
			return row.StopDirective
		}
	}
	return nil
}
func versionForDecision(state *d.StrategyState, id string) int {
	if row := state.Outbox.Value(id); row != nil {
		return row.TargetVersion
	}
	return 0
}
func projectedQuantity(current, target, alpha d.Decimal) d.Decimal {
	m := d.Math()
	if current.Sign() != 0 && target.Sign() != 0 && target.Sign() != current.Sign() {
		if alpha.Sign() == 0 {
			return current
		}
		return m.Mul(target, alpha)
	}
	return m.Add(current, m.Mul(alpha, m.Sub(target, current)))
}
func PreservedStopPlan(state *d.StrategyState, obj *d.ObservedObject, snap *d.TradingSnapshot, target d.Decimal) *d.StopPlan {
	if t := snap.Targets[obj.ObjectID]; t != nil && d.Amount(t["target_quantity"]).Sign() == target.Sign() && t["protection_plan"] != nil {
		var plan d.StopPlan
		raw, _ := d.Marshal(t["protection_plan"])
		if d.DecodeJSON(raw, &plan) == nil {
			return &plan
		}
	}
	var latest *d.TargetOutbox
	for _, row := range state.Outbox.Values() {
		if row.Decision.ObjectID == obj.ObjectID && row.Decision.TargetQuantity.Sign() == target.Sign() && row.Decision.StopPlan != nil && !d.Has([]string{"REJECTED", "EXPIRED"}, row.State) && (latest == nil || row.TargetVersion > latest.TargetVersion) {
			latest = row
		}
	}
	if latest != nil {
		return d.Clone(latest.Decision.StopPlan)
	}
	if effective := state.LastEffectiveStrategyStop.Value(obj.ObjectID); effective != nil && effective.PositionDirection == d.DirectionName(target) {
		p := effective.Plan
		return &p
	}
	return nil
}
func StopAlreadyEffective(snap *d.TradingSnapshot, obj *d.ObservedObject, plan *d.StopPlan) bool {
	pos := snap.Positions[obj.ObjectID]
	mode := d.Text(pos["effective_mode"])
	if mode == "" {
		mode = "VENUE"
	}
	if plan.StrategyProtectionMode == "UPPER_MANAGED" {
		decl := d.Object(pos["strategy_declaration"])
		return mode == "UPPER_MANAGED" && plan.ManagedStopPrice != nil && decl["managed_stop_price"] != nil && d.Amount(decl["managed_stop_price"]).Cmp(*plan.ManagedStopPrice) == 0 && decl["overshoot_allowance"] != nil && d.Amount(decl["overshoot_allowance"]).Cmp(*plan.OvershootAllowance) == 0 && d.Text(decl["trigger_kind"]) == plan.TriggerKind && d.Text(pos["protection_state"]) == "ACTIVE_VERIFIED" && d.Text(pos["transition_state"]) == "STABLE"
	}
	if mode != "VENUE" {
		return false
	}
	for _, p := range snap.Protections[obj.ObjectID] {
		v := d.Object(p["plan"])
		if d.Text(p["role"]) == "STRATEGY" && d.Text(p["state"]) == "ACTIVE_VERIFIED" && d.Text(p["position_direction"]) == d.DirectionName(snap.Actual[obj.ObjectID]) && d.Text(p["position_cycle_id"]) == d.Text(pos["position_cycle_id"]) && v["trigger_price"] != nil && d.Text(v["trigger_kind"]) == plan.TriggerKind && d.Amount(v["trigger_price"]).Cmp(plan.TriggerPrice) == 0 && d.Amount(v["covered_quantity"]).Cmp(snap.Actual[obj.ObjectID].Abs()) == 0 {
			return true
		}
	}
	return false
}
func TrackStopReceipts(state *d.StrategyState, obj *d.ObservedObject, snap *d.TradingSnapshot, at time.Time) {
	records := append([]*d.StopDirectiveRecord(nil), state.StopDirectives.Values()...)
	for _, row := range state.Outbox.Values() {
		if row.StopDirective != nil && findActiveDirective(state, row.StopDirective.Directive.DirectiveID) == nil {
			records = append(records, row.StopDirective)
		}
	}
	for _, r := range records {
		if r.Directive.ObjectID != obj.ObjectID || !r.Executable {
			continue
		}
		if !at.Before(r.Receipt.ExpiresAt) {
			r.Receipt.State = "EXPIRED"
		}
		if r.Receipt.SourceDecisionID == nil {
			continue
		}
		out := state.Outbox.Value(*r.Receipt.SourceDecisionID)
		if out == nil || out.State != "ACK" || out.Decision.StopPlan == nil {
			continue
		}
		if !StopAlreadyEffective(snap, obj, out.Decision.StopPlan) {
			continue
		}
		plan := *out.Decision.StopPlan
		receipt := &r.Receipt
		applicationState := "VERIFIED"
		if plan.StrategyProtectionMode == "UPPER_MANAGED" {
			applicationState = "RECORDED"
		}
		pos := snap.Positions[obj.ObjectID]
		if plan.StrategyProtectionMode == "UPPER_MANAGED" && d.Number(d.Object(pos["strategy_declaration"])["target_version"]) != out.TargetVersion {
			continue
		}
		receipt.VerifiedAt = d.Ptr(at)
		if plan.StrategyProtectionMode != "UPPER_MANAGED" {
			for _, p := range snap.Protections[obj.ObjectID] {
				if d.Text(p["role"]) == "STRATEGY" && d.Text(p["state"]) == "ACTIVE_VERIFIED" && d.Number(p["source_target_version"]) == out.TargetVersion {
					receipt.ProtectionID = d.Ptr(d.Text(p["protection_id"]))
					break
				}
			}
			if receipt.ProtectionID == nil {
				continue
			}
		}
		receipt.ApplicationState = applicationState
		out.StopDirective = d.Clone(r)
		state.LastEffectiveStrategyStop.Set(obj.ObjectID, &d.EffectiveStrategyStop{DirectiveID: r.Directive.DirectiveID, TargetVersion: out.TargetVersion, PositionDirection: d.DirectionName(snap.Actual[obj.ObjectID]), PositionCycleID: d.Text(pos["position_cycle_id"]), ProtectionID: receipt.ProtectionID, Plan: plan, VerifiedAt: at})
	}
	expireStopIndex(state, obj.ObjectID, at)
}

func stopWriterGrant(ctx context.Context, store ports.Store, state *d.StrategyState, record *d.StopDirectiveRecord) error {
	cap := d.SignalSIM
	if state.Environment == "LIVE" {
		cap = d.SignalLIVE
	}
	return store.CheckWorkload(ctx, d.WorkloadIdentity{WorkloadID: record.PrincipalID, InstanceID: state.InstanceID, Environment: state.Environment, ObjectIDs: []string{record.Directive.ObjectID}, Capabilities: []d.WorkloadCapability{cap}}, record.Directive.ObjectID)
}

func findActiveDirective(state *d.StrategyState, id string) *d.StopDirectiveRecord {
	for _, r := range state.StopDirectives.Values() {
		if r.Directive.DirectiveID == id {
			return r
		}
	}
	return nil
}
func StopRecordView(state *d.StrategyState, record *d.StopDirectiveRecord) *d.StopDirectiveRecord {
	copy := d.Clone(record)
	if row := findDirective(state, record.Directive.DirectiveID); row != nil {
		copy.Receipt = row.Receipt
		if d.Has([]string{"SUPERSEDED", "EXPIRED"}, record.Receipt.State) {
			copy.Receipt.State = record.Receipt.State
		}
	}
	return copy
}

func ValidTargetStop(snap *d.TradingSnapshot, obj *d.ObservedObject, plan *d.StopPlan, target d.Decimal, p *d.Policy, at time.Time) bool {
	if plan == nil || plan.SpecVersion != d.Text(snap.Specs[obj.ObjectID]["version"]) {
		return false
	}
	point, ok := snap.TriggerPrices[obj.ObjectID][plan.TriggerKind]
	if !ok {
		if plan.TriggerKind == "MARK" {
			point, ok = snap.Samples[obj.ObjectID]
		}
	}
	if !ok && obj.Environment == "SIM" && plan.TriggerKind == "MARK" {
		for _, bar := range snap.Bars[obj.ObjectID] {
			if bar.Final && bar.Quality == "VALID" && !bar.CloseAt.After(at) && !bar.AvailableAt.After(at) && (!ok || bar.AvailableAt.After(point.AvailableAt)) {
				point = d.MarketSample{Price: bar.Close, AvailableAt: bar.AvailableAt, Quality: "VALID"}
				ok = true
			}
		}
	}
	if !ok || point.Quality != "VALID" || point.AvailableAt.After(at) || at.Sub(point.AvailableAt).Seconds() > float64(p.MaxPriceAgeSeconds) {
		return false
	}
	trigger := plan.TriggerPrice
	if plan.StrategyProtectionMode == "UPPER_MANAGED" {
		if plan.ManagedStopPrice == nil || plan.OvershootAllowance == nil {
			return false
		}
		trigger = *plan.ManagedStopPrice
	}
	return trigger.Sign() > 0 && (target.Sign() > 0 && trigger.Cmp(point.Price) < 0 || target.Sign() < 0 && trigger.Cmp(point.Price) > 0)
}

func expireStopIndex(state *d.StrategyState, object string, at time.Time) {
	for index, collection := range []*d.Ordered[*d.StopDirectiveRecord]{&state.StopDirectives, &state.ResearchStopDirectives} {
		heads := &state.StopDirectiveVersions
		if index == 1 {
			heads = &state.ResearchStopVersions
		}
		for _, key := range collection.Keys() {
			record := collection.Value(key)
			if record.Directive.ObjectID != object || at.Before(record.Receipt.ExpiresAt) {
				continue
			}
			copy := d.Clone(record)
			copy.Receipt.State = "EXPIRED"
			state.StopDirectiveArchiveWrites = append(state.StopDirectiveArchiveWrites, copy)
			if heads.Value(key) < record.Directive.Version {
				heads.Set(key, record.Directive.Version)
			}
			collection.Delete(key)
		}
	}
}
