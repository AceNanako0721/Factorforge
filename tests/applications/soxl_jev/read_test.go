package soxl_jev_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"github.com/AceNanako0721/Factorforge/src/factorforge/applications/soxl_jev/api"
	d "github.com/AceNanako0721/Factorforge/src/factorforge/applications/soxl_jev/domain"
	"github.com/AceNanako0721/Factorforge/src/factorforge/applications/soxl_jev/operations"
	contract "github.com/AceNanako0721/Factorforge/tools/contractguard"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"
)

func ptr[T any](v T) *T { return &v }

type fixedClock struct{ at time.Time }

func (c *fixedClock) Now() time.Time { return c.at }

type memory struct {
	state         d.Snapshot
	raw           []byte
	originalReads int
}

func (m *memory) Binding() d.Binding                            { return m.state.Binding }
func (m *memory) Read(context.Context, int) (d.Snapshot, error) { return m.state, nil }
func (m *memory) Original(_ context.Context, id string, version int64, max int) ([]byte, error) {
	m.originalReads++
	if version != m.state.Version {
		return nil, d.Fail("QUERY_SNAPSHOT_CHANGED", 409)
	}
	if id != "evidence-one" {
		return nil, nil
	}
	return append([]byte(nil), m.raw...), nil
}
func fixture() (d.Snapshot, []byte, *fixedClock, d.ReadPrincipal, operations.ReadPolicy) {
	at := time.Date(2026, 10, 7, 0, 0, 0, 0, time.UTC)
	raw := []byte("虚构公告：本例仅为离线测试，不构成交易证据。")
	h := sha256.Sum256(raw)
	hash := hex.EncodeToString(h[:])
	b := d.Binding{InstanceID: "soxl-jev-fixture", Environment: "SIM"}
	s := d.Snapshot{Binding: b, Version: 0, SourceVersion: "fixture-projection-v1", Health: &d.Health{Stage: "R0", RecoveryState: "UNKNOWN", Capabilities: []d.Capability{{Name: "JEV", State: "EXAMPLE_OR_MOCK"}, {Name: "INGEST", State: "NOT_CONFIGURED"}}}, Sources: []d.Source{{SourceID: "source-a", RegistryVersion: "registry-fixture", LicenceRef: "licence-fixture", LicenceState: "VERIFIED", AllowOriginal: true, Enabled: true, LastSuccessAt: &at}, {SourceID: "source-b", RegistryVersion: "registry-fixture", LicenceRef: "licence-unknown", LicenceState: "UNKNOWN", Enabled: false}}, Jobs: []d.Job{{JobID: "job-a", QueueKind: "RESEARCH", State: "ABSTAINED", CreatedAt: at, Deadline: at.Add(time.Hour), CompletedAt: &at, ReasonCodes: []string{"EXAMPLE_OR_MOCK"}}, {JobID: "job-b", QueueKind: "SIM", State: "EXPIRED", CreatedAt: at, Deadline: at.Add(time.Hour)}}, Evidence: []d.Evidence{{EvidenceID: "evidence-one", SourceID: "source-a", ContentHash: hash, LicenceRef: "licence-fixture", ReceivedAt: at, ExtractionState: "UNKNOWN", Facts: []d.Fact{{ClaimID: "claim-fixture", SubjectID: "entity-fixture", EconomicItem: "item-fixture"}}, Spans: []d.Span{}}}, Budgets: []d.Budget{{QueueKind: "RESEARCH", Limit: ptr("2.000000000000000001"), Consumed: ptr("0.000000000000000001"), ReservedCapacity: ptr(1), ObservedAt: &at}}, Reports: []d.Report{{ReportID: "report-a", RecordedAt: at, State: "UNKNOWN", ReasonCodes: []string{"FRAMEWORK_REPORT_NOT_RECORDED"}}, {ReportID: "report-b", RecordedAt: at.Add(time.Minute), State: "INCOMPLETE"}}, Audit: []d.Audit{{AuditID: "audit-a", RecordedAt: at, Action: "EXAMPLE_OR_MOCK"}, {AuditID: "audit-b", RecordedAt: at.Add(time.Minute), Action: "ANALYSIS_ABSTAINED", JobID: ptr("job-a")}}}
	p := d.ReadPrincipal{PrincipalID: "fixture-reader", Binding: b, Read: true, AuthorizationVersion: "auth-v1"}
	policy := operations.ReadPolicy{DefaultLimit: 1, MaxLimit: 10, MaxRecords: 100, MaxSnapshotBytes: 1 << 20, MaxOriginalBytes: 4096, CursorAge: time.Minute, CursorKey: []byte("fixture-only-cursor-signing-32-bytes")}
	return s, raw, &fixedClock{at.Add(time.Minute)}, p, policy
}
func assertCode(t *testing.T, err error, code string) {
	t.Helper()
	var e *d.Error
	if !errors.As(err, &e) || e.Code != code {
		t.Fatalf("wanted %s; got %v", code, err)
	}
}
func TestInstanceHTTPReadFactsScopesPaginationAndNoSideEffects(t *testing.T) {
	s, raw, clock, p, policy := fixture()
	if err := s.Validate(); err != nil {
		t.Fatal(err)
	}
	store := &memory{state: s, raw: raw}
	q := operations.QueryService{Store: store, Clock: clock, Policy: policy}
	original := p
	original.PrincipalID = "fixture-original-reader"
	original.OriginalSources = []string{"source-a"}
	handler, err := api.New(api.Options{Query: q, Tokens: map[string]d.ReadPrincipal{"fixture-read": p, "fixture-original": original}})
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(handler)
	defer server.Close()
	before, _ := json.Marshal(store.state)
	var doc map[string]any
	json.Unmarshal(api.OpenAPI(), &doc)
	if err = contract.Lint(doc, false, nil); err != nil {
		t.Fatal(err)
	}
	compiler := contract.NewCompiler()
	if err = compiler.AddResource("urn:instance-test", doc); err != nil {
		t.Fatal(err)
	}
	read := func(token, route, template string, status int) map[string]any {
		t.Helper()
		r, _ := http.NewRequest("GET", server.URL+"/api/v2/instances/"+s.Binding.InstanceID+route, nil)
		r.Header.Set("Authorization", "Bearer "+token)
		response, e := server.Client().Do(r)
		if e != nil {
			t.Fatal(e)
		}
		defer response.Body.Close()
		body, e := io.ReadAll(response.Body)
		if e != nil || response.StatusCode != status {
			t.Fatalf("%s: status %d", route, response.StatusCode)
		}
		if response.Header.Get("Cache-Control") != "no-store" {
			t.Fatal("private response cacheable")
		}
		var value map[string]any
		if json.Unmarshal(body, &value) != nil {
			t.Fatal("bad JSON")
		}
		if status == 200 {
			pointer := "urn:instance-test#/paths/" + strings.ReplaceAll(api.Prefix+template, "/", "~1") + "/get/responses/200/content/application~1json/schema"
			schema, e := compiler.Compile(pointer)
			if e != nil {
				t.Fatal(e)
			}
			if e = schema.Validate(value); e != nil {
				t.Fatal(e)
			}
		}
		return value
	}
	health := read("fixture-read", "/health", "/health", 200)["data"].(map[string]any)
	if health["live_ready"] != false || health["checked_at"] != nil {
		t.Fatal("invented readiness or checked time")
	}
	for _, path := range []string{"/sources", "/analysis-jobs", "/budgets", "/reports", "/audit"} {
		read("fixture-read", path, path, 200)
	}
	read("fixture-read", "/analysis-jobs/job-b", "/analysis-jobs/{job_id}", 200)
	ev := read("fixture-read", "/evidence/evidence-one", "/evidence/{evidence_id}", 200)["data"].(map[string]any)
	if ev["raw_text"] != nil || ev["redaction_reason"] != "ORIGINAL_SCOPE_FORBIDDEN" || store.originalReads != 0 {
		t.Fatal("unauthorized original accessed")
	}
	ev = read("fixture-original", "/evidence/evidence-one", "/evidence/{evidence_id}", 200)["data"].(map[string]any)
	if ev["raw_text"] != string(raw) || ev["redaction_reason"] != nil || store.originalReads != 1 {
		t.Fatal("licensed original missing")
	}
	first := read("fixture-read", "/sources", "/sources", 200)
	c := first["cursor"].(string)
	page := read("fixture-read", "/sources?cursor="+url.QueryEscape(c), "/sources", 200)
	if page["cursor"] != nil || page["items"].([]any)[0].(map[string]any)["source_id"] != "source-b" {
		t.Fatal("bad stable page")
	}
	read("fixture-original", "/sources?cursor="+url.QueryEscape(c), "", 409)
	read("fixture-read", "/sources?cursor=forged", "", 409)
	read("fixture-read", "/sources?limit=2&cursor="+url.QueryEscape(c), "", 409)
	clock.at = clock.at.Add(2 * time.Minute)
	read("fixture-read", "/sources?cursor="+url.QueryEscape(c), "", 409)
	clock.at = clock.at.Add(-2 * time.Minute)
	for _, route := range []string{"/sources?limit=0", "/sources?limit=1&limit=2", "/sources?cursor=", "/sources?unknown=a", "/sources?limit=+1", "/sources?x=%ZZ", "/health?limit=1", "/reports?from=2026-10-07T00:00:00%2B00:00", "/reports?from=2026-10-08T00:00:00Z&to=2026-10-07T00:00:00Z", "/analysis-jobs?state=INVALID", "/evidence/evidence-one?url=http://private.invalid"} {
		read("fixture-read", route, "", 422)
	}
	read("fixture-read", "/analysis-jobs?queue_kind=LIVE", "", 403)
	read("missing", "/health", "", 401)
	read("fixture-read", "/evidence/missing", "", 404)
	read("fixture-read", "/analysis-jobs/missing", "", 404)
	filtered := read("fixture-read", "/analysis-jobs?queue_kind=SIM&state=EXPIRED", "/analysis-jobs", 200)
	if len(filtered["items"].([]any)) != 1 {
		t.Fatal("queue filter failed")
	}
	filtered = read("fixture-read", "/reports?from=2026-10-07T00:01:00Z", "/reports", 200)
	if len(filtered["items"].([]any)) != 1 {
		t.Fatal("time filter failed")
	}
	after, _ := json.Marshal(store.state)
	if !bytes.Equal(before, after) {
		t.Fatal("query altered recorded state")
	}
	for _, method := range []string{"POST", "PATCH", "DELETE"} {
		r, _ := http.NewRequest(method, server.URL+"/api/v2/instances/"+s.Binding.InstanceID+"/analysis-jobs", strings.NewReader(`{"route":"TRADING_CANDIDATE"}`))
		r.Header.Set("Authorization", "Bearer fixture-read")
		resp, e := server.Client().Do(r)
		if e != nil {
			t.Fatal(e)
		}
		resp.Body.Close()
		if resp.StatusCode != 405 {
			t.Fatal("write route exposed")
		}
	}
	r, _ := http.NewRequest("GET", server.URL+"/api/v2/instances/other-instance/health", nil)
	r.Header.Set("Authorization", "Bearer fixture-read")
	resp, err := server.Client().Do(r)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != 403 {
		t.Fatal("cross instance exposed")
	}
}
func TestInstanceReadPolicyMissingFactsAndLicenceFailure(t *testing.T) {
	s, raw, clock, p, policy := fixture()
	store := &memory{state: s, raw: raw}
	q := operations.QueryService{Store: store, Clock: clock, Policy: policy}
	ctx := context.Background()
	before, _ := json.Marshal(s)
	for _, state := range []string{"UNKNOWN", "DENIED", "EXPIRED"} {
		store.state.Sources[0].LicenceState = state
		v, err := q.Read(ctx, p, "evidence", "evidence-one", operations.Filter{})
		if err != nil {
			t.Fatal(err)
		}
		if v.(operations.Record).Data.(operations.EvidenceView).RawText != nil || store.originalReads != 0 {
			t.Fatal("unlicensed original leaked")
		}
	}
	store.state.Sources[0].LicenceState = "VERIFIED"
	p.OriginalSources = []string{"source-a"}
	store.state.Sources[0].LicenceValidUntil = ptr(clock.Now().Add(-time.Second))
	v, err := q.Read(ctx, p, "evidence", "evidence-one", operations.Filter{})
	if err != nil || v.(operations.Record).Data.(operations.EvidenceView).RawText != nil || store.originalReads != 0 {
		t.Fatal("expired licence leaked")
	}
	store.state.Sources[0].LicenceValidUntil = nil
	store.raw = []byte("corrupt")
	_, err = q.Read(ctx, p, "evidence", "evidence-one", operations.Filter{})
	assertCode(t, err, "ORIGINAL_INTEGRITY_INVALID")
	store.raw = nil
	v, err = q.Read(ctx, p, "evidence", "evidence-one", operations.Filter{})
	if err != nil || *v.(operations.Record).Data.(operations.EvidenceView).RedactionReason != "ORIGINAL_NOT_RECORDED" {
		t.Fatal("missing raw fabricated")
	}
	store.state.Health = nil
	_, err = q.Read(ctx, p, "health", "", operations.Filter{})
	assertCode(t, err, "HEALTH_NOT_RECORDED")
	store.state.Health = s.Health
	q.Policy.MaxRecords = 10
	_, err = q.Read(ctx, p, "sources", "", operations.Filter{})
	assertCode(t, err, "QUERY_RESOURCE_LIMIT")
	q.Policy = policy
	q.Policy.CursorKey = nil
	_, err = q.Read(ctx, p, "sources", "", operations.Filter{})
	assertCode(t, err, "QUERY_POLICY_REQUIRED")
	q.Policy = policy
	p.Binding.Environment = "LIVE"
	_, err = q.Read(ctx, p, "health", "", operations.Filter{})
	assertCode(t, err, "INSTANCE_SCOPE_FORBIDDEN")
	var original d.Snapshot
	if json.Unmarshal(before, &original) != nil {
		t.Fatal("fixture invalid")
	}
	original.Jobs[0].QueueKind = "LIVE"
	assertCode(t, original.Validate(), "INSTANCE_RECORD_INVALID")
	original.Jobs[0].QueueKind = "RESEARCH"
	original.Sources[0].LastFailureCode = ptr("private-input-text")
	assertCode(t, original.Validate(), "INSTANCE_RECORD_INVALID")
}
func TestInstanceContractGeneratedAndOfflineValidated(t *testing.T) {
	raw, err := os.ReadFile("../../../contracts/v2/instances/openapi.json")
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(raw, api.OpenAPI()) {
		t.Fatal("instance contract drift")
	}
	var doc, meta map[string]any
	json.Unmarshal(raw, &doc)
	raw, err = os.ReadFile("../../../contracts/meta/openapi-3.1-2025-09-15.json")
	if err != nil {
		t.Fatal(err)
	}
	json.Unmarshal(raw, &meta)
	if err = contract.Validate(meta, doc); err != nil {
		t.Fatal(err)
	}
	if err = contract.Lint(doc, false, nil); err != nil {
		t.Fatal(err)
	}
	if err = contract.StubSmoke(doc); err != nil {
		t.Fatal(err)
	}
}
