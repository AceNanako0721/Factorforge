// Package fixture owns synthetic in-memory native P1/P2/P3 listeners for console
// and browser tests. It never loads private config, exchanges or model services.
package fixture

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	ca "github.com/AceNanako0721/Factorforge/src/factorforge/applications/console/adapters"
	app "github.com/AceNanako0721/Factorforge/src/factorforge/applications/console/application"
	cd "github.com/AceNanako0721/Factorforge/src/factorforge/applications/console/domain"
	ia "github.com/AceNanako0721/Factorforge/src/factorforge/applications/soxl_jev/api"
	id "github.com/AceNanako0721/Factorforge/src/factorforge/applications/soxl_jev/domain"
	iope "github.com/AceNanako0721/Factorforge/src/factorforge/applications/soxl_jev/operations"
	sa "github.com/AceNanako0721/Factorforge/src/factorforge/strategy/adapters"
	sapi "github.com/AceNanako0721/Factorforge/src/factorforge/strategy/api"
	sapp "github.com/AceNanako0721/Factorforge/src/factorforge/strategy/application"
	sd "github.com/AceNanako0721/Factorforge/src/factorforge/strategy/domain"
	tm "github.com/AceNanako0721/Factorforge/src/factorforge/trading/adapters/memory"
	ts "github.com/AceNanako0721/Factorforge/src/factorforge/trading/adapters/sim"
	ta "github.com/AceNanako0721/Factorforge/src/factorforge/trading/api"
	tapp "github.com/AceNanako0721/Factorforge/src/factorforge/trading/application"
	td "github.com/AceNanako0721/Factorforge/src/factorforge/trading/domain"
	dec "github.com/AceNanako0721/Factorforge/src/factorforge/trading/domain/decimal"
	tw "github.com/AceNanako0721/Factorforge/src/factorforge/trading/workers"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync/atomic"
	"time"
)

type World struct {
	Selection                   cd.Selection
	Client                      ca.HTTPRead
	Trading, Strategy, Instance *httptest.Server
	TradingStore                *tm.Store
	StrategyStore               *sa.MemoryStore
	Sessions                    *app.SessionService
	Registry                    *app.SelectionRegistry
	Policy                      cd.Policy
	User                        cd.User
	DisableInstance             atomic.Bool
	RejectReads                 atomic.Bool
	Writes                      atomic.Int64
}

