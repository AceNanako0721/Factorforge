package monitoring

import (
	"context"
	"encoding/json"
	"fmt"
	d "github.com/AceNanako0721/Factorforge/src/factorforge/applications/soxl_jev/domain"
	"html"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"
)

// SECFilingSource obtains 8-K press-release exhibits from SEC EDGAR for the
// registered companies (v2.5.3 design chapter 25). EDGAR acceptance is only the
// provider publication time; it never becomes a verified first-public time.
type SECFilingSource struct {
	Fetcher               *Fetcher
	SourceID, LicenceRef  string
	Companies             []SECCompany
	Items                 []string
	Lookback              time.Duration
	MaxFilings            int
	MaxMetadataBytes      int
	MaxExhibitBytes       int
	MinInterval           time.Duration
	DataBase, ArchiveBase string
	Clock                 func() time.Time
	// Wait paces requests; nil uses a cancellable timer.
	Wait func(context.Context, time.Duration) error

	mu       sync.Mutex
	returned map[string]bool
	last     time.Time
}

type SECCompany struct {
	Ticker string `json:"ticker"`
	CIK    int64  `json:"cik"`
}

type secCandidate struct {
	company  SECCompany
	number   string
	accepted time.Time
}

const (
	SECDataBase    = "https://data.sec.gov"
	SECArchiveBase = "https://www.sec.gov"
)

var (
	secItemPattern      = regexp.MustCompile(`^[0-9]\.[0-9]{2}$`)
	secAccessionPattern = regexp.MustCompile(`^[0-9]{10}-[0-9]{2}-[0-9]{6}$`)
	secTickerPattern    = regexp.MustCompile(`^[A-Z][A-Z0-9.-]{0,9}$`)
	secRowPattern       = regexp.MustCompile(`(?is)<tr[^>]*>(.*?)</tr>`)
	secCellPattern      = regexp.MustCompile(`(?is)<td[^>]*>(.*?)</td>`)
	secHrefPattern      = regexp.MustCompile(`(?i)href="([^"]*)"`)
	secTagPattern       = regexp.MustCompile(`(?s)<[^>]*>`)
)

func (s *SECFilingSource) valid(cutoff time.Time) bool {
	if s.Fetcher == nil || s.Clock == nil || !d.UTC(cutoff) || !d.ValidID(s.SourceID) || !d.ValidID(s.LicenceRef) || len(s.Companies) == 0 || len(s.Items) == 0 ||
		s.Lookback <= 0 || s.MaxFilings <= 0 || s.MaxMetadataBytes <= 0 || s.MaxExhibitBytes <= 0 || s.MinInterval < 100*time.Millisecond || s.DataBase == "" || s.ArchiveBase == "" {
		return false
	}
	tickers := map[string]bool{}
	for _, c := range s.Companies {
		if c.CIK <= 0 || c.CIK > 9999999999 || !secTickerPattern.MatchString(c.Ticker) || tickers[c.Ticker] {
			return false
		}
		tickers[c.Ticker] = true
	}
	for _, item := range s.Items {
		if !secItemPattern.MatchString(item) {
			return false
		}
	}
	return true
}

