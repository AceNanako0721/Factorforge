package domain

import (
	"sort"
	"strings"
	"time"
)

type LayerEntry struct {
	ExposureSeconds Decimal `json:"exposure_seconds"`
	PNL             Decimal `json:"pnl"`
	CostFee         Decimal `json:"cost_fee"`
	CostFunding     Decimal `json:"cost_funding"`
	Hypothetical    bool    `json:"hypothetical"`
}
type LayerDay struct {
	ObjectID    string                 `json:"object_id"`
	Date        string                 `json:"date"`
	Sealed      bool                   `json:"sealed"`
	Entries     map[string]*LayerEntry `json:"entries"`
	PNL         Decimal                `json:"pnl"`
	CostFee     Decimal                `json:"cost_fee"`
	CostFunding Decimal                `json:"cost_funding"`
}
type LayerSettlement struct {
	At             time.Time            `json:"at"`
	Price          Decimal              `json:"price"`
	Quantity       Decimal              `json:"quantity"`
	Equity         Decimal              `json:"equity"`
	Components     *DecisionComponents  `json:"components"`
	SeenFills      map[string]time.Time `json:"seen_fills"`
	SeenIncomes    map[string]time.Time `json:"seen_incomes"`
	ShadowExposure map[string]Decimal   `json:"shadow_exposure"`
}

func LayerShares(c *DecisionComponents) (map[string]Decimal, error) {
	shares := map[string]Decimal{}
	if c == nil || c.Combined.Sign() == 0 {
		shares["UNATTRIBUTED"] = One()
		return shares, nil
	}
	m := Math()
	sum := m.Add(m.Add(c.Baseline, c.Assessment), c.Scheduled)
	for i, v := range []Decimal{c.Baseline, c.Assessment, c.Scheduled} {
		part := Zero()
		if sum.Sign() != 0 {
			part = m.Mul(c.Core, m.Div(v, sum))
		}
		shares[Layers[i]] = m.Div(part, c.Combined)
	}
	shares["ADJUSTMENT"] = m.Div(m.Sub(c.Combined, c.Core), c.Combined)
	return shares, m.Err()
}
func layerDay(s *StrategyState, id string, at time.Time) (*LayerDay, error) {
	date := at.UTC().Format("2006-01-02")
	key := id + "/" + date
	row := s.LayerLedger.Value(key)
	if row == nil {
		row = &LayerDay{ObjectID: id, Date: date, Entries: map[string]*LayerEntry{}}
		s.LayerLedger.Set(key, row)
	}
	if row.Sealed {
		return nil, &Error{"LAYER_LEDGER_IMBALANCE", 423}
	}
	return row, nil
}
func layerEntry(row *LayerDay, key string, hypothetical bool) *LayerEntry {
	e := row.Entries[key]
	if e == nil {
		e = &LayerEntry{Hypothetical: hypothetical}
		row.Entries[key] = e
	}
	return e
}
func VerifyLayerDay(row *LayerDay, tolerance Decimal) error {
	m := Math()
	pnl, fee, funding := Zero(), Zero(), Zero()
	for _, key := range compositionKeys(row.Entries) {
		e := row.Entries[key]
		if e.Hypothetical || strings.HasPrefix(key, "factor:") {
			continue
		}
		pnl = m.Add(pnl, e.PNL)
		fee = m.Add(fee, e.CostFee)
		funding = m.Add(funding, e.CostFunding)
	}
	if m.Sub(pnl, row.PNL).Abs().Cmp(tolerance) > 0 || m.Sub(fee, row.CostFee).Abs().Cmp(tolerance) > 0 || m.Sub(funding, row.CostFunding).Abs().Cmp(tolerance) > 0 {
		return &Error{"LAYER_LEDGER_IMBALANCE", 423}
	}
	return m.Err()
}

