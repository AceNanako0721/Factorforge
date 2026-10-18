package strategy_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"testing"
	"time"

	pg "github.com/AceNanako0721/Factorforge/src/factorforge/strategy/adapters/postgres"
	sd "github.com/AceNanako0721/Factorforge/src/factorforge/strategy/domain"
	tradingpg "github.com/AceNanako0721/Factorforge/src/factorforge/trading/adapters/postgres"
	"github.com/jackc/pgx/v5"
)

// retentionBase returns a frozen state that already holds a dispatched
// decision, projected through the v2.5.7 bar compaction.
func retentionBase(t *testing.T) (*sd.StrategyState, *sd.DecisionView, *sd.TargetOutbox) {
	t.Helper()
	states, cases := loadReplay(t)
	for _, item := range cases {
		if sd.Text(item["kind"]) != "dispatch" {
			continue
		}
		var state sd.StrategyState
		decodeRecord(t, compactFrozenDecisions(states[sd.Text(item["after"])]), &state)
		for _, id := range state.Outbox.Keys() {
			if decision := state.Decisions.Value(id); decision != nil {
				return &state, sd.Clone(decision), sd.Clone(state.Outbox.Value(id))
			}
		}
	}
	t.Fatal("no dispatched decision in frozen replay")
	return nil, nil, nil
}

// addDecision stores a settled decision and target at cutoff under id.
func addDecision(s *sd.StrategyState, template *sd.DecisionView, outbox *sd.TargetOutbox, id string, cutoff time.Time, targetState string) {
	decision := sd.Clone(template)
	decision.DecisionID, decision.AvailableCutoff = id, cutoff
	s.Decisions.Set(id, decision)
	if outbox != nil {
		target := sd.Clone(outbox)
		target.Decision, target.ExpiresAt, target.State, target.ReservationID = sd.TargetDecision(decision), cutoff.Add(time.Minute), targetState, nil
		s.Outbox.Set(id, target)
	}
}

func TestCompactDecisionInputKeepsHashAndDropsBars(t *testing.T) {
	close1, close2 := "2026-10-12T04:40:00Z", "2026-10-12T04:45:00Z"
	inputs := map[string]any{"policy": "p", "pool": map[string]any{"contributions": []any{}}, "snapshot": map[string]any{"version": 7,
		"bars": map[string]any{"obj-a": []any{map[string]any{"close_at": close1, "close": "1"}, map[string]any{"close_at": close2, "close": "2"}}, "obj-b": []any{}}}}
	hash := sd.Digest(inputs)
	compact := sd.CompactDecisionInput(inputs)
	if sd.Digest(inputs) != hash || sd.Object(inputs["snapshot"])["bars"] == nil {
		t.Fatal("compaction mutated the hashed inputs")
	}
	snapshot := sd.Object(compact["snapshot"])
	if _, present := snapshot["bars"]; present || sd.Number(snapshot["version"]) != 7 || compact["policy"] != "p" {
		t.Fatal("bars kept or other inputs changed", snapshot)
	}
	a, b := sd.Object(sd.Object(snapshot["bars_ref"])["obj-a"]), sd.Object(sd.Object(snapshot["bars_ref"])["obj-b"])
	if sd.Number(a["count"]) != 2 || a["first_close_at"] != close1 || a["last_close_at"] != close2 || a["digest"] != sd.Digest(sd.Object(sd.Object(inputs["snapshot"])["bars"])["obj-a"]) {
		t.Fatal("bar reference", a)
	}
	if sd.Number(b["count"]) != 0 || b["first_close_at"] != nil || b["digest"] != nil {
		t.Fatal("empty bar reference", b)
	}
	if sd.Digest(sd.CompactDecisionInput(compact)) != sd.Digest(compact) {
		t.Fatal("compaction is not idempotent")
	}
}

// The delivered-decision list in the trading snapshot only grows; a decision
// keeps its count and digest so 72 retained decisions stay small (E15 stall
// of 2026-10-18 at 4 MiB).
func TestCompactDecisionInputReferencesSourceDecisions(t *testing.T) {
	ids := []any{}
	for i := 0; i < 5000; i++ {
		ids = append(ids, fmt.Sprintf("decision-%024d", i))
	}
	inputs := map[string]any{"snapshot": map[string]any{"version": 3, "bars": map[string]any{}, "source_decisions": ids}}
	hash := sd.Digest(inputs)
	compact := sd.CompactDecisionInput(inputs)
	if sd.Digest(inputs) != hash || len(sd.Strings(sd.Object(inputs["snapshot"])["source_decisions"])) != 5000 {
		t.Fatal("compaction mutated the hashed inputs")
	}
	snapshot := sd.Object(compact["snapshot"])
	ref := sd.Object(snapshot["source_decisions_ref"])
	if _, present := snapshot["source_decisions"]; present || sd.Number(ref["count"]) != 5000 || ref["digest"] != sd.Digest(ids) {
		t.Fatal("source decision reference", ref)
	}
	if raw, _ := json.Marshal(compact); len(raw) > 1000 {
		t.Fatal("compacted input too large", len(raw))
	}
	if sd.Digest(sd.CompactDecisionInput(compact)) != sd.Digest(compact) {
		t.Fatal("compaction is not idempotent")
	}
}

