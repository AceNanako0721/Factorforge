package experiments_test

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	d "github.com/AceNanako0721/Factorforge/src/factorforge/applications/soxl_jev/domain"
	"github.com/AceNanako0721/Factorforge/src/factorforge/applications/soxl_jev/operations"
	"github.com/AceNanako0721/Factorforge/src/factorforge/trading/adapters/binance"
)

// Public product metadata only. The laboratory has no configuration/key reader,
// account endpoints, signed requests, acceptance POST or order operations.
func TestSOXLPublicProductLab(t *testing.T) {
	lab := os.Getenv("FACTORFORGE_PRODUCT_LAB")
	if lab == "" {
		t.Skip("opt-in public SOXL metadata laboratory")
	}
	if !filepath.IsAbs(lab) {
		t.Fatal("PRODUCT_LAB_PATH_INVALID")
	}
	var plan struct {
		MaxBytes       int `json:"max_bytes"`
		TimeoutSeconds int `json:"timeout_seconds"`
		MaxRequests    int `json:"max_requests"`
	}
	raw, err := os.ReadFile(filepath.Join(lab, "plan.json"))
	if err != nil || d.DecodePrivate(raw, &plan) != nil || plan.MaxBytes <= 0 || plan.TimeoutSeconds <= 0 || plan.TimeoutSeconds > 60 || plan.MaxRequests != 8 {
		t.Fatal("PRODUCT_LAB_PLAN_INVALID")
	}
	client := &http.Client{Timeout: time.Duration(plan.TimeoutSeconds) * time.Second, Transport: &http.Transport{TLSClientConfig: &tls.Config{MinVersion: tls.VersionTLS12}, DisableKeepAlives: true}, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	paths := []string{"/fapi/v1/exchangeInfo", "/fapi/v1/premiumIndex?symbol=SOXLUSDT", "/fapi/v1/ticker/bookTicker?symbol=SOXLUSDT", "/fapi/v1/tradingSchedule"}
	results := []map[string]any{}
	for _, host := range []string{"demo-fapi.binance.com", "fapi.binance.com"} {
		for i, path := range paths {
			ctx, cancel := context.WithTimeout(context.Background(), time.Duration(plan.TimeoutSeconds)*time.Second)
			request, err := http.NewRequestWithContext(ctx, "GET", "https://"+host+path, nil)
			if err != nil {
				t.Fatal("PRODUCT_LAB_REQUEST_INVALID")
			}
			at := time.Now().UTC()
			response, e := client.Do(request)
			result := map[string]any{"host": host, "path": path, "received_at": at, "credentials_used": false, "account_capability": "NOT_TESTED", "orders": 0}
			if e != nil {
				result["error"] = "PUBLIC_TRANSPORT_UNAVAILABLE"
				cancel()
				results = append(results, result)
				continue
			}
			body, e := io.ReadAll(io.LimitReader(response.Body, int64(plan.MaxBytes)+1))
			response.Body.Close()
			cancel()
			result["http_status"] = response.StatusCode
			if e != nil || len(body) > plan.MaxBytes {
				result["error"] = "PUBLIC_BODY_BUDGET_OR_READ_FAILED"
				results = append(results, result)
				continue
			}
			result["bytes"], result["sha256"] = len(body), d.ContentDigest(body)
			name := fmt.Sprintf("%s-%d.json", host, i)
			if os.WriteFile(filepath.Join(lab, name), body, 0600) != nil {
				t.Fatal("PRODUCT_LAB_WRITE_FAILED")
			}
			var decoded map[string]json.RawMessage
			if json.Unmarshal(body, &decoded) == nil {
				if i == 0 && response.StatusCode == 200 {
					var symbols []map[string]json.RawMessage
					if json.Unmarshal(decoded["symbols"], &symbols) != nil {
						t.Fatal("PRODUCT_LAB_SYMBOLS_INVALID")
					}
					result["symbol_count"] = len(symbols)
					result["soxl_found"] = false
					for _, symbol := range symbols {
						var name string
						json.Unmarshal(symbol["symbol"], &name)
						if name == "SOXLUSDT" {
							result["soxl_found"] = true
							result["soxl"] = symbol
						}
					}
				} else if i == 3 && response.StatusCode == 200 {
					result["schedule"] = decoded
				} else {
					result["data"] = decoded
				}
			}
			results = append(results, result)
			t.Logf("host=%s; path_index=%d; status=%d; bytes=%d; credentials=false; orders=0", host, i, response.StatusCode, len(body))
		}
	}
	encoded, err := json.MarshalIndent(map[string]any{"requests": len(results), "results": results, "credentials_used": false, "account_capability": "NOT_TESTED", "orders": 0}, "", "  ")
	if err != nil || os.WriteFile(filepath.Join(lab, "report.json"), encoded, 0600) != nil {
		t.Fatal("PRODUCT_LAB_REPORT_FAILED")
	}
}

func TestArchivedSOXLAdapterLab(t *testing.T) {
	lab := os.Getenv("FACTORFORGE_PRODUCT_LAB")
	if lab == "" {
		t.Skip("opt-in archived SOXL adapter laboratory")
	}
	body, err := os.ReadFile(filepath.Join(lab, "demo-fapi.binance.com-0.json"))
	if err != nil {
		t.Fatal("PRODUCT_LAB_ARCHIVE_UNAVAILABLE")
	}
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if r.Method != "GET" || r.URL.Path != "/fapi/v1/exchangeInfo" {
			t.Error("unexpected adapter operation")
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write(body)
	}))
	defer server.Close()
	// 1 is a development-only assumption to expose the contractType gate. This
	// does not validate a production contract multiplier or install P1 assets.
	market := &binance.Market{Client: server.Client(), Endpoint: server.URL, Multipliers: map[string]string{"SOXLUSDT": "1"}}
	specs, err := market.InstrumentSpecs(context.Background())
	if err != nil || calls != 1 {
		t.Fatal("PRODUCT_LAB_ADAPTER_FAILED", err)
	}
	report := map[string]any{"archived_hash": d.ContentDigest(body), "matched_specs": len(specs), "multiplier": "DEVELOPMENT_ASSUMPTION_ONLY", "network_calls": 0, "account_calls": 0, "orders": 0}
	phase := os.Getenv("FACTORFORGE_ADAPTER_LAB_PHASE")
	if !d.Has([]string{"before-design", "after-implementation"}, phase) {
		t.Fatal("PRODUCT_LAB_PHASE_INVALID")
	}
	if phase == "after-implementation" && len(specs) != 1 {
		t.Fatal("PRODUCT_LAB_TARGET_RULE_MISSING")
	}
	encoded, _ := json.MarshalIndent(report, "", "  ")
	f, err := os.OpenFile(filepath.Join(lab, "adapter-"+phase+".json"), os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		t.Fatal("PRODUCT_LAB_REPORT_EXISTS_OR_UNAVAILABLE")
	}
	_, err = f.Write(encoded)
	closeErr := f.Close()
	if err != nil || closeErr != nil {
		t.Fatal("PRODUCT_LAB_REPORT_WRITE_FAILED")
	}
	t.Logf("archived_SOXLUSDT_matches=%d; development_multiplier_only=true; public_network_calls=0; orders=0", len(specs))
}

