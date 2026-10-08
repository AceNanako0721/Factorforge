package domain

import (
	"encoding/json"
	"fmt"
	"strings"
)

// SafeSchema describes the same finite projection as Project. Source contracts
// are embedded public schemas; callers cannot introduce files/URLs or $refs.
func SafeSchema(service, path string, original bool) (map[string]any, error) {
	shapeOnce.Do(loadShapes)
	if shapeErr != nil {
		return nil, shapeErr
	}
	shape, ok := shapes[service]
	if !ok {
		return nil, fmt.Errorf("schema")
	}
	v, e := safeSchema(shape.Doc, shape.Routes[path], "", original, 0)
	if e != nil {
		return nil, e
	}
	return v, nil
}
func safeSchema(doc map[string]any, value any, field string, original bool, depth int) (map[string]any, error) {
	if depth > 64 {
		return nil, fmt.Errorf("schema depth")
	}
	if mask := publicMask(doc, field); mask != nil {
		value = mask
	}
	s, ok := value.(map[string]any)
	if !ok {
		return map[string]any{"type": "null"}, nil
	}
	if ref, ok := s["$ref"].(string); ok {
		if !strings.HasPrefix(ref, "#/components/schemas/") {
			return nil, fmt.Errorf("ref")
		}
		target := doc["components"].(map[string]any)["schemas"].(map[string]any)[strings.TrimPrefix(ref, "#/components/schemas/")]
		return safeSchema(doc, target, field, original, depth+1)
	}
	out := map[string]any{}
	for _, key := range []string{"type", "const", "enum", "pattern", "format", "minLength", "maxLength", "minimum", "maximum", "exclusiveMinimum", "exclusiveMaximum", "minItems", "maxItems", "uniqueItems", "title", "default"} {
		if x, ok := s[key]; ok {
			out[key] = x
		}
	}
	if choices, ok := s["anyOf"].([]any); ok {
		xs := []any{}
		for _, c := range choices {
			v, e := safeSchema(doc, c, field, original, depth+1)
			if e != nil {
				return nil, e
			}
			xs = append(xs, v)
		}
		out["anyOf"] = xs
	}
	if child, ok := s["items"]; ok {
		v, e := safeSchema(doc, child, "", original, depth+1)
		if e != nil {
			return nil, e
		}
		out["items"] = v
	}
	if prefix, ok := s["prefixItems"].([]any); ok {
		xs := []any{}
		for _, child := range prefix {
			v, e := safeSchema(doc, child, "", original, depth+1)
			if e != nil {
				return nil, e
			}
			xs = append(xs, v)
		}
		out["prefixItems"] = xs
	}
	if s["type"] == "object" {
		props, ok := s["properties"].(map[string]any)
		if !ok {
			if Has([]string{"cash_balances", "source_event_versions", "values", "bounds", "steps", "evidence_gates", "pnl_components", "labels", "numbers_with_units"}, field) {
				if child, ok := s["additionalProperties"].(map[string]any); ok {
					v, e := safeSchema(doc, child, "", original, depth+1)
					if e != nil {
						return nil, e
					}
					out["additionalProperties"] = v
					return out, nil
				}
			}
			return map[string]any{"type": "null"}, nil
		}
		safe := map[string]any{}
		for key, child := range props {
			if forbidden[key] && !(key == "raw_text" && original) {
				continue
			}
			v, e := safeSchema(doc, child, key, original, depth+1)
			if e != nil {
				return nil, e
			}
			safe[key] = v
		}
		out["properties"] = safe
		out["additionalProperties"] = false
		if required, ok := s["required"].([]any); ok {
			xs := []any{}
			for _, key := range required {
				if _, exists := safe[key.(string)]; exists {
					xs = append(xs, key)
				}
			}
			out["required"] = xs
		}
	}
	raw, _ := json.Marshal(out)
	var clone map[string]any
	json.Unmarshal(raw, &clone)
	return clone, nil
}
