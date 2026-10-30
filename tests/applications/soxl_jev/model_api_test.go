package soxl_jev_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/AceNanako0721/Factorforge/src/factorforge/applications/soxl_jev/adapters/modelapi"
	"github.com/AceNanako0721/Factorforge/src/factorforge/applications/soxl_jev/config"
	d "github.com/AceNanako0721/Factorforge/src/factorforge/applications/soxl_jev/domain"
)

func modelClient(t *testing.T, url, state string, mutate func(*config.ModelAccess)) modelapi.Client {
	t.Helper()
	access := config.ModelAccess{
		Enabled: true, Channel: "fixture", Endpoint: url, APIKey: "fixture-key", Model: "fixture-model",
		Thinking: true, ReasoningEffort: "high", MaxTokens: 32768,
		TimeoutSeconds: 5, MaxInputBytes: 4096, MaxOutputBytes: 4096,
		BudgetWindowSeconds: 86400, MaxRequests: 0, StatePath: state,
	}
	if mutate != nil {
		mutate(&access)
	}
	return modelapi.Client{Access: access, ModelID: "fixture-model", TimeoutSeconds: 5}
}

func reply(w http.ResponseWriter, status int, body string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_, _ = w.Write([]byte(body))
}

func modelError(t *testing.T, err error, code string) {
	t.Helper()
	var failure *d.Error
	if !errors.As(err, &failure) || failure.Code != code {
		t.Fatalf("want %s, got %v", code, err)
	}
}

func TestModelAPIRequestShapeAndSuccess(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer fixture-key" || r.Header.Get("Content-Type") != "application/json" {
			t.Errorf("headers %q %q", r.Header.Get("Authorization"), r.Header.Get("Content-Type"))
		}
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Error(err)
		}
		if body["model"] != "fixture-model" || body["stream"] != false || body["max_tokens"] != float64(32768) {
			t.Errorf("body %v", body)
		}
		if body["response_format"].(map[string]any)["type"] != "json_object" {
			t.Errorf("response_format %v", body["response_format"])
		}
		if body["thinking"].(map[string]any)["type"] != "enabled" || body["reasoning_effort"] != "high" {
			t.Errorf("thinking %v effort %v", body["thinking"], body["reasoning_effort"])
		}
		if _, exists := body["tools"]; exists {
			t.Error("tools must be absent")
		}
		messages := body["messages"].([]any)
		if len(messages) != 2 || messages[0].(map[string]any)["role"] != "system" || messages[1].(map[string]any)["role"] != "user" {
			t.Errorf("messages %v", messages)
		}
		reply(w, 200, `{"choices":[{"finish_reason":"stop","message":{"content":"{\"direction\":\"FLAT\"}"}}],"usage":{"prompt_tokens":10,"completion_tokens":5,"total_tokens":15}}`)
	}))
	defer server.Close()
	client := modelClient(t, server.URL, filepath.Join(t.TempDir(), "state.json"), nil)
	result, err := client.Generate(context.Background(), "request_1", "fixture news")
	if err != nil || result.Text != `{"direction":"FLAT"}` || result.Provider != "fixture" || result.Model != "fixture-model" || !d.UTC(result.CompletedAt) {
		t.Fatalf("%+v %v", result, err)
	}
}

func TestModelAPIStatusAndTerminalFailures(t *testing.T) {
	cases := []struct {
		name, body string
		status     int
		code       string
	}{
		{"auth", `{}`, 401, "MODEL_AUTH_FAILED"},
		{"forbidden", `{}`, 403, "MODEL_AUTH_FAILED"},
		{"rate", `{}`, 429, "MODEL_RATE_LIMITED"},
		{"server", `{}`, 500, "MODEL_ACCESS_FAILED"},
		{"envelope", `not json`, 200, "PROTOCOL_INVALID"},
		{"length", `{"choices":[{"finish_reason":"length","message":{"content":"{\"a\":1}"}}]}`, 200, "INCOMPLETE_RESPONSE"},
		{"tool", `{"choices":[{"finish_reason":"tool_calls","message":{"content":""}}]}`, 200, "UNSUPPORTED_OUTPUT"},
		{"empty", `{"choices":[{"finish_reason":"stop","message":{"content":"  "}}]}`, 200, "EMPTY_RESPONSE"},
		{"reason", `{"choices":[{"finish_reason":"content_filter","message":{"content":"{}"}}]}`, 200, "DELIVERY_UNKNOWN"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				reply(w, tc.status, tc.body)
			}))
			defer server.Close()
			client := modelClient(t, server.URL, filepath.Join(t.TempDir(), "state.json"), nil)
			_, err := client.Generate(context.Background(), "request_1", "fixture news")
			modelError(t, err, tc.code)
		})
	}
}

