package soxl_jev_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/AceNanako0721/Factorforge/src/factorforge/applications/soxl_jev/config"
	d "github.com/AceNanako0721/Factorforge/src/factorforge/applications/soxl_jev/domain"
	"github.com/AceNanako0721/Factorforge/src/factorforge/applications/soxl_jev/submission"
	"github.com/AceNanako0721/Factorforge/src/factorforge/applications/soxl_jev/workers"
)

// ---------- v2.13.0 judgment follower (S3-034) ----------

type fakeSource struct {
	rows   []d.SourcedJudgment
	asked  []time.Time
	failed error
}

func (s *fakeSource) After(_ context.Context, since time.Time) ([]d.SourcedJudgment, error) {
	s.asked = append(s.asked, since)
	if s.failed != nil {
		return nil, s.failed
	}
	out := []d.SourcedJudgment{}
	for _, r := range s.rows {
		if r.Judgment.At.After(since) {
			out = append(out, r)
		}
	}
	return out, nil
}

func TestT330FollowerCopiesAndAppliesSourceJudgments(t *testing.T) {
	at := time.Date(2026, 10, 23, 3, 1, 5, 0, time.UTC)
	w, store, fw, _ := judgmentWorker(at, fakeModel{err: d.Fail("MODEL_ACCESS_FAILED", 503)}, nil)
	w.Models, w.News = nil, nil
	item := d.NewsItem{ItemID: "item0000000001", Source: "cnbc-tech", Title: "Chipmaker raises guidance", PublishedAt: at.Add(-40 * time.Minute), ReceivedAt: at, LicenseScope: "RESEARCH_SIM"}
	before := judgment("j-20261023T023105Z", at.Add(-30*time.Minute), "SHORT", 4, []string{"item0000000009"}, false)
	stale := d.Judgment{JudgmentID: "j-20261023T030105Z", At: at, Reason: "NEWS_STALE"}
	long := judgment("j-20261023T033105Z", at.Add(30*time.Minute), "LONG", 3, []string{item.ItemID}, false)
	item.ReceivedAt = long.At
	src := &fakeSource{rows: []d.SourcedJudgment{{Judgment: before}, {Judgment: stale}, {Judgment: long, Items: []d.NewsItem{item}}}}
	w.Source, w.FollowSince = src, at.Add(-time.Minute)
	if err := w.Follow(context.Background()); err != nil {
		t.Fatal(err)
	}
	// Judgments before the registered start are never read.
	if !src.asked[0].Equal(w.FollowSince) {
		t.Fatal("start", src.asked)
	}
	rows, _ := store.Records(context.Background(), "JUDGMENT", at.Add(-time.Hour), at.Add(time.Hour), 10)
	if len(rows) != 2 {
		t.Fatal("copied judgments", len(rows))
	}
	news, _ := store.Records(context.Background(), "NEWS", at.Add(-time.Hour), at.Add(time.Hour), 10)
	if len(news) != 1 {
		t.Fatal("copied news", len(news))
	}
	// The LONG judgment injects exactly as a generated one would (S3-025).
	if len(fw.events) != 1 || len(fw.scores) != 1 || fw.scores[0].Score.Vector.Direction != 1 || fw.events[0].Event.EvidenceRefs[0].EvidenceID != item.ItemID {
		t.Fatal("injection", len(fw.events), len(fw.scores))
	}
	if fw.scores[0].Score.Vector.ImpactPoints.String() != "0.65" || fw.scores[0].Score.Vector.ExpectedHalfLife.String() != "86400" {
		t.Fatal("score", fw.scores[0].Score.Vector)
	}
	// The cursor advances; a later FLAT is recorded without injection and
	// nothing already copied is applied twice.
	flat := judgment("j-20261023T040105Z", at.Add(time.Hour), "FLAT", 2, []string{item.ItemID}, false)
	src.rows = append(src.rows, d.SourcedJudgment{Judgment: flat, Items: []d.NewsItem{item}})
	if err := w.Follow(context.Background()); err != nil {
		t.Fatal(err)
	}
	if !src.asked[1].Equal(long.At) || len(fw.events) != 1 || len(fw.scores) != 1 {
		t.Fatal("second pass", src.asked, len(fw.events))
	}
	var cursor workers.FollowState
	_, _ = store.LoadState(context.Background(), "FOLLOW", &cursor)
	if cursor.LastID != flat.JudgmentID {
		t.Fatal("cursor", cursor)
	}
	// A source outage writes nothing and keeps the cursor.
	src.failed = d.Fail("JUDGMENT_SOURCE_UNAVAILABLE", 503)
	if err := w.Follow(context.Background()); err == nil {
		t.Fatal("outage must surface")
	}
	if len(fw.events) != 1 {
		t.Fatal("outage wrote")
	}
}