func TestCompactDecisionInputDropsSpentContributionsAndClosedProtections(t *testing.T) {
	contributions := []any{map[string]any{"family_id": "live", "remaining_amount": "0.5", "state": "ACTIVE"}}
	protections := []any{map[string]any{"protection_id": "p-live", "state": "ACTIVE_VERIFIED"}}
	for i := 0; i < 500; i++ {
		contributions = append(contributions, map[string]any{"family_id": fmt.Sprintf("spent-%d", i), "remaining_amount": "0", "state": "INVALID"})
		protections = append(protections, map[string]any{"protection_id": fmt.Sprintf("p-%d", i), "state": "CLOSED"})
	}
	inputs := map[string]any{"pool": map[string]any{"net": "-1", "contributions": contributions}, "snapshot": map[string]any{"bars": map[string]any{}, "protections": map[string]any{"obj": protections}}}
	hash := sd.Digest(inputs)
	compact := sd.CompactDecisionInput(inputs)
	if sd.Digest(inputs) != hash {
		t.Fatal("compaction mutated the hashed inputs")
	}
	pool := sd.Object(compact["pool"])
	live := sd.Rows(pool["contributions"])
	if len(live) != 1 || live[0]["family_id"] != "live" || pool["net"] != "-1" || sd.Number(sd.Object(pool["spent_contributions_ref"])["count"]) != 500 {
		t.Fatal("pool compaction", pool)
	}
	snapshot := sd.Object(compact["snapshot"])
	kept := sd.Rows(sd.Object(snapshot["protections"])["obj"])
	if len(kept) != 1 || kept[0]["protection_id"] != "p-live" || sd.Number(sd.Object(sd.Object(snapshot["closed_protections_ref"])["obj"])["count"]) != 500 {
		t.Fatal("protection compaction", snapshot)
	}
	if raw, _ := json.Marshal(compact); len(raw) > 1500 {
		t.Fatal("compacted input too large", len(raw))
	}
	if sd.Digest(sd.CompactDecisionInput(compact)) != sd.Digest(compact) {
		t.Fatal("compaction is not idempotent")
	}
}

func TestArchivableDecisionsRequiresEveryCondition(t *testing.T) {
	base, template, outbox := retentionBase(t)
	t0 := time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)
	at := t0.Add(49 * time.Hour)
	fresh := func() *sd.StrategyState {
		s := sd.Clone(base)
		for _, id := range s.Decisions.Keys() {
			s.Decisions.Delete(id)
			s.Outbox.Delete(id)
		}
		addDecision(s, template, outbox, "decision-old", t0, "ACK")
		addDecision(s, template, outbox, "decision-new", t0.Add(48*time.Hour), "ACK")
		return s
	}
	if got := sd.ArchivableDecisions(fresh(), at); fmt.Sprint(got) != "[decision-old]" {
		t.Fatal("settled superseded decision not archivable", got)
	}
	cases := map[string]func(*sd.StrategyState) time.Time{
		"pending target": func(s *sd.StrategyState) time.Time { s.Outbox.Value("decision-old").State = "PENDING"; return at },
		"unknown delivery": func(s *sd.StrategyState) time.Time {
			s.Outbox.Value("decision-old").State = "DELIVERY_UNKNOWN"
			return at
		},
		"latest for object": func(s *sd.StrategyState) time.Time {
			s.Decisions.Delete("decision-new")
			s.Outbox.Delete("decision-new")
			return at
		},
		"cutoff inside retention": func(s *sd.StrategyState) time.Time { return t0.Add(5 * time.Hour) },
		"target expiry inside retention": func(s *sd.StrategyState) time.Time {
			s.Outbox.Value("decision-old").ExpiresAt = at.Add(-time.Hour)
			return at
		},
		"reservation": func(s *sd.StrategyState) time.Time {
			id := "reservation-fixture"
			s.Outbox.Value("decision-old").ReservationID = &id
			s.Reservations.Set(id, &sd.Reservation{ReservationID: id, ObjectID: template.ObjectID, WindowID: "w", State: "RESERVED"})
			return at
		},
		"open case entry": func(s *sd.StrategyState) time.Time {
			s.Cases.Set("case-fixture", &sd.CaseRecord{CaseID: "case-fixture", ObjectID: template.ObjectID, Status: "OPEN", EntrySnapshot: map[string]any{"source_decision_id": "decision-old"}})
			return at
		},
		"observing case lot": func(s *sd.StrategyState) time.Time {
			s.Cases.Set("case-fixture", &sd.CaseRecord{CaseID: "case-fixture", ObjectID: template.ObjectID, Status: "OBSERVING", EntrySnapshot: map[string]any{}, RiskLots: []map[string]any{{"source_decision_id": "decision-old"}}})
			return at
		},
	}
	for name, mutate := range cases {
		s := fresh()
		if got := sd.ArchivableDecisions(s, mutate(s)); len(got) != 0 {
			t.Fatal(name, "archived", got)
		}
	}
	s := fresh()
	s.Cases.Set("case-fixture", &sd.CaseRecord{CaseID: "case-fixture", ObjectID: template.ObjectID, Status: "MATURE", EntrySnapshot: map[string]any{"source_decision_id": "decision-old"}})
	if got := sd.ArchivableDecisions(s, at); fmt.Sprint(got) != "[decision-old]" {
		t.Fatal("finished case kept the decision", got)
	}
	s = fresh()
	s.Outbox.Delete("decision-old")
	if got := sd.ArchivableDecisions(s, at); fmt.Sprint(got) != "[decision-old]" {
		t.Fatal("decision without target not archivable", got)
	}
}

