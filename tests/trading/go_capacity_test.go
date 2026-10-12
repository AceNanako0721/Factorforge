package trading_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"reflect"
	"strconv"
	"testing"
	"time"

	"github.com/AceNanako0721/Factorforge/src/factorforge/trading/adapters/health"
	"github.com/AceNanako0721/Factorforge/src/factorforge/trading/adapters/memory"
	"github.com/AceNanako0721/Factorforge/src/factorforge/trading/adapters/sim"
	"github.com/AceNanako0721/Factorforge/src/factorforge/trading/api"
	a "github.com/AceNanako0721/Factorforge/src/factorforge/trading/application"
	d "github.com/AceNanako0721/Factorforge/src/factorforge/trading/domain"
	"github.com/AceNanako0721/Factorforge/src/factorforge/trading/domain/decimal"
	"github.com/AceNanako0721/Factorforge/src/factorforge/trading/ports"
	"github.com/AceNanako0721/Factorforge/src/factorforge/trading/workers"
)

func capacityCandle(run *d.Aggregate, closeAt time.Time) d.Candle {
	key := run.Specs.Values()[0].Key
	value, _ := decimal.Parse("100")
	return d.Candle{InstrumentKey: key, Interval: "5m", OpenAt: closeAt.Add(-5 * time.Minute), CloseAt: closeAt, AvailableAt: closeAt, Open: value, High: value, Low: value, Close: value, Volume: value, Final: true, SourceID: "capacity-fixture"}
}
func capacityCode(t *testing.T, err error, code string) {
	t.Helper()
	var failure *d.Error
	if !errors.As(err, &failure) || failure.Code != code {
		t.Fatalf("expected %s, got %v", code, err)
	}
}
func capacitySnapshot(t *testing.T, service *a.Service, p d.Principal, key d.RunKey, at time.Time, candles ...d.Candle) (a.Response, error) {
	t.Helper()
	run, err := service.Store.Read(context.Background(), key)
	if err != nil {
		t.Fatal(err)
	}
	id := "capacity-" + strconv.FormatInt(run.Version, 10) + "-" + strconv.FormatInt(at.UnixNano(), 10)
	body := d.MarketSnapshot{Command: d.Command{SchemaVersion: "trading-2.0", RunKey: key, RequestID: id, IdempotencyKey: id, ExpectedVersion: run.Version, Reason: "capacity fixture", ExpiresAtUTC: at.Add(time.Hour)}, At: at, Candles: candles}
	return service.IngestSnapshot(context.Background(), p, body)
}
func capacityPage(t *testing.T, handler http.Handler, key d.RunKey, resource string, query url.Values) map[string]json.RawMessage {
	t.Helper()
	if query == nil {
		query = url.Values{}
	}
	query.Set("environment", key.Environment)
	query.Set("account_id", key.AccountID)
	query.Set("run_id", key.RunID)
	request := httptest.NewRequest("GET", api.Prefix+"/"+resource+"?"+query.Encode(), nil)
	request.Header.Set("Authorization", "Bearer synthetic")
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)
	if recorder.Code != 200 {
		t.Fatalf("%s: %d %s", resource, recorder.Code, recorder.Body.String())
	}
	var result map[string]json.RawMessage
	if json.Unmarshal(recorder.Body.Bytes(), &result) != nil {
		t.Fatal("invalid page")
	}
	return result
}

