package operations

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"io"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	d "github.com/AceNanako0721/Factorforge/src/factorforge/applications/soxl_jev/domain"
	"github.com/AceNanako0721/Factorforge/src/factorforge/applications/soxl_jev/ports"
)

// DynamicQuery serves the v2.10.0 read-only views of v2.9.0 judgment, stop
// and baseline records (S3-032, P3 design 29). It reads recorded state only:
// it never calls a model, the framework or the trading layer.
type DynamicQuery struct {
	Store  ports.DynamicReadStore
	Clock  ports.Clock
	Policy ReadPolicy
}

type DynamicBatchView struct {
	CycleID            string     `json:"cycle_id"`
	Direction          string     `json:"direction"`
	Entry              string     `json:"entry"`
	OpenedAt           time.Time  `json:"opened_at"`
	TierHours          int        `json:"tier_hours"`
	RequestedTierHours int        `json:"requested_tier_hours"`
	Session            string     `json:"session"`
	R                  string     `json:"r"`
	Buffer             string     `json:"buffer"`
	Trail              string     `json:"trail"`
	Budget             string     `json:"budget"`
	BestClose          string     `json:"best_close"`
	LastBarAt          *time.Time `json:"last_bar_at"`
	CloseStop          string     `json:"close_stop"`
	Trailing           bool       `json:"trailing"`
	Transferred        bool       `json:"transferred"`
	TrailTrigger       string     `json:"trail_trigger"`
}
type DynamicEntryPlanView struct {
	Direction     string     `json:"direction"`
	Price         string     `json:"price"`
	Distance      string     `json:"distance"`
	CloseDistance string     `json:"close_distance"`
	RiskFraction  string     `json:"risk_fraction"`
	Equity        string     `json:"equity"`
	TierHours     int        `json:"tier_hours"`
	Session       string     `json:"session"`
	DirectiveID   string     `json:"directive_id"`
	PlannedAt     *time.Time `json:"planned_at"`
}
type DynamicBaselineView struct {
	Phase       string     `json:"phase"`
	Direction   string     `json:"direction"`
	Value       string     `json:"value"`
	StartedAt   *time.Time `json:"started_at"`
	SuspendedAt *time.Time `json:"suspended_at"`
	SourceChain *string    `json:"source_chain"`
}
type DynamicChainView struct {
	ChainID       string    `json:"chain_id"`
	Direction     string    `json:"direction"`
	Confidence    int       `json:"confidence"`
	Since         time.Time `json:"since"`
	JudgmentCount int       `json:"judgment_count"`
}
type DynamicNoiseView struct {
	NoiseID     *string    `json:"noise_id"`
	GeneratedAt *time.Time `json:"generated_at"`
}
type DynamicSourceView struct {
	SourceID    string     `json:"source_id"`
	Failing     bool       `json:"failing"`
	LastSuccess *time.Time `json:"last_success"`
}
type DynamicOutcomeView struct {
	R        string `json:"r"`
	ClosedAt string `json:"closed_at"`
}
type DynamicStatusView struct {
	Batch          *DynamicBatchView     `json:"batch"`
	NextEntry      *DynamicEntryPlanView `json:"next_entry"`
	Baseline       DynamicBaselineView   `json:"baseline"`
	Chain          *DynamicChainView     `json:"chain"`
	Noise          DynamicNoiseView      `json:"noise"`
	Sources        []DynamicSourceView   `json:"sources"`
	Outcomes       []DynamicOutcomeView  `json:"outcomes"`
	LastJudgmentID *string               `json:"last_judgment_id"`
	EquityPeak     string                `json:"equity_peak"`
}
type DynamicNewsRefView struct {
	ItemID       string     `json:"item_id"`
	Source       *string    `json:"source"`
	Title        *string    `json:"title"`
	PublishedAt  *time.Time `json:"published_at"`
	LicenseScope *string    `json:"license_scope"`
}
type DynamicJudgmentView struct {
	JudgmentID          string               `json:"judgment_id"`
	At                  time.Time            `json:"at"`
	OK                  bool                 `json:"ok"`
	Reason              *string              `json:"reason"`
	Provider            *string              `json:"provider"`
	Model               *string              `json:"model"`
	InputCount          int                  `json:"input_count"`
	Direction           *string              `json:"direction"`
	Confidence          *int                 `json:"confidence"`
	HorizonHours        *int                 `json:"horizon_hours"`
	Thesis              *string              `json:"thesis"`
	CounterPoints       *string              `json:"counter_points"`
	Invalidation        *string              `json:"invalidation"`
	PreviousInvalidated *bool                `json:"previous_invalidated"`
	KeyItems            []DynamicNewsRefView `json:"key_items"`
	InvalidationItems   []DynamicNewsRefView `json:"invalidation_items"`
	ErrorCodes          []string             `json:"error_codes"`
}
type DynamicActionView struct {
	ActionID     string    `json:"action_id"`
	At           time.Time `json:"at"`
	Source       string    `json:"source"`
	Kind         string    `json:"kind"`
	Reason       *string   `json:"reason"`
	Direction    *string   `json:"direction"`
	Confidence   *int      `json:"confidence"`
	HorizonHours *int      `json:"horizon_hours"`
	ChainID      *string   `json:"chain_id"`
	EventID      *string   `json:"event_id"`
	Targets      []string  `json:"targets"`
	CycleID      *string   `json:"cycle_id"`
	Entry        *string   `json:"entry"`
	Close        *string   `json:"close"`
	CloseStop    *string   `json:"close_stop"`
	Best         *string   `json:"best"`
	Value        *string   `json:"value"`
	Stopped      *bool     `json:"stopped"`
	Reconfirm    *bool     `json:"reconfirm"`
}