// AllocateLayers assigns residual rounding to the last layer and keeps factor
// sub-ledgers separate from the conserved top-level total.
func AllocateLayers(row *LayerDay, c *DecisionComponents, pnl, fee, funding, exposureSeconds Decimal, tolerance Decimal) error {
	if row.Sealed {
		return &Error{"LAYER_LEDGER_IMBALANCE", 423}
	}
	if err := VerifyLayerDay(row, tolerance); err != nil {
		return err
	}
	shares, err := LayerShares(c)
	if err != nil {
		return err
	}
	m := Math()
	keys := Layers
	if c == nil || c.Combined.Sign() == 0 {
		keys = []string{"UNATTRIBUTED"}
	}
	leftPNL, leftFunding, leftExposure := pnl, funding, exposureSeconds
	for i, key := range keys {
		sp, sf, se := m.Mul(pnl, shares[key]), m.Mul(funding, shares[key]), m.Mul(exposureSeconds, shares[key])
		if i == len(keys)-1 {
			sp, sf, se = leftPNL, leftFunding, leftExposure
		}
		leftPNL = m.Sub(leftPNL, sp)
		leftFunding = m.Sub(leftFunding, sf)
		leftExposure = m.Sub(leftExposure, se)
		e := layerEntry(row, key, false)
		e.PNL = m.Add(e.PNL, sp)
		e.CostFunding = m.Add(e.CostFunding, sf)
		e.ExposureSeconds = m.Add(e.ExposureSeconds, se)
	}
	trigger := "UNATTRIBUTED"
	if c != nil && Has(Layers, c.TriggerLayer) {
		trigger = c.TriggerLayer
	}
	e := layerEntry(row, trigger, false)
	e.CostFee = m.Add(e.CostFee, fee)
	if c != nil && c.Baseline.Sign() != 0 {
		ids := compositionKeys(c.Factors)
		factorTotal := Zero()
		for _, id := range ids {
			factorTotal = m.Add(factorTotal, c.Factors[id])
		}
		for _, id := range ids {
			share := Zero()
			if factorTotal.Sign() != 0 {
				share = m.Div(c.Factors[id], factorTotal)
			}
			entry := layerEntry(row, "factor:"+id, false)
			bs := shares["BASELINE"]
			entry.PNL = m.Add(entry.PNL, m.Mul(m.Mul(pnl, bs), share))
			entry.CostFunding = m.Add(entry.CostFunding, m.Mul(m.Mul(funding, bs), share))
			entry.ExposureSeconds = m.Add(entry.ExposureSeconds, m.Mul(m.Mul(exposureSeconds, bs), share))
			if trigger == "BASELINE" {
				entry.CostFee = m.Add(entry.CostFee, m.Mul(fee, share))
			}
		}
	}
	row.PNL = m.Add(row.PNL, pnl)
	row.CostFee = m.Add(row.CostFee, fee)
	row.CostFunding = m.Add(row.CostFunding, funding)
	if err := m.Err(); err != nil {
		return err
	}
	return VerifyLayerDay(row, tolerance)
}
func RetainLayerLedger(s *StrategyState, id string, at time.Time) {
	today := time.Date(at.Year(), at.Month(), at.Day(), 0, 0, 0, 0, time.UTC)
	cutoff := today.AddDate(0, 0, -LayerLedgerDays+1).Format("2006-01-02")
	for _, key := range s.LayerLedger.Keys() {
		row := s.LayerLedger.Value(key)
		if row.ObjectID != id {
			continue
		}
		if row.Date < cutoff {
			s.LayerLedger.Delete(key)
		} else if row.Date < today.Format("2006-01-02") {
			row.Sealed = true
		}
	}
}
func settlementComponents(s *StrategyState, id string, at time.Time) *DecisionComponents {
	if d := LatestExecuted(s, id, at); d != nil {
		return Clone(d.Components)
	}
	return nil
}

