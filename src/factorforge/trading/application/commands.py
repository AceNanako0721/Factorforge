"""Account-locked commands commit intent, reservations, audit and outbox together."""
from contextlib import contextmanager
from decimal import Decimal
from hashlib import sha256
import json

from factorforge.trading.domain.accounting import account_view, equity, apply_income
from factorforge.trading.domain.errors import TradingError
from factorforge.trading.domain.models import Aggregate, Order, OutboxItem, Protection, TERMINAL, ZERO
from factorforge.trading.domain.risk import authorize_order, assess_loss_gates, risk_day
from factorforge.trading.application.queries import order_view_data


def wire(value):
    if hasattr(value, "model_dump"):
        return wire(value.model_dump(mode="python"))
    if isinstance(value, Decimal):
        return str(value)
    if hasattr(value, "isoformat"):
        return value.isoformat().replace("+00:00", "Z")
    if isinstance(value, dict):
        return {k: wire(v) for k, v in value.items()}
    if isinstance(value, set):
        return sorted(wire(v) for v in value)
    if isinstance(value, (list, tuple)):
        return [wire(v) for v in value]
    return value


def request_hash(value):
    return sha256(json.dumps(wire(value), sort_keys=True, separators=(",", ":")).encode()).hexdigest()


def resource_id(run, prefix, identity):
    return prefix + "-" + request_hash({"run": run.run_key, "version": run.version,
                                       "clock": run.clock, "seed": run.sim_config.seed, "identity": identity})[:28]


def require(principal, key, permission):
    if principal.environment != key.environment or principal.account_id != key.account_id:
        raise TradingError("ACCOUNT_SCOPE_FORBIDDEN", 403)
    if permission not in principal.permissions:
        raise TradingError("PERMISSION_FORBIDDEN", 403)


def audit(run, action, principal, request_id, detail):
    run.audit.append({"sequence": len(run.audit) + 1, "action": action,
                      "principal_id": principal.principal_id, "request_id": request_id,
                      "at": wire(run.clock), "detail": wire(detail)})


