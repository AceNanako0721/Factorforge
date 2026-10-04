"""Configuration is private; each process is bound to exactly one environment."""
import argparse
from decimal import Decimal
from pathlib import Path
import time
import tomllib

from factorforge.trading.adapters.postgres.store import PostgresStore, initialize
from factorforge.trading.adapters.sim.broker import SimBroker
from factorforge.trading.api.app import create_app
from factorforge.trading.application.commands import TradingService, audit
from factorforge.trading.domain.errors import TradingError
from factorforge.trading.domain.models import Principal, RunKey
from factorforge.trading.workers.executor import ExecutionWorker


def load_config(path):
    try:
        with Path(path).open("rb") as stream:
            config = tomllib.load(stream)
        environment = config["runtime"]["environment"]
        if environment not in {"SIM", "LIVE"}:
            raise TradingError("ENVIRONMENT_UNSUPPORTED")
        if environment == "SIM" and config["trading"]["allow_live"]:
            raise TradingError("LIVE_CAPABILITIES_UNVERIFIED", 423)
        if environment == "LIVE" and not config["trading"].get("live_readiness"):
            raise TradingError("LIVE_CAPABILITIES_UNVERIFIED", 423)
        if config["trading"]["adapter"] != ("mock" if environment == "SIM" else "binance"):
            raise TradingError("EXECUTION_ADAPTER_UNVERIFIED", 423)
        if not config["services"]["database_url"] or not config["credentials"]["trading_api_token"]:
            raise TradingError("PRIVATE_CONFIGURATION_REQUIRED", 503)
        if not config["trading"]["account_id"] or not config["trading"]["principal_id"]:
            raise TradingError("ACCOUNT_BINDING_REQUIRED", 503)
        if any(config["credentials"].get(name) for name in ("exchange_api_key", "exchange_api_secret")):
            raise TradingError("EXECUTION_CREDENTIALS_IN_API_PROFILE", 403)
        return config
    except (OSError, KeyError, ValueError):
        raise TradingError("PRIVATE_CONFIGURATION_INVALID", 503) from None


def assemble(config, recover=False):
    environment = config["runtime"]["environment"]
    store = PostgresStore(config["services"]["database_url"], environment)
    store.verify_runtime_role()
    principal = Principal(principal_id=config["trading"]["principal_id"], environment=environment,
                          account_id=config["trading"]["account_id"], permissions=set(config["trading"]["permissions"]))
    health = None
    if config["trading"].get("runtime_health", {}).get("storage_path"):
        import httpx
        from datetime import datetime, timezone
        from factorforge.trading.adapters.health import OperationalProbe
        settings = config["trading"]["runtime_health"]
        def clock_offset():
            try:
                response = httpx.get(settings["clock_probe_url"], timeout=settings["timeout_seconds"],
                                     trust_env=False, follow_redirects=False)
                response.raise_for_status()
                return (datetime.now(timezone.utc).timestamp() * 1000 - response.json()["serverTime"]) / 1000
            except (httpx.HTTPError, ValueError, KeyError):
                raise TradingError("CLOCK_PROBE_UNAVAILABLE", 503) from None
        health = OperationalProbe(settings["storage_path"], clock_offset)
    service = TradingService(store, SimBroker() if environment == "SIM" else None, health)
    tokens = {config["credentials"]["trading_api_token"]: principal}
    if recover:
        bound = store.bound_run(principal.account_id)
        if bound:
            with store.transaction(bound) as run:
                run.state = "RECOVERY_CHECK"
                run.venue_reconciled_version = None
                run.version += 1
                audit(run, "PROCESS_RESTART", principal, "startup", {"state": run.state})
    return store, service, tokens


def _arguments():
    parser = argparse.ArgumentParser()
    parser.add_argument("--config", default="config/config.toml")
    return parser


def api_main():
    parser = _arguments()
    parser.add_argument("--host", default="127.0.0.1")
    parser.add_argument("--port", type=int, default=8000)
    parser.add_argument("--initialize-db", action="store_true")
    args = parser.parse_args()
    try:
        config = load_config(args.config)
        if args.initialize_db:
            initialize(config["services"]["database_url"], config["runtime"]["environment"])
            print("Schema initialized; configure the isolated runtime role before starting")
            return
        _, service, tokens = assemble(config, recover=True)
        import uvicorn
        uvicorn.run(create_app(service, tokens), host=args.host, port=args.port, access_log=False)
    except TradingError as error:
        raise SystemExit(error.code) from None


