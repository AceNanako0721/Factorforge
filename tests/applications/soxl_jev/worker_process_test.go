package soxl_jev_test

import (
	"context"
	"encoding/json"
	"fmt"
	pg "github.com/AceNanako0721/Factorforge/src/factorforge/applications/soxl_jev/adapters/postgres"
	a "github.com/AceNanako0721/Factorforge/src/factorforge/applications/soxl_jev/analysis"
	"github.com/AceNanako0721/Factorforge/src/factorforge/applications/soxl_jev/config"
	d "github.com/AceNanako0721/Factorforge/src/factorforge/applications/soxl_jev/domain"
	"github.com/AceNanako0721/Factorforge/src/factorforge/applications/soxl_jev/monitoring"
	"github.com/AceNanako0721/Factorforge/src/factorforge/applications/soxl_jev/operations"
	"github.com/AceNanako0721/Factorforge/src/factorforge/applications/soxl_jev/reports"
	"github.com/AceNanako0721/Factorforge/src/factorforge/applications/soxl_jev/submission"
	"github.com/AceNanako0721/Factorforge/src/factorforge/strategy/adapters"
	sapi "github.com/AceNanako0721/Factorforge/src/factorforge/strategy/api"
	app "github.com/AceNanako0721/Factorforge/src/factorforge/strategy/application"
	sd "github.com/AceNanako0721/Factorforge/src/factorforge/strategy/domain"
	tradingpg "github.com/AceNanako0721/Factorforge/src/factorforge/trading/adapters/postgres"
	dec "github.com/AceNanako0721/Factorforge/src/factorforge/trading/api/dto"
	"github.com/jackc/pgx/v5"
	"github.com/pelletier/go-toml/v2"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestNativeInstanceWorkerProfilesAndProcessWithoutPythonNode(t *testing.T) {
	ctx := context.Background()
	_, r, _, old := pipelineFixture()
	now := time.Now().UTC()
	shift := now.Sub(old)
	public := r.Evidence.Raw.FirstPublicAt.Add(shift)
	r.Evidence.Raw.FirstPublicAt = &public
	r.Evidence.Raw.PublishedAt = &public
	r.Evidence.Raw.ReceivedAt = r.Evidence.Raw.ReceivedAt.Add(shift)
	r.Evidence.CompletedAt = r.Evidence.CompletedAt.Add(shift)
	r.Evidence.Claims[0].FactTime = public
	r.Evidence.Claims[0].VerifiedAt = r.Evidence.Claims[0].VerifiedAt.Add(shift)
	r.Deadline = now.Add(time.Hour)
	r.Event.FirstPublicAt = public
	r.Event.OccurredAt = public
	r.Event.Claims = r.Evidence.Claims
	r.Event.EvidenceRefs[0].FirstPublicAt = public
	r.Event.EvidenceRefs[0].ReceivedAt = r.Evidence.Raw.ReceivedAt
	r.Event.EvidenceRefs[0].AvailableAt = r.Evidence.CompletedAt
	r.Routing.AvailableAt = r.Evidence.CompletedAt
	r.Routing.EvaluatedAt = now
	r.Routing.ManifestHash = d.Digest(r.Evidence)
	state, identity, create := p2Fixture(t)
	r.Binding = d.Binding{InstanceID: state.InstanceID, Environment: state.Environment}
	r.ObjectID = create.Object.ObjectID
	r.Event.ObjectIDs = []string{r.ObjectID}
	r.Routing.Binding = r.Binding
	r.Routing.ObjectID = r.ObjectID
	p2store := adapters.NewMemory(state)
	clock := adapters.NewReplay(now)
	handler, err := sapi.New(sapi.Options{Store: p2store, Clock: clock, Internal: true, Tokens: map[string]sd.Identity{"fixture-only-worker": identity}, ReadPolicy: app.ReadPolicy{DefaultLimit: 10, MaxLimit: 50, MaxRecords: 1000, CursorAge: time.Hour, CursorKey: []byte("fixture-only-cursor-secret")}})
	if err != nil {
		t.Fatal(err)
	}
	framework := httptest.NewServer(handler)
	defer framework.Close()
	client, err := submission.NewHTTP(submission.HTTPOptions{BaseURL: framework.URL, Token: "fixture-only-worker", Binding: r.Binding, Client: &http.Client{Timeout: time.Second}, MaxResponseBytes: 100000, MaxPages: 10})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = client.CreateObject(ctx, create); err != nil {
		t.Fatal(err)
	}
	publicIdentity := sd.PublicPrincipal{PrincipalID: "fixture-report", InstanceID: state.InstanceID, Environment: state.Environment, Scopes: []sd.ApiScope{sd.Query}}
	readHandler, err := sapi.New(sapi.Options{Store: p2store, Clock: clock, Tokens: map[string]sd.Identity{"fixture-report-read": publicIdentity}, ReadPolicy: app.ReadPolicy{DefaultLimit: 10, MaxLimit: 50, MaxRecords: 1000, CursorAge: time.Hour, CursorKey: []byte("fixture-only-cursor-secret")}})
	if err != nil {
		t.Fatal(err)
	}
	publicFramework := httptest.NewServer(readHandler)
	defer publicFramework.Close()
	var providerCalls atomic.Int64
	model := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		providerCalls.Add(1)
		json.NewEncoder(w).Encode(jevResult())
	}))
	defer model.Close()
	server, err := tradingpg.StartTemporary(ctx, t.TempDir(), nativePG(t))
	if err != nil {
		t.Fatal(err)
	}
	defer server.Close()
	if err = pg.InitializePipeline(ctx, server.AdminDSN, r.Binding); err != nil {
		t.Fatal(err)
	}
	admin, err := pgx.Connect(ctx, server.AdminDSN)
	if err != nil {
		t.Fatal(err)
	}
	defer admin.Close(ctx)
	dsns := map[string]string{}
	for _, role := range []string{"INGEST", "RESEARCH", "TRADING"} {
		login := "fixture_process_" + strings.ToLower(role)
		if _, err = admin.Exec(ctx, fmt.Sprintf("CREATE ROLE %s LOGIN PASSWORD 'fixture-only-password'", pgx.Identifier{login}.Sanitize())); err != nil {
			t.Fatal(err)
		}
		if err = pg.GrantPipeline(ctx, server.AdminDSN, r.Binding, login, role); err != nil {
			t.Fatal(err)
		}
		u, _ := url.Parse(server.AdminDSN)
		u.User = url.UserPassword(login, "fixture-only-password")
		dsns[role] = u.String()
	}
	if err = pg.ConfigureQueueBudget(ctx, server.AdminDSN, pg.QueueBudget{Binding: r.Binding, Kind: "SIM", Bucket: "fixture-budget", PolicyRef: "fixture-policy", MaxJobs: 2, MaxConcurrent: 1, ValidFrom: now.Add(-time.Hour), ValidUntil: now.Add(2 * time.Hour)}); err != nil {
		t.Fatal(err)
	}
	ingest, err := pg.OpenPipeline(ctx, dsns["INGEST"], r.Binding, "INGEST", 100000)
	if err != nil {
		t.Fatal(err)
	}
	defer ingest.Close()
	if err = ingest.Record(ctx, r.Evidence, r.Routing); err != nil {
		t.Fatal(err)
	}
	job := d.PipelineJob{JobID: r.RequestID, Binding: r.Binding, QueueKind: "SIM", Request: r, State: "QUEUED", CreatedAt: now, Deadline: r.Deadline, ReasonCodes: []string{}}
	if err = ingest.Enqueue(ctx, job, "fixture-budget"); err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	private := filepath.Join(root, "runtime", "instance-process")
	if err = os.MkdirAll(private, 0700); err != nil {
		t.Fatal(err)
	}
	settings := config.PipelineSettings{Environment: "SIM", InstanceID: r.Binding.InstanceID, ObjectID: r.ObjectID, Stage: "R2", AssetsFile: filepath.Join(private, "assets.json"), FixtureInputFile: filepath.Join(private, "inputs.json"), PollSeconds: 1, TimeoutSeconds: 10, LeaseSeconds: 60, TaskTTLSeconds: 3600, MaxInputBytes: 100000, MaxOutboxes: 10, MaxFrameworkPages: 10, ResearchBucket: "fixture-budget", TradingBucket: "fixture-budget", QuestionSetVersion: r.QuestionSetVersion, PromptVersion: r.PromptVersion, RubricVersion: r.RubricVersion, CalibrationVersion: r.CalibrationVersion, ModelVersion: r.ModelVersion}
	asset := config.PipelineAssets{Version: "fixture-assets", FixtureOnly: true, RoutingPolicy: d.RoutingPolicy{Version: "fixture-policy", Binding: r.Binding, ObjectID: r.ObjectID, CalibrationVersion: r.CalibrationVersion}, Calibration: a.CalibrationMapping{Version: r.CalibrationVersion, RubricVersion: r.RubricVersion, ProducerVersion: "fixture-producer", Impact: []dec.Decimal{number("0"), number("5")}, Relevance: []dec.Decimal{number("0"), number("1")}, Expectation: []dec.Decimal{number("0"), number("1")}, HalfLife: []dec.Decimal{number("60"), number("300")}, Credibility: number("1"), Quality: number("1"), Novelty: number("1"), Prepricing: number("0"), ClaimSupportMinimum: number("0.9"), Verified: true}}
	asset.ReportSchedule = &reports.Schedule{Anchor: now.Add(-time.Hour), PeriodSeconds: 3600, MaxRecords: 100}
	day := now.Truncate(24 * time.Hour)
	asset.Calendar = &operations.Calendar{Version: "fixture-process-calendar", Zone: "UTC", ValidFrom: day.Add(-24 * time.Hour), ValidUntil: day.Add(48 * time.Hour), Sessions: []operations.MarketSession{{Date: day.Add(-24 * time.Hour).Format("2006-01-02"), OpenLocal: "00:00", CloseLocal: "00:01"}, {Date: day.Add(24 * time.Hour).Format("2006-01-02"), OpenLocal: "00:00", CloseLocal: "00:01"}}}
	// Run RSS -> stored seed -> Brave 429 -> Exa URL -> fetched original through
	// the actual executable. Restart must reuse the durable clue cache.
	var searchEndpoint string
	var braveCalls, exaCalls atomic.Int64
	searchServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		switch request.URL.Path {
		case "/feed":
			w.Header().Set("Content-Type", "application/rss+xml")
			fmt.Fprintf(w, "<rss version=\"2.0\"><channel><item><link>%s/seed</link><pubDate>%s</pubDate></item></channel></rss>", searchEndpoint, now.Add(-time.Minute).Format(time.RFC1123Z))
		case "/brave":
			braveCalls.Add(1)
			var stored int
			if e := admin.QueryRow(ctx, "SELECT count(*) FROM instance_pipeline_sim.raw_evidence_manifest WHERE instance_id=$1", r.Binding.InstanceID).Scan(&stored); e != nil || stored < 2 {
				t.Error("independent seed not persisted before search", e, stored)
			}
			w.Header().Set("Retry-After", "60")
			w.WriteHeader(429)
		case "/exa":
			exaCalls.Add(1)
			var envelope struct {
				ID string `json:"id"`
			}
			if json.NewDecoder(request.Body).Decode(&envelope) != nil {
				t.Error("RPC request")
			}
			json.NewEncoder(w).Encode(map[string]any{"jsonrpc": "2.0", "id": envelope.ID, "result": map[string]any{"content": []any{map[string]any{"type": "text", "text": "Title: fixture\nURL: " + searchEndpoint + "/supplement\nText: ignored snippet"}}}})
		default:
			w.Header().Set("Content-Type", "text/plain")
			fmt.Fprint(w, "fixture original "+request.URL.Path)
		}
	}))
	defer searchServer.Close()
	searchEndpoint = searchServer.URL
	asset.Sources = map[string]d.SourceRegistration{"fixture-rss": {SourceID: "fixture-rss", Version: "fixture-source-version", LicenceRef: "fixture-licence", Enabled: true, LicenceVerified: true, AllowAnalysis: true, Environments: []string{"SIM"}, ValidFrom: now.Add(-time.Hour), ValidUntil: now.Add(time.Hour), MaxAge: time.Hour}}
	asset.RSS = []config.RSSRegistration{{FeedURL: searchEndpoint + "/feed", SourceID: "fixture-rss", LicenceRef: "fixture-licence", Hosts: []string{"127.0.0.1"}, MaxItems: 2}}
	asset.SearchPlans = []monitoring.SearchPlan{{Version: "fixture-search-plan", SourceID: "fixture-rss", Query: "fixture approved topic", Count: 2}}
	asset.SearchSourceHosts = map[string]string{"127.0.0.1": "fixture-rss"}
	searchPolicy := searchPolicy(now.Truncate(time.Second), "BRAVE", "EXA")
	searchPolicy.CacheTTLSeconds = 300
	asset.SearchRouting = &searchPolicy
	data, _ := json.Marshal(asset)
	if err = os.WriteFile(settings.AssetsFile, data, 0600); err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(settings.FixtureInputFile, []byte(`{"fixture_only":true,"evidence":[]}`), 0600); err != nil {
		t.Fatal(err)
	}
	questions := []map[string]any{}
	for id, q := range fixturePrompt().Questions {
		questions = append(questions, map[string]any{"id": id, "type": q.Type, "instructions": q.Instructions, "criteria": q.Criteria})
	}
	prompt := filepath.Join(private, "prompt.json")
	data, _ = json.Marshal(map[string]any{"schema_version": 1, "asset_kind": "EXAMPLE_OR_MOCK", "production_ready": false, "prompt_version": r.PromptVersion, "instructions": "", "state_template": "", "questions": questions})
	if err = os.WriteFile(prompt, data, 0600); err != nil {
		t.Fatal(err)
	}
	canonical := filepath.Join(private, "canonical.toml")
	sections := map[string]any{"mode": "mock", "services": map[string]any{"jev_api_url": model.URL + "/v1/systemone", "framework_api_url": publicFramework.URL}, "credentials": map[string]any{"jev_api_key": "fixture-token", "framework_api_token": "fixture-report-read"}, "application": map[string]any{"prompt_file": prompt, "pipeline": map[string]any{"settings": settings, "ingest": config.WorkerAccess{DatabaseURL: dsns["INGEST"], FrameworkURL: framework.URL, FrameworkToken: "fixture-only-worker"}, "trading": config.WorkerAccess{DatabaseURL: dsns["TRADING"], FrameworkURL: framework.URL, FrameworkToken: "fixture-only-worker"}, "research": config.WorkerAccess{DatabaseURL: dsns["RESEARCH"], FrameworkURL: framework.URL, FrameworkToken: "fixture-only-research"}}}}
	sections["application"].(map[string]any)["pipeline"].(map[string]any)["search_backends"] = []config.SearchAccess{{ID: "brave", Kind: "BRAVE", Endpoint: searchEndpoint + "/brave", Token: "fixture-search-token"}, {ID: "exa", Kind: "EXA", Endpoint: searchEndpoint + "/exa"}}
	data, err = toml.Marshal(sections)
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(canonical, data, 0600); err != nil {
		t.Fatal(err)
	}
	paths, err := config.PrepareWorkerProfiles(canonical, filepath.Join(private, "profiles"))
	if err != nil || len(paths) != 3 {
		t.Fatal(err)
	}
	for _, path := range paths {
		profile, err := config.LoadWorker(path, strings.ToUpper(strings.TrimSuffix(filepath.Base(path), ".toml")))
		if err != nil {
			t.Fatal(err)
		}
		bytes, _ := os.ReadFile(path)
		if profile.Role == "RESEARCH" && strings.Contains(string(bytes), "fixture-only-worker") {
			t.Fatal("signal identity copied to research profile")
		}
		if profile.Role == "INGEST" && profile.ProviderToken != "" {
			t.Fatal("model token copied to ingest")
		}
		if profile.Role != "INGEST" && (profile.ReportReadURL != "" || profile.ReportReadToken != "") {
			t.Fatal("report token copied to analysis")
		}
		if profile.Role != "INGEST" && (len(profile.SearchBackends) != 0 || strings.Contains(string(bytes), "fixture-search-token")) {
			t.Fatal("search credentials copied to analysis")
		}
	}
	if _, err = config.LoadWorker(paths[1], "TRADING"); err == nil {
		t.Fatal("role profile rebound")
	}
	ingestBinary := filepath.Join(private, "ingest-worker")
	buildIngest := exec.Command("go", "build", "-o", ingestBinary, "../../../src/factorforge/applications/soxl_jev/entrypoints/ingest-worker")
	if output, e := buildIngest.CombinedOutput(); e != nil {
		t.Fatal(e, string(output))
	}
	for i := 0; i < 2; i++ {
		command := exec.Command(ingestBinary, "--config", paths[0], "--once")
		command.Env = []string{"PATH=/nonexistent", "TZ=UTC"}
		if output, e := command.CombinedOutput(); e != nil {
			t.Fatal("native report iteration", e, string(output))
		}
	}
	var reportCount int
	if braveCalls.Load() != 1 || exaCalls.Load() != 1 {
		t.Fatal("native failover/cache across restart", braveCalls.Load(), exaCalls.Load())
	}
	var searchPayload []byte
	if err = admin.QueryRow(ctx, "SELECT payload FROM instance_pipeline_sim.search_state WHERE instance_id=$1", r.Binding.InstanceID).Scan(&searchPayload); err != nil {
		t.Fatal(err)
	}
	var searchState d.SearchState
	if d.DecodePrivate(searchPayload, &searchState) != nil || searchState.Providers["brave"].UsedRequests != 1 || searchState.Providers["exa"].UsedRequests != 1 || len(searchState.Pending) != 0 {
		t.Fatal("native allowance not durable")
	}
	if searchState.Providers["brave"].LastCode != "SEARCH_RATE_LIMITED" || searchState.Providers["exa"].LastCode != "SEARCH_OK" {
		t.Fatal("native per-provider result lost")
	}
	var originalCount int
	if err = admin.QueryRow(ctx, "SELECT count(*) FROM instance_pipeline_sim.raw_evidence_manifest WHERE instance_id=$1", r.Binding.InstanceID).Scan(&originalCount); err != nil || originalCount != 3 {
		t.Fatal("native seed/supplement persistence", err, originalCount)
	}
	if err = admin.QueryRow(ctx, "SELECT count(*) FROM instance_pipeline_sim.framework_report").Scan(&reportCount); err != nil || reportCount != 1 {
		t.Fatal("native periodic report once", reportCount, err)
	}
	binary := filepath.Join(private, "analysis-worker")
	build := exec.Command("go", "build", "-o", binary, "../../../src/factorforge/applications/soxl_jev/entrypoints/trading-analysis-worker")
	if output, e := build.CombinedOutput(); e != nil {
		t.Fatal(e, string(output))
	}
	for attempt := 0; attempt < 2; attempt++ {
		command := exec.Command(binary, "--config", paths[2], "--once")
		command.Env = []string{"PATH=/nonexistent", "TZ=UTC"}
		output, err := command.CombinedOutput()
		if err != nil {
			t.Fatal("native worker", err, string(output))
		}
	}
	if providerCalls.Load() != 1 {
		t.Fatal("model called again after restart", providerCalls.Load())
	}
	var delivery string
	if err = admin.QueryRow(ctx, "SELECT state FROM instance_pipeline_sim.trading_submission_outbox").Scan(&delivery); err != nil || delivery != "ACK" {
		t.Fatal("framework receipt not durable", delivery, err)
	}
}
