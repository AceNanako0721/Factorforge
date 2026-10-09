package experiments_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	d "github.com/AceNanako0721/Factorforge/src/factorforge/applications/soxl_jev/domain"
	"github.com/AceNanako0721/Factorforge/src/factorforge/applications/soxl_jev/operations"
	"golang.org/x/net/html"
)

func labText(node *html.Node) string {
	if node.Type == html.TextNode {
		return node.Data + " "
	}
	if node.Type == html.ElementNode && (node.Data == "script" || node.Data == "style") {
		return ""
	}
	var result strings.Builder
	for c := node.FirstChild; c != nil; c = c.NextSibling {
		result.WriteString(labText(c))
	}
	return strings.Join(strings.Fields(result.String()), " ")
}
func labElements(node *html.Node, tag string) []*html.Node {
	result := []*html.Node{}
	if node.Type == html.ElementNode && node.Data == tag {
		result = append(result, node)
	}
	for c := node.FirstChild; c != nil; c = c.NextSibling {
		result = append(result, labElements(c, tag)...)
	}
	return result
}

// This is a frozen October 2026 comparison, not a general NYSE calendar parser
// or a production source. HTML and exchange snapshots remain private captures.
func TestArchivedNYSECalendarReference(t *testing.T) {
	lab, productLab := os.Getenv("FACTORFORGE_REFERENCE_LAB"), os.Getenv("FACTORFORGE_PRODUCT_LAB")
	if lab == "" || productLab == "" {
		t.Skip("opt-in independent calendar comparison")
	}
	raw, e := os.ReadFile(filepath.Join(lab, "nyse.html"))
	if e != nil {
		t.Fatal(e)
	}
	doc, e := html.Parse(strings.NewReader(string(raw)))
	if e != nil {
		t.Fatal(e)
	}
	holidayDates := map[string]bool{}
	datePattern := regexp.MustCompile(`(January|February|March|April|May|June|July|August|September|October|November|December) [0-9]{1,2}`)
	for _, table := range labElements(doc, "table") {
		rows := labElements(table, "tr")
		if len(rows) == 0 {
			continue
		}
		headers := labElements(rows[0], "th")
		if len(headers) != 4 || labText(headers[0]) != "Holiday" || labText(headers[1]) != "2026" {
			continue
		}
		for _, row := range rows[1:] {
			cells := labElements(row, "td")
			if len(cells) != 3 {
				t.Fatal("NYSE_REFERENCE_TABLE_CHANGED")
			}
			date := datePattern.FindString(labText(cells[0]))
			at, e := time.Parse("January 2 2006", date+" 2026")
			if e != nil {
				t.Fatal("NYSE_REFERENCE_DATE_CHANGED")
			}
			holidayDates[at.Format("2006-01-02")] = true
		}
	}
	if len(holidayDates) != 10 {
		t.Fatal("NYSE_REFERENCE_HOLIDAYS_CHANGED", len(holidayDates))
	}
	core := ""
	for _, button := range labElements(doc, "button") {
		if labText(button) == "NYSE Arca Equities" && button.NextSibling != nil {
			core = labText(button.NextSibling)
			break
		}
	}
	hours := regexp.MustCompile(`Core Trading Session: ([0-9]{1,2}:[0-9]{2} [ap]\.m\.) to ([0-9]{1,2}:[0-9]{2} [ap]\.m\.) ET`).FindStringSubmatch(core)
	if len(hours) != 3 {
		t.Fatal("NYSE_ARCA_CORE_HOURS_UNAVAILABLE")
	}
	clock := func(s string) string {
		v, e := time.Parse("3:04 PM", strings.ReplaceAll(strings.ReplaceAll(s, "a.m.", "AM"), "p.m.", "PM"))
		if e != nil {
			t.Fatal(e)
		}
		return v.Format("15:04")
	}
	open, close := clock(hours[1]), clock(hours[2])
	var a operations.VenueCalendarArtifact
	b, e := os.ReadFile(filepath.Join(productLab, "DEMO-calendar-artifact.json"))
	if e != nil || d.DecodePrivate(b, &a) != nil || operations.ValidateVenueCalendar(a, a.Request.Binding, time.Now().UTC()) != nil {
		t.Fatal("VENUE_REFERENCE_ARTIFACT_INVALID")
	}
	actual := map[string]operations.MarketSession{}
	for _, s := range a.Calendar.Sessions {
		actual[s.Date] = s
	}
	first, _ := time.Parse("2006-01-02", a.Calendar.Sessions[0].Date)
	last, _ := time.Parse("2006-01-02", a.Calendar.Sessions[len(a.Calendar.Sessions)-1].Date)
	expected := 0
	mismatches := []string{}
	for day := first; !day.After(last); day = day.AddDate(0, 0, 1) {
		if day.Year() != 2026 || day.Month() != time.October {
			t.Fatal("NYSE_REFERENCE_OUTSIDE_FROZEN_SCOPE")
		}
		date := day.Format("2006-01-02")
		s, found := actual[date]
		trading := day.Weekday() != time.Saturday && day.Weekday() != time.Sunday && !holidayDates[date]
		if trading {
			expected++
		}
		if found != trading || found && (s.OpenLocal != open || s.CloseLocal != close) {
			mismatches = append(mismatches, date)
		}
	}
	if len(mismatches) != 0 || expected != len(a.Calendar.Sessions) {
		t.Fatal("NYSE_REFERENCE_MISMATCH", mismatches)
	}
	result, _ := json.MarshalIndent(map[string]any{"reference_url": "https://www.nyse.com/trade/hours-calendars", "reference_hash": d.ContentDigest(raw), "calendar_artifact": a.ArtifactID, "scope": "2026-10-01 through 2026-10-14 only", "holiday_rows": len(holidayDates), "expected_sessions": expected, "actual_sessions": len(actual), "core_open": open, "core_close": close, "mismatches": mismatches, "human_independent_review": false, "production_installed": false, "network_calls": 0, "orders": 0}, "", "  ")
	if os.WriteFile(filepath.Join(lab, "calendar-reference-report.json"), result, 0600) != nil {
		t.Fatal("NYSE_REFERENCE_WRITE_FAILED")
	}
	t.Logf("official_NYSE_Arca_core=%s-%s; calendar_sessions=%d; mismatches=0; human_review=false", open, close, expected)
}
