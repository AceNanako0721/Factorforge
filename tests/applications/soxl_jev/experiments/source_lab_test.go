package experiments_test

import (
	"context"
	"encoding/json"
	"encoding/xml"
	"os"
	"path/filepath"
	"testing"
	"time"

	d "github.com/AceNanako0721/Factorforge/src/factorforge/applications/soxl_jev/domain"
	"github.com/AceNanako0721/Factorforge/src/factorforge/applications/soxl_jev/monitoring"
)

// This opt-in source experiment acquires clues and one original only. It has
// no config, provider, database, framework or trading identity and no retries.
func TestOfficialSourceMethodProbe(t *testing.T) {
	lab := os.Getenv("FACTORFORGE_SOURCE_LAB")
	if lab == "" {
		t.Skip("opt-in private source laboratory")
	}
	if !filepath.IsAbs(lab) {
		t.Fatal("SOURCE_LAB_PATH_INVALID")
	}
	var plan struct {
		FeedURL        string   `json:"feed_url"`
		Hosts          []string `json:"hosts"`
		MaxBytes       int      `json:"max_bytes"`
		TimeoutSeconds int      `json:"timeout_seconds"`
		MaxOriginals   int      `json:"max_originals"`
	}
	encoded, err := os.ReadFile(filepath.Join(lab, "plan.json"))
	if err != nil || d.DecodePrivate(encoded, &plan) != nil || plan.MaxOriginals != 1 {
		t.Fatal("SOURCE_LAB_PLAN_INVALID")
	}
	f, err := monitoring.NewFetcher(monitoring.FetchPolicy{Hosts: plan.Hosts, MaxBytes: plan.MaxBytes, Timeout: time.Duration(plan.TimeoutSeconds) * time.Second})
	if err != nil {
		t.Fatal("SOURCE_LAB_POLICY_INVALID")
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Duration(plan.TimeoutSeconds)*time.Second)
	defer cancel()
	feed, media, err := f.Get(ctx, plan.FeedURL)
	if err != nil {
		t.Fatal("SOURCE_LAB_FEED_UNAVAILABLE")
	}
	if err = os.WriteFile(filepath.Join(lab, "feed.xml"), feed, 0600); err != nil {
		t.Fatal("SOURCE_LAB_WRITE_FAILED")
	}
	var rss struct {
		Channel struct {
			Items []struct {
				Link      string `xml:"link"`
				Published string `xml:"pubDate"`
			} `xml:"item"`
		} `xml:"channel"`
	}
	if xml.Unmarshal(feed, &rss) != nil || len(rss.Channel.Items) == 0 {
		t.Fatal("SOURCE_LAB_XML_INVALID")
	}
	// Inspect the real feed's item count before selecting one read-only original.
	// This does not silently change RSSSource's production whole-feed budget.
	item := rss.Channel.Items[0]
	pub, known := monitoring.ParseRSSPublished(item.Published)
	if !known {
		t.Fatal("SOURCE_LAB_PUBLICATION_UNKNOWN")
	}
	body, kind, err := f.Get(ctx, item.Link)
	if err != nil {
		t.Fatal("SOURCE_LAB_ORIGINAL_UNAVAILABLE")
	}
	if err = os.WriteFile(filepath.Join(lab, "original.html"), body, 0600); err != nil {
		t.Fatal("SOURCE_LAB_WRITE_FAILED")
	}
	report := struct {
		FeedURL                string     `json:"feed_url"`
		FeedMedia              string     `json:"feed_media"`
		FeedItems              int        `json:"feed_items"`
		FeedBytes              int        `json:"feed_bytes"`
		FeedHash               string     `json:"feed_hash"`
		OriginalURL            string     `json:"original_url"`
		OriginalMedia          string     `json:"original_media"`
		OriginalBytes          int        `json:"original_bytes"`
		OriginalHash           string     `json:"original_hash"`
		ProviderPublishedAt    time.Time  `json:"provider_published_at"`
		FirstPublicAt          *time.Time `json:"first_public_at"`
		ReceivedAt             time.Time  `json:"received_at"`
		ProductionRegistration string     `json:"production_registration"`
		ModelCalls             int        `json:"model_calls"`
		DownstreamWrites       int        `json:"downstream_writes"`
	}{plan.FeedURL, media, len(rss.Channel.Items), len(feed), d.ContentDigest(feed), item.Link, kind, len(body), d.ContentDigest(body), pub.UTC(), nil, time.Now().UTC(), "NOT_INSTALLED", 0, 0}
	b, err := json.MarshalIndent(report, "", "  ")
	if err != nil || os.WriteFile(filepath.Join(lab, "report.json"), b, 0600) != nil {
		t.Fatal("SOURCE_LAB_WRITE_FAILED")
	}
	t.Logf("feed_items=%d; original_bytes=%d; first_public_at=UNKNOWN; model_calls=0; downstream_writes=0", len(rss.Channel.Items), len(body))
}