func (w *World) Close() {
	if w.Trading != nil {
		w.Trading.Close()
	}
	if w.Strategy != nil {
		w.Strategy.Close()
	}
	if w.Instance != nil {
		w.Instance.Close()
	}
}
func New(root string) (*World, error) {
	ctx := context.Background()
	w := &World{}
	raw, e := os.ReadFile(filepath.Join(root, "tests/trading/fixtures/go_service.json"))
	if e != nil {
		return nil, e
	}
	var f struct {
		FixtureOnly bool   `json:"fixture_only"`
		Oracle      string `json:"oracle_commit"`
		Cases       []struct {
			Operation string
			Error     json.RawMessage
			Initial   json.RawMessage
			Input     map[string]json.RawMessage
		}
	}
	if json.Unmarshal(raw, &f) != nil || !f.FixtureOnly || f.Oracle != "e97c9e9a380d105a7e96933bdd325e1d9af06d93" {
		return nil, fmt.Errorf("fixture provenance")
	}
	var run td.Aggregate
	var principal td.Principal
	var command td.Command
	var order td.OrderRequest
	for _, row := range f.Cases {
		if row.Operation == "submit_order" && (len(row.Error) == 0 || string(row.Error) == "null") {
			if json.Unmarshal(row.Initial, &run) != nil || json.Unmarshal(row.Input["principal"], &principal) != nil || json.Unmarshal(row.Input["command"], &command) != nil || json.Unmarshal(row.Input["request"], &order) != nil {
				return nil, fmt.Errorf("trading fixture")
			}
			break
		}
	}
	if run.RunKey.RunID == "" {
		return nil, fmt.Errorf("missing fixture")
	}
	w.TradingStore = tm.New("SIM")
	if e = w.TradingStore.Create(ctx, &run); e != nil {
		return nil, e
	}
	service := &tapp.Service{Store: w.TradingStore, Simulator: &ts.Broker{}}
	order.OrderType = "MARKET"
	order.LimitPrice = nil
	order.TimeInForce = "GTC"
	command.RequestID = "console-fixture-order"
	command.IdempotencyKey = "console-fixture-order"
	result, e := service.SubmitOrder(ctx, principal, command, order, nil)
	if e != nil {
		return nil, e
	}
	if result["state"] != "RESERVED" {
		return nil, fmt.Errorf("fixture order")
	}
	if worked, err := (&tw.Executor{Store: w.TradingStore, Broker: &ts.Adapter{}}).Tick(ctx, run.RunKey); err != nil || !worked {
		return nil, fmt.Errorf("fixture executor: %v", err)
	}
	after, e := w.TradingStore.Read(ctx, run.RunKey)
	if e != nil {
		return nil, e
	}
	at := run.Clock.Add(time.Minute)
	command.ExpectedVersion = after.Version
	command.RequestID = "console-fixture-frame"
	command.IdempotencyKey = command.RequestID
	points := []td.MarketPoint{}
	for _, p := range run.Points.Values() {
		p.ObservedAt = at
		p.ReceivedAt = at
		p.AvailableAt = at
		points = append(points, *p)
	}
	candle := &td.Candle{InstrumentKey: order.InstrumentKey, Interval: "1m", OpenAt: at.Add(-time.Minute), CloseAt: at, AvailableAt: at, Open: value("100"), High: value("105"), Low: value("99"), Close: value("101"), Volume: value("20"), SourceID: "fixture", Final: true, Revision: 1}
	if _, e = service.AdvanceReplay(ctx, principal, command, td.ReplayFrame{At: at, InstrumentKey: order.InstrumentKey, Points: points, Liquidity: value("20"), Candle: candle}); e != nil {
		return nil, e
	}
	state, worker, create, e := strategy(root)
	if e != nil {
		return nil, e
	}
	create.Object.TradingRunKey = sd.RunBinding{Environment: "SIM", AccountID: run.RunKey.AccountID, RunID: run.RunKey.RunID}
	create.Object.InstrumentKey = sd.InstrumentBinding{Venue: order.InstrumentKey.Venue, Product: order.InstrumentKey.Product, InstrumentID: order.InstrumentKey.InstrumentID}
	create.Object.OwnerID = order.OwnerID
	w.StrategyStore = sa.NewMemory(state)
	clock := sa.NewReplay(at)
	if _, e = (&sapp.Service{Store: w.StrategyStore, Clock: clock}).Create(ctx, worker, create); e != nil {
		return nil, e
	}
	// The event comes from the same real framework command boundary.
	event := sd.Event{EventID: "fixture-event", FamilyID: "fixture-family", FactVersion: 1, Relation: "NEW", SubjectID: "fixture-entity", EventType: "earnings", OccurredAt: at.Add(-time.Minute), FirstPublicAt: at.Add(-time.Minute), EvidenceRefs: []sd.EvidenceRef{{EvidenceID: "evidence-fixture", ContentHash: id.ContentDigest([]byte("Synthetic original <img src=x onerror=alert(1)>")), SourceID: "fixture-source", LicenceRef: "fixture-licence", FirstPublicAt: at.Add(-time.Minute), ReceivedAt: at, AvailableAt: at, SpanRefs: []string{"fixture-span"}, VerificationRef: "fixture-verified"}}, Claims: []sd.Claim{}, ObjectIDs: []string{create.Object.ObjectID}, State: "QUARANTINED", Novelty: value("0")}
	snapshot, _ := w.StrategyStore.Read(ctx, state.InstanceID)
	eventCommand := sd.EventCommand{Command: sd.Command{SchemaVersion: "strategy-2.0", RequestID: "console-event", IdempotencyKey: "console-event", ExpectedVersion: snapshot.Version, Reason: "fixture only"}, Event: event}
	if _, e = (&sapp.Service{Store: w.StrategyStore, Clock: clock}).RegisterEvent(ctx, worker, eventCommand); e != nil {
		return nil, e
	}
	public := sd.PublicPrincipal{PrincipalID: "fixture-console", InstanceID: state.InstanceID, Environment: "SIM", Scopes: []sd.ApiScope{sd.Query}}
	readPolicy := sapp.ReadPolicy{DefaultLimit: 2, MaxLimit: 20, MaxRecords: 1000, CursorAge: time.Minute, CursorKey: []byte("synthetic-console-public-query-key")}
	handler, e := sapi.New(sapi.Options{Store: w.StrategyStore, Clock: clock, Tokens: map[string]sd.Identity{"fixture-p2-read": public}, ReadPolicy: readPolicy})
	if e != nil {
		return nil, e
	}
	w.Strategy = httptest.NewServer(w.guard(handler))
	readPrincipal := principal
	readPrincipal.Permissions = []string{"read"}
	w.Trading = httptest.NewServer(w.guard(ta.New(service, map[string]td.Principal{"fixture-p1-read": readPrincipal})))
	binding := id.Binding{InstanceID: state.InstanceID, Environment: "SIM"}
	original := []byte("Synthetic original <img src=x onerror=alert(1)>")
	store := &instanceStore{snapshot: id.Snapshot{Binding: binding, Version: 0, SourceVersion: "fixture-v1", Health: &id.Health{Stage: "R0", RecoveryState: "UNKNOWN", CheckedAt: &at, Degradation: []string{}, Capabilities: []id.Capability{{Name: "JEV", State: "EXAMPLE_OR_MOCK"}}}, Sources: []id.Source{{SourceID: "fixture-source", RegistryVersion: "fixture-source-v1", LicenceRef: "fixture-licence", LicenceState: "VERIFIED", AllowOriginal: true, Enabled: true, LastSuccessAt: &at}}, Jobs: []id.Job{}, Evidence: []id.Evidence{{EvidenceID: "evidence-fixture", SourceID: "fixture-source", ContentHash: id.ContentDigest(original), LicenceRef: "fixture-licence", ReceivedAt: at, ExtractionState: "UNKNOWN", Facts: []id.Fact{}, Spans: []id.Span{}}}, Budgets: []id.Budget{{QueueKind: "RESEARCH"}, {QueueKind: "SIM"}}, Reports: []id.Report{}, Audit: []id.Audit{}}, raw: original}
	if e = store.snapshot.Validate(); e != nil {
		return nil, e
	}
	start := at.Add(-time.Hour)
	reportReason := "INSUFFICIENT_EVIDENCE"
	archive := id.FrameworkReport{Binding: binding, ObjectID: create.Object.ObjectID, View: id.Report{ReportID: "fixture-periodic-report", RecordedAt: at, PeriodStart: &start, PeriodEnd: &at, State: "RECORDED", ParameterVersions: []string{create.Parameters.Version}, ActivationRefs: []string{}, AttributionRefs: []string{}, ReasonCodes: []string{"FROZEN_CONTROL_DIFFERENCE_NOT_RECORDED"}}, Learning: []id.LearningFact{{ObjectID: create.Object.ObjectID, Parameter: "w", At: start.Add(time.Minute), Reason: &reportReason, Error: "0.1", Neff: "1", Groups: 1}}, Changes: []id.ActivationFact{}, TotalCases: 2, UnknownCases: 1}
	store.snapshot.Reports = []id.Report{archive.PublicView()}
	if e = store.snapshot.Validate(); e != nil {
		return nil, e
	}
	ih, e := ia.New(ia.Options{Query: iope.QueryService{Store: store, Clock: clock, Policy: iope.ReadPolicy{DefaultLimit: 2, MaxLimit: 20, MaxRecords: 1000, MaxSnapshotBytes: 1 << 20, MaxOriginalBytes: 4096, CursorAge: time.Minute, CursorKey: []byte("synthetic-console-instance-query-key")}}, Tokens: map[string]id.ReadPrincipal{"fixture-p3-read": {PrincipalID: "fixture-console", Binding: binding, Read: true, OriginalSources: []string{"fixture-source"}, AuthorizationVersion: "fixture-auth"}}})
	if e != nil {
		return nil, e
	}
	w.Instance = httptest.NewServer(http.HandlerFunc(func(rw http.ResponseWriter, r *http.Request) {
		if w.DisableInstance.Load() {
			rw.WriteHeader(503)
			return
		}
		w.guard(ih).ServeHTTP(rw, r)
	}))
	w.Selection = cd.Selection{ID: "fixture-selection", DeploymentID: "fixture-local", Environment: "SIM", InstanceID: state.InstanceID, ObjectID: create.Object.ObjectID, TradingRunKey: run.RunKey, InstrumentKey: order.InstrumentKey, OwnerID: order.OwnerID, BindingVersion: "fixture-binding-v1"}
	w.Client = ca.HTTPRead{Bindings: map[string]ca.Services{w.Selection.ID: {Trading: ca.Service{URL: w.Trading.URL, Token: "fixture-p1-read", FixtureOnly: true}, Strategy: ca.Service{URL: w.Strategy.URL, Token: "fixture-p2-read", FixtureOnly: true}, Instance: ca.Service{URL: w.Instance.URL, Token: "fixture-p3-read", FixtureOnly: true}}}, Timeout: 5 * time.Second, MaxBytes: 1 << 20}
	w.Policy = cd.Policy{SessionTTL: time.Hour, ChallengeTTL: time.Minute, RateWindow: time.Minute, CursorTTL: time.Minute, RequestTimeout: 10 * time.Second, MaxAttempts: 100, MaxSessions: 20, MaxChallenges: 100, MaxBytes: 1 << 20, MaxRecords: 1000, DefaultLimit: 2, MaxLimit: 20, MaxPages: 10, CursorKey: []byte("synthetic-console-signed-cursor-key")}
	hash, e := app.HashPassword("fixture-pass", 100)
	if e != nil {
		return nil, e
	}
	w.User = cd.User{ID: "fixture-user", Name: "本地 SIM 验收", Username: "fixture-user", PasswordHash: hash, SelectionIDs: []string{w.Selection.ID}, Capabilities: []string{"view", "export"}, AuthorizationVersion: "fixture-auth-v1"}
	w.Registry, e = app.NewRegistry([]cd.Selection{w.Selection})
	if e != nil {
		return nil, e
	}
	w.Sessions, e = app.NewSessions([]cd.User{w.User}, w.Policy, 1000, time.Now)
	return w, e
}
func (w *World) guard(next http.Handler) http.Handler {
	return http.HandlerFunc(func(rw http.ResponseWriter, r *http.Request) {
		if r.Method != "GET" {
			w.Writes.Add(1)
		}
		if w.RejectReads.Load() {
			rw.WriteHeader(403)
			return
		}
		next.ServeHTTP(rw, r)
	})
}

