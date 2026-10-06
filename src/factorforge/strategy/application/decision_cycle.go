// Account-coordinated cycles commit contributions, labels, reservations and
// decisions atomically. Exchange execution remains exclusively owned by P1.
package application

import (
	"context"
	"errors"
	d "github.com/AceNanako0721/Factorforge/src/factorforge/strategy/domain"
	"github.com/AceNanako0721/Factorforge/src/factorforge/strategy/ports"
	"math"
	"sort"
	"time"
)

type DecisionCycle struct {
	Store   ports.Store
	Trading ports.Trading
	Clock   ports.Clock
}
type dueObject struct {
	obj       *d.ObservedObject
	p         *d.Policy
	sample    *d.MarketSample
	target    d.Decimal
	stop      *d.StopPlan
	stress    d.Decimal
	view      d.PoolView
	level     int
	raw       d.Decimal
	reasons   []string
	id        string
	scheduled time.Time
}

func unknownDelivery(s *d.StrategyState, id string) bool {
	for _, item := range s.Outbox.Values() {
		if item.Decision.ObjectID == id && d.Has([]string{"PENDING", "DELIVERY_UNKNOWN"}, item.State) {
			return true
		}
	}
	return false
}
func riskGroup(spec map[string]any) string {
	v := d.Text(spec["risk_group"])
	if v == "" {
		v = "DEFAULT"
	}
	return v
}
func sortedObjects(objects []*d.ObservedObject) []*d.ObservedObject {
	r := append([]*d.ObservedObject(nil), objects...)
	sort.SliceStable(r, func(i, j int) bool { return r[i].ObjectID < r[j].ObjectID })
	return r
}
func (c DecisionCycle) Snapshots(ctx context.Context, identity d.WorkloadIdentity, before *d.StrategyState) (d.Ordered[[]*d.ObservedObject], map[string]*d.TradingSnapshot, error) {
	groups := d.Ordered[[]*d.ObservedObject]{}
	for _, obj := range before.Objects.Values() {
		if !d.Has(identity.ObjectIDs, obj.ObjectID) {
			continue
		}
		if err := c.Store.CheckWorkload(ctx, identity, obj.ObjectID); err != nil {
			return groups, nil, err
		}
		key := d.Digest(obj.TradingRunKey)
		groups.Set(key, append(groups.Value(key), obj))
	}
	snaps := map[string]*d.TradingSnapshot{}
	for _, key := range groups.Keys() {
		snap, err := c.Trading.Snapshot(ctx, sortedObjects(groups.Value(key)))
		if err != nil {
			return groups, nil, err
		}
		snaps[key] = snap
	}
	return groups, snaps, nil
}

