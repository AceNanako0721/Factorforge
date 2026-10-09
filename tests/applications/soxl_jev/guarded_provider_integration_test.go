package soxl_jev_test

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	pg "github.com/AceNanako0721/Factorforge/src/factorforge/applications/soxl_jev/adapters/postgres"
	a "github.com/AceNanako0721/Factorforge/src/factorforge/applications/soxl_jev/analysis"
	d "github.com/AceNanako0721/Factorforge/src/factorforge/applications/soxl_jev/domain"
	"github.com/AceNanako0721/Factorforge/src/factorforge/applications/soxl_jev/submission"
	"github.com/AceNanako0721/Factorforge/src/factorforge/applications/soxl_jev/workers"
	"github.com/AceNanako0721/Factorforge/src/factorforge/strategy/adapters"
	sapi "github.com/AceNanako0721/Factorforge/src/factorforge/strategy/api"
	app "github.com/AceNanako0721/Factorforge/src/factorforge/strategy/application"
	sd "github.com/AceNanako0721/Factorforge/src/factorforge/strategy/domain"
	"github.com/pelletier/go-toml/v2"
)

type guardedLabClock struct{}

func (guardedLabClock) Now() time.Time { return time.Now().UTC() }

// This observer delegates to the real transport without changing the request.
// Its independent one-call ceiling also blocks accidental retries by the lab.
// Only bodies are journaled privately; Authorization and URLs are never saved.
type guardedLabTransport struct {
	base  http.RoundTripper
	lab   string
	calls atomic.Int32
	http  int
	bytes int
	ms    int64
}

func privateLabWrite(dir, name string, value []byte) error {
	f, err := os.OpenFile(filepath.Join(dir, name), os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return err
	}
	_, err = f.Write(value)
	closed := f.Close()
	if err != nil {
		return err
	}
	return closed
}

func (o *guardedLabTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	if o.calls.Add(1) != 1 {
		return nil, errors.New("LAB_REPEAT_BLOCKED")
	}
	raw, err := io.ReadAll(io.LimitReader(r.Body, 20001))
	r.Body.Close()
	if err != nil || len(raw) > 20000 {
		return nil, errors.New("LAB_REQUEST_BUDGET")
	}
	o.bytes = len(raw)
	if o.lab != "" && privateLabWrite(o.lab, "request.json", raw) != nil {
		return nil, errors.New("LAB_REQUEST_JOURNAL")
	}
	r.Body = io.NopCloser(bytes.NewReader(raw))
	started := time.Now()
	response, err := o.base.RoundTrip(r)
	if err != nil {
		return nil, errors.New("LAB_DELIVERY_UNKNOWN")
	}
	o.http = response.StatusCode
	body, err := io.ReadAll(io.LimitReader(response.Body, 65537))
	response.Body.Close()
	o.ms = time.Since(started).Milliseconds()
	if err != nil || len(body) > 65536 {
		return nil, errors.New("LAB_RESPONSE_BUDGET")
	}
	if o.lab != "" && privateLabWrite(o.lab, "response.json", body) != nil {
		return nil, errors.New("LAB_RESPONSE_JOURNAL")
	}
	response.Body = io.NopCloser(bytes.NewReader(body))
	return response, nil
}

func TestGuardedJevQueueActualP2ResearchAndPostgresReopen(t *testing.T) {
	runGuardedProviderIntegration(t, "")
}

// Explicit opt-in only: one synthetic request, no retry, no trading listener,
// no production configuration changes. A persistent marker prevents reruns.
func TestGuardedJevRealAccountIntegrationLab(t *testing.T) {
	lab := os.Getenv("FACTORFORGE_GUARDED_JEV_LAB")
	if lab == "" {
		t.Skip("explicit one-call private supplier integration")
	}
	runGuardedProviderIntegration(t, lab)
}

