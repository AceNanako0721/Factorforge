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
					if position.Quantity.Sign() > 0 {
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
		for _, code := range run.Positions.Keys() {
			position := run.Positions.Value(code)
			if position.Quantity.Sign() == 0 {
				continue
			}
			covered, pending := false, false
			for _, p := range run.Protections.Values() {
				if p.InstrumentKey.Code() == code {
					covered = covered || (p.State == "ACTIVE_VERIFIED" && p.Plan.CoveredQuantity.Cmp(position.Quantity.Abs()) == 0)
					pending = pending || p.State == "PENDING"
				}
			}
			if covered {
				position.ProtectionState = "ACTIVE_VERIFIED"
			} else if !pending {
				position.ProtectionState = "UNPROTECTED"
				run.State = "DEGRADED"
				run.Alerts = append(run.Alerts, d.Alert("PROTECTION_NOT_VERIFIED:"+code, run.Clock))
			}
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
