package strategy_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/AceNanako0721/Factorforge/src/factorforge/strategy/adapters"
	app "github.com/AceNanako0721/Factorforge/src/factorforge/strategy/application"
	sd "github.com/AceNanako0721/Factorforge/src/factorforge/strategy/domain"
	"github.com/AceNanako0721/Factorforge/src/factorforge/strategy/entrypoints/assembly"
	"github.com/AceNanako0721/Factorforge/src/factorforge/strategy/workers"
)

func TestP2WorkloadRetriesOnlySnapshotConflicts(t *testing.T) {
	for _, code := range []string{"CYCLE_SNAPSHOT_CONFLICT", "FEEDBACK_SNAPSHOT_CONFLICT", "TRADING_SNAPSHOT_CONFLICT"} {
		for _, successes := range []int{1, 2, 3} {
			t.Run(fmt.Sprintf("%s-%d", code, successes), func(t *testing.T) {
				calls, waits := 0, 0
				var stderr bytes.Buffer
				err := assembly.RetryTick(context.Background(), func(context.Context) error {
					calls++
					if calls <= successes {
						return fmt.Errorf("wrapped: %w", &sd.Error{Code: code, Status: 409})
					}
					return nil
				}, &stderr, func(_ context.Context, delay time.Duration) error {
					waits++
					if delay < 200*time.Millisecond || delay > 800*time.Millisecond {
						t.Fatal("jitter outside design bounds", delay)
					}
					return nil
				})
				if calls != min(successes+1, 3) || waits != min(successes, 2) {
					t.Fatal("retry count", calls, waits)
				}
				if successes == 3 {
					assertStrategyCode(t, err, code)
					if stderr.Len() != 0 {
						t.Fatal("intermediate failure was logged", stderr.String())
					}
				} else if err != nil || stderr.String() != fmt.Sprintf("RETRIED %s %d\n", code, successes) {
					t.Fatal("retry success", err, stderr.String())
				}
			})
		}
	}
	calls := 0
	err := assembly.RetryTick(context.Background(), func(context.Context) error {
		calls++
		return &sd.Error{Code: "STORE_UNAVAILABLE", Status: 503}
	}, &bytes.Buffer{}, func(context.Context, time.Duration) error { t.Fatal("non-conflict waited"); return nil })
	assertStrategyCode(t, err, "STORE_UNAVAILABLE")
	if calls != 1 {
		t.Fatal("non-conflict retried", calls)
	}
	ctx, cancel := context.WithCancel(context.Background())
	err = assembly.RetryTick(ctx, func(context.Context) error { return &sd.Error{Code: "CYCLE_SNAPSHOT_CONFLICT", Status: 409} }, &bytes.Buffer{}, func(context.Context, time.Duration) error { cancel(); return ctx.Err() })
	if err != context.Canceled {
		t.Fatal("cancelled retry continued", err)
	}
}

