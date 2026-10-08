package domain

import (
	"bytes"
	_ "embed"
	"encoding/json"
	"fmt"
	"github.com/dlclark/regexp2"
	js "github.com/santhosh-tekuri/jsonschema/v6"
	"strings"
	"sync"
	"time"
)

//go:embed projections.json
var projectionSchemas []byte

type Shape struct {
	Doc      map[string]any
	Routes   map[string]any
	Compiled map[string]*js.Schema
	Masks    map[string]*js.Schema
}
type SafeDTO struct{ raw json.RawMessage }

func (s SafeDTO) MarshalJSON() ([]byte, error) {
	if s.raw == nil {
		return []byte("null"), nil
	}
	return append([]byte(nil), s.raw...), nil
}
func (s SafeDTO) Bytes() []byte { return append([]byte(nil), s.raw...) }

// Window only removes already validated rows and replaces lower cursors with
// the console's opaque cursor. There is no constructor accepting unchecked JSON.
func (s SafeDTO) Window(offset, limit int, caseID string) (SafeDTO, error) {
	var node any
	if Decode(s.raw, &node) != nil {
		return SafeDTO{}, Fail("LOWER_RESPONSE_INVALID", 503)
	}
	var rows []any
	object, ok := node.(map[string]any)
	if ok {
		rows, _ = object["items"].([]any)
	} else {
		rows, _ = node.([]any)
	}
	if rows == nil {
		return s, nil
	}
	if caseID != "" {
		filtered := []any{}
		for _, r := range rows {
			if m, ok := r.(map[string]any); ok && m["case_id"] == caseID {
				filtered = append(filtered, r)
			}
		}
		rows = filtered
		if len(rows) != 1 {
			return SafeDTO{}, Fail("CASE_SCOPE_FORBIDDEN", 403)
		}
	}
	if offset < 0 || offset > len(rows) || limit < 1 {
		return SafeDTO{}, Fail("QUERY_CURSOR_INVALID", 409)
	}
	end := offset + limit
	if end > len(rows) {
		end = len(rows)
	}
	if ok {
		object["items"] = rows[offset:end]
		object["cursor"] = nil
		node = object
	} else {
		node = rows[offset:end]
	}
	raw, e := json.Marshal(node)
	if e != nil {
		return SafeDTO{}, Fail("LOWER_RESPONSE_INVALID", 503)
	}
	return SafeDTO{raw}, nil
}

var shapeOnce sync.Once
var shapes map[string]Shape
var shapeErr error

type offlineLoader struct{}

func (offlineLoader) Load(string) (any, error) { return nil, fmt.Errorf("external schema forbidden") }

type reg struct {
	re      *regexp2.Regexp
	pattern string
}

func (r reg) String() string            { return r.pattern }
func (r reg) MatchString(s string) bool { ok, e := r.re.MatchString(s); return ok && e == nil }
func loadShapes() {
	shapes = map[string]Shape{}
	var docs map[string]map[string]any
	if json.Unmarshal(projectionSchemas, &docs) != nil {
		shapeErr = fmt.Errorf("projection schemas")
		return
	}
	for service, doc := range docs {
		masks := map[string]any{}
		if service == "strategy" {
			for _, name := range []string{"risk_lots", "entry_snapshot", "manifest", "result"} {
				masks[name] = publicMask(doc, name)
			}
		}
		doc["console_masks"] = masks
		c := js.NewCompiler()
		c.UseLoader(offlineLoader{})
		c.AssertFormat()
		c.UseRegexpEngine(func(p string) (js.Regexp, error) {
			r, e := regexp2.Compile(p, regexp2.ECMAScript)
			if e != nil {
				return nil, e
			}
			r.MatchTimeout = time.Second
			return reg{r, p}, nil
		})
		doc["$schema"] = "https://json-schema.org/draft/2020-12/schema"
		name := "https://console.invalid/" + service + ".json"
		if e := c.AddResource(name, doc); e != nil {
			shapeErr = e
			return
		}
		s := Shape{Doc: doc, Routes: doc["routes"].(map[string]any), Compiled: map[string]*js.Schema{}, Masks: map[string]*js.Schema{}}
		for field := range masks {
			compiled, e := c.Compile(name + "#/console_masks/" + field)
			if e != nil {
				shapeErr = e
				return
			}
			s.Masks[field] = compiled
		}
		for path := range s.Routes {
			ptr := strings.ReplaceAll(strings.ReplaceAll(path, "~", "~0"), "/", "~1")
			compiled, e := c.Compile(name + "#/routes/" + ptr)
			if e != nil {
				shapeErr = e
				return
			}
			s.Compiled[path] = compiled
		}
		shapes[service] = s
	}
}

var forbidden = map[string]bool{"input_snapshot": true, "command_payload": true, "detail": true, "prompt": true, "prompt_text": true, "instructions": true, "provider_output": true, "provider_input": true, "principal_id": true, "raw_text": true, "url": true}

// Project validates the complete published lower DTO, then walks its frozen
// declared field shapes. Unknown/open dictionaries cannot become browser DTOs.
// Original text is a separate, explicitly authorized detail capability; export
// never passes allowOriginal. Amounts retain their exact JSON string values.
func Project(service, path string, raw []byte, allowOriginal bool) (SafeDTO, error) {
	return projectBound(service, path, raw, allowOriginal, "")
}

