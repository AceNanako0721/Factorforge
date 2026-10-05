package domain

import "strconv"

func (e *Engine) ImportExternal(fact ExternalFact) bool {
	if !e.validate(fact) {
		return false
	}
	run := e.Run
	if old := run.ExternalFacts.Value(fact.ExternalID); old != nil {
		if !sameExternal(*old, fact) {
			e.Fail("EXTERNAL_FACT_ID_CONFLICT", 409)
		}
		return false
	}
	seen := map[string]bool{}
	for _, id := range fact.ExternalFillIDs {
		if seen[id] {
			e.Fail("EXTERNAL_FILL_REFERENCE_CONFLICT", 409)
			return false
		}
		seen[id] = true
	}
	for _, fill := range run.Fills.Values() {
		if fill.InstrumentKey == fact.InstrumentKey && seen[fill.ExternalFillID] {
			e.Fail("EXTERNAL_FILL_ALREADY_ACCOUNTED", 409)
			return false
		}
	}
	for _, old := range run.ExternalFacts.Values() {
		if old.InstrumentKey == fact.InstrumentKey {
			for _, id := range old.ExternalFillIDs {
				if seen[id] {
					e.Fail("EXTERNAL_FILL_ALREADY_ACCOUNTED", 409)
					return false
				}
			}
		}
	}
	code := fact.InstrumentKey.Code()
	position := run.Positions.Value(code)
	actual := zero
	if position != nil {
		actual = position.Quantity
	}
	spec := run.Specs.Value(code)
	if spec == nil || fact.RuleVersion != spec.Version || actual.Cmp(fact.BeforeQuantity) != 0 || fact.HappenedAt.After(fact.ReceivedAt) || fact.ReceivedAt.After(run.Clock) {
		e.Fail("EXTERNAL_FACT_NOT_RECONCILED", 423)
		return false
	}
	if fact.AfterQuantity.Sign() != 0 && fact.AverageEntry == nil {
		e.Fail("EXTERNAL_ENTRY_UNKNOWN", 423)
		return false
	}
	for _, old := range run.ExternalFacts.Values() {
		if old.InstrumentKey == fact.InstrumentKey && old.HappenedAt.After(fact.HappenedAt) {
			e.Fail("EXTERNAL_FACT_OUT_OF_ORDER", 409)
			return false
		}
	}
	e.Convert(fact.CashDelta, fact.Currency)
	if fact.ReplacementSpec != nil {
		if fact.Kind != "INSTRUMENT_CHANGE" || fact.ReplacementSpec.Key != fact.InstrumentKey {
			e.Fail("INSTRUMENT_CHANGE_RULE_MISMATCH")
			return false
		}
		if fact.ReplacementSpec.ValidFrom.After(run.Clock) {
			e.Fail("INSTRUMENT_CHANGE_NOT_AVAILABLE", 423)
			return false
		}
		run.RuleHistory = append(run.RuleHistory, *spec)
		run.Specs.Set(code, fact.ReplacementSpec)
	}
	if position == nil {
		position = &Position{InstrumentKey: fact.InstrumentKey, OwnerID: "UNALLOCATED", ProtectionState: "CLOSED"}
	}
	position.Quantity = fact.AfterQuantity
	position.AverageEntry = nil
	if fact.AfterQuantity.Sign() != 0 {
		position.AverageEntry = fact.AverageEntry
		position.ProtectionState = "UNPROTECTED"
	} else {
		position.ProtectionState = "CLOSED"
	}
	run.Positions.Set(code, position)
	e.CreditCash(fact.CashDelta, fact.Currency)
	run.ExternalFacts.Set(fact.ExternalID, &fact)
	run.OwnerEpochs.Set(code, run.OwnerEpochs.Value(code)+1)
	for _, target := range run.Targets.Values() {
		if target.InstrumentKey == fact.InstrumentKey {
			target.State = "BLOCKED"
			target.Reasons = []string{"EXTERNAL_OWNER_EPOCH_CHANGED"}
		}
	}
	for _, p := range run.Protections.Values() {
		if p.InstrumentKey == fact.InstrumentKey {
			p.State = "CLOSED"
			if fact.AfterQuantity.Sign() != 0 {
				p.State = "UNKNOWN"
			}
		}
	}
	for _, order := range run.Orders.Values() {
		if order.Request.InstrumentKey == fact.InstrumentKey && !Terminal(order.State) {
			order.State = "CANCEL_PENDING"
			run.Outbox = append(run.Outbox, OutboxItem{CommandID: "external-cancel-" + fact.ExternalID + "-" + strconv.Itoa(len(run.Outbox)), Kind: "CANCEL", OrderID: order.OrderID, PrincipalID: "external-import", ExpiresAt: run.Clock, ExecutorEpoch: run.ExecutorEpoch, State: "PENDING"})
		}
	}
	run.State = "RECOVERY_CHECK"
	run.VenueReconciledVersion = nil
	issue := "EXTERNAL_OWNERSHIP:" + code
	if !Has(run.RecoveryIssues, issue) {
		run.RecoveryIssues = append(run.RecoveryIssues, issue)
	}
	run.Alerts = append(run.Alerts, Alert(issue, run.Clock, "external_id", fact.ExternalID))
	return e.Err() == nil
}
func sameExternal(a, b ExternalFact) bool {
	if a.BeforeQuantity.Cmp(b.BeforeQuantity) != 0 || a.AfterQuantity.Cmp(b.AfterQuantity) != 0 || a.CashDelta.Cmp(b.CashDelta) != 0 {
		return false
	}
	a.BeforeQuantity = b.BeforeQuantity
	a.AfterQuantity = b.AfterQuantity
	a.CashDelta = b.CashDelta
	if (a.AverageEntry == nil) != (b.AverageEntry == nil) {
		return false
	}
	if a.AverageEntry != nil {
		if a.AverageEntry.Cmp(*b.AverageEntry) != 0 {
			return false
		}
		a.AverageEntry = b.AverageEntry
	}
	return sameJSON(a, b)
}
