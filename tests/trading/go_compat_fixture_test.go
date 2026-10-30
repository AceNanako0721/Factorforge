package trading_test

import (
	"encoding/json"
	"fmt"
	d "github.com/AceNanako0721/Factorforge/src/factorforge/trading/domain"
	"regexp"
	"sort"
	"strings"
	"testing"
)

// Frozen JSON remains unchanged. Synthetic historical runs explicitly acquire
// an experimental policy and their observed legacy STRATEGY records are bound
// to the fixture's direction/cycle. This is a test migration, never recovery logic.
func upgradeTradingFixture(run *d.Aggregate) {
	if run.Policy.InsuranceStopFraction == nil {
		run.Policy.InsuranceStopFraction = cp("0.5")
		run.Policy.InsuranceTriggerKind = "MARK"
		run.Policy.InsuranceQualityState = "EXPERIMENT_ONLY"
		if run.RunKey.Environment == "LIVE" {
			run.Policy.InsuranceQualityState = "VALIDATED"
		}
	}
	for _, pos := range run.Positions.Values() {
		if pos.Quantity.Sign() == 0 {
			continue
		}
		if pos.PositionCycleID == "" {
			pos.PositionCycleID = "fixture-cycle"
		}
		if pos.EffectiveMode == "" {
			pos.EffectiveMode = "VENUE"
			pos.StrategyProtectionMode = "VENUE"
		}
		var strategy *d.Protection
		for _, p := range run.Protections.Values() {
			if p.InstrumentKey == pos.InstrumentKey && p.Role == "" {
				p.Role = "STRATEGY"
				p.PositionDirection = d.Direction(pos.Quantity)
				p.PositionCycleID = pos.PositionCycleID
				p.ClientAlgoID = p.ProtectionID
				if p.State == "ACTIVE_VERIFIED" || p.State == "PENDING" {
					strategy = p
				}
			}
		}
		if strategy != nil && containsCompat([]string{"ACTIVE_VERIFIED", "PENDING"}, pos.ProtectionState) {
			plan := strategy.Plan
			plan.TriggerKind = "MARK"
			price := d.NewEngine(run).Math.Mul(*pos.AverageEntry, cv("0.5"))
			if pos.Quantity.Sign() < 0 {
				price = d.NewEngine(run).Math.Mul(*pos.AverageEntry, cv("1.5"))
			}
			plan.TriggerPrice = price
			id := "fixture-insurance-" + pos.InstrumentKey.InstrumentID
			run.Protections.Set(id, &d.Protection{ProtectionID: id, Role: "INSURANCE", ClientAlgoID: id, InstrumentKey: pos.InstrumentKey, OwnerID: pos.OwnerID, Plan: plan, State: "ACTIVE_VERIFIED", PositionDirection: d.Direction(pos.Quantity), PositionCycleID: pos.PositionCycleID, VerifiedAt: strategy.VerifiedAt})
			p := strategy.Plan
			pos.RequestedPlan = &p
		}
	}
}

// Compare the inherited accounting/market/order behavior after projecting out
// -only metadata. New role, ownership and ID semantics have direct tests.
func projectCompatGolden(value any) any {
	switch v := value.(type) {
	case map[string]any:
		out := map[string]any{}
		for k, x := range v {
			if strings.HasPrefix(k, "insurance_") || containsCompat([]string{"sim_frame_at", "protection_degraded", "conditional_cleanups", "position_cycle_id", "strategy_protection_mode", "strategy_declaration", "requested_owner_epoch", "requested_mode", "effective_mode", "transition_state", "requested_plan", "mode_cleanup_verified", "reason_codes", "role", "position_direction", "generation", "source_target_version", "client_algo_id", "original_target_version", "authorized_actual", "authorized_owner_epoch", "intent_protection_plan", "replaces_id", "cancel_attempted", "cancel_requested"}, k) {
				continue
			}
			if k == "capabilities" && v["resource_id"] != nil && v["run_key"] != nil {
				continue
			}
			if k == "external_id" && v["protection_id"] != nil {
				continue
			}
			if k == "hash" || k == "target_version" && x == nil {
				continue
			}
			if containsCompat([]string{"orders", "fills", "converted_fees", "emergency_orders", "queue_remaining"}, k) {
				m := asMap(x)
				generated := false
				for key := range m {
					if strings.Contains(key, "ord-") || strings.Contains(key, "exit-") || strings.Contains(key, "fill-") {
						generated = true
					}
				}
				if generated {
					rows := []any{}
					for _, v := range m {
						rows = append(rows, projectCompatGolden(v))
					}
					sort.Slice(rows, func(i, j int) bool {
						a, _ := json.Marshal(rows[i])
						b, _ := json.Marshal(rows[j])
						return string(a) < string(b)
					})
					out[k] = rows
					continue
				}
			}
			if k == "protections" {
				rows := []any{}
				for _, p := range asMap(x) {
					m := asMap(p)
					if m["role"] == "INSURANCE" {
						continue
					}
					rows = append(rows, projectCompatGolden(p))
				}
				sort.Slice(rows, func(i, j int) bool {
					a, _ := json.Marshal(rows[i])
					b, _ := json.Marshal(rows[j])
					return string(a) < string(b)
				})
				out[k] = rows
				continue
			}
			if k == "protection_id" || k == "external_id" && strings.HasPrefix(fmt.Sprint(x), "ff-") {
				out[k] = "generated-protection"
				continue
			}
			out[k] = projectCompatGolden(x)
		}
		return out
	case []any:
		out := make([]any, len(v))
		for i, x := range v {
			out[i] = projectCompatGolden(x)
		}
		return out
	case string:
		if regexp.MustCompile("^(ord|cmd|ff|exit|fill|target-cancel)-[a-f0-9]{20,}$").MatchString(v) {
			return "generated-" + strings.Split(v, "-")[0]
		}
		if strings.HasPrefix(v, "prot-") || strings.HasPrefix(v, "ff-i-") || strings.HasPrefix(v, "ff-s-") {
			return "generated-protection"
		}
		return v
	}
	return value
}
func asMap(v any) map[string]any { m, _ := v.(map[string]any); return m }
func containsCompat(values []string, k string) bool {
	for _, v := range values {
		if k == v {
			return true
		}
	}
	return false
}

// For intentionally changed protection workflows, retain the complete economic
// and ordinary execution oracle. T1-13..19 assert the replacement state machine.
func executionEqualEconomics(t *testing.T, expected json.RawMessage, actual any) {
	t.Helper()
	var left, right map[string]json.RawMessage
	if json.Unmarshal(expected, &left) != nil {
		t.Fatal("economic fixture")
	}
	raw, _ := json.Marshal(actual)
	if json.Unmarshal(raw, &right) != nil {
		t.Fatal("economic result")
	}
	fields := []string{"run_key", "currency", "cash", "initial_cash", "clock", "positions", "orders", "fills", "incomes", "cash_balances", "fx_revaluation", "converted_fees", "converted_income", "consecutive_losses", "day_external_flow", "day_start_equity", "peak_equity"}
	l, r := map[string]any{}, map[string]any{}
	for _, key := range fields {
		if left[key] != nil {
			var x, y any
			json.Unmarshal(left[key], &x)
			json.Unmarshal(right[key], &y)
			l[key] = x
			r[key] = y
		}
	}
	for _, v := range []map[string]any{l, r} {
		for _, p := range asMap(v["positions"]) {
			delete(asMap(p), "protection_state")
		}
	}
	a, _ := json.Marshal(l)
	executionEqual(t, a, r)
}
