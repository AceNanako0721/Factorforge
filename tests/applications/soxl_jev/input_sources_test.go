package soxl_jev_test

import (
	"context"
	d "github.com/AceNanako0721/Factorforge/src/factorforge/applications/soxl_jev/domain"
	"github.com/AceNanako0721/Factorforge/src/factorforge/applications/soxl_jev/monitoring"
	p "github.com/AceNanako0721/Factorforge/src/factorforge/applications/soxl_jev/ports"
	"testing"
	"time"
)

type watchRouterFixture struct {
	rows []d.RawEvidence
	seed d.RawEvidence
}

func (s *watchRouterFixture) Search(_ context.Context, seed d.RawEvidence, _ time.Time) ([]d.RawEvidence, error) {
	s.seed = seed
	return s.rows, nil
}
func TestP3InputSearchOriginalClocksAndSourcePermissions(t *testing.T) {
	at := time.Date(2026, 7, 1, 16, 0, 0, 0, time.UTC)
	received := at.Add(time.Second)
	rows := []d.RawEvidence{{SourceID: "fixture-media", URL: "https://fixture.example/story", Content: `<html><head><title>NVIDIA new rule</title><meta property="article:published_time" content="2026-07-01T15:30:00Z"></head><body>Additional export rule<script>private-script-text</script></body></html>`, ReceivedAt: received}, {SourceID: "fixture-media", URL: "https://fixture.example/unknown-time", Content: `<title>NVIDIA update</title><p>Publication time unavailable</p>`, ReceivedAt: received}, {SourceID: "fixture-media", URL: "https://fixture.example/old-story", Content: `<meta property="article:published_time" content="2026-06-20T15:30:00Z"><p>Old information</p>`, ReceivedAt: received}}
	router := &watchRouterFixture{rows: rows}
	source := monitoring.WatchSearch{Router: router, Seeds: []d.RawEvidence{{SourceID: "fixture-plan", Content: "NVDA"}}, Grades: map[string]string{"fixture-media": "P1"}, MaxText: 100}
	articles, e := source.News(context.Background(), []d.WatchItem{{Symbol: "NVDA", Name: "NVIDIA"}}, nil, at.Add(-time.Hour), at)
	if e != nil || len(articles) != 2 || articles[0].PublishedAt == nil || articles[1].PublishedAt != nil || !articles[0].AvailableAt.Equal(received) || articles[0].Subject != "NVDA" || articles[0].Summary == "private-script-text" || !router.seed.ReceivedAt.Equal(at.Add(-time.Hour)) {
		t.Fatal("search timestamp/original mapping", articles, e)
	}
	calls := 0
	registration := d.SourceRegistration{SourceID: "fixture-official", LicenceRef: "fixture-licence", LicenceVerified: true, Enabled: true, AllowAnalysis: true, AllowProvider: true, AllowOriginal: true, Environments: []string{"SIM"}, ValidFrom: at.Add(-time.Hour), ValidUntil: at}
	official := monitoring.WatchOfficial{Sources: []p.MonitorSource{monitorFixture{calls: &calls}}, SourceIDs: []string{registration.SourceID}, Registry: map[string]d.SourceRegistration{registration.SourceID: registration}, Binding: d.Binding{InstanceID: "fixture-input", Environment: "SIM"}, MaxText: 100}
	if _, e := official.News(context.Background(), nil, nil, at.Add(-time.Hour), at); inputCode(e) != "SOURCE_LICENCE_REQUIRED" || calls != 0 {
		t.Fatal("expired licence was fetched", e, calls)
	}
}
