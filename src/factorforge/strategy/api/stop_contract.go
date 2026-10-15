package api

import (
	"encoding/json"
	d "github.com/AceNanako0721/Factorforge/src/factorforge/strategy/domain"
	"strings"
)

func addStopContract(paths, schemas map[string]any) {
	convert := func(v any) any {
		raw, _ := json.Marshal(v)
		var out any
		json.Unmarshal([]byte(strings.ReplaceAll(string(raw), "#/$defs/", "#/components/schemas/")), &out)
		return out
	}
	for n, v := range d.StopDirectiveSchemas() {
		schemas[n] = convert(v)
	}
	for n, fields := range d.StopContractExtensions() {
		if model, ok := schemas[n].(map[string]any); ok {
			props := model["properties"].(map[string]any)
			for k, v := range fields {
				props[k] = convert(v)
			}
		}
	}
	ref := func(n string) any { return map[string]any{"$ref": "#/components/schemas/" + n} }
	responses := func(n string) any {
		out := map[string]any{"200": map[string]any{"description": "Recorded directive; acceptance is separate from P1 effectiveness", "content": map[string]any{"application/json": map[string]any{"schema": ref(n)}}}}
		for _, code := range []string{"401", "403", "404", "409", "422", "423", "503"} {
			out[code] = map[string]any{"description": "Stop directive rejected; see documented reason codes", "content": map[string]any{"application/json": map[string]any{"schema": ref("Problem")}}}
		}
		return out
	}
	paths[Prefix+"/stop-directives"] = map[string]any{"post": map[string]any{"operationId": "SUBMIT_STOP_DIRECTIVE", "description": "Public object writers record RESEARCH_ONLY. Executable writes require object/environment/kind source binding plus workload signal:sim/live; RECORDED means UPPER_MANAGED declaration and insurance verified, VERIFIED means venue STRATEGY confirmed.", "security": []any{map[string]any{"bearerAuth": []string{}}}, "requestBody": map[string]any{"required": true, "content": map[string]any{"application/json": map[string]any{"schema": ref("StopDirectiveCommand")}}}, "responses": responses("StopDirectiveReceipt")}}
	schemas["StopDirectivePage"] = map[string]any{"type": "object", "additionalProperties": false, "required": []string{"items", "cursor", "snapshot_version"}, "properties": map[string]any{"items": map[string]any{"type": "array", "items": ref("StopDirectiveRecord")}, "cursor": map[string]any{"type": "string"}, "snapshot_version": map[string]any{"type": "integer"}}}
	paths[Prefix+"/objects/{object_id}/stop-directives"] = map[string]any{"get": map[string]any{"operationId": "GET_STOP_DIRECTIVES", "security": []any{map[string]any{"bearerAuth": []string{}}}, "parameters": []any{map[string]any{"name": "object_id", "in": "path", "required": true, "schema": map[string]any{"type": "string"}}, map[string]any{"name": "cursor", "in": "query", "schema": map[string]any{"type": "string"}}, map[string]any{"name": "limit", "in": "query", "schema": map[string]any{"type": "integer", "minimum": 1, "maximum": 100}}}, "responses": responses("StopDirectivePage")}}
	// StopPlan is a union: managed declarations omit venue-only fields.
	if model, ok := schemas["StopPlan"].(map[string]any); ok {
		required := []any{}
		for _, v := range model["required"].([]any) {
			if !d.Has([]string{"trigger_price", "covered_quantity", "exit_order_type"}, v.(string)) {
				required = append(required, v)
			}
		}
		model["required"] = required
	}
	schemas["StopDirectiveCommand"].(map[string]any)["x-error-codes"] = []string{"UNAUTHENTICATED", "STOP_DIRECTIVE_WRITE_FORBIDDEN", "OBJECT_NOT_FOUND", "STOP_DIRECTIVE_VERSION_CONFLICT", "STOP_DIRECTIVE_CONTENT_CONFLICT", "STOP_DIRECTIVE_INVALID", "STOP_DIRECTIVE_DIRECTION_MISMATCH", "STOP_DIRECTIVE_NOT_EFFECTIVE", "STOP_DIRECTIVE_EXPIRED", "STOP_DIRECTIVE_TRIGGER_UNSUPPORTED", "STOP_DIRECTIVE_TICK_MISMATCH", "STOP_DIRECTIVE_ALREADY_CROSSED", "STOP_DIRECTIVE_PRICE_UNUSABLE", "P1_LOWER_COMPAT_REQUIRED", "STRATEGY_NOT_TIGHTER_THAN_INSURANCE", "MANAGED_STOP_OUTSIDE_INSURANCE"}
}
