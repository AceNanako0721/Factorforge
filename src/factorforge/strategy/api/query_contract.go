package api

import (
	"encoding/json"
	"strings"
)

// OpenAPI extends the preserved v2 surface with the S2-024 read-only paths.
// Query policy controls availability, not the published response shape.
func OpenAPI(internal bool) []byte {
	base := publicOpenAPI
	if internal {
		base = workloadOpenAPI
	}
	var document map[string]any
	if json.Unmarshal([]byte(base), &document) != nil {
		panic("invalid embedded strategy contract")
	}
	paths := document["paths"].(map[string]any)
	components := document["components"].(map[string]any)
	schemas := components["schemas"].(map[string]any)
	components["securitySchemes"] = map[string]any{"bearerAuth": map[string]any{"type": "http", "scheme": "bearer"}}
	stringField := func() map[string]any { return map[string]any{"type": "string"} }
	nullableTime := map[string]any{"anyOf": []any{map[string]any{"type": "string", "format": "date-time"}, map[string]any{"type": "null"}}}
	fields := map[string][]string{
		"ObjectSummary":          {"object_id", "state", "instrument_key", "parameter_version", "aggregate_version", "owner_epoch", "recovery_state"},
		"EventReadView":          {"event_id", "family_id", "fact_version", "parent_event_id", "relation", "subject_id", "event_type", "occurred_at", "first_public_at", "evidence_refs", "claims", "object_ids", "state", "novelty"},
		"ScoreReadView":          {"submission_id", "event_id", "fact_version", "object_id", "score_version", "previous_score_id", "revision_kind", "vector", "evidence_refs", "producer_id", "producer_version", "rubric_version", "calibration_version", "completed_at", "admission_receipt"},
		"AttributionReadView":    {"candidate_id", "case_id", "category", "state", "confidence", "calibration_manifest", "eligible", "evidence_refs", "categories", "components", "record_kind"},
		"CounterfactualReadView": {"scenario_id", "case_id", "state", "changed_factor", "learning_frozen", "research_manifest", "baseline_run_id", "result", "interval"},
		"ActivationReadView":     {"activation_id", "object_id", "previous_version", "new_version", "state", "recorded_at", "effective_at", "evidence_refs", "reason_codes", "source_record_id", "record_status"},
		"AuditReadView":          {"action", "code", "request_id", "decision_id", "candidate_id", "version", "state", "source_record_id", "recorded_at"},
	}
	for name, keys := range fields {
		properties := map[string]any{}
		for _, key := range keys {
			properties[key] = map[string]any{"type": []string{"string", "number", "boolean", "null"}}
		}
		schemas[name] = map[string]any{"type": "object", "additionalProperties": false, "properties": properties}
	}
	// Copy only explicitly published fields from existing typed contracts.
	// Claim input dictionaries and model manifests cannot become read payloads.
	project := func(target, source string, keys []string) {
		properties := map[string]any{}
		original := schemas[source].(map[string]any)["properties"].(map[string]any)
		for _, key := range keys {
			properties[key] = original[key]
		}
		schemas[target] = map[string]any{"type": "object", "additionalProperties": false, "required": keys, "properties": properties}
	}
	project("ObjectSummary", "ObservedObject", fields["ObjectSummary"])
	project("EventReadView", "Event", fields["EventReadView"])
	project("ScoreReadView", "ScoreSubmission", fields["ScoreReadView"][:len(fields["ScoreReadView"])-1])
	claimKeys := []string{"claim_id", "normalized_fact", "subject_id", "economic_item", "period", "fact_time", "verified_at", "evidence_refs", "supersedes_claim_id"}
	project("ClaimReadView", "Claim", claimKeys)
	ref := func(name string) map[string]any { return map[string]any{"$ref": "#/components/schemas/" + name} }
	array := func(item map[string]any) map[string]any { return map[string]any{"type": "array", "items": item} }
	props := func(name string) map[string]any { return schemas[name].(map[string]any)["properties"].(map[string]any) }
	props("EventReadView")["claims"] = array(ref("ClaimReadView"))
	props("ScoreReadView")["admission_receipt"] = map[string]any{"anyOf": []any{ref("AdmissionReceipt"), map[string]any{"type": "null"}}}
	schemas["ScoreReadView"].(map[string]any)["required"] = fields["ScoreReadView"]
	scalarObject := func(keys ...string) map[string]any {
		properties := map[string]any{}
		for _, key := range keys {
			properties[key] = map[string]any{"type": []string{"string", "number", "boolean", "null"}}
		}
		return map[string]any{"type": "object", "additionalProperties": false, "properties": properties}
	}
	props("AttributionReadView")["components"] = scalarObject("data", "label", "isolation", "stability")
	props("CounterfactualReadView")["result"] = scalarObject("fees", "net_pnl", "gross_pnl", "equity_change", "risk_used")
	props("CounterfactualReadView")["interval"] = map[string]any{"type": "array", "items": stringField(), "minItems": 2, "maxItems": 2}
	for _, name := range []string{"AttributionReadView", "ActivationReadView"} {
		props(name)["evidence_refs"] = array(stringField())
	}
	props("AttributionReadView")["categories"] = array(stringField())
	props("ActivationReadView")["reason_codes"] = array(stringField())
	props("ActivationReadView")["record_status"] = map[string]any{"enum": []string{"RECORDED", "NOT_RECORDED"}}
	schemas["ActivationReadView"].(map[string]any)["required"] = fields["ActivationReadView"]
	ledgerProperties := map[string]any{}
	for _, name := range []string{"contribution_id", "reason", "start", "injection", "revision_delta", "time_consumption", "price_consumption", "invalidation", "rejected_amount", "end", "budget", "delta_high_water"} {
		ledgerProperties[name] = stringField()
	}
	ledgerProperties["sequence"] = map[string]any{"type": "integer"}
	ledgerProperties["at"] = map[string]any{"type": "string", "format": "date-time"}
	schemas["LedgerEntry"] = map[string]any{"type": "object", "additionalProperties": false, "properties": ledgerProperties}
	// Open dictionaries are limited to explicit projections by the handler;
	// their nested schemas document the only fields eligible for publication.
	for _, view := range []string{"ActivationReadView", "AuditReadView"} {
		properties := schemas[view].(map[string]any)["properties"].(map[string]any)
		properties["recorded_at"] = nullableTime
		if view == "ActivationReadView" {
			properties["effective_at"] = nullableTime
			properties["state"] = map[string]any{"enum": []any{"PUBLISHED", "REJECTED", "FROZEN", "ROLLED_BACK", nil}}
		}
	}
	for _, route := range []struct{ path, view, summary string }{
		{"/objects", "ObjectSummary", "List observed objects"}, {"/events", "EventReadView", "List persisted event fact versions"}, {"/events/{event_id}", "EventReadView", "Read persisted event versions"},
		{"/events/{event_id}/scores", "ScoreReadView", "Read scores with admission receipts"}, {"/objects/{object_id}/ledger", "LedgerEntry", "Read persisted contribution ledger"},
		{"/cases/{case_id}/attributions", "AttributionReadView", "Read candidate and verified attributions"}, {"/cases/{case_id}/counterfactuals", "CounterfactualReadView", "Read stored research counterfactuals"},
		{"/parameter-activations", "ActivationReadView", "Read recorded parameter transitions or NOT_RECORDED"}, {"/audit", "AuditReadView", "Read whitelisted audit fields"},
	} {
		pageName := "ReadPage_" + route.view
		schemas[pageName] = map[string]any{"type": "object", "additionalProperties": false, "required": []string{"items", "cursor", "snapshot_version", "observed_at"}, "properties": map[string]any{"items": map[string]any{"type": "array", "items": map[string]any{"$ref": "#/components/schemas/" + route.view}}, "cursor": map[string]any{"anyOf": []any{stringField(), map[string]any{"type": "null"}}}, "snapshot_version": map[string]any{"type": "integer"}, "observed_at": nullableTime}}
		parameters := []any{}
		for _, key := range []string{"event_id", "object_id", "case_id"} {
			if containsPath(route.path, key) {
				parameters = append(parameters, map[string]any{"name": key, "in": "path", "required": true, "schema": stringField()})
			}
		}
		for _, key := range []string{"cursor", "limit", "object_id", "from", "to"} {
			schema := stringField()
			if key == "limit" {
				schema = map[string]any{"type": "integer", "minimum": 1}
			}
			if key == "from" || key == "to" {
				schema["format"] = "date-time"
			}
			parameters = append(parameters, map[string]any{"name": key, "in": "query", "required": false, "schema": schema})
		}
		if route.path == "/events/{event_id}" {
			parameters = append(parameters, map[string]any{"name": "revision", "in": "query", "schema": map[string]any{"type": "integer", "minimum": 1}})
		}
		if route.view == "LedgerEntry" {
			parameters = append(parameters, map[string]any{"name": "contribution_id", "in": "query", "schema": stringField()})
		}
		responses := map[string]any{"200": map[string]any{"description": "Persisted read page; no state changes or inferred history", "content": map[string]any{"application/json": map[string]any{"schema": map[string]any{"$ref": "#/components/schemas/" + pageName}}}}}
		for _, status := range []string{"401", "403", "404", "409", "422", "503"} {
			responses[status] = map[string]any{"description": "Query refused or unavailable", "content": map[string]any{"application/json": map[string]any{"schema": map[string]any{"$ref": "#/components/schemas/Problem"}}}}
		}
		path := Prefix + route.path
		item, ok := paths[path].(map[string]any)
		if !ok {
			item = map[string]any{}
			paths[path] = item
		}
		item["get"] = map[string]any{"summary": route.summary, "description": "Requires public query scope or an existing workload capability in the bound instance/environment/object range. Limits and cursor retention require registered configuration; snapshot or authorization changes invalidate the signed cursor.", "operationId": "trace_" + route.view + strings.NewReplacer("/", "_", "{", "", "}", "", "-", "_").Replace(route.path), "parameters": parameters, "security": []any{map[string]any{"bearerAuth": []string{}}}, "responses": responses}
	}
	addWindowContract(paths, schemas, internal)
	raw, err := json.MarshalIndent(document, "", "  ")
	if err != nil {
		panic("strategy contract serialization failed")
	}
	return append(raw, '\n')
}
func containsPath(path, key string) bool {
	for i := 0; i+len(key)+2 <= len(path); i++ {
		if path[i:i+len(key)+2] == "{"+key+"}" {
			return true
		}
	}
	return false
}
