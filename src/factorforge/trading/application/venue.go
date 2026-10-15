package application

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"

	d "github.com/AceNanako0721/Factorforge/src/factorforge/trading/domain"
	"github.com/AceNanako0721/Factorforge/src/factorforge/trading/domain/decimal"
	"github.com/AceNanako0721/Factorforge/src/factorforge/trading/ports"
	"sort"
	"strings"
	"time"
)

type orderFacts struct {
	order *d.Order
	fills []d.Fill
}

func observed(value any) (time.Time, error) {
	switch v := value.(type) {
	case time.Time:
		return v, nil
	case string:
		at, err := time.Parse(time.RFC3339Nano, v)
		if err == nil {
			return at, nil
		}
	}
	return time.Time{}, problem("VENUE_ACCOUNT_FORMAT_INVALID", 503)
}

// Synchronize reads venue facts without holding a SQL lock, then applies them
// only to the exact snapshot version. Its overlapping cursor cannot skip facts
// arriving between sequential account and fill queries.
func Synchronize(ctx context.Context, store ports.Store, key d.RunKey, broker ports.Broker, tolerance decimal.Value, recovery bool) ([]string, error) {
	if tolerance.Sign() < 0 {
		return nil, problem("RECONCILIATION_TOLERANCE_INVALID")
	}
	if store.Environment() != broker.Environment() {
		return nil, problem("EXECUTOR_ENVIRONMENT_MISMATCH", 403)
	}
	snapshot, err := store.Read(ctx, key)
	if err != nil {
		return nil, err
	}
	// Legacy cycle labels are provisional until the external side/quantity query
	// has verified the corresponding STRATEGY record. No insurance is inferred.
	for _, pos := range snapshot.Positions.Values() {
		if pos.Quantity.Sign() != 0 && pos.PositionCycleID == "" {
			pos.PositionCycleID = d.StableID("cycle-", RequestHash(Response{"run": key, "instrument": pos.InstrumentKey, "direction": d.Direction(pos.Quantity), "epoch": snapshot.OwnerEpochs.Value(pos.InstrumentKey.Code()), "legacy": true}))
		}
	}
	results := []orderFacts{}
	protections := []*d.Protection{}
	for _, order := range snapshot.Orders.Values() {
		pending := false
		for _, item := range snapshot.Outbox {
			pending = pending || (item.OrderID == order.OrderID && (item.State == "DISPATCHING" || item.State == "UNKNOWN"))
		}
		if !d.Terminal(order.State) && (order.ExternalOrderID != nil || order.State == "UNKNOWN" || order.State == "DISPATCHING" || pending) {
			result, fills, err := broker.QueryOrder(ctx, snapshot, order.ClientOrderID)
			if err != nil {
				return nil, err
			}
			results = append(results, orderFacts{result, fills})
		}
	}
	known, err := snapshot.Clone()
	if err != nil {
		return nil, err
	}
	for _, p := range snapshot.Protections.Values() {
		if p.State != "CLOSED" && p.State != "PENDING" {
			fact, err := broker.QueryProtection(ctx, snapshot, p.ProtectionID)
			if err != nil {
				return nil, err
			}
			protections = append(protections, fact)
			known.Protections.Set(fact.ProtectionID, fact)
		}
	}
	conditionalFacts := map[string][]d.ConditionalOrderFact{}
	for _, spec := range snapshot.Specs.Values() {
		facts, err := broker.ListOpenConditionals(ctx, known, spec.Key)
		if err != nil {
			return nil, err
		}
		conditionalFacts[spec.Key.Code()] = facts
		for _, p := range protections {
			if p.InstrumentKey != spec.Key || p.State != "ACTIVE_VERIFIED" {
				continue
			}
			found := false
			for _, fact := range facts {
				if fact.ClientID != nil && *fact.ClientID == d.ConditionalClientID(snapshot.RunKey, p) {
					found = true
				}
			}
			if !found {
				p.State = "UNKNOWN"
			}
		}
	}
	if _, err = broker.ListOpenOrders(ctx, known); err != nil {
		return nil, err
	}
	fills, err := broker.ListFills(ctx, snapshot)
	if err != nil {
		return nil, err
	}
	incomes, err := broker.GetIncome(ctx, snapshot)
	if err != nil {
		return nil, err
	}
	positions, err := broker.GetPositions(ctx, snapshot)
	if err != nil {
		return nil, err
	}
	account, err := broker.GetAccount(ctx, snapshot)
	if err != nil {
		return nil, err
	}
	at, err := observed(account["observed_at"])
	if err != nil {
		return nil, err
	}
	equity, err := venueAmount(account["equity"])
	if err != nil {
		return nil, problem("VENUE_ACCOUNT_FORMAT_INVALID", 503)
	}
	cash, err := venueAmount(account["cash"])
	if err != nil {
		return nil, problem("VENUE_ACCOUNT_FORMAT_INVALID", 503)
	}
	var issues []string
	err = store.Transaction(ctx, key, func(run *d.Aggregate) error {
		if run.Version != snapshot.Version {
			return problem("VENUE_SNAPSHOT_CHANGED", 409)
		}
		e := d.NewEngine(run)
		if err := AdvanceLiveClock(e, at); err != nil {
			return err
		}
		kept := []string{}
		for _, issue := range run.RecoveryIssues {
			if issue != "EXTERNAL_ACCOUNT_DIFFERENCE" && !strings.HasPrefix(issue, "EXTERNAL_POSITION_DIFFERENCE:") && !strings.HasPrefix(issue, "EXTERNAL_FILL_UNALLOCATED:") {
				kept = append(kept, issue)
			}
		}
		run.RecoveryIssues = kept
		for _, result := range results {
			local := run.Orders.Value(result.order.OrderID)
			if local == nil || result.order.ClientOrderID != local.ClientOrderID || result.order.Request.InstrumentKey != local.Request.InstrumentKey {
				return problem("VENUE_ORDER_ID_CONFLICT", 423)
			}
			id := result.order.OrderID
			if result.order.ExternalOrderID != nil {
				id = *result.order.ExternalOrderID
			}
			local.ExternalOrderID = &id
		}
		for _, observed := range snapshot.Positions.Values() {
			if local := run.Positions.Value(observed.InstrumentKey.Code()); local != nil && local.PositionCycleID == "" && local.Quantity.Cmp(observed.Quantity) == 0 {
				local.PositionCycleID = observed.PositionCycleID
			}
		}
		for _, p := range protections {
			prior := run.Protections.Value(p.ProtectionID)
			if prior == nil {
				return problem("VENUE_PROTECTION_ID_CONFLICT", 423)
			}
			oldExit := prior.ExitOrderID
			run.Protections.Set(p.ProtectionID, p)
			if p.ExitOrderID != nil {
				hash := sha256.Sum256([]byte(p.InstrumentKey.Code() + *p.ExitOrderID))
				id := "exit-" + hex.EncodeToString(hash[:])[:28]
				if run.Orders.Value(id) == nil {
					position := run.Positions.Value(p.InstrumentKey.Code())
					if position == nil || position.Quantity.Sign() == 0 {
						continue
					}
					side := "BUY"
					if p.PositionDirection == "LONG" {
						side = "SELL"
					}
					created := run.Clock
					if p.VerifiedAt != nil {
						created = *p.VerifiedAt
					}
					source := p.ProtectionID
					run.Orders.Set(id, &d.Order{OrderID: id, ClientOrderID: id, ExternalOrderID: p.ExitOrderID, SourceProtectionID: &source, CreatedAt: created, State: "ACKNOWLEDGED", Request: d.OrderRequest{OwnerID: position.OwnerID, InstrumentKey: p.InstrumentKey, Side: side, OrderType: "MARKET", Quantity: p.Plan.CoveredQuantity, ReduceOnly: true, SpecVersion: p.Plan.SpecVersion, TimeInForce: "GTC", PositionSide: "BOTH"}})
				}
				if oldExit == nil {
					for _, order := range run.Orders.Values() {
						if order.Request.InstrumentKey == p.InstrumentKey && !order.Request.ReduceOnly && !d.Terminal(order.State) {
							order.State = "CANCEL_PENDING"
							commandID := d.StableID("stop-cancel-", p.ProtectionID+":"+order.OrderID)
							found := false
							for _, task := range run.Outbox {
								if task.CommandID == commandID {
									found = true
								}
							}
							if !found {
								run.Outbox = append(run.Outbox, d.OutboxItem{CommandID: commandID, Kind: "CANCEL", OrderID: order.OrderID, PrincipalID: "protection-exit", ExpiresAt: run.Clock, ExecutorEpoch: run.ExecutorEpoch, State: "PENDING"})
							}
						}
					}
					code := p.InstrumentKey.Code()
					run.OwnerEpochs.Set(code, run.OwnerEpochs.Value(code)+1)
					if target := run.Targets.Value(code); target != nil {
						target.State = "BLOCKED"
						target.Reasons = []string{"PROTECTION_EXIT_REVIEW_REQUIRED"}
					}
				}
			}
		}
		for _, result := range results {
			for i := range result.fills {
				fills = append(fills, &result.fills[i])
			}
		}
		sort.SliceStable(fills, func(i, j int) bool {
			if fills[i].HappenedAt.Equal(fills[j].HappenedAt) {
				return fills[i].ExternalFillID < fills[j].ExternalFillID
			}
			return fills[i].HappenedAt.Before(fills[j].HappenedAt)
		})
		changed := map[string]bool{}
		for _, fill := range fills {
			var local *d.Order
			for _, o := range run.Orders.Values() {
				if o.ExternalOrderID != nil && *o.ExternalOrderID == fill.ExternalOrderID && o.Request.InstrumentKey == fill.InstrumentKey {
					local = o
					break
				}
			}
			if local == nil {
				covered := false
				for _, fact := range run.ExternalFacts.Values() {
					covered = covered || (fact.InstrumentKey == fill.InstrumentKey && d.Has(fact.ExternalFillIDs, fill.ExternalFillID))
				}
				if !covered {
					run.RecoveryIssues = append(run.RecoveryIssues, "EXTERNAL_FILL_UNALLOCATED:"+fill.InstrumentKey.Code()+":"+fill.ExternalFillID)
				}
				continue
			}
			at := fill.HappenedAt
			if fill.ReceivedAt.After(at) {
				at = fill.ReceivedAt
			}
			if err := AdvanceLiveClock(e, at); err != nil {
				return err
			}
			if e.ApplyFill(local.OrderID, *fill) {
				changed[local.OrderID] = true
			}
		}
		for _, id := range run.Orders.Keys() {
			if changed[id] {
				e.ProtectActualPosition(run.Orders.Value(id))
			}
		}
		for _, result := range results {
			local := run.Orders.Value(result.order.OrderID)
			confirmed, err := MergeOrder(e, local, result.order, nil)
			if err != nil {
				return err
			}
			if !confirmed {
				run.RecoveryIssues = append(run.RecoveryIssues, "UNKNOWN_ORDER")
			}
			for i := range run.Outbox {
				item := &run.Outbox[i]
				if item.OrderID == local.OrderID && (item.Kind == "SUBMIT" || d.Terminal(local.State)) && local.State != "UNKNOWN" {
					item.State = "DONE"
				}
			}
		}
		for _, income := range incomes {
			e.ApplyIncome(*income)
		}
		remote := map[string]*d.Position{}
		codes := append([]string{}, run.Positions.Keys()...)
		for _, p := range positions {
			remote[p.InstrumentKey.Code()] = p
			if !d.Has(codes, p.InstrumentKey.Code()) {
				codes = append(codes, p.InstrumentKey.Code())
			}
		}
		sort.Strings(codes)
		for _, code := range codes {
			localQuantity, remoteQuantity := zero, zero
			if p := run.Positions.Value(code); p != nil {
				localQuantity = p.Quantity
			}
			if p := remote[code]; p != nil {
				remoteQuantity = p.Quantity
			}
			if localQuantity.Cmp(remoteQuantity) != 0 {
				issue := "EXTERNAL_POSITION_DIFFERENCE:" + code
				run.RecoveryIssues = append(run.RecoveryIssues, issue)
				if target := run.Targets.Value(code); target != nil {
					target.State = "BLOCKED"
					target.Reasons = []string{issue}
				}
			}
		}
		if e.Math.Abs(e.Math.Sub(e.Equity(), equity)).Cmp(tolerance) > 0 || e.Math.Abs(e.Math.Sub(run.Cash, cash)).Cmp(tolerance) > 0 {
			run.RecoveryIssues = append(run.RecoveryIssues, "EXTERNAL_ACCOUNT_DIFFERENCE")
		}
		for code, facts := range conditionalFacts {
			spec := run.Specs.Value(code)
			pos := run.Positions.Value(code)
			needsCleanup := false
			foreign := false
			for _, fact := range facts {
				if !d.OwnConditional(run, fact) {
					needsCleanup = true
					foreign = true
					run.Alerts = append(run.Alerts, d.Alert("UNRECOGNIZED_CONDITIONAL", run.Clock, "external_id", fact.ExternalID))
					continue
				}
				if pos == nil || pos.Quantity.Sign() == 0 || fact.LocalProtectionID == nil {
					needsCleanup = true
					continue
				}
				local := run.Protections.Value(*fact.LocalProtectionID)
				if local == nil || !d.CurrentProtection(local, pos) || local.State == "CLOSED" {
					needsCleanup = true
				}
			}
			if needsCleanup {
				state := "PENDING"
				if foreign {
					state = "FOREIGN"
				}
				run.ConditionalCleanups.Set(code, &d.ConditionalCleanup{Instrument: spec.Key, State: state, Facts: facts})
			} else if old := run.ConditionalCleanups.Value(code); old != nil {
				old.Facts = facts
				old.State = "CONFIRMED"
			}
		}
		for _, code := range run.Positions.Keys() {
			position := run.Positions.Value(code)
			if position.Quantity.Sign() == 0 {
				continue
			}
			if remotePos := remote[code]; remotePos != nil && remotePos.Quantity.Cmp(position.Quantity) == 0 {
				e.ProtectActualPosition(&d.Order{Request: d.OrderRequest{InstrumentKey: position.InstrumentKey, ReduceOnly: true}})
			}
			e.ReconcileStrategyMode(position.InstrumentKey)
		}
		if recovery || len(run.RecoveryIssues) > 0 {
			if err := Reconcile(e); err != nil {
				return err
			}
		} else {
			e.AssessLossGates()
		}
		if err := e.Err(); err != nil {
			return err
		}
		run.Version++
		if recovery || (run.State == "RECOVERY_CHECK" && len(run.RecoveryIssues) == 0) {
			if len(run.RecoveryIssues) == 0 {
				version := run.Version
				run.VenueReconciledVersion = &version
			} else {
				run.VenueReconciledVersion = nil
			}
		} else if len(run.RecoveryIssues) > 0 {
			run.VenueReconciledVersion = nil
		}
		if len(run.RecoveryIssues) == 0 {
			cursor := snapshot.Clock
			if at.Before(cursor) {
				cursor = at
			}
			run.VenueFactsCursorAt = &cursor
		}
		action := "VENUE_FACTS_CONFIRMED"
		if recovery {
			action = "VENUE_RECOVERY_REPORT"
		}
		Audit(run, action, d.Principal{PrincipalID: "venue-reader", Environment: key.Environment, AccountID: key.AccountID, Permissions: []string{}}, "venue-"+formatInt(run.Version), Response{"issues": run.RecoveryIssues, "orders_with_new_fills": len(changed), "snapshot_version": snapshot.Version})
		issues = append([]string{}, run.RecoveryIssues...)
		return nil
	})
	return issues, err
}
func Recover(ctx context.Context, store ports.Store, key d.RunKey, broker ports.Broker, tolerance decimal.Value) ([]string, error) {
	if err := store.Transaction(ctx, key, func(run *d.Aggregate) error {
		run.State = "RECOVERY_CHECK"
		run.VenueReconciledVersion = nil
		run.Version++
		return nil
	}); err != nil {
		return nil, err
	}
	return Synchronize(ctx, store, key, broker, tolerance, true)
}

func venueAmount(raw any) (decimal.Value, error) {
	switch v := raw.(type) {
	case decimal.Value:
		return v, nil
	case string:
		return decimal.Parse(v)
	case json.Number:
		return decimal.Parse(string(v))
	}
	return decimal.Value{}, problem("VENUE_ACCOUNT_FORMAT_INVALID", 503)
}
