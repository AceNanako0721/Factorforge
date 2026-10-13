package monitoring

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	d "github.com/AceNanako0721/Factorforge/src/factorforge/applications/soxl_jev/domain"
	"golang.org/x/net/html"
	"strconv"
	"strings"
	"time"
)

// Registrations are private source assets. FOMC publication time and IR event
// selectors are explicit; page publication timestamps are not earnings dates.
type OfficialScheduleSource struct {
	Fetcher     *Fetcher
	Kind        string        `json:"kind"`
	URL         string        `json:"url"`
	SourceRef   string        `json:"source_ref"`
	Subject     string        `json:"subject"`
	Period      string        `json:"period"`
	NextPeriod  string        `json:"next_period"`
	ReleaseTime string        `json:"release_time"`
	Year        int           `json:"year"`
	MinInterval time.Duration `json:"-"`
	last        time.Time
}

func (s *OfficialScheduleSource) Schedules(ctx context.Context, now time.Time) ([]d.ScheduleCandidate, error) {
	if s.Fetcher == nil || s.SourceRef == "" || !d.UTC(now) || s.MinInterval <= 0 {
		return nil, d.Fail("SCHEDULE_SOURCE_CONFIGURATION_REQUIRED", 503)
	}
	if !s.last.IsZero() && now.Before(s.last.Add(s.MinInterval)) {
		return nil, nil
	}
	s.last = now
	raw, _, e := s.Fetcher.Get(ctx, s.URL)
	if e != nil {
		return nil, e
	}
	return ParseOfficialSchedule(raw, *s)
}
func textNode(n *html.Node) string {
	var b strings.Builder
	var walk func(*html.Node)
	walk = func(v *html.Node) {
		if v.Type == html.TextNode {
			b.WriteString(v.Data)
			b.WriteByte(' ')
		}
		for c := v.FirstChild; c != nil; c = c.NextSibling {
			walk(c)
		}
	}
	walk(n)
	return strings.Join(strings.Fields(b.String()), " ")
}
func attr(n *html.Node, key string) string {
	for _, a := range n.Attr {
		if a.Key == key {
			return a.Val
		}
	}
	return ""
}
func withClass(n *html.Node, class string) bool {
	return d.Has(strings.Fields(attr(n, "class")), class)
}
func descendants(root *html.Node, match func(*html.Node) bool) []*html.Node {
	out := []*html.Node{}
	var walk func(*html.Node)
	walk = func(n *html.Node) {
		if match(n) {
			out = append(out, n)
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			walk(c)
		}
	}
	walk(root)
	return out
}
func nyTime(date, clock string) (time.Time, error) {
	zone, e := time.LoadLocation("America/New_York")
	if e != nil {
		return time.Time{}, e
	}
	for _, layout := range []string{"2006-01-02 15:04", "2006-01-02 03:04 PM"} {
		if t, e := time.ParseInLocation(layout, date+" "+clock, zone); e == nil {
			return t.UTC(), nil
		}
	}
	return time.Time{}, d.Fail("SCHEDULE_TIME_INVALID", 422)
}
func candidate(s OfficialScheduleSource, kind, subject, period string, at time.Time) d.ScheduleCandidate {
	return d.ScheduleCandidate{EventType: kind, Subject: subject, Period: period, ExpectedAt: at, Certainty: "CONFIRMED", SourceRef: s.SourceRef, Status: "SCHEDULED"}
}
func ParseOfficialSchedule(raw []byte, s OfficialScheduleSource) ([]d.ScheduleCandidate, error) {
	out := []d.ScheduleCandidate{}
	bad := func() ([]d.ScheduleCandidate, error) { return nil, d.Fail("SCHEDULE_PAGE_INVALID", 422) }
	if s.SourceRef == "" {
		return bad()
	}
	switch s.Kind {
	case "TREASURY":
		var rows []struct {
			CUSIP            string `json:"cusip"`
			AuctionDate      string `json:"auctionDate"`
			AnnouncementDate string `json:"announcementDate"`
			Term             string `json:"originalSecurityTerm"`
			SecurityTerm     string `json:"securityTerm"`
			Time             string `json:"closingTimeCompetitive"`
			Tips             string `json:"tips"`
		}
		if json.Unmarshal(raw, &rows) != nil {
			return bad()
		}
		for _, r := range rows {
			term := r.Term
			if term == "" {
				term = r.SecurityTerm
			}
			kind := ""
			if term == "10-Year" {
				kind = "AUCTION_10Y"
			}
			if term == "30-Year" {
				kind = "AUCTION_30Y"
			}
			if kind == "" || r.Tips == "Yes" {
				continue
			}
			if len(r.AuctionDate) < 10 || len(r.AnnouncementDate) < 10 || r.CUSIP == "" {
				return bad()
			}
			at, e := nyTime(r.AuctionDate[:10], r.Time)
			if e != nil {
				return bad()
			}
			out = append(out, candidate(s, kind, "UST", r.CUSIP+"-"+r.AnnouncementDate[:10], at))
		}
	case "SEC":
		var doc struct {
			Filings struct {
				Recent struct {
					Form        []string `json:"form"`
					Items       []string `json:"items"`
					Acceptance  []string `json:"acceptanceDateTime"`
					ReportDates []string `json:"reportDate"`
				} `json:"recent"`
			} `json:"filings"`
		}
		if json.Unmarshal(raw, &doc) != nil || s.Subject == "" || s.Period == "" {
			return bad()
		}
		r := doc.Filings.Recent
		if len(r.Form) != len(r.Items) || len(r.Form) != len(r.Acceptance) {
			return bad()
		}
		for i, form := range r.Form {
			if form != "8-K" || !d.Has(strings.Split(strings.ReplaceAll(r.Items[i], " ", ""), ","), "2.02") {
				continue
			}
			at, e := time.Parse(time.RFC3339, r.Acceptance[i])
			if e != nil {
				return bad()
			}
			c := candidate(s, "EARNINGS_LEADER", s.Subject, s.Period, at.UTC())
			c.Actual = true
			out = append(out, c)
			// The next fiscal-period identity is registered explicitly by the caller;
			// estimates use that identity, never a guessed company fiscal calendar.
			if s.NextPeriod != "" {
				out = append(out, EstimateEarnings(s.Subject, s.NextPeriod, s.SourceRef, at.UTC()))
			}
			break
		}
	case "FOMC", "BLS", "IR":
		root, e := html.Parse(bytes.NewReader(raw))
		if e != nil {
			return bad()
		}
		switch s.Kind {
		case "FOMC":
			year := 0
			ordinal := 0
			nodes := descendants(root, func(n *html.Node) bool {
				return n.Type == html.ElementNode && (n.Data == "h4" || withClass(n, "fomc-meeting"))
			})
			for _, n := range nodes {
				if n.Data == "h4" {
					t := textNode(n)
					if strings.Contains(t, "FOMC Meetings") {
						fmt.Sscanf(t, "%d FOMC Meetings", &year)
					}
					continue
				}
				if year != s.Year {
					continue
				}
				ordinal++
				month, date := "", ""
				for _, v := range descendants(n, func(v *html.Node) bool {
					return withClass(v, "fomc-meeting__month") || withClass(v, "fomc-meeting__date")
				}) {
					if withClass(v, "fomc-meeting__month") {
						month = textNode(v)
					} else {
						date = textNode(v)
					}
				}
				parts := strings.Split(strings.TrimSuffix(date, "*"), "-")
				day, e := strconv.Atoi(parts[len(parts)-1])
				if e != nil {
					return bad()
				}
				months := strings.Split(month, "/")
				m, e := time.Parse("January", months[len(months)-1])
				if e != nil {
					return bad()
				}
				ds := fmt.Sprintf("%04d-%02d-%02d", year, m.Month(), day)
				at, e := nyTime(ds, s.ReleaseTime)
				if e != nil {
					return bad()
				}
				v := candidate(s, "MACRO_FOMC", "FOMC", fmt.Sprintf("%d-meeting-%d", year, ordinal), at)
				if attr(n, "data-status") == "CANCELLED" {
					v.Status = "CANCELLED"
				}
				out = append(out, v)
			}
		case "BLS":
			for _, row := range descendants(root, func(n *html.Node) bool { return n.Type == html.ElementNode && n.Data == "tr" }) {
				date, clock, desc := "", "", ""
				for _, n := range descendants(row, func(n *html.Node) bool {
					return withClass(n, "date-cell") || withClass(n, "time-cell") || withClass(n, "desc-cell")
				}) {
					if withClass(n, "date-cell") {
						date = textNode(n)
					}
					if withClass(n, "time-cell") {
						clock = textNode(n)
					}
					if withClass(n, "desc-cell") {
						desc = textNode(n)
					}
				}
				kind := ""
				if strings.HasPrefix(desc, "Consumer Price Index") {
					kind = "MACRO_CPI"
				}
				if strings.HasPrefix(desc, "Employment Situation") {
					kind = "MACRO_NFP"
				}
				if kind == "" {
					continue
				}
				dt, e := time.Parse("Monday, January 2, 2006", date)
				if e != nil {
					return bad()
				}
				at, e := nyTime(dt.Format("2006-01-02"), clock)
				if e != nil {
					return bad()
				}
				period := strings.SplitN(desc, " for ", 2)
				if len(period) != 2 || period[1] == "" {
					return bad()
				}
				v := candidate(s, kind, "BLS", period[1], at)
				if attr(row, "data-status") == "CANCELLED" {
					v.Status = "CANCELLED"
				}
				out = append(out, v)
			}
		case "IR":
			if s.Subject == "" || s.Period == "" {
				return bad()
			}
			for _, n := range descendants(root, func(n *html.Node) bool { return n.Data == "time" && withClass(n, "earnings-date") }) {
				at, e := time.Parse(time.RFC3339, attr(n, "datetime"))
				if e != nil {
					return bad()
				}
				c := candidate(s, "EARNINGS_LEADER", s.Subject, s.Period, at.UTC())
				if attr(n, "data-status") == "CANCELLED" {
					c.Status = "CANCELLED"
				}
				out = append(out, c)
			}
		}
	default:
		return bad()
	}
	if len(out) == 0 {
		return bad()
	}
	return out, nil
}

// Estimates are an explicit next-period registration, based on an official
// 8-K 2.02 acceptance. They cannot masquerade as confirmed announcements.
func EstimateEarnings(subject, nextPeriod, sourceRef string, lastAcceptance time.Time) d.ScheduleCandidate {
	return d.ScheduleCandidate{EventType: "EARNINGS_LEADER", Subject: subject, Period: nextPeriod, SourceRef: sourceRef, ExpectedAt: lastAcceptance.AddDate(0, 0, 91), Certainty: "ESTIMATED", Status: "SCHEDULED"}
}
