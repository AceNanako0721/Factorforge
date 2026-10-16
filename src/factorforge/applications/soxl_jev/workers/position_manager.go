package workers

import (
	"context"
	"encoding/json"
	"math"
	"sort"
	"strconv"
	"strings"
	"time"

	d "github.com/AceNanako0721/Factorforge/src/factorforge/applications/soxl_jev/domain"
	"github.com/AceNanako0721/Factorforge/src/factorforge/applications/soxl_jev/submission"
	dto "github.com/AceNanako0721/Factorforge/src/factorforge/strategy/api/dto"
	tdto "github.com/AceNanako0721/Factorforge/src/factorforge/trading/api/dto"
)

// PositionFramework is the P2 surface used by the position manager.
type PositionFramework interface {
	ObjectState(ctx context.Context, id string) (*dto.ObservedObject, error)
	Pool(ctx context.Context, id string) (dto.PoolView, error)
	LatestDecision(ctx context.Context, id string) (*dto.DecisionView, error)
	StopDirectives(ctx context.Context, id string) ([]dto.StopDirectiveRecord, error)
	Cases(ctx context.Context, id string) ([]dto.CaseRecord, error)
	ScheduledEvents(ctx context.Context, object string) ([]d.ScheduledEvent, error)
	SubmitStopDirective(ctx context.Context, v dto.StopDirective) (dto.StopDirectiveReceipt, error)
	SubmitCloseExit(ctx context.Context, v dto.ExitRequest) (dto.ExitRequestRecord, error)
	TransferToBaseline(ctx context.Context, v dto.BaselineTransfer) (dto.BaselineTransferRecord, error)
	SubmitObservation(ctx context.Context, o d.FactorObservation) error
}

// PositionTrading is the read-only P1 surface.
type PositionTrading interface {
	Account(ctx context.Context) (submission.AccountRead, error)
	Positions(ctx context.Context) ([]submission.PositionRead, error)
	Protections(ctx context.Context) ([]submission.ProtectionRead, error)
	Candles(ctx context.Context, venue, instrument, interval string, start, end time.Time) ([]submission.CandleRead, error)
}
type MinuteSource interface {
	Range(ctx context.Context, start, end time.Time) ([]d.MinuteBar, error)
}

type PositionSettings struct {
	ObjectID, OwnerID, Venue, InstrumentID string
	SourceModule, TriggerKind              string
	BaselineFactorID                       string
	Stop                                   d.StopSettings
	DirectiveTTL, ReentryWait              time.Duration
	BaselineMaxShare, RoundTripCost        float64
	BaselineRenew, NoiseMaxAge             time.Duration
	NoiseConfig                            d.NoiseConfig
	EventTypes                             []string
	PriceTick                              float64
}

// BatchState is the instance view of one position cycle (S3-027～029).
type BatchState struct {
	CycleID     string         `json:"cycle_id"`
	Direction   int            `json:"direction"`
	Entry       float64        `json:"entry"`
	OpenedAt    time.Time      `json:"opened_at"`
	Geometry    d.StopGeometry `json:"geometry"`
	Budget      float64        `json:"budget"`
	BestClose   float64        `json:"best_close"`
	LastBarAt   time.Time      `json:"last_bar_at"`
	CloseStop   float64        `json:"close_stop"`
	Trailing    bool           `json:"trailing"`
	Transferred bool           `json:"transferred"`
	ExitID      string         `json:"exit_id,omitempty"`
}
type InitialPlan struct {
	Direction   int            `json:"direction"`
	Geometry    d.StopGeometry `json:"geometry"`
	Price       float64        `json:"price"`
	Distance    float64        `json:"distance"`
	CloseDist   float64        `json:"close_distance"`
	Fraction    float64        `json:"risk_fraction"`
	Equity      float64        `json:"equity"`
	DirectiveID string         `json:"directive_id"`
}
type PositionState struct {
	NoiseID    string           `json:"noise_id"`
	NoiseAt    time.Time        `json:"noise_at"`
	Noise      json.RawMessage  `json:"noise,omitempty"`
	Versions   map[string]int   `json:"versions"`
	Initial    *InitialPlan     `json:"initial,omitempty"`
	Batch      *BatchState      `json:"batch,omitempty"`
	Baseline   d.BaselineState  `json:"baseline"`
	BaselineAt time.Time        `json:"baseline_at"`
	EquityPeak float64          `json:"equity_peak"`
	Outcomes   []d.BatchOutcome `json:"outcomes"`
	SeenCases  []string         `json:"seen_cases"`
}

