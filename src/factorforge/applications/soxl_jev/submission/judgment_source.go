package submission

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"time"

	d "github.com/AceNanako0721/Factorforge/src/factorforge/applications/soxl_jev/domain"
)

// JudgmentSourceOptions bind the follower to one source instance's v2.10.0
// read API (S3-032) with a read-only token (S3-034, P3 design 34).
type JudgmentSourceOptions struct {
	BaseURL, Token, InstanceID string
	Client                     *http.Client
	MaxResponseBytes, MaxPages int
}
type JudgmentSourceHTTP struct{ options JudgmentSourceOptions }

func NewJudgmentSource(o JudgmentSourceOptions) (*JudgmentSourceHTTP, error) {
	u, err := url.Parse(o.BaseURL)
	if err != nil || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || strings.TrimRight(u.Path, "/") != "" || !d.Has([]string{"http", "https"}, u.Scheme) ||
		!d.ValidID(o.InstanceID) || o.Token == "" || o.Client == nil || o.Client.Timeout <= 0 || o.MaxResponseBytes <= 0 || o.MaxPages <= 0 {
		return nil, d.Fail("JUDGMENT_SOURCE_CONFIGURATION_REQUIRED", 503)
	}
	client := *o.Client
	client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	o.Client = &client
	o.BaseURL = strings.TrimRight(o.BaseURL, "/")
	return &JudgmentSourceHTTP{o}, nil
}

type sourceNewsRef struct {
	ItemID       string     `json:"item_id"`
	Source       *string    `json:"source"`
	Title        *string    `json:"title"`
	PublishedAt  *time.Time `json:"published_at"`
	LicenseScope *string    `json:"license_scope"`
}
type sourceJudgment struct {
	JudgmentID          string          `json:"judgment_id"`
	At                  time.Time       `json:"at"`
	OK                  bool            `json:"ok"`
	Reason              *string         `json:"reason"`
	Provider            *string         `json:"provider"`
	Model               *string         `json:"model"`
	Direction           *string         `json:"direction"`
	Confidence          *int            `json:"confidence"`
	HorizonHours        *int            `json:"horizon_hours"`
	Thesis              *string         `json:"thesis"`
	CounterPoints       *string         `json:"counter_points"`
	Invalidation        *string         `json:"invalidation"`
	PreviousInvalidated *bool           `json:"previous_invalidated"`
	KeyItems            []sourceNewsRef `json:"key_items"`
	InvalidationItems   []sourceNewsRef `json:"invalidation_items"`
	ErrorCodes          []string        `json:"error_codes"`
}
type sourcePage struct {
	InstanceID string           `json:"instance_id"`
	Items      []sourceJudgment `json:"items"`
	Cursor     *string          `json:"cursor"`
}

// After returns the source's judgments recorded strictly after since, oldest
// first. The read API pages newest first; every page of the window is read.
// It reports times to the second, so the judgment at the cursor itself comes
// back again; entries not after since are skipped as that boundary (v2.14.1).
func (c *JudgmentSourceHTTP) After(ctx context.Context, since time.Time) ([]d.SourcedJudgment, error) {
	o := c.options
	from := since.UTC().Add(time.Nanosecond).Format(time.RFC3339Nano)
	cursor := ""
	rows := []sourceJudgment{}
	for page := 0; ; page++ {
		if page == o.MaxPages {
			return nil, d.Fail("JUDGMENT_SOURCE_PAGES_EXCEEDED", 503)
		}
		q := url.Values{"from": {from}, "limit": {"100"}}
		if cursor != "" {
			q.Set("cursor", cursor)
		}
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, o.BaseURL+"/api/v2/instances/"+o.InstanceID+"/dynamic/judgments?"+q.Encode(), nil)
		if err != nil {
			return nil, d.Fail("JUDGMENT_SOURCE_REQUEST_INVALID", 422)
		}
		req.Header.Set("Authorization", "Bearer "+o.Token)
		response, err := o.Client.Do(req)
		if err != nil {
			return nil, d.Fail("JUDGMENT_SOURCE_UNAVAILABLE", 503)
		}
		raw, err := io.ReadAll(io.LimitReader(response.Body, int64(o.MaxResponseBytes)+1))
		response.Body.Close()
		if err != nil || len(raw) > o.MaxResponseBytes {
			return nil, d.Fail("JUDGMENT_SOURCE_BUDGET_EXCEEDED", 503)
		}
		if response.StatusCode != 200 {
			return nil, d.Fail("JUDGMENT_SOURCE_REJECTED", response.StatusCode)
		}
		var p sourcePage
		if json.Unmarshal(raw, &p) != nil || p.InstanceID != o.InstanceID {
			return nil, d.Fail("JUDGMENT_SOURCE_RESPONSE_INVALID", 502)
		}
		rows = append(rows, p.Items...)
		if p.Cursor == nil {
			break
		}
		cursor = *p.Cursor
	}
	out := make([]d.SourcedJudgment, 0, len(rows))
	for _, r := range rows {
		if !d.ValidID(r.JudgmentID) {
			return nil, d.Fail("JUDGMENT_SOURCE_RESPONSE_INVALID", 502)
		}
		if !r.At.After(since) {
			continue
		}
		at := r.At.UTC()
		j := d.Judgment{JudgmentID: r.JudgmentID, At: at, OK: r.OK, Reason: optText(r.Reason), Provider: optText(r.Provider), Model: optText(r.Model), InputIDs: []string{}, Errors: r.ErrorCodes}
		items := []d.NewsItem{}
		ids := func(list []sourceNewsRef) []string {
			out := []string{}
			for _, n := range list {
				out = append(out, n.ItemID)
				x := d.NewsItem{ItemID: n.ItemID, Source: optText(n.Source), Title: optText(n.Title), ReceivedAt: at, LicenseScope: optText(n.LicenseScope)}
				if n.PublishedAt != nil {
					x.PublishedAt = n.PublishedAt.UTC()
				}
				// A cited item the source no longer holds cannot back evidence.
				// Quick news carries no headline, so the title may be empty (v2.14.4).
				if x.Source != "" && !x.PublishedAt.IsZero() {
					items = append(items, x)
				}
			}
			return out
		}
		if r.OK {
			if r.Direction == nil || r.Confidence == nil || r.HorizonHours == nil || r.PreviousInvalidated == nil {
				return nil, d.Fail("JUDGMENT_SOURCE_RESPONSE_INVALID", 502)
			}
			j.Output = &d.JudgmentOutput{Direction: *r.Direction, Confidence: *r.Confidence, HorizonHours: *r.HorizonHours, Thesis: optText(r.Thesis), CounterPoints: optText(r.CounterPoints), Invalidation: optText(r.Invalidation), PreviousInvalidated: *r.PreviousInvalidated, KeyItemIDs: ids(r.KeyItems), InvalidationItemIDs: ids(r.InvalidationItems)}
		}
		out = append(out, d.SourcedJudgment{Judgment: j, Items: items})
	}
	sort.SliceStable(out, func(a, b int) bool {
		if !out[a].Judgment.At.Equal(out[b].Judgment.At) {
			return out[a].Judgment.At.Before(out[b].Judgment.At)
		}
		return out[a].Judgment.JudgmentID < out[b].Judgment.JudgmentID
	})
	return out, nil
}

func optText(v *string) string {
	if v == nil {
		return ""
	}
	return *v
}