func TestCapacityCandleCorrections(t *testing.T) {
	cases := []struct {
		name     string
		age      time.Duration
		revision int64
		source   string
		same     bool
		conflict bool
	}{
		{name: "automatic", age: time.Minute}, {name: "window-inclusive", age: time.Hour},
		{name: "window-exceeded", age: time.Hour + time.Nanosecond, conflict: true},
		{name: "same-values", age: 2 * time.Hour, revision: 9, same: true},
		{name: "explicit-higher", age: 2 * time.Hour, revision: 7},
		{name: "source-conflict", age: time.Minute, source: "other", conflict: true},
		{name: "stale-explicit", age: time.Minute, revision: 1, conflict: true},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			ctx := context.Background()
			run, p, _, _ := serviceBase(t)
			candle := capacityCandle(run, run.Clock)
			candle.Revision = 2
			run.Candles = []d.Candle{candle}
			store := memory.New("SIM")
			if err := store.Create(ctx, run); err != nil {
				t.Fatal(err)
			}
			service := &a.Service{Store: store}
			revised := candle
			revised.Revision = test.revision
			at := run.Clock.Add(test.age)
			revised.AvailableAt = candle.AvailableAt
			if test.same {
				revised.AvailableAt = at
				revised.Volume, _ = decimal.Parse("100.00")
			}
			if !test.same {
				revised.Volume, _ = decimal.Parse("101")
			}
			if test.source != "" {
				revised.SourceID = test.source
			}
			response, err := capacitySnapshot(t, service, p, run.RunKey, at, revised)
			if test.conflict {
				capacityCode(t, err, "CANDLE_REVISION_CONFLICT")
				after, _ := store.Read(ctx, run.RunKey)
				if after.Clock != run.Clock || !d.EqualModel(after.Candles[0], candle) {
					t.Fatal("conflict changed snapshot")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			after, _ := store.Read(ctx, run.RunKey)
			if test.same {
				if !d.EqualModel(after.Candles[0], candle) || len(response["revised_candles"].([]any)) != 0 {
					t.Fatal("same values rewritten")
				}
			} else {
				want := int64(3)
				if test.revision > 2 {
					want = test.revision
				}
				wantAt := at
				if test.revision > 2 {
					wantAt = revised.AvailableAt
				}
				if after.Candles[0].Revision != want || !after.Candles[0].AvailableAt.Equal(wantAt) {
					t.Fatal("revision/time incorrect", after.Candles)
				}
				changes := response["revised_candles"].([]any)
				detail := changes[0].(map[string]any)
				if len(changes) != 1 || !reflect.DeepEqual(detail["changed"], []any{"volume"}) {
					t.Fatal("changed fields incorrect", changes)
				}
				if err := store.View(ctx, run.RunKey, func(_ *d.Aggregate, history ports.History) error {
					events, err := history.Audit(0, 0)
					if err != nil {
						return err
					}
					var detail map[string]json.RawMessage
					json.Unmarshal(events[len(events)-1]["detail"], &detail)
					expected, _ := json.Marshal(response["revised_candles"])
					executionEqual(t, expected, json.RawMessage(detail["revised_candles"]))
					return nil
				}); err != nil {
					t.Fatal(err)
				}
			}
		})
	}
}

