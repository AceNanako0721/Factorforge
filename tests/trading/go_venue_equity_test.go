package trading_test

import (
	"context"
	"testing"
	"time"

	"github.com/AceNanako0721/Factorforge/src/factorforge/trading/adapters/memory"
	a "github.com/AceNanako0721/Factorforge/src/factorforge/trading/application"
	d "github.com/AceNanako0721/Factorforge/src/factorforge/trading/domain"
)

// The venue marks its equity at its own mark price. A demo run
// holding a position stayed in RECOVERY_CHECK because the run's collected mark
// differed from the venue's by a few ticks.
type markedVenue struct {
	demoVenue
	at          time.Time
	cash, entry string
	equity      string
}

func (v markedVenue) GetAccount(context.Context, *d.Aggregate) (map[string]any, error) {
	return map[string]any{"observed_at": v.at, "equity": v.equity, "cash": v.cash, "available_margin": v.cash}, nil
}
func (v markedVenue) GetPositions(_ context.Context, run *d.Aggregate) ([]*d.Position, error) {
	rows := []*d.Position{}
	for _, p := range run.Positions.Values() {
		copy := *p
		entry := cv(v.entry)
		copy.AverageEntry = &entry
		rows = append(rows, &copy)
	}
	return rows, nil
}

func (markedVenue) ListOpenOrders(context.Context, *d.Aggregate) ([]*d.Order, error) {
	return []*d.Order{}, nil
}
func (markedVenue) ListFills(context.Context, *d.Aggregate) ([]*d.Fill, error) {
	return []*d.Fill{}, nil
}
func (markedVenue) GetIncome(context.Context, *d.Aggregate) ([]*d.Income, error) {
	return []*d.Income{}, nil
}

func venueIssues(t *testing.T, cash, entry, equity string) []string {
	t.Helper()
	run := demoRun()
	compatPosition(t, run, "SELL", "1")
	store := memory.New("SIM")
	if err := store.Create(context.Background(), run); err != nil {
		t.Fatal(err)
	}
	venue := markedVenue{demoVenue: demoVenue{&compatVenue{facts: map[string]*d.Protection{}}}, at: run.Clock.Add(time.Second), cash: cash, entry: entry, equity: equity}
	issues, err := a.Synchronize(context.Background(), store, run.RunKey, venue, cv("0.00000001"), false)
	if err != nil {
		t.Fatal(err)
	}
	return issues
}

func TestT121VenueEquityComparedAtRunMark(t *testing.T) {
	// Short 1 @100, run mark 100; the venue marks at 99 and reports 10001.
	if issues := venueIssues(t, "10000", "100", "10001"); len(issues) != 0 {
		t.Fatal("mark difference blocked the run", issues)
	}
}

func TestT121VenueAccountFactsStillStrict(t *testing.T) {
	for _, tc := range []struct{ name, cash, entry string }{
		{"wallet", "10000.01", "100"},
		{"entry", "10000", "100.5"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if issues := venueIssues(t, tc.cash, tc.entry, "10001"); !d.Has(issues, "EXTERNAL_ACCOUNT_DIFFERENCE") {
				t.Fatal("difference not reported", issues)
			}
		})
	}
}
