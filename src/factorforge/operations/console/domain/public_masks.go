package domain

// These finite browser shapes expose recorded case/research facts while the
// lower legacy contract permits arbitrary dictionaries. No raw snapshot passes.
func publicMask(doc map[string]any, field string) map[string]any {
	str := map[string]any{"type": "string"}
	amount := map[string]any{"type": "string", "pattern": `^-?(?:0|[1-9][0-9]*)(?:\.[0-9]+)?(?:[Ee][+-]?[0-9]+)?$`}
	boolean := map[string]any{"type": "boolean"}
	nullable := func(v any) map[string]any { return map[string]any{"anyOf": []any{v, map[string]any{"type": "null"}}} }
	list := func(v any) map[string]any { return map[string]any{"type": "array", "items": v} }
	object := func(p map[string]any) map[string]any {
		return map[string]any{"type": "object", "properties": p, "additionalProperties": false}
	}
	switch field {
	case "risk_lots":
		return list(object(map[string]any{"fill_id": str, "quantity": amount, "entry_price": amount, "parameter_version": str, "source_decision_id": nullable(str), "stop_frozen": nullable(map[string]any{"$ref": "#/components/schemas/StopPlan"})}))
	case "entry_snapshot":
		return object(map[string]any{"parameter_version": str, "quantity": amount, "cash_flow": amount, "cost_known": boolean, "h_ref": amount, "policy_version": str, "source_decision_id": nullable(str)})
	case "manifest":
		return object(map[string]any{"run_id": str, "object_id": str, "object_ids": list(str), "licence_refs": list(str), "data_manifest": str, "hypotheses": list(str), "primary_metrics": list(str), "embargo_seconds": map[string]any{"type": "integer", "minimum": 0}, "sealed_set": str, "finalized": boolean, "ablations": list(str), "failed_trials": map[string]any{"type": "integer", "minimum": 0}})
	case "result":
		return object(map[string]any{"status": str, "reason_codes": list(str), "passed": boolean, "unknown_ratio": amount, "cost": amount, "latency_p95": amount, "latency_p99": amount, "forward_verified": boolean, "frozen_control_difference": amount})
	}
	return nil
}

func scopeLegacy(path string, value any, objectID string) any {
	rows, ok := value.([]any)
	if !ok {
		return value
	}
	if path != Routes["parameters"].Path && path != Routes["learning"].Path && path != Routes["validation"].Path {
		return value
	}
	filtered := []any{}
	for _, row := range rows {
		m, ok := row.(map[string]any)
		if !ok {
			continue
		}
		allowed := false
		switch path {
		case Routes["parameters"].Path:
			allowed = m["scope"] == objectID || m["scope"] == "*"
		case Routes["learning"].Path:
			allowed = m["object_id"] == objectID
		case Routes["validation"].Path:
			manifest, _ := m["manifest"].(map[string]any)
			allowed = manifest["object_id"] == objectID
			if ids, ok := manifest["object_ids"].([]any); ok {
				for _, id := range ids {
					if id == objectID {
						allowed = true
					}
				}
			}
		}
		if allowed {
			filtered = append(filtered, row)
		}
	}
	return filtered
}
