"""Read venue facts outside SQL, then atomically reconcile a versioned snapshot."""
from decimal import Decimal
from hashlib import sha256

from factorforge.trading.application.commands import audit
from factorforge.trading.application.execution_facts import advance_live_clock, merge_order
from factorforge.trading.application.reconcile import reconcile
from factorforge.trading.domain.accounting import apply_fill, apply_income, equity
from factorforge.trading.domain.errors import TradingError
from factorforge.trading.domain.models import Order, OrderRequest, Principal, TERMINAL, ZERO
from factorforge.trading.domain.protection import protect_actual_position
from factorforge.trading.domain.risk import assess_loss_gates


def synchronize(store, key, broker, tolerance, recovery=False):
    if not isinstance(tolerance, Decimal) or not tolerance.is_finite() or tolerance < 0:
        raise TradingError("RECONCILIATION_TOLERANCE_INVALID")
    snapshot = store.read(key)
    results = [broker.query_order(snapshot, local.client_order_id) for local in snapshot.orders.values()
               if local.state not in TERMINAL and (local.external_order_id or local.state in {"UNKNOWN", "DISPATCHING"}
                   or any(i.order_id == local.order_id and i.state in {"DISPATCHING", "UNKNOWN"} for i in snapshot.outbox))]
    protections = [broker.query_protection(snapshot, p.protection_id)
                   for p in snapshot.protections.values() if p.state not in {"CLOSED", "PENDING"}]
    known = snapshot.model_copy(deep=True)
    for protection in protections:
        known.protections[protection.protection_id] = protection
    broker.list_open_orders(known)  # rejects orders unrelated to an intent/known protection exit
    fills = broker.list_fills(snapshot)
    income = broker.get_income(snapshot)
    positions = broker.get_positions(snapshot)
    account = broker.get_account(snapshot)
    with store.transaction(key) as run:
        if run.version != snapshot.version:
            raise TradingError("VENUE_SNAPSHOT_CHANGED", 409)
        advance_live_clock(run, account["observed_at"])
        # Recompute discrepancies; explicit ownership acknowledgment stays pending.
        run.recovery_issues = [i for i in run.recovery_issues if i != "EXTERNAL_ACCOUNT_DIFFERENCE"
            and not i.startswith(("EXTERNAL_POSITION_DIFFERENCE:", "EXTERNAL_FILL_UNALLOCATED:"))]
        for result, _ in results:
            local = run.orders[result.order_id]
            if result.client_order_id != local.client_order_id or result.request.instrument_key != local.request.instrument_key:
                raise TradingError("VENUE_ORDER_ID_CONFLICT", 423)
            local.external_order_id = result.external_order_id or result.order_id
        for protection in protections:
            prior = run.protections[protection.protection_id]
            run.protections[protection.protection_id] = protection
            if protection.exit_order_id:
                identity = "exit-" + sha256((protection.instrument_key.code() + protection.exit_order_id).encode()).hexdigest()[:28]
                if identity not in run.orders:
                    position = run.positions.get(protection.instrument_key.code())
                    if position is None or not position.quantity:
                        continue
                    run.orders[identity] = Order(order_id=identity, client_order_id=identity,
                        external_order_id=protection.exit_order_id, source_protection_id=protection.protection_id,
                        created_at=protection.verified_at or run.clock, state="ACKNOWLEDGED",
                        request=OrderRequest(owner_id=position.owner_id, instrument_key=protection.instrument_key,
                            side="SELL" if position.quantity > 0 else "BUY", order_type="MARKET",
                            quantity=protection.plan.covered_quantity, reduce_only=True, spec_version=protection.plan.spec_version))
                if prior.exit_order_id is None:
                    code = protection.instrument_key.code()
                    run.owner_epochs[code] = run.owner_epochs.get(code, 0) + 1
                    if code in run.targets:
                        run.targets[code].state, run.targets[code].reasons = "BLOCKED", ["PROTECTION_EXIT_REVIEW_REQUIRED"]
        changed_orders = set()
        all_fills = fills + [f for _, items in results for f in items]
        for fill in sorted(all_fills, key=lambda f: (f.happened_at, f.external_fill_id)):
            local = next((o for o in run.orders.values() if o.external_order_id == fill.external_order_id
                and o.request.instrument_key == fill.instrument_key), None)
            if local is None:
                run.recovery_issues.append("EXTERNAL_FILL_UNALLOCATED:" + fill.instrument_key.code() + ":" + fill.external_fill_id)
                continue
            advance_live_clock(run, max(fill.happened_at, fill.received_at))
            if apply_fill(run, local.order_id, fill):
                changed_orders.add(local.order_id)
        for order_id in changed_orders:
            protect_actual_position(run, run.orders[order_id])
        for result, _ in results:
            local = run.orders[result.order_id]
            if not merge_order(run, local, result, []):
                run.recovery_issues.append("UNKNOWN_ORDER")
            for item in run.outbox:
                if item.order_id == local.order_id and (item.kind == "SUBMIT" or local.state in TERMINAL) and local.state != "UNKNOWN":
                    item.state = "DONE"
        for item in income:
            apply_income(run, item)
        remote = {p.instrument_key.code(): p for p in positions}
        for code in set(run.positions) | set(remote):
            local_quantity = run.positions[code].quantity if code in run.positions else ZERO
            remote_quantity = remote[code].quantity if code in remote else ZERO
            if local_quantity != remote_quantity:
                issue = "EXTERNAL_POSITION_DIFFERENCE:" + code
                run.recovery_issues.append(issue)
                if code in run.targets:
                    run.targets[code].state, run.targets[code].reasons = "BLOCKED", [issue]
        if abs(equity(run) - account["equity"]) > tolerance or abs(run.cash - account["cash"]) > tolerance:
            run.recovery_issues.append("EXTERNAL_ACCOUNT_DIFFERENCE")
        for code, position in run.positions.items():
            if not position.quantity:
                continue
            covered = any(p.instrument_key.code() == code and p.state == "ACTIVE_VERIFIED"
                and p.plan.covered_quantity == abs(position.quantity) for p in run.protections.values())
            if covered:
                position.protection_state = "ACTIVE_VERIFIED"
            elif not any(p.instrument_key.code() == code and p.state == "PENDING" for p in run.protections.values()):
                position.protection_state = "UNPROTECTED"
                run.state = "DEGRADED"
                run.alerts.append({"at": run.clock.isoformat(), "code": "PROTECTION_NOT_VERIFIED:" + code})
        if recovery or run.recovery_issues:
            reconcile(run)
        else:
            assess_loss_gates(run)
        run.version += 1
        if recovery:
            run.venue_reconciled_version = run.version if not run.recovery_issues else None
        elif run.state == "RECOVERY_CHECK" and not run.recovery_issues:
            run.venue_reconciled_version = run.version
        elif run.recovery_issues:
            run.venue_reconciled_version = None
        if not run.recovery_issues:
            # Queries are sequential, not an atomic venue snapshot. Re-read
            # the overlap on the next poll so facts appearing between the
            # fill/income query and the final account query cannot be skipped.
            run.venue_facts_cursor_at = min(snapshot.clock, account["observed_at"])
        audit(run, "VENUE_RECOVERY_REPORT" if recovery else "VENUE_FACTS_CONFIRMED",
            Principal(principal_id="venue-reader", environment=key.environment, account_id=key.account_id, permissions=set()),
            "venue-" + str(run.version), {"issues": run.recovery_issues, "orders_with_new_fills": len(changed_orders), "snapshot_version": snapshot.version})
        return list(run.recovery_issues)