func TestP2AuditSequenceAppendOnlyAndPagedHistory(t *testing.T) {
	ctx := context.Background()
	state, _, create := strategyBase(t)
	obj := create.Object
	state.Objects.Set(obj.ObjectID, &obj)
	state.Candidates.Set("candidate", map[string]any{"object_id": obj.ObjectID, "parent_version": "p0", "activated_version": "p1"})
	for i := 0; i < 5101; i++ {
		state.AppendAudit(map[string]any{"action": "CYCLE_COMMITTED", "at": sd.ISO(create.Parameters.ValidFrom)})
	}
	state.AppendAudit(map[string]any{"action": "PARAMETER_ACTIVATED", "candidate_id": "candidate", "at": sd.ISO(create.Parameters.ValidFrom)})
	store := adapters.NewMemory(state)
	loaded, err := store.Read(ctx, state.InstanceID)
	if err != nil || loaded.AuditCount != 5102 || len(loaded.Audit) != 0 {
		t.Fatal("history loaded into aggregate", err)
	}
	raw, _ := sd.Marshal(loaded)
	if bytes.Contains(raw, []byte("AuditCount")) || bytes.Contains(raw, []byte("audit_count")) {
		t.Fatal("nonserialized count persisted")
	}
	if sd.CloneState(loaded).AuditCount != loaded.AuditCount {
		t.Fatal("count lost on transaction clone")
	}
	assertStrategyCode(t, store.Transaction(ctx, state.InstanceID, func(s *sd.StrategyState) error { s.AuditCount--; return nil }), "APPEND_ONLY_MUTATION_FORBIDDEN")
	assertStrategyCode(t, store.Transaction(ctx, state.InstanceID, func(s *sd.StrategyState) error {
		s.Audit = []map[string]any{{"sequence": 1, "action": "rewritten"}}
		return nil
	}), "APPEND_ONLY_MUTATION_FORBIDDEN")
	buffer := sd.NewState("buffer", "SIM")
	buffer.AuditCount = 10
	buffer.AppendAudit(map[string]any{"action": "first"})
	for _, mutate := range []func(*sd.StrategyState){func(s *sd.StrategyState) { s.Audit = nil }, func(s *sd.StrategyState) { s.Audit[0]["action"] = "rewritten" }} {
		copy := sd.CloneState(buffer)
		mutate(copy)
		assertStrategyCode(t, sd.CheckAuditAppend(buffer, copy), "APPEND_ONLY_MUTATION_FORBIDDEN")
	}
	if err = store.Transaction(ctx, state.InstanceID, func(s *sd.StrategyState) error {
		s.AppendAudit(map[string]any{"action": "next"})
		s.AppendAudit(map[string]any{"action": "last"})
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	rows, count, err := store.ReadAudit(ctx, state.InstanceID, 5102, 5104, 10, nil)
	if err != nil || count != 5104 || len(rows) != 2 || rows[0].Sequence != 5103 || sd.Number(rows[1].Payload["sequence"]) != 5104 {
		t.Fatal("non-contiguous audit", err, count, rows)
	}
	rows[0].Payload["action"] = "outside mutation"
	rows, _, _ = store.ReadAudit(ctx, state.InstanceID, 5102, 5104, 1, nil)
	if sd.Text(rows[0].Payload["action"]) != "next" {
		t.Fatal("read mutated durable history")
	}
	q := app.QueryService{Store: store, Clock: adapters.NewReplay(create.Parameters.ValidFrom), Policy: app.ReadPolicy{DefaultLimit: 73, MaxLimit: 100, MaxRecords: 5000, CursorAge: time.Hour, CursorKey: []byte("fixture-key")}}
	identity := sd.PublicPrincipal{PrincipalID: "reader", InstanceID: state.InstanceID, Environment: "SIM", Scopes: []sd.ApiScope{sd.Query}}
	filter := app.ReadFilter{}
	seen := 0
	for {
		page, err := q.Trace(ctx, identity, "audit", "", filter)
		if err != nil || len(page.Items) > 73 {
			t.Fatal("paged audit unavailable above 5000", err)
		}
		for _, row := range page.Items {
			if row["source_record_id"] != fmt.Sprintf("audit:%d", seen) {
				t.Fatal("old slice index changed", row, seen)
			}
			seen++
		}
		if page.Cursor == nil {
			break
		}
		filter.Cursor = *page.Cursor
	}
	if seen != 5104 {
		t.Fatal("paging lost rows", seen)
	}
	page, err := q.Trace(ctx, identity, "parameter-activations", "", app.ReadFilter{})
	if err != nil || len(page.Items) != 1 || page.Items[0]["source_record_id"] != "audit:5101" {
		t.Fatal("irrelevant audits blocked activations", err, page)
	}
	// An append after loading a snapshot does not enter its high-water mark.
	rows, count, err = store.ReadAudit(ctx, state.InstanceID, 5101, 5102, 10, nil)
	if err != nil || count != 5102 || len(rows) != 1 || rows[0].Sequence != 5102 {
		t.Fatal("audit snapshot drift", err, rows)
	}
}

func TestP2TrendWindowAndPolicyAdmission(t *testing.T) {
	_, _, create := strategyBase(t)
	for _, value := range []any{create.Policy.CycleSeconds - 1, 604801, 1.5, "300", nil} {
		payload := sd.Map(create.Policy)
		payload["trend_window_seconds"] = value
		raw, _ := json.Marshal(payload)
		var policy sd.Policy
		assertStrategyCode(t, sd.DecodeJSON(raw, &policy), "INVALID_REQUEST")
	}
	for _, value := range []int{create.Policy.CycleSeconds, 604800} {
		payload := sd.Map(create.Policy)
		payload["trend_window_seconds"] = value
		var policy sd.Policy
		decodeRecord(t, payload, &policy)
		if policy.TrendWindowSeconds == nil || *policy.TrendWindowSeconds != value {
			t.Fatal("window lost on decoding")
		}
	}
	at := time.Date(2026, 10, 1, 2, 0, 0, 0, time.UTC)
	policy := create.Policy
	policy.TrendWindowSeconds = sd.Ptr(3600)
	manifest := map[string]any{"trend": map[string]any{"validated": true, "purpose": "trend", "available_at": sd.ISO(at.Add(-3 * time.Hour)), "missing_policy": "UNKNOWN", "validation_manifest": "fixture-only", "dependencies": []string{}}}
	samples := []sd.MarketSample{
		{AvailableAt: at.Add(-2 * time.Hour), Price: d("50"), Quality: "VALID"},
		{AvailableAt: at.Add(-time.Hour), Price: d("100"), Quality: "VALID"},
		{AvailableAt: at.Add(-time.Hour + time.Second), Price: d("105"), Quality: "VALID"},
		{AvailableAt: at, Price: d("110"), Sigma: sd.Ptr(d("0.01")), Quality: "VALID"},
		{AvailableAt: at.Add(time.Second), Price: d("1000"), Sigma: sd.Ptr(d("0.01")), Quality: "VALID"},
	}
	result, err := sd.UpdateRegime(nil, samples, at, &policy, manifest)
	m := sd.Math()
	expected := m.Div(m.Ln(d("1.1")), d("0.01"))
	if err != nil || sd.Amount(sd.Object(result["trend"])["metric"]).Cmp(expected) != 0 {
		t.Fatal("trend baseline is not the latest valid point at window cutoff", err, result)
	}
	policy.TrendWindowSeconds = nil
	result, err = sd.UpdateRegime(nil, samples, at, &policy, manifest)
	if err != nil || sd.Text(sd.Object(result["trend"])["reason"]) != "TREND_WINDOW_UNDEFINED" {
		t.Fatal("unregistered trend window used", err, result)
	}
	policy.TrendWindowSeconds = sd.Ptr(3600)
	result, err = sd.UpdateRegime(nil, samples[2:], at, &policy, manifest)
	if err != nil || sd.Text(sd.Object(result["trend"])["reason"]) != "FACTOR_MISSING" {
		t.Fatal("missing baseline manufactured", err, result)
	}
}

func TestP2SampleRetentionPreservesCausalInputsAndCaps(t *testing.T) {
	at := time.Date(2026, 10, 1, 10, 0, 0, 0, time.UTC)
	p := &sd.Policy{CycleSeconds: 300, TrendWindowSeconds: sd.Ptr(600)}
	build := func() *sd.StrategyState {
		s := sd.NewState("samples", "SIM")
		for i := 0; i <= 20; i++ {
			s.Samples.Set("o", append(s.Samples.Value("o"), sd.MarketSample{AvailableAt: at.Add(time.Duration(i-20) * time.Minute), Price: d("100"), Quality: "VALID"}))
		}
		return s
	}
	s := build()
	sd.RetainSamples(s, "o", at, p)
	if rows := s.Samples.Value("o"); len(rows) != 12 || !rows[0].AvailableAt.Equal(at.Add(-11*time.Minute)) {
		t.Fatal("cutoff predecessor lost", rows)
	}
	for _, window := range []*int{nil, sd.Ptr(300)} {
		s = build()
		sd.RetainSamples(s, "o", at, &sd.Policy{CycleSeconds: 300, TrendWindowSeconds: window})
		if len(s.Samples.Value("o")) != 12 {
			t.Fatal("two-cycle minimum window was lost")
		}
	}
	for _, status := range []string{"OPEN", "OBSERVING"} {
		s = build()
		s.Cases.Set("case", &sd.CaseRecord{ObjectID: "o", Status: status, EntryAt: at.Add(-13 * time.Minute), EntrySnapshot: map[string]any{"source_contributions": []map[string]any{{"effective_at": sd.ISO(at.Add(-12 * time.Minute))}, {"effective_at": sd.ISO(at.Add(-17 * time.Minute))}}}})
		sd.RetainSamples(s, "o", at, p)
		if rows := s.Samples.Value("o"); len(rows) != 19 || !rows[0].AvailableAt.Equal(at.Add(-18*time.Minute)) {
			t.Fatal("unfinished case tau lost", status, rows)
		}
	}
	s = build()
	s.Cases.Set("case", &sd.CaseRecord{ObjectID: "o", Status: "OPEN", EntryAt: at.Add(-19 * time.Minute), EntrySnapshot: map[string]any{"source_contributions": []map[string]any{{"effective_at": sd.ISO(at.Add(-12 * time.Minute))}}}})
	sd.RetainSamples(s, "o", at, p)
	if len(s.Samples.Value("o")) != 21 {
		t.Fatal("case entry before tau was not retained")
	}
	s = build()
	s.Scores.Set("score", &sd.ScoreSubmission{ObjectID: "o"})
	s.Receipts.Set("score", &sd.AdmissionReceipt{State: "READY_PENDING_PRICE", EligibleFrom: sd.Ptr(at.Add(-15 * time.Minute))})
	sd.RetainSamples(s, "o", at, p)
	if len(s.Samples.Value("o")) != 17 || sd.PriceAt(s.Samples.Value("o"), at.Add(-15*time.Minute)) == nil {
		t.Fatal("pending injection inputs lost")
	}
	s.Receipts.Value("score").State = "INJECTED"
	sd.RetainSamples(s, "o", at, p)
	if len(s.Samples.Value("o")) != 12 {
		t.Fatal("injected receipt kept obsolete history")
	}
	s = build()
	s.Cases.Set("case", &sd.CaseRecord{ObjectID: "o", Status: "OPEN", EntryAt: at.Add(-24 * time.Hour)})
	rows := []sd.MarketSample{}
	for i := 0; i < sd.MaxSamplesPerObject+100; i++ {
		rows = append(rows, sd.MarketSample{AvailableAt: at.Add(time.Duration(i-5200) * time.Second), Price: d("100"), Quality: "VALID"})
	}
	s.Samples.Set("o", rows)
	sd.RetainSamples(s, "o", at, p)
	if got := s.Samples.Value("o"); len(got) != sd.MaxSamplesPerObject || !got[0].AvailableAt.Equal(rows[100].AvailableAt) {
		t.Fatal("sample hard cap ineffective")
	}
	c := &sd.CaseRecord{Status: "OBSERVING", ExitAt: sd.Ptr(at.Add(-time.Hour)), ObservationEnd: &at, EntrySnapshot: map[string]any{"cost_known": true, "source_contributions": []map[string]any{{"effective_at": sd.ISO(rows[0].AvailableAt)}}}}
	if err := sd.LabelCase(c, s.Samples.Value("o"), at, &sd.Policy{CycleSeconds: 300, LabelSeconds: 300}, false); err != nil || c.Status != "CENSORED" {
		t.Fatal("trimmed case did not censor naturally", err, c.Status)
	}
}

type capacityTrading struct {
	clock     *adapters.ReplayClock
	template  *sd.TradingSnapshot
	snapshots int
	onRead    func()
}

func (f *capacityTrading) Snapshot(context.Context, []*sd.ObservedObject) (*sd.TradingSnapshot, error) {
	f.snapshots++
	if f.onRead != nil {
		f.onRead()
	}
	snap := sd.Clone(f.template)
	snap.At = f.clock.Now()
	for id, sample := range snap.Samples {
		sample.AvailableAt = snap.At
		snap.Samples[id] = sample
	}
	return snap, nil
}
func (f *capacityTrading) Prepare(obj *sd.ObservedObject, item *sd.TargetOutbox, snap *sd.TradingSnapshot) (map[string]any, error) {
	return (&adapters.TradingV2{}).Prepare(obj, item, snap)
}
func (f *capacityTrading) Deliver(context.Context, *sd.ObservedObject, *sd.TargetOutbox, *sd.TradingSnapshot) (map[string]any, error) {
	return map[string]any{}, nil
}
func (f *capacityTrading) Simulate(context.Context, map[string]any) (map[string]any, error) {
	return nil, fmt.Errorf("unused")
}

func capacityBase(t *testing.T) (*adapters.MemoryStore, sd.WorkloadIdentity, *capacityTrading) {
	t.Helper()
	state, identity, create := strategyBase(t)
	_, cases := loadReplay(t)
	var snapshot sd.TradingSnapshot
	found := false
	for _, item := range cases {
		for _, call := range sd.Rows(item["trading"]) {
			if sd.Text(call["method"]) == "snapshot" && sd.Object(sd.Object(call["result"])["samples"])[create.Object.ObjectID] != nil {
				decodeRecord(t, call["result"], &snapshot)
				found = true
				break
			}
		}
		if found {
			break
		}
	}
	if !found {
		t.Fatal("frozen snapshot missing")
	}
	create.Policy.CycleSeconds, create.Policy.TargetTTLSeconds = 300, 600
	create.Policy.TrendWindowSeconds = sd.Ptr(86400) // synthetic capacity policy, not production calibration
	state.Objects.Set(create.Object.ObjectID, &create.Object)
	state.Policies.Set(create.Policy.Version, &create.Policy)
	state.Parameters.Set(create.Parameters.Version, &create.Parameters)
	return adapters.NewMemory(state), identity, &capacityTrading{clock: adapters.NewReplay(snapshot.At), template: &snapshot}
}

func TestP2RetryRereadsWorkerSnapshots(t *testing.T) {
	for _, feedback := range []bool{false, true} {
		store, identity, trading := capacityBase(t)
		trading.onRead = func() {
			if trading.snapshots == 1 {
				if err := store.Transaction(context.Background(), identity.InstanceID, func(s *sd.StrategyState) error { s.Version++; return nil }); err != nil {
					t.Fatal(err)
				}
			}
		}
		cycle := app.DecisionCycle{Store: store, Trading: trading, Clock: trading.clock}
		var tick func(context.Context) error
		if feedback {
			tick = (workers.Feedback{Cycle: cycle, Identity: identity}).Tick
		} else {
			tick = func(ctx context.Context) error {
				_, err := (workers.Scheduler{Cycle: cycle, Identity: identity}).Tick(ctx)
				return err
			}
		}
		var stderr bytes.Buffer
		if err := assembly.RetryTick(context.Background(), tick, &stderr, func(context.Context, time.Duration) error { return nil }); err != nil {
			t.Fatal("fresh snapshot retry failed", feedback, err)
		}
		code := "CYCLE_SNAPSHOT_CONFLICT"
		if feedback {
			code = "FEEDBACK_SNAPSHOT_CONFLICT"
		}
		if trading.snapshots < 2 || !strings.Contains(stderr.String(), "RETRIED "+code+" 1") {
			t.Fatal("worker reused snapshot", feedback, trading.snapshots, stderr.String())
		}
	}
}

func TestP2TargetProjectionPreservesDeliveryAndSixHourArchive(t *testing.T) {
	store, identity, trading := capacityBase(t)
	ctx := context.Background()
	cycle := app.DecisionCycle{Store: store, Trading: trading, Clock: trading.clock}
	if _, err := cycle.Tick(ctx, identity, nil); err != nil {
		t.Fatal(err)
	}
	s, _ := store.Read(ctx, identity.InstanceID)
	if s.Outbox.Len() == 0 {
		t.Fatal("capacity scenario did not create targets")
	}
	for _, item := range s.Outbox.Values() {
		decision := s.Decisions.Value(item.Decision.DecisionID)
		if len(item.Decision.InputSnapshot) != 0 || len(decision.InputSnapshot) == 0 || item.Decision.InputHash != decision.InputHash {
			t.Fatal("target input copy was not removed")
		}
		full := sd.Clone(item)
		full.Decision = *decision
		obj := s.Objects.Value(decision.ObjectID)
		a, err := trading.Prepare(obj, full, trading.template)
		if err != nil {
			t.Fatal(err)
		}
		b, err := trading.Prepare(obj, item, trading.template)
		if err != nil || sd.Digest(a) != sd.Digest(b) {
			t.Fatal("P1 delivery command changed", err)
		}
		item.State, item.ExpiresAt = "ACK", decision.AvailableCutoff
		copy := sd.Clone(decision)
		copy.DecisionID, copy.AvailableCutoff = "new", decision.AvailableCutoff.Add(6*time.Hour)
		s.Decisions.Set("new", copy)
		if len(sd.ArchivableDecisions(s, copy.AvailableCutoff)) != 0 || len(sd.ArchivableDecisions(s, copy.AvailableCutoff.Add(time.Second))) != 1 || sd.DecisionRetention != 6*time.Hour {
			t.Fatal("six-hour archive boundary changed")
		}
	}
}

func TestP2SevenDayCapacityAtFiveMinuteCycles(t *testing.T) {
	store, identity, trading := capacityBase(t)
	cycle := app.DecisionCycle{Store: store, Trading: trading, Clock: trading.clock}
	ctx := context.Background()
	start := trading.clock.Now()
	maximum := 0
	for i := 0; i <= 7*24*12; i++ {
		if err := trading.clock.Advance(start.Add(time.Duration(i) * 5 * time.Minute)); err != nil {
			t.Fatal(err)
		}
		if _, err := cycle.Tick(ctx, identity, nil); err != nil {
			t.Fatal("cycle", i, err)
		}
		if err := cycle.Dispatch(ctx, identity); err != nil {
			t.Fatal("dispatch", i, err)
		}
		state, err := store.Read(ctx, identity.InstanceID)
		if err != nil {
			t.Fatal(err)
		}
		// Full aggregate encoding includes the public fields and ledger too,
		// so this is stricter than the internal-state partition size check.
		raw, _ := sd.Marshal(state)
		maximum = max(maximum, len(raw))
		if len(raw) >= 1<<20 || state.Decisions.Len() > 75 || state.Outbox.Len() > 75 || len(state.Audit) != 0 {
			t.Fatal("state exceeded capacity", i, len(raw), state.Decisions.Len(), state.Outbox.Len())
		}
	}
	state, _ := store.Read(ctx, identity.InstanceID)
	if state.AuditCount < 2016 || len(state.Samples.Value(identity.ObjectIDs[0])) > 290 {
		t.Fatal("seven days did not exercise history retention", state.AuditCount)
	}
	t.Logf("7 days, 2017 cycles: maximum aggregate bytes=%d, decisions=%d, samples=%d, audit rows=%d", maximum, state.Decisions.Len(), len(state.Samples.Value(identity.ObjectIDs[0])), state.AuditCount)
}