type PositionManager struct {
	Store     DynamicStore
	Framework PositionFramework
	Trading   PositionTrading
	Minutes   MinuteSource
	Clock     func() time.Time
	Alerts    Alerter
	Settings  PositionSettings
}

// Run performs one manager cycle; every write is idempotent by identifier and
// state advances only after the writes of the step succeeded.
func (m *PositionManager) Run(ctx context.Context) error {
	// Seconds precision: stored timestamps round-trip exactly through PostgreSQL.
	now := m.Clock().UTC().Truncate(time.Second)
	s := m.Settings
	var st PositionState
	version, err := m.Store.LoadState(ctx, "POSITION", &st)
	if err != nil {
		return err
	}
	if st.Versions == nil {
		st.Versions = map[string]int{}
	}
	if err = m.refreshNoise(ctx, &st, now); err != nil {
		m.alert(ctx, "NOISE_TABLE_UNAVAILABLE", errCode(err))
	}
	account, err := m.Trading.Account(ctx)
	if err != nil {
		return err
	}
	equity, _ := strconv.ParseFloat(account.Equity, 64)
	if equity > st.EquityPeak {
		st.EquityPeak = equity
	}
	position, err := m.position(ctx)
	if err != nil {
		return err
	}
	bars, err := m.Trading.Candles(ctx, s.Venue, s.InstrumentID, "5m", now.Add(-40*time.Minute), now)
	if err != nil {
		return err
	}
	price := lastClose(bars, now)
	obj, err := m.Framework.ObjectState(ctx, s.ObjectID)
	if err != nil || obj == nil {
		return d.Fail("FRAMEWORK_OBJECT_REQUIRED", 503)
	}
	if err = m.outcomes(ctx, &st); err != nil {
		return err
	}
	qty := 0.0
	if position != nil {
		qty, _ = strconv.ParseFloat(position.Quantity, 64)
	}
	switch {
	case qty == 0 && st.Batch != nil:
		m.closeBatch(ctx, &st, obj, now)
	case qty != 0:
		if err = m.manage(ctx, &st, position, qty, bars, price, equity, now); err != nil {
			return err
		}
	}
	// The initial-distance directive stays fresh during a batch as well, so
	// level increases are sized from R + buffer, never from a fallback.
	if price > 0 {
		if err = m.initial(ctx, &st, price, equity, now); err != nil {
			return err
		}
	}
	if err = m.baseline(ctx, &st, now); err != nil {
		return err
	}
	st.Noise = nil
	return m.Store.SaveState(ctx, "POSITION", version, now, st)
}

func (m *PositionManager) position(ctx context.Context) (*submission.PositionRead, error) {
	rows, err := m.Trading.Positions(ctx)
	if err != nil {
		return nil, err
	}
	for i := range rows {
		if rows[i].OwnerID == m.Settings.OwnerID && rows[i].InstrumentKey["instrument_id"] == m.Settings.InstrumentID {
			return &rows[i], nil
		}
	}
	return nil, nil
}

// refreshNoise rebuilds the table daily and records it as a NOISE record.
func (m *PositionManager) refreshNoise(ctx context.Context, st *PositionState, now time.Time) error {
	if !st.NoiseAt.IsZero() && now.Sub(st.NoiseAt) < 24*time.Hour {
		return nil
	}
	cfg := m.Settings.NoiseConfig
	start := now.Add(-time.Duration(cfg.LookbackDays)*24*time.Hour - 73*time.Hour)
	bars, err := m.Minutes.Range(ctx, start, now)
	if err != nil {
		return err
	}
	schedules, err := m.Framework.ScheduledEvents(ctx, m.Settings.ObjectID)
	if err != nil {
		return err
	}
	events := []int64{}
	for _, e := range schedules {
		if d.Has(m.Settings.EventTypes, e.EventType) && e.Status == "SCHEDULED" {
			events = append(events, e.ExpectedAt.Unix())
		}
	}
	sort.Slice(events, func(i, j int) bool { return events[i] < events[j] })
	table, err := d.ComputeNoiseTable(bars, events, now.Unix()-now.Unix()%60, now, cfg)
	if err != nil {
		return err
	}
	id := "noise-" + now.Format("20060102T150405Z")
	if err = m.Store.AppendRecord(ctx, "NOISE", id, now, table); err != nil {
		return err
	}
	st.NoiseID, st.NoiseAt = id, now
	return nil
}