// Post-design verification uses the production compiler on immutable captures;
// budgets and fixture binding here are laboratory inputs, never installed assets.
func TestArchivedProductionCalendar(t *testing.T) {
	lab := os.Getenv("FACTORFORGE_PRODUCT_LAB")
	if lab == "" {
		t.Skip("opt-in production calendar archive replay")
	}
	wd, _ := os.Getwd()
	root := filepath.Clean(filepath.Join(wd, "..", "..", "..", ".."))
	var report struct {
		Results []struct {
			Host       string    `json:"host"`
			Path       string    `json:"path"`
			ReceivedAt time.Time `json:"received_at"`
		} `json:"results"`
	}
	raw, err := os.ReadFile(filepath.Join(lab, "report.json"))
	if err != nil || json.Unmarshal(raw, &report) != nil {
		t.Fatal("PRODUCT_LAB_REPORT_INVALID")
	}
	for environment, host := range map[string]string{"DEMO": "demo-fapi.binance.com", "PUBLIC_MAIN": "fapi.binance.com"} {
		snap := func(index int, path string) operations.VenueSnapshot {
			t.Helper()
			body, e := os.ReadFile(filepath.Join(lab, fmt.Sprintf("%s-%d.json", host, index)))
			if e != nil {
				t.Fatal(e)
			}
			var at time.Time
			for _, r := range report.Results {
				if r.Host == host && r.Path == path {
					at = r.ReceivedAt
				}
			}
			return operations.VenueSnapshot{URL: "https://" + host + path, Content: string(body), ContentHash: d.ContentDigest(body), ReceivedAt: at}
		}
		r := operations.VenueCalendarRequest{SchemaVersion: 1, Binding: d.Binding{InstanceID: "lab-soxl-calendar", Environment: "SIM"}, Version: "lab-calendar-snapshot-20261009", ProviderEnvironment: environment, ProductSnapshot: snap(0, "/fapi/v1/exchangeInfo"), ScheduleSnapshot: snap(3, "/fapi/v1/tradingSchedule"), Limits: operations.VenueCalendarLimits{MaxInputBytes: 2000000, MaxSessions: 1000, MaxUpdateAgeSeconds: 86400}}
		input, output := filepath.Join(lab, environment+"-calendar-request.json"), filepath.Join(lab, environment+"-calendar-artifact.json")
		body, _ := json.Marshal(r)
		if os.WriteFile(input, body, 0600) != nil {
			t.Fatal("PRODUCT_LAB_WRITE_FAILED")
		}
		now := time.Now().UTC()
		if e := operations.CompileVenueCalendarFile(root, input, output, 4000000, now); e != nil {
			t.Fatal(e)
		}
		a, e := operations.LoadVenueCalendar(root, output, r.Binding, now, 4000000)
		if e != nil {
			t.Fatal(e)
		}
		windows, e := a.Calendar.Windows(context.Background())
		if e != nil || len(windows) != 19 {
			t.Fatal("PRODUCT_LAB_WINDOW_COUNT", e)
		}
		weekend, e := a.Calendar.Window(context.Background(), time.Date(2026, 10, 10, 12, 0, 0, 0, time.UTC))
		if e != nil || weekend.End.Sub(weekend.Start) != 65*time.Hour+30*time.Minute {
			t.Fatal("PRODUCT_LAB_WEEKEND_CHANGED", e)
		}
		result, _ := json.MarshalIndent(map[string]any{"environment": environment, "artifact_id": a.ArtifactID, "regular_sessions": len(a.Calendar.Sessions), "windows": len(windows), "calendar_version": a.Calendar.Version, "valid_from": a.Calendar.ValidFrom, "valid_until": a.Calendar.ValidUntil, "weekend_hours": weekend.End.Sub(weekend.Start).Hours(), "production_installed": false, "network_calls": 0, "downstream_writes": 0, "orders": 0}, "", "  ")
		if os.WriteFile(filepath.Join(lab, environment+"-production-calendar-report.json"), result, 0600) != nil {
			t.Fatal("PRODUCT_LAB_WRITE_FAILED")
		}
		t.Logf("environment=%s; windows=%d; weekend_hours=65.5; downstream_writes=0; orders=0", environment, len(windows))
	}
}

