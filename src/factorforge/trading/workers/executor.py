"""Network I/O occurs only after the account transaction has committed."""

from factorforge.trading.application.commands import audit
from factorforge.trading.domain.protection import protect_actual_position
from factorforge.trading.domain.accounting import apply_fill
from factorforge.trading.domain.errors import AmbiguousResult, TradingError
from factorforge.trading.domain.models import Principal, TERMINAL, ZERO
from factorforge.trading.domain.risk import authorize_order


class ExecutionWorker:
    def __init__(self, store, broker):
        if store.environment != broker.environment:
            raise TradingError("EXECUTOR_ENVIRONMENT_MISMATCH", 403)
        if broker.environment == "LIVE":
            raise TradingError("LIVE_CAPABILITIES_UNVERIFIED", 423)
        self.store, self.broker = store, broker

    def tick(self, key):
        # Claim atomically. A restart sees DISPATCHING and queries the original ID.
        selected = None
        with self.store.transaction(key) as run:
            pending = [i for i in run.outbox if i.state != "DONE"]
            pending.sort(key=lambda i: 0 if i.kind == "CANCEL" else 1)
            if not pending:
                return False
            item = pending[0]
            order = run.orders[item.order_id]
            previous = item.state
            if item.executor_epoch != run.executor_epoch:
                raise TradingError("EXECUTOR_EPOCH_MISMATCH", 423)
            if order.state in TERMINAL:
                item.state = "DONE"
                run.version += 1
                return True
            if item.kind == "SUBMIT" and order.state == "CANCEL_PENDING" and not order.external_order_id:
                order.state, order.reserved_notional, item.state = "CANCELED", ZERO, "DONE"
                run.version += 1
                return True
            if previous == "PENDING" and item.kind == "SUBMIT" and not order.request.reduce_only:
                if run.clock > item.expires_at or run.state != "NORMAL" or run.risk_locks:
                    order.state, order.reserved_notional, item.state = "REJECTED", ZERO, "DONE"
                    run.version += 1
                    return True
                # Recheck the latest rules/data/account without double-counting this reservation.
                check = run.model_copy(deep=True)
                del check.orders[order.order_id]
                try:
                    authorize_order(check, order.request)
                except TradingError:
                    order.state, order.reserved_notional, item.state = "REJECTED", ZERO, "DONE"
                    run.version += 1
                    return True
            item.state = "DISPATCHING"
            selected = (run.model_copy(deep=True), item.model_copy(deep=True), order.model_copy(deep=True), previous)
            run.version += 1
        snapshot, item, order, previous = selected
        try:
            if previous in {"DISPATCHING", "UNKNOWN"}:
                result, fills = self.broker.query_order(snapshot, order.client_order_id)
                # A canceled request still needs confirmation if a restart found it active.
                if item.kind == "CANCEL" and result.state not in TERMINAL:
                    result, extra = self.broker.cancel_order(snapshot, result)
                    fills.extend(extra)
            elif item.kind == "CANCEL":
                result, fills = self.broker.cancel_order(snapshot, order)
            else:
                result, fills = self.broker.submit_order(snapshot, order)
        except (AmbiguousResult, TimeoutError, ConnectionError):
            self._unknown(key, item.command_id)
            return True
        except TradingError as error:
            if previous in {"DISPATCHING", "UNKNOWN"} or error.code == "ORDER_NOT_FOUND":
                self._unknown(key, item.command_id)
                return True
            raise
        with self.store.transaction(key) as run:
            target = next(i for i in run.outbox if i.command_id == item.command_id)
            local = run.orders[item.order_id]
            if result.client_order_id != local.client_order_id or result.request.instrument_key != local.request.instrument_key:
                target.state, local.state, run.state = "UNKNOWN", "UNKNOWN", "DEGRADED"
                run.version += 1
                return True
            local.external_order_id = result.external_order_id or result.order_id
            for fill in fills:
                if apply_fill(run, local.order_id, fill):
                    protect_actual_position(run, local)
            if result.filled_quantity > local.filled_quantity or (result.state == "FILLED" and local.remaining):
                # A venue status without matching fill facts is insufficient for the ledger.
                target.state, local.state, run.state = "UNKNOWN", "UNKNOWN", "DEGRADED"
                run.version += 1
                return True
            # The terminal cumulative fill cannot be overwritten by a late cancel.
            if local.remaining == 0:
                local.state, local.reserved_notional = "FILLED", ZERO
            elif result.state in {"CANCELED", "REJECTED"}:
                local.state, local.reserved_notional = result.state, ZERO
            elif local.filled_quantity:
                local.state = "PARTIALLY_FILLED"
            else:
                local.state = result.state
            target.state = "DONE"
            run.version += 1
            executor = Principal(principal_id="execution-sim", environment="SIM", account_id=key.account_id, permissions=set())
            audit(run, "EXECUTION_CONFIRMED", executor, item.command_id, {"order_id": local.order_id, "state": local.state})
        return True

    def _unknown(self, key, command_id):
        with self.store.transaction(key) as run:
            item = next(i for i in run.outbox if i.command_id == command_id)
            item.state = "UNKNOWN"
            run.orders[item.order_id].state = "UNKNOWN"
            run.state = "DEGRADED"
            run.version += 1
