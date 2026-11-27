package soxl_test

import (
	"context"
	"encoding/json"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/AceNanako0721/Factorforge/src/factorforge/applications/soxl/config"
	d "github.com/AceNanako0721/Factorforge/src/factorforge/applications/soxl/domain"
	"github.com/AceNanako0721/Factorforge/src/factorforge/applications/soxl/entrypoints/assembly"
	"github.com/AceNanako0721/Factorforge/src/factorforge/applications/soxl/submission"
	"github.com/AceNanako0721/Factorforge/src/factorforge/applications/soxl/workers"
	dto "github.com/AceNanako0721/Factorforge/src/factorforge/strategy/api/dto"
)

// ---------- T3-34: re-entry after a stop (S3-036) ----------

func TestT334ReentryInjectionDecision(t *testing.T) {
	at := time.Date(2026, 11, 24, 1, 1, 0, 0, time.UTC)
	stop := at.Add(10 * time.Minute)
	wait := d.ReentryWait{Direction: -1, StoppedAt: stop}
	reasons := func(a []d.InjectionAction) string {
		out := []string{}
		for _, x := range a {
			out = append(out, x.Kind+":"+x.Reason)
		}
		return strings.Join(out, ",")
	}
	s, _ := d.DecideInjection(d.InjectionState{}, judgment("j1", at, "SHORT", 3, []string{"n1"}, false))

	// Without a closed gate the rule changes nothing: a weaker same-direction
	// view is only recorded.
	if _, a := d.DecideInjectionAt(s, judgment("j2", stop.Add(20*time.Minute), "SHORT", 2, []string{"n1"}, false), d.ReentryWait{}); len(a) != 0 {
		t.Fatal("no gate injected", reasons(a))
	}
	// A view before the stop does not confirm it.
	if s2, a := d.DecideInjectionAt(s, judgment("j2", stop.Add(-time.Minute), "SHORT", 2, []string{"n1"}, false), wait); len(a) != 0 || s2.ReentryFor != nil {
		t.Fatal("pre-stop view confirmed", reasons(a))
	}
	// FLAT or opposite views do not confirm.
	for _, dir := range []string{"FLAT", "LONG"} {
		if _, a := d.DecideInjectionAt(s, judgment("j3", stop.Add(20*time.Minute), dir, 4, []string{"n7"}, false), wait); slices.ContainsFunc(a, func(x d.InjectionAction) bool { return x.Reason == "REENTRY_CONFIRM" }) {
			t.Fatal("non-confirming view", dir, reasons(a))
		}
	}
	// The first same-direction view after the stop is injected into the
	// current chain whatever its confidence and items.
	s4, a := d.DecideInjectionAt(s, judgment("j4", stop.Add(20*time.Minute), "SHORT", 2, []string{"n1"}, false), wait)
	if reasons(a) != "INJECT:REENTRY_CONFIRM" || a[0].ChainID != s.Active.ChainID || a[0].Confidence != 2 || s4.ReentryFor == nil || !s4.ReentryFor.Equal(stop) || s4.Active.Confidence != 3 || len(s4.Active.EventIDs) != 2 {
		t.Fatal("re-entry confirm", reasons(a), s4.ReentryFor)
	}
	// One stop is confirmed once.
	if _, a := d.DecideInjectionAt(s4, judgment("j5", stop.Add(50*time.Minute), "SHORT", 2, []string{"n1"}, false), wait); len(a) != 0 {
		t.Fatal("confirmed twice", reasons(a))
	}
	// A later stop is confirmed again.
	later := d.ReentryWait{Direction: -1, StoppedAt: stop.Add(3 * time.Hour)}
	if _, a := d.DecideInjectionAt(s4, judgment("j6", later.StoppedAt.Add(time.Minute), "SHORT", 1, []string{"n1"}, false), later); reasons(a) != "REVISE:CONFIDENCE_DOWN,INJECT:REENTRY_CONFIRM" {
		t.Fatal("later stop", reasons(a))
	}
	// A view S3-025 injects anyway is not injected twice; it confirms the stop.
	if s7, a := d.DecideInjectionAt(s, judgment("j7", stop.Add(20*time.Minute), "SHORT", 3, []string{"n1", "n2"}, false), wait); reasons(a) != "INJECT:NEW_ITEMS" || s7.ReentryFor == nil {
		t.Fatal("new items", reasons(a))
	}
	// A stopped direction the chain no longer holds is not confirmed.
	if _, a := d.DecideInjectionAt(s, judgment("j8", stop.Add(20*time.Minute), "LONG", 2, []string{"n1"}, false), d.ReentryWait{Direction: 1, StoppedAt: stop}); len(a) != 0 {
		t.Fatal("chain direction", reasons(a))
	}
}

