package domain

import "time"

const MaxFactorObservations = 512
const LayerLedgerDays = 400

var Layers = []string{"BASELINE", "ASSESSMENT", "SCHEDULED", "ADJUSTMENT"}

type BaselineFactor struct {
	FactorID      string  `json:"factor_id"`
	Weight        Decimal `json:"weight"`
	State         string  `json:"state"`
	MaxAgeSeconds int     `json:"max_age_seconds"`
	EvidenceRef   string  `json:"evidence_ref"`
}
type AssessmentFactor struct {
	FactorID      string `json:"factor_id"`
	State         string `json:"state"`
	MaxAgeSeconds int    `json:"max_age_seconds"`
	EvidenceRef   string `json:"evidence_ref"`
	ForceReduce   bool   `json:"force_reduce"`
}
type LayerCap struct {
	State string  `json:"state"`
	Cap   Decimal `json:"cap"`
}
type AdjustmentRule struct {
	LayerCap
	Scale        Decimal `json:"scale"`
	EventOnlyCap Decimal `json:"event_only_cap"`
}
type RebalanceRule struct {
	BandFloor             Decimal  `json:"band_floor"`
	ExpectedHoldSeconds   int      `json:"expected_hold_seconds"`
	FundingFallbackAnnual Decimal  `json:"funding_fallback_annual"`
	AdjustIntervalSeconds int      `json:"adjust_interval_seconds"`
	ReverseConfirmSeconds int      `json:"reverse_confirm_seconds"`
	BandRatioOfUnit       *Decimal `json:"band_ratio_of_unit,omitempty"`
}
type Composition struct {
	Baseline           []BaselineFactor  `json:"baseline"`
	AllowShortBaseline bool              `json:"allow_short_baseline"`
	MissingPolicy      string            `json:"missing_policy"`
	Assessment         *AssessmentFactor `json:"assessment"`
	Scheduled          LayerCap          `json:"scheduled"`
	Adjustment         AdjustmentRule    `json:"adjustment"`
	Levels             []Level           `json:"levels"`
	Rebalance          RebalanceRule     `json:"rebalance"`
	Sizing             *SizingRule       `json:"sizing,omitempty"`
	Reentry            *ReentryRule      `json:"reentry,omitempty"`
	ChainLoss          *ChainLossRule    `json:"chain_loss,omitempty"`
	PreEvent           []PreEventRule    `json:"pre_event,omitempty"`
	Transfer           *TransferRule     `json:"transfer,omitempty"`
}
type FactorDefinition struct {
	FactorID       string  `json:"factor_id"`
	Purpose        string  `json:"purpose"`
	ValueMin       Decimal `json:"value_min"`
	ValueMax       Decimal `json:"value_max"`
	CadenceSeconds int     `json:"cadence_seconds"`
	SourceManifest string  `json:"source_manifest"`
}
type FactorObservation struct {
	ObservationID string    `json:"observation_id"`
	AvailableAt   time.Time `json:"available_at"`
	Value         Decimal   `json:"value"`
	ForceReduce   bool      `json:"force_reduce"`
	InputsHash    string    `json:"inputs_hash"`
	EvidenceRef   string    `json:"evidence_ref"`
	Supersedes    *string   `json:"supersedes"`
	Referenced    bool      `json:"referenced,omitempty"`
	Reconfirm     bool      `json:"reconfirm,omitempty"`
	ReasonCode    string    `json:"reason_code,omitempty"`
}
type FactorSeries struct {
	Definition   FactorDefinition    `json:"definition"`
	Observations []FactorObservation `json:"observations"`
}
type FactorObservationCommand struct {
	Command
	FactorID string `json:"factor_id"`
	FactorObservation
}
type ReverseCandidate struct {
	Direction int       `json:"direction"`
	Since     time.Time `json:"since"`
}
type ShadowComponent struct {
	Value    Decimal `json:"value"`
	Core     Decimal `json:"core"`
	Combined Decimal `json:"combined"`
	Level    Decimal `json:"level"`
	Exposure Decimal `json:"exposure"`
}
type DecisionComponents struct {
	Baseline        Decimal                    `json:"baseline"`
	Assessment      Decimal                    `json:"assessment"`
	Scheduled       Decimal                    `json:"scheduled"`
	Adjustment      Decimal                    `json:"adjustment"`
	Core            Decimal                    `json:"core"`
	Combined        Decimal                    `json:"combined"`
	Level           Decimal                    `json:"level"`
	Exposure        Decimal                    `json:"exposure"`
	TriggerLayer    string                     `json:"trigger_layer"`
	Shadow          map[string]ShadowComponent `json:"shadow"`
	Factors         map[string]Decimal         `json:"factors"`
	ObservationRefs map[string]string          `json:"observation_refs"`
}
type FundingObservation struct {
	Rate          Decimal   `json:"rate"`
	NextFundingAt time.Time `json:"next_funding_at"`
	IntervalHours Decimal   `json:"interval_hours"`
	ObservedAt    time.Time `json:"observed_at"`
}
type ComposeResult struct {
	Components *DecisionComponents
	Raw        Decimal
	Level      int
	Force      bool
	Reasons    []string
}

