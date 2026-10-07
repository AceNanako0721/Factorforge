package soxl_jev_test

import (
	"bytes"
	"context"
	"encoding/json"
	d "github.com/AceNanako0721/Factorforge/src/factorforge/applications/soxl_jev/domain"
	"github.com/AceNanako0721/Factorforge/src/factorforge/applications/soxl_jev/submission"
	"github.com/AceNanako0721/Factorforge/src/factorforge/strategy/adapters"
	sapi "github.com/AceNanako0721/Factorforge/src/factorforge/strategy/api"
	dto "github.com/AceNanako0721/Factorforge/src/factorforge/strategy/api/dto"
	app "github.com/AceNanako0721/Factorforge/src/factorforge/strategy/application"
	sd "github.com/AceNanako0721/Factorforge/src/factorforge/strategy/domain"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"
)

// Reuse the frozen P2 replay provenance rather than inventing a production
// registry or a second policy fixture with different business rules.
func p2Fixture(t *testing.T) (*sd.StrategyState, sd.WorkloadIdentity, dto.CreateObject) {
	t.Helper()
	raw, err := os.ReadFile("../../strategy/fixtures/go_replay.json")
	if err != nil {
		t.Fatal(err)
	}
	var root map[string]any
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	if decoder.Decode(&root) != nil || root["fixture_only"] != true || root["source_commit"] != "e97c9e9a380d105a7e96933bdd325e1d9af06d93" {
		t.Fatal("fixture provenance")
	}
	nodes := root["nodes"].(map[string]any)
	var expand func(any) any
	expand = func(v any) any {
		switch x := v.(type) {
		case map[string]any:
			if ref, ok := x["$fixture_ref"].(string); ok {
				return expand(nodes[ref])
			}
			out := map[string]any{}
			for k, y := range x {
				out[k] = expand(y)
			}
			return out
		case []any:
			out := []any{}
			for _, y := range x {
				out = append(out, expand(y))
			}
			return out
		default:
			return v
		}
	}
	decode := func(v any, target any) {
		data, _ := json.Marshal(v)
		if err := sd.DecodeJSON(data, target); err != nil {
			t.Fatal(err)
		}
	}
	for _, entry := range root["cases"].([]any) {
		item := expand(entry).(map[string]any)
		if item["kind"] != "create" {
			continue
		}
		var state sd.StrategyState
		decode(expand(root["states"].(map[string]any)[item["before"].(string)]), &state)
		input := item["input"].(map[string]any)
		var identity sd.WorkloadIdentity
		decode(input["identity"], &identity)
		var request dto.CreateObject
		decode(input["command"], &request.Command)
		decode(input["obj"], &request.Object)
		decode(input["policy"], &request.Policy)
		decode(input["params"], &request.Parameters)
		return &state, identity, request
	}
	t.Fatal("missing create fixture")
	return nil, sd.WorkloadIdentity{}, dto.CreateObject{}
}
func TestP3HTTPToActualGoP2CreationScoresRevisionsRetractionAndScope(t *testing.T) {
	ctx := context.Background()
	state, identity, create := p2Fixture(t)
	_, r, c, now := pipelineFixture()
	store := adapters.NewMemory(state)
	clock := adapters.NewReplay(now)
	readPolicy := app.ReadPolicy{DefaultLimit: 10, MaxLimit: 50, MaxRecords: 1000, CursorAge: time.Hour, CursorKey: []byte("fixture-only-cursor-secret")}
	handler, err := sapi.New(sapi.Options{Store: store, Clock: clock, Internal: true, Tokens: map[string]sd.Identity{"fixture-only-worker": identity}, ReadPolicy: readPolicy})
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(handler)
	defer server.Close()
	binding := d.Binding{InstanceID: state.InstanceID, Environment: state.Environment}
	client, err := submission.NewHTTP(submission.HTTPOptions{BaseURL: server.URL, Token: "fixture-only-worker", Binding: binding, Client: &http.Client{Timeout: time.Second}, MaxResponseBytes: 100000, MaxPages: 10})
	if err != nil {
		t.Fatal(err)
	}
	object, err := client.CreateObject(ctx, create)
	if err != nil {
		t.Fatal("create object", err)
	}
	r.Binding = binding
	r.ObjectID = object.ObjectID
	r.Event.ObjectIDs = []string{object.ObjectID}
	version, err := client.Version(ctx, object.ObjectID)
	if err != nil {
		t.Fatal(err)
	}
	command := dto.EventCommand{Command: dto.Command{SchemaVersion: "strategy-2.0", RequestID: "fixture-create-event", IdempotencyKey: "fixture-create-event", ExpectedVersion: version, Reason: "FIXTURE_ONLY"}, Event: r.Event}
	_, err = client.RegisterEvent(ctx, command, false)
	if err != nil {
		t.Fatal("event", err)
	}
	if exists, err := client.EventExists(ctx, r.Event.EventID, object.ObjectID, 1); err != nil || !exists {
		t.Fatal("query saved event", err)
	}
	version, _ = client.Version(ctx, object.ObjectID)
	score := dto.ScoreCommand{Command: dto.Command{SchemaVersion: "strategy-2.0", RequestID: "fixture-score-one", IdempotencyKey: "fixture-score-one", ExpectedVersion: version, Reason: "FIXTURE_ONLY"}, Score: dto.ScoreSubmission{SubmissionID: "fixture-score-one", EventID: r.Event.EventID, FactVersion: 1, ObjectID: object.ObjectID, ScoreVersion: 1, RevisionKind: "INITIAL", Vector: c.Vector, EvidenceRefs: []string{r.Evidence.Raw.EvidenceID}, ProducerID: "fixture-provider", ProducerVersion: c.ProducerVersion, RubricVersion: r.RubricVersion, CalibrationVersion: r.CalibrationVersion, CompletedAt: now, InputManifestHash: r.Routing.ManifestHash}}
	first, err := client.Submit(ctx, score)
	if err != nil {
		t.Fatal("initial score", err)
	}
	// The fixed registry deliberately lacks these new calibration/verification
	// assets. A coherent HTTP flow must retain QUARANTINED, never bypass admission.
	if first.State != "QUARANTINED" {
		t.Fatal("unregistered fixture promoted", first.State)
	}
	again, err := client.Submit(ctx, score)
	if err != nil || d.Digest(first) != d.Digest(again) {
		t.Fatal("duplicate score", err)
	}
	recorded, err := client.Receipt(ctx, r.Event.EventID, object.ObjectID, score.Score.SubmissionID)
	if err != nil || recorded == nil || d.Digest(recorded) != d.Digest(first) {
		t.Fatal("reconcile receipt", err)
	}
	version, _ = client.Version(ctx, object.ObjectID)
	revision := command
	revision.Command = dto.Command{SchemaVersion: "strategy-2.0", RequestID: "fixture-event-correction", IdempotencyKey: "fixture-event-correction", ExpectedVersion: version, Reason: "FIXTURE_ONLY"}
	revision.Event.FactVersion = 2
	revision.Event.Relation = "CORRECTION"
	if _, err = client.RegisterEvent(ctx, revision, true); err != nil {
		t.Fatal("correction", err)
	}
	version, _ = client.Version(ctx, object.ObjectID)
	second := score
	second.Command = dto.Command{SchemaVersion: "strategy-2.0", RequestID: "fixture-score-two", IdempotencyKey: "fixture-score-two", ExpectedVersion: version, Reason: "FIXTURE_ONLY"}
	second.Score.SubmissionID = "fixture-score-two"
	second.Score.FactVersion = 2
	second.Score.ScoreVersion = 2
	second.Score.RevisionKind = "REVISION"
	previous := score.Score.SubmissionID
	second.Score.PreviousScoreID = &previous
	if _, err = client.Submit(ctx, second); err != nil {
		t.Fatal("score revision", err)
	}
	version, _ = client.Version(ctx, object.ObjectID)
	revision.Command = dto.Command{SchemaVersion: "strategy-2.0", RequestID: "fixture-event-retraction", IdempotencyKey: "fixture-event-retraction", ExpectedVersion: version, Reason: "FIXTURE_ONLY"}
	revision.Event.FactVersion = 3
	revision.Event.Relation = "RETRACTION"
	if _, err = client.RegisterEvent(ctx, revision, true); err != nil {
		t.Fatal("retraction", err)
	}
	bad, err := submission.NewHTTP(submission.HTTPOptions{BaseURL: server.URL, Token: "fixture-only-worker", Binding: binding, ResearchOnly: true, Client: &http.Client{Timeout: time.Second}, MaxResponseBytes: 10000, MaxPages: 10})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = bad.Version(ctx, object.ObjectID); err == nil {
		t.Fatal("research accepted workload listener")
	}
	after, err := store.Read(ctx, state.InstanceID)
	if err != nil {
		t.Fatal(err)
	}
	if after.Scores.Len() != 2 || after.EventVersions.Len() != 3 {
		t.Fatal("duplicate or missing facts", after.Scores.Len(), after.EventVersions.Len())
	}
}