func TestDecisionRetentionBoundsSerializedState(t *testing.T) {
	base, template, outbox := retentionBase(t)
	s := sd.Clone(base)
	for _, id := range s.Decisions.Keys() {
		s.Decisions.Delete(id)
		s.Outbox.Delete(id)
	}
	bars := []any{}
	for i := 0; i < 287; i++ {
		bars = append(bars, map[string]any{"close_at": fmt.Sprintf("2026-10-12T%02d:%02d:00Z", i/60, i%60), "close": "142.12000000", "high": "142.50000000", "low": "141.90000000", "final": true, "quality": "VALID", "available_at": "2026-10-12T00:00:00Z"})
	}
	inputs := map[string]any{"policy": template.InputSnapshot["policy"], "pool": template.InputSnapshot["pool"], "snapshot": map[string]any{"bars": map[string]any{template.ObjectID: bars}}}
	template.InputSnapshot, template.InputHash = sd.CompactDecisionInput(inputs), sd.Digest(inputs)
	t0 := time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)
	for i := 0; i < 300; i++ {
		at := t0.Add(time.Duration(i) * 7 * time.Minute)
		addDecision(s, template, outbox, fmt.Sprintf("decision-%04d", i), at, "ACK")
		for _, id := range sd.ArchivableDecisions(s, at) {
			s.Decisions.Delete(id)
			s.Outbox.Delete(id)
		}
	}
	raw, _ := json.Marshal(sd.JSONValue(s))
	if s.Decisions.Len() > 6*60/7+2 || len(raw) > 1500000 {
		t.Fatal("retained decisions unbounded", s.Decisions.Len(), len(raw))
	}
	if s.Decisions.Value("decision-0299") == nil || s.Decisions.Value("decision-0000") != nil {
		t.Fatal("wrong decisions retained")
	}
}

