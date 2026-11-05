package strategy_test

import (
	"context"
	"testing"

	"github.com/AceNanako0721/Factorforge/src/factorforge/strategy/adapters"
	app "github.com/AceNanako0721/Factorforge/src/factorforge/strategy/application"
)

// T2-40: after the object's own venue protection exit flattened the
// position, the object takes the raised owner epoch and leaves recovery;
// every other epoch change keeps it in recovery.
func TestProtectionExitOwnerEpochIsAdopted(t *testing.T) {
	cases := []struct {
		name    string
		epoch   int
		reasons []any
		actual  string
		adopted bool
	}{
		{"own protection exit", 1, []any{"PROTECTION_EXIT_REVIEW_REQUIRED"}, "0", true},
		{"external fact", 1, []any{"EXTERNAL_OWNER_EPOCH_CHANGED"}, "0", false},
		{"position left", 1, []any{"PROTECTION_EXIT_REVIEW_REQUIRED"}, "1", false},
		{"more than one change", 2, []any{"PROTECTION_EXIT_REVIEW_REQUIRED"}, "0", false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			s, o, _, snap, id, at := compositionCycleBase(t)
			snap.OwnerEpochs[o.ObjectID] = c.epoch
			snap.Actual[o.ObjectID] = compDecimal(c.actual)
			snap.Targets = map[string]map[string]any{o.ObjectID: {"state": "BLOCKED", "reasons": c.reasons, "owner_epoch": 0, "target_version": 1, "target_quantity": "1"}}
			snap.TargetVersions[o.ObjectID] = 1
			store := adapters.NewMemory(s)
			cycle := app.DecisionCycle{Store: store, Clock: adapters.NewReplay(at), Trading: &compositionTrading{snap: snap}}
			if _, err := cycle.Tick(context.Background(), id, nil); err != nil {
				t.Fatal(err)
			}
			state, _ := store.Read(context.Background(), s.InstanceID)
			obj := state.Objects.Value(o.ObjectID)
			adopted := obj.OwnerEpoch == c.epoch && obj.RecoveryState == "NORMAL"
			if adopted != c.adopted || !c.adopted && (obj.OwnerEpoch != 0 || obj.RecoveryState == "NORMAL") {
				t.Fatal("owner epoch", obj.OwnerEpoch, obj.RecoveryState)
			}
			_, audited, err := store.ReadAudit(context.Background(), s.InstanceID, 0, state.AuditCount, 10, []string{"OWNER_EPOCH_ADOPTED"})
			if err != nil || audited == 1 != c.adopted {
				t.Fatal("audit", audited, err)
			}
			// a second cycle does not adopt again
			if c.adopted && app.AdoptsProtectionExit(obj, snap, c.epoch) {
				t.Fatal("adopted twice")
			}
		})
	}
}