type fakeObjects struct {
	object *dto.ObservedObject
	calls  int
}

func (f *fakeObjects) ObjectState(context.Context, string) (*dto.ObservedObject, error) {
	f.calls++
	return f.object, nil
}

func TestT334FollowerConfirmsReentry(t *testing.T) {
	at := time.Date(2026, 11, 24, 1, 1, 5, 0, time.UTC)
	w, _, fw, _ := judgmentWorker(at, fakeModel{err: d.Fail("MODEL_ACCESS_FAILED", 503)}, nil)
	w.Models, w.News = nil, nil
	item := d.NewsItem{ItemID: "item0000000001", Source: "cnbc-tech", Title: "Chip demand slows", PublishedAt: at.Add(-40 * time.Minute), ReceivedAt: at, LicenseScope: "RESEARCH_SIM"}
	first := judgment("j-20261124T010105Z", at, "SHORT", 3, []string{item.ItemID}, false)
	weaker := judgment("j-20261124T020105Z", at.Add(time.Hour), "SHORT", 2, []string{item.ItemID}, false)
	src := &fakeSource{rows: []d.SourcedJudgment{{Judgment: first, Items: []d.NewsItem{item}}}}
	w.Source, w.FollowSince = src, at.Add(-time.Minute)
	objects := &fakeObjects{object: &dto.ObservedObject{}}
	w.Objects = objects
	w.Settings.ReentryRule = d.ReentrySameDirection
	if err := w.Follow(context.Background()); err != nil || len(fw.scores) != 1 {
		t.Fatal("first", err, len(fw.scores))
	}
	// Stopped short at 01:30; the gate is closed when the weaker view arrives.
	objects.object = &dto.ObservedObject{Reentry: &dto.ReentryState{Direction: -1, StoppedAt: at.Add(30 * time.Minute)}}
	src.rows = append(src.rows, d.SourcedJudgment{Judgment: weaker, Items: []d.NewsItem{item}})
	if err := w.Follow(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(fw.scores) != 2 || fw.scores[1].Score.Vector.Direction != -1 || fw.scores[1].Score.Vector.ImpactPoints.String() != "0.45" {
		t.Fatal("re-entry confirm", len(fw.scores))
	}
	// Under the default rule the gate is never read and the weaker view is
	// only recorded.
	w2, _, fw2, _ := judgmentWorker(at, fakeModel{err: d.Fail("MODEL_ACCESS_FAILED", 503)}, nil)
	w2.Models, w2.News = nil, nil
	w2.Source, w2.FollowSince = &fakeSource{rows: src.rows}, at.Add(-time.Minute)
	other := &fakeObjects{object: objects.object}
	w2.Objects = other
	if err := w2.Follow(context.Background()); err != nil || len(fw2.scores) != 1 || other.calls != 0 {
		t.Fatal("default rule", err, len(fw2.scores), other.calls)
	}
	// SAME_DIRECTION without an object reader is a configuration error.
	w3, _, _, _ := judgmentWorker(at, fakeModel{err: d.Fail("MODEL_ACCESS_FAILED", 503)}, nil)
	w3.Models, w3.News = nil, nil
	w3.Source, w3.FollowSince = &fakeSource{rows: src.rows}, at.Add(-time.Minute)
	w3.Settings.ReentryRule = d.ReentrySameDirection
	if err := w3.Follow(context.Background()); err == nil {
		t.Fatal("missing object reader accepted")
	}
}

// A REENTRY gate decides only the batch on the side a stop closed.
func TestT334PositionManagerReentryGate(t *testing.T) {
	at := time.Date(2026, 10, 15, 15, 0, 5, 0, time.UTC)
	m, store, fw, tr, _ := positionManager(at)
	states := &fakeStates{state: "RANGE"}
	m.Hours, m.States = &fakeHours{}, states
	m.Settings.PriceState = workers.PriceStateSettings{Enabled: true, MaxAge: 2 * time.Hour, OnUnavailable: d.PriceStateBlock, Scope: d.PriceStateScopeReentry}
	_ = store.AppendRecord(context.Background(), "JUDGMENT", "j1", at.Add(-time.Minute), judgment("j1", at.Add(-time.Minute), "SHORT", 3, []string{"n1"}, false))
	load := func() workers.PositionState {
		var st workers.PositionState
		_, _ = store.LoadState(context.Background(), "POSITION", &st)
		return st
	}
	run := func(now time.Time) {
		t.Helper()
		m.Clock = func() time.Time { return now }
		tr.bars = []submission.CandleRead{bar(now.Truncate(time.Minute), "100")}
		if err := m.Run(context.Background()); err != nil {
			t.Fatal(err)
		}
	}
	// The first batch is not a re-entry: it opens in a range.
	run(at)
	if g := load().Gate; g == nil || !g.Open || len(fw.directives) == 0 || fw.directives[len(fw.directives)-1].RiskFraction == nil {
		t.Fatal("first batch gated", g)
	}
	// The batch closes without a stop: the next one on the same side is not
	// a re-entry either.
	tr.qty, tr.entry, tr.cycle = "-1", "100", "cycle-1"
	run(at.Add(5 * time.Minute))
	tr.qty, tr.entry, tr.cycle = "", "", ""
	run(at.Add(time.Hour))
	if st := load(); st.LastStopped || st.Gate == nil || !st.Gate.Open {
		t.Fatal("plain close", st.LastStopped, st.Gate)
	}
	// A batch that ends in a stop: the same side now waits for the state.
	tr.qty, tr.entry, tr.cycle = "-1", "100", "cycle-2"
	run(at.Add(time.Hour + 5*time.Minute))
	fw.object.Reentry = &dto.ReentryState{Direction: -1, StoppedAt: at.Add(2 * time.Hour)}
	tr.qty, tr.entry, tr.cycle = "", "", ""
	run(at.Add(2 * time.Hour))
	st := load()
	if !st.LastStopped || st.Gate == nil || st.Gate.Open || st.Gate.Reason != "PRICE_STATE_DISAGREES" {
		t.Fatal("re-entry not gated", st.LastStopped, st.Gate)
	}
	states.state = "DOWNTREND"
	run(at.Add(3*time.Hour + 5*time.Minute))
	if g := load().Gate; g == nil || !g.Open || g.Reason != "PRICE_STATE_AGREES" || fw.directives[len(fw.directives)-1].RiskFraction == nil {
		t.Fatal("agreeing re-entry", g)
	}
}

func TestT334ReentryProfile(t *testing.T) {
	var p config.DynamicProfile
	p.InstanceID, p.Environment, p.ObjectID, p.TimeoutSeconds, p.MaxBytes = "soxl-peer", "SIM", "soxlusdt-peer", 60, 1<<20
	p.Access.DatabaseURL, p.Access.FrameworkURL, p.Access.FrameworkToken = "postgresql://fixture", "http://127.0.0.1:1", "t"
	p.Access.TradingReadURL, p.Access.TradingReadToken, p.Access.AccountID, p.Access.RunID = "http://127.0.0.1:2", "t", "account-peer", "soxl-peer"
	p.Judgment.ImpactByConfidence = [5]string{"0.22", "0.45", "0.65", "0.85", "1"}
	p.Judgment.MinHalfLifeHours, p.Judgment.MaxHalfLifeHours = 4, 72
	p.Position.CloseMinutes, p.Position.InsuranceFraction, p.Position.WickMultiplier, p.Position.BaselineMaxShare = 5, 0.25, 2, 0.5
	p.Position.DirectiveTTLMinutes, p.Position.NoiseLookbackDays, p.Position.PriceTick = 15, 28, 0.01
	p.Follow.SourceURL, p.Follow.SourceToken, p.Follow.SourceInstanceID = "http://127.0.0.1:18311", "read", "soxl-example"
	p.Follow.Since = time.Date(2026, 10, 23, 0, 0, 0, 0, time.UTC)
	if err := p.Validate(); err != nil {
		t.Fatal(err)
	}
	rec := assembly.RecordedSettings(p)
	if rec.ReentryRule != d.ReentryConfidence {
		t.Fatal("default rule", rec.ReentryRule)
	}
	p.Judgment.ReentryRule = d.ReentrySameDirection
	if err := p.Validate(); err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(assembly.RecordedSettings(p))
	if !strings.Contains(string(raw), `"reentry_rule":"SAME_DIRECTION"`) {
		t.Fatalf("recorded %s", raw)
	}
	p.Judgment.ReentryRule = "ANY"
	if p.Validate() == nil {
		t.Fatal("unknown rule accepted")
	}
}
