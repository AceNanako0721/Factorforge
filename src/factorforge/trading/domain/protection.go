package domain

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"github.com/AceNanako0721/Factorforge/src/factorforge/trading/domain/decimal"
	"strconv"
)

func StableID(prefix, body string) string {
	hash := sha256.Sum256([]byte(body))
	return prefix + hex.EncodeToString(hash[:])[:28]
}
func (e *Engine) VerifyProtection(key InstrumentKey, plan ProtectionPlan, replaceID *string) *Protection {
	if !e.validate(key) || !e.validate(plan) {
		return nil
	}
	run := e.Run
	code := key.Code()
	position := run.Positions.Value(code)
	spec := run.Specs.Value(code)
	if position == nil || position.Quantity.Sign() == 0 {
		e.Fail("NO_POSITION_TO_PROTECT")
		return nil
	}
	if spec == nil || plan.SpecVersion != spec.Version || !Has(spec.Capabilities, "CONDITIONAL_PROTECTION") {
		e.Fail("PROTECTION_CAPABILITY_UNVERIFIED", 423)
		return nil
	}
	if plan.CoveredQuantity.Cmp(e.Math.Abs(position.Quantity)) != 0 {
		e.Fail("PROTECTION_MUST_MATCH_ACTUAL_POSITION")
		return nil
	}
	if e.Math.Step(plan.TriggerPrice, spec.PriceTick, decimal.TowardZero).Cmp(plan.TriggerPrice) != 0 {
		e.Fail("PROTECTION_TICK_MISMATCH")
		return nil
	}
	current := e.Quote(key, plan.TriggerKind)
	if (position.Quantity.Sign() > 0 && plan.TriggerPrice.Cmp(current) >= 0) || (position.Quantity.Sign() < 0 && plan.TriggerPrice.Cmp(current) <= 0) {
		e.Fail("PROTECTION_ALREADY_CROSSED", 423)
		return nil
	}
	var old *Protection
	if replaceID != nil {
		old = run.Protections.Value(*replaceID)
		if old == nil || old.InstrumentKey != key || old.State != "ACTIVE_VERIFIED" {
			e.Fail("OLD_PROTECTION_NOT_VERIFIED", 423)
			return nil
		}
	}
	for _, active := range run.Protections.Values() {
		if active.InstrumentKey == key && active.State == "ACTIVE_VERIFIED" && ((position.Quantity.Sign() > 0 && plan.TriggerPrice.Cmp(active.Plan.TriggerPrice) < 0) || (position.Quantity.Sign() < 0 && plan.TriggerPrice.Cmp(active.Plan.TriggerPrice) > 0)) {
			e.Fail("PROTECTION_CANNOT_LOOSEN")
			return nil
		}
	}
	if e.Err() != nil {
		return nil
	}
	keyJSON, _ := json.Marshal(run.RunKey)
	planJSON, _ := json.Marshal(plan)
	identity := string(keyJSON) + ":" + PythonTime(run.Clock) + ":" + code + ":" + strconv.Itoa(run.Protections.Len()) + ":" + string(planJSON)
	if replaceID == nil && run.RunKey.Environment == "LIVE" {
		keys := run.Protections.Keys()
		for i := len(keys) - 1; i >= 0; i-- {
			p := run.Protections.Value(keys[i])
			if p.InstrumentKey == key && p.State == "ACTIVE_VERIFIED" {
				id := p.ProtectionID
				replaceID = &id
				break
			}
		}
	}
	protection := &Protection{ProtectionID: StableID("prot-", identity), InstrumentKey: key, OwnerID: position.OwnerID, Plan: plan, State: "PENDING", ReplacesID: replaceID}
	if run.RunKey.Environment == "SIM" {
		at := run.Clock
		protection.State = "ACTIVE_VERIFIED"
		protection.VerifiedAt = &at
	}
	run.Protections.Set(protection.ProtectionID, protection)
	position.ProtectionState = protection.State
	if old != nil && protection.State == "ACTIVE_VERIFIED" {
		old.State = "CLOSED"
	}
	return protection
}
func (e *Engine) ProtectActualPosition(order *Order) {
	run := e.Run
	code := order.Request.InstrumentKey.Code()
	p := run.Positions.Value(code)
	if p == nil {
		e.Fail("POSITION_NOT_FOUND", 423)
		return
	}
	active := []*Protection{}
	for _, old := range run.Protections.Values() {
		if old.InstrumentKey.Code() == code && old.State == "ACTIVE_VERIFIED" {
			active = append(active, old)
		}
	}
	if p.Quantity.Sign() == 0 {
		for _, old := range active {
			old.State = "CLOSED"
			if run.RunKey.Environment == "LIVE" {
				old.State = "CANCEL_PENDING"
				old.CancelRequested = true
			}
		}
		return
	}
	template := order.Request.ProtectionPlan
	if template == nil && len(active) > 0 {
		template = &active[len(active)-1].Plan
	}
	if template == nil {
		p.ProtectionState = "UNPROTECTED"
		run.State = "DEGRADED"
		return
	}
	plan := *template
	plan.CoveredQuantity = e.Math.Abs(p.Quantity)
	// Protection failure degrades the run but cannot discard an actual fill.
	check := NewEngine(run)
	protection := check.VerifyProtection(order.Request.InstrumentKey, plan, nil)
	if err := check.Err(); err != nil {
		if _, ok := err.(*Error); ok {
			p.ProtectionState = "UNPROTECTED"
			run.State = "DEGRADED"
		} else {
			e.err = err
		}
		return
	}
	for _, old := range active {
		if old.ProtectionID != protection.ProtectionID && protection.State == "ACTIVE_VERIFIED" {
			old.State = "CLOSED"
		}
	}
}
