package domain

import (
	"encoding/json"
	"reflect"
	"sort"
	"strings"
	"time"
)

// DynamicSchemas is the single source for admission shapes and the
// exported contract. Fields tagged omitempty are optional, so records written
// before keep validating and hashing unchanged.
func DynamicSchemas() map[string]any {
	models := []any{SizingRule{}, ReentryRule{}, ChainLossRule{}, PreEventRule{}, TransferRule{}, RiskUnitState{}, ReentryState{}, ChainState{}, BatchRecord{}, ExitRequest{}, ExitRequestRecord{}, TransferObservation{}, BaselineTransfer{}, BaselineTransferRecord{}}
	schemas := map[string]any{}
	for _, model := range models {
		schemas[reflect.TypeOf(model).Name()] = reflectSchema(reflect.TypeOf(model))
	}
	for name, typ := range map[string]reflect.Type{"ExitRequestCommand": reflect.TypeOf(ExitRequestCommand{}), "BaselineTransferCommand": reflect.TypeOf(BaselineTransferCommand{})} {
		schema := reflectSchema(typ)
		var command map[string]any
		json.Unmarshal(shapesCommandProperties(), &command)
		props := schema["properties"].(map[string]any)
		for k, v := range command {
			props[k] = v
		}
		required := []string{}
		for k := range props {
			required = append(required, k)
		}
		sort.Strings(required)
		schema["required"] = required
		schemas[name] = schema
	}
	id := map[string]any{"type": "string", "minLength": 1, "maxLength": 160, "pattern": "^[A-Za-z0-9_.:-]+$"}
	for _, name := range []string{"ExitRequest", "ExitRequestCommand"} {
		props := schemas[name].(map[string]any)["properties"].(map[string]any)
		props["position_direction"] = map[string]any{"type": "string", "enum": []string{"LONG", "SHORT"}}
		props["bar_interval"] = map[string]any{"type": "integer", "enum": []int{300, 900}}
		props["directive_version"] = map[string]any{"type": "integer", "minimum": 1}
		for _, k := range []string{"exit_id", "object_id", "directive_id", "source_module"} {
			props[k] = id
		}
	}
	for _, name := range []string{"BaselineTransfer", "BaselineTransferCommand"} {
		props := schemas[name].(map[string]any)["properties"].(map[string]any)
		props["contribution_ids"] = map[string]any{"type": "array", "minItems": 1, "maxItems": 64, "items": id}
		for _, k := range []string{"transfer_key", "object_id", "factor_id"} {
			props[k] = id
		}
	}
	schemas["SizingRule"].(map[string]any)["properties"].(map[string]any)["mode"] = map[string]any{"type": "string", "enum": []string{"RISK_UNIT"}}
	schemas["PreEventRule"].(map[string]any)["properties"].(map[string]any)["certainty_required"] = map[string]any{"type": "string", "enum": []string{"CONFIRMED", "ESTIMATED"}}
	return schemas
}