func TestCapacityAuditHistoryAndHealth(t *testing.T) {
	ctx := context.Background()
	run, p, _, _ := serviceBase(t)
	store := memory.New("SIM")
	if err := store.Create(ctx, run); err != nil {
		t.Fatal(err)
	}
	initialCount := int64(len(run.Audit))
	for i := 0; i < 5; i++ {
		if err := store.Transaction(ctx, run.RunKey, func(r *d.Aggregate) error {
			if len(r.Audit) != 0 || r.AuditCount != initialCount+int64(i) {
				t.Fatal("historical audit loaded", r.AuditCount)
			}
			a.Audit(r, "PROCESS_RESTART", p, "fixture", nil)
			return nil
		}); err != nil {
			t.Fatal(err)
		}
	}
	after, _ := store.Read(ctx, run.RunKey)
	encoded, _ := json.Marshal(after)
	var payload map[string]json.RawMessage
	json.Unmarshal(encoded, &payload)
	if payload["audit"] != nil || payload["AuditCount"] != nil || payload["audit_count"] != nil || len(after.Audit) != 0 || after.AuditCount != initialCount+5 {
		t.Fatal("aggregate audit not external", after.AuditCount)
	}
	clone, _ := after.Clone()
	if clone.AuditCount != after.AuditCount {
		t.Fatal("clone dropped count")
	}
	service := &a.Service{Store: store}
	if err := service.RecordRejection(ctx, p, "TEST_REJECTED", "POST"); err != nil {
		t.Fatal(err)
	}
	handler := api.New(service, map[string]d.Principal{"synthetic": p})
	offset := 0
	sequence := int64(0)
	for {
		page := capacityPage(t, handler, run.RunKey, "audit", url.Values{"limit": {"2"}, "cursor": {strconv.Itoa(offset)}})
		var events []d.Object
		json.Unmarshal(page["items"], &events)
		var version int64
		json.Unmarshal(page["snapshot_version"], &version)
		if version != run.Version || len(events) > 2 {
			t.Fatal("page snapshot/limit mismatch")
		}
		for _, event := range events {
			var number int64
			json.Unmarshal(event["sequence"], &number)
			sequence++
			if number != sequence {
				t.Fatal("noncontiguous sequence", number, sequence)
			}
		}
		if string(page["cursor"]) == "null" {
			break
		}
		var cursor string
		json.Unmarshal(page["cursor"], &cursor)
		offset, _ = strconv.Atoi(cursor)
	}
	if sequence != initialCount+6 {
		t.Fatal("lost audit rows", sequence)
	}
	page := capacityPage(t, handler, run.RunKey, "audit", nil)
	var events []d.Object
	json.Unmarshal(page["items"], &events)
	var requestID string
	json.Unmarshal(events[len(events)-1]["request_id"], &requestID)
	if requestID != "rejection-"+strconv.FormatInt(initialCount+5, 10) {
		t.Fatal("rejection ID reused", requestID)
	}
	capacityCode(t, store.Transaction(ctx, run.RunKey, func(r *d.Aggregate) error { r.AuditCount--; return nil }), "AUDIT_MUTATION_FORBIDDEN")
	capacityCode(t, store.Transaction(ctx, run.RunKey, func(r *d.Aggregate) error { r.Audit = append(r.Audit, events[0]); return nil }), "AUDIT_MUTATION_FORBIDDEN")
	if err := store.View(ctx, run.RunKey, func(_ *d.Aggregate, h ports.History) error {
		rows, err := h.Audit(0, 1)
		if err == nil {
			rows[0]["action"] = json.RawMessage(`"tampered"`)
		}
		return err
	}); err != nil {
		t.Fatal(err)
	}
	unchanged := capacityPage(t, handler, run.RunKey, "audit", nil)
	executionEqual(t, page["items"], json.RawMessage(unchanged["items"]))
	after, _ = store.Read(ctx, run.RunKey)
	after.Policy.Operational = &d.OperationalPolicy{MinDiskBytes: 1, MaxClockSkewSeconds: probeAmount(t, "1"), MaxAuditRecords: after.AuditCount, MaxPendingCommands: 100, MaxCommandAgeSeconds: 100, LeaseSeconds: 1}
	probe := &health.Probe{FreeBytes: func(string) (uint64, error) { return 100, nil }, ClockOffset: func(context.Context) (decimal.Value, error) { return decimal.Value{}, nil }}
	issues, err := probe.Check(ctx, after)
	if err != nil || !d.Has(issues, "AUDIT_RETENTION_CAPACITY") {
		t.Fatal("health ignored external audit", issues, err)
	}
}

