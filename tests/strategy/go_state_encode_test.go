package strategy_test

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/AceNanako0721/Factorforge/src/factorforge/strategy/adapters"
	app "github.com/AceNanako0721/Factorforge/src/factorforge/strategy/application"
	sd "github.com/AceNanako0721/Factorforge/src/factorforge/strategy/domain"
)

// The store encoder writes exactly json.Marshal(JSONValue(v)) without the
// intermediate maps, for a whole aggregate after a decision, a dispatch and
// a stop directive, and for its parts.
func TestEncodeMatchesJSONValue(t *testing.T) {
	state, obj, _, snap, id, at := stopFixture(t)
	store := adapters.NewMemory(state)
	clock := adapters.NewReplay(at)
	trading := &retryPlanTrading{compositionTrading: compositionTrading{snap: snap}}
	service := app.Service{Store: store, Clock: clock, Trading: trading}
	if _, err := service.SubmitStopDirective(context.Background(), id, directiveCommand(state, obj, at, "INITIAL_DISTANCE", "UPPER_MANAGED", 1)); err != nil {
		t.Fatal(err)
	}
	cycle := app.DecisionCycle{Store: store, Clock: clock, Trading: trading}
	if _, err := cycle.Tick(context.Background(), id, nil); err != nil {
		t.Fatal(err)
	}
	if err := cycle.Dispatch(context.Background(), id); err != nil {
		t.Fatal(err)
	}
	after, err := store.Read(context.Background(), state.InstanceID)
	if err != nil {
		t.Fatal(err)
	}
	want, err := json.Marshal(sd.JSONValue(after))
	if err != nil {
		t.Fatal(err)
	}
	fields, err := sd.EncodeFields(after)
	if err != nil {
		t.Fatal(err)
	}
	if got := sd.EncodeObject(fields); string(got) != string(want) {
		t.Fatalf("aggregate encoding differs:\n got %.400s\nwant %.400s", got, want)
	}
	if len(want) < 2000 || after.Decisions.Len() == 0 || after.Outbox.Len() == 0 {
		t.Fatal("fixture too small to exercise the encoder", len(want))
	}
	for _, value := range []any{after.Objects, after.Decisions, after.Outbox, sd.ExposureRisk{Value: sd.Int(1), Stress: sd.Int(2), Group: "<g&>"}, map[int]any{1: []byte{1, 2}, 2: json.Number("3.5"), 3: nil, 4: "é "}, struct {
		Scopes []string `json:"scopes"`
		At     *int     `json:"at,omitempty"`
	}{Scopes: []string{"b", "a", "b"}}} {
		want, _ := json.Marshal(sd.JSONValue(value))
		got, err := sd.Marshal(value)
		if err != nil || string(got) != string(want) {
			t.Fatalf("value encoding differs: %s / %s (%v)", got, want, err)
		}
	}
}

// VerifyLedger checks only rows after a prefix it already accepted, and a
// changed row anywhere is still refused, also when the caller's slice is
// changed in place after verification.
func TestVerifyLedgerRechecksChangedRows(t *testing.T) {
	row := func(seq, start, injection, consumed, end int) sd.LedgerEntry {
		z := sd.Int(0)
		return sd.LedgerEntry{Sequence: seq, ContributionID: "c", Start: sd.Int(start), Injection: sd.Int(injection), RevisionDelta: z, TimeConsumption: sd.Int(consumed), PriceConsumption: z, Invalidation: z, RejectedAmount: z, End: sd.Int(end), Budget: z, DeltaHighWater: z}
	}
	tolerance := sd.Int(0)
	ledger := []sd.LedgerEntry{row(1, 0, 4, 0, 4), row(2, 4, 0, 1, 3)}
	if err := sd.VerifyLedger(sd.SentimentState{Ledger: ledger}, tolerance); err != nil {
		t.Fatal(err)
	}
	ledger = append(ledger, row(3, 3, 2, 0, 5))
	if err := sd.VerifyLedger(sd.SentimentState{Ledger: ledger}, tolerance); err != nil {
		t.Fatal(err)
	}
	if err := sd.VerifyLedger(sd.SentimentState{Ledger: append(append([]sd.LedgerEntry(nil), ledger...), row(4, 4, 0, 0, 4))}, tolerance); err == nil {
		t.Fatal("new row with a wrong start accepted")
	}
	ledger[1].End = sd.Int(7) // changed in place after it was verified
	if err := sd.VerifyLedger(sd.SentimentState{Ledger: ledger}, tolerance); err == nil {
		t.Fatal("changed verified row accepted")
	}
	ledger[1].End = sd.Int(3)
	if err := sd.VerifyLedger(sd.SentimentState{Ledger: ledger, Contributions: []sd.Contribution{{ContributionID: "c", RemainingAmount: sd.Int(5)}}}, tolerance); err != nil {
		t.Fatal(err)
	}
	if err := sd.VerifyLedger(sd.SentimentState{Ledger: ledger, Contributions: []sd.Contribution{{ContributionID: "c", RemainingAmount: sd.Int(6)}}}, tolerance); err == nil {
		t.Fatal("contribution balance mismatch accepted")
	}
}