// DynamicExtensions lists optional properties added to existing models.
func DynamicExtensions() map[string]map[string]any {
	ref := func(n string) any { return map[string]any{"$ref": "#/$defs/" + n} }
	nullable := func(v any) any { return map[string]any{"anyOf": []any{v, map[string]any{"type": "null"}}} }
	dict := func(v any) any { return map[string]any{"type": "object", "additionalProperties": v} }
	decimal := map[string]any{"type": "string", "x-decimal": true}
	text := map[string]any{"type": "string"}
	strings := map[string]any{"type": "array", "items": text}
	return map[string]map[string]any{
		"Policy":                   {"judgment_event_types": strings, "prepricing_modes": dict(map[string]any{"type": "string", "enum": []string{"ASSESSED", "NOT_ASSESSED"}}), "experiment_calibrations": strings, "learning_mode": map[string]any{"type": "string", "enum": []string{"ACTIVE", "FROZEN"}}, "realization_disabled_types": strings, "cleanup_fraction_of_initial": nullable(map[string]any{"type": "string", "x-decimal": true, "gt": "0", "lt": "1"})},
		"Composition":              {"sizing": nullable(ref("SizingRule")), "reentry": nullable(ref("ReentryRule")), "chain_loss": nullable(ref("ChainLossRule")), "pre_event": map[string]any{"type": "array", "items": ref("PreEventRule")}, "transfer": nullable(ref("TransferRule"))},
		"RebalanceRule":            {"band_ratio_of_unit": nullable(map[string]any{"type": "string", "x-decimal": true, "ge": "0", "lt": "1"})},
		"FactorObservation":        {"reconfirm": map[string]any{"type": "boolean"}, "reason_code": text},
		"FactorObservationCommand": {"reconfirm": map[string]any{"type": "boolean"}, "reason_code": text},
		"ObservedObject":           {"risk_unit": nullable(ref("RiskUnitState")), "reentry": nullable(ref("ReentryState")), "chain": nullable(ref("ChainState")), "pre_event_applied": strings},
		"StrategyState":            {"exit_requests": dict(ref("ExitRequestRecord")), "baseline_transfers": dict(ref("BaselineTransferRecord")), "batches": dict(ref("BatchRecord")), "frozen_candidates": dict(map[string]any{"type": "object"})},
		"StopDirective":            {"risk_fraction": nullable(decimal), "close_stop_price": nullable(decimal), "close_stop_distance": nullable(decimal)},
		"StopDirectiveCommand":     {"risk_fraction": nullable(decimal), "close_stop_price": nullable(decimal), "close_stop_distance": nullable(decimal)},
		"EffectiveStrategyStop":    {"close_stop": nullable(decimal), "directive_version": map[string]any{"type": "integer", "minimum": 1}},
		"Contribution":             {"realization_disabled": map[string]any{"type": "boolean"}},
	}
}

func reflectSchema(typ reflect.Type) map[string]any {
	var field func(reflect.Type) any
	field = func(t reflect.Type) any {
		if t.Kind() == reflect.Pointer {
			return map[string]any{"anyOf": []any{field(t.Elem()), map[string]any{"type": "null"}}}
		}
		if t == reflect.TypeFor[Decimal]() {
			return map[string]any{"type": "string", "x-decimal": true}
		}
		if t == reflect.TypeFor[time.Time]() {
			return map[string]any{"type": "string", "format": "date-time"}
		}
		switch t.Kind() {
		case reflect.String:
			return map[string]any{"type": "string"}
		case reflect.Bool:
			return map[string]any{"type": "boolean"}
		case reflect.Int:
			return map[string]any{"type": "integer"}
		case reflect.Slice:
			return map[string]any{"type": "array", "items": field(t.Elem())}
		case reflect.Map:
			return map[string]any{"type": "object", "additionalProperties": field(t.Elem())}
		case reflect.Interface:
			return map[string]any{}
		}
		return map[string]any{"$ref": "#/$defs/" + t.Name()}
	}
	props := map[string]any{}
	required := []string{}
	var add func(reflect.Type)
	add = func(t reflect.Type) {
		for i := 0; i < t.NumField(); i++ {
			f := t.Field(i)
			if f.Anonymous && f.Type.Name() != "Command" {
				add(f.Type)
				continue
			}
			if f.Anonymous {
				continue
			}
			tag := strings.Split(f.Tag.Get("json"), ",")
			props[tag[0]] = field(f.Type)
			if !strings.Contains(f.Tag.Get("json"), "omitempty") {
				required = append(required, tag[0])
			}
		}
	}
	add(typ)
	sort.Strings(required)
	return map[string]any{"type": "object", "additionalProperties": false, "properties": props, "required": required}
}

func extendDynamicShapes(s map[string]shape) {
	for n, v := range DynamicSchemas() {
		raw, _ := json.Marshal(v)
		var sh shape
		json.Unmarshal(raw, &sh)
		s[n] = sh
	}
	for n, fields := range DynamicExtensions() {
		var props map[string]any
		json.Unmarshal(s[n]["properties"], &props)
		for k, v := range fields {
			props[k] = v
		}
		s[n]["properties"], _ = json.Marshal(props)
	}
	// adjust_interval_seconds may be 0 (S2-040).
	var props map[string]any
	json.Unmarshal(s["RebalanceRule"]["properties"], &props)
	props["adjust_interval_seconds"] = map[string]any{"type": "integer", "minimum": 0}
	s["RebalanceRule"]["properties"], _ = json.Marshal(props)
}
