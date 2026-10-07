// Package contractguard validates local Draft 2020-12 schemas and OpenAPI
// surfaces without network loaders or Python. Errors identify rules, not values.
package contractguard

import (
	"bytes"
	"encoding/json"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"go/types"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

func object(v any) map[string]any {
	if m, ok := v.(map[string]any); ok {
		return m
	}
	return map[string]any{}
}
func text(v any) string { s, _ := v.(string); return s }
func list(v any) []any {
	if a, ok := v.([]any); ok {
		return a
	}
	return []any{}
}
func read(path string) (map[string]any, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var out map[string]any
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	err = decoder.Decode(&out)
	if err == nil {
		var extra any
		if out == nil || decoder.Decode(&extra) != io.EOF {
			err = fmt.Errorf("single JSON object required")
		}
	}
	return out, err
}
func equal(a, b any) bool { x, _ := json.Marshal(a); y, _ := json.Marshal(b); return bytes.Equal(x, y) }
func subset(a, b []any) bool {
	for _, x := range a {
		exists := false
		for _, y := range b {
			exists = exists || equal(x, y)
		}
		if !exists {
			return false
		}
	}
	return true
}
func resolvePointer(value any, fragment string) (any, error) {
	for _, part := range strings.Split(strings.TrimPrefix(fragment, "/"), "/") {
		part = strings.NewReplacer("~1", "/", "~0", "~").Replace(part)
		m := object(value)
		v, exists := m[part]
		if !exists {
			return nil, fmt.Errorf("missing local schema reference")
		}
		value = v
	}
	return value, nil
}
func Bundle(value any, base, root string, ancestors map[string]bool) (any, error) {
	switch v := value.(type) {
	case []any:
		out := []any{}
		for _, child := range v {
			x, e := Bundle(child, base, root, ancestors)
			if e != nil {
				return nil, e
			}
			out = append(out, x)
		}
		return out, nil
	case map[string]any:
		if ref := text(v["$ref"]); ref != "" && !strings.HasPrefix(ref, "#") {
			r, fileFragment, _ := strings.Cut(ref, "#")
			if strings.Contains(r, ":") {
				return nil, fmt.Errorf("remote schema reference is forbidden")
			}
			target := filepath.Clean(filepath.Join(filepath.Dir(base), filepath.FromSlash(r)))
			target, err := filepath.EvalSymlinks(target)
			if err != nil {
				return nil, fmt.Errorf("missing local schema file")
			}
			resolvedRoot, err := filepath.EvalSymlinks(root)
			if err != nil {
				return nil, err
			}
			relative, err := filepath.Rel(resolvedRoot, target)
			if err != nil || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) || ancestors[target] {
				return nil, fmt.Errorf("unsafe or cyclic file schema reference")
			}
			body, err := read(target)
			if err != nil {
				return nil, err
			}
			var selected any = body
			if fileFragment != "" {
				selected, err = resolvePointer(body, fileFragment)
				if err != nil {
					return nil, err
				}
			}
			next := map[string]bool{}
			for k, x := range ancestors {
				next[k] = x
			}
			next[target] = true
			return Bundle(selected, target, root, next)
		}
		out := map[string]any{}
		for k, child := range v {
			if k == "$id" {
				continue
			}
			x, e := Bundle(child, base, root, ancestors)
			if e != nil {
				return nil, e
			}
			out[k] = x
		}
		return out, nil
	default:
		return value, nil
	}
}
func Validate(schema, instance any) error {
	compiler := NewCompiler()
	if err := compiler.AddResource("urn:factorforge:local", schema); err != nil {
		return fmt.Errorf("invalid local schema")
	}
	compiled, err := compiler.Compile("urn:factorforge:local")
	if err != nil {
		return fmt.Errorf("invalid JSON Schema")
	}
	if compiled.Validate(instance) != nil {
		return fmt.Errorf("schema validation rejected instance")
	}
	return nil
}

type operation struct {
	Path, Method string
	Value        map[string]any
}

