package trading_test

import (
	"context"
	"testing"
	"time"

	"github.com/AceNanako0721/Factorforge/src/factorforge/trading/adapters/memory"
	d "github.com/AceNanako0721/Factorforge/src/factorforge/trading/domain"
	"github.com/AceNanako0721/Factorforge/src/factorforge/trading/entrypoints/venueexec"
	"github.com/AceNanako0721/Factorforge/src/factorforge/trading/workers"
)

// v2.14.7 (P1 design ch. 23): E20 added 0.72 to a 2.13 long on the demo
// account. The demo executor declared no overlapping protections, so each
// replacement covering 2.85 was refused, closed and regenerated every pass
// (170 generations); the venue kept only the 2.13 stops, the run went
// DEGRADED with the position UNPROTECTED.
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
	// Control: the capability E20 ran with before v2.14.7.
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
		t.Fatal("control differs from E20", refused, n, current.State, current.Positions.Values()[0].ProtectionState)
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
