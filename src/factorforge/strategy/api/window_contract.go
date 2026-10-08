package api

import "sort"

func addWindowContract(paths, schemas map[string]any, internal bool) {
	str := map[string]any{"type": "string", "pattern": "^[A-Za-z0-9_.-]{1,160}$"}
	at := map[string]any{"type": "string", "format": "date-time", "pattern": "Z$"}
	ref := func(n string) map[string]any { return map[string]any{"$ref": "#/components/schemas/" + n} }
	obj := func(p map[string]any) map[string]any {
		keys := []string{}
		for k := range p {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		return map[string]any{"type": "object", "additionalProperties": false, "properties": p, "required": keys}
	}
	schemas["PolicyWindow"] = obj(map[string]any{"window_id": str, "start": at, "end": at, "enforce_limits": map[string]any{"type": "boolean"}})
	schemas["TimeWindowPlan"] = obj(map[string]any{"plan_id": str, "object_id": str, "policy_version": str, "source_version": str, "windows": map[string]any{"type": "array", "minItems": 1, "items": ref("PolicyWindow")}})
	schemas["TimeWindowRegistry"] = obj(map[string]any{"object_id": str, "snapshot_version": map[string]any{"type": "integer"}, "plans": map[string]any{"type": "array", "items": ref("TimeWindowPlan")}})
	props := map[string]any{}
	base := schemas["CreateObject"].(map[string]any)["properties"].(map[string]any)
	for _, k := range []string{"schema_version", "request_id", "idempotency_key", "expected_version", "reason"} {
		props[k] = base[k]
	}
	props["plan"] = ref("TimeWindowPlan")
	schemas["WindowCommand"] = obj(props)
	responses := func(code, schema string) map[string]any {
		out := map[string]any{code: map[string]any{"description": "Recorded immutable UTC plan", "content": map[string]any{"application/json": map[string]any{"schema": ref(schema)}}}}
		for _, n := range []string{"401", "403", "404", "409", "422", "423", "503"} {
			out[n] = map[string]any{"description": "Registration or query rejected", "content": map[string]any{"application/json": map[string]any{"schema": ref("Problem")}}}
		}
		return out
	}
	parameters := []any{map[string]any{"name": "object_id", "in": "path", "required": true, "schema": str}}
	route := map[string]any{"get": map[string]any{"operationId": "read_time_windows", "summary": "Read immutable UTC intervals without side effects", "parameters": parameters, "security": []any{map[string]any{"bearerAuth": []string{}}}, "responses": responses("200", "TimeWindowRegistry")}}
	if internal {
		route["post"] = map[string]any{"operationId": "register_time_windows", "summary": "Append intervals using a bound workload identity; preserve historical counters", "parameters": parameters, "security": []any{map[string]any{"bearerAuth": []string{}}}, "requestBody": map[string]any{"required": true, "content": map[string]any{"application/json": map[string]any{"schema": ref("WindowCommand")}}}, "responses": responses("201", "TimeWindowPlan")}
	}
	paths[Prefix+"/objects/{object_id}/time-windows"] = route
}