func (m *PositionManager) noise(ctx context.Context, st *PositionState, now time.Time) (d.NoiseView, bool) {
	if st.NoiseID == "" || now.Sub(st.NoiseAt) > m.Settings.NoiseMaxAge {
		return nil, false
	}
	rows, err := m.Store.Records(ctx, "NOISE", st.NoiseAt, st.NoiseAt.Add(time.Nanosecond), 1)
	if err != nil || len(rows) != 1 {
		return nil, false
	}
	view, err := d.DecodeNoiseView(rows[0])
	return view, err == nil
}

// outcomes folds newly closed P2 cases of tracked batches into R history.
func (m *PositionManager) outcomes(ctx context.Context, st *PositionState) error {
	cases, err := m.Framework.Cases(ctx, m.Settings.ObjectID)
	if err != nil {
		return err
	}
	sort.Slice(cases, func(i, j int) bool { return cases[i].EntryAt.Before(cases[j].EntryAt) })
	for _, c := range cases {
		if c.ExitAt == nil || d.Has(st.SeenCases, c.CaseID) || c.EntrySnapshot["batch_id"] == nil {
			continue
		}
		budget := 0.0
		if st.Batch != nil && st.Batch.Budget > 0 {
			budget = st.Batch.Budget
		}
		if st.Initial != nil && budget == 0 {
			budget = st.Initial.Fraction * st.Initial.Equity
		}
		net, _ := strconv.ParseFloat(c.PNLComponents["net"].String(), 64)
		if budget > 0 {
			st.Outcomes = append(st.Outcomes, d.BatchOutcome{R: -net / budget, ClosedAt: c.ExitAt.UTC().Format(time.RFC3339)})
		}
		st.SeenCases = append(st.SeenCases, c.CaseID)
	}
	return nil
}

// initial keeps a fresh INITIAL_DISTANCE directive for the current view so
// the framework can open a batch with Q frozen from R + buffer (S3-027).
func (m *PositionManager) initial(ctx context.Context, st *PositionState, price, equity float64, now time.Time) error {
	s := m.Settings
	direction, horizon, err := m.view(ctx, now)
	if err != nil || direction == 0 {
		return err
	}
	view, ok := m.noise(ctx, st, now)
	if !ok {
		m.alert(ctx, "NOISE_TABLE_STALE", st.NoiseID)
		return nil
	}
	drawdown := 0.0
	if st.EquityPeak > 0 {
		drawdown = (st.EquityPeak - equity) / st.EquityPeak
	}
	fraction := d.RiskFraction(st.Outcomes, drawdown)
	if fraction <= 0 {
		m.alert(ctx, "RISK_FRACTION_ZERO", "")
		return nil
	}
	g, ok := d.PlanStop(view, s.Stop, now, direction, horizon)
	if !ok {
		m.alert(ctx, "HARD_STOP_BEYOND_INSURANCE", strconv.Itoa(horizon))
		return nil
	}
	if st.Initial != nil && st.Initial.Direction == direction && st.Initial.Geometry == g && now.Sub(lastDirectiveAt(st.Initial.DirectiveID)) < s.DirectiveTTL/2 {
		return nil
	}
	st.Versions["INITIAL_DISTANCE"]++
	v := st.Versions["INITIAL_DISTANCE"]
	id := "initial-" + now.Format("20060102T150405Z")
	plan := InitialPlan{Direction: direction, Geometry: g, Price: price, Distance: (g.R + g.Buffer) * price, CloseDist: g.R * price, Fraction: fraction, Equity: equity, DirectiveID: id}
	directive := dto.StopDirective{DirectiveID: id, ObjectID: s.ObjectID, Version: v, Kind: "INITIAL_DISTANCE", PositionDirection: sideName(direction), TriggerKind: s.TriggerKind, InitialStopDistance: dec(plan.Distance), EffectiveAt: now, ValidForSeconds: int(s.DirectiveTTL.Seconds()), SourceModule: s.SourceModule, ReasonCodes: []string{"DYNAMIC_STOP_INITIAL", "TIER_" + strconv.Itoa(g.Tier) + "H"}, Mode: "VENUE", RiskFraction: dec(fraction), CloseStopDistance: dec(plan.CloseDist)}
	if _, err = m.Framework.SubmitStopDirective(ctx, directive); err != nil {
		return err
	}
	st.Initial = &plan
	return m.Store.AppendRecord(ctx, "STOP_EVENT", id, now, map[string]any{"kind": "INITIAL", "plan": plan, "noise_id": st.NoiseID})
}

