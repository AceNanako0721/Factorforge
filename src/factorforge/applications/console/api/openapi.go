package api

import (
	"encoding/json"
	app "github.com/AceNanako0721/Factorforge/src/factorforge/applications/console/application"
	d "github.com/AceNanako0721/Factorforge/src/factorforge/applications/console/domain"
	"sort"
	"strings"
)

func object(props map[string]any, required ...string) map[string]any {
	if required == nil {
		required = []string{}
	}
	return map[string]any{"type": "object", "properties": props, "required": required, "additionalProperties": false}
}
func stringSchema() map[string]any { return map[string]any{"type": "string"} }
func nullable(v any) map[string]any {
	return map[string]any{"anyOf": []any{v, map[string]any{"type": "null"}}}
}
func OpenAPI() []byte {
	str := stringSchema()
	at := map[string]any{"type": "string", "format": "date-time"}
	id := map[string]any{"type": "string", "pattern": "^[A-Za-z0-9_.-]{1,128}$"}
	integer := map[string]any{"type": "integer"}
	stringsArray := map[string]any{"type": "array", "items": str}
	session := object(map[string]any{"user_id": id, "display_name": str, "capabilities": stringsArray, "expires_at": at, "csrf": str}, "user_id", "display_name", "capabilities", "expires_at", "csrf")
	run := object(map[string]any{"environment": map[string]any{"type": "string", "enum": []string{"SIM", "LIVE"}}, "account_id": id, "run_id": id}, "environment", "account_id", "run_id")
	instrument := object(map[string]any{"venue": id, "product": map[string]any{"type": "string", "const": "LINEAR_PERPETUAL"}, "instrument_id": id}, "venue", "product", "instrument_id")
	selection := object(map[string]any{"selection_id": id, "deployment_id": id, "environment": map[string]any{"type": "string", "enum": []string{"SIM", "LIVE"}}, "instance_id": id, "object_id": str, "trading_run_key": run, "instrument_key": instrument, "owner_id": id, "binding_version": id}, "selection_id", "deployment_id", "environment", "instance_id", "object_id", "trading_run_key", "instrument_key", "owner_id", "binding_version")
	schemas := map[string]any{"SessionView": session, "SelectionView": selection, "Problem": object(map[string]any{"code": str, "message": str, "retryable": map[string]any{"type": "boolean"}}, "code", "message", "retryable")}
	variants := []any{}
	names := []string{}
	for name := range d.Routes {
		if !strings.HasSuffix(name, ".health") && name != "objects" && name != "job" {
			names = append(names, name)
		}
	}
	sort.Strings(names)
	for _, name := range names {
		route := d.Routes[name]
		safe, e := d.SafeSchema(route.Service, route.Path, name == "evidence")
		if e != nil {
			panic("CONSOLE_SCHEMA_UNAVAILABLE")
		}
		p := object(map[string]any{"source": map[string]any{"type": "string", "const": name}, "state": map[string]any{"type": "string", "enum": []string{"AVAILABLE", "EMPTY", "UNAVAILABLE", "NOT_CONFIGURED", "NOT_APPLICABLE", "FORBIDDEN", "MISSING"}}, "code": nullable(str), "fetched_at": at, "observed_at": nullable(at), "source_version": nullable(str), "snapshot_version": nullable(str), "data": nullable(safe), "cursor": nullable(str), "retry_at": nullable(at)}, "source", "state", "code", "fetched_at", "observed_at", "source_version", "snapshot_version", "data", "cursor", "retry_at")
		variants = append(variants, p)
	}
	// instance.health is a visible panel as well as a binding probe.
	safe, e := d.SafeSchema("instances", d.Routes["instance.health"].Path, false)
	if e != nil {
		panic("CONSOLE_SCHEMA_UNAVAILABLE")
	}
	variants = append(variants, object(map[string]any{"source": map[string]any{"type": "string", "const": "instance.health"}, "state": str, "code": nullable(str), "fetched_at": at, "observed_at": nullable(at), "source_version": nullable(str), "snapshot_version": nullable(str), "data": nullable(safe), "cursor": nullable(str), "retry_at": nullable(at)}, "source", "state", "code", "fetched_at", "observed_at", "source_version", "snapshot_version", "data", "cursor", "retry_at"))
	schemas["ConsoleResponse"] = object(map[string]any{"schema_version": map[string]any{"const": d.SchemaVersion, "type": "string"}, "selection": selection, "generated_at": at, "panels": map[string]any{"type": "array", "items": map[string]any{"anyOf": variants}}}, "schema_version", "selection", "generated_at", "panels")
	paths := map[string]any{}
	ref := func(name string) map[string]any { return map[string]any{"$ref": "#/components/schemas/" + name} }
	response := func(schema any) map[string]any {
		return map[string]any{"description": "Successful read", "content": map[string]any{"application/json": map[string]any{"schema": schema}}}
	}
	add := func(path, method string, schema any, authenticated bool, params []any, body any, status string) {
		responses := map[string]any{status: response(schema)}
		for _, code := range []string{"401", "403", "404", "409", "422", "429", "503"} {
			responses[code] = response(ref("Problem"))
		}
		if status == "204" {
			responses[status] = map[string]any{"description": "Session revoked"}
		}
		op := map[string]any{"operationId": method + "_" + strings.NewReplacer("/", "_", "{", "", "}", "", "-", "_").Replace(path), "responses": responses}
		if authenticated {
			op["security"] = []any{map[string]any{"consoleCookie": []string{}}}
		}
		if len(params) > 0 {
			op["parameters"] = params
		}
		if body != nil {
			op["requestBody"] = map[string]any{"required": true, "content": map[string]any{"application/json": map[string]any{"schema": body}}}
		}
		full := Prefix + path
		if paths[full] == nil {
			paths[full] = map[string]any{}
		}
		paths[full].(map[string]any)[method] = op
	}
	param := func(name string, required bool, schema any) map[string]any {
		return map[string]any{"name": name, "in": "query", "required": required, "schema": schema}
	}
	add("/session/challenge", "get", object(map[string]any{"csrf": str, "expires_at": at}, "csrf", "expires_at"), false, nil, nil, "200")
	add("/session", "get", ref("SessionView"), true, nil, nil, "200")
	add("/session", "post", ref("SessionView"), false, nil, object(map[string]any{"username": str, "password": str, "csrf": str}, "username", "password", "csrf"), "200")
	add("/session", "delete", nil, true, []any{map[string]any{"name": "X-CSRF-Token", "in": "header", "required": true, "schema": str}}, nil, "204")
	add("/selections", "get", map[string]any{"type": "array", "items": selection}, true, []any{param("environment", false, map[string]any{"type": "string", "enum": []string{"SIM", "LIVE"}})}, nil, "200")
	pages := map[string]any{}
	for page := range app.Views {
		pages[page] = str
	}
	add("/capabilities", "get", object(map[string]any{"schema_version": str, "pages": object(pages), "refresh_seconds": integer, "max_retries": integer}, "schema_version", "pages", "refresh_seconds", "max_retries"), true, []any{param("selection_id", false, id)}, nil, "200")
	for _, path := range []string{"overview", "market", "events", "events/{event_id}", "evidence/{evidence_id}", "sentiment", "decisions", "execution", "cases", "cases/{case_id}", "learning", "operations", "reports", "audit", "export"} {
		params := []any{param("selection_id", true, id), param("source", false, str), param("cursor", false, str), param("limit", false, map[string]any{"type": "integer", "minimum": 1})}
		if strings.Contains(path, "{") {
			name := strings.TrimSuffix(strings.Split(path, "{")[1], "}")
			params = append(params, map[string]any{"name": name, "in": "path", "required": true, "schema": id})
		}
		extras := []string{}
		switch path {
		case "market":
			extras = []string{"interval", "start", "end"}
		case "events", "reports", "audit", "learning":
			extras = []string{"from", "to"}
		case "events/{event_id}":
			extras = []string{"revision"}
		case "sentiment":
			extras = []string{"contribution_id"}
		case "operations":
			extras = []string{"queue_kind", "state"}
		case "export":
			extras = []string{"view", "format", "from", "to", "interval", "start", "end"}
		}
		for _, name := range extras {
			schema := any(str)
			if name == "revision" {
				schema = integer
			}
			if d.Has([]string{"from", "to", "start", "end"}, name) {
				schema = at
			}
			params = append(params, param(name, false, schema))
		}
		add("/"+path, "get", ref("ConsoleResponse"), true, params, nil, "200")
		if path == "export" {
			op := paths[Prefix+"/export"].(map[string]any)["get"].(map[string]any)
			op["responses"].(map[string]any)["200"].(map[string]any)["content"].(map[string]any)["text/csv"] = map[string]any{"schema": str}
		}
	}
	doc := map[string]any{"openapi": "3.1.0", "info": map[string]any{"title": "Factorforge read-only console", "version": d.SchemaVersion}, "paths": paths, "components": map[string]any{"schemas": schemas, "securitySchemes": map[string]any{"consoleCookie": map[string]any{"type": "apiKey", "in": "cookie", "name": sessionCookie}}}}
	raw, _ := json.MarshalIndent(doc, "", "  ")
	return append(raw, '\n')
}
