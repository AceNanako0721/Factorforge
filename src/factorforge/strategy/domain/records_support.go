package domain

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"github.com/AceNanako0721/Factorforge/src/factorforge/trading/api/dto"
	"io"
	"math"
	"reflect"
	"sort"
	"strconv"
	"strings"
	"time"
)

func Parse(s string) (Decimal, error)        { return dto.ParseDecimal(s) }
func Has(values []string, value string) bool { return has(values, value) }
func Ptr[T any](value T) *T                  { return &value }
func Math() *dto.Math                        { return dto.NewMath(28) }
func Zero() Decimal                          { return Decimal{} }
func One() Decimal                           { return constant("1") }
func Int(n int) Decimal                      { return constant(strconv.Itoa(n)) }
func Min(v ...Decimal) Decimal               { return dto.Min(v...) }
func Max(v ...Decimal) Decimal               { return dto.Max(v...) }
func Unique(values []string) []string {
	r := []string{}
	for _, v := range values {
		if !Has(r, v) {
			r = append(r, v)
		}
	}
	sort.Strings(r)
	return r
}
func Subset(a, b []string) bool {
	for _, v := range a {
		if !Has(b, v) {
			return false
		}
	}
	return true
}
func Text(value any) string {
	if value == nil {
		return ""
	}
	if v, ok := value.(string); ok {
		return v
	}
	return fmt.Sprint(value)
}
func Flag(v any) bool { b, ok := v.(bool); return ok && b }
func Object(v any) map[string]any {
	if m, ok := v.(map[string]any); ok {
		return m
	}
	return map[string]any{}
}
func Rows(v any) []map[string]any {
	r := []map[string]any{}
	switch xs := v.(type) {
	case []map[string]any:
		return xs
	case []any:
		for _, x := range xs {
			r = append(r, Object(x))
		}
	}
	return r
}
func Strings(v any) []string {
	r := []string{}
	switch xs := v.(type) {
	case []string:
		return xs
	case []any:
		for _, x := range xs {
			r = append(r, Text(x))
		}
	}
	return r
}
func Number(v any) int { n, _ := strconv.Atoi(Text(v)); return n }

// Operator research manifests remain open records. Invalid numeric/time fields
// abort their enclosing use case; callers recover only these business errors.
func Amount(v any) Decimal {
	if d, ok := v.(Decimal); ok {
		return d
	}
	d, err := Parse(Text(v))
	if err != nil {
		panic(&Error{"MANIFEST_DECIMAL_INVALID", 422})
	}
	return d
}
func At(v any) time.Time {
	if t, ok := v.(time.Time); ok {
		return t
	}
	t, err := time.Parse(time.RFC3339Nano, Text(v))
	if err != nil {
		panic(&Error{"MANIFEST_TIME_INVALID", 422})
	}
	_, offset := t.Zone()
	if offset != 0 {
		panic(&Error{"MANIFEST_TIME_INVALID", 422})
	}
	return t.UTC()
}
func Guard(fn func() error) (err error) {
	defer func() {
		if p := recover(); p != nil {
			if e, ok := p.(*Error); ok {
				err = e
			} else {
				panic(p)
			}
		}
	}()
	return fn()
}
func Seconds(d time.Duration) Decimal { return constant(strconv.FormatFloat(d.Seconds(), 'f', -1, 64)) }
func ISO(t time.Time) string {
	t = t.UTC()
	base := t.Format("2006-01-02T15:04:05")
	if t.Nanosecond() != 0 {
		base += fmt.Sprintf(".%06d", t.Nanosecond()/1000)
	}
	return base + "+00:00"
}