func lastDirectiveAt(id string) time.Time {
	if len(id) < 16 {
		return time.Time{}
	}
	t, _ := time.Parse("20060102T150405Z", id[len(id)-16:])
	return t
}

// view returns the direction the framework composes now and the horizon of
// the newest valid judgment (decision first, judgment as fallback).
func (m *PositionManager) view(ctx context.Context, now time.Time) (int, int, error) {
	decision, err := m.Framework.LatestDecision(ctx, m.Settings.ObjectID)
	if err != nil {
		return 0, 0, err
	}
	last, err := m.judgments(ctx, now, 1)
	if err != nil {
		return 0, 0, err
	}
	direction, horizon := 0, 24
	if len(last) == 1 && last[0].Output != nil {
		direction, horizon = d.DirectionSign(last[0].Output.Direction), last[0].Output.HorizonHours
	}
	if decision != nil && decision.Components != nil && decision.Components.Combined.Sign() != 0 {
		direction = decision.Components.Combined.Sign()
	}
	return direction, horizon, nil
}

func (m *PositionManager) judgments(ctx context.Context, now time.Time, n int) ([]d.Judgment, error) {
	rows, err := m.Store.Records(ctx, "JUDGMENT", now.Add(-72*time.Hour), now.Add(time.Nanosecond), 400)
	if err != nil {
		return nil, err
	}
	out := []d.Judgment{}
	for i := len(rows) - 1; i >= 0 && len(out) < n; i-- {
		var j d.Judgment
		if json.Unmarshal(rows[i], &j) == nil && j.OK && j.Output != nil {
			out = append([]d.Judgment{j}, out...)
		}
	}
	return out, nil
}