// Stored shapes (P3 design 28.1), decoded independently of the workers so
// the read path cannot change what workers persist.
type storedGeometry struct {
	Tier          int     `json:"tier"`
	Session       string  `json:"session"`
	R             float64 `json:"r"`
	Buffer        float64 `json:"buffer"`
	Trail         float64 `json:"trail"`
	RequestedTier int     `json:"requested_tier"`
}
type storedBatch struct {
	CycleID     string         `json:"cycle_id"`
	Direction   int            `json:"direction"`
	Entry       float64        `json:"entry"`
	OpenedAt    time.Time      `json:"opened_at"`
	Geometry    storedGeometry `json:"geometry"`
	Budget      float64        `json:"budget"`
	BestClose   float64        `json:"best_close"`
	LastBarAt   time.Time      `json:"last_bar_at"`
	CloseStop   float64        `json:"close_stop"`
	Trailing    bool           `json:"trailing"`
	Transferred bool           `json:"transferred"`
}
type storedPlan struct {
	Direction   int            `json:"direction"`
	Geometry    storedGeometry `json:"geometry"`
	Price       float64        `json:"price"`
	Distance    float64        `json:"distance"`
	CloseDist   float64        `json:"close_distance"`
	Fraction    float64        `json:"risk_fraction"`
	Equity      float64        `json:"equity"`
	DirectiveID string         `json:"directive_id"`
}
type storedPosition struct {
	NoiseID  string       `json:"noise_id"`
	NoiseAt  time.Time    `json:"noise_at"`
	Initial  *storedPlan  `json:"initial"`
	Batch    *storedBatch `json:"batch"`
	Baseline struct {
		Phase       string    `json:"phase"`
		Direction   int       `json:"direction"`
		SourceChain string    `json:"source_chain"`
		StartedAt   time.Time `json:"started_at"`
		SuspendedAt time.Time `json:"suspended_at"`
		Value       float64   `json:"value"`
	} `json:"baseline"`
	EquityPeak float64 `json:"equity_peak"`
	Outcomes   []struct {
		R        float64 `json:"r"`
		ClosedAt string  `json:"closed_at"`
	} `json:"outcomes"`
}
type storedInjection struct {
	Active *struct {
		Direction  string    `json:"direction"`
		Confidence int       `json:"confidence"`
		EventIDs   []string  `json:"event_ids"`
		ChainID    string    `json:"chain_id"`
		Since      time.Time `json:"since"`
	} `json:"active"`
	LastJudgment string `json:"last_judgment"`
}
type storedSource struct {
	LastSuccess time.Time `json:"last_success"`
	Failing     bool      `json:"failing"`
}
type storedNews struct {
	Source       string    `json:"source"`
	Title        string    `json:"title"`
	PublishedAt  time.Time `json:"published_at"`
	LicenseScope string    `json:"license_scope"`
}

var stableCode = regexp.MustCompile(`^[A-Z][A-Z0-9_]{1,63}$`)

