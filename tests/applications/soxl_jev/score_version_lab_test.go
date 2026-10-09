package soxl_jev_test

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	d "github.com/AceNanako0721/Factorforge/src/factorforge/applications/soxl_jev/domain"
	"github.com/AceNanako0721/Factorforge/src/factorforge/applications/soxl_jev/ports"
	"github.com/AceNanako0721/Factorforge/src/factorforge/applications/soxl_jev/workers"
	dto "github.com/AceNanako0721/Factorforge/src/factorforge/strategy/api/dto"
)

// Research prototype only: refresh transport CAS metadata, preserving every
// immutable score field, identity and idempotency key. Not a production retry.
type freshScoreVersionPrototype struct{ ports.FrameworkClient }

func (p freshScoreVersionPrototype) Submit(ctx context.Context, c dto.ScoreCommand) (dto.AdmissionReceipt, error) {
	version, err := p.Version(ctx, c.Score.ObjectID)
	if err != nil {
		return dto.AdmissionReceipt{}, err
	}
	c.ExpectedVersion = version
	return p.FrameworkClient.Submit(ctx, c)
}

type scoreVersionOutcome struct {
	States, Receipts               []string
	PersistedVersions              []int
	ProviderCalls                  int
	SecondP2Conflict               string
	ImmutableCommandsAndCandidates bool
}

func scoreVersionCase(t *testing.T, prototype bool) scoreVersionOutcome {
	t.Helper()
	ctx := context.Background()
	ingest, analysis, first, candidate, now, base := workerIdentityFixture(t)
	var second d.AnalysisRequest
	raw, _ := json.Marshal(first)
	if d.DecodePrivate(raw, &second) != nil {
		t.Fatal("clone synthetic request")
	}
	second.RequestID += "-two"
	second.Event.EventID += "-two"
	second.Event.FamilyID += "-two"
	second.Evidence.Raw.EvidenceID += "-two"
	second.Evidence.Raw.Content = strings.ReplaceAll(second.Evidence.Raw.Content, "12", "13")
	second.Evidence.Raw.ContentHash = d.ContentDigest([]byte(second.Evidence.Raw.Content))
	second.Evidence.Claims[0].ClaimID += "-two"
	second.Evidence.Claims[0].NormalizedFact = second.Evidence.Raw.Content
	second.Evidence.Claims[0].NumbersWithUnits["USD"] = "13"
	second.Evidence.Claims[0].EvidenceRefs = []string{second.Evidence.Raw.EvidenceID}
	second.Evidence.Spans[0].SpanID += "-two"
	second.Evidence.Spans[0].TextHash = second.Evidence.Raw.ContentHash
	second.Event.Claims = second.Evidence.Claims
	second.Event.EvidenceRefs[0].EvidenceID = second.Evidence.Raw.EvidenceID
	second.Event.EvidenceRefs[0].ContentHash = second.Evidence.Raw.ContentHash
	second.Event.EvidenceRefs[0].SpanRefs = []string{second.Evidence.Spans[0].SpanID}
	second.Routing.ManifestHash = d.Digest(second.Evidence)
	registerHandoffEvent(t, ingest, first)
	registerHandoffEvent(t, ingest, second)
	var framework ports.FrameworkClient = analysis
	if prototype {
		framework = freshScoreVersionPrototype{framework}
	}
	stores := []*pipelineMemory{}
	workerSet := []workers.AnalysisWorker{}
	providerSet := []*fixtureJev{}
	for _, r := range []d.AnalysisRequest{first, second} {
		c := candidate
		c.RequestID = r.RequestID
		c.ManifestHash = r.Routing.ManifestHash
		c.SupportedClaims = map[string]bool{r.Evidence.Claims[0].ClaimID: true}
		store := &pipelineMemory{binding: r.Binding, kind: "SIM", job: &d.PipelineJob{JobID: r.RequestID, Binding: r.Binding, QueueKind: "SIM", State: "QUEUED", Request: r, CreatedAt: now, Deadline: r.Deadline}}
		provider := &fixtureJev{candidate: c}
		worker := workers.AnalysisWorker{Store: store, Framework: framework, Provider: provider, Clock: &pipelineClock{now}, WorkerID: "fixture-score-version", Lease: time.Minute, AllowMock: true, MaxOutboxes: 10}
		if processed, err := worker.ProcessOne(ctx); err != nil || !processed || store.outbox == nil {
			t.Fatal("prepare independent immutable score", err)
		}
		stores, workerSet, providerSet = append(stores, store), append(workerSet, worker), append(providerSet, provider)
	}
	commands, candidates := []string{}, []string{}
	result := scoreVersionOutcome{ImmutableCommandsAndCandidates: true}
	for _, s := range stores {
		commands = append(commands, d.Digest(s.outbox.Command))
		candidates = append(candidates, d.Digest(s.job.Candidate))
		result.PersistedVersions = append(result.PersistedVersions, s.outbox.Command.ExpectedVersion)
	}
	if result.PersistedVersions[0] != result.PersistedVersions[1] {
		t.Fatal("fixture requires two scores prepared before either dispatch")
	}
	for _, worker := range workerSet {
		if err := worker.Dispatch(ctx); err != nil {
			t.Fatal("dispatch", err)
		}
	}
	for i, s := range stores {
		result.States = append(result.States, s.outbox.DeliveryState)
		if s.outbox.Receipt != nil {
			result.Receipts = append(result.Receipts, s.outbox.Receipt.State)
		}
		result.ProviderCalls += providerSet[i].calls
		result.ImmutableCommandsAndCandidates = result.ImmutableCommandsAndCandidates && commands[i] == d.Digest(s.outbox.Command) && candidates[i] == d.Digest(s.job.Candidate)
	}
	if stores[1].outbox.DeliveryState == "REJECTED" {
		// Inspect only the allowlisted P2 reason; the public client intentionally
		// collapses other conflicts. Failed CAS has no score receipt; P2 may
		// separately append rejection audit and advance the aggregate version.
		body, _ := json.Marshal(stores[1].outbox.Command)
		req, _ := http.NewRequest("POST", base+"/api/v2/strategy/events/"+second.Event.EventID+"/scores", bytes.NewReader(body))
		req.Header.Set("Authorization", "Bearer fixture-only-analysis-token")
		req.Header.Set("Content-Type", "application/json")
		response, err := (&http.Client{Timeout: time.Second}).Do(req)
		if err != nil {
			t.Fatal("read conflict reason")
		}
		data, err := io.ReadAll(io.LimitReader(response.Body, 10000))
		response.Body.Close()
		var reply struct {
			Code string `json:"code"`
		}
		if err != nil || response.StatusCode != 409 || json.Unmarshal(data, &reply) != nil || reply.Code != "AGGREGATE_VERSION_CONFLICT" {
			t.Fatal("expected isolated aggregate version rejection")
		}
		result.SecondP2Conflict = reply.Code
	}
	return result
}

