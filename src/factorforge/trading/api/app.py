"""Every route uses this layer's application service and bound authentication."""
import hmac
from typing import Annotated

from fastapi import Depends, FastAPI, Header, Query, Request
from fastapi.exceptions import RequestValidationError
from fastapi.responses import JSONResponse
from fastapi.security import HTTPBearer, HTTPAuthorizationCredentials

from factorforge.trading.application.commands import TradingService, wire
from factorforge.trading.application.replay import advance
from factorforge.trading.application.targets import set_target
from factorforge.trading.domain.accounting import account_view
from factorforge.trading.domain.errors import TradingError
from factorforge.trading.domain.models import Command, RunKey, InstrumentKey, UTC
from factorforge.trading.api.dto import (CreateRun, SubmitOrder, RegisterSpec, SetProtection, RecordIncome,
                                       AdvanceReplay, TargetRequest, ImportExternal, RegisterFx, ResolveExternal, FenceExecutor, MarketSnapshot)
from factorforge.trading.api.views import (Receipt, RunView, OrderView, AccountView, PositionView, ProtectionView,
                                          TargetView, Page, Problem, AcceptedOrder, RunReceipt, order_view)
from factorforge.trading.api.views import AlertView, AuditView, OperationalView
from factorforge.trading.api.views import OwnerBindingView
from factorforge.trading.domain.models import InstrumentSpec, MarketPoint, MarketTrade, Candle, Fill, Income, ExternalFact, Target

PREFIX = "/api/v2/trading"


def envelope(items, run):
    return wire({"items": items, "cursor": None, "snapshot_version": run.version})