func TestModelAPIBudgetAndDedupe(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		reply(w, 200, `{"choices":[{"finish_reason":"stop","message":{"content":"{}"}}]}`)
	}))
	defer server.Close()
	state := filepath.Join(t.TempDir(), "state.json")
	client := modelClient(t, server.URL, state, func(a *config.ModelAccess) { a.MaxRequests = 1 })
	if _, err := client.Generate(context.Background(), "request_1", "fixture news"); err != nil {
		t.Fatal(err)
	}
	if _, err := client.Generate(context.Background(), "request_2", "fixture news"); err == nil {
		t.Fatal("budget not enforced")
	} else {
		modelError(t, err, "SEMANTIC_BUDGET_EXCEEDED")
	}
	unlimited := modelClient(t, server.URL, filepath.Join(t.TempDir(), "unlimited.json"), func(a *config.ModelAccess) { a.MaxRequests = 0 })
	if _, err := unlimited.Generate(context.Background(), "request_1", "fixture news"); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ {
		if _, err := unlimited.Generate(context.Background(), "request_1", "fixture news"); err == nil {
			t.Fatal("duplicate id accepted")
		} else {
			modelError(t, err, "SEMANTIC_DUPLICATE_ID")
		}
	}
	info, err := os.Stat(state)
	if err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("state mode %v %v", info, err)
	}
}

func TestModelAPIStateBoundaries(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		reply(w, 200, `{"choices":[{"finish_reason":"stop","message":{"content":"{}"}}]}`)
	}))
	defer server.Close()
	t.Run("symlink", func(t *testing.T) {
		dir := t.TempDir()
		target := filepath.Join(dir, "target.json")
		if err := os.WriteFile(target, []byte(`{}`), 0600); err != nil {
			t.Fatal(err)
		}
		link := filepath.Join(dir, "state.json")
		if err := os.Symlink(target, link); err != nil {
			t.Fatal(err)
		}
		client := modelClient(t, server.URL, link, nil)
		_, err := client.Generate(context.Background(), "request_1", "fixture news")
		modelError(t, err, "MODEL_STATE_INVALID")
	})
	t.Run("clock rollback", func(t *testing.T) {
		state := filepath.Join(t.TempDir(), "state.json")
		client := modelClient(t, server.URL, state, nil)
		client.Now = func() time.Time { return time.Date(2026, 10, 30, 12, 0, 0, 0, time.UTC) }
		if _, err := client.Generate(context.Background(), "request_1", "fixture news"); err != nil {
			t.Fatal(err)
		}
		client.Now = func() time.Time { return time.Date(2026, 10, 30, 11, 0, 0, 0, time.UTC) }
		_, err := client.Generate(context.Background(), "request_2", "fixture news")
		modelError(t, err, "MODEL_STATE_INVALID")
	})
	t.Run("input limit", func(t *testing.T) {
		client := modelClient(t, server.URL, filepath.Join(t.TempDir(), "state.json"), func(a *config.ModelAccess) { a.MaxInputBytes = 8 })
		_, err := client.Generate(context.Background(), "request_1", "this is too long")
		modelError(t, err, "SEMANTIC_INPUT_INVALID")
	})
}

func TestModelAccessConfigValidation(t *testing.T) {
	valid := `[model_access]
enabled = true
channel = "deepseek"
endpoint = "https://api.deepseek.com/chat/completions"
api_key = "fixture"
model = "deepseek-flash"
thinking = true
reasoning_effort = "high"
max_tokens = 32768
timeout_seconds = 120
max_input_bytes = 65536
max_output_bytes = 65536
budget_window_seconds = 86400
max_requests = 500
state_path = "/private/runtime/model-state.json"
`
	cases := map[string]string{
		"valid":           valid,
		"http endpoint":   replaceOnce(valid, "https://", "http://"),
		"bad effort":      replaceOnce(valid, `"high"`, `"medium"`),
		"cap no window":   replaceOnce(valid, "budget_window_seconds = 86400", "budget_window_seconds = 0"),
		"missing key":     replaceOnce(valid, `api_key = "fixture"`, `api_key = ""`),
		"huge max tokens": replaceOnce(valid, "max_tokens = 32768", "max_tokens = 400000"),
		"extra section":   valid + "\n[deployment]\ndata_root = \"/private/data\"\n",
	}
	for name, text := range cases {
		t.Run(name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "model.toml")
			if err := os.WriteFile(path, []byte(text), 0600); err != nil {
				t.Fatal(err)
			}
			_, err := config.LoadModelAccess(path)
			if (name == "valid") != (err == nil) {
				t.Fatalf("%s: %v", name, err)
			}
		})
	}
}

func replaceOnce(text, old, new string) string {
	return strings.Replace(text, old, new, 1)
}