func TestScoreVersionIdempotencyBoundaryLab(t *testing.T) {
	if os.Getenv("FACTORFORGE_SCORE_VERSION_LAB") != "1" {
		t.Skip("explicit offline version experiment")
	}
	ctx := context.Background()
	ingest, analysis, r, candidate, now, _ := workerIdentityFixture(t)
	registerHandoffEvent(t, ingest, r)
	store := &pipelineMemory{binding: r.Binding, kind: "SIM", job: &d.PipelineJob{JobID: r.RequestID, Binding: r.Binding, QueueKind: "SIM", State: "QUEUED", Request: r, CreatedAt: now, Deadline: r.Deadline}}
	provider := &fixtureJev{candidate: candidate}
	worker := workers.AnalysisWorker{Store: store, Framework: analysis, Provider: provider, Clock: &pipelineClock{now}, WorkerID: "fixture-version-boundary", Lease: time.Minute, AllowMock: true, MaxOutboxes: 2}
	if _, err := worker.ProcessOne(ctx); err != nil || store.outbox == nil {
		t.Fatal("prepare boundary score")
	}
	command := store.outbox.Command
	first, err := analysis.Submit(ctx, command)
	if err != nil {
		t.Fatal("first score")
	}
	version, err := analysis.Version(ctx, r.ObjectID)
	if err != nil || version <= command.ExpectedVersion {
		t.Fatal("framework version not advanced")
	}
	command.ExpectedVersion = version
	repeated, err := analysis.Submit(ctx, command)
	if err != nil || d.Digest(first) != d.Digest(repeated) {
		t.Fatal("transport version changed accepted idempotency", err)
	}
	changed := command
	changed.Score.Vector.Direction = -changed.Score.Vector.Direction
	if _, err := analysis.Submit(ctx, changed); providerError(err) != "FRAMEWORK_CONFLICT" {
		t.Fatal("different payload reused score key")
	}
	if _, err := ingest.Submit(ctx, command); providerError(err) != "FRAMEWORK_CONFLICT" {
		t.Fatal("another identity reused score key")
	}
	root, _ := filepath.Abs(filepath.Join("..", "..", ".."))
	dir := filepath.Join(root, "runtime", "score-idempotency-lab-20261009")
	if os.Mkdir(dir, 0700) != nil {
		t.Fatal("fresh boundary evidence required")
	}
	report, _ := json.MarshalIndent(map[string]any{"fixture_only": true, "same_identity_key_payload_new_cas": "SAME_RECEIPT", "changed_payload": "CONFLICT", "changed_identity": "CONFLICT", "mock_calls": provider.calls, "external_calls": 0, "orders": 0}, "", "  ")
	if privateLabWrite(dir, "report.json", report) != nil {
		t.Fatal("boundary report unavailable")
	}
	t.Log("same identity/key/payload and new CAS retains receipt; changed payload/identity rejected")
}

func TestConcurrentPreparedScoreVersionLab(t *testing.T) {
	if os.Getenv("FACTORFORGE_SCORE_VERSION_LAB") != "1" {
		t.Skip("explicit offline version experiment")
	}
	current, prototype := scoreVersionCase(t, false), scoreVersionCase(t, true)
	if !prototype.ImmutableCommandsAndCandidates || len(prototype.States) != 2 || prototype.States[0] != "ACK" || prototype.States[1] != "ACK" || prototype.ProviderCalls != 2 {
		t.Fatal("prototype changed immutable facts or repeated model")
	}
	root, _ := filepath.Abs(filepath.Join("..", "..", ".."))
	dir := filepath.Join(root, "runtime", "score-version-lab-20261009")
	if os.Mkdir(dir, 0700) != nil {
		t.Fatal("fresh evidence directory required")
	}
	encoded, _ := json.MarshalIndent(map[string]any{"base": "add63dc077c90b7c0e113b0c30b89b36aadc1964", "fixture_only": true, "current": current, "prototype": prototype, "external_calls": 0, "orders": 0}, "", "  ")
	if privateLabWrite(dir, "report.json", encoded) != nil {
		t.Fatal("version experiment report unavailable")
	}
	t.Logf("current=%v; P2=%s; prototype=%v; immutable=%t; mock_calls=%d", current.States, current.SecondP2Conflict, prototype.States, prototype.ImmutableCommandsAndCandidates, prototype.ProviderCalls)
}
