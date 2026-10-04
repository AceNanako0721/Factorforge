"""Explicit external position facts invalidate target ownership epochs."""
from factorforge.trading.domain.accounting import convert, credit_cash
from factorforge.trading.domain.errors import TradingError
from factorforge.trading.domain.models import Position, TERMINAL, OutboxItem


def import_external(run, fact):
    previous = run.external_facts.get(fact.external_id)
    if previous:
        if previous != fact:
            raise TradingError("EXTERNAL_FACT_ID_CONFLICT", 409)
        return False
    code = fact.instrument_key.code()
    if len(set(fact.external_fill_ids)) != len(fact.external_fill_ids):
        raise TradingError("EXTERNAL_FILL_REFERENCE_DUPLICATED", 409)
    referenced = set(fact.external_fill_ids)
    if any(f.instrument_key == fact.instrument_key and f.external_fill_id in referenced
           for f in run.fills.values()) or any(
        old.instrument_key == fact.instrument_key and referenced.intersection(old.external_fill_ids)
        for old in run.external_facts.values()
    ):
        raise TradingError("EXTERNAL_FILL_ALREADY_ACCOUNTED", 409)
    spec = run.specs.get(code)
    position = run.positions.get(code)
    actual = position.quantity if position else 0
    if (not spec or fact.rule_version != spec.version or actual != fact.before_quantity
            or fact.happened_at > fact.received_at or fact.received_at > run.clock):
        raise TradingError("EXTERNAL_FACT_NOT_RECONCILED", 423)
    if fact.after_quantity and fact.average_entry is None:
        raise TradingError("EXTERNAL_ENTRY_UNKNOWN", 423)
    if any(old.instrument_key == fact.instrument_key and old.happened_at > fact.happened_at
           for old in run.external_facts.values()):
        raise TradingError("EXTERNAL_FACT_OUT_OF_ORDER", 409)
    cash_delta = convert(run, fact.cash_delta, fact.currency)
    if fact.replacement_spec:
        if fact.kind != "INSTRUMENT_CHANGE" or fact.replacement_spec.key != fact.instrument_key:
            raise TradingError("INSTRUMENT_CHANGE_MISMATCH")
        if fact.replacement_spec.valid_from > run.clock:
            raise TradingError("SPEC_NOT_EFFECTIVE")
        run.rule_history.append(spec.model_copy(deep=True))
        run.specs[code] = fact.replacement_spec
    if not position:
        position = Position(instrument_key=fact.instrument_key, owner_id="UNALLOCATED")
    position.quantity, position.average_entry = fact.after_quantity, fact.average_entry if fact.after_quantity else None
    position.protection_state = "UNPROTECTED" if fact.after_quantity else "CLOSED"
    run.positions[code] = position
    credit_cash(run, fact.cash_delta, fact.currency)
    run.external_facts[fact.external_id] = fact
    run.owner_epochs[code] = run.owner_epochs.get(code, 0) + 1
    if code in run.targets:
        run.targets[code].state = "BLOCKED"
        run.targets[code].reasons = ["EXTERNAL_OWNER_EPOCH_CHANGED"]
    for protection in run.protections.values():
        if protection.instrument_key == fact.instrument_key:
            protection.state = "UNKNOWN" if fact.after_quantity else "CLOSED"
    for order in run.orders.values():
        if order.request.instrument_key == fact.instrument_key and order.state not in TERMINAL:
            order.state = "CANCEL_PENDING"
            run.outbox.append(OutboxItem(command_id="external-cancel-" + fact.external_id + "-" + str(len(run.outbox)),
                kind="CANCEL", order_id=order.order_id, principal_id="external-import", expires_at=run.clock,
                executor_epoch=run.executor_epoch))
    run.state = "RECOVERY_CHECK"
    run.venue_reconciled_version = None
    issue = "EXTERNAL_OWNERSHIP:" + code
    if issue not in run.recovery_issues:
        run.recovery_issues.append(issue)
    run.alerts.append({"at": run.clock.isoformat(), "code": issue, "external_id": fact.external_id})
    return True
