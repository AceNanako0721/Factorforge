package monitoring

import (
	"bytes"
	"context"
	d "github.com/AceNanako0721/Factorforge/src/factorforge/applications/soxl_jev/domain"
	p "github.com/AceNanako0721/Factorforge/src/factorforge/applications/soxl_jev/ports"
	"golang.org/x/net/html"
	"strings"
	"time"
)

// WatchSearch delegates discovery, cache and provider limits to the existing
// search router. Originals are fetched only from its licensed source registry.
type WatchSearch struct {
	Router   p.SearchProvider
	Seeds    []d.RawEvidence
	Grades   map[string]string
	Official map[string]bool
	MaxText  int
}

func (s WatchSearch) News(ctx context.Context, items []d.WatchItem, themes []string, from, to time.Time) ([]d.WatchArticle, error) {
	if s.Router == nil || len(s.Seeds) == 0 || s.MaxText <= 0 {
		return nil, d.Fail("WATCH_SEARCH_CONFIGURATION_REQUIRED", 503)
	}
	out := []d.WatchArticle{}
	for _, seed := range s.Seeds {
		seed.ReceivedAt = from
		rows, e := s.Router.Search(ctx, seed, to)
		if e != nil {
			return nil, e
		}
		for _, r := range rows {
			title, summary, published := watchText(r.Content, s.MaxText)
			if r.PublishedAt != nil {
				published = r.PublishedAt
			}
			if published != nil && (published.Before(from) || published.After(to)) {
				continue
			}
			subject := seed.Content
			for _, item := range items {
				text := strings.ToLower(title + " " + summary)
				if strings.Contains(text, strings.ToLower(item.Symbol)) || item.Name != "" && strings.Contains(text, strings.ToLower(item.Name)) {
					subject = item.Symbol
					break
				}
			}
			if title == "" {
				title = subject
			}
			out = append(out, d.WatchArticle{URL: r.URL, Subject: subject, Title: title, Summary: summary, SourceGrade: s.Grades[r.SourceID], Official: s.Official[r.SourceID], AvailableAt: r.ReceivedAt, PublishedAt: published})
		}
	}
	return out, nil
}

func watchText(raw string, limit int) (string, string, *time.Time) {
	root, e := html.Parse(bytes.NewBufferString(raw))
	if e != nil {
		return "", "", nil
	}
	title := ""
	var published *time.Time
	for _, n := range descendants(root, func(n *html.Node) bool { return n.Data == "title" || n.Data == "meta" }) {
		if n.Data == "title" {
			title = textNode(n)
		}
		if attr(n, "property") == "article:published_time" {
			if t, e := time.Parse(time.RFC3339, attr(n, "content")); e == nil {
				t = t.UTC()
				published = &t
			}
		}
	}
	var b strings.Builder
	var walk func(*html.Node)
	walk = func(n *html.Node) {
		if n.Data == "script" || n.Data == "style" || n.Data == "nav" {
			return
		}
		if n.Type == html.TextNode {
			b.WriteString(n.Data)
			b.WriteByte(' ')
		}
		for child := n.FirstChild; child != nil; child = child.NextSibling {
			walk(child)
		}
	}
	walk(root)
	text := []rune(strings.Join(strings.Fields(b.String()), " "))
	if len(text) > limit {
		text = text[:limit]
	}
	return title, string(text), published
}

type WatchOfficial struct {
	Sources   []p.MonitorSource
	Grades    map[string]string
	Subjects  map[string]string
	MaxText   int
	Registry  map[string]d.SourceRegistration
	Binding   d.Binding
	SourceIDs []string
}

func (s WatchOfficial) News(ctx context.Context, _ []d.WatchItem, _ []string, from, to time.Time) ([]d.WatchArticle, error) {
	out := []d.WatchArticle{}
	if s.MaxText <= 0 {
		return nil, d.Fail("WATCH_SOURCE_CONFIGURATION_REQUIRED", 503)
	}
	if s.Registry != nil && len(s.SourceIDs) != len(s.Sources) {
		return nil, d.Fail("WATCH_SOURCE_CONFIGURATION_REQUIRED", 503)
	}
	for index, source := range s.Sources {
		if s.Registry != nil {
			registered, ok := s.Registry[s.SourceIDs[index]]
			if !ok || !registered.Enabled || !registered.LicenceVerified || !registered.AllowAnalysis || !registered.AllowProvider || !registered.AllowOriginal || !d.Has(registered.Environments, s.Binding.Environment) || to.Before(registered.ValidFrom) || !to.Before(registered.ValidUntil) {
				return nil, d.Fail("SOURCE_LICENCE_REQUIRED", 403)
			}
		}
		rows, e := source.Poll(ctx, to)
		if e != nil {
			return nil, e
		}
		for _, r := range rows {
			if s.Registry != nil {
				registered, ok := s.Registry[r.SourceID]
				if !ok || !registered.Enabled || !registered.LicenceVerified || !registered.AllowAnalysis || !registered.AllowProvider || !registered.AllowOriginal || !d.Has(registered.Environments, s.Binding.Environment) || to.Before(registered.ValidFrom) || !to.Before(registered.ValidUntil) || registered.LicenceRef != r.LicenceRef {
					return nil, d.Fail("SOURCE_LICENCE_REQUIRED", 403)
				}
			}
			if r.PublishedAt == nil || r.PublishedAt.Before(from) || r.PublishedAt.After(to) {
				continue
			}
			title, text, _ := watchText(r.Content, s.MaxText)
			if title == "" {
				title = s.Subjects[r.SourceID]
			}
			out = append(out, d.WatchArticle{URL: r.URL, Subject: s.Subjects[r.SourceID], Title: title, Summary: text, SourceGrade: s.Grades[r.SourceID], Official: true, AvailableAt: r.ReceivedAt, PublishedAt: r.PublishedAt})
		}
	}
	return out, nil
}