// manage tracks the open batch: best close, trailing stop, close-stop exit
// and transfer to baseline (S3-027～029).
func (m *PositionManager) manage(ctx context.Context, st *PositionState, p *submission.PositionRead, qty float64, bars []submission.CandleRead, price, equity float64, now time.Time) error {
	s := m.Settings
	direction := 1
	if qty < 0 {
		direction = -1
	}
	entry := price
	if p.AverageEntry != nil {
		entry, _ = strconv.ParseFloat(*p.AverageEntry, 64)
	}
	b := st.Batch
	if b == nil || b.CycleID != p.PositionCycleID {
		b = &BatchState{CycleID: p.PositionCycleID, Direction: direction, Entry: entry, OpenedAt: now, BestClose: entry, LastBarAt: now.Truncate(5 * time.Minute)}
		if st.Initial != nil && st.Initial.Direction == direction {
			b.Geometry, b.Budget = st.Initial.Geometry, st.Initial.Fraction*st.Initial.Equity
			b.CloseStop = entry - float64(direction)*st.Initial.CloseDist
		}
		st.Batch = b
		if err := m.Store.AppendRecord(ctx, "STOP_EVENT", "open-"+p.PositionCycleID, now, map[string]any{"kind": "OPEN", "batch": b}); err != nil {
			return err
		}
	}
	if b.Geometry.R == 0 {
		return nil // no recorded geometry: leave the framework's stops untouched
	}
	effective := m.effectiveStop(ctx, p)
	if effective != nil && effective.close != 0 {
		b.CloseStop = effective.close
	}
	// Completed 5-minute closes since the last processed bar.
	for _, bar := range bars {
		if !bar.Final || bar.CloseAt.After(now) || !bar.CloseAt.After(b.LastBarAt) || bar.CloseAt.Unix()%300 != 0 {
			continue
		}
		close, _ := strconv.ParseFloat(bar.Close, 64)
		if float64(direction)*(close-b.BestClose) > 0 {
			b.BestClose = close
		}
		b.LastBarAt = bar.CloseAt
		if bar.CloseAt.Unix()%int64(s.Stop.CloseMinutes*60) == 0 && b.ExitID == "" && b.CloseStop != 0 && d.CloseCrossed(direction, close, b.CloseStop) && effective != nil {
			id := "exit-" + p.PositionCycleID + "-" + bar.CloseAt.Format("20060102T1504")
			_, err := m.Framework.SubmitCloseExit(ctx, dto.ExitRequest{ExitID: safeID(id), ObjectID: s.ObjectID, PositionDirection: sideName(direction), DirectiveID: effective.id, DirectiveVersion: effective.version, BarInterval: s.Stop.CloseMinutes * 60, BarCloseAt: bar.CloseAt, ClosePrice: *dec(close), SourceModule: s.SourceModule})
			if err != nil {
				m.alert(ctx, "CLOSE_EXIT_REJECTED", errCode(err))
			} else {
				b.ExitID = safeID(id)
				if err = m.Store.AppendRecord(ctx, "STOP_EVENT", b.ExitID, now, map[string]any{"kind": "CLOSE_EXIT", "close": close, "close_stop": b.CloseStop}); err != nil {
					return err
				}
			}
		}
	}
	if b.ExitID != "" {
		return nil
	}
	m1, moved := d.Trailing(direction, b.Entry, b.BestClose, b.Geometry.R, b.Geometry.Trail, s.RoundTripCost*b.Entry, b.CloseStop)
	if moved && math.Abs(m1-b.CloseStop) >= s.PriceTick {
		trigger := roundTick(m1-float64(direction)*b.Geometry.Buffer*b.Entry, s.PriceTick, direction)
		st.Versions["POSITION_STOP"]++
		id := safeID("trail-" + p.PositionCycleID + "-" + strconv.Itoa(st.Versions["POSITION_STOP"]))
		directive := dto.StopDirective{DirectiveID: id, ObjectID: s.ObjectID, Version: st.Versions["POSITION_STOP"], Kind: "POSITION_STOP", PositionDirection: sideName(direction), TriggerPrice: dec(trigger), TriggerKind: s.TriggerKind, EffectiveAt: now, ValidForSeconds: int((72 * time.Hour).Seconds()), SourceModule: s.SourceModule, ReasonCodes: []string{"DYNAMIC_STOP_TRAIL"}, Mode: "VENUE", CloseStopPrice: dec(m1)}
		if _, err := m.Framework.SubmitStopDirective(ctx, directive); err != nil {
			m.alert(ctx, "TRAIL_REJECTED", errCode(err))
		} else {
			b.Trailing = true
			if err = m.Store.AppendRecord(ctx, "STOP_EVENT", id, now, map[string]any{"kind": "TRAIL", "close_stop": m1, "trigger": trigger, "best": b.BestClose}); err != nil {
				return err
			}
		}
	}
	// +1R with the verified stop at break-even: the whole batch becomes baseline.
	breakEven := effective != nil && effective.close != 0 && float64(direction)*(effective.close-(b.Entry+float64(direction)*s.RoundTripCost*b.Entry)) >= 0
	if !b.Transferred && breakEven && float64(direction)*(price-b.Entry) >= b.Geometry.R*b.Entry && math.Abs(qty)*price <= s.BaselineMaxShare*equity {
		if err := m.transfer(ctx, st, b, direction, now); err != nil {
			m.alert(ctx, "TRANSFER_REJECTED", errCode(err))
		}
	}
	return nil
}

type effectiveDirective struct {
	id      string
	version int
	close   float64
}