func decimal(v float64) string { return strconv.FormatFloat(v, 'f', -1, 64) }
func direction(v int) string {
	switch {
	case v > 0:
		return "LONG"
	case v < 0:
		return "SHORT"
	}
	return "FLAT"
}
func optionalTime(t time.Time) *time.Time {
	if t.IsZero() || t.Year() <= 1 {
		return nil
	}
	u := t.UTC()
	return &u
}
func optionalString(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}
func strictDecode(raw []byte, target any) error {
	if json.Unmarshal(raw, target) != nil {
		return d.Fail("DYNAMIC_STATE_INVALID", 503)
	}
	return nil
}

func (q DynamicQuery) ready() error {
	if q.Store == nil || q.Clock == nil {
		return d.Fail("DYNAMIC_READ_NOT_CONFIGURED", 503)
	}
	if !q.Policy.Valid() {
		return d.Fail("QUERY_POLICY_REQUIRED", 503)
	}
	return nil
}

// metadata reads the three state rows and returns the shared envelope.
func (q DynamicQuery) metadata(ctx context.Context) (Metadata, map[string]json.RawMessage, error) {
	b := q.Store.Binding()
	raws := map[string]json.RawMessage{}
	versions := []string{}
	var observed *time.Time
	var position int64
	for _, key := range []string{"POSITION", "INJECTION", "NEWS_SOURCES"} {
		raw, version, at, err := q.Store.StateWithVersion(ctx, key)
		if err != nil {
			return Metadata{}, nil, err
		}
		raws[key] = raw
		versions = append(versions, strings.ToLower(key)+"-"+strconv.FormatInt(version, 10))
		if key == "POSITION" {
			position = version
		}
		if version > 0 {
			observed = latest(observed, &at)
		}
	}
	meta := Metadata{SchemaVersion: d.SchemaVersion, Environment: b.Environment, InstanceID: b.InstanceID, SourceVersion: "dynamic-state-v" + strconv.FormatInt(position, 10), ObservedAt: observed, SnapshotVersion: strings.Join(versions, ".")}
	return meta, raws, nil
}

// Status implements GET /dynamic/status.
func (q DynamicQuery) Status(ctx context.Context, p d.ReadPrincipal) (Record, error) {
	if err := q.ready(); err != nil {
		return Record{}, err
	}
	if !p.Valid() || p.Binding != q.Store.Binding() {
		return Record{}, d.Fail("INSTANCE_SCOPE_FORBIDDEN", 403)
	}
	meta, raws, err := q.metadata(ctx)
	if err != nil {
		return Record{}, err
	}
	if raws["POSITION"] == nil {
		return Record{}, d.Fail("DYNAMIC_STATE_NOT_RECORDED", 404)
	}
	var pos storedPosition
	if err = strictDecode(raws["POSITION"], &pos); err != nil {
		return Record{}, err
	}
	v := DynamicStatusView{Sources: []DynamicSourceView{}, Outcomes: []DynamicOutcomeView{}, EquityPeak: decimal(pos.EquityPeak)}
	if b := pos.Batch; b != nil {
		dir := float64(b.Direction)
		v.Batch = &DynamicBatchView{CycleID: b.CycleID, Direction: direction(b.Direction), Entry: decimal(b.Entry), OpenedAt: b.OpenedAt.UTC(), TierHours: b.Geometry.Tier, RequestedTierHours: b.Geometry.RequestedTier, Session: b.Geometry.Session,
			R: decimal(b.Geometry.R), Buffer: decimal(b.Geometry.Buffer), Trail: decimal(b.Geometry.Trail), Budget: decimal(b.Budget), BestClose: decimal(b.BestClose), LastBarAt: optionalTime(b.LastBarAt),
			CloseStop: decimal(b.CloseStop), Trailing: b.Trailing, Transferred: b.Transferred,
			// The same +1R condition as domain.Trailing (P3 design 28), shown only.
			TrailTrigger: decimal(b.Entry * (1 + dir*b.Geometry.R))}
	}
	if pl := pos.Initial; pl != nil {
		v.NextEntry = &DynamicEntryPlanView{Direction: direction(pl.Direction), Price: decimal(pl.Price), Distance: decimal(pl.Distance), CloseDistance: decimal(pl.CloseDist), RiskFraction: decimal(pl.Fraction), Equity: decimal(pl.Equity), TierHours: pl.Geometry.Tier, Session: pl.Geometry.Session, DirectiveID: pl.DirectiveID}
		if at, ok := directiveTime(pl.DirectiveID); ok {
			v.NextEntry.PlannedAt = &at
		}
	}
	bl := pos.Baseline
	phase := bl.Phase
	if phase == "" {
		phase = "NONE"
	}
	v.Baseline = DynamicBaselineView{Phase: phase, Direction: direction(bl.Direction), Value: decimal(bl.Value), StartedAt: optionalTime(bl.StartedAt), SuspendedAt: optionalTime(bl.SuspendedAt), SourceChain: optionalString(bl.SourceChain)}
	v.Noise = DynamicNoiseView{NoiseID: optionalString(pos.NoiseID), GeneratedAt: optionalTime(pos.NoiseAt)}
	for _, o := range pos.Outcomes {
		v.Outcomes = append(v.Outcomes, DynamicOutcomeView{R: decimal(o.R), ClosedAt: o.ClosedAt})
	}
	if raw := raws["INJECTION"]; raw != nil {
		var inj storedInjection
		if err = strictDecode(raw, &inj); err != nil {
			return Record{}, err
		}
		if a := inj.Active; a != nil {
			v.Chain = &DynamicChainView{ChainID: a.ChainID, Direction: a.Direction, Confidence: a.Confidence, Since: a.Since.UTC(), JudgmentCount: len(a.EventIDs)}
		}
		v.LastJudgmentID = optionalString(inj.LastJudgment)
	}
	if raw := raws["NEWS_SOURCES"]; raw != nil {
		sources := map[string]storedSource{}
		if err = strictDecode(raw, &sources); err != nil {
			return Record{}, err
		}
		for id, s := range sources {
			v.Sources = append(v.Sources, DynamicSourceView{SourceID: id, Failing: s.Failing, LastSuccess: optionalTime(s.LastSuccess)})
		}
		sort.Slice(v.Sources, func(i, j int) bool { return v.Sources[i].SourceID < v.Sources[j].SourceID })
	}
	return Record{meta, v}, nil
}