func validLayerState(s string) bool { return Has([]string{"ENABLED", "SHADOW", "DISABLED"}, s) }
func unit(d Decimal) bool           { return d.Sign() >= 0 && d.Cmp(One()) <= 0 }
func (c Composition) Validate() error {
	m := Math()
	sum := Zero()
	seen := map[string]bool{}
	if !Has([]string{"ZERO", "HOLD_REDUCE_ONLY"}, c.MissingPolicy) || !validLayerState(c.Scheduled.State) || !unit(c.Scheduled.Cap) || !validLayerState(c.Adjustment.State) || !unit(c.Adjustment.Cap) || !unit(c.Adjustment.EventOnlyCap) || c.Adjustment.EventOnlyCap.Cmp(c.Adjustment.Cap) > 0 || c.Adjustment.Scale.Sign() <= 0 || len(c.Levels) == 0 || c.Rebalance.BandFloor.Sign() < 0 || c.Rebalance.ExpectedHoldSeconds <= 0 || c.Rebalance.AdjustIntervalSeconds < 0 || c.Rebalance.ReverseConfirmSeconds < 0 || !c.validDynamic() {
		return invalid()
	}
	for _, f := range c.Baseline {
		if f.FactorID == "" || seen[f.FactorID] || !validLayerState(f.State) || f.MaxAgeSeconds <= 0 || f.State == "ENABLED" && f.EvidenceRef == "" {
			return invalid()
		}
		seen[f.FactorID] = true
		sum = m.Add(sum, f.Weight.Abs())
	}
	if sum.Cmp(One()) > 0 {
		return invalid()
	}
	if f := c.Assessment; f != nil && (f.FactorID == "" || !validLayerState(f.State) || f.MaxAgeSeconds <= 0 || f.State == "ENABLED" && f.EvidenceRef == "") {
		return invalid()
	}
	for i, l := range c.Levels {
		if !unit(l.Exposure) || !unit(l.Enter) || !unit(l.Exit) || l.Exit.Cmp(l.Enter) >= 0 || i > 0 && (l.Enter.Cmp(c.Levels[i-1].Enter) <= 0 || l.Exit.Cmp(c.Levels[i-1].Exit) <= 0 || l.Exposure.Cmp(c.Levels[i-1].Exposure) <= 0) {
			return invalid()
		}
	}
	return m.Err()
}
func clip(v, cap Decimal) Decimal { return Min(cap, Max(cap.Neg(), v)) }
func FactorValue(series *FactorSeries, at time.Time, age int) *FactorObservation {
	if series == nil {
		return nil
	}
	replaced := map[string]bool{}
	for _, o := range series.Observations {
		if !o.AvailableAt.After(at) && o.Supersedes != nil {
			replaced[*o.Supersedes] = true
		}
	}
	for i := len(series.Observations) - 1; i >= 0; i-- {
		o := &series.Observations[i]
		if !o.AvailableAt.After(at) && !replaced[o.ObservationID] {
			if at.Sub(o.AvailableAt) > time.Duration(age)*time.Second {
				return nil
			}
			return o
		}
	}
	return nil
}
func Combined(core, adjustment, eventOnlyCap Decimal) Decimal {
	m := Math()
	if core.Sign() == 0 {
		return clip(adjustment, eventOnlyCap)
	}
	x := m.Add(core, adjustment)
	if x.Sign() != core.Sign() {
		return Zero()
	}
	return clip(x, One())
}

