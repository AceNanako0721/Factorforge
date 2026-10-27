package domain

import (
	"github.com/AceNanako0721/Factorforge/src/factorforge/trading/domain/decimal"
	"sort"
	"strconv"
	"time"
)

// PnL sources (design chapter 24): a venue-backed run reports the venue's
// own account figures; an internal-matching run reports system figures. Every
// run also gets a reference funding from settled mainnet records, which never
// enters Equity(), risk, sizing or reconciliation.
type PnLBlock struct {
	Since            *time.Time     `json:"since"`
	Trading          *decimal.Value `json:"trading"`
	BookedFunding    *decimal.Value `json:"booked_funding"`
	ReferenceFunding *decimal.Value `json:"reference_funding"`
	Booked           *decimal.Value `json:"booked"`
	Reference        *decimal.Value `json:"reference"`
}

// PositionPnL is the current position cycle of one instrument.
type PositionPnL struct {
	InstrumentKey InstrumentKey `json:"instrument_key"`
	PnLBlock
}

type ReferenceStatus struct {
	SourceID    *string    `json:"source_id"`
	Settlements int64      `json:"settlements"`
	ThroughAt   *time.Time `json:"through_at"`
	Stale       bool       `json:"stale"`
	Check       string     `json:"check"`
}

type PnLView struct {
	Basis           string          `json:"basis"`
	Environment     string          `json:"environment"`
	VenueObservedAt *time.Time      `json:"venue_observed_at"`
	Total           PnLBlock        `json:"total"`
	Positions       []PositionPnL   `json:"positions"`
	Reference       ReferenceStatus `json:"reference"`
	Missing         []string        `json:"missing"`
}

type positionHistory struct {
	fills []*Fill
}

func (e *Engine) history(code string) positionHistory {
	h := positionHistory{}
	for _, f := range e.Run.Fills.Values() {
		if f.InstrumentKey.Code() == code {
			h.fills = append(h.fills, f)
		}
	}
	sort.SliceStable(h.fills, func(i, j int) bool { return h.fills[i].HappenedAt.Before(h.fills[j].HappenedAt) })
	return h
}

// before is the net filled quantity of fills strictly before at.
func (e *Engine) before(h positionHistory, at time.Time) decimal.Value {
	q := zero
	for _, f := range h.fills {
		if !f.HappenedAt.Before(at) {
			break
		}
		q = e.Math.Add(q, signed(e, f))
	}
	return q
}

func signed(e *Engine, f *Fill) decimal.Value {
	if f.Side == "BUY" {
		return f.Quantity
	}
	return e.Math.Neg(f.Quantity)
}

// opened is when the current position last went from flat or flipped side.
func (e *Engine) opened(h positionHistory) (*time.Time, decimal.Value) {
	q := zero
	var at *time.Time
	for _, f := range h.fills {
		next := e.Math.Add(q, signed(e, f))
		if next.Sign() != 0 && (q.Sign() == 0 || q.Sign() != next.Sign()) {
			t := f.HappenedAt
			at = &t
		}
		if next.Sign() == 0 {
			at = nil
		}
		q = next
	}
	return at, q
}

func (e *Engine) settlementAmount(s *FundingSettlement, q decimal.Value) decimal.Value {
	spec := e.Run.Specs.Value(s.InstrumentKey.Code())
	if spec == nil || q.Sign() == 0 {
		return zero
	}
	native := e.Math.Neg(e.Math.Mul(e.Math.Mul(e.Math.Mul(q, spec.ContractMultiplier), s.MarkPrice), s.Rate))
	return e.Convert(native, spec.SettlementCurrency)
}

func ptr(v decimal.Value) *decimal.Value { return &v }