// ProjectBound filters legacy instance-wide lists before browser pagination.
// No matching recorded object scope means no readable validation record.
func ProjectBound(service, path string, raw []byte, allowOriginal bool, objectID string) (SafeDTO, error) {
	if !ID(objectID) && service == "strategy" {
		return SafeDTO{}, Fail("OBJECT_SCOPE_REQUIRED", 403)
	}
	return projectBound(service, path, raw, allowOriginal, objectID)
}
func projectBound(service, path string, raw []byte, allowOriginal bool, objectID string) (SafeDTO, error) {
	shapeOnce.Do(loadShapes)
	if shapeErr != nil {
		return SafeDTO{}, Fail("READ_SCHEMA_UNAVAILABLE", 503)
	}
	shape, ok := shapes[service]
	if !ok {
		return SafeDTO{}, Fail("READ_SCHEMA_UNAVAILABLE", 503)
	}
	schema, ok := shape.Compiled[path]
	if !ok {
		return SafeDTO{}, Fail("READ_ROUTE_FORBIDDEN", 403)
	}
	var value any
	if Decode(raw, &value) != nil || schema.Validate(value) != nil {
		return SafeDTO{}, Fail("LOWER_RESPONSE_INVALID", 503)
	}
	if service == "strategy" && objectID != "" {
		value = scopeLegacy(path, value, objectID)
	}
	safe, e := project(shape.Doc, shape.Routes[path], value, allowOriginal, 0)
	if e != nil {
		return SafeDTO{}, Fail("LOWER_RESPONSE_INVALID", 503)
	}
	out, e := json.Marshal(safe)
	if e != nil {
		return SafeDTO{}, Fail("LOWER_RESPONSE_INVALID", 503)
	}
	return SafeDTO{out}, nil
}
func project(doc map[string]any, schema, value any, original bool, depth int) (any, error) {
	if depth > 64 {
		return nil, fmt.Errorf("depth")
	}
	s, ok := schema.(map[string]any)
	if !ok {
		return nil, nil
	}
	if ref, ok := s["$ref"].(string); ok {
		if !strings.HasPrefix(ref, "#/components/schemas/") {
			return nil, fmt.Errorf("ref")
		}
		target := doc["components"].(map[string]any)["schemas"].(map[string]any)[strings.TrimPrefix(ref, "#/components/schemas/")]
		return project(doc, target, value, original, depth+1)
	}
	if options, ok := s["anyOf"].([]any); ok {
		if value == nil {
			return nil, nil
		}
		for _, o := range options {
			q := o.(map[string]any)
			if q["type"] == "null" {
				continue
			}
			if q["$ref"] != nil || q["type"] != nil {
				return project(doc, q, value, original, depth+1)
			}
		}
		return nil, fmt.Errorf("shape")
	}
	if s["format"] == "date-time" && value != nil {
		t, ok := value.(string)
		at, e := time.Parse(time.RFC3339Nano, t)
		if !ok || e != nil || at.IsZero() || !strings.HasSuffix(t, "Z") {
			return nil, fmt.Errorf("utc")
		}
	}
	switch v := value.(type) {
	case map[string]any:
		out := map[string]any{}
		props, _ := s["properties"].(map[string]any)
		if props == nil {
			return nil, nil
		}
		for k, sub := range props {
			if forbidden[k] && !(k == "raw_text" && original) {
				continue
			}
			x, exists := v[k]
			if !exists {
				continue
			}
			if mask := publicMask(doc, k); mask != nil {
				q, e := project(doc, mask, x, original, depth+1)
				if e != nil {
					return nil, e
				}
				if compiled := shapes["strategy"].Masks[k]; compiled != nil && compiled.Validate(q) != nil {
					return nil, fmt.Errorf("public shape")
				}
				out[k] = q
				continue
			}
			// These registered dictionaries contain only declared scalar/tuple
			// financial values, not free audit/input/provider objects.
			if Has([]string{"cash_balances", "source_event_versions", "values", "bounds", "steps", "evidence_gates", "pnl_components", "labels", "numbers_with_units"}, k) {
				if dict, ok := x.(map[string]any); ok {
					def, _ := sub.(map[string]any)
					child, ok := def["additionalProperties"].(map[string]any)
					if ok {
						saved := map[string]any{}
						for key, item := range dict {
							if !ID(key) {
								return nil, fmt.Errorf("dictionary key")
							}
							q, e := project(doc, child, item, original, depth+1)
							if e != nil {
								return nil, e
							}
							saved[key] = q
						}
						out[k] = saved
						continue
					}
				}
			}
			q, e := project(doc, sub, x, original, depth+1)
			if e != nil {
				return nil, e
			}
			out[k] = q
		}
		return out, nil
	case []any:
		out := make([]any, 0, len(v))
		for index, x := range v {
			child := s["items"]
			if tuple, ok := s["prefixItems"].([]any); ok && index < len(tuple) {
				child = tuple[index]
			}
			q, e := project(doc, child, x, original, depth+1)
			if e != nil {
				return nil, e
			}
			out = append(out, q)
		}
		return out, nil
	default:
		return value, nil
	}
}
func Unpack(raw []byte) (map[string]any, error) {
	var v map[string]any
	e := Decode(bytes.TrimSpace(raw), &v)
	return v, e
}
