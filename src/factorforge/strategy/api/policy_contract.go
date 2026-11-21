package api

// addPolicyContract publishes the S2-024 policy read: the object's registered
// policy with its effective composition.
func addPolicyContract(paths, schemas map[string]any) {
	str := map[string]any{"type": "string", "pattern": "^[A-Za-z0-9_.-]{1,160}$"}
	ref := func(n string) map[string]any { return map[string]any{"$ref": "#/components/schemas/" + n} }
	schemas["PolicyRead"] = map[string]any{"type": "object", "additionalProperties": false, "required": []string{"object_id", "snapshot_version", "policy_version", "policy"}, "properties": map[string]any{
		"object_id": str, "snapshot_version": map[string]any{"type": "integer"}, "policy_version": map[string]any{"type": "string", "minLength": 1, "maxLength": 160}, "policy": ref("Policy"),
	}}
	responses := map[string]any{"200": map[string]any{"description": "Registered policy with effective composition", "content": map[string]any{"application/json": map[string]any{"schema": ref("PolicyRead")}}}}
	for _, n := range []string{"401", "403", "404", "422", "503"} {
		responses[n] = map[string]any{"description": "Query rejected", "content": map[string]any{"application/json": map[string]any{"schema": ref("Problem")}}}
	}
	parameters := []any{map[string]any{"name": "object_id", "in": "path", "required": true, "schema": str}}
	paths[Prefix+"/objects/{object_id}/policy"] = map[string]any{"get": map[string]any{"operationId": "read_object_policy", "summary": "Read the object's registered policy without side effects", "parameters": parameters, "security": []any{map[string]any{"bearerAuth": []string{}}}, "responses": responses}}
}