func (c DecisionCycle) Tick(ctx context.Context, identity d.Identity, requested *time.Time) ([]*d.DecisionView, error) {
	worker := identity.Worker()
	if worker == nil {
		return nil, &d.Error{Code: "WORKLOAD_IDENTITY_REQUIRED", Status: 403}
	}
	at := c.Clock.Now()
	if requested != nil {
		at = *requested
	}
	if at.After(c.Clock.Now()) {
		return nil, &d.Error{Code: "CYCLE_TIME_NOT_YET_AVAILABLE", Status: 422}
	}
	before, err := c.Store.Read(ctx, identity.Instance())
	if err != nil {
		return nil, err
	}
	if err = Authorize(before, identity, d.Query, ""); err != nil {
		return nil, err
	}
	groups, snapshots, err := c.Snapshots(ctx, *worker, before)
	if err != nil {
		return nil, err
	}
	for _, snap := range snapshots {
		if snap.At.After(at) {
			return nil, &d.Error{Code: "FUTURE_TRADING_SNAPSHOT", Status: 422}
		}
	}
	outputs := []*d.DecisionView{}
	err = c.Store.Transaction(ctx, identity.Instance(), func(state *d.StrategyState) error {
		if state.Version != before.Version {
			return &d.Error{Code: "CYCLE_SNAPSHOT_CONFLICT", Status: 409}
		}
		m := d.Math()
		keys := groups.Keys()
		sort.Strings(keys)
		for _, key := range keys {
			snap := snapshots[key]
			due := []*dueObject{}
			base := []d.BaseRisk{}
			for _, risk := range snap.OtherExposures {
				base = append(base, d.BaseRisk{Value: risk.Value, Stress: risk.Stress, Group: risk.Group})
			}
			rows := []d.Candidate{}
			for _, previous := range sortedObjects(groups.Value(key)) {
				obj := state.Objects.Value(previous.ObjectID)
				p := state.Policies.Value(obj.TimePolicyVersion)
				params := state.Parameters.Value(obj.ParameterVersion)
				if p == nil || params == nil {
					return &d.Error{Code: "OBJECT_POLICY_MISSING", Status: 423}
				}
				scheduled := p.WindowAnchor.Add(time.Duration(int64(math.Floor(at.Sub(p.WindowAnchor).Seconds()/float64(p.CycleSeconds)))*int64(p.CycleSeconds)) * time.Second)
				if obj.LastCutoff != nil && at.Before(*obj.LastCutoff) {
					return &d.Error{Code: "CLOCK_REWIND", Status: 422}
				}
				if err := d.VerifyLedger(state.Sentiment(), p.NumericalTolerance); err != nil {
					return err
				}
				actual, pending := snap.Actual[obj.ObjectID], snap.Pending[obj.ObjectID]
				obj.ActualQuantity = actual
				obj.PendingQuantity = pending
				epoch, ok := snap.OwnerEpochs[obj.ObjectID]
				if !ok {
					return &d.Error{Code: "OWNER_SNAPSHOT_MISSING", Status: 423}
				}
				obj.RecoveryState = "RECOVERY_REQUIRED"
				if epoch == obj.OwnerEpoch && !snap.ExternalChange && snap.RunState == "NORMAL" {
					obj.RecoveryState = "NORMAL"
				}
				var sample *d.MarketSample
				if v, ok := snap.Samples[obj.ObjectID]; ok {
					copy := v
					sample = &copy
					if at.Sub(sample.AvailableAt).Seconds() > float64(p.MaxPriceAgeSeconds) {
						sample.Quality = "STALE"
					}
				}
				bindings := state.FactorManifests.Value(obj.PriceProxyBinding)
				if sample != nil {
					observations := []map[string]any{}
					for _, v := range d.Rows(bindings["observations"]) {
						if !d.At(v["available_at"]).After(at) {
							observations = append(observations, v)
						}
					}
					sort.SliceStable(observations, func(i, j int) bool {
						return d.At(observations[i]["available_at"]).Before(d.At(observations[j]["available_at"]))
					})
					if d.ValidManifest(bindings, "PRICE_PROXY", at) && len(observations) > 0 {
						v := observations[len(observations)-1]
						t := d.At(v["available_at"])
						if t.After(sample.AvailableAt) {
							sample.AvailableAt = t
						}
						sample.Sigma = nil
						sample.Liquidity = nil
						sample.Benchmark = nil
						for name, target := range map[string]**d.Decimal{"sigma": &sample.Sigma, "liquidity": &sample.Liquidity, "benchmark": &sample.Benchmark} {
							if v[name] != nil && d.Text(v[name]) != "" {
								*target = d.Ptr(d.Amount(v[name]))
							}
						}
					}
					samples := state.Samples.Value(obj.ObjectID)
					if len(samples) == 0 || !samples[len(samples)-1].AvailableAt.Equal(sample.AvailableAt) {
						state.Samples.Set(obj.ObjectID, append(samples, *sample))
					}
				}
				required := true
				for _, name := range []string{"w", "g", "h", "eta", "kappa", "k_stop"} {
					if _, ok := params.Values[name]; !ok {
						required = false
					}
				}
				if !required {
					if obj.LastCycle != nil && at.Sub(*obj.LastCycle).Seconds() < float64(p.CycleSeconds) {
						continue
					}
					if err := state.Advance(obj, at, nil, p, params); err != nil {
						return err
					}
					view, err := state.Pool(obj.ObjectID)
					if err != nil {
						return err
					}
					id := "decision-" + d.Digest([]any{state.InstanceID, state.Environment, obj.TradingRunKey, obj.ObjectID, d.ISO(scheduled)})[:24]
					decision := &d.DecisionView{DecisionID: id, ObjectID: obj.ObjectID, CycleID: obj.ObjectID + ":" + d.ISO(at), AvailableCutoff: at, PoolPlus: view.Plus, PoolMinus: view.Minus, PoolNet: view.Net, PreviousLevel: obj.Level, NewLevel: obj.Level, TargetQuantity: m.Add(actual, pending), ActualQuantity: actual, PendingQuantity: pending, ParameterVersion: obj.ParameterVersion, InputHash: d.Digest([]any{snap, params}), ReasonCodes: []string{"PARAMETERS_REQUIRED_UNSET"}, InputSnapshot: map[string]any{}, SourceEventVersions: map[string]int{}}
					state.Decisions.Set(id, decision)
					obj.LastCycle = d.Ptr(scheduled)
					obj.LastCutoff = d.Ptr(at)
					outputs = append(outputs, decision)
					if sample != nil {
						spec := snap.Specs[obj.ObjectID]
						value := m.Mul(m.Mul(m.Add(actual, pending), sample.Price), d.Amount(spec["contract_multiplier"]))
						base = append(base, d.BaseRisk{Value: value, Stress: value.Abs(), Group: riskGroup(spec)})
					}
					continue
				}
				if err := UpdateCases(state, obj, snap, at, p, params); err != nil {
					return err
				}
				corrected, err := CorrectActualRisk(state, obj, snap, at)
				if err != nil {
					return err
				}
				if corrected != nil {
					outputs = append(outputs, corrected)
					if sample != nil {
						spec := snap.Specs[obj.ObjectID]
						value := m.Mul(m.Mul(m.Add(actual, pending), sample.Price), d.Amount(spec["contract_multiplier"]))
						base = append(base, d.BaseRisk{Value: value, Stress: m.Mul(value.Abs(), p.MaxStopFraction), Group: riskGroup(spec)})
					}
					continue
				}
				if obj.LastCycle != nil && at.Before(*obj.LastCycle) {
					return &d.Error{Code: "CLOCK_REWIND", Status: 422}
				}
				if obj.LastCycle != nil && at.Sub(*obj.LastCycle).Seconds() < float64(p.CycleSeconds) {
					if sample != nil {
						spec := snap.Specs[obj.ObjectID]
						value := m.Mul(m.Mul(m.Add(actual, pending), sample.Price), d.Amount(spec["contract_multiplier"]))
						base = append(base, d.BaseRisk{Value: value, Stress: m.Mul(value.Abs(), p.MaxStopFraction), Group: riskGroup(spec)})
					}
					continue
				}
				if obj.LastCycle != nil {
					missed := int(math.Floor(scheduled.Sub(*obj.LastCycle).Seconds()/float64(p.CycleSeconds))) - 1
					if missed != 0 {
						state.SkippedCycles = append(state.SkippedCycles, map[string]any{"object_id": obj.ObjectID, "from": d.ISO(*obj.LastCycle), "to": d.ISO(at), "count": missed, "state": "SKIPPED"})
					}
				}
				if err := d.Activate(state, obj, at); err != nil {
					return err
				}
				if err := d.Monitor(state, obj, at); err != nil {
					return err
				}
				params = state.Parameters.Value(obj.ParameterVersion)
				for _, contribution := range state.Contributions.Values() {
					if contribution.ObjectID != obj.ObjectID || contribution.State != "ACTIVE" {
						continue
					}
					event := state.Events.Value(contribution.EventID)
					if event.State == "RETRACTED" {
						if err := state.Append(contribution, d.LedgerEntry{At: at, Reason: "RETRACTED", Invalidation: contribution.RemainingAmount}); err != nil {
							return err
						}
						contribution.State = "INVALID"
					} else if len(contribution.FactWeights) > 0 && event.Relation == "CORRECTION" {
						current := []string{}
						for _, claim := range event.Claims {
							current = append(current, d.FactKey(claim))
						}
						removed, total := d.Zero(), d.Zero()
						keep := map[string]d.Decimal{}
						weightKeys := []string{}
						for k := range contribution.FactWeights {
							weightKeys = append(weightKeys, k)
						}
						sort.Strings(weightKeys)
						for _, k := range weightKeys {
							weight := contribution.FactWeights[k]
							total = m.Add(total, weight)
							if !d.Has(current, k) {
								removed = m.Add(removed, weight)
							} else {
								keep[k] = weight
							}
						}
						if removed.Sign() != 0 {
							if err := state.Append(contribution, d.LedgerEntry{At: at, Reason: "CLAIM_INVALIDATED", Invalidation: m.Mul(contribution.RemainingAmount, m.Div(removed, total))}); err != nil {
								return err
							}
							contribution.FactWeights = keep
							if len(keep) == 0 {
								contribution.State = "INVALID"
							}
						}
					}
				}
				if err := state.Advance(obj, at, sample, p, params); err != nil {
					return err
				}
				if err := InjectReady(state, obj, at, sample, p, params); err != nil {
					return err
				}
				view, err := state.Pool(obj.ObjectID)
				if err != nil {
					return err
				}
				regime, err := d.UpdateRegime(state.Regimes.Value(obj.ObjectID), state.Samples.Value(obj.ObjectID), at, p, state.FactorManifests.Value(obj.RegimeBinding))
				if err != nil {
					return err
				}
				state.Regimes.Set(obj.ObjectID, regime)
				if err := EvaluateLearning(state, obj, at); err != nil {
					return err
				}
				level, err := d.LevelFor(view.Net.Abs(), obj.Level, p.Levels)
				if err != nil {
					return err
				}
				raw, err := d.Exposure(view, level, sample, snap.Equity, p.Numerical())
				if err != nil {
					return err
				}
				for _, v := range regime {
					if d.Text(d.Object(v)["state"]) == "UNKNOWN" {
						raw = m.Mul(raw, p.UnknownRegimeMultiplier)
						break
					}
				}
				target, stress := actual, p.MaxStopFraction
				var stop *d.StopPlan
				reasons := []string{}
				if sample == nil || sample.Quality != "VALID" {
					reasons = append(reasons, "PRICE_UNKNOWN")
				} else {
					spec := snap.Specs[obj.ObjectID]
					rules := Rules(spec)
					if d.Text(spec["settlement_currency"]) != d.Text(bindings["account_currency"]) {
						raw = d.Zero()
						reasons = append(reasons, "FX_OR_CURRENCY_UNKNOWN")
					}
					target, err = d.Quantity(raw, snap.Equity, sample.Price, rules.Multiplier, rules.QuantityStep)
					if err != nil {
						return err
					}
					if target.Sign() < 0 && !d.Has(rules.Capabilities, "SHORT") {
						target = d.Zero()
						reasons = append(reasons, "SHORT_UNAVAILABLE")
					}
					if m.Mul(view.Net, actual).Sign() < 0 || m.Mul(view.Net, pending).Sign() < 0 {
						target = d.Zero()
						reasons = append(reasons, "FLATTEN_BEFORE_REVERSE")
					}
					if target.Sign() != 0 {
						noise, err := d.NormalNoise(snap.Bars[obj.ObjectID], at, p.Numerical())
						if err != nil {
							return err
						}
						result, err := d.SizeAndStop(target, sample.Price, rules, noise, p.Numerical(), params.Values["k_stop"])
						if err != nil {
							return err
						}
						target, stop, stress = result.Quantity, result.Plan, result.Fraction
						reasons = append(reasons, result.Reason)
					}
					if actual.Sign() != 0 && m.Mul(target, actual).Sign() > 0 && target.Abs().Cmp(actual.Abs()) <= 0 {
						stop = nil
					} else if actual.Sign() != 0 && stop != nil {
						active := []d.Decimal{}
						for _, protection := range snap.Protections[obj.ObjectID] {
							if d.Text(protection["state"]) == "ACTIVE_VERIFIED" {
								active = append(active, d.Amount(d.Object(protection["plan"])["trigger_price"]))
							}
						}
						if len(active) == 0 {
							target, stop = actual, nil
							reasons = append(reasons, "EXISTING_PROTECTION_UNVERIFIED")
						} else {
							tightest := d.Min(active...)
							if actual.Sign() > 0 {
								tightest = d.Max(active...)
								stop.TriggerPrice = d.Max(stop.TriggerPrice, tightest)
							} else {
								stop.TriggerPrice = d.Min(stop.TriggerPrice, tightest)
							}
							if m.Mul(actual, m.Sub(sample.Price, stop.TriggerPrice)).Sign() <= 0 {
								target, stop = actual, nil
								reasons = append(reasons, "EXISTING_PROTECTION_CROSSED")
							}
						}
					}
				}
				blocked := obj.State != "ACTIVE" || obj.RecoveryState != "NORMAL" || len(snap.RiskLocks) > 0 || p.QualityState != "VALIDATED" || params.QualityState != "VALIDATED"
				if (blocked || unknownDelivery(state, obj.ObjectID)) && target.Abs().Cmp(actual.Abs()) > 0 {
					target, stop = actual, nil
					reasons = append(reasons, "RECOVERY_OR_PAUSE_OR_DELIVERY_BLOCK")
				}
				stopNow := false
				for _, fill := range snap.Fills {
					if d.Flag(fill["protection_exit"]) && d.Text(fill["owner_id"]) == obj.OwnerID {
						happened := d.At(fill["happened_at"])
						if obj.LastCutoff != nil {
							stopNow = stopNow || happened.After(*obj.LastCutoff)
						} else {
							stopNow = stopNow || !happened.Before(at)
						}
					}
				}
				if stopNow && target.Abs().Cmp(actual.Abs()) > 0 {
					target, stop = actual, nil
					reasons = append(reasons, "STOP_CYCLE_NO_REOPEN")
				}
				current := m.Add(actual, pending)
				if target.Abs().Cmp(current.Abs()) > 0 && (m.Sub(target, current).Abs().Cmp(p.MinAdjustment) < 0 || obj.LastAdjustment != nil && at.Sub(*obj.LastAdjustment).Seconds() < float64(p.CooldownSeconds)) {
					target, stop = current, nil
					reasons = append(reasons, "ADJUSTMENT_COOLDOWN")
				}
				id := "decision-" + d.Digest([]any{state.InstanceID, state.Environment, obj.TradingRunKey, obj.ObjectID, d.ISO(scheduled)})[:24]
				due = append(due, &dueObject{obj: obj, p: p, sample: sample, target: target, stop: stop, stress: stress, view: view, level: level, raw: raw, reasons: reasons, id: id, scheduled: scheduled})
				if sample != nil {
					mult := d.Amount(snap.Specs[obj.ObjectID]["contract_multiplier"])
					rows = append(rows, d.Candidate{Current: m.Mul(m.Mul(current, sample.Price), mult), Desired: m.Mul(m.Mul(target, sample.Price), mult), Stress: stress, Group: riskGroup(snap.Specs[obj.ObjectID])})
				}
			}
			unresolved := false
			riskScale := d.One()
			for _, item := range due {
				scale, err := d.ExistingRiskScale(rows, base, item.p.Numerical())
				if err != nil {
					return err
				}
				if scale == nil {
					unresolved = true
				} else {
					riskScale = d.Min(riskScale, *scale)
				}
			}
			if riskScale.Cmp(d.One()) < 0 && !unresolved {
				correctedRows := []d.Candidate{}
				for _, item := range due {
					obj := item.obj
					spec := snap.Specs[obj.ObjectID]
					if item.sample != nil && item.sample.Quality == "VALID" {
						target, err := d.ReduceQuantity(m.Add(obj.ActualQuantity, obj.PendingQuantity), item.target, riskScale, d.Amount(spec["quantity_step"]))
						if err != nil {
							return err
						}
						item.target = target
						if target.Abs().Cmp(obj.ActualQuantity.Abs()) <= 0 {
							item.stop = nil
						} else if item.stop != nil {
							item.stop.CoveredQuantity = target.Abs()
						}
						item.reasons = append(item.reasons, "ACCOUNT_EXISTING_RISK_REDUCED")
					}
					if item.sample != nil {
						mult := d.Amount(spec["contract_multiplier"])
						correctedRows = append(correctedRows, d.Candidate{Current: m.Mul(m.Mul(m.Add(obj.ActualQuantity, obj.PendingQuantity), item.sample.Price), mult), Desired: m.Mul(m.Mul(item.target, item.sample.Price), mult), Stress: item.stress, Group: riskGroup(spec)})
					}
				}
				rows = correctedRows
				for _, item := range due {
					scale, err := d.ExistingRiskScale(rows, base, item.p.Numerical())
					if err != nil {
						return err
					}
					if scale == nil || scale.Cmp(d.One()) != 0 {
						unresolved = true
					}
				}
				if unresolved {
					for _, item := range due {
						item.target = m.Add(item.obj.ActualQuantity, item.obj.PendingQuantity)
						item.stop = nil
						reasons := []string{}
						for _, r := range item.reasons {
							if r != "ACCOUNT_EXISTING_RISK_REDUCED" {
								reasons = append(reasons, r)
							}
						}
						item.reasons = append(reasons, "ACCOUNT_QUANTITY_STEP_RECONCILIATION_REQUIRED")
					}
				}
			}
			alpha := d.One()
			if unresolved {
				alpha = d.Zero()
			} else {
				for _, item := range due {
					scale, err := d.SharedProjection(rows, base, item.p.Numerical())
					if err != nil {
						return err
					}
					alpha = d.Min(alpha, scale)
				}
			}
			for _, item := range due {
				obj, p, sample := item.obj, item.p, item.sample
				actual, pending := obj.ActualQuantity, obj.PendingQuantity
				current := m.Add(actual, pending)
				if unresolved {
					item.reasons = append(item.reasons, "ACCOUNT_RISK_RECONCILIATION_REQUIRED")
				}
				if alpha.Cmp(d.One()) < 0 && item.target.Abs().Cmp(current.Abs()) > 0 {
					if sample == nil {
						return &d.Error{Code: "PROJECTION_PRICE_MISSING", Status: 423}
					}
					spec := snap.Specs[obj.ObjectID]
					mult := d.Amount(spec["contract_multiplier"])
					target, err := d.Quantity(m.Div(m.Mul(m.Mul(m.Add(current, m.Mul(alpha, m.Sub(item.target, current))), sample.Price), mult), snap.Equity), snap.Equity, sample.Price, mult, d.Amount(spec["quantity_step"]))
					if err != nil {
						return err
					}
					item.target = target
					item.reasons = append(item.reasons, "ACCOUNT_ALPHA_PROJECTED")
					if item.stop != nil && target.Sign() != 0 {
						item.stop.CoveredQuantity = target.Abs()
					}
				}
				var reservation *d.Reservation
				if item.target.Abs().Cmp(current.Abs()) > 0 {
					r, err := d.Reserve(state, obj, at, p, item.id)
					if err != nil {
						var known *d.Error
						if !errors.As(err, &known) {
							return err
						}
						item.target, item.stop = current, nil
						item.reasons = append(item.reasons, known.Code)
					} else {
						reservation = r
					}
				}
				if item.target.Sign() == 0 {
					item.stop = nil
				}
				pricing := map[string]any{}
				sourceVersions := map[string]int{}
				for _, contribution := range item.view.Contributions {
					if v, ok := state.PrepricingAssessments.Get(contribution.ScoreID); ok {
						pricing[contribution.ScoreID] = v
					}
					if contribution.RemainingAmount.Sign() > 0 {
						sourceVersions[contribution.EventID] = state.Events.Value(contribution.EventID).FactVersion
					}
				}
				inputs := map[string]any{"pool": d.Map(item.view), "snapshot": d.Map(snap), "policy": p.Version, "parameter_version": obj.ParameterVersion, "regime": state.Regimes.Value(obj.ObjectID), "prepricing": pricing}
				projected := d.Zero()
				if sample != nil && snap.Equity.Sign() > 0 {
					projected = m.Div(m.Mul(m.Mul(item.target, sample.Price), d.Amount(snap.Specs[obj.ObjectID]["contract_multiplier"])), snap.Equity)
				}
				reasons := item.reasons
				if len(reasons) == 0 {
					reasons = []string{"TARGET_COMPUTED"}
				}
				decision := &d.DecisionView{DecisionID: item.id, ObjectID: obj.ObjectID, CycleID: obj.ObjectID + ":" + d.ISO(item.scheduled), AvailableCutoff: at, PoolPlus: item.view.Plus, PoolMinus: item.view.Minus, PoolNet: item.view.Net, PreviousLevel: obj.Level, NewLevel: item.level, RawExposure: item.raw, ProjectedExposure: projected, TargetQuantity: item.target, ActualQuantity: actual, PendingQuantity: pending, StopPlan: item.stop, ParameterVersion: obj.ParameterVersion, InputHash: d.Digest(inputs), InputSnapshot: inputs, ReasonCodes: reasons, SourceEventVersions: sourceVersions}
				state.Decisions.Set(item.id, decision)
				canReduce := m.Mul(item.target, actual).Sign() >= 0 && item.target.Abs().Cmp(actual.Abs()) <= 0 && obj.OwnerEpoch == snap.OwnerEpochs[obj.ObjectID]
				unknown := unknownDelivery(state, obj.ObjectID)
				if sample != nil && sample.Quality == "VALID" && (obj.RecoveryState == "NORMAL" || canReduce) && (!unknown || canReduce) {
					version := snap.TargetVersions[obj.ObjectID]
					for _, i := range state.Outbox.Values() {
						if i.Decision.ObjectID == obj.ObjectID && i.TargetVersion > version {
							version = i.TargetVersion
						}
					}
					var reservationID *string
					if reservation != nil {
						reservationID = d.Ptr(reservation.ReservationID)
					}
					state.Outbox.Set(item.id, &d.TargetOutbox{Decision: *decision, TargetVersion: version + 1, ExpiresAt: at.Add(time.Duration(p.TargetTTLSeconds) * time.Second), ReservationID: reservationID, State: "PENDING", OwnerEpoch: obj.OwnerEpoch})
				}
				obj.LastCycle = d.Ptr(item.scheduled)
				obj.LastCutoff = d.Ptr(at)
				obj.Level = item.level
				obj.Direction = item.view.Net.Sign()
				if err := d.VerifyLedger(state.Sentiment(), p.NumericalTolerance); err != nil {
					return err
				}
				outputs = append(outputs, decision)
			}
		}
		if err := m.Err(); err != nil {
			return err
		}
		state.Version++
		ids := []string{}
		for _, v := range outputs {
			ids = append(ids, v.DecisionID)
		}
		state.Audit = append(state.Audit, map[string]any{"action": "CYCLE_COMMITTED", "at": d.ISO(at), "decisions": ids, "snapshot_version": before.Version})
		return nil
	})
	return outputs, err
}