type instanceStore struct {
	snapshot id.Snapshot
	raw      []byte
}

func (s *instanceStore) Binding() id.Binding                            { return s.snapshot.Binding }
func (s *instanceStore) Read(context.Context, int) (id.Snapshot, error) { return s.snapshot, nil }
func (s *instanceStore) Original(_ context.Context, e string, v int64, _ int) ([]byte, error) {
	if e != "evidence-fixture" || v != s.snapshot.Version {
		return nil, id.Fail("QUERY_SNAPSHOT_CHANGED", 409)
	}
	return append([]byte(nil), s.raw...), nil
}
func strategy(root string) (*sd.StrategyState, sd.WorkloadIdentity, sd.CreateObject, error) {
	raw, e := os.ReadFile(filepath.Join(root, "tests/strategy/fixtures/go_replay.json"))
	if e != nil {
		return nil, sd.WorkloadIdentity{}, sd.CreateObject{}, e
	}
	var r map[string]any
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	if dec.Decode(&r) != nil || r["fixture_only"] != true || r["source_commit"] != "e97c9e9a380d105a7e96933bdd325e1d9af06d93" {
		return nil, sd.WorkloadIdentity{}, sd.CreateObject{}, fmt.Errorf("provenance")
	}
	nodes := r["nodes"].(map[string]any)
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
			return x
		}
	}
	decode := func(v any, target any) error { raw, _ := json.Marshal(v); return sd.DecodeJSON(raw, target) }
	for _, entry := range r["cases"].([]any) {
		item := expand(entry).(map[string]any)
		if item["kind"] != "create" {
			continue
		}
		var state sd.StrategyState
		var worker sd.WorkloadIdentity
		var create sd.CreateObject
		input := item["input"].(map[string]any)
		for _, pair := range []struct {
			value  any
			target any
		}{{expand(r["states"].(map[string]any)[item["before"].(string)]), &state}, {input["identity"], &worker}, {input["command"], &create.Command}, {input["obj"], &create.Object}, {input["policy"], &create.Policy}, {input["params"], &create.Parameters}} {
			if e = decode(pair.value, pair.target); e != nil {
				return nil, worker, create, e
			}
		}
		return &state, worker, create, nil
	}
	return nil, sd.WorkloadIdentity{}, sd.CreateObject{}, fmt.Errorf("missing create")
}

func value(s string) dec.Value {
	v, e := dec.Parse(s)
	if e != nil {
		panic(e)
	}
	return v
}
