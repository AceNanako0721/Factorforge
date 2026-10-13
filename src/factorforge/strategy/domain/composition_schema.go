package domain

import (
	"encoding/json"
	"sort"
)

// CompositionSchemas is shared by admission and the exported HTTP contract.
// Optional extensions have no defaults in legacy records and therefore do not
// enter frozen hashes until an operator explicitly registers composition.
func CompositionSchemas() map[string]any {
	schemas := map[string]any{}
	ref := func(n string) map[string]any { return map[string]any{"$ref": "#/$defs/" + n} }
	str := map[string]any{"type": "string", "minLength": 1, "maxLength": 160}
	text := map[string]any{"type": "string"}
	at := map[string]any{"type": "string", "format": "date-time"}
	boolField := map[string]any{"type": "boolean"}
	integer := map[string]any{"type": "integer"}
	seconds := map[string]any{"type": "integer", "minimum": 1}
	decimal := map[string]any{"type": "string", "x-decimal": true}
	unitField := map[string]any{"type": "string", "x-decimal": true, "ge": "0", "le": "1"}
	nonnegative := map[string]any{"type": "string", "x-decimal": true, "ge": "0"}
	positive := map[string]any{"type": "string", "x-decimal": true, "gt": "0"}
	enum := func(v ...string) map[string]any { return map[string]any{"type": "string", "enum": v} }
	state := enum("ENABLED", "SHADOW", "DISABLED")
	array := func(n string) map[string]any { return map[string]any{"type": "array", "items": ref(n)} }
	dict := func(child any) map[string]any { return map[string]any{"type": "object", "additionalProperties": child} }
	nullable := func(child any) map[string]any {
		return map[string]any{"anyOf": []any{child, map[string]any{"type": "null"}}}
	}
	model := func(n string, fields map[string]any, optional ...string) {
		required := []string{}
		for k := range fields {
			if !Has(optional, k) {
				required = append(required, k)
			}
		}
		sort.Strings(required)
		schemas[n] = map[string]any{"type": "object", "additionalProperties": false, "properties": fields, "required": required}
	}
	model("BaselineFactor", map[string]any{"factor_id": str, "weight": decimal, "state": state, "max_age_seconds": seconds, "evidence_ref": text})
	model("AssessmentFactor", map[string]any{"factor_id": str, "state": state, "max_age_seconds": seconds, "evidence_ref": text, "force_reduce": boolField})
	model("LayerCap", map[string]any{"state": state, "cap": unitField})
	model("AdjustmentRule", map[string]any{"state": state, "cap": unitField, "scale": positive, "event_only_cap": unitField})
	model("RebalanceRule", map[string]any{"band_floor": nonnegative, "expected_hold_seconds": seconds, "funding_fallback_annual": decimal, "adjust_interval_seconds": seconds, "reverse_confirm_seconds": seconds})
	model("Composition", map[string]any{"baseline": array("BaselineFactor"), "allow_short_baseline": boolField, "missing_policy": enum("ZERO", "HOLD_REDUCE_ONLY"), "assessment": nullable(ref("AssessmentFactor")), "scheduled": ref("LayerCap"), "adjustment": ref("AdjustmentRule"), "levels": array("Level"), "rebalance": ref("RebalanceRule")})
	model("FactorDefinition", map[string]any{"factor_id": str, "purpose": enum("BASELINE", "ASSESSMENT"), "value_min": map[string]any{"type": "string", "x-decimal": true, "ge": "-1", "le": "-1"}, "value_max": map[string]any{"type": "string", "x-decimal": true, "ge": "1", "le": "1"}, "cadence_seconds": seconds, "source_manifest": str})
	observation := map[string]any{"observation_id": str, "available_at": at, "value": decimal, "force_reduce": map[string]any{"type": "boolean", "default": false}, "inputs_hash": str, "evidence_ref": str, "supersedes": map[string]any{"anyOf": []any{str, map[string]any{"type": "null"}}, "default": nil}}
	stored := map[string]any{}
	for k, v := range observation {
		stored[k] = v
	}
	stored["referenced"] = boolField
	model("FactorObservation", stored, "force_reduce", "supersedes", "referenced")
	model("FactorSeries", map[string]any{"definition": ref("FactorDefinition"), "observations": array("FactorObservation")})
	cmdFields := map[string]any{}
	var commandProperties map[string]any
	json.Unmarshal(shapesCommandProperties(), &commandProperties)
	for k, v := range commandProperties {
		cmdFields[k] = v
	}
	cmdFields["factor_id"] = str
	for k, v := range observation {
		cmdFields[k] = v
	}
	model("FactorObservationCommand", cmdFields, "force_reduce", "supersedes")
	model("ReverseCandidate", map[string]any{"direction": map[string]any{"type": "integer", "enum": []int{-1, 1}}, "since": at})
	model("ShadowComponent", map[string]any{"value": decimal, "core": decimal, "combined": decimal, "level": decimal, "exposure": decimal})
	model("DecisionComponents", map[string]any{"baseline": decimal, "assessment": decimal, "scheduled": decimal, "adjustment": decimal, "core": decimal, "combined": decimal, "level": decimal, "exposure": decimal, "trigger_layer": enum("BASELINE", "ASSESSMENT", "SCHEDULED", "ADJUSTMENT", "UNATTRIBUTED"), "shadow": dict(ref("ShadowComponent")), "factors": dict(decimal), "observation_refs": dict(str)})
	model("FundingObservation", map[string]any{"rate": decimal, "next_funding_at": at, "interval_hours": positive, "observed_at": at})
	model("TemplatePrior", map[string]any{"mean": decimal, "n0": nonnegative, "evidence_ref": text})
	model("TemplateStats", map[string]any{"n": map[string]any{"type": "integer", "minimum": 0}, "mean": decimal, "m2": nonnegative, "n_eff": nonnegative})
	model("ScheduledTemplate", map[string]any{"start_offset_seconds": integer, "end_offset_seconds": integer, "certainty_required": enum("CONFIRMED", "ESTIMATED"), "prior": ref("TemplatePrior"), "scale": positive, "cap": unitField, "min_n": positive, "gate_t": positive, "forget_cases": nonnegative, "stats": ref("TemplateStats"), "state": state})
	model("ScheduledWindow", map[string]any{"start": at, "end": at, "certainty": enum("CONFIRMED", "ESTIMATED"), "start_price": nullable(positive), "template": ref("ScheduledTemplate")})
	schedule := map[string]any{"schedule_id": str, "object_id": str, "event_type": str, "expected_at": at, "certainty": enum("CONFIRMED", "ESTIMATED"), "source_ref": str, "revision": seconds, "status": enum("SCHEDULED", "CANCELLED")}
	event := map[string]any{}
	cmd := map[string]any{}
	for k, v := range commandProperties {
		cmd[k] = v
	}
	for k, v := range schedule {
		event[k] = v
		cmd[k] = v
	}
	event["windows_state"] = dict(enum("PENDING", "OPEN", "CLOSED", "VOID"))
	event["windows"] = dict(ref("ScheduledWindow"))
	model("ScheduledWindowTimes", map[string]any{"start_at": at, "end_at": at})
	event["window_times"] = dict(ref("ScheduledWindowTimes"))
	cmd["windows"] = dict(ref("ScheduledWindowTimes"))
	model("ScheduledEvent", event, "windows", "window_times")
	model("ScheduledEventCommand", cmd, "windows")
	model("LayerEntry", map[string]any{"exposure_seconds": decimal, "pnl": decimal, "cost_fee": decimal, "cost_funding": decimal, "hypothetical": boolField})
	model("LayerDay", map[string]any{"object_id": str, "date": str, "sealed": boolField, "entries": dict(ref("LayerEntry")), "pnl": decimal, "cost_fee": decimal, "cost_funding": decimal})
	model("LayerSettlement", map[string]any{"at": at, "price": positive, "quantity": decimal, "equity": positive, "components": nullable(ref("DecisionComponents")), "seen_fills": dict(at), "seen_incomes": dict(at), "shadow_exposure": dict(decimal)})
	return schemas
}