// directiveTime parses the UTC stamp of "initial-YYYYMMDDTHHMMSSZ".
func directiveTime(id string) (time.Time, bool) {
	_, stamp, ok := strings.Cut(id, "-")
	if !ok {
		return time.Time{}, false
	}
	t, err := time.Parse("20060102T150405Z", stamp)
	return t.UTC(), err == nil
}

type dynamicCursor struct {
	Binding  string             `json:"binding"`
	Resource string             `json:"resource"`
	From     *time.Time         `json:"from"`
	To       *time.Time         `json:"to"`
	Before   d.DynamicRecordKey `json:"before"`
	Issued   time.Time          `json:"issued"`
}

func (q DynamicQuery) sign(c dynamicCursor) string {
	raw, _ := json.Marshal(c)
	mac := hmac.New(sha256.New, q.Policy.CursorKey)
	mac.Write(raw)
	return base64.RawURLEncoding.EncodeToString(raw) + "." + base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
}
func (q DynamicQuery) parse(token, binding, resource string, f Filter) (*d.DynamicRecordKey, error) {
	bad := func() (*d.DynamicRecordKey, error) { return nil, d.Fail("QUERY_CURSOR_INVALID", 409) }
	parts := strings.Split(token, ".")
	if len(parts) != 2 || len(token) > 4096 {
		return bad()
	}
	body, e1 := base64.RawURLEncoding.DecodeString(parts[0])
	sig, e2 := base64.RawURLEncoding.DecodeString(parts[1])
	if e1 != nil || e2 != nil {
		return bad()
	}
	mac := hmac.New(sha256.New, q.Policy.CursorKey)
	mac.Write(body)
	if !hmac.Equal(mac.Sum(nil), sig) {
		return bad()
	}
	var c dynamicCursor
	dec := json.NewDecoder(bytes.NewReader(body))
	dec.DisallowUnknownFields()
	if dec.Decode(&c) != nil || dec.Decode(new(any)) != io.EOF {
		return bad()
	}
	sameTime := func(a, b *time.Time) bool { return a == nil && b == nil || a != nil && b != nil && a.Equal(*b) }
	now := q.Clock.Now()
	if c.Binding != binding || c.Resource != resource || !sameTime(c.From, f.From) || !sameTime(c.To, f.To) || c.Issued.After(now) || now.Sub(c.Issued) > q.Policy.CursorAge || c.Before.ID == "" {
		return bad()
	}
	return &c.Before, nil
}