// SettleLayers marks actual holdings between fills, decision changes and UTC
// day boundaries. Fees/incomes use immutable external identities for replay.
func SettleLayers(s *StrategyState, obj *ObservedObject, snap *TradingSnapshot, at time.Time, p *Policy) error {
	if p.Composition == nil {
		return nil
	}
	m := Math()
	for _, row := range s.LayerLedger.Values() {
		if row.ObjectID == obj.ObjectID {
			if err := VerifyLayerDay(row, p.NumericalTolerance); err != nil {
				obj.LayerLedgerBlocked = true
				s.AppendAudit(map[string]any{"action": "LAYER_LEDGER_IMBALANCE", "object_id": obj.ObjectID, "at": ISO(at)})
				return nil
			}
		}
	}
	sample, ok := snap.Samples[obj.ObjectID]
	if !ok || sample.Quality != "VALID" || sample.AvailableAt.After(at) || sample.Price.Sign() <= 0 || snap.Equity.Sign() <= 0 {
		return nil
	}
	spec := snap.Specs[obj.ObjectID]
	mult := Amount(spec["contract_multiplier"])
	funding, err := FundingAnnual(snap, obj.ObjectID, p)
	if err != nil {
		return err
	}
	st := s.LayerSettlements.Value(obj.ObjectID)
	type fact struct {
		at    time.Time
		id    string
		kind  string
		value map[string]any
	}
	facts := []fact{}
	for _, f := range snap.Fills {
		if Text(f["owner_id"]) == obj.OwnerID && Digest(f["instrument_key"]) == Digest(obj.InstrumentKey) {
			facts = append(facts, fact{At(f["happened_at"]), Text(f["external_fill_id"]), "FILL", f})
		}
	}
	for _, income := range snap.Incomes {
		if Digest(income["instrument_key"]) == Digest(obj.InstrumentKey) && Has([]string{"FUNDING", "SPECIAL_FUNDING"}, Text(income["kind"])) {
			facts = append(facts, fact{At(income["happened_at"]), Text(income["external_id"]), "INCOME", income})
		}
	}
	sort.SliceStable(facts, func(i, j int) bool {
		if facts[i].at.Equal(facts[j].at) {
			return facts[i].kind+facts[i].id < facts[j].kind+facts[j].id
		}
		return facts[i].at.Before(facts[j].at)
	})
	if st == nil {
		start := sample.AvailableAt
		price := sample.Price
		q := snap.Actual[obj.ObjectID]
		for _, f := range facts {
			if f.at.After(at) {
				continue
			}
			if f.at.Before(start) {
				start = f.at
				if point := PriceAt(s.Samples.Value(obj.ObjectID), start); point != nil {
					price = point.Price
				} else if f.kind == "FILL" {
					price = Amount(f.value["price"])
				}
			}
			if f.kind == "FILL" {
				signed := Amount(f.value["quantity"])
				if Text(f.value["side"]) != "BUY" {
					signed = signed.Neg()
				}
				q = m.Sub(q, signed)
			}
		}
		st = &LayerSettlement{At: start, Price: price, Quantity: q, Equity: snap.Equity, Components: settlementComponents(s, obj.ObjectID, start), SeenFills: map[string]time.Time{}, SeenIncomes: map[string]time.Time{}, ShadowExposure: map[string]Decimal{}}
		s.LayerSettlements.Set(obj.ObjectID, st)
	}
	if at.Before(st.At) {
		return &Error{"CLOCK_REWIND", 422}
	}
	mark := func(end time.Time, price Decimal) error {
		if end.Before(st.At) {
			return &Error{"LAYER_LEDGER_IMBALANCE", 423}
		}
		row, e := layerDay(s, obj.ObjectID, st.At)
		if e != nil {
			return e
		}
		seconds := Seconds(end.Sub(st.At))
		pnl := m.Mul(m.Mul(st.Quantity, m.Sub(price, st.Price)), mult)
		exposure := m.Div(m.Mul(m.Mul(st.Quantity, st.Price), mult), st.Equity)
		if e = AllocateLayers(row, st.Components, pnl, Zero(), Zero(), m.Mul(exposure, seconds), p.NumericalTolerance); e != nil {
			return e
		}
		if st.Components != nil {
			for key, shadow := range st.Components.Shadow {
				entry := layerEntry(row, "shadow:"+key, true)
				ex := st.ShadowExposure[key]
				entry.ExposureSeconds = m.Add(entry.ExposureSeconds, m.Mul(ex, seconds))
				entry.PNL = m.Add(entry.PNL, m.Mul(m.Mul(ex, st.Equity), m.Div(m.Sub(price, st.Price), st.Price)))
				entry.CostFunding = m.Add(entry.CostFunding, m.Div(m.Mul(m.Mul(m.Mul(ex, st.Equity), funding), seconds), Int(31536000)))
				_ = shadow
			}
		}
		st.At = end
		st.Price = price
		return m.Err()
	}
	updateShadow := func(c *DecisionComponents) error {
		row, e := layerDay(s, obj.ObjectID, st.At)
		if e != nil {
			return e
		}
		keys := map[string]bool{}
		for k := range st.ShadowExposure {
			keys[k] = true
		}
		if c != nil {
			for k := range c.Shadow {
				keys[k] = true
			}
		}
		ids := []string{}
		for k := range keys {
			ids = append(ids, k)
		}
		sort.Strings(ids)
		for _, k := range ids {
			next := Zero()
			if c != nil {
				next = c.Shadow[k].Exposure
			}
			fee := m.Mul(m.Mul(m.Sub(next, st.ShadowExposure[k]).Abs(), st.Equity), m.Add(p.FeeRate, p.SlippageFraction))
			entry := layerEntry(row, "shadow:"+k, true)
			entry.CostFee = m.Add(entry.CostFee, fee)
			st.ShadowExposure[k] = next
		}
		st.Components = c
		return m.Err()
	}
	// Build a chronological timeline, so a new decision never receives old PNL.
	for _, o := range s.Outbox.Values() {
		if o.State == "ACK" && o.AcceptedAt != nil && o.Decision.ObjectID == obj.ObjectID && o.Decision.Components != nil && o.AcceptedAt.After(st.At) && !o.AcceptedAt.After(at) {
			facts = append(facts, fact{*o.AcceptedAt, o.Decision.DecisionID, "DECISION", Map(o.Decision.Components)})
		}
	}
	for next := time.Date(st.At.Year(), st.At.Month(), st.At.Day()+1, 0, 0, 0, 0, time.UTC); !next.After(at); next = next.AddDate(0, 0, 1) {
		facts = append(facts, fact{next, "", "DAY", nil})
	}
	sort.SliceStable(facts, func(i, j int) bool {
		if facts[i].at.Equal(facts[j].at) {
			return facts[i].kind+facts[i].id < facts[j].kind+facts[j].id
		}
		return facts[i].at.Before(facts[j].at)
	})
	if err = updateShadow(st.Components); err != nil {
		obj.LayerLedgerBlocked = true
		return nil
	}
	if current := settlementComponents(s, obj.ObjectID, st.At); current != nil && Digest(current) != Digest(st.Components) {
		if err = updateShadow(current); err != nil {
			obj.LayerLedgerBlocked = true
			return nil
		}
	}
	for _, f := range facts {
		if f.at.After(at) {
			continue
		}
		if f.kind == "FILL" {
			if _, seen := st.SeenFills[f.id]; seen {
				continue
			}
		}
		if f.kind == "INCOME" {
			if _, seen := st.SeenIncomes[f.id]; seen {
				continue
			}
		}
		if f.at.Before(st.At) {
			obj.LayerLedgerBlocked = true
			continue
		}
		price := st.Price
		if point := PriceAt(s.Samples.Value(obj.ObjectID), f.at); point != nil {
			price = point.Price
		}
		if f.kind == "FILL" {
			price = Amount(f.value["price"])
		}
		if err = mark(f.at, price); err != nil {
			obj.LayerLedgerBlocked = true
			return nil
		}
		if f.kind == "DECISION" {
			var c DecisionComponents
			raw, _ := Marshal(f.value)
			if err = DecodeJSON(raw, &c); err != nil {
				return err
			}
			if err = updateShadow(&c); err != nil {
				obj.LayerLedgerBlocked = true
				return nil
			}
			continue
		}
		if f.kind == "DAY" {
			continue
		}
		row, e := layerDay(s, obj.ObjectID, f.at)
		if e != nil {
			obj.LayerLedgerBlocked = true
			return nil
		}
		if f.kind == "FILL" {
			signed := Amount(f.value["quantity"])
			if Text(f.value["side"]) != "BUY" {
				signed = signed.Neg()
			}
			st.Quantity = m.Add(st.Quantity, signed)
			origin := s.Decisions.Value(Text(f.value["source_decision_id"]))
			c := st.Components
			if origin != nil && origin.Components != nil {
				c = origin.Components
			}
			if Text(f.value["fee_currency"]) != Text(spec["settlement_currency"]) {
				obj.LayerLedgerBlocked = true
				continue
			}
			err = AllocateLayers(row, c, Zero(), Amount(f.value["fee"]), Zero(), Zero(), p.NumericalTolerance)
			st.SeenFills[f.id] = f.at
		} else {
			if Text(f.value["currency"]) != Text(spec["settlement_currency"]) {
				obj.LayerLedgerBlocked = true
				continue
			}
			err = AllocateLayers(row, st.Components, Zero(), Zero(), Amount(f.value["amount"]).Neg(), Zero(), p.NumericalTolerance)
			st.SeenIncomes[f.id] = f.at
		}
		if err != nil {
			obj.LayerLedgerBlocked = true
			return nil
		}
	}
	if err = mark(at, sample.Price); err != nil {
		obj.LayerLedgerBlocked = true
		return nil
	}
	st.Equity = snap.Equity
	if st.Quantity.Cmp(snap.Actual[obj.ObjectID]) != 0 {
		obj.LayerLedgerBlocked = true
	}
	RetainLayerLedger(s, obj.ObjectID, at)
	cutoff := at.AddDate(0, 0, -LayerLedgerDays)
	for id, t := range st.SeenFills {
		if t.Before(cutoff) {
			delete(st.SeenFills, id)
		}
	}
	for id, t := range st.SeenIncomes {
		if t.Before(cutoff) {
			delete(st.SeenIncomes, id)
		}
	}
	return m.Err()
}
