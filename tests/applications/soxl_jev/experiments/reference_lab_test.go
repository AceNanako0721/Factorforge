package experiments_test

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"

	d "github.com/AceNanako0721/Factorforge/src/factorforge/applications/soxl_jev/domain"
)

// This laboratory is never imported by a worker. It records at most three
// public GET responses, with an honest project identity and no credentials.
func TestIndependentReferenceCapture(t *testing.T) {
	lab := os.Getenv("FACTORFORGE_REFERENCE_LAB")
	if lab == "" {
		t.Skip("opt-in independent source laboratory")
	}
	urls := []string{"https://www.nyse.com/trade/hours-calendars", "https://data.sec.gov/api/xbrl/companyconcept/CIK0001045810/us-gaap/RevenueFromContractWithCustomerExcludingAssessedTax.json", "https://data.sec.gov/submissions/CIK0001045810.json"}
	captureReferences(t, lab, urls, []string{"nyse.html", "company-concept.json", "submissions.json"})
}

func TestFedCorpusCapture(t *testing.T) {
	lab := os.Getenv("FACTORFORGE_FED_CORPUS_LAB")
	if lab == "" {
		t.Skip("opt-in official statement corpus")
	}
	captureReferences(t, lab, []string{"https://www.federalreserve.gov/monetarypolicy/openmarket.htm", "https://www.federalreserve.gov/newsevents/pressreleases/monetary20260916a.htm", "https://www.federalreserve.gov/newsevents/pressreleases/monetary20251210a.htm", "https://www.federalreserve.gov/newsevents/pressreleases/monetary20250319a.htm"}, []string{"rate-history.html", "statement-20260916.html", "statement-20251210.html", "statement-20250319.html"})
}

func captureReferences(t *testing.T, lab string, urls, names []string) {
	t.Helper()
	var plan struct {
		MaxBytes       int `json:"max_bytes"`
		TimeoutSeconds int `json:"timeout_seconds"`
		MaxRequests    int `json:"max_requests"`
	}
	b, e := os.ReadFile(filepath.Join(lab, "plan.json"))
	if !filepath.IsAbs(lab) || e != nil || d.DecodePrivate(b, &plan) != nil || plan.MaxBytes <= 0 || plan.TimeoutSeconds <= 0 || plan.TimeoutSeconds > 20 || plan.MaxRequests != len(urls) || len(urls) != len(names) {
		t.Fatal("REFERENCE_PLAN_INVALID")
	}
	client := &http.Client{Timeout: time.Duration(plan.TimeoutSeconds) * time.Second, Transport: &http.Transport{TLSClientConfig: &tls.Config{MinVersion: tls.VersionTLS12}, DisableKeepAlives: true}, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	results := []map[string]any{}
	for i, url := range urls {
		ctx, cancel := context.WithTimeout(context.Background(), time.Duration(plan.TimeoutSeconds)*time.Second)
		request, e := http.NewRequestWithContext(ctx, "GET", url, nil)
		if e != nil {
			t.Fatal("REFERENCE_REQUEST_INVALID")
		}
		request.Header.Set("User-Agent", "Factorforge/2.1.8 (+https://github.com/AceNanako0721/Factorforge)")
		result := map[string]any{"url": url, "started_at": time.Now().UTC(), "credentials_used": false, "retries": 0}
		response, e := client.Do(request)
		if e != nil {
			result["error"] = "PUBLIC_TRANSPORT_UNAVAILABLE"
			cancel()
			results = append(results, result)
			continue
		}
		raw, e := io.ReadAll(io.LimitReader(response.Body, int64(plan.MaxBytes)+1))
		response.Body.Close()
		cancel()
		result["received_at"], result["http_status"], result["media_type"] = time.Now().UTC(), response.StatusCode, response.Header.Get("Content-Type")
		if e != nil || len(raw) > plan.MaxBytes {
			result["error"] = "PUBLIC_BODY_BUDGET_OR_READ_FAILED"
			results = append(results, result)
			continue
		}
		result["bytes"], result["sha256"] = len(raw), d.ContentDigest(raw)
		name := names[i]
		f, e := os.OpenFile(filepath.Join(lab, name), os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
		if e != nil {
			t.Fatal("REFERENCE_CAPTURE_EXISTS_OR_UNAVAILABLE")
		}
		_, e = f.Write(raw)
		closed := f.Close()
		if e != nil || closed != nil {
			t.Fatal("REFERENCE_CAPTURE_WRITE_FAILED")
		}
		if response.Header.Get("Content-Type") == "application/json" {
			result["valid_json"] = json.Valid(raw)
		}
		results = append(results, result)
		t.Logf("request=%d; status=%d; bytes=%d; credentials=false; retries=0", i, response.StatusCode, len(raw))
	}
	b, _ = json.MarshalIndent(map[string]any{"requests": len(results), "results": results, "model_calls": 0, "downstream_writes": 0, "orders": 0}, "", "  ")
	if os.WriteFile(filepath.Join(lab, "report.json"), b, 0600) != nil {
		t.Fatal("REFERENCE_REPORT_WRITE_FAILED")
	}
}
