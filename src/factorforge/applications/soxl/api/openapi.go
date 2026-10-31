package api

import (
	"encoding/json"
	"github.com/AceNanako0721/Factorforge/src/factorforge/applications/soxl/operations"
	"reflect"
	"strings"
	"time"
)

// Shapes are generated from the explicit read values, never persistence maps.
func shape(t reflect.Type) map[string]any {
	if t.Kind() == reflect.Pointer {
		return map[string]any{"anyOf": []any{shape(t.Elem()), map[string]any{"type": "null"}}}
	}
	if t == reflect.TypeFor[time.Time]() {
		return map[string]any{"type": "string", "format": "date-time", "pattern": "Z$"}
	}
	switch t.Kind() {
	case reflect.String:
		return map[string]any{"type": "string"}
	case reflect.Bool:
		return map[string]any{"type": "boolean"}
	case reflect.Int, reflect.Int64:
		return map[string]any{"type": "integer"}
	case reflect.Float64:
		return map[string]any{"type": "number"}
	case reflect.Slice:
		return map[string]any{"type": "array", "items": shape(t.Elem())}
	case reflect.Map:
		if t.Key().Kind() != reflect.String {
			panic("unsupported instance read contract type")
		}
		return map[string]any{"type": "object", "additionalProperties": shape(t.Elem())}
	case reflect.Struct:
		props := map[string]any{}
		required := []string{}
		for i := 0; i < t.NumField(); i++ {
			f := t.Field(i)
			if !f.IsExported() {
				continue
			}
			tag := strings.Split(f.Tag.Get("json"), ",")[0]
			if tag == "-" {
				continue
			}
			if f.Anonymous && tag == "" {
				embedded := shape(f.Type)
				for k, v := range embedded["properties"].(map[string]any) {
					props[k] = v
				}
				required = append(required, embedded["required"].([]string)...)
				continue
			}
			if tag == "" {
				tag = f.Name
			}
			props[tag] = shape(f.Type)
			if !strings.Contains(f.Tag.Get("json"), ",omitempty") {
				required = append(required, tag)
			}
		}
		return map[string]any{"type": "object", "additionalProperties": false, "properties": props, "required": required}
	default:
		panic("unsupported instance read contract type")
	}
}
func OpenAPI() []byte {
	schemas := map[string]any{"DynamicStatusView": shape(reflect.TypeFor[DynamicStatusView]()), "DynamicJudgmentView": shape(reflect.TypeFor[DynamicJudgmentView]()), "DynamicActionView": shape(reflect.TypeFor[DynamicActionView]()), "Problem": shape(reflect.TypeFor[Problem]())}
	ref := func(name string) map[string]any { return map[string]any{"$ref": "#/components/schemas/" + name} }
	paths := map[string]any{}
	for _, r := range []struct {
		path, view string
		page       bool
		query      []string
	}{{"/dynamic/status", "DynamicStatusView", false, nil}, {"/dynamic/judgments", "DynamicJudgmentView", true, []string{"from", "to", "cursor", "limit"}}, {"/dynamic/actions", "DynamicActionView", true, []string{"from", "to", "cursor", "limit"}}} {
		wrapped := shape(reflect.TypeFor[operations.Metadata]())
		props := wrapped["properties"].(map[string]any)
		required := wrapped["required"].([]string)
		props["schema_version"] = map[string]any{"const": "instance-2.0", "type": "string"}
		props["environment"] = map[string]any{"enum": []string{"SIM", "LIVE"}, "type": "string"}
		view := ref(r.view)
		if r.page {
			props["items"] = map[string]any{"type": "array", "items": view}
			props["cursor"] = shape(reflect.TypeFor[*string]())
			required = append(required, "items", "cursor")
		} else {
			props["data"] = view
			required = append(required, "data")
		}
		wrapped["required"] = required
		parameters := []any{}
		parameters = append(parameters, map[string]any{"name": "instance_id", "in": "path", "required": true, "schema": map[string]any{"type": "string", "pattern": "^[A-Za-z0-9_.-]{1,128}$"}})
		for _, name := range r.query {
			schema := map[string]any{"type": "string", "minLength": 1}
			switch name {
			case "limit":
				schema = map[string]any{"type": "integer", "minimum": 1}
			case "from", "to":
				schema["format"] = "date-time"
				schema["pattern"] = "Z$"
			}
			parameters = append(parameters, map[string]any{"name": name, "in": "query", "schema": schema})
		}
		responses := map[string]any{"200": map[string]any{"description": "Recorded instance facts; unknown timestamps and measurements remain null", "content": map[string]any{"application/json": map[string]any{"schema": wrapped}}}}
		for _, code := range []string{"401", "403", "404", "409", "422", "503"} {
			responses[code] = map[string]any{"description": "Stable error code; no private values", "content": map[string]any{"application/json": map[string]any{"schema": ref("Problem")}}}
		}
		paths[Prefix+r.path] = map[string]any{"get": map[string]any{"operationId": "read_instance_" + strings.NewReplacer("/", "_", "-", "_", "{", "", "}", "").Replace(strings.Trim(r.path, "/")), "parameters": parameters, "responses": responses, "security": []any{map[string]any{"bearerAuth": []string{}}}}}
	}
	document := map[string]any{"openapi": "3.1.0", "info": map[string]any{"title": "Factorforge instance read API", "version": "instance-2.0"}, "paths": paths, "components": map[string]any{"schemas": schemas, "securitySchemes": map[string]any{"bearerAuth": map[string]any{"type": "http", "scheme": "bearer"}}}}
	raw, err := json.MarshalIndent(document, "", "  ")
	if err != nil {
		panic("invalid instance read contract")
	}
	return append(raw, '\n')
}
