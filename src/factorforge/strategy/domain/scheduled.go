package domain

import "time"

type ScheduledEvent struct {
	ScheduleID   string                      `json:"schedule_id"`
	ObjectID     string                      `json:"object_id"`
	EventType    string                      `json:"event_type"`
	ExpectedAt   time.Time                   `json:"expected_at"`
	Certainty    string                      `json:"certainty"`
	SourceRef    string                      `json:"source_ref"`
	Revision     int                         `json:"revision"`
	Status       string                      `json:"status"`
	WindowsState map[string]string           `json:"windows_state"`
	Windows      map[string]*ScheduledWindow `json:"windows,omitempty"`
	// WindowTimes are application-computed spans (v2.6.1), e.g. trading-day
	// windows that fixed template offsets cannot express.
	WindowTimes map[string]ScheduledWindowTimes `json:"window_times,omitempty"`
}
type ScheduledWindowTimes struct {
	StartAt time.Time `json:"start_at"`
	EndAt   time.Time `json:"end_at"`
}

// Span prefers the submitted window times over the template offsets.
func (e *ScheduledEvent) Span(id string, t *ScheduledTemplate) (time.Time, time.Time) {
	if w, ok := e.WindowTimes[id]; ok {
		return w.StartAt, w.EndAt
	}
	return e.ExpectedAt.Add(time.Duration(t.StartOffsetSeconds) * time.Second), e.ExpectedAt.Add(time.Duration(t.EndOffsetSeconds) * time.Second)
}

type ScheduledWindow struct {
	Start      time.Time          `json:"start"`
	End        time.Time          `json:"end"`
	Certainty  string             `json:"certainty"`
	StartPrice *Decimal           `json:"start_price"`
	Template   *ScheduledTemplate `json:"template"`
}
type ScheduledEventCommand struct {
	Command
	ScheduleID string                          `json:"schedule_id"`
	ObjectID   string                          `json:"object_id"`
	EventType  string                          `json:"event_type"`
	ExpectedAt time.Time                       `json:"expected_at"`
	Certainty  string                          `json:"certainty"`
	SourceRef  string                          `json:"source_ref"`
	Revision   int                             `json:"revision"`
	Status     string                          `json:"status"`
	Windows    map[string]ScheduledWindowTimes `json:"windows,omitempty"`
}
type TemplatePrior struct {
	Mean        Decimal `json:"mean"`
	N0          Decimal `json:"n0"`
	EvidenceRef string  `json:"evidence_ref"`
}
type TemplateStats struct {
	N    int     `json:"n"`
	Mean Decimal `json:"mean"`
	M2   Decimal `json:"m2"`
	NEff Decimal `json:"n_eff"`
}
type ScheduledTemplate struct {
	StartOffsetSeconds int           `json:"start_offset_seconds"`
	EndOffsetSeconds   int           `json:"end_offset_seconds"`
	CertaintyRequired  string        `json:"certainty_required"`
	Prior              TemplatePrior `json:"prior"`
	Scale              Decimal       `json:"scale"`
	Cap                Decimal       `json:"cap"`
	MinN               Decimal       `json:"min_n"`
	GateT              Decimal       `json:"gate_t"`
	ForgetCases        Decimal       `json:"forget_cases"`
	Stats              TemplateStats `json:"stats"`
	State              string        `json:"state"`
}