func TestCapacityCommandDedupRetention(t *testing.T) {
	ctx := context.Background()
	run, p, command, _ := serviceBase(t)
	run.Dedup.Set("legacy", d.Object{"hash": json.RawMessage(`"old"`), "response": json.RawMessage(`{}`)})
	run.Dedup.Set("create", d.Object{"hash": json.RawMessage(`"create"`), "response": json.RawMessage(`{"run_key":{}}`)})
	store := memory.New("SIM")
	if err := store.Create(ctx, run); err != nil {
		t.Fatal(err)
	}
	service := &a.Service{Store: store}
	accepted, err := service.RunAction(ctx, p, command, "stop")
	if err != nil {
		t.Fatal(err)
	}
	after, _ := store.Read(ctx, run.RunKey)
	var retain time.Time
	json.Unmarshal(after.Dedup.Value(p.PrincipalID + ":" + command.IdempotencyKey)["retain_until"], &retain)
	if !retain.Equal(command.ExpiresAtUTC.Add(a.CommandDedupRetention)) {
		t.Fatal("new command retention incorrect", retain)
	}
	json.Unmarshal(after.Dedup.Value("legacy")["retain_until"], &retain)
	if !retain.Equal(run.Clock.Add(a.CommandDedupRetention)) {
		t.Fatal("legacy retention incorrect", retain)
	}
	if err := store.Transaction(ctx, run.RunKey, func(r *d.Aggregate) error { r.Clock = command.ExpiresAtUTC.Add(time.Hour); return nil }); err != nil {
		t.Fatal(err)
	}
	replay, err := service.RunAction(ctx, p, command, "stop")
	if err != nil {
		t.Fatal(err)
	}
	expected, _ := json.Marshal(accepted)
	executionEqual(t, expected, replay)
	at := command.ExpiresAtUTC.Add(a.CommandDedupRetention + time.Nanosecond)
	if _, err = capacitySnapshot(t, service, p, run.RunKey, at); err != nil {
		t.Fatal(err)
	}
	after, _ = store.Read(ctx, run.RunKey)
	if _, ok := after.Dedup.Get(p.PrincipalID + ":" + command.IdempotencyKey); ok {
		t.Fatal("expired run action retained")
	}
	if _, ok := after.Dedup.Get("legacy"); ok {
		t.Fatal("expired legacy retained")
	}
	if _, ok := after.Dedup.Get("create"); !ok {
		t.Fatal("create removed")
	}
	_, err = service.RunAction(ctx, p, command, "stop")
	capacityCode(t, err, "VERSION_CONFLICT")
	command.ExpectedVersion = after.Version
	_, err = service.RunAction(ctx, p, command, "stop")
	capacityCode(t, err, "REQUEST_EXPIRED")
}

func TestCapacityCandleArchivePaginationAndReplay(t *testing.T) {
	ctx := context.Background()
	run, p, command, _ := serviceBase(t)
	// Two roots straddle the retention boundary. Old revisions and a candle
	// accepted then immediately trimmed must still be available in history.
	old := capacityCandle(run, run.Clock.Add(-a.CandleRetention))
	old.Revision = 1
	recent := capacityCandle(run, old.CloseAt.Add(5*time.Minute))
	run.Candles = []d.Candle{old, recent}
	store := memory.New("SIM")
	if err := store.Create(ctx, run); err != nil {
		t.Fatal(err)
	}
	service := &a.Service{Store: store, Simulator: &sim.Broker{}}
	older := capacityCandle(run, old.CloseAt.Add(-5*time.Minute))
	revisions := []d.Candle{old, old}
	revisions[0].Revision = 2
	revisions[0].Volume, _ = decimal.Parse("101")
	revisions[1].Revision = 3
	revisions[1].Volume, _ = decimal.Parse("102")
	if _, err := capacitySnapshot(t, service, p, run.RunKey, run.Clock, append(revisions, older)...); err != nil {
		t.Fatal(err)
	}
	after, _ := store.Read(ctx, run.RunKey)
	if len(after.Candles) != 1 || !after.Candles[0].OpenAt.Equal(recent.OpenAt) {
		t.Fatal("retention boundary incorrect", after.Candles)
	}
	handler := api.New(service, map[string]d.Principal{"synthetic": p})
	query := url.Values{"venue": {old.InstrumentKey.Venue}, "instrument_id": {old.InstrumentKey.InstrumentID}, "interval": {old.Interval}, "start": {older.OpenAt.Format(time.RFC3339Nano)}, "end": {run.Clock.Format(time.RFC3339Nano)}, "limit": {"1"}}
	var candles []d.Candle
	for {
		page := capacityPage(t, handler, run.RunKey, "market/candles", query)
		var items []d.Candle
		json.Unmarshal(page["items"], &items)
		candles = append(candles, items...)
		if string(page["cursor"]) == "null" {
			break
		}
		var cursor string
		json.Unmarshal(page["cursor"], &cursor)
		query.Set("cursor", cursor)
	}
	if len(candles) != 3 || candles[1].Revision != 3 || !candles[2].OpenAt.Equal(recent.OpenAt) {
		t.Fatal("archive/aggregate pagination incorrect", candles)
	}
	// Replay still rejects a lower/different revision and honors explicit ones.
	command.ExpectedVersion = after.Version
	command.RequestID = "replay-conflict"
	command.IdempotencyKey = "replay-conflict"
	command.ExpiresAtUTC = run.Clock.Add(time.Hour)
	frame := d.ReplayFrame{At: run.Clock.Add(time.Minute), InstrumentKey: recent.InstrumentKey, Candle: &recent}
	frame.Candle = new(d.Candle)
	*frame.Candle = recent
	frame.Candle.Volume, _ = decimal.Parse("103")
	_, err := service.AdvanceReplay(ctx, p, command, frame)
	capacityCode(t, err, "CANDLE_REVISION_CONFLICT")
	frame.Candle.Revision = 1
	if _, err = service.AdvanceReplay(ctx, p, command, frame); err != nil {
		t.Fatal(err)
	}
	after, _ = store.Read(ctx, run.RunKey)
	if after.Candles[0].Revision != 1 || !after.Candles[0].AvailableAt.Equal(recent.AvailableAt) {
		t.Fatal("replay revision rule changed")
	}
	// Advancing the replay clock also trims the last retained candle.
	command.ExpectedVersion = after.Version
	command.RequestID = "replay-trim"
	command.IdempotencyKey = "replay-trim"
	frame.Candle = nil
	frame.At = run.Clock.Add(time.Hour)
	command.ExpiresAtUTC = frame.At.Add(time.Hour)
	if _, err = service.AdvanceReplay(ctx, p, command, frame); err != nil {
		t.Fatal(err)
	}
	after, _ = store.Read(ctx, run.RunKey)
	if len(after.Candles) != 0 {
		t.Fatal("replay did not trim candles")
	}
}