func TestArchivedEquityCalendarLab(t *testing.T) {
	lab := os.Getenv("FACTORFORGE_PRODUCT_LAB")
	if lab == "" {
		t.Skip("opt-in archived calendar laboratory")
	}
	body, err := os.ReadFile(filepath.Join(lab, "demo-fapi.binance.com-3.json"))
	if err != nil {
		t.Fatal("PRODUCT_LAB_ARCHIVE_UNAVAILABLE")
	}
	var wire struct {
		UpdateTime int64 `json:"updateTime"`
		Markets    map[string]struct {
			Sessions []struct {
				Start int64  `json:"startTime"`
				End   int64  `json:"endTime"`
				Type  string `json:"type"`
			} `json:"sessions"`
		} `json:"marketSchedules"`
	}
	if json.Unmarshal(body, &wire) != nil {
		t.Fatal("PRODUCT_LAB_CALENDAR_INVALID")
	}
	zone, err := time.LoadLocation("America/New_York")
	if err != nil {
		t.Fatal(err)
	}
	calendar := operations.Calendar{Version: "lab-binance-equity-calendar", Zone: "America/New_York", Sessions: []operations.MarketSession{}}
	previous := int64(0)
	regular := 0
	for _, session := range wire.Markets["EQUITY"].Sessions {
		if session.Start <= 0 || session.End <= session.Start || previous != 0 && session.Start != previous || !d.Has([]string{"PRE_MARKET", "REGULAR", "AFTER_MARKET", "OVERNIGHT", "NO_TRADING"}, session.Type) {
			t.Fatal("PRODUCT_LAB_CALENDAR_COVERAGE_INVALID")
		}
		previous = session.End
		if session.Type != "REGULAR" {
			continue
		}
		open, close := time.UnixMilli(session.Start).UTC(), time.UnixMilli(session.End).UTC()
		if regular == 0 {
			calendar.ValidFrom = open
		}
		calendar.ValidUntil = close
		regular++
		calendar.Sessions = append(calendar.Sessions, operations.MarketSession{Date: open.In(zone).Format("2006-01-02"), OpenLocal: open.In(zone).Format("15:04"), CloseLocal: close.In(zone).Format("15:04")})
	}
	windows, err := calendar.Windows(context.Background())
	if err != nil {
		t.Fatal("PRODUCT_LAB_CALENDAR_MAPPING_FAILED", err)
	}
	at := time.Date(2026, 10, 10, 12, 0, 0, 0, time.UTC)
	weekend, err := calendar.Window(context.Background(), at)
	if err != nil || weekend.Kind != "NON_TRADITIONAL" || weekend.End.Sub(weekend.Start) < 48*time.Hour {
		t.Fatal("PRODUCT_LAB_WEEKEND_SPLIT", err)
	}
	plan, err := calendar.Plan(context.Background(), "lab-soxl-object", "lab-window-policy", nil)
	if err != nil {
		t.Fatal("PRODUCT_LAB_PLAN_FAILED", err)
	}
	report := map[string]any{"raw_hash": d.ContentDigest(body), "regular_sessions": regular, "source_sessions": len(wire.Markets["EQUITY"].Sessions), "windows": len(windows), "plan_windows": len(plan.Windows), "weekend_window": weekend, "calendar": calendar, "provider_update_time": time.UnixMilli(wire.UpdateTime).UTC(), "production_installed": false, "network_calls": 0, "downstream_writes": 0}
	encoded, _ := json.MarshalIndent(report, "", "  ")
	if os.WriteFile(filepath.Join(lab, "calendar-method-report.json"), encoded, 0600) != nil {
		t.Fatal("PRODUCT_LAB_REPORT_WRITE_FAILED")
	}
	t.Logf("equity_sessions=%d; regular_sessions=%d; canonical_windows=%d; weekend_hours=%.1f; production_installed=false", len(wire.Markets["EQUITY"].Sessions), regular, len(windows), weekend.End.Sub(weekend.Start).Hours())
}