func (t ScheduledTemplate) Validate() error {
	if t.StartOffsetSeconds >= t.EndOffsetSeconds || !Has([]string{"CONFIRMED", "ESTIMATED"}, t.CertaintyRequired) || t.Prior.N0.Sign() < 0 || t.Prior.N0.Sign() > 0 && t.Prior.EvidenceRef == "" || t.Scale.Sign() <= 0 || !unit(t.Cap) || t.MinN.Sign() <= 0 || t.GateT.Sign() <= 0 || t.ForgetCases.Sign() < 0 || !validLayerState(t.State) || t.Stats.N < 0 || t.Stats.NEff.Sign() < 0 || t.Stats.M2.Sign() < 0 {
		return invalid()
	}
	return nil
}
func (t *ScheduledTemplate) Estimate() (Decimal, bool, error) {
	m := Math()
	n := m.Add(t.Prior.N0, t.Stats.NEff)
	if n.Sign() == 0 {
		return Zero(), false, nil
	}
	mean := m.Div(m.Add(m.Mul(t.Prior.N0, t.Prior.Mean), m.Mul(t.Stats.NEff, t.Stats.Mean)), n)
	if t.Prior.N0.Sign() > 0 {
		return mean, true, m.Err()
	}
	if t.Stats.NEff.Cmp(t.MinN) < 0 || t.Stats.NEff.Cmp(One()) <= 0 {
		return mean, false, m.Err()
	}
	variance := m.Div(t.Stats.M2, m.Sub(t.Stats.NEff, One()))
	se := m.Sqrt(m.Div(variance, t.Stats.NEff))
	passed := t.Stats.Mean.Sign() != 0
	if se.Sign() > 0 {
		passed = m.Div(t.Stats.Mean.Abs(), se).Cmp(t.GateT) >= 0
	}
	return mean, passed, m.Err()
}
func (t *ScheduledTemplate) Observe(r Decimal) error {
	m := Math()
	lambda := One()
	if t.ForgetCases.Sign() > 0 {
		v, err := Decay(One(), One(), t.ForgetCases)
		if err != nil {
			return err
		}
		lambda = v
	}
	weight := m.Mul(lambda, t.Stats.NEff)
	n := m.Add(weight, One())
	delta := m.Sub(r, t.Stats.Mean)
	mean := m.Add(t.Stats.Mean, m.Div(delta, n))
	t.Stats.M2 = m.Add(m.Mul(lambda, t.Stats.M2), m.Mul(delta, m.Sub(r, mean)))
	t.Stats.Mean = mean
	t.Stats.NEff = n
	t.Stats.N++
	return m.Err()
}
func templateWindows(s *StrategyState, eventType string) []string {
	ids := []string{}
	for _, key := range s.Templates.Keys() {
		if len(key) > len(eventType)+1 && key[:len(eventType)+1] == eventType+"/" {
			ids = append(ids, key[len(eventType)+1:])
		}
	}
	return ids
}
func UpsertSchedule(s *StrategyState, request ScheduledEventCommand) (*ScheduledEvent, error) {
	if s.Objects.Value(request.ObjectID) == nil {
		return nil, &Error{"OBJECT_NOT_FOUND", 404}
	}
	ids := templateWindows(s, request.EventType)
	if len(ids) == 0 {
		return nil, &Error{"SCHEDULED_TEMPLATE_UNKNOWN", 422}
	}
	if request.Revision <= 0 || !Has([]string{"CONFIRMED", "ESTIMATED"}, request.Certainty) || !Has([]string{"SCHEDULED", "CANCELLED"}, request.Status) || request.SourceRef == "" {
		return nil, invalid()
	}
	old := s.ScheduledEvents.Value(request.ScheduleID)
	if old != nil && (request.Revision <= old.Revision || request.ObjectID != old.ObjectID || request.EventType != old.EventType || old.Status == "CANCELLED" && request.Status != "CANCELLED") {
		return nil, &Error{"SCHEDULED_REVISION_CONFLICT", 409}
	}
	for id, w := range request.Windows {
		if !Has(ids, id) {
			return nil, &Error{"SCHEDULED_WINDOW_UNKNOWN", 422}
		}
		if !w.StartAt.Before(w.EndAt) {
			return nil, invalid()
		}
	}
	next := &ScheduledEvent{ScheduleID: request.ScheduleID, ObjectID: request.ObjectID, EventType: request.EventType, ExpectedAt: request.ExpectedAt, Certainty: request.Certainty, SourceRef: request.SourceRef, Revision: request.Revision, Status: request.Status, WindowsState: map[string]string{}, Windows: map[string]*ScheduledWindow{}}
	if old != nil {
		next.WindowsState = Clone(old.WindowsState)
		next.Windows = Clone(old.Windows)
		if next.Windows == nil {
			next.Windows = map[string]*ScheduledWindow{}
		}
	}
	for _, id := range ids {
		if next.WindowsState[id] == "" {
			next.WindowsState[id] = "PENDING"
		}
		if request.Status == "CANCELLED" && Has([]string{"OPEN", "PENDING"}, next.WindowsState[id]) {
			next.WindowsState[id] = "VOID"
		}
	}
	if len(request.Windows) > 0 {
		next.WindowTimes = map[string]ScheduledWindowTimes{}
		for id, w := range request.Windows {
			next.WindowTimes[id] = ScheduledWindowTimes{w.StartAt.UTC(), w.EndAt.UTC()}
		}
	}
	s.ScheduledEvents.Set(next.ScheduleID, next)
	return next, nil
}

