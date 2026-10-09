package operations

import (
	"context"
	"encoding/json"
	"math"
	"time"
	"unicode/utf8"

	d "github.com/AceNanako0721/Factorforge/src/factorforge/applications/soxl_jev/domain"
)

type VenueSnapshot struct {
	URL         string    `json:"url"`
	Content     string    `json:"content"`
	ContentHash string    `json:"content_hash"`
	ReceivedAt  time.Time `json:"received_at"`
}
type VenueCalendarLimits struct {
	MaxInputBytes       int   `json:"max_input_bytes"`
	MaxSessions         int   `json:"max_sessions"`
	MaxUpdateAgeSeconds int64 `json:"max_update_age_seconds"`
}
type VenueCalendarRequest struct {
	SchemaVersion       int                 `json:"schema_version"`
	Binding             d.Binding           `json:"binding"`
	Version             string              `json:"version"`
	ProviderEnvironment string              `json:"provider_environment"`
	ProductSnapshot     VenueSnapshot       `json:"product_snapshot"`
	ScheduleSnapshot    VenueSnapshot       `json:"schedule_snapshot"`
	Limits              VenueCalendarLimits `json:"limits"`
}
type VenueCalendarArtifact struct {
	SchemaVersion int                  `json:"schema_version"`
	Method        string               `json:"method"`
	ArtifactID    string               `json:"artifact_id"`
	Request       VenueCalendarRequest `json:"request"`
	Calendar      Calendar             `json:"calendar"`
}