func TestT330JudgmentSourceReadsAllPagesOldestFirst(t *testing.T) {
	at := time.Date(2026, 10, 23, 3, 1, 5, 0, time.UTC)
	ref := map[string]any{"item_id": "item0000000001", "source": "cnbc-tech", "title": "Chipmaker raises guidance", "published_at": at.Add(-time.Hour), "license_scope": "RESEARCH_SIM"}
	view := func(id string, at time.Time, dir string) map[string]any {
		conf, hz, prev := 3, 24, false
		return map[string]any{"judgment_id": id, "at": at, "ok": true, "direction": dir, "confidence": conf, "horizon_hours": hz, "previous_invalidated": prev, "thesis": "t", "key_items": []any{ref}, "invalidation_items": []any{}, "error_codes": []string{}}
	}
	auth, instance := "", "soxl-e15"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		auth = r.Header.Get("Authorization")
		if r.URL.Path != "/api/v2/instances/soxl-e15/dynamic/judgments" || r.URL.Query().Get("from") == "" {
			w.WriteHeader(404)
			return
		}
		page := map[string]any{"instance_id": instance, "items": []any{view("j-2", at.Add(30*time.Minute), "LONG")}, "cursor": "next"}
		if r.URL.Query().Get("cursor") == "next" {
			page = map[string]any{"instance_id": instance, "items": []any{view("j-1", at, "SHORT")}, "cursor": nil}
		}
		_ = json.NewEncoder(w).Encode(page)
	}))
	defer server.Close()
	src, err := submission.NewJudgmentSource(submission.JudgmentSourceOptions{BaseURL: server.URL, Token: "read-token", InstanceID: "soxl-e15", Client: &http.Client{Timeout: time.Second}, MaxResponseBytes: 1 << 20, MaxPages: 4})
	if err != nil {
		t.Fatal(err)
	}
	rows, err := src.After(context.Background(), at.Add(-time.Hour))
	if err != nil || len(rows) != 2 || rows[0].Judgment.JudgmentID != "j-1" || rows[1].Judgment.Output.Direction != "LONG" || auth != "Bearer read-token" {
		t.Fatal("pages", rows, err, auth)
	}
	if rows[0].Judgment.Output.KeyItemIDs[0] != "item0000000001" || len(rows[0].Items) != 1 || !rows[0].Items[0].ReceivedAt.Equal(at) {
		t.Fatal("items", rows[0])
	}
	// v2.14.1: the read API reports seconds, so the judgment at the cursor
	// comes back; it is skipped as the boundary, not a reason to refuse.
	rows, err = src.After(context.Background(), at)
	if err != nil || len(rows) != 1 || rows[0].Judgment.JudgmentID != "j-2" {
		t.Fatal("boundary", rows, err)
	}
	// A response for another instance is refused.
	instance = "soxl-e20"
	if _, err = src.After(context.Background(), at.Add(-time.Hour)); err == nil {
		t.Fatal("foreign instance accepted")
	}
	if _, err = submission.NewJudgmentSource(submission.JudgmentSourceOptions{BaseURL: server.URL, InstanceID: "soxl-e15", Client: &http.Client{Timeout: time.Second}, MaxResponseBytes: 1, MaxPages: 1}); err == nil {
		t.Fatal("missing token accepted")
	}
}

func TestT330FollowerProfileValidation(t *testing.T) {
	base := func() config.DynamicProfile {
		var p config.DynamicProfile
		p.InstanceID, p.Environment, p.ObjectID, p.TimeoutSeconds, p.MaxBytes = "soxl-e20", "SIM", "soxlusdt-e20", 60, 1<<20
		p.Access.DatabaseURL, p.Access.FrameworkURL, p.Access.FrameworkToken = "postgresql://fixture", "http://127.0.0.1:1", "t"
		p.Access.TradingReadURL, p.Access.TradingReadToken, p.Access.AccountID, p.Access.RunID = "http://127.0.0.1:2", "t", "account-e20", "soxl-e20"
		p.Judgment.ImpactByConfidence = [5]string{"0.22", "0.45", "0.65", "0.85", "1"}
		p.Judgment.MinHalfLifeHours, p.Judgment.MaxHalfLifeHours = 4, 72
		p.Position.CloseMinutes, p.Position.InsuranceFraction, p.Position.WickMultiplier, p.Position.BaselineMaxShare = 5, 0.25, 2, 0.5
		p.Position.DirectiveTTLMinutes, p.Position.NoiseLookbackDays, p.Position.PriceTick, p.Position.LadderStep = 15, 28, 0.01, 0.04
		p.Follow.SourceURL, p.Follow.SourceToken, p.Follow.SourceInstanceID = "http://127.0.0.1:18311", "read", "soxl-e15"
		p.Follow.Since = time.Date(2026, 10, 23, 0, 0, 0, 0, time.UTC)
		return p
	}
	if err := base().Validate(); err != nil {
		t.Fatal("follower needs no models or judge minutes", err)
	}
	for name, change := range map[string]func(*config.DynamicProfile){
		"self":     func(p *config.DynamicProfile) { p.Follow.SourceInstanceID = p.InstanceID },
		"token":    func(p *config.DynamicProfile) { p.Follow.SourceToken = "" },
		"since":    func(p *config.DynamicProfile) { p.Follow.Since = time.Time{} },
		"ladder":   func(p *config.DynamicProfile) { p.Position.LadderStep = 1 },
		"generate": func(p *config.DynamicProfile) { p.Follow.SourceURL = "" },
	} {
		p := base()
		change(&p)
		if p.Validate() == nil {
			t.Fatal("accepted", name)
		}
	}
}