// effectiveStop finds the verified directive for the current cycle and the
// close stop the framework derives from it (S2-034 CloseStopFor).
func (m *PositionManager) effectiveStop(ctx context.Context, p *submission.PositionRead) *effectiveDirective {
	rows, err := m.Framework.StopDirectives(ctx, m.Settings.ObjectID)
	if err != nil {
		return nil
	}
	var best *dto.StopDirectiveRecord
	for i := range rows {
		r := &rows[i]
		if r.Receipt.VerifiedAt == nil || !d.Has([]string{"VERIFIED", "RECORDED"}, r.Receipt.ApplicationState) {
			continue
		}
		if r.Directive.Kind == "POSITION_STOP" && r.PositionCycleID != p.PositionCycleID {
			continue
		}
		if best == nil || r.Receipt.VerifiedAt.After(*best.Receipt.VerifiedAt) {
			best = r
		}
	}
	if best == nil {
		return nil
	}
	out := &effectiveDirective{id: best.Directive.DirectiveID, version: best.Directive.Version}
	if v := best.Directive.CloseStopPrice; v != nil {
		out.close, _ = strconv.ParseFloat(v.String(), 64)
	} else if best.Directive.Kind == "INITIAL_DISTANCE" && best.Directive.CloseStopDistance != nil {
		protections, err := m.Trading.Protections(ctx)
		if err != nil {
			return out
		}
		for _, pr := range protections {
			if pr.Role == "STRATEGY" && pr.State == "ACTIVE_VERIFIED" && pr.PositionCycleID == p.PositionCycleID {
				trigger, _ := strconv.ParseFloat(toString(pr.Plan["trigger_price"]), 64)
				dist, _ := strconv.ParseFloat(best.Directive.InitialStopDistance.String(), 64)
				cd, _ := strconv.ParseFloat(best.Directive.CloseStopDistance.String(), 64)
				sign := 1.0
				if pr.PositionDirection == "SHORT" {
					sign = -1
				}
				out.close = trigger + sign*(dist-cd)
			}
		}
	}
	return out
}

func (m *PositionManager) transfer(ctx context.Context, st *PositionState, b *BatchState, direction int, now time.Time) error {
	s := m.Settings
	pool, err := m.Framework.Pool(ctx, s.ObjectID)
	if err != nil {
		return err
	}
	ids := []string{}
	for _, c := range pool.Contributions {
		if c.State == "ACTIVE" && c.Direction == direction && c.RemainingAmount.Sign() > 0 {
			ids = append(ids, c.ContributionID)
		}
	}
	decision, err := m.Framework.LatestDecision(ctx, s.ObjectID)
	if err != nil || decision == nil || decision.Components == nil || len(ids) == 0 {
		return d.Fail("TRANSFER_INPUT_UNAVAILABLE", 503)
	}
	value := decision.Components.Combined
	key := safeID("transfer-" + b.CycleID)
	if _, err = m.Framework.TransferToBaseline(ctx, dto.BaselineTransfer{TransferKey: key, ObjectID: s.ObjectID, ContributionIDs: ids, FactorID: s.BaselineFactorID, Observation: dto.TransferObservation{ObservationID: safeID("baseline-" + now.Format("20060102T150405Z")), Value: value, ReasonCode: "PLUS_ONE_R"}}); err != nil {
		return err
	}
	v, _ := strconv.ParseFloat(value.String(), 64)
	b.Transferred = true
	st.Baseline = d.BaselineState{Phase: "ACTIVE", Direction: direction, SourceChain: b.CycleID, StartedAt: now, Value: math.Abs(v)}
	st.BaselineAt = now
	return m.Store.AppendRecord(ctx, "STOP_EVENT", key, now, map[string]any{"kind": "TRANSFER", "contributions": ids, "value": v})
}

// closeBatch: a stop only flattens; the baseline is suspended, not cleared.
func (m *PositionManager) closeBatch(ctx context.Context, st *PositionState, obj *dto.ObservedObject, now time.Time) {
	b := st.Batch
	stopped := obj.Reentry != nil && !obj.Reentry.StoppedAt.Before(b.OpenedAt)
	if stopped && st.Baseline.Phase == "ACTIVE" {
		st.Baseline.Phase, st.Baseline.SuspendedAt = "SUSPENDED", now
		if err := m.observe(ctx, st, 0, false, "STOP_SUSPEND", now); err != nil {
			m.alert(ctx, "BASELINE_SUSPEND_FAILED", errCode(err))
		}
	}
	_ = m.Store.AppendRecord(ctx, "STOP_EVENT", safeID("close-"+b.CycleID), now, map[string]any{"kind": "CLOSE", "stopped": stopped, "batch": b})
	st.Batch = nil
}

