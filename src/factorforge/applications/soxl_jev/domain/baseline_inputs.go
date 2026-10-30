package domain

import (
	"math"
	"regexp"
	"strconv"
	"strings"
	"time"
)

type DailyBar struct {
	Date     string  `json:"date"`
	Open     float64 `json:"open"`
	High     float64 `json:"high"`
	Low      float64 `json:"low"`
	Close    float64 `json:"close"`
	AdjClose float64 `json:"adjclose"`
	Volume   int64   `json:"volume"`
	Final    bool    `json:"final"`
}

// These local wire values isolate the parallel P2 implementation. Decimal
// values remain strings at the public boundary.
type FactorObservation struct {
	FactorID      string    `json:"factor_id"`
	ObservationID string    `json:"observation_id"`
	AvailableAt   time.Time `json:"available_at"`
	Value         string    `json:"value"`
	ForceReduce   bool      `json:"force_reduce"`
	InputsHash    string    `json:"inputs_hash"`
	EvidenceRef   string    `json:"evidence_ref"`
	Supersedes    *string   `json:"supersedes,omitempty"`
	// S2-035/S2-038: re-confirmation marker and reason code.
	Reconfirm  bool   `json:"reconfirm,omitempty"`
	ReasonCode string `json:"reason_code,omitempty"`
}

func (v FactorObservation) Valid() bool {
	value, e := strconv.ParseFloat(v.Value, 64)
	return e == nil && Finite(value) && value >= -1 && value <= 1 && v.FactorID != "" && ValidID(v.ObservationID) && UTC(v.AvailableAt) && v.InputsHash != "" && v.EvidenceRef != "" && (v.Supersedes == nil || ValidID(*v.Supersedes))
}

type ScheduledEvent struct {
	ScheduleID string    `json:"schedule_id"`
	ObjectID   string    `json:"object_id"`
	EventType  string    `json:"event_type"`
	ExpectedAt time.Time `json:"expected_at"`
	Certainty  string    `json:"certainty"`
	SourceRef  string    `json:"source_ref"`
	Revision   int       `json:"revision"`
	Status     string    `json:"status"`
	// WindowTimes are trading-day spans computed here (framework);
	// the framework echoes them back as window_times.
	WindowTimes map[string]WindowTimes `json:"window_times,omitempty"`
}
type WindowTimes struct {
	StartAt time.Time `json:"start_at"`
	EndAt   time.Time `json:"end_at"`
}
type ScheduleCandidate struct {
	EventType  string    `json:"event_type"`
	Subject    string    `json:"subject"`
	Period     string    `json:"period"`
	ExpectedAt time.Time `json:"expected_at"`
	Certainty  string    `json:"certainty"`
	SourceRef  string    `json:"source_ref"`
	Status     string    `json:"status"`
	Actual     bool      `json:"actual"`
}
type WatchItem struct {
	Symbol   string   `json:"symbol"`
	Name     string   `json:"name"`
	Keywords []string `json:"keywords"`
}
type WatchArticle struct {
	URL         string     `json:"url"`
	Subject     string     `json:"subject"`
	Title       string     `json:"title"`
	Summary     string     `json:"summary"`
	SourceGrade string     `json:"source_grade"`
	Official    bool       `json:"official"`
	AvailableAt time.Time  `json:"available_at"`
	PublishedAt *time.Time `json:"published_at"`
}
type QuoteObservation struct {
	Price, PreviousClose float64
	At                   time.Time
}
type WatchEvent struct {
	Subject             string   `json:"subject"`
	Kind                string   `json:"kind"`
	ConfirmedByOfficial bool     `json:"confirmed_by_official"`
	Novelty             string   `json:"novelty"`
	Severity            string   `json:"severity"`
	DirectionHint       string   `json:"direction_hint"`
	NewInformation      string   `json:"new_information"`
	SourceURLs          []string `json:"source_urls"`
}
type SectorView struct {
	Direction   float64 `json:"direction"`
	ForceReduce bool    `json:"force_reduce"`
}
type AbnormalMove struct {
	Symbol         string  `json:"symbol"`
	IntradayReturn float64 `json:"intraday_return"`
	DailyVol20     float64 `json:"daily_vol20"`
}
type WatchAssessment struct {
	Events        []WatchEvent   `json:"events"`
	SectorView    SectorView     `json:"sector_view"`
	AbnormalMoves []AbnormalMove `json:"abnormal_moves"`
}
type WatchNotification struct {
	Events        []WatchEvent     `json:"events"`
	Sources       []WatchArticle   `json:"sources"`
	AbnormalMoves []AbnormalMove   `json:"abnormal_moves"`
	RiskRefs      []string         `json:"risk_refs"`
	Risks         []ScheduledEvent `json:"scheduled_risks"`
}

var watchURLPattern = regexp.MustCompile(`^https?://(?:[A-Za-z0-9.-]+|\[[0-9a-fA-F:]+\])(?::[0-9]+)?(?:/[^\s]*)?$`)

func EventFingerprint(e WatchEvent) string {
	return Digest([]any{strings.ToLower(strings.TrimSpace(e.Subject)), strings.ToLower(strings.TrimSpace(e.Kind)), strings.ToLower(strings.Join(strings.Fields(e.NewInformation), " ")), e.ConfirmedByOfficial})
}
func Finite(x float64) bool { return !math.IsNaN(x) && !math.IsInf(x, 0) }
func (v WatchNotification) Valid() bool {
	if len(v.Events) == 0 && len(v.AbnormalMoves) == 0 {
		return false
	}
	for _, s := range v.Sources {
		if !watchURLPattern.MatchString(s.URL) || !UTC(s.AvailableAt) || s.PublishedAt != nil && !UTC(*s.PublishedAt) || s.Official && s.SourceGrade != "P0" || !Has([]string{"P0", "P1", "P2", "P3"}, s.SourceGrade) {
			return false
		}
	}
	for _, e := range v.Events {
		if e.Subject == "" || e.Kind == "" || e.NewInformation == "" || e.Severity != "MAJOR" || !Has([]string{"NEW", "UPDATE"}, e.Novelty) || !Has([]string{"POSITIVE", "NEGATIVE", "NEUTRAL"}, e.DirectionHint) || len(e.SourceURLs) == 0 {
			return false
		}
		confirmed := false
		for _, u := range e.SourceURLs {
			found := false
			for _, s := range v.Sources {
				if u == s.URL {
					found = true
					confirmed = confirmed || s.Official
				}
			}
			if !found {
				return false
			}
		}
		if e.ConfirmedByOfficial && !confirmed {
			return false
		}
	}
	for _, m := range v.AbnormalMoves {
		if !Finite(m.IntradayReturn) || !Finite(m.DailyVol20) || m.DailyVol20 <= 0 || math.Abs(m.IntradayReturn) <= 2.5*m.DailyVol20 {
			return false
		}
	}
	for _, r := range v.Risks {
		if r.Status != "SCHEDULED" || !UTC(r.ExpectedAt) || !ValidID(r.ScheduleID) || r.EventType == "" || r.SourceRef == "" {
			return false
		}
	}
	return true
}