// composition uses liquidity only. Confidence has already entered A;
// historical volatility and regime state do not scale actual or shadow exposure.
func CompositionExposure(x Decimal, level int, sample *MarketSample, equity Decimal, c *Composition, p *Policy) (Decimal, error) {
	if level == 0 || x.Sign() == 0 || sample == nil || sample.Quality != "VALID" || sample.Liquidity == nil {
		return Zero(), nil
	}
	if level < 0 || level > len(c.Levels) {
		return Zero(), &Error{"LEVEL_INVALID", 422}
	}
	m := Math()
	v := c.Levels[level-1].Exposure
	liq := Min(One(), m.Div(Min(*sample.Liquidity, p.LiquidityBudget), Max(m.Mul(equity, v), p.Epsilon)))
	return m.Mul(m.Mul(Int(x.Sign()), v), liq), m.Err()
}
func LatestExecuted(s *StrategyState, id string, at time.Time) *DecisionView {
	var result *DecisionView
	for _, o := range s.Outbox.Values() {
		if o.State != "ACK" || o.AcceptedAt == nil || o.AcceptedAt.After(at) || o.Decision.ObjectID != id || o.Decision.Components == nil {
			continue
		}
		if result == nil || o.Decision.AvailableCutoff.After(result.AvailableCutoff) {
			copy := o.Decision
			result = &copy
		}
	}
	return result
}
func ComponentValues(c *DecisionComponents) []Decimal {
	return []Decimal{c.Baseline, c.Assessment, c.Scheduled, c.Adjustment}
}
func TriggerLayer(previous, current *DecisionComponents) string {
	m := Math()
	before := []Decimal{Zero(), Zero(), Zero(), Zero()}
	if previous != nil {
		before = ComponentValues(previous)
	}
	best, layer := Zero(), "BASELINE"
	for i, v := range ComponentValues(current) {
		delta := m.Sub(v, before[i]).Abs()
		if delta.Cmp(best) > 0 {
			best, layer = delta, Layers[i]
		}
	}
	return layer
}