class TradingService:
    def __init__(self, store, simulator=None):
        self.store, self.simulator = store, simulator

    def create_run(self, principal, body):
        require(principal, body.run_key, "run:create")
        if body.run_key.environment != "SIM":
            raise TradingError("LIVE_CAPABILITIES_UNVERIFIED", 423)
        if body.expires_at_utc < body.clock:
            raise TradingError("REQUEST_EXPIRED", 409)
        if body.account_policy.valid_from > body.clock:
            raise TradingError("POLICY_NOT_EFFECTIVE")
        if (body.account_policy.breach_action != "KEEP_PROTECTION" and any(g.mode == "ENFORCE" for g in
                (body.account_policy.daily_loss, body.account_policy.drawdown, body.account_policy.consecutive_loss))):
            raise TradingError("BREACH_ACTION_CAPABILITY_UNVERIFIED", 423)
        # Check persisted creation identity before recreating an account on retry.
        identity = principal.principal_id + ":" + body.idempotency_key
        fingerprint = request_hash(body)
        try:
            old = self.store.read(body.run_key)
        except TradingError as error:
            if error.code != "RUN_NOT_FOUND":
                raise
        else:
            saved = old.dedup.get(identity)
            if saved and saved["hash"] == fingerprint:
                return saved["response"]
            raise TradingError("CREATE_IDEMPOTENCY_CONFLICT", 409)
        run = Aggregate(run_key=body.run_key, execution_mode=body.execution_mode,
                        policy=body.account_policy, sim_config=body.sim_config,
                        initial_cash=body.initial_cash, currency=body.currency, cash=body.initial_cash, clock=body.clock,
                        risk_day=risk_day(body.clock, body.account_policy.risk_day_zone),
                        day_start_equity=body.initial_cash, peak_equity=body.initial_cash)
        response = self.run_view(run)
        run.dedup[identity] = {"hash": fingerprint, "response": response}
        audit(run, "RUN_CREATED", principal, body.request_id, response)
        self.store.create(run)
        return response

    @contextmanager
    def command(self, principal, command, action, payload, permission):
        require(principal, command.run_key, permission)
        with self.store.transaction(command.run_key) as run:
            identity = principal.principal_id + ":" + command.idempotency_key
            fingerprint = request_hash({"action": action, "command": command, "payload": payload})
            prior = run.dedup.get(identity)
            if prior:
                if prior["hash"] != fingerprint:
                    raise TradingError("IDEMPOTENCY_CONFLICT", 409)
                yield run, prior["response"], True
                return
            if command.expected_version != run.version:
                raise TradingError("VERSION_CONFLICT", 409)
            if command.expires_at_utc < run.clock:
                raise TradingError("REQUEST_EXPIRED", 409)
            response = {}
            yield run, response, False
            run.version += 1
            response.update(aggregate_version=run.version, correlation_id=command.request_id,
                            accepted_at_utc=wire(run.clock), reason_codes=[])
            audit(run, action, principal, command.request_id, response)
            canonical_response = wire(response)
            response.clear()
            response.update(canonical_response)
            run.dedup[identity] = {"hash": fingerprint, "response": canonical_response}

    def submit_order(self, principal, command, request, target_version=None):
        with self.command(principal, command, "ORDER_SUBMIT", request, "order:write") as (run, response, repeated):
            if not repeated:
                reservation = authorize_order(run, request)
                order_id = resource_id(run, "ord", principal.principal_id + ":" + command.idempotency_key)
                order = Order(order_id=order_id, client_order_id="ff-" + sha256(order_id.encode()).hexdigest()[:28],
                              request=request, state="RESERVED", reserved_notional=reservation,
                              created_at=run.clock, target_version=target_version)
                run.owners[request.instrument_key.code()] = request.owner_id
                run.orders[order_id] = order
                run.outbox.append(OutboxItem(command_id=resource_id(run, "cmd", order_id + ":submit"), kind="SUBMIT", order_id=order_id,
                                            principal_id=principal.principal_id, expires_at=command.expires_at_utc,
                                            executor_epoch=run.executor_epoch))
                response.update(resource_id=order_id, state=order.state)
                response.update(order_view_data(order))
        return response

    def cancel_order(self, principal, command, order_id):
        with self.command(principal, command, "ORDER_CANCEL", {"order_id": order_id}, "order:write") as (run, response, repeated):
            if not repeated:
                order = run.orders.get(order_id)
                if not order:
                    raise TradingError("ORDER_NOT_FOUND", 404)
                if order.state not in TERMINAL:
                    order.state = "CANCEL_PENDING"
                    run.outbox.append(OutboxItem(command_id=resource_id(run, "cmd", order_id + ":cancel"), kind="CANCEL", order_id=order_id,
                                                principal_id=principal.principal_id, expires_at=command.expires_at_utc,
                                                executor_epoch=run.executor_epoch))
                response.update(resource_id=order_id, state=order.state)
        return response

    def register_spec(self, principal, command, spec):
        with self.command(principal, command, "SPEC_REGISTER", spec, "market:write") as (run, response, repeated):
            if not repeated:
                if spec.valid_from > run.clock:
                    raise TradingError("SPEC_NOT_EFFECTIVE")
                if spec.settlement_currency != run.currency or spec.quote_currency != run.currency:
                    raise TradingError("ACCOUNT_CURRENCY_CAPABILITY_UNVERIFIED", 423)
                code = spec.key.code()
                existing = run.specs.get(code)
                if existing and existing.contract_multiplier != spec.contract_multiplier and (
                    (code in run.positions and run.positions[code].quantity) or any(o.request.instrument_key == spec.key
                        and o.state not in TERMINAL for o in run.orders.values())):
                    raise TradingError("ACCOUNTING_CHANGE_REQUIRES_SETTLEMENT", 423)
                if existing and existing.version == spec.version and existing != spec:
                    raise TradingError("SPEC_VERSION_CONFLICT", 409)
                if existing and existing.version != spec.version and any(
                    o.request.instrument_key == spec.key and o.state not in TERMINAL for o in run.orders.values()
                ):
                    run.state = "RECOVERY_CHECK"
                    run.recovery_issues.append("RULES_CHANGED_WITH_OPEN_ORDERS")
                run.specs[code] = spec
                response.update(resource_id=code, state="REGISTERED")
        return response

    def maintain_protection(self, principal, command, key, plan, replace_id=None):
        from factorforge.trading.domain.protection import verify_protection
        with self.command(principal, command, "PROTECTION_SET", {"key": key, "plan": plan, "replace_id": replace_id}, "protection:write") as (run, response, repeated):
            if not repeated:
                protection = verify_protection(run, key, plan, replace_id)
                response.update(resource_id=protection.protection_id, state=protection.state)
        return response

    def cancel_protection(self, principal, command, protection_id):
        with self.command(principal, command, "PROTECTION_CANCEL", {"id": protection_id}, "protection:write") as (run, response, repeated):
            if not repeated:
                protection = run.protections.get(protection_id)
                if not protection:
                    raise TradingError("PROTECTION_NOT_FOUND", 404)
                position = run.positions.get(protection.instrument_key.code())
                alternatives = [p for p in run.protections.values() if p.protection_id != protection_id
                                and p.instrument_key == protection.instrument_key and p.state == "ACTIVE_VERIFIED"
                                and position and p.plan.covered_quantity >= abs(position.quantity)]
                if position and position.quantity and not alternatives:
                    raise TradingError("PROTECTION_STILL_REQUIRED", 423)
                protection.state = "CLOSED"
                response.update(resource_id=protection_id, state=protection.state)
        return response

    def record_income(self, principal, command, income):
        with self.command(principal, command, "INCOME_IMPORT", income, "income:write") as (run, response, repeated):
            if not repeated:
                apply_income(run, income)
                assess_loss_gates(run)
                response.update(resource_id=income.external_id, state="RECORDED")
        return response

    def run_action(self, principal, command, action):
        from factorforge.trading.application.reconcile import reconcile
        with self.command(principal, command, "RUN_" + action.upper(), {}, "run:" + action) as (run, response, repeated):
            if not repeated:
                if action == "stop":
                    run.state = "STOPPED"
                    for order in run.orders.values():
                        if not order.request.reduce_only and order.state not in TERMINAL:
                            order.state = "CANCEL_PENDING"
                            run.outbox.append(OutboxItem(command_id=resource_id(run, "cmd", order.order_id + ":stop-cancel"),
                                                        kind="CANCEL", order_id=order.order_id,
                                                        principal_id=principal.principal_id, expires_at=command.expires_at_utc,
                                                        executor_epoch=run.executor_epoch))
                elif action == "reconcile":
                    reconcile(run)
                elif action == "resume":
                    if run.recovery_issues or run.risk_locks or any(o.state == "UNKNOWN" for o in run.orders.values()):
                        raise TradingError("RECOVERY_NOT_VERIFIED", 423)
                    if any(p.quantity and p.protection_state != "ACTIVE_VERIFIED" for p in run.positions.values()):
                        raise TradingError("POSITION_UNPROTECTED", 423)
                    if run.state != "RECOVERY_CHECK":
                        raise TradingError("RECONCILE_REQUIRED", 423)
                    run.state = "NORMAL"
                else:
                    raise TradingError("RUN_ACTION_UNSUPPORTED")
                response.update(self.run_view(run))
        return response

    @staticmethod
    def run_view(run):
        return wire({"resource_id": run.run_key.run_id, "run_key": run.run_key,
                     "aggregate_version": run.version, "execution_mode": run.execution_mode,
                     "state": run.state, "policy_version": run.policy.version,
                     "executor_epoch": run.executor_epoch, "active_owners": sorted(set(run.owners.values())),
                     "reasons": run.recovery_issues + run.risk_locks})

    def read(self, principal, key):
        require(principal, key, "read")
        return self.store.read(key)