// baseline advances the market-state baseline from the latest judgments and
// renews ACTIVE observations within their validity.
func (m *PositionManager) baseline(ctx context.Context, st *PositionState, now time.Time) error {
	if st.Baseline.Phase != "ACTIVE" && st.Baseline.Phase != "SUSPENDED" {
		return nil
	}
	recent, err := m.judgments(ctx, now, 2)
	if err != nil {
		return err
	}
	views := []d.JudgmentView{}
	for _, j := range recent {
		views = append(views, d.JudgmentView{At: j.At, Direction: d.DirectionSign(j.Output.Direction), Invalidated: j.Output.PreviousInvalidated && len(j.Output.InvalidationItemIDs) > 0})
	}
	next, reopen := d.AdvanceBaseline(st.Baseline, views, now, m.Settings.ReentryWait)
	switch {
	case next.Phase == "CLEARED":
		st.Baseline = next
		return m.observe(ctx, st, 0, false, "BASELINE_CLEARED", now)
	case reopen:
		st.Baseline = next
		return m.observe(ctx, st, float64(next.Direction)*next.Value, true, "BASELINE_RECONFIRM", now)
	case next.Phase == "ACTIVE" && now.Sub(st.BaselineAt) >= m.Settings.BaselineRenew:
		st.Baseline = next
		return m.observe(ctx, st, float64(next.Direction)*next.Value, false, "BASELINE_RENEW", now)
	}
	st.Baseline = next
	return nil
}

func (m *PositionManager) observe(ctx context.Context, st *PositionState, value float64, reconfirm bool, reason string, now time.Time) error {
	id := safeID("baseline-" + now.Format("20060102T150405Z"))
	o := d.FactorObservation{FactorID: m.Settings.BaselineFactorID, ObservationID: id, AvailableAt: now, Value: strconv.FormatFloat(value, 'f', 6, 64), InputsHash: "baseline-state-" + st.Baseline.Phase, EvidenceRef: "baseline:" + st.Baseline.SourceChain, Reconfirm: reconfirm, ReasonCode: reason}
	if err := m.Framework.SubmitObservation(ctx, o); err != nil {
		return err
	}
	st.BaselineAt = now
	return m.Store.AppendRecord(ctx, "STOP_EVENT", id, now, map[string]any{"kind": reason, "value": value, "reconfirm": reconfirm})
}

func (m *PositionManager) alert(ctx context.Context, code, detail string) {
	if m.Alerts != nil {
		m.Alerts.Alert(ctx, code, detail)
	}
}

func lastClose(bars []submission.CandleRead, now time.Time) float64 {
	var at time.Time
	price := 0.0
	for _, b := range bars {
		if b.Final && !b.CloseAt.After(now) && b.CloseAt.After(at) {
			at = b.CloseAt
			price, _ = strconv.ParseFloat(b.Close, 64)
		}
	}
	return price
}

func sideName(direction int) string {
	if direction > 0 {
		return "LONG"
	}
	return "SHORT"
}

func dec(v float64) *tdto.Decimal {
	text := strings.TrimRight(strings.TrimRight(strconv.FormatFloat(v, 'f', 8, 64), "0"), ".")
	x, err := tdto.ParseDecimal(text)
	if err != nil {
		return nil
	}
	return &x
}

// roundTick rounds a venue trigger away from the position (looser by < 1 tick).
func roundTick(v, tick float64, direction int) float64 {
	if tick <= 0 {
		return v
	}
	if direction > 0 {
		return math.Floor(v/tick) * tick
	}
	return math.Ceil(v/tick) * tick
}

func safeID(v string) string {
	out := []rune{}
	for _, r := range v {
		if r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '-' || r == '_' || r == '.' {
			out = append(out, r)
		}
		if len(out) == 128 {
			break
		}
	}
	return string(out)
}

func toString(v any) string {
	switch x := v.(type) {
	case string:
		return x
	case json.Number:
		return x.String()
	case float64:
		return strconv.FormatFloat(x, 'f', -1, 64)
	}
	return ""
}