def worker_main():
    parser = _arguments()
    parser.add_argument("--run-id", required=True)
    parser.add_argument("--once", action="store_true")
    parser.add_argument("--executor-id")
    args = parser.parse_args()
    try:
        config = load_config(args.config)
        if config["runtime"]["environment"] != "SIM":
            raise TradingError("SIM_EXECUTOR_ENVIRONMENT_REQUIRED", 403)
        store, service, _ = assemble(config)
        key = RunKey(environment="SIM", account_id=config["trading"]["account_id"], run_id=args.run_id)
        from datetime import datetime, timezone
        from factorforge.trading.domain.lease import acquire
        clock = lambda: datetime.now(timezone.utc)
        epoch = None
        if store.read(key).policy.operational:
            if not args.executor_id:
                raise TradingError("EXECUTOR_FENCE_REQUIRED", 423)
            with store.transaction(key) as run:
                epoch = acquire(run, args.executor_id, clock(), run.policy.operational.lease_seconds)
                run.version += 1
        worker = ExecutionWorker(store, SimBroker(), args.executor_id if epoch else None, epoch, clock, service.health)
        while True:
            if epoch:
                with store.transaction(key) as run:
                    acquire(run, args.executor_id, clock(), run.policy.operational.lease_seconds)
            worker.tick(key)
            if args.once:
                break
            time.sleep(0.25)
    except TradingError as error:
        raise SystemExit(error.code) from None


