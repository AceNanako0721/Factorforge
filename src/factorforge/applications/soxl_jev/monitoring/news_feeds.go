package monitoring

import (
	"bytes"
	"context"
	"encoding/json"
	"encoding/xml"
	"io"
	"strconv"
	"strings"
	"time"

	d "github.com/AceNanako0721/Factorforge/src/factorforge/applications/soxl_jev/domain"
)

// NewsFeed is one registered headline source (S3-023). Kind RSS reads an
// RSS 2.0 channel; kind WSCN_LIVE reads the public live-news JSON. Chinese
// live news is licensed only for SIM/shadow; LIVE configuration rejects it.
type NewsFeed struct {
	SourceID     string `toml:"source_id" json:"source_id"`
	Kind         string `toml:"kind" json:"kind"`
	URL          string `toml:"url" json:"url"`
	LicenseScope string `toml:"license_scope" json:"license_scope"`
}

type NewsCollector struct {
	Fetcher *Fetcher
	Feeds   []NewsFeed
	Clock   func() time.Time
}

// FeedResult reports one source poll; Err is kept for the freshness gate.
type FeedResult struct {
	SourceID string
	Items    []d.NewsItem
	Err      error
}

func (c NewsCollector) Poll(ctx context.Context) []FeedResult {
	out := []FeedResult{}
	for _, feed := range c.Feeds {
		raw, _, err := c.Fetcher.Get(ctx, feed.URL)
		r := FeedResult{SourceID: feed.SourceID, Err: err}
		if err == nil {
			r.Items, r.Err = ParseNewsFeed(feed, raw, c.Clock())
		}
		out = append(out, r)
	}
	return out
}

// ParseNewsFeed converts one response to news items; recaps are flagged.
func ParseNewsFeed(feed NewsFeed, raw []byte, received time.Time) ([]d.NewsItem, error) {
	items := []d.NewsItem{}
	add := func(title, summary, link string, published time.Time) {
		title, summary = d.CleanText(title, 300), d.CleanText(summary, 600)
		if title == "" && summary == "" {
			return
		}
		if published.IsZero() || published.After(received) {
			published = received
		}
		items = append(items, d.NewsItem{ItemID: d.NewsItemID(feed.SourceID, link, title), Source: feed.SourceID, Title: title, Summary: summary, Link: link, PublishedAt: published.UTC(), ReceivedAt: received.UTC(), LicenseScope: feed.LicenseScope, Recap: d.PriceRecap(title + " " + summary)})
	}
	switch feed.Kind {
	case "RSS":
		var doc struct {
			Channel struct {
				Items []struct {
					Title       string `xml:"title"`
					Link        string `xml:"link"`
					Description string `xml:"description"`
					PubDate     string `xml:"pubDate"`
				} `xml:"item"`
			} `xml:"channel"`
		}
		decoder := xml.NewDecoder(bytes.NewReader(raw))
		// Feeds such as BEA use HTML entities (for example &ndash;) that plain
		// XML rejects; the stdlib table keeps the expansion bounded.
		decoder.Entity = xml.HTMLEntity
		// BEA declares us-ascii while serving valid UTF-8 bytes; accept the
		// ASCII label as-is and keep every other charset out of the parser.
		decoder.CharsetReader = func(charset string, input io.Reader) (io.Reader, error) {
			switch strings.ToLower(strings.TrimSpace(charset)) {
			case "us-ascii", "ascii":
				return input, nil
			default:
				return nil, d.Fail("NEWS_FEED_INVALID", 422)
			}
		}
		if decoder.Decode(&doc) != nil {
			return nil, d.Fail("NEWS_FEED_INVALID", 422)
		}
		for _, it := range doc.Channel.Items {
			published, _ := ParseRSSPublished(it.PubDate)
			add(it.Title, it.Description, it.Link, published)
		}
	case "WSCN_LIVE":
		var doc struct {
			Data struct {
				Items []struct {
					ID          json.Number `json:"id"`
					Title       string      `json:"title"`
					ContentText string      `json:"content_text"`
					URI         string      `json:"uri"`
					DisplayTime json.Number `json:"display_time"`
				} `json:"items"`
			} `json:"data"`
		}
		if json.Unmarshal(raw, &doc) != nil {
			return nil, d.Fail("NEWS_FEED_INVALID", 422)
		}
		for _, it := range doc.Data.Items {
			var published time.Time
			if v, err := strconv.ParseInt(it.DisplayTime.String(), 10, 64); err == nil && v > 0 {
				published = time.Unix(v, 0)
			}
			link := it.URI
			if link == "" {
				link = "wscn:" + it.ID.String()
			}
			add(it.Title, it.ContentText, link, published)
		}
	default:
		return nil, d.Fail("NEWS_FEED_KIND_INVALID", 422)
	}
	return items, nil
}
