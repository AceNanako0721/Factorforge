package soxl_jev_test

import (
	"bytes"
	"context"
	"encoding/json"
	d "github.com/AceNanako0721/Factorforge/src/factorforge/applications/soxl_jev/domain"
	"github.com/AceNanako0721/Factorforge/src/factorforge/applications/soxl_jev/operations"
	"github.com/AceNanako0721/Factorforge/src/factorforge/applications/soxl_jev/ports"
	"github.com/AceNanako0721/Factorforge/src/factorforge/applications/soxl_jev/reports"
	"github.com/AceNanako0721/Factorforge/src/factorforge/applications/soxl_jev/submission"
	"github.com/AceNanako0721/Factorforge/src/factorforge/applications/soxl_jev/workers"
	sa "github.com/AceNanako0721/Factorforge/src/factorforge/strategy/adapters"
	sapi "github.com/AceNanako0721/Factorforge/src/factorforge/strategy/api"
	sapp "github.com/AceNanako0721/Factorforge/src/factorforge/strategy/application"
	sd "github.com/AceNanako0721/Factorforge/src/factorforge/strategy/domain"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

type operationMemory struct {
	rows    []d.OperationFact
	reports map[string]d.FrameworkReport
	failed  bool
}

func (s *operationMemory) RecordOperation(_ context.Context, v d.OperationFact) error {
	if s.failed {
		return d.Fail("OPERATION_STORE_UNAVAILABLE", 503)
	}
	if !v.Valid() {
		return d.Fail("OPERATION_INVALID", 422)
	}
	s.rows = append(s.rows, v)
	return nil
}
func (s *operationMemory) RecordedReport(_ context.Context, id string) (*d.FrameworkReport, error) {
	if v, ok := s.reports[id]; ok {
		return &v, nil
	}
	return nil, nil
}
func (s *operationMemory) RecordReport(_ context.Context, v d.FrameworkReport) error {
	if s.reports == nil {
		s.reports = map[string]d.FrameworkReport{}
	}
	s.reports[v.View.ReportID] = v
	return nil
}

type monitorFixture struct {
	rows  []d.RawEvidence
	err   error
	calls *int
}

func (s monitorFixture) Poll(context.Context, time.Time) ([]d.RawEvidence, error) {
	*s.calls++
	return s.rows, s.err
}

func TestIndependentSourcesAndSafeOperationFacts(t *testing.T) {
	_, r, _, now := pipelineFixture()
	store := &operationMemory{}
	failedCalls, successCalls := 0, 0
	cycle := workers.IngestCycle{Worker: workers.IngestWorker{Clock: &pipelineClock{now}, Policy: d.RoutingPolicy{Binding: r.Binding}}, Store: store, Sources: map[string]ports.MonitorSource{"source-a": monitorFixture{err: d.Fail("SOURCE_TIMEOUT", 503), calls: &failedCalls}, "source-b": monitorFixture{rows: []d.RawEvidence{}, calls: &successCalls}}}
	if e := cycle.Run(context.Background(), nil); e == nil || failedCalls != 1 || successCalls != 1 || len(store.rows) != 2 || store.rows[0].Success || !store.rows[1].Success {
		t.Fatal("independent polling", e, store.rows)
	}
	if e := operations.RecordFact(context.Background(), store, r.Binding, "INGEST", "SEARCH", nil, nil, now, errWithPrivateText{}); e != nil {
		t.Fatal(e)
	}
	raw, _ := json.Marshal(store.rows)
	if bytes.Contains(raw, []byte("private-canary")) || store.rows[2].Code != "SEARCH_UNAVAILABLE" {
		t.Fatal("unsafe exception persisted")
	}
	store.failed = true
	if e := cycle.Run(context.Background(), nil); e == nil {
		t.Fatal("failed operation persistence ignored")
	}
}

type errWithPrivateText struct{}

func (errWithPrivateText) Error() string { return "private-canary" }

func TestReportActualPublicGoP2SnapshotAndScheduledOnce(t *testing.T) {
	ctx := context.Background()
	state, identity, create := p2Fixture(t)
	_, _, _, now := pipelineFixture()
	clock := sa.NewReplay(now)
	store := sa.NewMemory(state)
	service := sapp.Service{Store: store, Clock: clock}
	if _, e := service.Create(ctx, identity, create); e != nil {
		t.Fatal(e)
	}
	public := sd.PublicPrincipal{PrincipalID: "fixture-report", InstanceID: state.InstanceID, Environment: state.Environment, Scopes: []sd.ApiScope{sd.Query}}
	policy := sapp.ReadPolicy{DefaultLimit: 2, MaxLimit: 10, MaxRecords: 100, CursorAge: time.Hour, CursorKey: []byte("fixture-only-cursor-secret")}
	handler, e := sapi.New(sapi.Options{Store: store, Clock: clock, Tokens: map[string]sd.Identity{"fixture-report-read": public}, ReadPolicy: policy})
	if e != nil {
		t.Fatal(e)
	}
	writes := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "GET" {
			writes++
		}
		handler.ServeHTTP(w, r)
	}))
	defer server.Close()
	before, _ := store.Read(ctx, state.InstanceID)
	baseline, _ := json.Marshal(before)
	binding := d.Binding{InstanceID: state.InstanceID, Environment: state.Environment}
	client, e := submission.NewReportRead(submission.ReportReadOptions{URL: server.URL, Token: "fixture-report-read", Binding: binding, ObjectID: create.Object.ObjectID, MaxBytes: 100000, MaxRecords: 100, MaxPages: 10, Timeout: time.Second, FixtureOnly: true})
	if e != nil {
		t.Fatal(e)
	}
	from, to := now.Add(-time.Hour), now
	report, e := client.ReadReport(ctx, create.Object.ObjectID, from, to, now)
	if e != nil || !report.Valid() || len(report.View.ParameterVersions) != 1 || report.TotalCases != 0 || report.FrozenControlDifference != nil || !d.Has(report.View.ReasonCodes, "LEARNING_DECISION_NOT_RECORDED") {
		t.Fatal("native report", e, report)
	}
	archive := &operationMemory{}
	scheduled := reports.Scheduled{Store: archive, Source: client, Clock: &pipelineClock{now}, Binding: binding, ObjectID: create.Object.ObjectID, Policy: reports.Schedule{Anchor: from, PeriodSeconds: 3600}}
	if e = scheduled.Tick(ctx); e != nil {
		t.Fatal(e)
	}
	if e = scheduled.Tick(ctx); e != nil || len(archive.reports) != 1 {
		t.Fatal("report repeat", e, len(archive.reports))
	}
	after, _ := store.Read(ctx, state.InstanceID)
	result, _ := json.Marshal(after)
	if writes != 0 || !bytes.Equal(baseline, result) {
		t.Fatal("report changed framework state")
	}
	if _, e = client.ReadReport(ctx, "foreign-object", from, to, now); e == nil {
		t.Fatal("object override")
	}
	if _, e = client.ReadReport(ctx, create.Object.ObjectID, from, now.Add(time.Hour), now); e == nil {
		t.Fatal("future report")
	}
}
