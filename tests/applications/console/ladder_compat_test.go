package console_test

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	d "github.com/AceNanako0721/Factorforge/src/factorforge/applications/console/domain"
	iope "github.com/AceNanako0721/Factorforge/src/factorforge/applications/soxl_jev/operations"
)

// v2.19.1 (P3 design 38): the batch's ladder fields are optional, so the
// console still reads an instance API older than v2.19.0 (E15 until its
// upgrade), and reads the new fields when present.
func TestS417LadderFieldsOptionalForOlderInstanceAPI(t *testing.T) {
	at := time.Date(2026, 10, 28, 2, 0, 0, 0, time.UTC)
	status := func(b *iope.DynamicBatchView) []byte {
		raw, err := json.Marshal(iope.Record{Metadata: iope.Metadata{SchemaVersion: "instance-2.0", Environment: "SIM", InstanceID: "soxl-e15", SourceVersion: "fixture", ObservedAt: &at, SnapshotVersion: "1"},
			Data: iope.DynamicStatusView{Batch: b, Baseline: iope.DynamicBaselineView{Phase: "NONE"}, Sources: []iope.DynamicSourceView{}, Outcomes: []iope.DynamicOutcomeView{}, EquityPeak: "4000"}})
		if err != nil {
			t.Fatal(err)
		}
		return raw
	}
	batch := iope.DynamicBatchView{CycleID: "cycle-1", Direction: "LONG", Entry: "85.84", OpenedAt: at, TierHours: 24, RequestedTierHours: 72, Session: "US_REGULAR", R: "0.13", Buffer: "0.02", Trail: "0.12", Budget: "37", BestClose: "103", CloseStop: "96.14", Trailing: true, Transferred: true, TrailTrigger: "97"}
	old := status(&batch)
	if strings.Contains(string(old), "stop_source") || strings.Contains(string(old), "ladder") {
		t.Fatal("old-format fixture still carries the new fields", string(old))
	}
	path := d.Routes["dynamic.status"].Path
	if _, err := d.Project("instances", path, old, false); err != nil {
		t.Fatal("older instance API rejected", err)
	}
	stop := "96.1397"
	batch.StopSource, batch.Ladder = "LADDER", &iope.DynamicLadderView{Step: "0.04", Rung: 4, RungStop: &stop, NextBest: "103.008", NextStop: "99.5744"}
	batch.ExitPending = &iope.DynamicExitPendView{ExitID: "exit-1", BarCloseAt: at, ExpiresAt: at.Add(30 * time.Minute)}
	safe, err := d.Project("instances", path, status(&batch), false)
	if err != nil || !strings.Contains(string(safe.Bytes()), `"next_best":"103.008"`) || !strings.Contains(string(safe.Bytes()), `"exit_id":"exit-1"`) {
		t.Fatal("new fields", err, string(safe.Bytes()))
	}
}