// Avoid referencing the lazily initialized shapes registry while building it.
func shapesCommandProperties() json.RawMessage {
	var base map[string]shape
	_ = json.Unmarshal([]byte(recordSchema), &base)
	return base["Command"]["properties"]
}
func extendCompositionShapes(s map[string]shape) {
	raw, _ := json.Marshal(CompositionSchemas())
	var extra map[string]shape
	_ = json.Unmarshal(raw, &extra)
	for k, v := range extra {
		s[k] = v
	}
	add := func(model, key string, v any) {
		var props map[string]any
		_ = json.Unmarshal(s[model]["properties"], &props)
		props[key] = v
		s[model]["properties"], _ = json.Marshal(props)
	}
	ref := func(n string) map[string]any { return map[string]any{"$ref": "#/$defs/" + n} }
	nullable := func(n string) map[string]any {
		return map[string]any{"anyOf": []any{ref(n), map[string]any{"type": "null"}}}
	}
	add("Policy", "composition", nullable("Composition"))
	add("DecisionView", "components", nullable("DecisionComponents"))
	add("ObservedObject", "core_direction", map[string]any{"type": "integer", "minimum": -1, "maximum": 1})
	add("ObservedObject", "reverse_candidate", nullable("ReverseCandidate"))
	add("ObservedObject", "last_adjustment_trade_at", map[string]any{"anyOf": []any{map[string]any{"type": "string", "format": "date-time"}, map[string]any{"type": "null"}}})
	add("ObservedObject", "layer_ledger_blocked", map[string]any{"type": "boolean"})
	add("CaseRecord", "kind", map[string]any{"type": "string", "enum": []string{"SCHEDULED_WINDOW"}})
	// A scheduled window censored for missing prices, or with a zero return, has
	// no direction; position cases keep producing only -1 or 1.
	add("CaseRecord", "direction", map[string]any{"type": "integer", "enum": []int{-1, 0, 1}})
	add("TradingSnapshot", "account_currency", map[string]any{"type": "string"})
	add("TradingSnapshot", "funding", map[string]any{"type": "object", "additionalProperties": ref("FundingObservation")})
	for key, n := range map[string]string{"factor_series": "FactorSeries", "scheduled_events": "ScheduledEvent", "templates": "ScheduledTemplate", "layer_ledger": "LayerDay", "layer_settlements": "LayerSettlement"} {
		add("StrategyState", key, map[string]any{"type": "object", "additionalProperties": ref(n)})
	}
}