// page reads one newest-first page of the given kinds.
func (q DynamicQuery) page(ctx context.Context, p d.ReadPrincipal, resource string, kinds []string, f Filter) (Metadata, []d.DynamicRecord, *string, error) {
	if err := q.ready(); err != nil {
		return Metadata{}, nil, nil, err
	}
	if !p.Valid() || p.Binding != q.Store.Binding() {
		return Metadata{}, nil, nil, d.Fail("INSTANCE_SCOPE_FORBIDDEN", 403)
	}
	if f.Limit == 0 {
		f.Limit = q.Policy.DefaultLimit
	}
	if f.Limit < 1 || f.Limit > q.Policy.MaxLimit {
		return Metadata{}, nil, nil, d.Fail("QUERY_LIMIT_INVALID", 422)
	}
	if f.QueueKind != "" || f.State != "" {
		return Metadata{}, nil, nil, d.Fail("QUERY_PARAMETER_INVALID", 422)
	}
	if f.From != nil && f.To != nil && f.From.After(*f.To) {
		return Metadata{}, nil, nil, d.Fail("QUERY_RANGE_INVALID", 422)
	}
	binding := p.Binding.Environment + "/" + p.Binding.InstanceID
	var before *d.DynamicRecordKey
	if f.Cursor != "" {
		var err error
		if before, err = q.parse(f.Cursor, binding, resource, f); err != nil {
			return Metadata{}, nil, nil, err
		}
	}
	since, until := time.Unix(0, 0).UTC(), q.Clock.Now().UTC().Add(time.Second)
	if f.From != nil {
		since = f.From.UTC()
	}
	if f.To != nil {
		// Stored timestamps are microseconds; +1s keeps the bound inclusive.
		until = f.To.UTC().Add(time.Second)
	}
	meta, _, err := q.metadata(ctx)
	if err != nil {
		return Metadata{}, nil, nil, err
	}
	rows, err := q.Store.RecordPage(ctx, kinds, since, until, before, true, f.Limit+1)
	if err != nil {
		return Metadata{}, nil, nil, err
	}
	var next *string
	if len(rows) > f.Limit {
		rows = rows[:f.Limit]
		token := q.sign(dynamicCursor{Binding: binding, Resource: resource, From: f.From, To: f.To, Before: rows[len(rows)-1].DynamicRecordKey, Issued: q.Clock.Now().UTC()})
		next = &token
	}
	return meta, rows, next, nil
}

// Judgments implements GET /dynamic/judgments.
func (q DynamicQuery) Judgments(ctx context.Context, p d.ReadPrincipal, f Filter) (Page, error) {
	meta, rows, next, err := q.page(ctx, p, "judgments", []string{"JUDGMENT"}, f)
	if err != nil {
		return Page{}, err
	}
	type stored struct {
		JudgmentID string            `json:"judgment_id"`
		At         time.Time         `json:"at"`
		OK         bool              `json:"ok"`
		Reason     string            `json:"reason"`
		Provider   string            `json:"provider"`
		Model      string            `json:"model"`
		InputIDs   []string          `json:"input_ids"`
		Output     *d.JudgmentOutput `json:"output"`
		Errors     []string          `json:"errors"`
	}
	decoded := make([]stored, len(rows))
	ids := []string{}
	for i, r := range rows {
		if err = strictDecode(r.Payload, &decoded[i]); err != nil {
			return Page{}, err
		}
		if o := decoded[i].Output; o != nil {
			for _, id := range append(append([]string{}, o.KeyItemIDs...), o.InvalidationItemIDs...) {
				if !d.Has(ids, id) && len(ids) < 64 {
					ids = append(ids, id)
				}
			}
		}
	}
	news := map[string]storedNews{}
	if len(ids) > 0 {
		raws, err := q.Store.RecordsByID(ctx, "NEWS", ids)
		if err != nil {
			return Page{}, err
		}
		for id, raw := range raws {
			var n storedNews
			if err = strictDecode(raw, &n); err != nil {
				return Page{}, err
			}
			news[id] = n
		}
	}
	live := p.Binding.Environment == "LIVE"
	refs := func(list []string) []DynamicNewsRefView {
		out := []DynamicNewsRefView{}
		for _, id := range list {
			ref := DynamicNewsRefView{ItemID: id}
			if n, ok := news[id]; ok {
				ref.Source, ref.PublishedAt, ref.LicenseScope = optionalString(n.Source), optionalTime(n.PublishedAt), optionalString(n.LicenseScope)
				// Chinese live news is licensed for SIM/shadow only (S3-023).
				if !(live && n.LicenseScope == "SIM_ONLY") {
					ref.Title = optionalString(n.Title)
				}
			}
			out = append(out, ref)
		}
		return out
	}
	items := []any{}
	for _, s := range decoded {
		v := DynamicJudgmentView{JudgmentID: s.JudgmentID, At: s.At.UTC(), OK: s.OK, Reason: optionalString(s.Reason), Provider: optionalString(s.Provider), Model: optionalString(s.Model), InputCount: len(s.InputIDs), KeyItems: []DynamicNewsRefView{}, InvalidationItems: []DynamicNewsRefView{}, ErrorCodes: []string{}}
		for _, e := range s.Errors {
			// Stored as "<model name>: <CODE>"; only that stable pair is public.
			name, code, ok := strings.Cut(e, ": ")
			if ok && d.ValidID(name) && stableCode.MatchString(code) {
				v.ErrorCodes = append(v.ErrorCodes, name+": "+code)
			}
		}
		if o := s.Output; o != nil {
			dir, conf, hz, prev := o.Direction, o.Confidence, o.HorizonHours, o.PreviousInvalidated
			v.Direction, v.Confidence, v.HorizonHours, v.PreviousInvalidated = &dir, &conf, &hz, &prev
			v.Thesis, v.CounterPoints, v.Invalidation = optionalString(o.Thesis), optionalString(o.CounterPoints), optionalString(o.Invalidation)
			v.KeyItems, v.InvalidationItems = refs(o.KeyItemIDs), refs(o.InvalidationItemIDs)
		}
		items = append(items, v)
	}
	return Page{meta, items, next}, nil
}

