// Package experiments contains opt-in probes, never production extraction code.
package experiments_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"

	d "github.com/AceNanako0721/Factorforge/src/factorforge/applications/soxl_jev/domain"
	"github.com/pelletier/go-toml/v2"
)

// Requests/prompts and full responses remain in the ignored private laboratory.
// The labels are read only after all calls. Self-authored synthetic labels must
// still be reported as development controls, never independent blind validation.
func TestJevSemanticMethodProbe(t *testing.T) {
	lab := os.Getenv("FACTORFORGE_SEMANTIC_LAB")
	if lab == "" {
		t.Skip("opt-in private semantic laboratory")
	}
	if !filepath.IsAbs(lab) {
		t.Fatal("LAB_PATH_INVALID")
	}
	var plan struct {
		Model          string   `json:"model"`
		MaxRequests    int      `json:"max_requests"`
		TimeoutSeconds int      `json:"timeout_seconds"`
		MaxBytes       int      `json:"max_bytes"`
		Requests       []string `json:"requests"`
	}
	load := func(name string, dst any) {
		t.Helper()
		if filepath.Base(name) != name {
			t.Fatal("LAB_FILE_INVALID")
		}
		b, err := os.ReadFile(filepath.Join(lab, name))
		if err != nil || d.DecodePrivate(b, dst) != nil {
			t.Fatal("LAB_FILE_INVALID")
		}
	}
	load("plan.json", &plan)
	if plan.Model == "" || plan.MaxRequests <= 0 || len(plan.Requests) > plan.MaxRequests || plan.TimeoutSeconds <= 0 || plan.MaxBytes <= 0 {
		t.Fatal("LAB_BUDGET_REQUIRED")
	}
	root := filepath.Clean(filepath.Join(lab, "..", ".."))
	var config struct {
		Credentials struct {
			Key string `toml:"jev_api_key"`
		} `toml:"credentials"`
	}
	private, err := os.ReadFile(filepath.Join(root, "config", "config.toml"))
	if err != nil || toml.Unmarshal(private, &config) != nil || config.Credentials.Key == "" {
		t.Fatal("LAB_CREDENTIAL_UNAVAILABLE")
	}
	client := &http.Client{Timeout: time.Duration(plan.TimeoutSeconds) * time.Second,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	// An opt-in probe is still a delivery. Do not repeat a partially completed
	// private run just because the process or response capture was interrupted.
	for i := range plan.Requests {
		if _, e := os.Lstat(filepath.Join(lab, fmt.Sprintf("case-%03d-response.json", i+1))); !os.IsNotExist(e) {
			t.Fatal("LAB_PREVIOUS_DELIVERY_EXISTS_NO_RETRY")
		}
	}
	marker, e := os.OpenFile(filepath.Join(lab, "started.json"), os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if e != nil {
		t.Fatal("LAB_ALREADY_STARTED_NO_RETRY")
	}
	_, e = marker.Write([]byte(fmt.Sprintf(`{"started_at":%q,"retries":0}`, time.Now().UTC().Format(time.RFC3339Nano))))
	closed := marker.Close()
	if e != nil || closed != nil {
		t.Fatal("LAB_START_WRITE_FAILED")
	}
	type row struct {
		ID           string          `json:"id"`
		RequestHash  string          `json:"request_hash"`
		ResponseHash string          `json:"response_hash"`
		HTTP         int             `json:"http_status"`
		Milliseconds int64           `json:"milliseconds"`
		Model        string          `json:"resolved_model"`
		Answers      json.RawMessage `json:"answers"`
		Usage        json.RawMessage `json:"usage"`
	}
	rows := []row{}
	for i, name := range plan.Requests {
		if filepath.Base(name) != name || i >= plan.MaxRequests {
			t.Fatal("LAB_REQUEST_INVALID")
		}
		raw, e := os.ReadFile(filepath.Join(lab, name))
		var request struct {
			Model     string          `json:"model"`
			State     json.RawMessage `json:"state"`
			Questions json.RawMessage `json:"questions"`
		}
		if e != nil || len(raw) > plan.MaxBytes || d.DecodePrivate(raw, &request) != nil || request.Model != plan.Model {
			t.Fatal("LAB_REQUEST_INVALID")
		}
		ctx, cancel := context.WithTimeout(context.Background(), time.Duration(plan.TimeoutSeconds)*time.Second)
		req, e := http.NewRequestWithContext(ctx, "POST", "https://api.typesafe.ai/v1/systemone", bytes.NewReader(raw))
		if e != nil {
			cancel()
			t.Fatal("LAB_REQUEST_INVALID")
		}
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Authorization", "Bearer "+config.Credentials.Key)
		started := time.Now()
		res, e := client.Do(req)
		if e != nil {
			cancel()
			t.Fatal("LAB_DELIVERY_UNKNOWN_NO_RETRY")
		}
		body, readErr := io.ReadAll(io.LimitReader(res.Body, int64(plan.MaxBytes)+1))
		res.Body.Close()
		cancel()
		if readErr != nil || len(body) > plan.MaxBytes {
			t.Fatal("LAB_RESPONSE_INVALID")
		}
		id := fmt.Sprintf("case-%03d", i+1)
		if e = os.WriteFile(filepath.Join(lab, id+"-response.json"), body, 0600); e != nil {
			t.Fatal("LAB_WRITE_FAILED")
		}
		var reply struct {
			Model   string          `json:"model"`
			Answers json.RawMessage `json:"answers"`
			Usage   json.RawMessage `json:"usage"`
		}
		if res.StatusCode != 200 || d.DecodePrivate(body, &reply) != nil || reply.Model != plan.Model {
			t.Fatal("LAB_PROTOCOL_REJECTED_NO_RETRY")
		}
		rows = append(rows, row{id, d.ContentDigest(raw), d.ContentDigest(body), res.StatusCode, time.Since(started).Milliseconds(), reply.Model, reply.Answers, reply.Usage})
	}
	// Expected labels are not inserted into model state or instructions.
	var labels any
	load("labels.json", &labels)
	report := struct {
		Purpose                    string  `json:"purpose"`
		IndependentBlindValidation bool    `json:"independent_blind_validation"`
		DownstreamWrites           int     `json:"downstream_writes"`
		Cost                       *string `json:"billed_cost"`
		Rows                       []row   `json:"rows"`
		Labels                     any     `json:"development_labels"`
	}{"BOUNDED_DEVELOPMENT_PROTOCOL_CONTROLS", false, 0, nil, rows, labels}
	encoded, e := json.MarshalIndent(report, "", "  ")
	if e != nil || os.WriteFile(filepath.Join(lab, "report.json"), encoded, 0600) != nil {
		t.Fatal("LAB_WRITE_FAILED")
	}
	t.Logf("requests=%d; model=%s; billed_cost=UNKNOWN; independent_blind_validation=false; downstream_writes=0", len(rows), plan.Model)
}
