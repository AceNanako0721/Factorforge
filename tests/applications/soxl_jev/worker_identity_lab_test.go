package soxl_jev_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	d "github.com/AceNanako0721/Factorforge/src/factorforge/applications/soxl_jev/domain"
	"github.com/AceNanako0721/Factorforge/src/factorforge/applications/soxl_jev/submission"
	"github.com/AceNanako0721/Factorforge/src/factorforge/applications/soxl_jev/workers"
	"github.com/AceNanako0721/Factorforge/src/factorforge/strategy/adapters"
	sapi "github.com/AceNanako0721/Factorforge/src/factorforge/strategy/api"
	dto "github.com/AceNanako0721/Factorforge/src/factorforge/strategy/api/dto"
	app "github.com/AceNanako0721/Factorforge/src/factorforge/strategy/application"
	sd "github.com/AceNanako0721/Factorforge/src/factorforge/strategy/domain"
)

func workerIdentityFixture(t *testing.T) (*submission.HTTPClient, *submission.HTTPClient, d.AnalysisRequest, d.AnalysisCandidate, time.Time, string) {
	t.Helper()
	ctx := context.Background()
	state, identity, create := p2Fixture(t)
	_, request, candidate, now := pipelineFixture()
	analysisIdentity := identity
	analysisIdentity.WorkloadID = "fixture-analysis-identity"
	if identity.WorkloadID == analysisIdentity.WorkloadID {
		t.Fatal("fixture identities are not distinct")
	}
	handler, err := sapi.New(sapi.Options{Store: adapters.NewMemory(state), Clock: adapters.NewReplay(now), Internal: true, Tokens: map[string]sd.Identity{"fixture-only-ingest-token": identity, "fixture-only-analysis-token": analysisIdentity}, ReadPolicy: app.ReadPolicy{DefaultLimit: 10, MaxLimit: 50, MaxRecords: 1000, CursorAge: time.Hour, CursorKey: []byte("fixture-only-cursor-secret")}})
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)
	binding := d.Binding{InstanceID: state.InstanceID, Environment: state.Environment}
	client := func(token string) *submission.HTTPClient {
		t.Helper()
		c, e := submission.NewHTTP(submission.HTTPOptions{BaseURL: server.URL, Token: token, Binding: binding, Client: &http.Client{Timeout: time.Second}, MaxResponseBytes: 100000, MaxPages: 10})
		if e != nil {
			t.Fatal(e)
		}
		return c
	}
	ingest, analysis := client("fixture-only-ingest-token"), client("fixture-only-analysis-token")
	object, err := ingest.CreateObject(ctx, create)
	if err != nil {
		t.Fatal(err)
	}
	request.Binding, request.Routing.Binding = binding, binding
	request.ObjectID, request.Routing.ObjectID = object.ObjectID, object.ObjectID
	request.Event.ObjectIDs = []string{object.ObjectID}
	return ingest, analysis, request, candidate, now, server.URL
}

func registerHandoffEvent(t *testing.T, client *submission.HTTPClient, r d.AnalysisRequest) dto.EventCommand {
	t.Helper()
	version, err := client.Version(context.Background(), r.ObjectID)
	if err != nil {
		t.Fatal(err)
	}
	key := "event-" + d.Digest([]any{r.Binding, r.Event.EventID, r.Event.FactVersion, r.Event.Relation, r.Evidence.Raw.ContentHash})
	command := dto.EventCommand{Command: dto.Command{SchemaVersion: "strategy-2.0", RequestID: key, IdempotencyKey: key, ExpectedVersion: version, Reason: "VERIFIED_INSTANCE_EVIDENCE"}, Event: r.Event}
	if _, err = client.RegisterEvent(context.Background(), command, false); err != nil {
		t.Fatal("ingest event", err)
	}
	return command
}

// Reproduce with actual P2 authorization and identity-bound idempotency. No
// external model, account, database, production profile or order is involved.
func TestWorkerIdentityHandoffLab(t *testing.T) {
	if os.Getenv("FACTORFORGE_WORKER_HANDOFF_LAB") != "1" {
		t.Skip("explicit offline experiment only")
	}
	ingest, analysis, r, candidate, now, base := workerIdentityFixture(t)
	command := registerHandoffEvent(t, ingest, r)
	if _, err := ingest.RegisterEvent(context.Background(), command, false); err != nil {
		t.Fatal("same-identity repeat", err)
	}
	_, crossIdentityErr := analysis.RegisterEvent(context.Background(), command, false)
	if fmt.Sprint(crossIdentityErr) != "FRAMEWORK_CONFLICT" {
		t.Fatal("identity-bound idempotency control changed", crossIdentityErr)
	}
	// Inspect the actual P2 code as well as the intentionally closed client error.
	encoded, _ := json.Marshal(command)
	req, _ := http.NewRequest("POST", base+"/api/v2/strategy/events", bytes.NewReader(encoded))
	req.Header.Set("Authorization", "Bearer fixture-only-analysis-token")
	req.Header.Set("Content-Type", "application/json")
	response, err := (&http.Client{Timeout: time.Second}).Do(req)
	if err != nil {
		t.Fatal(err)
	}
	data, err := io.ReadAll(io.LimitReader(response.Body, 100000))
	response.Body.Close()
	var rejection struct {
		Code string `json:"code"`
	}
	if err != nil || response.StatusCode != 409 || json.Unmarshal(data, &rejection) != nil || rejection.Code != "IDEMPOTENCY_CONFLICT" {
		t.Fatal("actual P2 identity conflict missing", err)
	}
	exists, readErr := analysis.EventExists(context.Background(), r.Event.EventID, r.ObjectID, r.Event.FactVersion)
	if readErr != nil || !exists {
		t.Fatal("existing read-only handoff unavailable", readErr)
	}
	store := &pipelineMemory{binding: r.Binding, kind: "SIM", job: &d.PipelineJob{JobID: r.RequestID, Binding: r.Binding, QueueKind: "SIM", State: "QUEUED", Request: r, CreatedAt: now, Deadline: r.Deadline}}
	provider := &fixtureJev{candidate: candidate}
	worker := workers.AnalysisWorker{Store: store, Framework: analysis, Provider: provider, Clock: &pipelineClock{now}, WorkerID: "fixture-analysis-worker", Lease: time.Minute, AllowMock: true, MaxOutboxes: 10}
	processed, workerErr := worker.ProcessOne(context.Background())
	if !processed || workerErr != nil {
		t.Fatal(workerErr)
	}
	report := map[string]any{"fixture_only": true, "reproduction_base": "bb1a8eff34597e53f0ede679ecd7c2c73db9279d", "same_identity_repeat": "OK", "different_identity_register_error": fmt.Sprint(crossIdentityErr), "p2_error_code": rejection.Code, "different_identity_read_exists": exists, "analysis_job_state": store.job.State, "analysis_reason_codes": store.job.ReasonCodes, "mock_provider_calls": provider.calls, "external_calls": 0, "orders": 0}
	root, err := filepath.Abs(filepath.Join("..", "..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(root, "runtime", "worker-handoff-lab-20261009")
	if err = os.Mkdir(dir, 0700); err != nil {
		t.Fatal("fresh evidence directory required", err)
	}
	data, _ = json.MarshalIndent(report, "", "  ")
	if err = os.WriteFile(filepath.Join(dir, "report.json"), data, 0600); err != nil {
		t.Fatal(err)
	}
	t.Logf("same-ID POST from another identity: %v; query exists: %v; worker state: %s, reasons: %v, provider calls: %d", crossIdentityErr, exists, store.job.State, store.job.ReasonCodes, provider.calls)
}