// Actions implements GET /dynamic/actions.
func (q DynamicQuery) Actions(ctx context.Context, p d.ReadPrincipal, f Filter) (Page, error) {
	meta, rows, next, err := q.page(ctx, p, "actions", []string{"ACTION", "STOP_EVENT"}, f)
	if err != nil {
		return Page{}, err
	}
	type stored struct {
		Kind         string       `json:"kind"`
		Reason       string       `json:"reason"`
		Direction    any          `json:"direction"`
		Confidence   *int         `json:"confidence"`
		HorizonHours *int         `json:"horizon_hours"`
		ChainID      string       `json:"chain_id"`
		EventID      string       `json:"event_id"`
		Targets      []string     `json:"targets"`
		Close        *float64     `json:"close"`
		CloseStop    *float64     `json:"close_stop"`
		Best         *float64     `json:"best"`
		Value        *float64     `json:"value"`
		Stopped      *bool        `json:"stopped"`
		Reconfirm    *bool        `json:"reconfirm"`
		Batch        *storedBatch `json:"batch"`
	}
	num := func(v *float64) *string {
		if v == nil {
			return nil
		}
		s := decimal(*v)
		return &s
	}
	items := []any{}
	for _, r := range rows {
		var s stored
		if err = strictDecode(r.Payload, &s); err != nil {
			return Page{}, err
		}
		v := DynamicActionView{ActionID: r.ID, At: r.At, Source: "POSITION", Kind: s.Kind, Reason: optionalString(s.Reason), Confidence: s.Confidence, HorizonHours: s.HorizonHours, ChainID: optionalString(s.ChainID), EventID: optionalString(s.EventID), Targets: []string{}, Close: num(s.Close), CloseStop: num(s.CloseStop), Best: num(s.Best), Value: num(s.Value), Stopped: s.Stopped, Reconfirm: s.Reconfirm}
		if r.Kind == "ACTION" {
			v.Source = "INJECTION"
		}
		if s.Targets != nil {
			v.Targets = s.Targets
		}
		switch x := s.Direction.(type) {
		case string:
			v.Direction = optionalString(x)
		case float64:
			dir := direction(int(x))
			v.Direction = &dir
		}
		if b := s.Batch; b != nil {
			dir := direction(b.Direction)
			v.Direction, v.CycleID = &dir, optionalString(b.CycleID)
			entry, stop := decimal(b.Entry), decimal(b.CloseStop)
			v.Entry = &entry
			if v.CloseStop == nil {
				v.CloseStop = &stop
			}
		}
		if v.Kind == "" {
			return Page{}, d.Fail("DYNAMIC_STATE_INVALID", 503)
		}
		items = append(items, v)
	}
	return Page{meta, items, next}, nil
}
