"""Network I/O occurs only after the account transaction has committed."""

from factorforge.trading.application.commands import audit
from factorforge.trading.application.execution_facts import merge_order
from factorforge.trading.domain.errors import AmbiguousResult, TradingError
from factorforge.trading.domain.models import Principal, TERMINAL, ZERO
from factorforge.trading.domain.risk import authorize_order


class ExecutionWorker:
    def __init__(self, store, broker, executor_id=None, lease_epoch=None, wall_clock=None, health=None):
        if store.environment != broker.environment:
            raise TradingError("EXECUTOR_ENVIRONMENT_MISMATCH", 403)
        if broker.environment == "LIVE" and not getattr(broker, "execution_admitted", False):
            raise TradingError("LIVE_CAPABILITIES_UNVERIFIED", 423)
        if broker.environment == "LIVE" and (not executor_id or lease_epoch is None or wall_clock is None):
            raise TradingError("EXECUTOR_FENCE_REQUIRED", 423)
        self.store, self.broker = store, broker
        self.executor_id, self.lease_epoch, self.wall_clock, self.health = executor_id, lease_epoch, wall_clock, health

    def tick(self, key):
        # Claim atomically. A restart sees DISPATCHING and queries the original ID.
        selected = None
        with self.store.transaction(key) as run:
            if run.lease_holder and not self.executor_id:
                raise TradingError("EXECUTOR_FENCE_REQUIRED", 423)
            if self.executor_id:
                from factorforge.trading.domain.lease import assert_lease
                assert_lease(run, self.executor_id, self.lease_epoch, self.wall_clock())
            if self.health:
                run.health_issues, run.health_checked_at = self.health.check(run), run.clock
            from factorforge.trading.application.targets import advance_targets
            from factorforge.trading.application.emergency import apply_breach_action
            before = len(run.outbox)
            apply_breach_action(run)
            advance_targets(run)
            if len(run.outbox) != before:
                run.version += 1
                audit(run, "AUTOMATIC_RECONCILIATION", Principal(principal_id="execution-sim", environment=key.environment,
                    account_id=key.account_id, permissions=set()), "worker", {"commands_added": len(run.outbox) - before})
            pending = [i for i in run.outbox if i.state != "DONE"]
            pending.sort(key=lambda i: (0 if i.kind == "CANCEL" else 1 if run.orders[i.order_id].request.reduce_only else 2))
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
            item.claimed_by = self.executor_id
            selected = (run.model_copy(deep=True), item.model_copy(deep=True), order.model_copy(deep=True), previous)
            run.version += 1
        snapshot, item, order, previous = selected
        if self.executor_id:
            # Re-read committed state immediately before outbound; a signed broker
            # also uses an independent fence at the transport boundary.
            from factorforge.trading.domain.lease import assert_lease
            assert_lease(self.store.read(key), self.executor_id, self.lease_epoch, self.wall_clock())
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
            if not merge_order(run, local, result, fills):
                target.state, local.state, run.state = "UNKNOWN", "UNKNOWN", "DEGRADED"
                run.version += 1
                return True
            target.state = "DONE"
            run.version += 1
            executor = Principal(principal_id="executor", environment=key.environment, account_id=key.account_id, permissions=set())
            audit(run, "EXECUTION_CONFIRMED", executor, item.command_id, {"order_id": local.order_id, "state": local.state})
        return True

    def _unknown(self, key, command_id):
        with self.store.transaction(key) as run:
            item = next(i for i in run.outbox if i.command_id == command_id)
            item.state = "UNKNOWN"
            run.orders[item.order_id].state = "UNKNOWN"
            run.state = "DEGRADED"
            run.version += 1