// AdvanceScheduled pins window times, certainty and start price at opening.
// Later schedule revisions only change windows which are still PENDING.
func AdvanceScheduled(s *StrategyState, obj *ObservedObject, at time.Time, p *Policy) error {
	m := Math()
	for _, event := range s.ScheduledEvents.Values() {
		if event.ObjectID != obj.ObjectID || event.Status != "SCHEDULED" {
			continue
		}
		if event.Windows == nil {
			event.Windows = map[string]*ScheduledWindow{}
		}
		if event.WindowsState == nil {
			event.WindowsState = map[string]string{}
		}
		for _, id := range templateWindows(s, event.EventType) {
			key := event.EventType + "/" + id
			t := s.Templates.Value(key)
			status := event.WindowsState[id]
			if status == "" {
				status = "PENDING"
				event.WindowsState[id] = status
			}
			if Has([]string{"CLOSED", "VOID"}, status) {
				continue
			}
			w := event.Windows[id]
			if status == "PENDING" {
				start, end := event.Span(id, t)
				if at.Before(start) {
					continue
				}
				w = &ScheduledWindow{Start: start, End: end, Certainty: event.Certainty, Template: Clone(t)}
				if price := PriceAt(s.Samples.Value(obj.ObjectID), start); price != nil {
					w.StartPrice = Ptr(price.Price)
				}
				event.Windows[id] = w
				event.WindowsState[id] = "OPEN"
			}
			if w == nil {
				return &Error{"SCHEDULED_WINDOW_STATE_INVALID", 423}
			}
			if at.Before(w.End) {
				continue
			}
			event.WindowsState[id] = "CLOSED"
			caseID := "scheduled-" + Digest([]any{event.ScheduleID, id, ISO(w.Start), ISO(w.End)})[:24]
			if s.Cases.Value(caseID) != nil {
				continue
			}
			record := &CaseRecord{Kind: "SCHEDULED_WINDOW", CaseID: caseID, ObjectID: obj.ObjectID, EntryAt: w.Start, ExitAt: Ptr(w.End), ObservationEnd: Ptr(w.End), EntryWindow: id, ObservationSeconds: int(w.End.Sub(w.Start).Seconds()), EntrySnapshot: map[string]any{"schedule_id": event.ScheduleID, "template_key": key, "policy_version": p.Version, "parameter_version": obj.ParameterVersion, "certainty": w.Certainty, "prior": Map(w.Template.Prior), "cost_known": true}, EventGroups: []string{event.ScheduleID}, PNLComponents: map[string]Decimal{}, Labels: map[string]*Decimal{}, Status: "CENSORED", LabelStatus: "UNKNOWN"}
			end := PriceAt(s.Samples.Value(obj.ObjectID), w.End)
			if w.StartPrice != nil && w.StartPrice.Sign() > 0 && end != nil && end.Price.Sign() > 0 {
				r := m.Ln(m.Div(end.Price, *w.StartPrice))
				// v2.9.0 S2-042: window returns are recorded for audit but no
				// longer update template direction or size (no historical-price
				// inference). Estimate keeps using the registered prior only.
				record.Status = "MATURE"
				record.Direction = r.Sign()
				record.LabelStatus = "NEUTRAL"
				if r.Abs().Cmp(m.Add(p.LabelCostBand, p.LabelNoiseBand)) > 0 {
					record.LabelStatus = "CORRECT"
				}
				record.Labels["direction_y"] = Ptr(r)
				record.PNLComponents["window_return"] = r
				record.EntrySnapshot["start_price"] = w.StartPrice.String()
				record.EntrySnapshot["end_price"] = end.Price.String()
			}
			s.Cases.Set(caseID, record)
			s.AppendAudit(map[string]any{"action": "SCHEDULED_WINDOW_CLOSED", "object_id": obj.ObjectID, "case_id": caseID, "at": ISO(at), "state": record.Status})
		}
	}
	return m.Err()
}
func ScheduledContributions(s *StrategyState, obj *ObservedObject, at time.Time, c *Composition) (Decimal, map[string]Decimal, error) {
	m := Math()
	total := Zero()
	shadow := map[string]Decimal{}
	if c.Scheduled.State == "DISABLED" {
		return total, shadow, nil
	}
	for _, e := range s.ScheduledEvents.Values() {
		if e.ObjectID != obj.ObjectID || e.Status != "SCHEDULED" {
			continue
		}
		for _, id := range templateWindows(s, e.EventType) {
			key := e.EventType + "/" + id
			t := s.Templates.Value(key)
			w := e.Windows[id]
			if e.WindowsState[id] != "OPEN" || w == nil || at.Before(w.Start) || !at.Before(w.End) || t.CertaintyRequired == "CONFIRMED" && w.Certainty != "CONFIRMED" || t.State == "DISABLED" {
				continue
			}
			mean, passed, err := t.Estimate()
			if err != nil {
				return total, shadow, err
			}
			v := m.Mul(clip(m.Div(mean, t.Scale), One()), t.Cap)
			if passed && t.State == "ENABLED" && c.Scheduled.State == "ENABLED" {
				total = m.Add(total, v)
			} else {
				shadow["template:"+e.ScheduleID+"/"+id] = v
			}
		}
	}
	return clip(total, c.Scheduled.Cap), shadow, m.Err()
}