// EffectiveComposition reads learned values from the versioned parameter snapshot;
// policy records remain immutable and rollback restores both weight and state.
func EffectiveComposition(s *StrategyState, obj *ObservedObject, p *Policy) *Composition {
	c := Clone(p.Composition)
	if c == nil {
		return nil
	}
	params := s.Parameters.Value(obj.ParameterVersion)
	if params == nil {
		return c
	}
	for i := range c.Baseline {
		key := "factor:" + c.Baseline[i].FactorID
		if v, ok := params.Values[key]; ok {
			c.Baseline[i].Weight = v
			c.Baseline[i].State = learnedState(s, obj, key, c.Baseline[i].State, v)
			if c.Baseline[i].State == "SHADOW" && v.Sign() == 0 {
				c.Baseline[i].Weight = params.Steps[key]
			}
			if c.Baseline[i].State == "ENABLED" && c.Baseline[i].EvidenceRef == "" {
				c.Baseline[i].EvidenceRef = learnedEvidence(s, obj, key)
			}
		}
	}
	if c.Assessment != nil {
		if v, ok := params.Values["layer:ASSESSMENT"]; ok {
			c.Assessment.State = learnedState(s, obj, "layer:ASSESSMENT", c.Assessment.State, v)
			if c.Assessment.State == "ENABLED" && c.Assessment.EvidenceRef == "" {
				c.Assessment.EvidenceRef = learnedEvidence(s, obj, "layer:ASSESSMENT")
			}
		}
	}
	if v, ok := params.Values["layer:BASELINE"]; ok {
		state := learnedState(s, obj, "layer:BASELINE", compositionBaselineState(c), v)
		for i := range c.Baseline {
			// A layer can silence its factors. Restoring the layer cannot grant
			// evidence qualification to a factor that is itself still SHADOW.
			if state == "SHADOW" && c.Baseline[i].State == "ENABLED" {
				c.Baseline[i].State = "SHADOW"
			}
		}
	}
	for _, layer := range []string{"SCHEDULED", "ADJUSTMENT"} {
		key := "layer:" + layer
		if v, ok := params.Values[key]; ok {
			cap := &c.Scheduled
			if layer == "ADJUSTMENT" {
				cap = &c.Adjustment.LayerCap
			}
			cap.Cap = v
			cap.State = learnedState(s, obj, key, cap.State, v)
			if cap.State == "SHADOW" && v.Sign() == 0 {
				cap.Cap = params.Steps[key]
			}
		}
	}
	c.Adjustment.EventOnlyCap = Min(c.Adjustment.EventOnlyCap, c.Adjustment.Cap)
	// BASELINE/ASSESSMENT layer values are registered multipliers, bounded to [0,1].
	return c
}
func learnedState(s *StrategyState, obj *ObservedObject, key, state string, value Decimal) string {
	if value.Sign() == 0 && state == "ENABLED" {
		return "SHADOW"
	}
	if candidate := learnedCandidate(s, obj, key); candidate != nil && Text(candidate["layer_state"]) != "" {
		return Text(candidate["layer_state"])
	}
	return state
}
func compositionBaselineState(c *Composition) string {
	state := "DISABLED"
	for _, f := range c.Baseline {
		if f.State == "ENABLED" {
			return "ENABLED"
		}
		if f.State == "SHADOW" {
			state = "SHADOW"
		}
	}
	return state
}
func learnedCandidate(s *StrategyState, obj *ObservedObject, key string) map[string]any {
	version := obj.ParameterVersion
	seen := map[string]bool{}
	for version != "" && !seen[version] {
		seen[version] = true
		for _, c := range s.Candidates.Values() {
			if Text(c["object_id"]) == obj.ObjectID && Text(c["parameter"]) == key && Text(c["activated_version"]) == version && Text(c["state"]) == "MONITORING" {
				return c
			}
		}
		p := s.Parameters.Value(version)
		if p == nil || p.ParentVersion == nil {
			break
		}
		version = *p.ParentVersion
	}
	return nil
}
func learnedEvidence(s *StrategyState, obj *ObservedObject, key string) string {
	if c := learnedCandidate(s, obj, key); c != nil && c["validation"] != nil {
		return "validation:" + Text(c["candidate_id"])
	}
	return ""
}
func Compose(s *StrategyState, obj *ObservedObject, at time.Time, view PoolView, sample *MarketSample, equity, currentExposure Decimal, p *Policy) (ComposeResult, error) {
	c := EffectiveComposition(s, obj, p)
	result := ComposeResult{Components: &DecisionComponents{Shadow: map[string]ShadowComponent{}, Factors: map[string]Decimal{}, ObservationRefs: map[string]string{}}}
	if c == nil {
		return result, invalid()
	}
	out := result.Components
	m := Math()
	shadow := map[string]struct {
		layer string
		value Decimal
	}{}
	stale, missing := false, false
	read := func(id string, age int, purpose string) *FactorObservation {
		series := s.FactorSeries.Value(id)
		if series == nil || series.Definition.Purpose != purpose {
			return nil
		}
		o := FactorValue(series, at, age)
		if o != nil {
			o.Referenced = true
			out.ObservationRefs[id] = o.ObservationID
		}
		return o
	}
	params := s.Parameters.Value(obj.ParameterVersion)
	layerWeight := func(layer string) Decimal {
		if params != nil {
			if v, ok := params.Values["layer:"+layer]; ok {
				if v.Sign() == 0 {
					return params.Steps["layer:"+layer]
				}
				return v
			}
		}
		return One()
	}
	for _, f := range c.Baseline {
		if f.State == "DISABLED" {
			continue
		}
		value := Zero()
		if o := read(f.FactorID, f.MaxAgeSeconds, "BASELINE"); o != nil {
			value = m.Mul(f.Weight, o.Value)
		} else if f.State == "ENABLED" {
			// HOLD_REDUCE_ONLY keeps the last value for one more
			// max_age, reduce-only; afterwards the factor counts as zero.
			if c.MissingPolicy == "HOLD_REDUCE_ONLY" {
				if o := read(f.FactorID, 2*f.MaxAgeSeconds, "BASELINE"); o != nil {
					value = m.Mul(f.Weight, o.Value)
					stale = true
				} else {
					missing = true
				}
			} else {
				missing = true
			}
		}
		if f.State == "ENABLED" {
			out.Baseline = m.Add(out.Baseline, value)
			out.Factors[f.FactorID] = value
		} else {
			shadow["factor:"+f.FactorID] = struct {
				layer string
				value Decimal
			}{"BASELINE", value}
		}
	}
	if stale {
		result.Force = true
		result.Reasons = append(result.Reasons, "BASELINE_STALE")
	}
	if missing {
		result.Reasons = append(result.Reasons, "BASELINE_MISSING")
	}
	if !c.AllowShortBaseline {
		out.Baseline = Max(Zero(), out.Baseline)
	}
	out.Baseline = clip(m.Mul(out.Baseline, layerWeight("BASELINE")), One())
	shadowBaseline := Zero()
	for _, key := range compositionKeys(shadow) {
		v := shadow[key]
		if v.layer == "BASELINE" {
			v.value = m.Mul(v.value, layerWeight("BASELINE"))
			shadow[key] = v
			shadowBaseline = m.Add(shadowBaseline, v.value)
		}
	}
	if compositionBaselineState(c) == "SHADOW" {
		shadow["BASELINE"] = struct {
			layer string
			value Decimal
		}{"BASELINE", shadowBaseline}
	}
	if f := c.Assessment; f != nil && f.State != "DISABLED" {
		value := Zero()
		if o := read(f.FactorID, f.MaxAgeSeconds, "ASSESSMENT"); o != nil {
			value = m.Mul(o.Value, layerWeight("ASSESSMENT"))
			if f.State == "ENABLED" {
				result.Force = o.ForceReduce || f.ForceReduce || result.Force
			}
		}
		if f.State == "ENABLED" {
			out.Assessment = value
		} else {
			shadow["ASSESSMENT"] = struct {
				layer string
				value Decimal
			}{"ASSESSMENT", value}
		}
	}
	scheduled, shadows, err := ScheduledContributions(s, obj, at, c)
	if err != nil {
		return result, err
	}
	out.Scheduled = scheduled
	for k, v := range shadows {
		shadow[k] = struct {
			layer string
			value Decimal
		}{"SCHEDULED", v}
	}
	if len(shadows) > 0 {
		sum := Zero()
		for _, key := range compositionKeys(shadows) {
			sum = m.Add(sum, shadows[key])
		}
		shadow["SCHEDULED"] = struct {
			layer string
			value Decimal
		}{"SCHEDULED", clip(sum, c.Scheduled.Cap)}
	}
	confidence := m.Div(m.Mul(view.Quality, view.Net.Abs()), m.Add(m.Add(view.Plus, view.Minus), p.Epsilon))
	adjustment := m.Mul(m.Mul(clip(m.Div(view.Net, c.Adjustment.Scale), One()), c.Adjustment.Cap), confidence)
	if c.Adjustment.State == "ENABLED" {
		out.Adjustment = adjustment
	} else if c.Adjustment.State == "SHADOW" {
		shadow["ADJUSTMENT"] = struct {
			layer string
			value Decimal
		}{"ADJUSTMENT", adjustment}
	}
	out.Core = clip(m.Add(m.Add(out.Baseline, out.Assessment), out.Scheduled), One())
	out.Combined = Combined(out.Core, out.Adjustment, c.Adjustment.EventOnlyCap)
	level, err := LevelFor(out.Combined.Abs(), obj.Level, c.Levels)
	if err != nil {
		return result, err
	}
	dir := out.Core.Sign()
	if dir != 0 && obj.CoreDirection == 0 {
		obj.CoreDirection = obj.ActualQuantity.Sign()
		if obj.CoreDirection == 0 {
			obj.CoreDirection = dir
		}
	}
	if dir != 0 && dir != obj.CoreDirection {
		if obj.ReverseCandidate == nil || obj.ReverseCandidate.Direction != dir {
			obj.ReverseCandidate = &ReverseCandidate{dir, at}
		}
		if at.Sub(obj.ReverseCandidate.Since) < time.Duration(c.Rebalance.ReverseConfirmSeconds)*time.Second {
			level = obj.Level
			result.Reasons = append(result.Reasons, "REVERSE_CONFIRMING")
		} else {
			obj.CoreDirection = dir
			obj.ReverseCandidate = nil
		}
	} else {
		obj.ReverseCandidate = nil
	}
	raw, err := CompositionExposure(out.Combined, level, sample, equity, c, p)
	if err != nil {
		return result, err
	}
	if result.Force {
		if raw.Sign() != currentExposure.Sign() {
			raw = Zero()
		} else if raw.Abs().Cmp(currentExposure.Abs()) > 0 {
			raw = currentExposure
		}
		result.Reasons = append(result.Reasons, "FORCE_REDUCE_ONLY")
	}
	if Has(result.Reasons, "REVERSE_CONFIRMING") && !result.Force {
		raw = currentExposure
	}
	previous := LatestExecuted(s, obj.ObjectID, at)
	var prev *DecisionComponents
	if previous != nil {
		prev = previous.Components
	}
	out.TriggerLayer = TriggerLayer(prev, out)
	for _, key := range compositionKeys(shadow) {
		v := shadow[key]
		b, l, sc, a := out.Baseline, out.Assessment, out.Scheduled, out.Adjustment
		switch v.layer {
		case "BASELINE":
			b = clip(m.Add(b, v.value), One())
			if !c.AllowShortBaseline {
				b = Max(Zero(), b)
			}
		case "ASSESSMENT":
			l = v.value
		case "SCHEDULED":
			sc = clip(m.Add(sc, v.value), c.Scheduled.Cap)
		case "ADJUSTMENT":
			a = v.value
		}
		core := clip(m.Add(m.Add(b, l), sc), One())
		x := Combined(core, a, c.Adjustment.EventOnlyCap)
		sl, err := LevelFor(x.Abs(), obj.Level, c.Levels)
		if err != nil {
			return result, err
		}
		exposure, err := CompositionExposure(x, sl, sample, equity, c, p)
		if err != nil {
			return result, err
		}
		out.Shadow[key] = ShadowComponent{v.value, core, x, Int(sl), exposure}
	}
	out.Level = Int(level)
	out.Exposure = raw
	result.Raw = raw
	result.Level = level
	return result, m.Err()
}
func FundingAnnual(snap *TradingSnapshot, id string, p *Policy) (Decimal, error) {
	if f, ok := snap.Funding[id]; ok && f.IntervalHours.Sign() > 0 && !f.ObservedAt.After(snap.At) {
		m := Math()
		return m.Mul(m.Div(m.Mul(f.Rate, Int(24)), f.IntervalHours), Int(365)), m.Err()
	}
	return p.Composition.Rebalance.FundingFallbackAnnual, nil
}
func NoTradeBand(p *Policy, funding Decimal) (Decimal, error) {
	m := Math()
	c := p.Composition.Rebalance
	band := Max(c.BandFloor, m.Add(m.Mul(Int(2), m.Add(p.FeeRate, p.SlippageFraction)), m.Div(m.Mul(funding.Abs(), Int(c.ExpectedHoldSeconds)), Int(31536000))))
	return band, m.Err()
}
func RebalanceReason(obj *ObservedObject, at time.Time, current, target, funding Decimal, p *Policy, force bool, components *DecisionComponents, previous *DecisionComponents) (string, error) {
	if force || target.Sign() == 0 && current.Sign() != 0 {
		return "", nil
	}
	band, err := NoTradeBand(p, funding)
	if err != nil {
		return "", err
	}
	m := Math()
	if m.Sub(target, current).Abs().Cmp(band) < 0 {
		return "NO_TRADE_BAND", nil
	}
	// under RISK_UNIT the target is Q × level ratio, so an unchanged
	// level already yields an unchanged target; comparing with the last
	// executed level would wrongly hold a batch whose opening a gate delayed.
	onlyAdjustment := p.Composition.Sizing == nil && previous != nil && components.Core.Cmp(previous.Core) == 0 && components.Baseline.Cmp(previous.Baseline) == 0 && components.Assessment.Cmp(previous.Assessment) == 0 && components.Scheduled.Cmp(previous.Scheduled) == 0 && components.Adjustment.Cmp(previous.Adjustment) != 0
	if onlyAdjustment {
		components.TriggerLayer = "ADJUSTMENT"
		if components.Level.Cmp(previous.Level) == 0 {
			return "ADJUSTMENT_LEVEL_UNCHANGED", nil
		}
		if obj.LastAdjustmentTradeAt != nil && at.Sub(*obj.LastAdjustmentTradeAt) < time.Duration(p.Composition.Rebalance.AdjustIntervalSeconds)*time.Second {
			return "ADJUSTMENT_INTERVAL", nil
		}
	}
	return "", m.Err()
}