// A direct handler client exercises collector commands without opening a port.
type capacityAPI struct {
	handler       http.Handler
	registrations int
}

func (c *capacityAPI) Call(ctx context.Context, method, path string, q map[string][]string, body any) (json.RawMessage, error) {
	var data []byte
	if body != nil {
		data, _ = json.Marshal(body)
	}
	request := httptest.NewRequest(method, path+"?"+url.Values(q).Encode(), nil).WithContext(ctx)
	if body != nil {
		request.Body = http.NoBody
		request.Body = io.NopCloser(bytes.NewReader(data))
		request.Header.Set("Content-Type", "application/json")
	}
	request.Header.Set("Authorization", "Bearer synthetic")
	recorder := httptest.NewRecorder()
	c.handler.ServeHTTP(recorder, request)
	if method == "POST" && path == api.Prefix+"/instruments" {
		c.registrations++
	}
	if recorder.Code >= 400 {
		var problem struct{ Code string }
		json.Unmarshal(recorder.Body.Bytes(), &problem)
		return nil, &d.Error{Code: problem.Code, Status: recorder.Code}
	}
	return recorder.Body.Bytes(), nil
}

type capacityMarket struct {
	spec   *d.InstrumentSpec
	points []*d.MarketPoint
}

func (m *capacityMarket) InstrumentSpecs(context.Context) ([]*d.InstrumentSpec, error) {
	return []*d.InstrumentSpec{m.spec}, nil
}
func (m *capacityMarket) LatestPoints(context.Context, d.InstrumentKey) ([]*d.MarketPoint, error) {
	points := []*d.MarketPoint{}
	for _, point := range m.points {
		copy := *point
		copy.SpecVersion = m.spec.Version
		points = append(points, &copy)
	}
	return points, nil
}
func (m *capacityMarket) Trades(context.Context, d.InstrumentKey, int) ([]*d.MarketTrade, error) {
	return []*d.MarketTrade{}, nil
}
func (m *capacityMarket) Candles(context.Context, d.InstrumentKey, string, time.Time, time.Time) ([]d.Candle, error) {
	return []d.Candle{}, nil
}
func TestCapacityCollectorSkipsUnchangedRules(t *testing.T) {
	ctx := context.Background()
	run, p, _, _ := serviceBase(t)
	spec := *run.Specs.Values()[0]
	store := memory.New("SIM")
	if err := store.Create(ctx, run); err != nil {
		t.Fatal(err)
	}
	client := &capacityAPI{handler: api.New(&a.Service{Store: store}, map[string]d.Principal{"synthetic": p})}
	market := &capacityMarket{spec: &spec, points: run.Points.Values()}
	collector := &workers.Collector{Client: client, Market: market, Key: run.RunKey, Now: func() time.Time { return run.Clock }}
	if _, _, err := collector.Collect(ctx, spec.Key.InstrumentID, "5m", run.Clock.Add(-time.Hour), run.Clock); err != nil {
		t.Fatal(err)
	}
	if client.registrations != 0 {
		t.Fatal("unchanged rules registered")
	}
	before, _ := store.Read(ctx, run.RunKey)
	if before.Version != run.Version+1 {
		t.Fatal("extra command submitted")
	}
	spec.Version = "capacity-version"
	if _, _, err := collector.Collect(ctx, spec.Key.InstrumentID, "5m", run.Clock.Add(-time.Hour), run.Clock); err != nil {
		t.Fatal(err)
	}
	if client.registrations != 1 {
		t.Fatal("changed rules not registered")
	}
	spec.PriceTick, _ = decimal.Parse("2")
	_, _, err := collector.Collect(ctx, spec.Key.InstrumentID, "5m", run.Clock.Add(-time.Hour), run.Clock)
	capacityCode(t, err, "COLLECTOR_RULES_CONFLICT")
}