func (c DecisionCycle) Dispatch(ctx context.Context, identity d.Identity) error {
	before, err := c.Store.Read(ctx, identity.Instance())
	if err != nil {
		return err
	}
	if err = Authorize(before, identity, d.Query, ""); err != nil {
		return err
	}
	for _, key := range before.Outbox.Keys() {
		item := before.Outbox.Value(key)
		if !d.Has([]string{"PENDING", "DELIVERY_UNKNOWN"}, item.State) {
			continue
		}
		obj := before.Objects.Value(item.Decision.ObjectID)
		if err = Authorize(before, identity, d.Query, obj.ObjectID); err != nil {
			return err
		}
		if worker := identity.Worker(); worker != nil {
			if err = c.Store.CheckWorkload(ctx, *worker, obj.ObjectID); err != nil {
				return err
			}
		}
		now := c.Clock.Now()
		snapshot, err := c.Trading.Snapshot(ctx, []*d.ObservedObject{obj})
		if err != nil {
			return err
		}
		m := d.Math()
		increase := item.Decision.TargetQuantity.Abs().Cmp(m.Add(snapshot.Actual[obj.ObjectID], snapshot.Pending[obj.ObjectID]).Abs()) > 0
		revoked := obj.State != "ACTIVE" || len(snapshot.RiskLocks) > 0 || snapshot.RunState != "NORMAL" || snapshot.OwnerEpochs[obj.ObjectID] != item.OwnerEpoch
		for eventID, version := range item.Decision.SourceEventVersions {
			e := before.Events.Value(eventID)
			if e != nil && e.State == "RETRACTED" && e.FactVersion > version {
				revoked = true
			}
		}
		status := ""
		var reason *string
		if d.Has(snapshot.SourceDecisions, key) {
			status = "ACK"
		} else if increase && revoked {
			status = "DELIVERY_UNKNOWN"
			if item.State == "PENDING" {
				status = "EXPIRED"
			}
			reason = d.Ptr("RISK_REVOKED_BEFORE_DELIVERY")
		} else if !now.Before(item.ExpiresAt) {
			status = "EXPIRED"
			reason = d.Ptr("TARGET_EXPIRED")
		} else {
			proceed := false
			err = c.Store.Transaction(ctx, identity.Instance(), func(state *d.StrategyState) error {
				row := state.Outbox.Value(key)
				if row == nil || !d.Has([]string{"PENDING", "DELIVERY_UNKNOWN"}, row.State) {
					return nil
				}
				if row.CommandPayload == nil {
					payload, err := c.Trading.Prepare(obj, item, snapshot)
					if err != nil {
						return err
					}
					row.CommandPayload = &payload
				}
				item.CommandPayload = d.Clone(row.CommandPayload)
				row.State = "DELIVERY_UNKNOWN"
				if item.ReservationID != nil {
					state.Reservations.Value(*item.ReservationID).State = "UNKNOWN"
				}
				proceed = true
				return nil
			})
			if err != nil {
				return err
			}
			if !proceed {
				continue
			}
			var deliveryErr error
			if worker := identity.Worker(); worker != nil {
				deliveryErr = c.Store.CheckWorkload(ctx, *worker, obj.ObjectID)
			}
			if deliveryErr == nil {
				_, deliveryErr = c.Trading.Deliver(ctx, obj, item, snapshot)
			}
			if deliveryErr == nil {
				status = "ACK"
			} else {
				var known *d.Error
				if !errors.As(deliveryErr, &known) {
					return deliveryErr
				}
				status = "REJECTED"
				if known.Status >= 500 {
					status = "DELIVERY_UNKNOWN"
				}
				reason = d.Ptr(known.Code)
			}
		}
		if err = m.Err(); err != nil {
			return err
		}
		err = c.Store.Transaction(ctx, identity.Instance(), func(state *d.StrategyState) error {
			row := state.Outbox.Value(key)
			row.State = status
			row.Reason = reason
			if status == "ACK" {
				row.AcceptedAt = d.Ptr(now)
				state.Objects.Value(obj.ObjectID).LastAdjustment = d.Ptr(now)
			}
			if item.ReservationID != nil {
				r := state.Reservations.Value(*item.ReservationID)
				r.State = "UNKNOWN"
				if status == "ACK" {
					r.State = "ACCEPTED"
				} else if status == "REJECTED" || status == "EXPIRED" && item.State == "PENDING" {
					r.State = "RELEASED"
				}
			}
			state.Audit = append(state.Audit, map[string]any{"action": "TARGET_DELIVERY", "decision_id": key, "state": status, "reason": reason, "at": d.ISO(now)})
			state.Version++
			return nil
		})
		if err != nil {
			return err
		}
	}
	return nil
}