func runGuardedProviderIntegration(t *testing.T, lab string) {
	t.Helper()
	ctx := context.Background()
	_, request, _, fixtureNow := pipelineFixture()
	prompt, token, model := fixturePrompt(), "fixture-only-token", "fixture-model"
	if lab != "" {
		var plan struct {
			Kind             string `json:"kind"`
			Model            string `json:"model"`
			PromptFile       string `json:"prompt_file"`
			PromptVersion    string `json:"prompt_version"`
			MaxRequests      int    `json:"max_requests"`
			TimeoutSeconds   int    `json:"timeout_seconds"`
			MaxRequestBytes  int    `json:"max_request_bytes"`
			MaxResponseBytes int    `json:"max_response_bytes"`
		}
		info, err := os.Lstat(lab)
		raw, readErr := os.ReadFile(filepath.Join(lab, "plan.json"))
		if !filepath.IsAbs(lab) || filepath.Base(filepath.Dir(lab)) != "runtime" || err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 || info.Mode().Perm() != 0700 || readErr != nil || len(raw) > 4096 || d.DecodePrivate(raw, &plan) != nil || plan.Kind != "SYNTHETIC_RESEARCH_ONLY" || plan.Model != "jev-1.13.0" || plan.MaxRequests != 1 || plan.TimeoutSeconds != 20 || plan.MaxRequestBytes != 20000 || plan.MaxResponseBytes != 65536 || filepath.Base(plan.PromptFile) != plan.PromptFile || !d.ValidID(plan.PromptVersion) {
			t.Fatal("GUARDED_LAB_PLAN_INVALID")
		}
		// A laboratory PromptAsset deliberately has no production-ready label.
		// It is never installed or passed through production asset provisioning.
		path := filepath.Join(lab, plan.PromptFile)
		info, err = os.Lstat(path)
		raw, readErr = os.ReadFile(path)
		if err != nil || !info.Mode().IsRegular() || info.Mode().Perm() != 0600 || readErr != nil || len(raw) > 65536 || d.DecodePrivate(raw, &prompt) != nil || prompt.Version != plan.PromptVersion || len(prompt.Questions) != 7 {
			t.Fatal("GUARDED_LAB_PROMPT_INVALID")
		}
		var canonical struct {
			Credentials struct {
				Key string `toml:"jev_api_key"`
			} `toml:"credentials"`
		}
		raw, err = os.ReadFile(filepath.Join(lab, "..", "..", "config", "config.toml"))
		if err != nil || toml.Unmarshal(raw, &canonical) != nil || canonical.Credentials.Key == "" {
			t.Fatal("GUARDED_LAB_CREDENTIAL_UNAVAILABLE")
		}
		token, model = canonical.Credentials.Key, plan.Model
	}
	policy := providerPolicy()
	policy.MaxCalls, policy.MaxConcurrent, policy.MaxRequestBytes = 1, 1, 20000
	policy.Partitions = map[string]d.ProviderPartition{"RESEARCH": {MaxCalls: 1, MaxConcurrent: 1}, "SIM": {}, "LIVE": {}}
	f := newProviderFixture(t, policy)
	binding := f.grants["research_a"].Binding
	if err := pg.InitializePipeline(ctx, f.dsn, binding); err != nil {
		t.Fatal("GUARDED_LAB_PIPELINE_INIT")
	}
	login := "fixture_guarded_ingest"
	if _, err := f.admin.Exec(ctx, "CREATE ROLE "+login+" LOGIN PASSWORD 'fixture-only-password'"); err != nil {
		t.Fatal("GUARDED_LAB_INGEST_ROLE")
	}
	for role, kind := range map[string]string{login: "INGEST", f.grants["research_a"].Login: "RESEARCH"} {
		if err := pg.GrantPipeline(ctx, f.dsn, binding, role, kind); err != nil {
			t.Fatal("GUARDED_LAB_PIPELINE_GRANT")
		}
	}
	u, _ := url.Parse(f.dsn)
	u.User = url.UserPassword(login, "fixture-only-password")
	ingest, err := pg.OpenPipeline(ctx, u.String(), binding, "INGEST", 200000)
	if err != nil {
		t.Fatal("GUARDED_LAB_INGEST_OPEN")
	}
	defer ingest.Close()
	open := func() *pg.PipelineStore {
		t.Helper()
		s, err := pg.OpenPipeline(ctx, f.dsns["research_a"], binding, "RESEARCH", 200000)
		if err != nil {
			t.Fatal("GUARDED_LAB_RESEARCH_OPEN")
		}
		t.Cleanup(s.Close)
		return s
	}
	store := open()
	state, identity, create := p2Fixture(t)
	state.InstanceID, identity.InstanceID, create.Object.InstanceID = binding.InstanceID, binding.InstanceID, binding.InstanceID
	frameworkState, clock := adapters.NewMemory(state), adapters.NewReplay(fixtureNow)
	readPolicy := app.ReadPolicy{DefaultLimit: 10, MaxLimit: 50, MaxRecords: 1000, CursorAge: time.Hour, CursorKey: []byte("fixture-only-cursor-secret")}
	internal, err := sapi.New(sapi.Options{Store: frameworkState, Clock: clock, Internal: true, Tokens: map[string]sd.Identity{"fixture-only-ingest": identity}, ReadPolicy: readPolicy})
	if err != nil {
		t.Fatal("GUARDED_LAB_P2_INTERNAL")
	}
	public := sd.PublicPrincipal{PrincipalID: "fixture-guarded-research", InstanceID: binding.InstanceID, Environment: binding.Environment, Scopes: []sd.ApiScope{sd.Query, sd.Research}}
	external, err := sapi.New(sapi.Options{Store: frameworkState, Clock: clock, Tokens: map[string]sd.Identity{"fixture-only-research": public}, ReadPolicy: readPolicy})
	if err != nil {
		t.Fatal("GUARDED_LAB_P2_PUBLIC")
	}
	internalServer, publicServer := httptest.NewServer(internal), httptest.NewServer(external)
	defer internalServer.Close()
	defer publicServer.Close()
	client := func(base, token string, research bool) *submission.HTTPClient {
		t.Helper()
		c, err := submission.NewHTTP(submission.HTTPOptions{BaseURL: base, Token: token, Binding: binding, ResearchOnly: research, Client: &http.Client{Timeout: time.Second}, MaxResponseBytes: 200000, MaxPages: 10})
		if err != nil {
			t.Fatal("GUARDED_LAB_P2_CLIENT")
		}
		return c
	}
	owner, research := client(internalServer.URL, "fixture-only-ingest", false), client(publicServer.URL, "fixture-only-research", true)
	object, err := owner.CreateObject(ctx, create)
	if err != nil {
		t.Fatal("GUARDED_LAB_P2_OBJECT")
	}
	request.Binding, request.Routing.Binding = binding, binding
	request.ObjectID, request.Routing.ObjectID, request.Event.ObjectIDs = object.ObjectID, object.ObjectID, []string{object.ObjectID}
	request.Event.EvidenceRefs[0].AvailableAt = request.Evidence.Raw.ReceivedAt
	request.ModelVersion, request.PromptVersion = model, prompt.Version
	request.Deadline = time.Now().UTC().Add(time.Minute)
	registered := registerHandoffEvent(t, owner, request)
	now := time.Now().UTC()
	if err := pg.ConfigureQueueBudget(ctx, f.dsn, pg.QueueBudget{Binding: binding, Kind: "RESEARCH", Bucket: "fixture-guarded-budget", PolicyRef: "fixture-guarded-policy", MaxJobs: 1, MaxConcurrent: 1, ValidFrom: now.Add(-time.Minute), ValidUntil: now.Add(time.Hour)}); err != nil {
		t.Fatal("GUARDED_LAB_QUEUE_BUDGET")
	}
	if err := ingest.Record(ctx, request.Evidence, request.Routing); err != nil {
		t.Fatal("GUARDED_LAB_RECORD")
	}
	job := d.PipelineJob{JobID: request.RequestID, Binding: binding, QueueKind: "RESEARCH", Request: request, State: "QUEUED", CreatedAt: now, Deadline: request.Deadline, ReasonCodes: []string{}}
	if err := ingest.Enqueue(ctx, job, "fixture-guarded-budget"); err != nil {
		t.Fatal("GUARDED_LAB_ENQUEUE")
	}
	transport := &http.Transport{TLSClientConfig: &tls.Config{MinVersion: tls.VersionTLS12}, MaxConnsPerHost: 1}
	defer transport.CloseIdleConnections()
	observer := &guardedLabTransport{base: transport, lab: lab}
	options := controlledJevOptions("", request, now)
	options.Prompt, options.ModelVersion, options.Token, options.Admission = prompt, model, token, f.controls["research_a"]
	options.Client, options.Clock = &http.Client{Timeout: 20 * time.Second, Transport: observer}, guardedLabClock{}.Now
	options.MaxRequestBytes, options.MaxResponseBytes = 20000, 65536
	options.Endpoint, options.FixtureOnly = "https://api.typesafe.ai/v1/systemone", lab == ""
	if lab == "" {
		fixture := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { json.NewEncoder(w).Encode(jevResult()) }))
		defer fixture.Close()
		options.Endpoint = fixture.URL + "/v1/systemone"
	}
	provider, err := a.NewJev(options)
	if err != nil {
		t.Fatal("GUARDED_LAB_PROVIDER_CONSTRUCTION", providerError(err))
	}
	worker := workers.AnalysisWorker{Store: store, Framework: research, Provider: provider, Clock: guardedLabClock{}, WorkerID: "fixture-guarded-worker", Lease: time.Minute, AllowMock: lab == "", MaxOutboxes: 2}
	readJob := func() d.PipelineJob {
		t.Helper()
		var raw []byte
		var saved d.PipelineJob
		if f.admin.QueryRow(ctx, "SELECT payload FROM instance_pipeline_sim.app_research_analysis_job WHERE job_id=$1", job.JobID).Scan(&raw) != nil || d.DecodePrivate(raw, &saved) != nil {
			t.Fatal("GUARDED_LAB_JOB_READ")
		}
		return saved
	}
	reopened, receiptState := false, ""
	if lab != "" {
		marker, _ := json.Marshal(map[string]any{"started_at": now, "max_requests": 1, "retries": 0, "kind": "SYNTHETIC_RESEARCH_ONLY"})
		if privateLabWrite(lab, "started.json", marker) != nil {
			t.Fatal("GUARDED_LAB_ALREADY_STARTED_NO_RETRY")
		}
		defer func() {
			saved := readJob()
			var calls int
			var outcome string
			f.admin.QueryRow(ctx, "SELECT count(*) FROM instance_provider_control.call").Scan(&calls)
			f.admin.QueryRow(ctx, "SELECT state FROM instance_provider_control.call LIMIT 1").Scan(&outcome)
			report := map[string]any{"captured_at": time.Now().UTC(), "synthetic_research_only": true, "http_status": observer.http, "http_calls": observer.calls.Load(), "request_bytes": observer.bytes, "http_milliseconds": observer.ms, "pool_calls": calls, "pool_outcome": outcome, "job_state": saved.State, "job_reasons": saved.ReasonCodes, "candidate": saved.Candidate, "reopened": reopened, "receipt_state": receiptState, "test_passed": !t.Failed(), "retries": 0, "orders": 0, "production_assets_installed": false, "independent_blinded_holdout": false, "billed_cost": nil, "live_capacity_proven": false}
			encoded, err := json.MarshalIndent(report, "", "  ")
			if err != nil || privateLabWrite(lab, "report.json", encoded) != nil {
				t.Error("GUARDED_LAB_REPORT_WRITE")
			}
		}()
	}
	if processed, err := worker.ProcessOne(ctx); err != nil || !processed {
		t.Fatal("GUARDED_LAB_PROCESS", providerError(err))
	}
	saved := readJob()
	if saved.State != "COMPLETED" || saved.Candidate == nil || saved.Candidate.Mock != (lab == "") || a.Validate(request, *saved.Candidate, time.Now().UTC(), lab == "") != nil || observer.calls.Load() != 1 {
		t.Fatal("GUARDED_LAB_CANDIDATE_NOT_ADMITTED", saved.State, saved.ReasonCodes)
	}
	var originalOutbox []byte
	var originalCommand d.SubmissionOutbox
	if f.admin.QueryRow(ctx, "SELECT payload FROM instance_pipeline_sim.research_submission_outbox WHERE job_id=$1", job.JobID).Scan(&originalOutbox) != nil || d.DecodePrivate(originalOutbox, &originalCommand) != nil {
		t.Fatal("GUARDED_LAB_OUTBOX_READ")
	}
	if lab == "" {
		// Ordinary CI also advances P2 after the outbox was frozen. The rejected
		// command changes only audit/version, never a business fact or score.
		registered.RequestID, registered.IdempotencyKey, registered.ExpectedVersion = "fixture-guarded-version-audit", "fixture-guarded-version-audit", 0
		if _, err := owner.RegisterEvent(ctx, registered, false); providerError(err) != "FRAMEWORK_AGGREGATE_VERSION_CONFLICT" {
			t.Fatal("GUARDED_LAB_VERSION_ADVANCE")
		}
	}
	// Reopen both durable boundaries before dispatch; recovery may read/submit
	// the recorded candidate but must not issue another supplier request.
	store.Close()
	f.controls["research_a"].Close()
	options.Admission, err = pg.OpenProviderControl(ctx, f.dsns["research_a"], binding, "RESEARCH")
	if err != nil {
		t.Fatal("GUARDED_LAB_CONTROL_REOPEN")
	}
	defer options.Admission.(*pg.ProviderControl).Close()
	worker.Store = open()
	worker.Provider, err = a.NewJev(options)
	if err != nil {
		t.Fatal("GUARDED_LAB_PROVIDER_REOPEN")
	}
	if processed, err := worker.ProcessOne(ctx); err != nil || processed {
		t.Fatal("GUARDED_LAB_COMPLETED_RECLAIMED")
	}
	if d.Digest(readJob().Candidate) != d.Digest(saved.Candidate) {
		t.Fatal("GUARDED_LAB_CANDIDATE_CHANGED_AFTER_REOPEN")
	}
	reopened = true
	if clock.Advance(time.Now().UTC()) != nil || worker.Dispatch(ctx) != nil || worker.Dispatch(ctx) != nil {
		t.Fatal("GUARDED_LAB_DISPATCH")
	}
	var raw []byte
	var out d.SubmissionOutbox
	if f.admin.QueryRow(ctx, "SELECT payload FROM instance_pipeline_sim.research_submission_outbox WHERE job_id=$1", job.JobID).Scan(&raw) != nil || d.DecodePrivate(raw, &out) != nil || out.DeliveryState != "ACK" || out.Receipt == nil || out.Receipt.State != "RESEARCH_ONLY" {
		t.Fatal("GUARDED_LAB_RESEARCH_RECEIPT")
	}
	receiptState = out.Receipt.State
	if d.Digest(out.Command) != d.Digest(originalCommand.Command) || out.CandidateHash != originalCommand.CandidateHash {
		t.Fatal("GUARDED_LAB_IMMUTABLE_OUTBOX_CHANGED")
	}
	// The identical request is independently blocked by durable account control.
	if _, err := worker.Provider.Analyze(ctx, request); providerError(err) != "PROVIDER_CALL_ALREADY_RESERVED" || observer.calls.Load() != 1 {
		t.Fatal("GUARDED_LAB_DUPLICATE_HTTP", providerError(err))
	}
	final, err := frameworkState.Read(ctx, binding.InstanceID)
	if err != nil || final.Contributions.Len() != 0 || final.Outbox.Len() != 0 || final.ResearchReceipts.Len() != 1 {
		t.Fatal("GUARDED_LAB_RESEARCH_ESCALATED")
	}
	var poolCalls, queueUsed int
	var poolState string
	if f.admin.QueryRow(ctx, "SELECT count(*),min(state) FROM instance_provider_control.call").Scan(&poolCalls, &poolState) != nil || f.admin.QueryRow(ctx, "SELECT used_jobs FROM instance_pipeline_sim.budget WHERE queue_kind='RESEARCH'").Scan(&queueUsed) != nil || poolCalls != 1 || poolState != "COMPLETE" || queueUsed != 1 {
		t.Fatal("GUARDED_LAB_DURABLE_ACCOUNTING")
	}
	t.Logf("http_calls=1; pool_calls=1; queue_debits=1; job=COMPLETED; receipt=RESEARCH_ONLY; reopened=true; orders=0")
}