func (s *SECFilingSource) Poll(ctx context.Context, cutoff time.Time) ([]d.RawEvidence, error) {
	if !s.valid(cutoff) {
		return nil, d.Fail("SOURCE_CONFIGURATION_REQUIRED", 503)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.returned == nil {
		s.returned = map[string]bool{}
	}
	candidates := []secCandidate{}
	for _, company := range s.Companies {
		raw, err := s.get(ctx, fmt.Sprintf("%s/submissions/CIK%010d.json", s.DataBase, company.CIK), []string{"application/json"}, s.MaxMetadataBytes)
		if err != nil {
			return nil, err
		}
		filings, err := SECRecent8K(raw, s.Items, cutoff.Add(-s.Lookback), cutoff)
		if err != nil {
			return nil, err
		}
		for _, f := range filings {
			candidates = append(candidates, secCandidate{company, f.Accession, f.AcceptedAt})
		}
	}
	if len(candidates) > s.MaxFilings {
		return nil, d.Fail("SOURCE_FILING_BUDGET_EXCEEDED", 422)
	}
	sort.SliceStable(candidates, func(i, j int) bool { return candidates[i].accepted.Before(candidates[j].accepted) })
	result, numbers := []d.RawEvidence{}, []string{}
	for _, c := range candidates {
		if s.returned[c.number] {
			continue
		}
		folder := fmt.Sprintf("/Archives/edgar/data/%d/%s/", c.company.CIK, strings.ReplaceAll(c.number, "-", ""))
		index, err := s.get(ctx, s.ArchiveBase+folder+c.number+"-index.htm", []string{"text/html"}, s.MaxMetadataBytes)
		if err != nil {
			return nil, err
		}
		href, found, err := SECPressExhibit(index, folder)
		if err != nil {
			return nil, err
		}
		numbers = append(numbers, c.number)
		if !found {
			continue
		}
		link := s.ArchiveBase + href
		body, err := s.get(ctx, link, []string{"text/html", "application/xhtml+xml", "text/plain"}, s.MaxExhibitBytes)
		if err != nil {
			return nil, err
		}
		hash := d.ContentDigest(body)
		accepted := c.accepted
		result = append(result, d.RawEvidence{EvidenceID: "evidence-" + d.Digest([]string{s.SourceID, link, hash}), SourceID: s.SourceID, URL: link, ContentHash: hash, Content: string(body), PublishedAt: &accepted, ReceivedAt: s.Clock().UTC(), LicenceRef: s.LicenceRef})
	}
	// Only a fully successful poll is remembered; a failed poll is reconsidered
	// at the next scheduled cycle instead of being retried immediately.
	for _, number := range numbers {
		s.returned[number] = true
	}
	return result, nil
}

func (s *SECFilingSource) get(ctx context.Context, endpoint string, media []string, limit int) ([]byte, error) {
	if !s.last.IsZero() {
		wait := s.MinInterval - s.Clock().Sub(s.last)
		if wait > 0 {
			pause := s.Wait
			if pause == nil {
				pause = secSleep
			}
			if err := pause(ctx, wait); err != nil {
				return nil, d.Fail("SOURCE_FETCH_UNAVAILABLE", 503)
			}
		}
	}
	s.last = s.Clock()
	raw, kind, err := s.Fetcher.Get(ctx, endpoint)
	if err != nil {
		return nil, err
	}
	if !d.Has(media, kind) {
		if d.Has(media, "application/json") || strings.HasSuffix(endpoint, "-index.htm") {
			return nil, d.Fail("SEC_METADATA_FORMAT_INVALID", 422)
		}
		return nil, d.Fail("MISSING_ORIGINAL", 422)
	}
	if len(raw) > limit {
		return nil, d.Fail("SOURCE_BODY_BUDGET_EXCEEDED", 503)
	}
	return raw, nil
}

func secSleep(ctx context.Context, wait time.Duration) error {
	timer := time.NewTimer(wait)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

type SECFiling struct {
	Accession  string
	AcceptedAt time.Time
	Items      []string
}

// SECRecent8K selects original 8-K filings accepted in (from, to] whose items
// intersect the allowed set. Amendments are excluded until a revision mapping
// to the original evidence is designed.
func SECRecent8K(raw []byte, allowed []string, from, to time.Time) ([]SECFiling, error) {
	var doc struct {
		Filings struct {
			Recent struct {
				Form               []string `json:"form"`
				AccessionNumber    []string `json:"accessionNumber"`
				AcceptanceDateTime []string `json:"acceptanceDateTime"`
				Items              []string `json:"items"`
			} `json:"recent"`
		} `json:"filings"`
	}
	if json.Unmarshal(raw, &doc) != nil {
		return nil, d.Fail("SEC_SUBMISSIONS_INVALID", 422)
	}
	r := doc.Filings.Recent
	n := len(r.Form)
	if len(r.AccessionNumber) != n || len(r.AcceptanceDateTime) != n || len(r.Items) != n {
		return nil, d.Fail("SEC_SUBMISSIONS_INVALID", 422)
	}
	out := []SECFiling{}
	for i := range r.Form {
		if r.Form[i] != "8-K" {
			continue
		}
		accepted, err := time.Parse(time.RFC3339Nano, r.AcceptanceDateTime[i])
		if err != nil || !secAccessionPattern.MatchString(r.AccessionNumber[i]) {
			return nil, d.Fail("SEC_SUBMISSIONS_INVALID", 422)
		}
		accepted = accepted.UTC()
		if !accepted.After(from) || accepted.After(to) {
			continue
		}
		items := []string{}
		match := false
		for _, item := range strings.Split(r.Items[i], ",") {
			item = strings.TrimSpace(item)
			if item == "" {
				continue
			}
			items = append(items, item)
			match = match || d.Has(allowed, item)
		}
		if match {
			out = append(out, SECFiling{r.AccessionNumber[i], accepted, items})
		}
	}
	return out, nil
}

// SECPressExhibit returns the first EX-99 document listed in a filing index.
// A listed document outside the filing folder is rejected, not followed.
func SECPressExhibit(raw []byte, folder string) (string, bool, error) {
	for _, row := range secRowPattern.FindAllSubmatch(raw, -1) {
		cells := secCellPattern.FindAllSubmatch(row[1], -1)
		if len(cells) < 4 {
			continue
		}
		kind := strings.ToUpper(strings.TrimSpace(html.UnescapeString(secTagPattern.ReplaceAllString(string(cells[3][1]), ""))))
		if !strings.HasPrefix(kind, "EX-99") {
			continue
		}
		href := secHrefPattern.FindSubmatch(cells[2][1])
		if href == nil {
			return "", false, d.Fail("SEC_INDEX_INVALID", 422)
		}
		link := html.UnescapeString(string(href[1]))
		name := strings.TrimPrefix(link, folder)
		if !strings.HasPrefix(link, folder) || name == "" || strings.ContainsAny(name, "/?#\\") || strings.Contains(name, "..") {
			return "", false, d.Fail("SEC_INDEX_INVALID", 422)
		}
		return link, true, nil
	}
	return "", false, nil
}
