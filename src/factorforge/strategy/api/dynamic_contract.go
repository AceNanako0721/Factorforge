package api

import (
	"encoding/json"
	d "github.com/AceNanako0721/Factorforge/src/factorforge/strategy/domain"
	"strings"
)

// addDynamicContract publishes the v2.9.0 commands, records and read view.
func addDynamicContract(paths, schemas map[string]any) {
	convert := func(v any) any {
		raw, _ := json.Marshal(v)
		var out any
		json.Unmarshal([]byte(strings.ReplaceAll(string(raw), "#/$defs/", "#/components/schemas/")), &out)
		return out
	}
	for n, v := range d.DynamicSchemas() {
		schemas[n] = convert(v)
	}
	for n, fields := range d.DynamicExtensions() {
		if model, ok := schemas[n].(map[string]any); ok {
			props := model["properties"].(map[string]any)
			for k, v := range fields {
				props[k] = convert(v)
			}
		}
	}
	if model, ok := schemas["RebalanceRule"].(map[string]any); ok {
		model["properties"].(map[string]any)["adjust_interval_seconds"] = map[string]any{"type": "integer", "minimum": 0}
	}
	ref := func(n string) any { return map[string]any{"$ref": "#/components/schemas/" + n} }
	responses := func(n, description string) any {
		out := map[string]any{"200": map[string]any{"description": description, "content": map[string]any{"application/json": map[string]any{"schema": ref(n)}}}}
		for _, code := range []string{"401", "403", "404", "409", "422", "423", "503"} {
			out[code] = map[string]any{"description": "Rejected; see documented reason codes", "content": map[string]any{"application/json": map[string]any{"schema": ref("Problem")}}}
		}
		return out
	}
	security := []any{map[string]any{"bearerAuth": []string{}}}
	paths[Prefix+"/exit-requests"] = map[string]any{"post": map[string]any{"operationId": "SUBMIT_CLOSE_EXIT", "description": "S2-034: a completed, already available bar close crossing the effective close stop. Workload writers bound for POSITION_STOP only; ACCEPTED requests become a zero target that bypasses band, interval, cooldown and re-entry gate.", "security": security, "requestBody": map[string]any{"required": true, "content": map[string]any{"application/json": map[string]any{"schema": ref("ExitRequestCommand")}}}, "responses": responses("ExitRequestRecord", "Recorded close-stop exit request")}}
	paths[Prefix+"/baseline-transfers"] = map[string]any{"post": map[string]any{"operationId": "TRANSFER_TO_BASELINE", "description": "S2-038: clears contributions (ledger reason TRANSFER) and writes the baseline observation in one transaction; sign kept and combined change within tolerance.", "security": security, "requestBody": map[string]any{"required": true, "content": map[string]any{"application/json": map[string]any{"schema": ref("BaselineTransferCommand")}}}, "responses": responses("BaselineTransferRecord", "Committed transfer")}}
	schemas["ExitRequestCommand"].(map[string]any)["x-error-codes"] = []string{"UNAUTHENTICATED", "STOP_DIRECTIVE_WRITE_FORBIDDEN", "OBJECT_NOT_FOUND", "EXIT_REQUEST_INVALID", "EXIT_REQUEST_CONFLICT", "EXIT_DIRECTION_MISMATCH", "EXIT_NO_CLOSE_STOP", "EXIT_BAR_INCOMPLETE", "EXIT_PRICE_MISMATCH", "EXIT_NOT_TRIGGERED", "STOP_DIRECTIVE_PRICE_UNUSABLE"}
	schemas["BaselineTransferCommand"].(map[string]any)["x-error-codes"] = []string{"WORKLOAD_IDENTITY_REQUIRED", "OBJECT_NOT_FOUND", "TRANSFER_NOT_REGISTERED", "TRANSFER_CONTRIBUTION_INVALID", "TRANSFER_CHANGES_POSITION", "TRANSFER_CONFLICT", "FACTOR_VALUE_OUT_OF_RANGE", "FACTOR_OBSERVATION_CONFLICT", "FACTOR_OBSERVATION_TIME_INVALID"}
	if cmd, ok := schemas["StopDirectiveCommand"].(map[string]any); ok {
		if codes, ok := cmd["x-error-codes"].([]string); ok {
			cmd["x-error-codes"] = append(codes, "STOP_DIRECTIVE_CLOSE_STOP_INVALID")
		}
	}
}