func TestCapacityPendingAuditAndLongRunRetention(t *testing.T) {
	ctx := context.Background()
	run, p, _, _ := serviceBase(t)
	store := memory.New("SIM")
	if err := store.Create(ctx, run); err != nil {
		t.Fatal(err)
	}
	if err := store.Transaction(ctx, run.RunKey, func(r *d.Aggregate) error {
		a.Audit(r, "PROCESS_RESTART", p, "first", nil)
		a.Audit(r, "PROCESS_RESTART", p, "second", nil)
		for i, event := range r.Audit {
			var sequence int64
			if json.Unmarshal(event["sequence"], &sequence) != nil || sequence != r.AuditCount+int64(i)+1 {
				t.Fatal("pending audit sequence incorrect")
			}
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	service := &a.Service{Store: store}
	for i := 1; i <= 200; i++ {
		at := run.Clock.Add(time.Duration(i) * time.Hour)
		candle := capacityCandle(run, at)
		candle.Interval = "1h"
		candle.OpenAt = at.Add(-time.Hour)
		if _, err := capacitySnapshot(t, service, p, run.RunKey, at, candle); err != nil {
			t.Fatal(err)
		}
	}
	after, err := store.Read(ctx, run.RunKey)
	if err != nil {
		t.Fatal(err)
	}
	if len(after.Candles) != 168 || after.Dedup.Len() > 28 || len(after.Audit) != 0 || after.AuditCount != int64(len(run.Audit))+202 {
		t.Fatal("long run state grew outside retention", len(after.Candles), after.Dedup.Len(), after.AuditCount)
	}
	// CreateRun's original response remains replayable after all other records
	// have expired and the run clock has advanced by a month.
	fresh := memory.New("SIM")
	creator := &a.Service{Store: fresh}
	body := d.CreateRun{SchemaVersion: "trading-2.0", RequestID: "create-permanent", IdempotencyKey: "create-permanent", RunKey: run.RunKey, Reason: "fixture", ExpiresAtUTC: run.Clock.Add(time.Minute), ExecutionMode: run.ExecutionMode, InitialCash: run.InitialCash, Currency: run.Currency, Clock: run.Clock, AccountPolicy: run.Policy, SimConfig: run.SimConfig}
	accepted, err := creator.CreateRun(ctx, p, body)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := capacitySnapshot(t, creator, p, run.RunKey, run.Clock.Add(31*24*time.Hour)); err != nil {
		t.Fatal(err)
	}
	replay, err := creator.CreateRun(ctx, p, body)
	if err != nil {
		t.Fatal(err)
	}
	expected, _ := json.Marshal(accepted)
	executionEqual(t, expected, replay)
}