// CompileVenueCalendar translates a captured public response, without fetching
// data, installing windows, accepting venue agreements or inferring a multiplier.
func CompileVenueCalendar(r VenueCalendarRequest, now time.Time) (VenueCalendarArtifact, error) {
	empty := VenueCalendarArtifact{}
	fail := func() (VenueCalendarArtifact, error) { return empty, d.Fail("VENUE_CALENDAR_INVALID", 422) }
	if r.SchemaVersion != 1 || !r.Binding.Valid() || r.Binding.Environment != "SIM" || !d.ValidID(r.Version) || !d.UTC(now) || r.Limits.MaxInputBytes <= 0 || r.Limits.MaxSessions < 2 || r.Limits.MaxUpdateAgeSeconds <= 0 || r.Limits.MaxUpdateAgeSeconds > math.MaxInt64/int64(time.Second) {
		return fail()
	}
	host := map[string]string{"DEMO": "demo-fapi.binance.com", "PUBLIC_MAIN": "fapi.binance.com"}[r.ProviderEnvironment]
	if host == "" || len(r.ProductSnapshot.Content) > r.Limits.MaxInputBytes || len(r.ScheduleSnapshot.Content) > r.Limits.MaxInputBytes-len(r.ProductSnapshot.Content) {
		return fail()
	}
	age := time.Duration(r.Limits.MaxUpdateAgeSeconds) * time.Second
	for i, s := range []VenueSnapshot{r.ProductSnapshot, r.ScheduleSnapshot} {
		path := []string{"/fapi/v1/exchangeInfo", "/fapi/v1/tradingSchedule"}[i]
		if s.URL != "https://"+host+path || s.Content == "" || !utf8.ValidString(s.Content) || s.ContentHash != d.ContentDigest([]byte(s.Content)) || !d.UTC(s.ReceivedAt) || s.ReceivedAt.After(now) || now.Sub(s.ReceivedAt) > age {
			return fail()
		}
	}
	// Validate strings before marshaling: encoding/json replaces invalid UTF-8.
	frozen, err := json.Marshal(r)
	if err != nil || d.DecodePrivate(frozen, &r) != nil {
		return fail()
	}
	var product map[string]json.RawMessage
	var symbols []map[string]json.RawMessage
	if d.DecodePrivate([]byte(r.ProductSnapshot.Content), &product) != nil || d.DecodePrivate(product["symbols"], &symbols) != nil {
		return fail()
	}
	field := func(m map[string]json.RawMessage, key string) string {
		var value string
		_ = json.Unmarshal(m[key], &value)
		return value
	}
	found := 0
	for _, s := range symbols {
		if field(s, "symbol") != "SOXLUSDT" {
			continue
		}
		found++
		for key, value := range map[string]string{"contractType": "TRADIFI_PERPETUAL", "underlyingType": "EQUITY", "baseAsset": "SOXL", "quoteAsset": "USDT", "marginAsset": "USDT", "status": "TRADING"} {
			if field(s, key) != value {
				return fail()
			}
		}
	}
	if found != 1 {
		return fail()
	}
	var schedule, markets, equity map[string]json.RawMessage
	var updated int64
	var sessions []struct {
		Start int64  `json:"startTime"`
		End   int64  `json:"endTime"`
		Type  string `json:"type"`
	}
	// External records remain open to new fields; recursive duplicate checking
	// occurs on the complete snapshot before selecting the known market fields.
	if d.DecodePrivate([]byte(r.ScheduleSnapshot.Content), &schedule) != nil || json.Unmarshal(schedule["updateTime"], &updated) != nil || d.DecodePrivate(schedule["marketSchedules"], &markets) != nil || d.DecodePrivate(markets["EQUITY"], &equity) != nil {
		return fail()
	}
	var rows []map[string]json.RawMessage
	if d.DecodePrivate(equity["sessions"], &rows) != nil || len(rows) < 2 || len(rows) > r.Limits.MaxSessions {
		return fail()
	}
	for _, row := range rows {
		var s struct {
			Start int64
			End   int64
			Type  string
		}
		if json.Unmarshal(row["startTime"], &s.Start) != nil || json.Unmarshal(row["endTime"], &s.End) != nil || json.Unmarshal(row["type"], &s.Type) != nil {
			return fail()
		}
		sessions = append(sessions, struct {
			Start int64  `json:"startTime"`
			End   int64  `json:"endTime"`
			Type  string `json:"type"`
		}{s.Start, s.End, s.Type})
	}
	update := time.UnixMilli(updated).UTC()
	if updated <= 0 || update.After(r.ScheduleSnapshot.ReceivedAt) || now.Sub(update) > age {
		return fail()
	}
	zone, err := time.LoadLocation("America/New_York")
	if err != nil {
		return fail()
	}
	calendar := Calendar{Zone: "America/New_York", Sessions: []MarketSession{}}
	previous := int64(0)
	dates := map[string]bool{}
	for _, s := range sessions {
		if s.Start <= 0 || s.End <= s.Start || previous != 0 && s.Start != previous || !d.Has([]string{"REGULAR", "PRE_MARKET", "AFTER_MARKET", "OVERNIGHT", "NO_TRADING"}, s.Type) {
			return fail()
		}
		previous = s.End
		if s.Type != "REGULAR" {
			continue
		}
		open, close := time.UnixMilli(s.Start).UTC(), time.UnixMilli(s.End).UTC()
		localOpen, localClose := open.In(zone), close.In(zone)
		date := localOpen.Format("2006-01-02")
		if s.Start%60000 != 0 || s.End%60000 != 0 || date != localClose.Format("2006-01-02") || dates[date] {
			return fail()
		}
		for _, at := range []time.Time{open, close} {
			parsed, e := time.ParseInLocation("2006-01-02 15:04", at.In(zone).Format("2006-01-02 15:04"), zone)
			if e != nil || !parsed.UTC().Equal(at) {
				return fail()
			}
		}
		dates[date] = true
		if calendar.ValidFrom.IsZero() {
			calendar.ValidFrom = open
		}
		calendar.ValidUntil = close
		calendar.Sessions = append(calendar.Sessions, MarketSession{Date: date, OpenLocal: localOpen.Format("15:04"), CloseLocal: localClose.Format("15:04")})
	}
	// Irrelevant markets and refresh timestamps cannot churn a calendar version.
	calendar.Version = "venue-" + d.Digest([]any{r.Version, r.ProviderEnvironment, r.Binding, calendar})
	if _, err = calendar.Window(context.Background(), now); err != nil {
		return empty, err
	}
	return VenueCalendarArtifact{SchemaVersion: 1, Method: "binance-equity-calendar-1", ArtifactID: "calendar-" + d.Digest(r), Request: r, Calendar: calendar}, nil
}

func ValidateVenueCalendar(a VenueCalendarArtifact, binding d.Binding, now time.Time) error {
	if !binding.Valid() || binding != a.Request.Binding {
		return d.Fail("VENUE_CALENDAR_ARTIFACT_INVALID", 422)
	}
	rebuilt, err := CompileVenueCalendar(a.Request, now)
	if err != nil || d.Digest(rebuilt) != d.Digest(a) {
		return d.Fail("VENUE_CALENDAR_ARTIFACT_INVALID", 422)
	}
	return nil
}