func (e *Engine) PnLView() PnLView {
	run := e.Run
	view := PnLView{Basis: "SYSTEM", Environment: run.RunKey.Environment, Positions: []PositionPnL{}, Missing: []string{}}
	if run.VenueBacked() {
		view.Basis = "VENUE"
	}
	bookedTotal, flows := zero, zero
	for _, i := range run.Incomes.Values() {
		amount, ok := run.ConvertedIncome.Get(i.ExternalID)
		if !ok {
			amount = i.Amount
		}
		if i.Kind == "FUNDING" || i.Kind == "SPECIAL_FUNDING" {
			bookedTotal = e.Math.Add(bookedTotal, amount)
		}
		if i.Kind == "TRANSFER" {
			flows = e.Math.Add(flows, amount)
		}
	}

	settlements := run.ReferenceFunding.Values()
	sort.SliceStable(settlements, func(i, j int) bool { return settlements[i].SettledAt.Before(settlements[j].SettledAt) })
	histories := map[string]positionHistory{}
	complete := true
	for _, code := range run.Positions.Keys() {
		h := e.history(code)
		histories[code] = h
		if _, q := e.opened(h); q.Cmp(run.Positions.Value(code).Quantity) != 0 {
			complete = false
		}
	}
	referenceTotal := zero
	for _, s := range settlements {
		code := s.InstrumentKey.Code()
		h, ok := histories[code]
		if !ok {
			h = e.history(code)
			histories[code] = h
		}
		referenceTotal = e.Math.Add(referenceTotal, e.settlementAmount(s, e.before(h, s.SettledAt)))
	}
	if !complete {
		view.Missing = append(view.Missing, "POSITION_HISTORY_INCOMPLETE")
	}
	// No record yet means the reference has not been obtained, not zero.
	if len(settlements) == 0 {
		complete = false
		view.Missing = append(view.Missing, "REFERENCE_FUNDING_NOT_OBSERVED")
	}

	// Reference status: stale once a whole interval has passed without the
	// next record (the last observed interval, 8 h before two records exist).
	view.Reference.Check = "NOT_APPLICABLE"
	view.Reference.Settlements = int64(len(settlements))
	if n := len(settlements); n > 0 {
		last := settlements[n-1]
		through := last.SettledAt
		source := last.SourceID
		view.Reference.ThroughAt, view.Reference.SourceID = &through, &source
		interval := 8 * time.Hour
		if n > 1 {
			interval = last.SettledAt.Sub(settlements[n-2].SettledAt)
		}
		view.Reference.Stale = run.Clock.After(through.Add(interval + 30*time.Minute))
	} else {
		view.Reference.Stale = true
	}

	total := PnLBlock{}
	if complete {
		total.ReferenceFunding = ptr(referenceTotal)
	}
	if view.Basis == "VENUE" {
		total.BookedFunding = ptr(bookedTotal)
		if v := run.VenueAccount; v != nil {
			at := v.ObservedAt
			view.VenueObservedAt = &at
			trading := e.Math.Sub(e.Math.Sub(e.Math.Sub(v.MarginBalance, run.InitialCash), flows), bookedTotal)
			total.Trading = ptr(trading)
			total.Booked = ptr(e.Math.Add(trading, bookedTotal))
		} else {
			view.Missing = append(view.Missing, "VENUE_ACCOUNT_NOT_OBSERVED")
		}
		if run.RunKey.Environment == "LIVE" && complete {
			step, _ := decimal.Parse("0.00000001")
			count, _ := decimal.Parse(strconv.Itoa(len(settlements)))
			tolerance := e.Math.Mul(step, count)
			view.Reference.Check = "MATCH"
			if e.Math.Abs(e.Math.Sub(referenceTotal, bookedTotal)).Cmp(tolerance) > 0 {
				view.Reference.Check = "DIFFERENT"
			}
		}
	} else {
		total.Trading = ptr(e.Math.Sub(e.Math.Sub(e.Math.Sub(e.Equity(), run.InitialCash), flows), bookedTotal))
	}
	if total.Trading != nil && total.ReferenceFunding != nil {
		total.Reference = ptr(e.Math.Add(*total.Trading, *total.ReferenceFunding))
	}
	if first := firstFill(run); first != nil {
		total.Since = first
	}
	view.Total = total

	for _, code := range run.Positions.Keys() {
		p := run.Positions.Value(code)
		if p.Quantity.Sign() == 0 {
			continue
		}
		block := &PnLBlock{}
		since, _ := e.opened(histories[code])
		if since != nil {
			at := *since
			block.Since = &at
		}
		booked := zero
		for _, i := range run.Incomes.Values() {
			if i.InstrumentKey == nil || i.InstrumentKey.Code() != code || (i.Kind != "FUNDING" && i.Kind != "SPECIAL_FUNDING") || since == nil || i.HappenedAt.Before(*since) {
				continue
			}
			amount, ok := run.ConvertedIncome.Get(i.ExternalID)
			if !ok {
				amount = i.Amount
			}
			booked = e.Math.Add(booked, amount)
		}
		if complete && since != nil {
			reference := zero
			for _, s := range settlements {
				if s.InstrumentKey.Code() == code && s.SettledAt.After(*since) {
					reference = e.Math.Add(reference, e.settlementAmount(s, e.before(histories[code], s.SettledAt)))
				}
			}
			block.ReferenceFunding = ptr(reference)
		}
		var unrealized *decimal.Value
		if view.Basis == "VENUE" {
			block.BookedFunding = ptr(booked)
			if v := run.VenueAccount; v != nil {
				if u, ok := v.PositionUnrealized.Get(code); ok {
					unrealized = ptr(u)
				}
			}
		} else if spec := run.Specs.Value(code); spec != nil && p.AverageEntry != nil {
			unrealized = ptr(e.Convert(e.Math.Mul(e.Math.Mul(p.Quantity, spec.ContractMultiplier), e.Math.Sub(e.Mark(code), *p.AverageEntry)), spec.SettlementCurrency))
		}
		if unrealized != nil {
			trading := e.Math.Add(e.Math.Sub(p.CycleNet, booked), *unrealized)
			block.Trading = ptr(trading)
			if block.BookedFunding != nil {
				block.Booked = ptr(e.Math.Add(trading, booked))
			}
			if block.ReferenceFunding != nil {
				block.Reference = ptr(e.Math.Add(trading, *block.ReferenceFunding))
			}
		}
		view.Positions = append(view.Positions, PositionPnL{InstrumentKey: p.InstrumentKey, PnLBlock: *block})
	}
	return view
}

func firstFill(run *Aggregate) *time.Time {
	var first *time.Time
	for _, f := range run.Fills.Values() {
		if first == nil || f.HappenedAt.Before(*first) {
			t := f.HappenedAt
			first = &t
		}
	}
	return first
}
