package strategy_test

import (
	"bytes"
	"context"
	"encoding/json"
	"github.com/AceNanako0721/Factorforge/src/factorforge/strategy/adapters"
	api "github.com/AceNanako0721/Factorforge/src/factorforge/strategy/api"
	app "github.com/AceNanako0721/Factorforge/src/factorforge/strategy/application"
	sd "github.com/AceNanako0721/Factorforge/src/factorforge/strategy/domain"
	contract "github.com/AceNanako0721/Factorforge/tools/contractguard"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"
)

func TestP2TraceReadOnlyPaginationPrivacyAndMissingFacts(t *testing.T) {
	state, worker, create := strategyBase(t)
	clock := adapters.NewReplay(create.Parameters.ValidFrom.Add(time.Hour))
	obj := create.Object
	state.Objects.Set(obj.ObjectID, &obj)
	other := sd.Clone(obj)
	other.ObjectID = "other-object"
	state.Objects.Set(other.ObjectID, &other)
	addQueryFacts(state, obj.ObjectID, other.ObjectID, clock.Now())
	state.Cases.Set("empty-case", &sd.CaseRecord{CaseID: "empty-case", ObjectID: other.ObjectID})
	state.Candidates.Set("missing-history", map[string]any{"object_id": obj.ObjectID, "state": "MONITORING", "parent_version": "p0", "activated_version": "p1", "history": []string{"ACTIVE", "MONITORING"}, "prompt": "private-canary"})
	state.Candidates.Set("real-history", map[string]any{"object_id": obj.ObjectID, "state": "FROZEN", "parent_version": "p0", "activated_version": "p1", "proposal_evidence": []string{"e0"}})
	state.Audit = append(state.Audit, map[string]any{"action": "PARAMETER_ACTIVATED", "candidate_id": "real-history", "version": "p1", "at": sd.ISO(clock.Now().Add(-time.Minute)), "identity": map[string]any{"prompt": "private-canary"}, "reason": "private-canary"}, map[string]any{"action": "PARAMETER_ROLLED_BACK", "candidate_id": "real-history", "at": sd.ISO(clock.Now()), "reason": "private-canary", "command_payload": map[string]any{"token": "private-canary"}})
	state.Cases.Set("case", &sd.CaseRecord{CaseID: "case", ObjectID: obj.ObjectID})
	state.Attributions.Set("candidate", map[string]any{"candidate_id": "candidate", "case_id": "case", "state": "UNVERIFIED", "confidence": "0.1", "alternative_causes": []string{"private-canary"}, "prompt": "private-canary"})
	state.Counterfactuals.Set("scenario", map[string]any{"scenario_id": "scenario", "case_id": "case", "state": "INTERVAL_ONLY", "learning_frozen": true, "result": map[string]any{"fees": "1", "private": "private-canary"}, "create_run": map[string]any{"prompt": "private-canary"}})
	public := sd.PublicPrincipal{PrincipalID: "query", InstanceID: state.InstanceID, Environment: state.Environment, Scopes: []sd.ApiScope{sd.Query}}
	denied := public
	denied.PrincipalID = "denied"
	denied.Scopes = []sd.ApiScope{sd.Research}
	cross := public
	cross.Environment = "LIVE"
	store := adapters.NewMemory(state)
	policy := app.ReadPolicy{DefaultLimit: 1, MaxLimit: 10, MaxRecords: 100, CursorAge: time.Minute, CursorKey: []byte("fixture-only-query-signing")}
	handler, err := api.New(api.Options{Store: store, Clock: clock, ReadPolicy: policy, Tokens: map[string]sd.Identity{"query": public, "denied": denied, "cross": cross}})
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(handler)
	defer server.Close()
	var document map[string]any
	json.Unmarshal(api.OpenAPI(false), &document)
	compiler := contract.NewCompiler()
	if err := compiler.AddResource("urn:query-test", document); err != nil {
		t.Fatal(err)
	}
	before, err := store.Read(context.Background(), state.InstanceID)
	if err != nil {
		t.Fatal(err)
	}
	beforeRaw, _ := json.Marshal(before)
	read := func(token, route string, status int) app.ReadPage {
		t.Helper()
		request, _ := http.NewRequest(http.MethodGet, server.URL+api.Prefix+route, nil)
		request.Header.Set("Authorization", "Bearer "+token)
		response, e := server.Client().Do(request)
		if e != nil {
			t.Fatal(e)
		}
		defer response.Body.Close()
		var raw json.RawMessage
		if json.NewDecoder(response.Body).Decode(&raw) != nil {
			t.Fatal("invalid query response")
		}
		if response.StatusCode != status {
			t.Fatalf("%s: %d %s", route, response.StatusCode, raw)
		}
		if bytes.Contains(raw, []byte("private-canary")) {
			t.Fatal("private payload leaked")
		}
		var page app.ReadPage
		if status == 200 && json.Unmarshal(raw, &page) != nil {
			t.Fatal("invalid page")
		}
		if status == 200 {
			resource := strings.Split(route, "?")[0]
			view := map[string]string{"/objects": "ObjectSummary", "/events": "EventReadView", "/events/fact": "EventReadView", "/events/fact/scores": "ScoreReadView", "/objects/" + obj.ObjectID + "/ledger": "LedgerEntry", "/cases/case/attributions": "AttributionReadView", "/cases/case/counterfactuals": "CounterfactualReadView", "/audit": "AuditReadView", "/parameter-activations": "ActivationReadView"}[resource]
			schema, e := compiler.Compile("urn:query-test#/components/schemas/ReadPage_" + view)
			if e != nil {
				t.Fatal(e)
			}
			var actual any
			decoder := json.NewDecoder(bytes.NewReader(raw))
			decoder.UseNumber()
			decoder.Decode(&actual)
			if e = schema.Validate(actual); e != nil {
				t.Fatalf("%s response contract: %v", route, e)
			}
		}
		return page
	}
	page := read("query", "/objects", 200)
	if len(page.Items) != 1 || page.Cursor == nil || page.ObservedAt != nil {
		t.Fatal("bounded object page or missing observation incorrect")
	}
	next := read("query", "/objects?cursor="+url.QueryEscape(*page.Cursor), 200)
	if len(next.Items) != 1 || next.Cursor != nil || next.Items[0]["object_id"] == page.Items[0]["object_id"] {
		t.Fatal("unstable paging")
	}
	read("query", "/objects?limit=11", 422)
	read("query", "/objects?limit=0", 422)
	read("query", "/objects?limit=1&limit=2", 422)
	read("query", "/objects?limit=", 422)
	read("query", "/objects?limit=%xx", 422)
	read("query", "/objects?cursor="+url.QueryEscape(*page.Cursor)+"&object_id="+obj.ObjectID, 409)
	read("query", "/objects?cursor=forged", 409)
	read("denied", "/objects", 403)
	read("cross", "/objects", 403)
	for _, path := range []string{"/events", "/objects/" + obj.ObjectID + "/ledger", "/cases/case/attributions", "/cases/case/counterfactuals", "/audit"} {
		read("query", path, 200)
	}
	versions := read("query", "/events/fact?limit=10", 200)
	if len(versions.Items) != 2 || versions.Items[0]["fact_version"] != float64(1) || versions.Items[1]["fact_version"] != float64(2) {
		t.Fatal("event version history missing")
	}
	read("query", "/events/fact?revision=99", 404)
	revision := read("query", "/events/fact?revision=1", 200)
	if len(revision.Items) != 1 {
		t.Fatal("revision filter missing")
	}
	scores := read("query", "/events/fact/scores", 200)
	if len(scores.Items) != 1 || scores.Items[0]["admission_receipt"] == nil {
		t.Fatal("score admission missing")
	}
	read("query", "/events/not-recorded/scores", 404)
	filtered := read("query", "/events/fact?from="+url.QueryEscape(sd.ISO(clock.Now().Add(time.Second))), 200)
	if len(filtered.Items) != 0 {
		t.Fatal("future filter returned historical facts")
	}
	activations := read("query", "/parameter-activations?limit=10", 200)
	if len(activations.Items) != 3 {
		t.Fatal("missing or fabricated activation records", activations.Items)
	}
	missing, published, rolled := false, false, false
	for _, item := range activations.Items {
		switch item["state"] {
		case "PUBLISHED":
			published = true
		case "ROLLED_BACK":
			rolled = true
		}
		if item["record_status"] == "NOT_RECORDED" {
			missing = true
			if item["effective_at"] != nil || item["new_version"] != nil {
				t.Fatal("unrecorded history inferred")
			}
		}
	}
	if !missing || !published || !rolled {
		t.Fatal("activation provenance missing")
	}
	read("query", "/cases/not-owned/attributions", 404)
	read("query", "/audit?from=2026-01-01T00:00:00%2B09:00", 422)
	after, _ := store.Read(context.Background(), state.InstanceID)
	afterRaw, _ := json.Marshal(after)
	if !bytes.Equal(beforeRaw, afterRaw) || !clock.Now().Equal(create.Parameters.ValidFrom.Add(time.Hour)) {
		t.Fatal("query or rejected query changed state/clock/outbox")
	}
	if err = clock.Advance(clock.Now().Add(2 * time.Minute)); err != nil {
		t.Fatal(err)
	}
	read("query", "/objects?cursor="+url.QueryEscape(*page.Cursor), 409)
	if err = store.Transaction(context.Background(), state.InstanceID, func(s *sd.StrategyState) error { s.Version++; return nil }); err != nil {
		t.Fatal(err)
	}
	read("query", "/objects?cursor="+url.QueryEscape(*page.Cursor), 409)
	query := app.QueryService{Store: store, Clock: clock, Policy: policy}
	worker.ObjectIDs = []string{obj.ObjectID}
	_, err = query.Trace(context.Background(), worker, "attributions", "empty-case", app.ReadFilter{})
	assertStrategyCode(t, err, "QUERY_OBJECT_FORBIDDEN")
	_, err = query.Trace(context.Background(), worker, "ledger", other.ObjectID, app.ReadFilter{})
	assertStrategyCode(t, err, "QUERY_OBJECT_FORBIDDEN")
	policy.MaxRecords = 10
	policy.MaxLimit = 1
	query.Policy = policy
	if err = store.Transaction(context.Background(), state.InstanceID, func(s *sd.StrategyState) error {
		for i := 0; i < 11; i++ {
			s.Audit = append(s.Audit, map[string]any{"action": "fixture"})
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	_, err = query.Trace(context.Background(), public, "audit", "", app.ReadFilter{})
	assertStrategyCode(t, err, "QUERY_RESOURCE_LIMIT")
}

// These persisted facts include a hidden free dictionary and a shared event.
// The public query may project only the documented facts and visible bindings.
func addQueryFacts(state *sd.StrategyState, objectID, otherID string, at time.Time) {
	evidence := sd.EvidenceRef{EvidenceID: "evidence", ContentHash: strings.Repeat("a", 64), SourceID: "source", LicenceRef: "fixture", FirstPublicAt: at, ReceivedAt: at, AvailableAt: at, SpanRefs: []string{"span"}, VerificationRef: "verification"}
	claim := sd.Claim{ClaimID: "claim", NormalizedFact: "synthetic fact", SubjectID: "subject", EconomicItem: "item", Period: "period", FactTime: at, VerifiedAt: at, EvidenceRefs: []string{"evidence"}, NumbersWithUnits: map[string]string{"prompt": "private-canary"}, Weight: d("1"), VerificationManifest: "fixture"}
	event := sd.Event{EventID: "fact", FamilyID: "family", FactVersion: 1, Relation: "NEW", SubjectID: "subject", EventType: "type", OccurredAt: at, FirstPublicAt: at, Claims: []sd.Claim{claim}, EvidenceRefs: []sd.EvidenceRef{evidence}, ObjectIDs: []string{objectID, otherID}, State: "VERIFIED", Novelty: d("1")}
	if otherID == "" {
		event.ObjectIDs = []string{objectID}
	}
	state.EventVersions.Set("fact:1", &event)
	second := sd.Clone(event)
	second.FactVersion = 2
	second.Relation = "NEW_FACT"
	state.EventVersions.Set("fact:2", &second)
	state.Events.Set(event.EventID, &second)
	score := sd.ScoreSubmission{SubmissionID: "score", EventID: event.EventID, FactVersion: 2, ObjectID: objectID, ScoreVersion: 1, RevisionKind: "INITIAL", Vector: sd.ScoreVector{Direction: 1, ImpactPoints: d("1"), UnknownFields: []string{}}, EvidenceRefs: []string{"evidence"}, ProducerID: "fixture", ProducerVersion: "fixture", RubricVersion: "fixture", CalibrationVersion: "fixture", CompletedAt: at, InputManifestHash: strings.Repeat("b", 64)}
	state.Scores.Set(score.SubmissionID, &score)
	state.Receipts.Set(score.SubmissionID, &sd.AdmissionReceipt{SubmissionID: score.SubmissionID, State: "QUARANTINED", ReasonCodes: []string{"FIXTURE_UNKNOWN"}})
	state.Contributions.Set("contribution", &sd.Contribution{ContributionID: "contribution", ObjectID: objectID, EventID: "fact", FamilyID: "family", Direction: 1, InitialAmount: d("1"), RemainingAmount: d("1"), EffectiveAt: at, LastUpdatedAt: at, HalfLife: d("1"), Eta: d("1"), HighWater: d("0"), ReferencePrice: d("1"), FrozenParameterVersion: "fixture", ScoreID: "score", Quality: d("1"), PriceConsumed: d("0"), State: "ACTIVE", FactWeights: map[string]sd.Decimal{"claim": d("1")}})
	state.Ledger = append(state.Ledger, sd.LedgerEntry{Sequence: 1, ContributionID: "contribution", At: at, Start: d("0"), Injection: d("1"), RevisionDelta: d("0"), TimeConsumption: d("0"), PriceConsumption: d("0"), Invalidation: d("0"), RejectedAmount: d("0"), End: d("1"), Reason: "FIXTURE", Budget: d("1"), DeltaHighWater: d("0")})
}
