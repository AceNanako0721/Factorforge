package domain

import (
	"encoding/json"
	"reflect"
	"sort"
	"strings"
	"time"
)

// StopDirectiveSchemas is the single source for native admission and OpenAPI.
func StopDirectiveSchemas() map[string]any {
	models := []any{StopDirective{}, StopDirectiveCommand{}, StopDirectiveReceipt{}, StopDirectiveRecord{}, StopDirectiveWriterBinding{}, EffectiveStrategyStop{}}
	schemas := map[string]any{}
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
		}
		return map[string]any{"$ref": "#/$defs/" + t.Name()}
	}
	for _, model := range models {
		typ := reflect.TypeOf(model)
		props := map[string]any{}
		required := []string{}
		optional := []string{}
		var add func(reflect.Type)
		add = func(t reflect.Type) {
			for i := 0; i < t.NumField(); i++ {
				f := t.Field(i)
				if f.Anonymous {
					add(f.Type)
					continue
				}
				key := strings.Split(f.Tag.Get("json"), ",")[0]
				props[key] = field(f.Type)
				if strings.Contains(f.Tag.Get("json"), "omitempty") {
					optional = append(optional, key)
					continue
				}
				required = append(required, key)
			}
		}
		add(typ)
		if typ.Name() == "StopDirectiveCommand" || typ.Name() == "StopDirective" {
			props["kind"] = map[string]any{"type": "string", "enum": []string{"POSITION_STOP", "INITIAL_DISTANCE"}}
			props["position_direction"] = map[string]any{"type": "string", "enum": []string{"LONG", "SHORT"}}
			props["mode"] = map[string]any{"type": "string", "enum": []string{"VENUE", "UPPER_MANAGED"}, "default": "VENUE"}
			for _, name := range []string{"directive_id", "object_id", "source_module"} {
				props[name] = map[string]any{"type": "string", "minLength": 1, "maxLength": 128, "pattern": "^[A-Za-z0-9_.-]+$"}
			}
			for _, name := range []string{"version", "valid_for_seconds"} {
				props[name] = map[string]any{"type": "integer", "minimum": 1}
			}
			props["reason_codes"] = map[string]any{"type": "array", "minItems": 1, "maxItems": 32, "items": map[string]any{"type": "string", "minLength": 1, "maxLength": 128}}
			if typ.Name() == "StopDirectiveCommand" {
				var command map[string]any
				json.Unmarshal(shapesCommandProperties(), &command)
				for k, v := range command {
					props[k] = v
				}
			}
			required = nil
			for i := 0; i < typ.NumField(); i++ {
				_ = i
			}
			for k := range props {
				if !Has(append([]string{"mode", "overshoot_allowance"}, optional...), k) {
					required = append(required, k)
				}
			}
		}
		sort.Strings(required)
		schemas[typ.Name()] = map[string]any{"type": "object", "additionalProperties": false, "properties": props, "required": required}
	}
	return schemas
}
func StopContractExtensions() map[string]map[string]any {
	ref := func(n string) any { return map[string]any{"$ref": "#/$defs/" + n} }
	dict := func(n string) any { return map[string]any{"type": "object", "additionalProperties": ref(n)} }
	nullableString := map[string]any{"anyOf": []any{map[string]any{"type": "string"}, map[string]any{"type": "null"}}}
	return map[string]map[string]any{
		"Policy":          {"reverse_confirm_seconds": map[string]any{"anyOf": []any{map[string]any{"type": "integer", "minimum": 0}, map[string]any{"type": "null"}}}},
		"StrategyState":   {"stop_directive_versions": map[string]any{"type": "object", "additionalProperties": map[string]any{"type": "integer", "minimum": 1}}, "research_stop_versions": map[string]any{"type": "object", "additionalProperties": map[string]any{"type": "integer", "minimum": 1}}, "research_stop_directives": dict("StopDirectiveRecord"), "stop_directives": dict("StopDirectiveRecord"), "stop_directive_writers": dict("StopDirectiveWriterBinding"), "last_effective_strategy_stop": dict("EffectiveStrategyStop")},
		"StopPlan":        {"strategy_protection_mode": map[string]any{"type": "string", "enum": []string{"VENUE", "UPPER_MANAGED"}}, "managed_stop_price": map[string]any{"anyOf": []any{map[string]any{"type": "string", "x-decimal": true}, map[string]any{"type": "null"}}}, "overshoot_allowance": map[string]any{"anyOf": []any{map[string]any{"type": "string", "x-decimal": true}, map[string]any{"type": "null"}}}},
		"TargetOutbox":    {"stop_directive": map[string]any{"anyOf": []any{ref("StopDirectiveRecord"), map[string]any{"type": "null"}}}},
		"DecisionView":    {"stop_directive_id": nullableString, "protection_only": map[string]any{"type": "boolean"}},
		"TradingSnapshot": {"lower_compat": map[string]any{"type": "boolean"}, "positions": map[string]any{"type": "object"}, "targets": map[string]any{"type": "object"}, "trigger_prices": map[string]any{"type": "object"}},
	}
}
func extendStopShapes(s map[string]shape) {
	for n, v := range StopDirectiveSchemas() {
		raw, _ := json.Marshal(v)
		var sh shape
		json.Unmarshal(raw, &sh)
		s[n] = sh
	}
	for n, fields := range StopContractExtensions() {
		var props map[string]any
		json.Unmarshal(s[n]["properties"], &props)
		for k, v := range fields {
			props[k] = v
		}
		s[n]["properties"], _ = json.Marshal(props)
	}
	var required []string
	json.Unmarshal(s["StopPlan"]["required"], &required)
	out := []string{}
	for _, k := range required {
		if !Has([]string{"trigger_price", "covered_quantity", "exit_order_type"}, k) {
			out = append(out, k)
		}
	}
	s["StopPlan"]["required"], _ = json.Marshal(out)
}
