package experiments_test

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	d "github.com/AceNanako0721/Factorforge/src/factorforge/applications/soxl_jev/domain"
	"github.com/pelletier/go-toml/v2"
)

// This opt-in laboratory measures a tiny, predeclared workload on the provided
// account. It is not a capacity stress test or an admission authority. Private
// synthetic requests are separate from production assets; no worker is used.
// A durable start marker forbids accidentally repeating uncertain deliveries.
func TestJevAccountCapacityProbe(t *testing.T) {
	lab := os.Getenv("FACTORFORGE_CAPACITY_LAB")
	if lab == "" {
		t.Skip("opt-in private account laboratory")
	}
	var plan struct {
		Model                  string `json:"model"`
		MaxRequests            int    `json:"max_requests"`
		MaxConcurrent          int    `json:"max_concurrent"`
		MaxBytes               int    `json:"max_bytes"`
		TimeoutSeconds         int    `json:"timeout_seconds"`
		MinStartIntervalMillis int    `json:"min_start_interval_millis"`
		ShortRequest           string `json:"short_request"`
		LongRequest            string `json:"long_request"`
	}
	encoded, e := os.ReadFile(filepath.Join(lab, "plan.json"))
	if !filepath.IsAbs(lab) || filepath.Base(filepath.Dir(lab)) != "runtime" || e != nil || d.DecodePrivate(encoded, &plan) != nil ||
		plan.Model != "jev-1.13.0" || plan.MaxRequests != 12 || plan.MaxConcurrent != 3 || plan.MaxBytes < 1024 || plan.MaxBytes > 65536 ||
		plan.TimeoutSeconds <= 0 || plan.TimeoutSeconds > 20 || plan.MinStartIntervalMillis < 250 || plan.MinStartIntervalMillis > 1000 {
		t.Fatal("CAPACITY_PLAN_INVALID")
	}
	requests := map[string][]byte{}
	for _, item := range []struct{ kind, name string }{{"SHORT", plan.ShortRequest}, {"LONG", plan.LongRequest}} {
		var request struct {
			Model     string                     `json:"model"`
			State     json.RawMessage            `json:"state"`
			Questions map[string]json.RawMessage `json:"questions"`
		}
		raw, err := os.ReadFile(filepath.Join(lab, item.name))
		if filepath.Base(item.name) != item.name || err != nil || len(raw) > plan.MaxBytes || d.DecodePrivate(raw, &request) != nil ||
			request.Model != plan.Model || len(request.State) == 0 || len(request.Questions) != 7 {
			t.Fatal("CAPACITY_REQUEST_INVALID")
		}
		requests[item.kind] = raw
	}
	if len(requests["LONG"]) < 10*len(requests["SHORT"]) {
		t.Fatal("CAPACITY_INPUT_CONTRAST_REQUIRED")
	}
	var config struct {
		Credentials struct {
			Key string `toml:"jev_api_key"`
		} `toml:"credentials"`
	}
	private, err := os.ReadFile(filepath.Join(lab, "..", "..", "config", "config.toml"))
	if err != nil || toml.Unmarshal(private, &config) != nil || config.Credentials.Key == "" {
		t.Fatal("CAPACITY_CREDENTIAL_UNAVAILABLE")
	}
	write := func(name string, raw []byte) error {
		f, err := os.OpenFile(filepath.Join(lab, name), os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
		if err != nil {
			return err
		}
		_, err = f.Write(raw)
		closed := f.Close()
		if err != nil {
			return err
		}
		return closed
	}
	if write("started.json", []byte(fmt.Sprintf(`{"started_at":%q,"max_requests":12,"retries":0}`, time.Now().UTC().Format(time.RFC3339Nano)))) != nil {
		t.Fatal("CAPACITY_ALREADY_STARTED_OR_UNAVAILABLE_NO_RETRY")
	}
	transport := &http.Transport{TLSClientConfig: &tls.Config{MinVersion: tls.VersionTLS12}, MaxConnsPerHost: plan.MaxConcurrent}
	defer transport.CloseIdleConnections()
	client := &http.Client{Transport: transport, Timeout: time.Duration(plan.TimeoutSeconds) * time.Second,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	type row struct {
		ID           string            `json:"id"`
		Phase        string            `json:"phase"`
		Kind         string            `json:"input_kind"`
		RequestHash  string            `json:"request_hash"`
		RequestBytes int               `json:"request_bytes"`
		StartedAt    time.Time         `json:"started_at"`
		FinishedAt   time.Time         `json:"finished_at"`
		Milliseconds int64             `json:"milliseconds"`
		HTTP         int               `json:"http_status"`
		ResponseHash string            `json:"response_hash,omitempty"`
		Model        string            `json:"resolved_model,omitempty"`
		Usage        json.RawMessage   `json:"usage,omitempty"`
		Headers      map[string]string `json:"allowlisted_rate_headers"`
		Error        string            `json:"error,omitempty"`
	}
	rows := make([]row, plan.MaxRequests)
	var active, peak atomic.Int32
	var stopped atomic.Bool
	var schedule sync.Mutex
	var nextStart time.Time
	call := func(index int, phase, kind string) {
		raw := requests[kind]
		r := row{ID: fmt.Sprintf("request-%02d", index+1), Phase: phase, Kind: kind, RequestHash: d.ContentDigest(raw), RequestBytes: len(raw), Headers: map[string]string{}}
		schedule.Lock()
		if delay := time.Until(nextStart); delay > 0 {
			time.Sleep(delay)
		}
		nextStart = time.Now().Add(time.Duration(plan.MinStartIntervalMillis) * time.Millisecond)
		schedule.Unlock()
		if stopped.Load() {
			r.Error = "NOT_SENT_AFTER_FAILURE"
			rows[index] = r
			return
		}
		ctx, cancel := context.WithTimeout(context.Background(), time.Duration(plan.TimeoutSeconds)*time.Second)
		defer cancel()
		req, err := http.NewRequestWithContext(ctx, "POST", "https://api.typesafe.ai/v1/systemone", bytes.NewReader(raw))
		if err != nil {
			r.Error = "REQUEST_INVALID"
			stopped.Store(true)
			rows[index] = r
			return
		}
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Authorization", "Bearer "+config.Credentials.Key)
		r.StartedAt = time.Now().UTC()
		inFlight := active.Add(1)
		for previous := peak.Load(); inFlight > previous && !peak.CompareAndSwap(previous, inFlight); previous = peak.Load() {
		}
		res, err := client.Do(req)
		if err != nil {
			r.Error = "DELIVERY_UNKNOWN_NO_RETRY"
		} else {
			r.HTTP = res.StatusCode
			for _, name := range []string{"Retry-After", "X-RateLimit-Limit-Requests", "X-RateLimit-Remaining-Requests", "X-RateLimit-Reset-Requests", "X-RateLimit-Limit-Tokens", "X-RateLimit-Remaining-Tokens", "X-RateLimit-Reset-Tokens"} {
				if value := res.Header.Get(name); value != "" && len(value) <= 256 {
					r.Headers[name] = value
				}
			}
			body, readErr := io.ReadAll(io.LimitReader(res.Body, int64(plan.MaxBytes)+1))
			res.Body.Close()
			if readErr != nil || len(body) > plan.MaxBytes {
				r.Error = "RESPONSE_READ_OR_BUDGET_FAILED"
			} else if write(r.ID+"-response.json", body) != nil {
				r.Error = "RESPONSE_SAVE_FAILED"
			} else {
				r.ResponseHash = d.ContentDigest(body)
				var reply struct {
					Model   string                     `json:"model"`
					Answers map[string]json.RawMessage `json:"answers"`
					Usage   json.RawMessage            `json:"usage"`
				}
				if r.HTTP != 200 || d.DecodePrivate(body, &reply) != nil || reply.Model != plan.Model || len(reply.Answers) != 7 {
					r.Error = "PROTOCOL_OR_HTTP_REJECTED_NO_RETRY"
				} else {
					r.Model, r.Usage = reply.Model, reply.Usage
				}
			}
		}
		active.Add(-1)
		r.FinishedAt = time.Now().UTC()
		r.Milliseconds = r.FinishedAt.Sub(r.StartedAt).Milliseconds()
		if r.Error != "" {
			stopped.Store(true)
		}
		rows[index] = r
	}
	for i := 0; i < 6; i++ {
		kind := "SHORT"
		if i >= 3 {
			kind = "LONG"
		}
		call(i, "SEQUENTIAL", kind)
	}
	for wave := 0; wave < 2; wave++ {
		var group sync.WaitGroup
		for item, kind := range []string{"LONG", "LONG", "SHORT"} {
			group.Add(1)
			go func(index int, input string) { defer group.Done(); call(index, fmt.Sprintf("MIXED_%d", wave+1), input) }(6+wave*3+item, kind)
		}
		group.Wait()
	}
	report := map[string]any{"purpose": "BOUNDED_SYNTHETIC_ACCOUNT_LATENCY", "captured_at": time.Now().UTC(), "rows": rows,
		"peak_in_flight": peak.Load(), "max_requests": plan.MaxRequests, "retries": 0, "downstream_writes": 0, "orders": 0,
		"production_installed": false, "independent_blinded_holdout": false, "live_reserved_capacity_proven": false,
		"billed_cost": nil, "account_capacity_limit": nil, "plan_hash": d.ContentDigest(encoded)}
	result, err := json.MarshalIndent(report, "", "  ")
	if err != nil || write("report.json", result) != nil {
		t.Fatal("CAPACITY_REPORT_WRITE_FAILED")
	}
	t.Logf("planned_requests=%d; peak_in_flight=%d; stopped=%t; billed_cost=UNKNOWN; live_reserved_capacity_proven=false; orders=0", plan.MaxRequests, peak.Load(), stopped.Load())
	if stopped.Load() {
		t.Fatal("CAPACITY_PROBE_STOPPED_WITH_EVIDENCE_NO_RETRY")
	}
}