// JSONValue produces the legacy decimal-string and UTC representation, including
// empty factory collections. It is also the canonical input to frozen hashes.
func JSONValue(value any) any {
	return jsonValue(reflect.ValueOf(value))
}
func jsonValue(v reflect.Value) any {
	if !v.IsValid() {
		return nil
	}
	if v.Kind() == reflect.Interface || v.Kind() == reflect.Pointer {
		if v.IsNil() {
			return nil
		}
		return jsonValue(v.Elem())
	}
	if t, ok := v.Interface().(time.Time); ok {
		return strings.TrimSuffix(ISO(t), "+00:00") + "Z"
	}
	if d, ok := v.Interface().(Decimal); ok {
		return d.String()
	}
	if e, ok := v.Interface().(ExposureRisk); ok {
		return []any{e.Value.String(), e.Stress.String(), e.Group}
	}
	if o, ok := v.Interface().(interface{ comparisonEntries() map[string]any }); ok {
		return JSONValue(o.comparisonEntries())
	}
	switch v.Kind() {
	case reflect.Struct:
		r := map[string]any{}
		t := v.Type()
		for i := 0; i < v.NumField(); i++ {
			f := t.Field(i)
			if !f.IsExported() {
				continue
			}
			if strings.Contains(f.Tag.Get("json"), ",omitempty") && v.Field(i).IsZero() {
				continue
			}
			key := strings.Split(f.Tag.Get("json"), ",")[0]
			if key == "-" {
				continue
			}
			if f.Anonymous {
				for k, x := range Object(jsonValue(v.Field(i))) {
					r[k] = x
				}
				continue
			}
			if key == "" {
				key = f.Name
			}
			value := jsonValue(v.Field(i))
			if Has([]string{"scopes", "capabilities", "object_ids", "fill_ids", "income_ids", "consumed_evidence", "source_decisions", "calibrations", "rubrics", "verification_manifests"}, key) {
				if values, ok := value.([]any); ok {
					text := []string{}
					for _, item := range values {
						text = append(text, Text(item))
					}
					value = JSONValue(Unique(text))
				}
			}
			r[key] = value
		}
		return r
	case reflect.Slice, reflect.Array:
		r := []any{}
		for i := 0; i < v.Len(); i++ {
			r = append(r, jsonValue(v.Index(i)))
		}
		return r
	case reflect.Map:
		r := map[string]any{}
		it := v.MapRange()
		for it.Next() {
			r[Text(it.Key().Interface())] = jsonValue(it.Value())
		}
		return r
	default:
		return v.Interface()
	}
}
func Marshal(value any) ([]byte, error)        { return json.Marshal(JSONValue(value)) }
func DecodeJSON(data []byte, target any) error { return Decode(bytes.NewReader(data), target) }
func Clone[T any](v T) T {
	b, e := json.Marshal(v)
	if e != nil {
		panic(e)
	}
	var r T
	dec := json.NewDecoder(bytes.NewReader(b))
	dec.UseNumber()
	if e = dec.Decode(&r); e != nil {
		panic(e)
	}
	return r
}
func Map(value any) map[string]any { return Object(JSONValue(value)) }
func Digest(value any) string {
	var out bytes.Buffer
	canonical(&out, JSONValue(value))
	h := sha256.Sum256(out.Bytes())
	return hex.EncodeToString(h[:])
}
func canonical(out *bytes.Buffer, value any) {
	switch v := value.(type) {
	case map[string]any:
		keys := []string{}
		for k := range v {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		out.WriteByte('{')
		for i, k := range keys {
			if i > 0 {
				out.WriteByte(',')
			}
			canonical(out, k)
			out.WriteByte(':')
			canonical(out, v[k])
		}
		out.WriteByte('}')
	case []any:
		out.WriteByte('[')
		for i, x := range v {
			if i > 0 {
				out.WriteByte(',')
			}
			canonical(out, x)
		}
		out.WriteByte(']')
	case string:
		var b bytes.Buffer
		e := json.NewEncoder(&b)
		e.SetEscapeHTML(false)
		_ = e.Encode(v)
		s := strings.TrimSuffix(b.String(), "\n")
		for _, r := range s {
			if r < 128 {
				out.WriteRune(r)
			} else if r <= 0xffff {
				fmt.Fprintf(out, "\\u%04x", r)
			} else {
				r -= 0x10000
				fmt.Fprintf(out, "\\u%04x\\u%04x", 0xd800+(r>>10), 0xdc00+(r&0x3ff))
			}
		}
	default:
		b, e := json.Marshal(v)
		if e != nil {
			panic(e)
		}
		out.Write(b)
	}
}
func NewState(id, environment string) *StrategyState {
	s := &StrategyState{}
	data, _ := json.Marshal(map[string]any{"instance_id": id, "environment": environment})
	if err := DecodeJSON(data, s); err != nil {
		panic(err)
	}
	return s
}

type ExposureRisk struct {
	Value, Stress Decimal
	Group         string
}

func (e ExposureRisk) MarshalJSON() ([]byte, error) {
	return json.Marshal([]any{e.Value, e.Stress, e.Group})
}
func (e *ExposureRisk) UnmarshalJSON(b []byte) error {
	var a []json.RawMessage
	if json.Unmarshal(b, &a) != nil || len(a) != 3 {
		return invalid()
	}
	if json.Unmarshal(a[0], &e.Value) != nil || json.Unmarshal(a[1], &e.Stress) != nil || json.Unmarshal(a[2], &e.Group) != nil {
		return invalid()
	}
	return nil
}

func (p Policy) Numerical() NumericalPolicy {
	return NumericalPolicy{Levels: p.Levels, CleanupThreshold: p.CleanupThreshold, NumericalTolerance: p.NumericalTolerance, Epsilon: p.Epsilon, SigmaRef: p.SigmaRef, LiquidityBudget: p.LiquidityBudget, ObjectLossBudget: p.ObjectLossBudget, PortfolioGrossLimit: p.PortfolioGrossLimit, PortfolioNetLimit: p.PortfolioNetLimit, PortfolioStressLimit: p.PortfolioStressLimit, GroupLimits: p.GroupLimits, MicroDistance: p.MicroDistance, MaxStopFraction: p.MaxStopFraction, FeeRate: p.FeeRate, SlippageFraction: p.SlippageFraction, GapFraction: p.GapFraction, NoiseQuantile: p.NoiseQuantile, MaxNoiseFraction: p.MaxNoiseFraction, Beta: p.Beta, NoiseWindow: p.NoiseWindow, MaxBarGapSeconds: p.MaxBarGapSeconds, EventTTLSeconds: p.EventTTLSeconds, AbsolutePriceProxyValidated: p.AbsolutePriceProxyValidated}
}
func (s *StrategyState) Sentiment() SentimentState {
	v := SentimentState{Contributions: []Contribution{}, Ledger: s.Ledger}
	for _, c := range s.Contributions.Values() {
		v.Contributions = append(v.Contributions, *c)
	}
	return v
}
func (s *StrategyState) Advance(obj *ObservedObject, at time.Time, sample *MarketSample, p *Policy, params *ParameterSnapshot) error {
	v := s.Sentiment()
	if err := AdvanceContributions(&v, obj.ObjectID, at, sample, p.Numerical(), params.Values["kappa"]); err != nil {
		return err
	}
	for _, c := range v.Contributions {
		copy := c
		s.Contributions.Set(c.ContributionID, &copy)
	}
	s.Ledger = v.Ledger
	return nil
}
func (s *StrategyState) Append(c *Contribution, row LedgerEntry) error {
	v := SentimentState{Ledger: s.Ledger}
	if _, err := AppendEntry(&v, c, row); err != nil {
		return err
	}
	s.Ledger = v.Ledger
	return nil
}
func (s *StrategyState) Pool(id string) (PoolView, error) { return Pool(s.Sentiment(), id) }

// Cross-field invariants complement presence, enum, bounds and UTC checks in
// the captured schemas. They apply recursively to records loaded from storage.
func Validate(value any) error {
	return walkValidate(reflect.ValueOf(value))
}
func walkValidate(v reflect.Value) error {
	if !v.IsValid() {
		return nil
	}
	if v.Kind() == reflect.Pointer || v.Kind() == reflect.Interface {
		if v.IsNil() {
			return nil
		}
		return walkValidate(v.Elem())
	}
	if _, ok := v.Interface().(Decimal); ok {
		return nil
	}
	if _, ok := v.Interface().(time.Time); ok {
		return nil
	}
	if o, ok := v.Interface().(interface{ validationChildren() []any }); ok {
		for _, x := range o.validationChildren() {
			if e := Validate(x); e != nil {
				return e
			}
		}
		return nil
	}
	switch x := v.Interface().(type) {
	case TimeWindowPlan:
		if !x.Valid() {
			return invalid()
		}
	case StrategyState:
		for id, plan := range x.TimeWindows {
			if id != plan.PlanID || !plan.Valid() || x.Objects.Value(plan.ObjectID) == nil || x.Policies.Value(plan.PolicyVersion) == nil {
				return invalid()
			}
		}
	case ParameterSnapshot:
		for key, b := range x.Bounds {
			if b[0].Cmp(b[1]) > 0 {
				return invalid()
			}
			if val, ok := x.Values[key]; ok && (val.Cmp(b[0]) < 0 || val.Cmp(b[1]) > 0) {
				return invalid()
			}
		}
		for _, s := range x.Steps {
			if s.Sign() <= 0 {
				return invalid()
			}
		}
	case Level:
		if x.Exit.Cmp(x.Enter) >= 0 {
			return invalid()
		}
	case Policy:
		if x.RegimeExit.Cmp(x.RegimeEnter) >= 0 || x.ObservationMinSeconds > x.ObservationMaxSeconds || x.HalfLifeBounds[0].Sign() <= 0 || x.HalfLifeBounds[0].Cmp(x.HalfLifeBounds[1]) > 0 {
			return invalid()
		}
		for i, l := range x.Levels {
			if i > 0 && (l.Enter.Cmp(x.Levels[i-1].Enter) <= 0 || l.Exit.Cmp(x.Levels[i-1].Exit) <= 0 || l.Exposure.Cmp(x.Levels[i-1].Exposure) <= 0) {
				return invalid()
			}
		}
	case EvidenceRef:
		if x.AvailableAt.Before(x.FirstPublicAt) || x.AvailableAt.Before(x.ReceivedAt) {
			return invalid()
		}
	}
	switch v.Kind() {
	case reflect.Struct:
		t := v.Type()
		for i := 0; i < v.NumField(); i++ {
			if t.Field(i).IsExported() {
				if e := walkValidate(v.Field(i)); e != nil {
					return e
				}
			}
		}
	case reflect.Slice, reflect.Array:
		for i := 0; i < v.Len(); i++ {
			if e := walkValidate(v.Index(i)); e != nil {
				return e
			}
		}
	case reflect.Map:
		it := v.MapRange()
		for it.Next() {
			if e := walkValidate(it.Value()); e != nil {
				return e
			}
		}
	}
	return nil
}
func WindowID(id string, at time.Time, p *Policy) string {
	return fmt.Sprintf("%s:%s:%d", id, p.Version, int64(math.Floor(at.Sub(p.WindowAnchor).Seconds()/float64(p.WindowSeconds))))
}
func Reserve(s *StrategyState, o *ObservedObject, at time.Time, p *Policy, id string) (*Reservation, error) {
	if r, ok := s.Reservations.Get(id); ok {
		return r, nil
	}
	window, enforce, err := RiskWindow(s, o, at, p)
	if err != nil {
		return nil, err
	}
	used := 0
	for _, r := range s.Reservations.Values() {
		if r.WindowID == window && r.State != "RELEASED" {
			used++
		}
	}
	if enforce && used >= p.MaxNewRisk {
		return nil, &Error{"NEW_RISK_WINDOW_LIMIT", 423}
	}
	if enforce && len(s.LossCases.Value(window)) >= p.MaxLossCases {
		return nil, &Error{"LOSS_CASE_WINDOW_LIMIT", 423}
	}
	r := &Reservation{ReservationID: id, ObjectID: o.ObjectID, WindowID: window, State: "RESERVED"}
	s.Reservations.Set(id, r)
	return r, nil
}
func RawDecode(reader io.Reader, target any) error {
	dec := json.NewDecoder(reader)
	dec.UseNumber()
	dec.DisallowUnknownFields()
	return dec.Decode(target)
}

// Truthy preserves open research-manifest presence semantics (for example an independent group list).
func Truthy(v any) bool {
	if v == nil {
		return false
	}
	if b, ok := v.(bool); ok {
		return b
	}
	r := reflect.ValueOf(v)
	switch r.Kind() {
	case reflect.Slice, reflect.Map, reflect.Array, reflect.String:
		return r.Len() > 0
	default:
		return Text(v) != "0" && Text(v) != ""
	}
}
