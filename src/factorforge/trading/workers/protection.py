"""A separate work pool confirms exchange protection before removing the old one."""
from factorforge.trading.domain.errors import TradingError, AmbiguousResult
from factorforge.trading.application.commands import audit
from factorforge.trading.domain.models import Principal


class ProtectionWorker:
    def __init__(self, store, broker, executor_id=None, lease_epoch=None, wall_clock=None):
        if store.environment != broker.environment:
            raise TradingError("EXECUTOR_ENVIRONMENT_MISMATCH", 403)
        self.store, self.broker = store, broker
        self.executor_id, self.lease_epoch, self.wall_clock = executor_id, lease_epoch, wall_clock
        if broker.environment == "LIVE" and (not executor_id or lease_epoch is None or wall_clock is None
                or not getattr(broker, "execution_admitted", False)):
            raise TradingError("EXECUTOR_FENCE_REQUIRED", 423)

    def _fence(self, run):
        if run.lease_holder and not self.executor_id:
            raise TradingError("EXECUTOR_FENCE_REQUIRED", 423)
        if self.executor_id:
            from factorforge.trading.domain.lease import assert_lease
            assert_lease(run, self.executor_id, self.lease_epoch, self.wall_clock())

    def _audit(self, run, action, item):
        audit(run, action, Principal(principal_id="protection-worker", environment=run.run_key.environment,
            account_id=run.run_key.account_id, permissions=set()), item.protection_id,
            {"protection_id": item.protection_id, "state": item.state})

    def _cancel(self, key, item):
        current = self.store.read(key)
        self._fence(current)
        position = current.positions.get(item.instrument_key.code())
        if position and position.quantity and not any(p.protection_id != item.protection_id
            and p.instrument_key == item.instrument_key and p.state == "ACTIVE_VERIFIED"
            and p.plan.covered_quantity == abs(position.quantity) for p in current.protections.values()):
            raise TradingError("PROTECTION_STILL_REQUIRED", 423)
        try:
            confirmed = self.broker.query_protection(current, item.protection_id)
            if confirmed.state != "CLOSED":
                self.broker.cancel_protection(current, item.protection_id)
                confirmed = self.broker.query_protection(current, item.protection_id)
            if confirmed.state != "CLOSED":
                raise AmbiguousResult()
        except (TradingError, AmbiguousResult, TimeoutError, ConnectionError):
            with self.store.transaction(key) as run:
                self._fence(run)
                run.protections[item.protection_id].state = "UNKNOWN"
                run.version += 1
                self._audit(run, "PROTECTION_CANCEL_UNKNOWN", run.protections[item.protection_id])
            return True
        with self.store.transaction(key) as run:
            self._fence(run)
            run.protections[item.protection_id] = confirmed
            run.version += 1
            self._audit(run, "PROTECTION_CANCEL_CONFIRMED", confirmed)
        return True

    def tick(self, key):
        chosen = None
        with self.store.transaction(key) as run:
            self._fence(run)
            candidates = sorted(run.protections.values(), key=lambda p: (
                0 if p.state == "PENDING" else 2 if p.cancel_requested else 1))
            for item in candidates:
                if item.state not in {"PENDING", "DISPATCHING", "UNKNOWN", "CANCEL_PENDING"}:
                    continue
                previous = item.state
                item.state = "DISPATCHING"
                chosen = run.model_copy(deep=True), item.model_copy(deep=True), previous
                run.version += 1
                self._audit(run, "PROTECTION_DISPATCH", item)
                break
        if chosen is None:
            return False
        run, item, previous = chosen
        if item.cancel_requested:
            return self._cancel(key, item)
        old = run.protections.get(item.replaces_id) if item.replaces_id else None
        try:
            capabilities = self.broker.get_capabilities()
            if previous in {"DISPATCHING", "UNKNOWN"}:
                confirmed = self.broker.query_protection(run, item.protection_id)
            elif old and not capabilities.get("overlapping_protections", False):
                if not capabilities.get("atomic_protection_modify", False) or not hasattr(self.broker, "replace_protection_atomic"):
                    raise TradingError("ATOMIC_PROTECTION_CAPABILITY_UNVERIFIED", 423)
                confirmed = self.broker.replace_protection_atomic(run, old, item)
            else:
                submitted = self.broker.submit_protection(run, item)
                run.protections[item.protection_id] = submitted
                confirmed = self.broker.query_protection(run, item.protection_id)
            if (confirmed.state != "ACTIVE_VERIFIED" or confirmed.plan != item.plan
                    or confirmed.instrument_key != item.instrument_key):
                raise AmbiguousResult()
        except (AmbiguousResult, TimeoutError, ConnectionError, TradingError) as error:
            with self.store.transaction(key) as current:
                self._fence(current)
                if previous == "PENDING" and isinstance(error, TradingError) and error.code == "VENUE_REQUEST_REJECTED":
                    # A definitive POST rejection created no physical order.
                    # Preserve confirmed old coverage instead of repeatedly
                    # querying a nonexistent replacement as UNKNOWN.
                    current.protections[item.protection_id].state = "CLOSED"
                    covered = any(p.instrument_key == item.instrument_key and p.state == "ACTIVE_VERIFIED"
                        and p.plan.covered_quantity == abs(current.positions[item.instrument_key.code()].quantity)
                        for p in current.protections.values())
                    current.positions[item.instrument_key.code()].protection_state = "ACTIVE_VERIFIED" if covered else "UNPROTECTED"
                    if not covered:
                        current.state = "DEGRADED"
                    current.alerts.append({"code": "PROTECTION_REJECTED", "id": item.protection_id,
                                          "at": current.clock.isoformat()})
                    current.version += 1
                    self._audit(current, "PROTECTION_REJECTED", current.protections[item.protection_id])
                    return True
                current.protections[item.protection_id].state = "UNKNOWN"
                current.positions[item.instrument_key.code()].protection_state = "UNKNOWN"
                current.state = "DEGRADED"
                current.alerts.append({"code": "PROTECTION_CONFIRMATION_REQUIRED", "id": item.protection_id,
                                      "at": current.clock.isoformat()})
                current.version += 1
                self._audit(current, "PROTECTION_UNKNOWN", current.protections[item.protection_id])
            return True
        with self.store.transaction(key) as current:
            self._fence(current)
            position = current.positions[item.instrument_key.code()]
            if abs(position.quantity) != confirmed.plan.covered_quantity:
                current.protections[item.protection_id].state = "UNKNOWN"
                position.protection_state = "UNPROTECTED"
                current.state = "DEGRADED"
                current.version += 1
                return True
            current.protections[item.protection_id] = confirmed
            position.protection_state = "ACTIVE_VERIFIED"
            current.version += 1
            self._audit(current, "PROTECTION_CONFIRMED", confirmed)
        # New coverage is persisted before an old conditional order can be canceled.
        if old and capabilities.get("overlapping_protections", False):
            try:
                self.broker.cancel_protection(self.store.read(key), old.protection_id)
                checked = self.broker.query_protection(self.store.read(key), old.protection_id)
                if checked.state != "CLOSED":
                    raise AmbiguousResult()
            except (AmbiguousResult, TradingError, TimeoutError, ConnectionError):
                with self.store.transaction(key) as current:
                    self._fence(current)
                    current.protections[old.protection_id].cancel_requested = True
                    current.protections[old.protection_id].state = "CANCEL_PENDING"
                    current.version += 1
                return True
            with self.store.transaction(key) as current:
                self._fence(current)
                current.protections[old.protection_id].state = "CLOSED"
                current.version += 1
                self._audit(current, "PROTECTION_REPLACED", current.protections[old.protection_id])
        elif old:
            with self.store.transaction(key) as current:
                self._fence(current)
                current.protections[old.protection_id].state = "CLOSED"
                current.version += 1
        return True
