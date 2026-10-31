package soxl_test

import (
	"bytes"
	"encoding/json"
	"errors"
	d "github.com/AceNanako0721/Factorforge/src/factorforge/applications/soxl/domain"
	dto "github.com/AceNanako0721/Factorforge/src/factorforge/strategy/api/dto"
	sd "github.com/AceNanako0721/Factorforge/src/factorforge/strategy/domain"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func ptr[T any](v T) *T { return &v }

type fixedClock struct{ at time.Time }

func (c *fixedClock) Now() time.Time { return c.at }

// providerError returns the stable code of an application error.
func providerError(err error) string {
	if err == nil {
		return "OK"
	}
	var known *d.Error
	if errors.As(err, &known) {
		return known.Code
	}
	return "UNEXPECTED"
}

func assertCode(t *testing.T, err error, code string) {
	t.Helper()
	var e *d.Error
	if !errors.As(err, &e) || e.Code != code {
		t.Fatalf("wanted %s; got %v", code, err)
	}
}

func nativePG(t *testing.T) string {
	t.Helper()
	if p := os.Getenv("FACTORFORGE_TEST_PG_BIN"); p != "" {
		return p
	}
	for _, pattern := range []string{"../../../runtime/native-pg/pgserver/pginstall/bin", "/usr/lib/postgresql/*/bin"} {
		paths, _ := filepath.Glob(pattern)
		if len(paths) > 0 {
			p, err := filepath.Abs(paths[0])
			if err != nil {
				t.Fatal(err)
			}
			return p
		}
	}
	t.Fatal("native PostgreSQL required")
	return ""
}

// Reuse the frozen P2 replay provenance rather than inventing a production
// registry or a second policy fixture with different business rules.
func p2Fixture(t *testing.T) (*sd.StrategyState, sd.WorkloadIdentity, dto.CreateObject) {
	t.Helper()
	raw, err := os.ReadFile("../../strategy/fixtures/go_replay.json")
	if err != nil {
		t.Fatal(err)
	}
	var root map[string]any
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	if decoder.Decode(&root) != nil || root["fixture_only"] != true || root["source_commit"] != "e97c9e9a380d105a7e96933bdd325e1d9af06d93" {
		t.Fatal("fixture provenance")
	}
	nodes := root["nodes"].(map[string]any)
	var expand func(any) any
	expand = func(v any) any {
		switch x := v.(type) {
		case map[string]any:
			if ref, ok := x["$fixture_ref"].(string); ok {
				return expand(nodes[ref])
			}
			out := map[string]any{}
			for k, y := range x {
				out[k] = expand(y)
			}
			return out
		case []any:
			out := []any{}
			for _, y := range x {
				out = append(out, expand(y))
			}
			return out
		default:
			return v
		}
	}
	decode := func(v any, target any) {
		data, _ := json.Marshal(v)
		if err := sd.DecodeJSON(data, target); err != nil {
			t.Fatal(err)
		}
	}
	for _, entry := range root["cases"].([]any) {
		item := expand(entry).(map[string]any)
		if item["kind"] != "create" {
			continue
		}
		var state sd.StrategyState
		decode(expand(root["states"].(map[string]any)[item["before"].(string)]), &state)
		input := item["input"].(map[string]any)
		var identity sd.WorkloadIdentity
		decode(input["identity"], &identity)
		var request dto.CreateObject
		decode(input["command"], &request.Command)
		decode(input["obj"], &request.Object)
		decode(input["policy"], &request.Policy)
		decode(input["params"], &request.Parameters)
		return &state, identity, request
	}
	t.Fatal("missing create fixture")
	return nil, sd.WorkloadIdentity{}, dto.CreateObject{}
}