def create_app(service: TradingService, tokens: dict):
    errors = {status: {"model": Problem} for status in (401, 403, 404, 409, 422, 423, 503)}
    app = FastAPI(title="Factorforge Trading", version="trading-2.0", docs_url=None, redoc_url=None, responses=errors)

    @app.exception_handler(TradingError)
    async def trading_error(request: Request, error: TradingError):
        principal = getattr(request.state, "principal", None)
        if principal:
            try:
                service.record_rejection(principal, error.code, request.method)
            except TradingError:
                pass  # original failure is returned; no cached execution is enabled
        return JSONResponse(status_code=error.status, content={"code": error.code, "message": error.code,
                            "field": error.field, "correlation_id": request.headers.get("x-request-id"), "retryable": False})

    @app.exception_handler(RequestValidationError)
    async def invalid_request(request: Request, error: RequestValidationError):
        principal = getattr(request.state, "principal", None)
        if principal:
            try:
                service.record_rejection(principal, "INVALID_REQUEST", request.method)
            except TradingError:
                pass
        # Validation input can contain credentials. Never echo request bodies in errors.
        return JSONResponse(status_code=422, content={"code": "INVALID_REQUEST", "message": "INVALID_REQUEST",
                            "field": None, "correlation_id": request.headers.get("x-request-id"), "retryable": False})

    def authenticate(request: Request, credentials: Annotated[HTTPAuthorizationCredentials | None, Depends(HTTPBearer(auto_error=False))]):
        if not credentials:
            raise TradingError("AUTHENTICATION_REQUIRED", 401)
        supplied = credentials.credentials
        for expected, principal in tokens.items():
            if expected and hmac.compare_digest(supplied, expected):
                request.state.principal = principal
                return principal
        raise TradingError("AUTHENTICATION_REQUIRED", 401)

    Identity = Depends(authenticate)

    def key(environment: str, account_id: str, run_id: str):
        return RunKey(environment=environment, account_id=account_id, run_id=run_id)

    @app.get(PREFIX + "/health")
    def health():
        return {"schema_version": "trading-2.0", "environment": service.store.environment,
                "live_ready": False, "upper_layers_required": False}

    @app.post(PREFIX + "/runs", status_code=201, response_model=RunView)
    def create(body: CreateRun, principal=Identity):
        return wire(service.create_run(principal, body))

    @app.get(PREFIX + "/runs/{run_id}", response_model=RunView)
    def get_run(run_id: str, environment: str, account_id: str, principal=Identity):
        return service.run_view(service.read(principal, key(environment, account_id, run_id)))

    @app.post(PREFIX + "/orders", status_code=202, response_model=AcceptedOrder)
    def submit(body: SubmitOrder, principal=Identity):
        return wire(service.submit_order(principal, body, body.order))

    @app.get(PREFIX + "/orders", response_model=Page[OrderView])
    def orders(environment: str, account_id: str, run_id: str, principal=Identity):
        run = service.read(principal, key(environment, account_id, run_id))
        return envelope([order_view(order) for order in run.orders.values()], run)

    @app.get(PREFIX + "/orders/{order_id}", response_model=OrderView)
    def order(order_id: str, environment: str, account_id: str, run_id: str, principal=Identity):
        run = service.read(principal, key(environment, account_id, run_id))
        if order_id not in run.orders:
            raise TradingError("ORDER_NOT_FOUND", 404)
        return wire(order_view(run.orders[order_id]))

    @app.post(PREFIX + "/orders/{order_id}/cancel", status_code=202, response_model=Receipt)
    def cancel(order_id: str, body: Command, principal=Identity):
        return wire(service.cancel_order(principal, body, order_id))

    @app.post(PREFIX + "/instruments", status_code=201, response_model=Receipt)
    def instrument(body: RegisterSpec, principal=Identity):
        return wire(service.register_spec(principal, body, body.spec))

    @app.get(PREFIX + "/instruments", response_model=Page[InstrumentSpec])
    def instruments(environment: str, account_id: str, run_id: str, principal=Identity):
        run = service.read(principal, key(environment, account_id, run_id))
        return envelope(list(run.specs.values()), run)

    @app.get(PREFIX + "/positions", response_model=Page[PositionView])
    def positions(environment: str, account_id: str, run_id: str, principal=Identity):
        run = service.read(principal, key(environment, account_id, run_id))
        views = [PositionView(instrument_key=p.instrument_key, owner_id=p.owner_id, quantity=p.quantity,
                              average_entry=p.average_entry, protection_state=p.protection_state,
                              reconciliation_state=run.state, observed_at=run.clock) for p in run.positions.values()]
        return envelope(views, run)

    @app.get(PREFIX + "/owners", response_model=Page[OwnerBindingView])
    def owners(environment: str, account_id: str, run_id: str, principal=Identity):
        run = service.read(principal, key(environment, account_id, run_id))
        return envelope([OwnerBindingView(instrument_key=spec.key, owner_id=run.owners.get(code),
            owner_epoch=run.owner_epochs.get(code, 0)) for code,spec in run.specs.items()],run)

    @app.get(PREFIX + "/protections", response_model=Page[ProtectionView])
    def protections(environment: str, account_id: str, run_id: str, principal=Identity):
        run = service.read(principal, key(environment, account_id, run_id))
        return envelope(list(run.protections.values()),run)

    @app.get(PREFIX + "/account", response_model=AccountView)
    def account(environment: str, account_id: str, run_id: str, principal=Identity):
        return wire(account_view(service.read(principal, key(environment, account_id, run_id))))

    @app.get(PREFIX + "/market/points", response_model=Page[MarketPoint])
    def points(environment: str, account_id: str, run_id: str, principal=Identity):
        run = service.read(principal, key(environment, account_id, run_id))
        points = [p.model_copy(deep=True) for p in run.points.values()]
        for point in points:
            if (run.clock - point.observed_at).total_seconds() > run.policy.max_market_age_seconds:
                point.quality = "STALE"
        return envelope(points, run)

    @app.get(PREFIX + "/market/candles", response_model=Page[Candle])
    def candles(environment: str, account_id: str, run_id: str, venue: str, instrument_id: str,
                interval: str, start: UTC, end: UTC, principal=Identity):
        run = service.read(principal, key(environment, account_id, run_id))
        requested = InstrumentKey(venue=venue, product="LINEAR_PERPETUAL", instrument_id=instrument_id)
        return envelope([c for c in run.candles if c.instrument_key == requested and c.interval == interval
                         and c.final and c.available_at <= run.clock and start <= c.open_at and c.close_at <= end], run)

    @app.post(PREFIX + "/protections", status_code=201, response_model=Receipt)
    def protection(body: SetProtection, principal=Identity):
        return wire(service.maintain_protection(principal, body, body.instrument_key, body.plan))

    @app.get(PREFIX + "/protections/{protection_id}", response_model=ProtectionView)
    def get_protection(protection_id: str, environment: str, account_id: str, run_id: str, principal=Identity):
        run = service.read(principal, key(environment, account_id, run_id))
        if protection_id not in run.protections:
            raise TradingError("PROTECTION_NOT_FOUND", 404)
        return wire(run.protections[protection_id])

    @app.post(PREFIX + "/protections/{protection_id}/replace", status_code=202, response_model=Receipt)
    def replace(protection_id: str, body: SetProtection, principal=Identity):
        return wire(service.maintain_protection(principal, body, body.instrument_key, body.plan, protection_id))

    @app.post(PREFIX + "/protections/{protection_id}/cancel", status_code=202, response_model=Receipt)
    def cancel_stop(protection_id: str, body: Command, principal=Identity):
        return wire(service.cancel_protection(principal, body, protection_id))

    @app.put(PREFIX + "/owners/{owner}/targets/{instrument}", status_code=202, response_model=TargetView)
    def target(owner: str, instrument: str, body: TargetRequest, principal=Identity):
        if owner != body.owner_id or instrument != body.instrument_key.instrument_id:
            raise TradingError("TARGET_PATH_MISMATCH")
        return wire(set_target(service, principal, body))

    @app.post(PREFIX + "/runs/{run_id}/{action}", status_code=202, response_model=RunReceipt)
    def run_action(run_id: str, action: str, body: Command, principal=Identity):
        if run_id != body.run_key.run_id or action not in {"reconcile", "stop", "resume"}:
            raise TradingError("RUN_ACTION_UNSUPPORTED")
        return wire(service.run_action(principal, body, action))

    @app.post(PREFIX + "/simulation/frames", status_code=202, response_model=Receipt)
    def frame(body: AdvanceReplay, principal=Identity):
        return wire(advance(service, principal, body, body.frame))

    @app.post(PREFIX + "/income", status_code=202, response_model=Receipt)
    def income(body: RecordIncome, principal=Identity):
        return wire(service.record_income(principal, body, body.income))

    @app.post(PREFIX + "/external-facts", status_code=202, response_model=Receipt)
    def external_import(body: ImportExternal, principal=Identity):
        return wire(service.import_external(principal, body, body.fact))

    @app.post(PREFIX + "/external-facts/resolve", status_code=202, response_model=Receipt)
    def external_resolve(body: ResolveExternal, principal=Identity):
        return wire(service.resolve_external(principal, body, body))

    @app.post(PREFIX + "/fx", status_code=201, response_model=Receipt)
    def fx(body: RegisterFx, principal=Identity):
        return wire(service.register_fx(principal, body, body.rate))

    @app.post(PREFIX + "/market/snapshots", status_code=202, response_model=Receipt)
    def snapshot(body: MarketSnapshot, principal=Identity):
        from factorforge.trading.application.market import ingest_snapshot
        return wire(ingest_snapshot(service, principal, body))

    @app.get(PREFIX + "/market/trades", response_model=Page[MarketTrade])
    def market_trades(environment: str, account_id: str, run_id: str, venue: str,
                      instrument_id: str, principal=Identity):
        run = service.read(principal, key(environment, account_id, run_id))
        requested = InstrumentKey(venue=venue, product="LINEAR_PERPETUAL", instrument_id=instrument_id)
        return envelope([t for t in run.market_trades.values() if t.instrument_key == requested], run)

    @app.post(PREFIX + "/executors/fence", status_code=202, response_model=Receipt)
    def executor_fence(body: FenceExecutor, principal=Identity):
        return wire(service.fence_executor(principal, body, body))

    @app.get(PREFIX + "/fills", response_model=Page[Fill])
    def fills(environment: str, account_id: str, run_id: str, principal=Identity):
        run = service.read(principal, key(environment, account_id, run_id))
        return envelope(list(run.fills.values()), run)

    @app.get(PREFIX + "/income", response_model=Page[Income])
    def income_list(environment: str, account_id: str, run_id: str, principal=Identity):
        run = service.read(principal, key(environment, account_id, run_id))
        return envelope(list(run.incomes.values()), run)

    @app.get(PREFIX + "/external-facts", response_model=Page[ExternalFact])
    def external_list(environment: str, account_id: str, run_id: str, principal=Identity):
        run = service.read(principal, key(environment, account_id, run_id))
        return envelope(list(run.external_facts.values()), run)

    @app.get(PREFIX + "/targets", response_model=Page[Target])
    def targets(environment: str, account_id: str, run_id: str, principal=Identity):
        run = service.read(principal, key(environment, account_id, run_id))
        return envelope(list(run.targets.values()), run)

    @app.get(PREFIX + "/alerts", response_model=Page[AlertView])
    def alerts(environment: str, account_id: str, run_id: str, principal=Identity):
        run = service.read(principal, key(environment, account_id, run_id))
        return envelope(run.alerts, run)

    @app.get(PREFIX + "/audit", response_model=Page[AuditView])
    def audit_events(environment: str, account_id: str, run_id: str, principal=Identity):
        run = service.read(principal, key(environment, account_id, run_id))
        return envelope(run.audit, run)

    @app.get(PREFIX + "/operational-health", response_model=OperationalView)
    def operational_health(environment: str, account_id: str, run_id: str, principal=Identity):
        run = service.read(principal, key(environment, account_id, run_id))
        return wire({"issues": run.health_issues, "checked_at": run.health_checked_at,
            "executor_epoch": run.executor_epoch, "lease_epoch": run.lease_epoch, "lease_until": run.lease_until})

    return app