func TestNativeP2DecisionArchiveIsTransactional(t *testing.T) {
	ctx := context.Background()
	server, err := tradingpg.StartTemporary(ctx, t.TempDir(), nativePG(t))
	if err != nil {
		t.Fatal(err)
	}
	defer server.Close()
	if err = pg.Initialize(ctx, server.AdminDSN, "SIM"); err != nil {
		t.Fatal(err)
	}
	adminConn, err := pgx.Connect(ctx, server.AdminDSN)
	if err != nil {
		t.Fatal(err)
	}
	defer adminConn.Close(ctx)
	dsns := map[string]string{}
	for _, kind := range []string{"public", "worker"} {
		role := "p2_archive_" + kind
		if _, err = adminConn.Exec(ctx, "CREATE ROLE "+pgx.Identifier{role}.Sanitize()+" LOGIN PASSWORD 'fixture-only-password'"); err != nil {
			t.Fatal(err)
		}
		if _, err = adminConn.Exec(ctx, "GRANT factorforge_strategy_sim_"+kind+" TO "+pgx.Identifier{role}.Sanitize()); err != nil {
			t.Fatal(err)
		}
		u, _ := url.Parse(server.AdminDSN)
		u.User = url.UserPassword(role, "fixture-only-password")
		dsns[kind] = u.String()
	}
	base, template, outbox := retentionBase(t)
	t0 := time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)
	for _, id := range base.Decisions.Keys() {
		base.Decisions.Delete(id)
		base.Outbox.Delete(id)
	}
	addDecision(base, template, outbox, "decision-old", t0, "ACK")
	addDecision(base, template, outbox, "decision-new", t0.Add(48*time.Hour), "ACK")
	admin, err := pg.Open(ctx, server.AdminDSN, "SIM", "worker")
	if err != nil {
		t.Fatal(err)
	}
	defer admin.Close()
	if err = admin.Create(ctx, base); err != nil {
		t.Fatal(err)
	}
	worker, err := pg.Open(ctx, dsns["worker"], "SIM", "worker")
	if err != nil {
		t.Fatal(err)
	}
	defer worker.Close()
	public, err := pg.Open(ctx, dsns["public"], "SIM", "public")
	if err != nil {
		t.Fatal(err)
	}
	defer public.Close()
	id := base.InstanceID
	archived := func() int {
		var n int
		if err := adminConn.QueryRow(ctx, "SELECT count(*) FROM strategy_sim.decision_archive WHERE instance_id=$1", id).Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n
	}
	remove := func(s *sd.StrategyState) {
		for _, key := range sd.ArchivableDecisions(s, t0.Add(49*time.Hour)) {
			s.Decisions.Delete(key)
			s.Outbox.Delete(key)
		}
	}
	assertStrategyCode(t, public.Transaction(ctx, id, func(s *sd.StrategyState) error { remove(s); return nil }), "PUBLIC_INTERNAL_STATE_FORBIDDEN")
	rollback := worker.Transaction(ctx, id, func(s *sd.StrategyState) error {
		remove(s)
		return &sd.Error{Code: "FIXTURE_ROLLBACK", Status: 409}
	})
	assertStrategyCode(t, rollback, "FIXTURE_ROLLBACK")
	if state, err := worker.Read(ctx, id); err != nil || state.Decisions.Value("decision-old") == nil || archived() != 0 {
		t.Fatal("rolled-back archive left effects", err, archived())
	}
	if err = worker.Transaction(ctx, id, func(s *sd.StrategyState) error { remove(s); return nil }); err != nil {
		t.Fatal(err)
	}
	state, err := worker.Read(ctx, id)
	if err != nil || state.Decisions.Value("decision-old") != nil || state.Outbox.Value("decision-old") != nil || state.Decisions.Value("decision-new") == nil || archived() != 1 {
		t.Fatal("archive and removal not committed together", err, archived())
	}
	var payload map[string]any
	if err = adminConn.QueryRow(ctx, "SELECT payload FROM strategy_sim.decision_archive WHERE instance_id=$1 AND decision_id='decision-old'", id).Scan(&payload); err != nil ||
		sd.Text(sd.Object(payload["decision"])["decision_id"]) != "decision-old" || sd.Text(sd.Object(payload["outbox"])["state"]) != "ACK" {
		t.Fatal("archive payload", err, payload)
	}
	reenter := worker.Transaction(ctx, id, func(s *sd.StrategyState) error {
		addDecision(s, template, outbox, "decision-old", t0, "ACK")
		return nil
	})
	assertStrategyCode(t, reenter, "STORE_STATE_INVALID")
	if _, err = adminConn.Exec(ctx, "DELETE FROM strategy_sim.decision_archive WHERE instance_id=$1", id); err == nil {
		t.Fatal("archive is mutable")
	}
}

// A stored state above the request-body limit still loads, so retention can
// shrink it; request bodies keep the 4 MiB bound.
func TestDecodeStateLoadsAboveRequestLimit(t *testing.T) {
	base, template, outbox := retentionBase(t)
	s := sd.Clone(base)
	t0 := time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)
	pad := make([]any, 0, 60000)
	for i := 0; i < 60000; i++ {
		pad = append(pad, fmt.Sprintf("decision-%024d", i))
	}
	big := sd.Clone(template)
	big.InputSnapshot = map[string]any{"pad": pad}
	for i := 0; i < 3; i++ {
		addDecision(s, big, outbox, fmt.Sprintf("decision-big-%d", i), t0.Add(time.Duration(i)*time.Minute), "ACK")
	}
	raw, _ := json.Marshal(sd.JSONValue(s))
	if len(raw) <= 4<<20 {
		t.Fatal("fixture below the request limit", len(raw))
	}
	var viaRequest, viaStore sd.StrategyState
	if sd.DecodeJSON(raw, &viaRequest) == nil {
		t.Fatal("request decoding accepted more than 4 MiB")
	}
	if err := sd.DecodeState(raw, &viaStore); err != nil || viaStore.Decisions.Len() != s.Decisions.Len() {
		t.Fatal("stored state above 4 MiB did not load", err)
	}
}
