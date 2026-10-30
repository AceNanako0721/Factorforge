package trading_test

import (
	"context"
	"testing"
	"time"

	"github.com/AceNanako0721/Factorforge/src/factorforge/trading/adapters/memory"
	a "github.com/AceNanako0721/Factorforge/src/factorforge/trading/application"
	d "github.com/AceNanako0721/Factorforge/src/factorforge/trading/domain"
	"github.com/AceNanako0721/Factorforge/src/factorforge/trading/entrypoints/venueexec"
	"github.com/AceNanako0721/Factorforge/src/factorforge/trading/ports"
	"github.com/AceNanako0721/Factorforge/src/factorforge/trading/workers"
)

// Adding to a long on a demo account: when the demo executor declared no
// overlapping protections, each replacement covering the larger position was
// refused, closed and regenerated every pass; the venue kept only the old
// stops and the run went DEGRADED with the position UNPROTECTED.
type capVenue struct {
	*compatVenue
	caps map[string]any
}

func (v capVenue) Environment() string          { return "SIM" }
func (v capVenue) Venue() bool                  { return true }
func (v capVenue) Capabilities() map[string]any { return v.caps }

func demoAddAfterCover(t *testing.T, caps map[string]any) (*d.Aggregate, int) {
	t.Helper()
	run := demoRun()
	compatPosition(t, run, "BUY", "1")
	epoch, err := run.AcquireLease("demo-worker", run.Clock, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	store := memory.New("SIM")
	if err = store.Create(context.Background(), run); err != nil {
		t.Fatal(err)
	}
	venue := capVenue{&compatVenue{facts: map[string]*d.Protection{}}, caps}
	w := &workers.Protection{Store: store, Broker: venue, ExecutorID: "demo-worker", LeaseEpoch: epoch, Now: func() time.Time { return run.Clock }}
	drainProtection(t, w, run.RunKey, 2)
	if err = store.Transaction(context.Background(), run.RunKey, func(r *d.Aggregate) error {
		o := compatOrder(r, "add", "BUY", "1", compatPlan("2", "95"))
		compatFill(t, r, o, "add-fill", "1", "100", "0")
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	drainProtection(t, w, run.RunKey, 12)
	current, _ := store.Read(context.Background(), run.RunKey)
	return current, len(current.Protections.Values())
}

func TestT122DemoReplacementPlacedBeforeCancel(t *testing.T) {
	caps := venueexec.Capabilities("SIM", d.Readiness{})
	if caps["overlapping_protections"] != true || caps["atomic_protection_modify"] != false {
		t.Fatal("demo capabilities", caps)
	}
	current, _ := demoAddAfterCover(t, caps)
	for _, role := range []string{"STRATEGY", "INSURANCE"} {
		p := activeRole(current, role)
		if p == nil || p.Plan.CoveredQuantity.Cmp(cv("2")) != 0 {
			t.Fatal(role, "not resized to the added position", p)
		}
	}
	for _, p := range current.Protections.Values() {
		if p.State != "CLOSED" && p.State != "ACTIVE_VERIFIED" {
			t.Fatal("replacement left open", p.ProtectionID, p.State)
		}
	}
}

func TestT122DemoWithoutOverlapReproducesChurn(t *testing.T) {
	// Control: the capability without overlapping protections.
	current, n := demoAddAfterCover(t, map[string]any{"ordinary_and_conditional_verified": true, "overlapping_protections": false})
	if p := activeRole(current, "INSURANCE"); p != nil && p.Plan.CoveredQuantity.Cmp(cv("2")) == 0 {
		t.Fatal("control unexpectedly resized")
	}
	refused := 0
	for _, p := range current.Protections.Values() {
		if p.ReplacesID != nil && p.State == "CLOSED" {
			refused++
		}
	}
	if refused != 2 || n != 4 || current.State != "DEGRADED" || current.Positions.Values()[0].ProtectionState != "UNPROTECTED" {
		t.Fatal("control differs from the churn case", refused, n, current.State, current.Positions.Values()[0].ProtectionState)
	}
}

func TestT122LiveCapabilitiesStillFromReadiness(t *testing.T) {
	if venueexec.Capabilities("LIVE", d.Readiness{})["overlapping_protections"] != false {
		t.Fatal("LIVE overlap granted without readiness")
	}
	if venueexec.Capabilities("LIVE", d.Readiness{OverlappingProtections: true})["overlapping_protections"] != true {
		t.Fatal("LIVE readiness ignored")
	}
}

// After the 2.85 stops were verified the worker marked both 2.13 stops
// CANCEL_PENDING; the venue sync then queried them, found them still live and
// wrote them back ACTIVE_VERIFIED with the cancellation still requested. The
// worker only picks CANCEL_PENDING, so both stayed on the venue (23.2).
type syncVenue struct{ markedVenue }

func (v syncVenue) Capabilities() map[string]any {
	return venueexec.Capabilities("SIM", d.Readiness{})
}

func TestT122SyncKeepsRequestedCancellation(t *testing.T) {
	run := demoRun()
	compatPosition(t, run, "BUY", "1")
	epoch, err := run.AcquireLease("demo-worker", run.Clock, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	store := memory.New("SIM")
	if err = store.Create(context.Background(), run); err != nil {
		t.Fatal(err)
	}
	venue := syncVenue{markedVenue{demoVenue: demoVenue{&compatVenue{facts: map[string]*d.Protection{}}}, at: run.Clock.Add(time.Second), cash: "10000", entry: "100", equity: "10000"}}
	w := &workers.Protection{Store: store, Broker: venue, ExecutorID: "demo-worker", LeaseEpoch: epoch, Now: func() time.Time { return run.Clock }}
	drainProtection(t, w, run.RunKey, 2)
	before, _ := store.Read(context.Background(), run.RunKey)
	old := activeRole(before, "INSURANCE").ProtectionID
	if err = store.Transaction(context.Background(), run.RunKey, func(r *d.Aggregate) error {
		o := compatOrder(r, "add", "BUY", "1", compatPlan("2", "95"))
		compatFill(t, r, o, "add-fill", "1", "100", "0")
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	drainProtection(t, w, run.RunKey, 1) // new insurance verified, old marked
	if p, _ := store.Read(context.Background(), run.RunKey); p.Protections.Value(old).State != "CANCEL_PENDING" {
		t.Fatal("setup: old insurance", p.Protections.Value(old).State)
	}
	if _, err = a.Synchronize(context.Background(), store, run.RunKey, venue, cv("0.00000001"), false); err != nil {
		t.Fatal(err)
	}
	synced, _ := store.Read(context.Background(), run.RunKey)
	if p := synced.Protections.Value(old); p.State != "CANCEL_PENDING" || !p.CancelRequested {
		t.Fatal("sync reverted a requested cancellation", p.State, p.CancelRequested)
	}
	drainProtection(t, w, run.RunKey, 6)
	current, _ := store.Read(context.Background(), run.RunKey)
	if current.Protections.Value(old).State != "CLOSED" {
		t.Fatal("old insurance left on the venue", current.Protections.Value(old).State)
	}
	if p := activeRole(current, "INSURANCE"); p == nil || p.Plan.CoveredQuantity.Cmp(cv("2")) != 0 {
		t.Fatal("replacement lost", p)
	}
}

// A raised strategy stop that the query right after the submit did not
// confirm: the worker left the new generation UNKNOWN; the venue sync then found it live
// and wrote ACTIVE_VERIFIED. The worker only picks UNKNOWN, so the handoff
// that cancels the replaced generation never ran and both stops stayed live.
type flakyVenue struct {
	syncVenue
	fail *int
}

func (v flakyVenue) QueryProtection(ctx context.Context, run *d.Aggregate, id string) (*d.Protection, error) {
	if *v.fail > 0 {
		*v.fail--
		return nil, &ports.Ambiguous{}
	}
	return v.syncVenue.QueryProtection(ctx, run, id)
}

func TestT122SyncLeavesUnconfirmedReplacementToWorker(t *testing.T) {
	run := demoRun()
	compatPosition(t, run, "BUY", "1")
	epoch, err := run.AcquireLease("demo-worker", run.Clock, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	store := memory.New("SIM")
	if err = store.Create(context.Background(), run); err != nil {
		t.Fatal(err)
	}
	fail := 0
	venue := flakyVenue{syncVenue{markedVenue{demoVenue: demoVenue{&compatVenue{facts: map[string]*d.Protection{}}}, at: run.Clock.Add(time.Second), cash: "10000", entry: "100", equity: "10000"}}, &fail}
	w := &workers.Protection{Store: store, Broker: venue, ExecutorID: "demo-worker", LeaseEpoch: epoch, Now: func() time.Time { return run.Clock }}
	drainProtection(t, w, run.RunKey, 2)
	if err = store.Transaction(context.Background(), run.RunKey, func(r *d.Aggregate) error {
		o := compatOrder(r, "add", "BUY", "1", compatPlan("2", "95"))
		compatFill(t, r, o, "add-fill", "1", "100", "0")
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	fail = 1
	drainProtection(t, w, run.RunKey, 1) // submitted, confirmation query fails
	mid, _ := store.Read(context.Background(), run.RunKey)
	var unknown *d.Protection
	for _, p := range mid.Protections.Values() {
		if p.State == "UNKNOWN" {
			unknown = p
		}
	}
	if unknown == nil || unknown.ReplacesID == nil {
		t.Fatal("setup: no unconfirmed replacement")
	}
	if _, err = a.Synchronize(context.Background(), store, run.RunKey, venue, cv("0.00000001"), false); err != nil {
		t.Fatal(err)
	}
	drainProtection(t, w, run.RunKey, 8)
	current, _ := store.Read(context.Background(), run.RunKey)
	if p := current.Protections.Value(*unknown.ReplacesID); p.State != "CLOSED" {
		t.Fatal("replaced generation left on the venue", p.State, p.CancelRequested)
	}
	for _, role := range []string{"STRATEGY", "INSURANCE"} {
		live := 0
		for _, p := range current.Protections.Values() {
			if p.Role == role && p.State != "CLOSED" {
				live++
			}
		}
		if p := activeRole(current, role); live != 1 || p == nil || p.Plan.CoveredQuantity.Cmp(cv("2")) != 0 {
			t.Fatal(role, "live generations", live, p)
		}
	}
}

// Both generations stayed ACTIVE_VERIFIED after the sync took over; the
// worker retires the replaced one once the replacement covers the position.
func TestT122WorkerRetiresGenerationLeftBySync(t *testing.T) {
	run := demoRun()
	compatPosition(t, run, "BUY", "1")
	epoch, err := run.AcquireLease("demo-worker", run.Clock, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	store := memory.New("SIM")
	if err = store.Create(context.Background(), run); err != nil {
		t.Fatal(err)
	}
	venue := capVenue{&compatVenue{facts: map[string]*d.Protection{}}, venueexec.Capabilities("SIM", d.Readiness{})}
	w := &workers.Protection{Store: store, Broker: venue, ExecutorID: "demo-worker", LeaseEpoch: epoch, Now: func() time.Time { return run.Clock }}
	drainProtection(t, w, run.RunKey, 2)
	if err = store.Transaction(context.Background(), run.RunKey, func(r *d.Aggregate) error {
		o := compatOrder(r, "add", "BUY", "1", compatPlan("2", "95"))
		compatFill(t, r, o, "add-fill", "1", "100", "0")
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	drainProtection(t, w, run.RunKey, 1)
	var replaced string
	// Recreate that snapshot: the replaced generation was never marked.
	if err = store.Transaction(context.Background(), run.RunKey, func(r *d.Aggregate) error {
		for _, p := range r.Protections.Values() {
			if p.ReplacesID != nil && p.State == "ACTIVE_VERIFIED" {
				replaced = *p.ReplacesID
				old := r.Protections.Value(replaced)
				old.State, old.CancelRequested = "ACTIVE_VERIFIED", false
				venue.facts[replaced].State = "ACTIVE_VERIFIED"
			}
		}
		return nil
	}); err != nil || replaced == "" {
		t.Fatal("setup", err, replaced)
	}
	drainProtection(t, w, run.RunKey, 10)
	current, _ := store.Read(context.Background(), run.RunKey)
	if p := current.Protections.Value(replaced); p.State != "CLOSED" {
		t.Fatal("replaced generation left live", p.State, p.CancelRequested)
	}
}
