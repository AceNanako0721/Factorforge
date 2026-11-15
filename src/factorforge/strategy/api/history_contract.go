package api

import (
	"encoding/json"

	d "github.com/AceNanako0721/Factorforge/src/factorforge/strategy/domain"
)

// addHistoryContract publishes the batch list and the decision history (S2-024).
func addHistoryContract(paths, schemas map[string]any) {
	ref := func(n string) any { return map[string]any{"$ref": "#/components/schemas/" + n} }
	nullable := func(v any) any { return map[string]any{"anyOf": []any{v, map[string]any{"type": "null"}}} }
	responses := func(schema any, description string) any {
		out := map[string]any{"200": map[string]any{"description": description, "content": map[string]any{"application/json": map[string]any{"schema": schema}}}}
		for _, code := range []string{"401", "403", "404", "422", "503"} {
			out[code] = map[string]any{"description": "Query refused or unavailable", "content": map[string]any{"application/json": map[string]any{"schema": ref("Problem")}}}
		}
		return out
	}
	security := []any{map[string]any{"bearerAuth": []string{}}}
	object := map[string]any{"name": "object_id", "in": "path", "required": true, "schema": map[string]any{"type": "string"}}
	schemas["DecisionHistoryItem"] = map[string]any{"type": "object", "additionalProperties": false, "required": []string{"decision", "delivery_state", "delivery_reason"}, "properties": map[string]any{
		"decision": ref("DecisionView"), "delivery_state": nullable(map[string]any{"type": "string"}), "delivery_reason": nullable(map[string]any{"type": "string"})}}
	decimal := map[string]any{"type": "string"}
	level := map[string]any{"type": "object", "additionalProperties": false, "required": []string{"exposure", "enter", "exit"}, "properties": map[string]any{"exposure": decimal, "enter": decimal, "exit": decimal}}
	schemas["SizingView"] = map[string]any{"type": "object", "additionalProperties": false, "required": []string{"max_risk_fraction", "object_loss_budget", "fee_rate", "slippage_fraction", "gap_fraction", "band_ratio_of_unit", "levels"}, "properties": map[string]any{
		"max_risk_fraction": nullable(decimal), "object_loss_budget": decimal, "fee_rate": decimal, "slippage_fraction": decimal, "gap_fraction": decimal, "band_ratio_of_unit": nullable(decimal), "levels": map[string]any{"type": "array", "items": level}}}
	schemas["DecisionHistoryPage"] = map[string]any{"type": "object", "additionalProperties": false, "required": []string{"items", "cursor", "snapshot_version", "sizing"}, "properties": map[string]any{
		"items": map[string]any{"type": "array", "items": ref("DecisionHistoryItem")}, "cursor": map[string]any{"type": "string"}, "snapshot_version": map[string]any{"type": "integer"}, "sizing": nullable(ref("SizingView"))}}
	// BatchView is BatchRecord with closed_at null while the batch is open.
	var batch map[string]any
	raw, _ := json.Marshal(schemas["BatchRecord"])
	_ = json.Unmarshal(raw, &batch)
	batch["properties"].(map[string]any)["closed_at"] = nullable(map[string]any{"type": "string", "format": "date-time"})
	schemas["BatchView"] = batch
	paths[Prefix+"/objects/{object_id}/batches"] = map[string]any{"get": map[string]any{"operationId": "GET_OBJECT_BATCHES", "description": "S2-024: the object's risk batches (S2-033) by opening time; an open batch has a null closed_at and an empty exit_kind.", "security": security, "parameters": []any{object},
		"responses": responses(map[string]any{"type": "array", "items": ref("BatchView")}, "Risk batches")}}
	paths[Prefix+"/objects/{object_id}/decision-history"] = map[string]any{"get": map[string]any{"operationId": "GET_DECISION_HISTORY", "description": "S2-024: every decision of the object with available_cutoff in [from, to), archived ones included, oldest first, with the delivery state and reason of its target, and the object's current sizing limits, costs and levels (S2-033). The cursor is the last item's cutoff and decision id.", "security": security,
		"parameters": []any{object,
			map[string]any{"name": "from", "in": "query", "required": true, "schema": map[string]any{"type": "string", "format": "date-time"}},
			map[string]any{"name": "to", "in": "query", "description": "Defaults to from plus the maximum span.", "schema": map[string]any{"type": "string", "format": "date-time"}},
			map[string]any{"name": "cursor", "in": "query", "schema": map[string]any{"type": "string"}},
			map[string]any{"name": "limit", "in": "query", "schema": map[string]any{"type": "integer", "minimum": 1, "maximum": d.DecisionHistoryMaxLimit}}},
		"responses": responses(ref("DecisionHistoryPage"), "Decision history page")}}
}