func operations(spec map[string]any) []operation {
	out := []operation{}
	for path, raw := range object(spec["paths"]) {
		for method, v := range object(raw) {
			if strings.Contains("|get|post|patch|put|delete|", "|"+method+"|") {
				out = append(out, operation{path, method, object(v)})
			}
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Path+out[i].Method < out[j].Path+out[j].Method })
	return out
}
func checkRefs(node any, document map[string]any) error {
	switch v := node.(type) {
	case []any:
		for _, x := range v {
			if err := checkRefs(x, document); err != nil {
				return err
			}
		}
	case map[string]any:
		if ref := text(v["$ref"]); ref != "" {
			if !strings.HasPrefix(ref, "#/") {
				return fmt.Errorf("non-local OpenAPI reference")
			}
			if _, err := resolvePointer(document, strings.TrimPrefix(ref, "#")); err != nil {
				return err
			}
		}
		for _, x := range v {
			if err := checkRefs(x, document); err != nil {
				return err
			}
		}
	}
	return nil
}
func Lint(spec map[string]any, legacy bool, safe []any) error {
	if !strings.HasPrefix(text(spec["openapi"]), "3.1.") || text(object(spec["info"])["title"]) == "" || text(object(spec["info"])["version"]) == "" {
		return fmt.Errorf("OpenAPI 3.1 envelope required")
	}
	if err := checkRefs(spec, spec); err != nil {
		return err
	}
	ids := map[string]bool{}
	for _, op := range operations(spec) {
		id := text(op.Value["operationId"])
		if !regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`).MatchString(id) || ids[id] {
			return fmt.Errorf("invalid or duplicate operationId")
		}
		ids[id] = true
		if len(object(op.Value["responses"])) == 0 {
			return fmt.Errorf("operation responses missing")
		}
		params := map[string]bool{}
		for _, p := range list(op.Value["parameters"]) {
			v := object(p)
			if text(v["in"]) == "path" {
				name := text(v["name"])
				if v["required"] != true || !strings.Contains(op.Path, "{"+name+"}") {
					return fmt.Errorf("invalid path parameter")
				}
				params[name] = true
			}
		}
		if !legacy {
			for _, m := range regexp.MustCompile(`\{([^}]+)\}`).FindAllStringSubmatch(op.Path, -1) {
				if !params[m[1]] {
					return fmt.Errorf("path parameter declaration missing")
				}
			}
			continue
		}
		if _, ok := object(op.Value["responses"])["default"]; !ok {
			return fmt.Errorf("legacy default response missing")
		}
		if !strings.HasPrefix(op.Path, "/health/") && len(list(op.Value["x-required-scopes"])) == 0 && op.Value["x-authorization-rule"] == nil {
			return fmt.Errorf("legacy authorization metadata missing")
		}
		if !subset(list(op.Value["x-required-scopes"]), safe) {
			return fmt.Errorf("legacy scope outside public enum")
		}
		if op.Method != "get" {
			refs := []any{}
			for _, p := range list(op.Value["parameters"]) {
				refs = append(refs, last(text(object(p)["$ref"])))
			}
			if !subset([]any{"IdempotencyKey", "CorrelationId"}, refs) || op.Value["requestBody"] == nil || op.Value["x-audit"] == nil {
				return fmt.Errorf("legacy command idempotency/audit contract missing")
			}
		}
	}
	return nil
}
func last(ref string) string { parts := strings.Split(ref, "/"); return parts[len(parts)-1] }
func Surface(spec map[string]any) map[string]any {
	ops, schemas := map[string]any{}, map[string]any{}
	for _, op := range operations(spec) {
		body := object(object(object(op.Value["requestBody"])["content"])["application/json"])["schema"]
		success := []any{}
		for status := range object(op.Value["responses"]) {
			if strings.HasPrefix(status, "2") {
				success = append(success, status)
			}
		}
		sort.Slice(success, func(i, j int) bool { return text(success[i]) < text(success[j]) })
		ops[strings.ToUpper(op.Method)+" "+op.Path] = map[string]any{"operationId": op.Value["operationId"], "scopes": list(op.Value["x-required-scopes"]), "success": success, "body_schema": last(text(object(body)["$ref"]))}
	}
	for name, raw := range object(object(spec["components"])["schemas"]) {
		s := object(raw)
		props := map[string]any{}
		for field, v := range object(s["properties"]) {
			x := object(v)
			props[field] = map[string]any{"type": x["type"], "enum": x["enum"], "items_enum": object(x["items"])["enum"], "const": x["const"]}
		}
		schemas[name] = map[string]any{"required": list(s["required"]), "properties": props}
	}
	return map[string]any{"operations": ops, "schemas": schemas}
}
func CheckBreaking(old, new map[string]any) error {
	for key, raw := range object(old["operations"]) {
		current, ok := object(new["operations"])[key]
		if !ok {
			return fmt.Errorf("removed operation")
		}
		before, after := object(raw), object(current)
		if !equal(before["operationId"], after["operationId"]) || !equal(before["body_schema"], after["body_schema"]) || !subset(list(before["success"]), list(after["success"])) || !subset(list(after["scopes"]), list(before["scopes"])) {
			return fmt.Errorf("breaking operation change")
		}
	}
	for name, raw := range object(old["schemas"]) {
		current, ok := object(new["schemas"])[name]
		if !ok {
			return fmt.Errorf("removed schema")
		}
		before, after := object(raw), object(current)
		if !subset(list(after["required"]), list(before["required"])) {
			return fmt.Errorf("new required field")
		}
		for field, raw := range object(before["properties"]) {
			current, ok := object(after["properties"])[field]
			if !ok {
				return fmt.Errorf("removed schema field")
			}
			a, b := object(raw), object(current)
			if !equal(a["type"], b["type"]) || a["enum"] != nil && !subset(list(a["enum"]), list(b["enum"])) || a["items_enum"] != nil && !subset(list(a["items_enum"]), list(b["items_enum"])) || a["const"] != nil && !equal(a["const"], b["const"]) {
				return fmt.Errorf("breaking schema field change")
			}
		}
	}
	return nil
}
func StubSmoke(spec map[string]any) error {
	var source strings.Builder
	source.WriteString("package generated\n")
	for _, op := range operations(spec) {
		fmt.Fprintf(&source, "func %s(request any)(any,error){panic(\"UNIMPLEMENTED\")}\n", text(op.Value["operationId"]))
	}
	set := token.NewFileSet()
	file, err := parser.ParseFile(set, "generated.go", source.String(), 0)
	if err != nil {
		return fmt.Errorf("generated stubs invalid")
	}
	_, err = (&types.Config{}).Check("generated", set, []*ast.File{file}, nil)
	if err != nil {
		return fmt.Errorf("generated stubs failed type check")
	}
	return nil
}
func Check(root string) (int, int, error) {
	abs, err := filepath.Abs(filepath.Join(root, "contracts"))
	if err != nil {
		return 0, 0, err
	}
	raw, err := read(filepath.Join(abs, "openapi/v1.json"))
	if err != nil {
		return 0, 0, err
	}
	bundled, err := Bundle(raw, filepath.Join(abs, "openapi/v1.json"), abs, map[string]bool{})
	if err != nil {
		return 0, 0, err
	}
	spec := object(bundled)
	meta, err := read(filepath.Join(abs, "meta/openapi-3.1-2025-09-15.json"))
	if err != nil {
		return 0, 0, err
	}
	if err = Validate(meta, spec); err != nil {
		return 0, 0, fmt.Errorf("legacy OpenAPI structural validation failed")
	}
	scopes, err := read(filepath.Join(abs, "schemas/api-scope.json"))
	if err != nil {
		return 0, 0, err
	}
	capabilities, err := read(filepath.Join(abs, "schemas/workload-capability.json"))
	if err != nil {
		return 0, 0, err
	}
	if !equal(capabilities["enum"], []any{"signal:sim", "signal:live"}) {
		return 0, 0, fmt.Errorf("workload capability enum changed")
	}
	for _, cap := range list(capabilities["enum"]) {
		if subset([]any{cap}, list(scopes["enum"])) {
			return 0, 0, fmt.Errorf("public/workload enums overlap")
		}
	}
	for _, filename := range []string{"api-key-create.json", "permission-update.json", "api-key.json"} {
		value, err := read(filepath.Join(abs, "schemas", filename))
		if err != nil {
			return 0, 0, err
		}
		if !equal(object(object(value["properties"])["scopes"])["items"], map[string]any{"$ref": "./api-scope.json"}) {
			return 0, 0, fmt.Errorf("public key scope schema not shared")
		}
	}
	if err = Lint(spec, true, list(scopes["enum"])); err != nil {
		return 0, 0, err
	}
	for path, v := range object(spec["paths"]) {
		if strings.HasPrefix(path, "/signals") || strings.HasPrefix(path, "/signal-eligibility") {
			return 0, 0, fmt.Errorf("public signal endpoint forbidden")
		}
		_ = v
	}
	for path, contract := range map[string][2]string{"/analysis/jobs": {"analysis:request", "AnalysisJobRequest"}, "/analysis-candidates": {"analysis:submit-research", "ResearchCandidateSubmission"}} {
		op := object(object(object(spec["paths"])[path])["post"])
		body := object(object(object(op["requestBody"])["content"])["application/json"])
		if !equal(op["x-required-scopes"], []any{contract[0]}) || !strings.HasPrefix(text(op["x-signal-effect"]), "RESEARCH_ONLY") || last(text(object(body["schema"])["$ref"])) != contract[1] {
			return 0, 0, fmt.Errorf("research authorization changed")
		}
	}
	for _, name := range []string{"ApiKeyCreate", "PermissionUpdate", "ApiKey"} {
		value := object(object(object(spec["components"])["schemas"])[name])
		items := object(object(object(value["properties"])["scopes"])["items"])
		if !equal(items["enum"], scopes["enum"]) {
			return 0, 0, fmt.Errorf("public key capability mismatch")
		}
	}
	baseline, err := read(filepath.Join(abs, "baseline/v1.1-surface.json"))
	if err != nil {
		return 0, 0, err
	}
	if err = CheckBreaking(baseline, Surface(spec)); err != nil {
		return 0, 0, err
	}
	if err = StubSmoke(spec); err != nil {
		return 0, 0, err
	}
	count := 0
	for folder, valid := range map[string]bool{"valid": true, "invalid": false} {
		paths, _ := filepath.Glob(filepath.Join(abs, "fixtures", folder, "*.json"))
		for _, path := range paths {
			fixture, err := read(path)
			if err != nil {
				return 0, 0, err
			}
			schemaPath := filepath.Join(abs, "schemas", text(fixture["_schema"]))
			schema, err := read(schemaPath)
			if err != nil {
				return 0, 0, err
			}
			value, err := Bundle(schema, schemaPath, abs, map[string]bool{})
			if err != nil {
				return 0, 0, err
			}
			err = Validate(value, fixture["data"])
			if valid != (err == nil) {
				return 0, 0, fmt.Errorf("fixture classification failed: %s", filepath.Base(path))
			}
			count++
		}
	}
	if count < 4 {
		return 0, 0, fmt.Errorf("contract fixtures missing")
	}
	for _, name := range []string{"v2/trading/openapi.json", "v2/strategy/openapi.json", "v2/strategy/workload.openapi.json"} {
		value, err := read(filepath.Join(abs, name))
		if err != nil {
			return 0, 0, err
		}
		if err = Validate(meta, value); err != nil {
			return 0, 0, fmt.Errorf("v2 OpenAPI structural validation failed: %s", name)
		}
		if err = Lint(value, false, nil); err != nil {
			return 0, 0, fmt.Errorf("%s: %w", name, err)
		}
		if err = StubSmoke(value); err != nil {
			return 0, 0, err
		}
		compiler := NewCompiler()
		if err = compiler.AddResource("urn:factorforge:contract", value); err != nil {
			return 0, 0, err
		}
		for schemaName := range object(object(value["components"])["schemas"]) {
			if _, err = compiler.Compile("urn:factorforge:contract#/components/schemas/" + schemaName); err != nil {
				return 0, 0, fmt.Errorf("invalid v2 schema: %s: %w", schemaName, err)
			}
		}
	}
	return len(operations(spec)), count, nil
}
