package engineering_test

import (
	"path/filepath"
	"testing"

	guard "github.com/AceNanako0721/Factorforge/tools/contractguard"
)

func TestContractDecimalLookaheadAndOfflineLoader(t *testing.T) {
	schema := map[string]any{"type": "string", "pattern": `^-?(?!0+(?:\.0+)?$)[0-9]+(?:\.[0-9]+)?$`}
	for _, value := range []string{"1", "-0.01", "00.10", "123456789012345678901234567890.123456789"} {
		if err := guard.Validate(schema, value); err != nil {
			t.Fatal(value, err)
		}
	}
	for _, value := range []string{"0", "-0", "0.000", "1e3", "+1", "NaN", "1suffix"} {
		if guard.Validate(schema, value) == nil {
			t.Fatal("invalid decimal accepted", value)
		}
	}
	if guard.Validate(map[string]any{"$ref": "https://example.invalid/schema"}, map[string]any{}) == nil {
		t.Fatal("network schema loader enabled")
	}
	root := t.TempDir()
	base := filepath.Join(root, "base.json")
	for _, ref := range []string{"../outside.json", "https://example.invalid/schema"} {
		if _, err := guard.Bundle(map[string]any{"$ref": ref}, base, root, nil); err == nil {
			t.Fatal("unsafe file reference accepted")
		}
	}
	put(t, root, "cycle.json", `{"$ref":"cycle.json"}`)
	if _, err := guard.Bundle(map[string]any{"$ref": "cycle.json"}, base, root, nil); err == nil {
		t.Fatal("cyclic schema accepted")
	}
}
func TestContractBreakingSurfaceAndTypedStubs(t *testing.T) {
	fixture := func(id string) map[string]any {
		operation := map[string]any{"operationId": id, "parameters": []any{map[string]any{"name": "id", "in": "path", "required": true}}, "responses": map[string]any{"200": map[string]any{"description": "OK"}}}
		properties := map[string]any{"id": map[string]any{"type": "string", "enum": []any{"a", "b"}}}
		schemas := map[string]any{"Record": map[string]any{"type": "object", "required": []any{"id"}, "properties": properties}}
		return map[string]any{"openapi": "3.1.0", "info": map[string]any{"title": "Fixture", "version": "2.1.1"}, "paths": map[string]any{"/items/{id}": map[string]any{"get": operation}}, "components": map[string]any{"schemas": schemas}}
	}
	old := fixture("get_item")
	if err := guard.Lint(old, false, nil); err != nil {
		t.Fatal(err)
	}
	if err := guard.StubSmoke(old); err != nil {
		t.Fatal(err)
	}
	if err := guard.CheckBreaking(guard.Surface(old), guard.Surface(fixture("renamed_item"))); err == nil {
		t.Fatal("operation rename accepted")
	}
	if err := guard.Lint(fixture("get-item"), false, nil); err == nil {
		t.Fatal("invalid operation identifier accepted")
	}
	changed := fixture("get_item")
	changed["paths"].(map[string]any)["/items/{id}"].(map[string]any)["get"].(map[string]any)["parameters"] = []any{}
	if guard.Lint(changed, false, nil) == nil {
		t.Fatal("missing path parameter accepted")
	}
	changed = fixture("get_item")
	changed["components"].(map[string]any)["schemas"].(map[string]any)["Record"].(map[string]any)["required"] = []any{"id", "new_field"}
	if guard.CheckBreaking(guard.Surface(old), guard.Surface(changed)) == nil {
		t.Fatal("required field addition accepted")
	}
	changed = fixture("get_item")
	changed["components"].(map[string]any)["schemas"].(map[string]any)["Record"].(map[string]any)["properties"].(map[string]any)["id"].(map[string]any)["enum"] = []any{"a"}
	if guard.CheckBreaking(guard.Surface(old), guard.Surface(changed)) == nil {
		t.Fatal("enum narrowing accepted")
	}
}