def live_main():
    parser = _arguments()
    parser.add_argument("--run-id", required=True)
    parser.add_argument("--executor-id")
    parser.add_argument("--probe-only", action="store_true")
    parser.add_argument("--initialize-run", action="store_true")
    parser.add_argument("--recover-only", action="store_true")
    parser.add_argument("--once", action="store_true")
    args = parser.parse_args()
    try:
        with Path(args.config).open("rb") as stream:
            config = tomllib.load(stream)
        if config["runtime"]["environment"] != "LIVE" or config["trading"]["adapter"] != "binance":
            raise TradingError("LIVE_CONFIGURATION_NOT_ISOLATED", 423)
        store = PostgresStore(config["services"]["execution_database_url"], "LIVE")
        store.verify_runtime_role()
        key = RunKey(environment="LIVE", account_id=config["trading"]["account_id"], run_id=args.run_id)
        from datetime import datetime, timezone
        import httpx
        from factorforge.trading.domain.readiness import LiveReadiness
        from factorforge.trading.domain.lease import acquire, assert_lease
        from factorforge.trading.adapters.binance.broker import SignedTransport, BinanceBroker
        from factorforge.trading.adapters.binance.probes import account_probe
        from factorforge.trading.adapters.health import OperationalProbe
        clock = lambda: datetime.now(timezone.utc)
        endpoint = config["services"]["exchange_api_url"]
        if not endpoint.startswith("https://"):
            raise TradingError("LIVE_ENDPOINT_UNVERIFIED", 423)
        readiness = None
        if not (args.probe_only or args.initialize_run or args.recover_only):
            if not config["trading"]["allow_live"] or not args.executor_id:
                raise TradingError("LIVE_CAPABILITIES_UNVERIFIED", 423)
            readiness = LiveReadiness.model_validate(config["trading"]["live_readiness"])
            run = store.read(key)
            readiness.require(run, clock(), endpoint)
        # Secret-loading occurs only in this isolated execution process. API
        # commands cannot change readiness, the target endpoint, or this holder.
        credentials = config["credentials"]
        secrets = lambda: (credentials["exchange_api_key"], credentials["exchange_api_secret"])
        transport_policy = config["trading"]["signed_transport"]
        if readiness is not None and (transport_policy["priority_request_reserve"] <= 0
                or transport_policy["timeout_seconds"] <= 0 or transport_policy["poll_seconds"] <= 0):
            raise TradingError("EXECUTION_CAPACITY_POLICY_REQUIRED", 423)
        with httpx.Client(base_url=endpoint, timeout=transport_policy["timeout_seconds"],
            trust_env=False, follow_redirects=False) as client:
            transport = SignedTransport(client, secrets, clock, transport_policy["recv_window_ms"], None,
                transport_policy["request_budget"], transport_policy["budget_window_seconds"], transport_policy["request_weights"],
                transport_policy["priority_request_reserve"])
            if args.probe_only:
                import json
                print(json.dumps(account_probe(transport)))
                return
            if args.initialize_run:
                from factorforge.trading.domain.models import Aggregate, AccountPolicy, SimConfig
                from factorforge.trading.domain.risk import risk_day
                findings = account_probe(transport)
                positions = transport.request("GET", "/fapi/v3/positionRisk", {})
                if findings["ordinary_open_count"] or findings["conditional_open_count"] or any(
                        Decimal(p["positionAmt"]) != 0 for p in positions):
                    raise TradingError("LIVE_INITIALIZATION_REQUIRES_FLAT_ACCOUNT", 423)
                policy = AccountPolicy.model_validate(config["trading"]["account_policy"])
                model = SimConfig.model_validate(config["trading"]["cost_model"])
                now = clock()
                raw = transport.request("GET", "/fapi/v3/account", {})
                currency = config["trading"]["account_currency"]
                asset = next((a for a in raw["assets"] if a["asset"] == currency), None)
                if asset is None or Decimal(asset["walletBalance"]) <= 0 or any(
                        Decimal(a["walletBalance"]) != 0 for a in raw["assets"] if a["asset"] != currency):
                    raise TradingError("LIVE_ACCOUNT_CURRENCY_UNVERIFIED", 423)
                cash = Decimal(asset["walletBalance"])
                run = Aggregate(run_key=key, execution_mode="LIVE", state="RECOVERY_CHECK", policy=policy, sim_config=model,
                    initial_cash=cash, currency=currency, cash=cash, clock=now, risk_day=risk_day(now, policy.risk_day_zone),
                    day_start_equity=cash, peak_equity=cash, facts_start_at=now)
                run.audit.append({"sequence": 1, "action": "VENUE_BALANCE_BOOTSTRAP", "principal_id": "execution-live",
                    "request_id": "initialize", "at": now.isoformat(), "detail": {"source": "binance-account-v3"}})
                store.create(run)
                print("LIVE facts initialized; reconciliation and separate resume permission required")
                return
            if args.recover_only:
                from factorforge.trading.application.recovery import recover_from_broker
                issues = recover_from_broker(store, key, BinanceBroker(transport),
                    Decimal(config["trading"]["reconciliation_tolerance"]))
                print("RECOVERY_CHECK: " + ("verified" if not issues else "unresolved"))
                return
            with store.transaction(key) as current:
                epoch = acquire(current, args.executor_id, clock(), current.policy.operational.lease_seconds)
                current.version += 1
            def fence():
                current = store.read(key)
                assert_lease(current, args.executor_id, epoch, clock())
                readiness.require(current, clock(), endpoint)
            transport.fence = fence
            def offset():
                try:
                    response = client.get("/fapi/v1/time")
                    response.raise_for_status()
                    return (clock().timestamp() * 1000 - response.json()["serverTime"]) / 1000
                except (httpx.HTTPError, ValueError, KeyError):
                    raise TradingError("CLOCK_PROBE_UNAVAILABLE", 503) from None
            health = OperationalProbe(config["trading"]["storage_path"], offset)
            broker = BinanceBroker(transport, {"ordinary_and_conditional_verified": True,
                "overlapping_protections": readiness.overlapping_protections,
                "atomic_protection_modify": readiness.atomic_protection_modify})
            broker.execution_admitted = True
            from factorforge.trading.workers.protection import ProtectionWorker
            from factorforge.trading.workers.feedback import FeedbackWorker
            protection = ProtectionWorker(store, broker, args.executor_id, epoch, clock)
            feedback = FeedbackWorker(store, broker, Decimal(config["trading"]["reconciliation_tolerance"]))
            executor = ExecutionWorker(store, broker, args.executor_id, epoch, clock, health)
            if args.once:
                protection.tick(key)
                if feedback.tick(key):
                    protection.tick(key)
                    executor.tick(key)
                return
            from factorforge.trading.workers.protection_pool import ProtectionPool
            with ProtectionPool(protection, key, transport_policy["poll_seconds"]) as pool:
                while True:
                    with store.transaction(key) as current:
                        acquire(current, args.executor_id, clock(), current.policy.operational.lease_seconds)
                    fence()
                    pool.check()
                    if feedback.tick(key):
                        pool.check()
                        executor.tick(key)
                    time.sleep(transport_policy["poll_seconds"])
    except (OSError, KeyError, ValueError, TradingError) as error:
        raise SystemExit(error.code if isinstance(error, TradingError) else "LIVE_CONFIGURATION_INVALID") from None
