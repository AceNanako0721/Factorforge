package api

import (
	"encoding/json"
	d "github.com/AceNanako0721/Factorforge/src/factorforge/strategy/domain"
	"strings"
)

func addCompositionContract(paths, schemas map[string]any) {
	for name, value := range d.CompositionSchemas() {
		raw, _ := json.Marshal(value)
		raw = []byte(strings.ReplaceAll(string(raw), "#/$defs/", "#/components/schemas/"))
		var schema any
		_ = json.Unmarshal(raw, &schema)
		schemas[name] = schema
	}
	ref := func(n string) map[string]any { return map[string]any{"$ref": "#/components/schemas/" + n} }
	add := func(model, key string, v any) {
		schemas[model].(map[string]any)["properties"].(map[string]any)[key] = v
	}
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
	response := func(code, name string) map[string]any {
		out := map[string]any{code: map[string]any{"description": "Recorded composition data", "content": map[string]any{"application/json": map[string]any{"schema": ref(name)}}}}
		for _, status := range []string{"401", "403", "404", "409", "422", "423", "503"} {
			out[status] = map[string]any{"description": "Rejected or unavailable", "content": map[string]any{"application/json": map[string]any{"schema": ref("Problem")}}}
		}
		return out
	}
	for _, cmd := range []struct{ path, name, result, op string }{{"/factor-observations", "FactorObservationCommand", "FactorObservation", "SUBMIT_FACTOR_OBSERVATION"}, {"/scheduled-events", "ScheduledEventCommand", "ScheduledEvent", "UPSERT_SCHEDULED_EVENT"}} {
		paths[Prefix+cmd.path] = map[string]any{"post": map[string]any{"operationId": cmd.op, "description": "Requires a bound workload signal capability; public research credentials do not grant signal qualification.", "security": []any{map[string]any{"bearerAuth": []string{}}}, "requestBody": map[string]any{"required": true, "content": map[string]any{"application/json": map[string]any{"schema": ref(cmd.name)}}}, "responses": response("201", cmd.result)}}
	}
	// Internal pinning and reference bookkeeping are absent from read views.
	raw, _ := json.Marshal(schemas["ScheduledEvent"])
	var event map[string]any
	_ = json.Unmarshal(raw, &event)
	delete(event["properties"].(map[string]any), "windows")
	schemas["ScheduledEventRead"] = event
	schemas["TemplateRead"] = map[string]any{"type": "object", "additionalProperties": false, "required": []string{"template_key", "template"}, "properties": map[string]any{"template_key": map[string]any{"type": "string"}, "template": ref("ScheduledTemplate")}}
	for _, route := range []struct{ path, model string }{{"factor-series", "FactorSeries"}, {"scheduled-events", "ScheduledEventRead"}, {"templates", "TemplateRead"}, {"layer-ledger", "LayerDay"}} {
		page := "ReadPage_" + route.model
		schemas[page] = map[string]any{"type": "object", "additionalProperties": false, "required": []string{"items", "cursor", "snapshot_version", "observed_at"}, "properties": map[string]any{"items": map[string]any{"type": "array", "items": ref(route.model)}, "cursor": map[string]any{"anyOf": []any{map[string]any{"type": "string"}, map[string]any{"type": "null"}}}, "snapshot_version": map[string]any{"type": "integer"}, "observed_at": map[string]any{"anyOf": []any{map[string]any{"type": "string", "format": "date-time"}, map[string]any{"type": "null"}}}}}
		parameters := []any{}
		for _, key := range []string{"cursor", "limit", "object_id", "from", "to"} {
			schema := map[string]any{"type": "string"}
			if key == "limit" {
				schema = map[string]any{"type": "integer", "minimum": 1}
			}
			if key == "from" || key == "to" {
				schema["format"] = "date-time"
			}
			parameters = append(parameters, map[string]any{"name": key, "in": "query", "required": false, "schema": schema})
		}
		path := Prefix + "/" + route.path
		item, ok := paths[path].(map[string]any)
		if !ok {
			item = map[string]any{}
			paths[path] = item
		}
		item["get"] = map[string]any{"operationId": "trace_" + strings.ReplaceAll(route.path, "-", "_"), "summary": "Read bounded composition records without side effects", "parameters": parameters, "security": []any{map[string]any{"bearerAuth": []string{}}}, "responses": response("200", page)}
	}
}
