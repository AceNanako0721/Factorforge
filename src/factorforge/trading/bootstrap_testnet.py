"""Reproducible P1 acceptance using virtual funds and the production adapters.

This entrypoint is an EXPERIMENT_ONLY capability probe. It owns a fresh local
database, kernel-isolated signer/API processes, short-lived egress permits and
bounded testnet intents. It does not approve a production account or policy.
"""
import argparse
from datetime import datetime, timedelta, timezone
from decimal import Decimal, ROUND_CEILING, ROUND_FLOOR
import json
import os
from pathlib import Path
import secrets
import socket
import subprocess
import sys
import time

import httpx
import psycopg
from psycopg.conninfo import make_conninfo
from psycopg import sql

from factorforge.trading.adapters.binance.market import BinanceMarket
from factorforge.trading.adapters.binance.testnet import RemoteProbeBroker, signer_main
from factorforge.trading.adapters.isolation import RevocableEgress, TESTNET_URL, restrict_filesystem
from factorforge.trading.adapters.postgres.store import PostgresStore, initialize
from factorforge.trading.adapters.health import OperationalProbe
from factorforge.trading.api.app import create_app, PREFIX
from factorforge.trading.api.dto import MarketSnapshot, ResolveExternal, FenceExecutor
from factorforge.trading.application.commands import TradingService, wire, audit
from factorforge.trading.application.market import ingest_snapshot
from factorforge.trading.application.venue import synchronize
from factorforge.trading.domain.errors import TradingError
from factorforge.trading.domain.models import (AccountPolicy, Aggregate, Command, ExternalFact, LossGate,
    OperationalPolicy, OrderRequest, Principal, ProtectionPlan, RunKey, SimConfig, TERMINAL)
from factorforge.trading.domain.risk import risk_day
from factorforge.trading.workers.executor import ExecutionWorker
from factorforge.trading.workers.protection import ProtectionWorker


def now():
    return datetime.now(timezone.utc)


def private_json(path, value):
    descriptor = os.open(path, os.O_WRONLY | os.O_CREAT | os.O_TRUNC, 0o600)
    with os.fdopen(descriptor, "w") as stream:
        json.dump(wire(value), stream, indent=2)


def api_process(profile):
    # Import infrastructure before applying filesystem rules; subsequent file
    # access, subprocesses and descendants remain restricted by the kernel.
    import uvicorn
    settings = json.loads(Path(profile).read_text())
    store = PostgresStore(settings["dsn"], "LIVE")
    store.verify_runtime_role()
    principal = Principal.model_validate(settings["principal"])
    key = RunKey.model_validate(settings["key"])
    def clock_offset():
        sample = json.loads(Path(profile).with_name("clock.json").read_text())
        if (now() - datetime.fromisoformat(sample["at"])).total_seconds() > 120:
            raise TradingError("CLOCK_PROBE_UNAVAILABLE", 503)
        return sample["offset"]
    service = TradingService(store, health=OperationalProbe(Path(profile).parent, clock_offset))
    with store.transaction(key) as run:
        run.state, run.venue_reconciled_version = "RECOVERY_CHECK", None
        run.version += 1
        audit(run, "PROCESS_RESTART", principal, "testnet-api", {"state": run.state})
    restrict_filesystem(["/usr", "/lib", "/lib64", "/etc/ssl", "/etc/localtime", "/dev/null", "/dev/urandom",
        sys.prefix, Path(__file__).parents[2], profile], [Path(profile).parent])
    try:
        with open(settings["private_config"], "rb"):
            pass
    except PermissionError:
        pass
    else:
        raise SystemExit("PRIVATE_CONFIG_ISOLATION_FAILED")
    try:
        with open("/proc/" + str(settings["signer_pid"]) + "/environ", "rb"):
            pass
    except PermissionError:
        pass
    else:
        raise SystemExit("SIGNER_PROC_ISOLATION_FAILED")
    try:
        with psycopg.connect(make_conninfo(settings["dsn"], user=settings["admin_user"])):
            pass
    except psycopg.Error:
        pass
    else:
        raise SystemExit("DATABASE_ADMIN_ISOLATION_FAILED")
    try:
        with socket.create_connection(("192.0.2.1", 443), timeout=0.2):
            pass
    except OSError:
        pass
    else:
        raise SystemExit("API_DIRECT_NETWORK_ISOLATION_FAILED")
    print("PRIVATE_CONFIG_ACCESS_DENIED", flush=True)
    uvicorn.run(create_app(service, {settings["token"]: principal}), uds=settings["socket"],
        access_log=False, log_level="error")


class Acceptance:
    def __init__(self, root, config, symbol, max_notional):
        if max_notional <= 0 or max_notional > Decimal("100"):
            raise TradingError("TESTNET_NOTIONAL_LIMIT_INVALID")
        self.root, self.config, self.symbol, self.limit = root, config, symbol, max_notional
        self.directory = root / "runtime" / ("p1-" + now().strftime("%Y%m%dT%H%M%S"))
        self.directory.mkdir(mode=0o700, parents=True, exist_ok=False)
        self.report = {"scope": "EXPERIMENT_ONLY", "venue": "BINANCE_FUTURES_TESTNET",
            "production_approved": False, "started_at": now(), "checks": {}, "symbol": symbol,
            "max_notional": max_notional, "complete": False}
        self.broker, self.gateway, self.api, self.server = None, None, None, None
        self.err = (self.directory / "process.log").open("w")
        os.chmod(self.directory / "process.log", 0o600)

    def checked(self, name, condition=True):
        if not condition:
            raise TradingError("ACCEPTANCE_FAILED_" + name.upper(), 423)
        self.report["checks"][name] = {"passed": True, "at": now()}
        private_json(self.directory / "report.json", self.report)
        print("PASS " + name, flush=True)

    def command(self):
        identifier = "p1-" + secrets.token_hex(8)
        return Command(schema_version="trading-2.0", request_id=identifier, idempotency_key=identifier,
            run_key=self.key, expected_version=self.store.read(self.key).version, reason="EXPERIMENT_ONLY P1 acceptance",
            expires_at_utc=now() + timedelta(minutes=5))

    def refresh(self):
        points = self.market.latest_points(self.instrument)
        stamp = self.market._get("/fapi/v1/time")["serverTime"]
        sample = {"at": now(), "offset": (now().timestamp()*1000 - stamp)/1000}
        if hasattr(self, "api_folder"):
            private_json(self.api_folder / "clock.json", sample)
        command = self.command()
        ingest_snapshot(self.service, self.principal,
            MarketSnapshot(**command.model_dump(), at=max(now(), *(p.available_at for p in points)), points=points, trades=[], candles=[]))
        return {p.kind: p.value for p in points}

    def reconcile(self, resume=False):
        for _ in range(4):
            self.refresh()
            issues = synchronize(self.store, self.key, self.broker, Decimal("0.0000001"), recovery=True)
            if not issues:
                if resume:
                    self.service.run_action(self.principal, self.command(), "resume")
                return
            time.sleep(0.5)
        raise TradingError("TESTNET_RECONCILIATION_UNRESOLVED", 423)

    def new_signer(self, manual=False):
        token = self.gateway.issue()
        manifest = {"endpoint": TESTNET_URL, "config": str(self.config), "gateway": self.gateway.path,
            "token": token, "dsn": self.dsn, "key": self.key.model_dump(), "holder": "probe-" + secrets.token_hex(6),
            "expires_at": (now() + timedelta(minutes=45)).isoformat(), "symbol": self.symbol,
            "max_quantity": str(self.quantity), "max_notional": str(self.limit), "manual": manual,
            "manual_id": "ff-emergency-" + secrets.token_hex(8)}
        return RemoteProbeBroker(manifest, self.err), token, manifest

    def start_api(self):
        folder = self.directory / ("api-" + secrets.token_hex(3))
        folder.mkdir(mode=0o700)
        self.api_folder = folder
        stamp = self.market._get("/fapi/v1/time")["serverTime"]
        private_json(folder / "clock.json", {"at": now(), "offset": (now().timestamp()*1000-stamp)/1000})
        token = secrets.token_hex(32)
        settings = {"dsn": self.dsn, "key": self.key, "principal": self.principal, "token": token,
            "socket": str(folder / "api.sock"), "private_config": str(self.config),
            "admin_user": self.admin_user, "signer_pid": self.broker.process.pid}
        private_json(folder / "profile.json", settings)
        self.api = subprocess.Popen(["unshare", "--user", "--map-root-user", "--net", sys.executable,
            "-m", "factorforge.trading.bootstrap_testnet", "--api-profile", str(folder / "profile.json")],
            stdout=subprocess.PIPE, stderr=self.err, text=True)
        marker = self.api.stdout.readline().strip()
        if marker != "PRIVATE_CONFIG_ACCESS_DENIED":
            raise TradingError("API_FILESYSTEM_ISOLATION_FAILED", 503)
        self.http = httpx.Client(base_url="http://api.local", transport=httpx.HTTPTransport(uds=settings["socket"]),
            timeout=5, headers={"Authorization": "Bearer " + token})
        for _ in range(50):
            try:
                response = self.http.get(PREFIX + "/health")
                if response.status_code == 200:
                    self.checked("api_kernel_secret_denial")
                    self.checked("api_signer_proc_database_admin_and_direct_network_denial")
                    return
            except httpx.HTTPError:
                pass
            time.sleep(0.1)
        raise TradingError("API_STARTUP_FAILED", 503)

    def stop_api(self):
        if self.api:
            self.api.terminate()
            self.api.wait(timeout=10)
            self.api.stdout.close()
            self.http.close()
            self.api = None

    def setup(self):
        import pgserver
        self.server = pgserver.get_server(self.directory / "pgdata", cleanup_mode="stop")
        admin = self.server.get_uri()
        initialize(admin, "SIM")
        initialize(admin, "LIVE")
        with psycopg.connect(admin) as connection:
            connection.execute("CREATE ROLE p1_live LOGIN NOSUPERUSER NOCREATEDB NOCREATEROLE IN ROLE factorforge_live")
            connection.execute("CREATE ROLE p1_sim LOGIN NOSUPERUSER NOCREATEDB NOCREATEROLE IN ROLE factorforge_sim")
            self.admin_user = connection.execute("SELECT current_user").fetchone()[0]
            admin_password, live_password, sim_password = (secrets.token_hex(32) for _ in range(3))
            for role, password in [(self.admin_user, admin_password), ("p1_live", live_password), ("p1_sim", sim_password)]:
                connection.execute(sql.SQL("ALTER ROLE {} PASSWORD {}").format(sql.Identifier(role), sql.Literal(password)))
            # Credential-specific authentication prevents a sandboxed API from
            # changing the username on its connection string to a superuser.
            (self.directory / "pgdata" / "pg_hba.conf").write_text("local all all scram-sha-256\nhost all all 127.0.0.1/32 reject\nhost all all ::1/128 reject\n")
            connection.execute("SELECT pg_reload_conf()")
        admin = make_conninfo(admin, password=admin_password)
        self.admin = admin
        self.dsn = make_conninfo(admin, user="p1_live", password=live_password)
        private_json(self.directory / "database-profile.json", {"admin": admin, "live": self.dsn, "admin_user": self.admin_user})
        self.store = PostgresStore(self.dsn, "LIVE")
        self.store.verify_runtime_role()
        self.key = RunKey(environment="LIVE", account_id="p1-testnet", run_id=self.directory.name)
        self.principal = Principal(principal_id="testnet-operator", environment="LIVE", account_id=self.key.account_id,
            permissions={"read", "order:write", "market:write", "protection:write", "external:import", "external:resolve",
                         "executor:fence", "run:stop", "run:resume", "run:reconcile", "target:write"})
        self.market = BinanceMarket(TESTNET_URL, 15, {self.symbol: "1"}, 0)
        def clock_offset():
            stamp = self.market._get("/fapi/v1/time")["serverTime"]
            return (now().timestamp()*1000-stamp)/1000
        self.health = OperationalProbe(self.directory, clock_offset)
        self.service = TradingService(self.store, health=self.health)
        specs = self.market.instrument_specs()
        spec = next((s for s in specs if s.key.instrument_id == self.symbol), None)
        if not spec or spec.halted or spec.settlement_currency != "USDT":
            raise TradingError("TESTNET_PRODUCT_UNAVAILABLE", 423)
        self.instrument = spec.key
        quotes = {p.kind: p.value for p in self.market.latest_points(self.instrument)}
        # An explicit diagnostic assumption; these capabilities are exercised
        # below, never registered as approved production instrument records.
        spec.capabilities |= {"SHORT", "REDUCE_ONLY", "CONDITIONAL_PROTECTION"}
        self.spec = spec
        self.quantity = ((spec.min_notional / min(quotes["ASK"], quotes["MARK"]) / spec.quantity_step)
            .to_integral_value(rounding=ROUND_CEILING) + 1) * spec.quantity_step
        if self.quantity * max(quotes.values()) * 2 > self.limit:
            raise TradingError("TESTNET_MINIMUM_EXCEEDS_BUDGET", 423)
        self.report["quantity"] = self.quantity
        self.gateway = RevocableEgress(self.directory / "gate.sock")
        self.broker, self.token, self.manifest = self.new_signer()
        account = self.broker.call("raw", "/fapi/v3/account", {})
        configuration = self.broker.call("raw", "/fapi/v1/accountConfig", {})
        positions = self.broker.call("raw", "/fapi/v3/positionRisk", {})
        ordinary = self.broker.call("raw", "/fapi/v1/openOrders", {})
        conditional = self.broker.call("raw", "/fapi/v1/openAlgoOrders", {})
        if (configuration.get("canTrade") is not True or configuration.get("dualSidePosition") is not False
                or configuration.get("multiAssetsMargin") is not False or ordinary or conditional
                or any(Decimal(p["positionAmt"]) for p in positions)):
            raise TradingError("TESTNET_EXCLUSIVE_FLAT_ACCOUNT_REQUIRED", 423)
        collateral = next(a for a in account["assets"] if a["asset"] == "USDT")
        cash = Decimal(collateral["walletBalance"])
        if cash != Decimal(account["totalWalletBalance"]) or cash < self.limit:
            raise TradingError("TESTNET_COLLATERAL_SCOPE_UNVERIFIED", 423)
        policy = AccountPolicy(version="EXPERIMENT_ONLY", valid_from=now(), risk_day_zone="UTC", notional_limit=self.limit,
            margin_limit=self.limit, trade_loss_limit="10", daily_loss=LossGate(mode="ENFORCE", amount="20"),
            drawdown=LossGate(mode="ENFORCE", amount="20"), consecutive_loss=LossGate(mode="ENFORCE", count=5),
            breach_action="EXIT_WHEN_TRADABLE", recovery_policy="MANUAL_RECONCILE", max_market_age_seconds=120,
            operational=OperationalPolicy(min_disk_bytes=1024*1024, max_clock_skew_seconds="2", max_audit_records=10000,
                max_pending_commands=20, max_command_age_seconds=300, lease_seconds=180))
        model = SimConfig(seed=1, fee_rate="0.001", slippage_bps="10", participation_rate="1", latency_seconds=0,
            maintenance_margin_rate="0.05", ohlc_rule="CONSERVATIVE")
        timestamp = now()
        run = Aggregate(run_key=self.key, execution_mode="LIVE", state="RECOVERY_CHECK", policy=policy, sim_config=model,
            initial_cash=cash, currency="USDT", cash=cash, clock=timestamp, risk_day=risk_day(timestamp, "UTC"),
            day_start_equity=cash, peak_equity=cash, facts_start_at=timestamp)
        self.store.create(run)
        self.service.register_spec(self.principal, self.command(), spec)
        self.epoch = self.broker.call("claim")
        self.holder = self.manifest["holder"]
        self.executor = ExecutionWorker(self.store, self.broker, self.holder, self.epoch, now, self.health)
        self.protection = ProtectionWorker(self.store, self.broker, self.holder, self.epoch, now)
        self.start_api()
        self.reconcile(resume=True)
        self.checked("account_and_isolated_database_roles")

    def submit(self, market=False):
        quotes = self.refresh()
        price = (quotes["BID"] * Decimal("0.99") / self.spec.price_tick).to_integral_value(rounding=ROUND_FLOOR) * self.spec.price_tick
        stop = (min(price if not market else quotes["ASK"], quotes["MARK"]) * Decimal("0.995")
            / self.spec.price_tick).to_integral_value(rounding=ROUND_FLOOR) * self.spec.price_tick
        plan = ProtectionPlan(trigger_kind="MARK", trigger_price=stop, covered_quantity=self.quantity,
            exit_order_type="MARKET", max_slippage_bps="10", spec_version=self.spec.version)
        request = OrderRequest(owner_id="p1-owner", instrument_key=self.instrument, side="BUY", quantity=self.quantity,
            order_type="MARKET" if market else "LIMIT", limit_price=None if market else price,
            time_in_force="GTC" if market else "POST_ONLY", spec_version=self.spec.version, protection_plan=plan)
        command = self.command()
        response = self.http.post(PREFIX + "/orders", json={**wire(command), "order": wire(request)})
        if response.status_code != 202:
            raise TradingError(response.json().get("code", "TESTNET_API_SUBMIT_FAILED"), 423)
        identity = response.json()["resource_id"]
        self.executor.tick(self.key)
        return identity

    def cancel(self, identity):
        self.service.cancel_order(self.principal, self.command(), identity)
        for _ in range(5):
            self.executor.tick(self.key)
            if self.store.read(self.key).orders[identity].state == "CANCELED":
                return
        raise TradingError("TESTNET_CANCEL_UNCONFIRMED", 423)

    def ordinary(self):
        identity = self.submit()
        self.checked("ordinary_submit_acknowledged", self.store.read(self.key).orders[identity].state == "ACKNOWLEDGED")
        self.cancel(identity)
        self.checked("ordinary_cancel_confirmed")
        before = self.broker.call("stats")["write_attempts"]
        self.broker.call("drop_response")
        identity = self.submit()
        self.checked("actual_response_loss_persisted_unknown", self.store.read(self.key).orders[identity].state == "UNKNOWN")
        self.executor.tick(self.key)
        self.checked("original_id_query_no_duplicate_post", self.store.read(self.key).orders[identity].state == "ACKNOWLEDGED"
            and self.broker.call("stats")["write_attempts"] == before + 1)
        self.cancel(identity)
        self.reconcile(resume=True)

    def open_protected(self):
        identity = self.submit(market=True)
        for _ in range(12):
            self.refresh()
            synchronize(self.store, self.key, self.broker, Decimal("0.05"))
            self.protection.tick(self.key)
            run = self.store.read(self.key)
            position = run.positions.get(self.instrument.code())
            if run.orders[identity].state == "FILLED" and position and position.protection_state == "ACTIVE_VERIFIED":
                return next(p for p in reversed(list(run.protections.values())) if p.state == "ACTIVE_VERIFIED")
            time.sleep(0.3)
        raise TradingError("TESTNET_PHYSICAL_PROTECTION_UNCONFIRMED", 423)

    def protective_exit(self):
        old = self.open_protected()
        self.checked("actual_market_fill_and_physical_stop")
        # First prove overlap at a material distance from the mark. A tighter
        # stop may cross during transit and be definitively rejected; keep the
        # existing physical cover in that case.
        quotes = self.refresh()
        trigger = (quotes["MARK"] * Decimal("0.9975") / self.spec.price_tick).to_integral_value(rounding=ROUND_FLOOR) * self.spec.price_tick
        plan = old.plan.model_copy(update={"trigger_price": trigger})
        result = self.service.maintain_protection(self.principal, self.command(), self.instrument, plan, old.protection_id)
        for _ in range(8):
            self.protection.tick(self.key)
            current = self.store.read(self.key)
            if current.protections[result["resource_id"]].state == "ACTIVE_VERIFIED" and current.protections[old.protection_id].state == "CLOSED":
                break
            time.sleep(0.2)
        self.report["overlap_probe"] = {"new": current.protections[result["resource_id"]].state,
            "old": current.protections[old.protection_id].state, "transport": self.broker.call("stats")}
        self.checked("physical_overlap_replace", current.protections[result["resource_id"]].state == "ACTIVE_VERIFIED"
            and current.protections[old.protection_id].state == "CLOSED")
        active = current.protections[result["resource_id"]]
        # Natural exchange mark-price movement must produce the trigger. Keep
        # this experiment-only stop two valid price ticks below the current
        # mark; a percentage gap can remain untouched on a quiet testnet.
        for _ in range(6):
            quotes = self.refresh()
            trigger = (quotes["MARK"] / self.spec.price_tick).to_integral_value(rounding=ROUND_FLOOR) * self.spec.price_tick - 2 * self.spec.price_tick
            if trigger <= active.plan.trigger_price:
                break
            result = self.service.maintain_protection(self.principal, self.command(), self.instrument,
                active.plan.model_copy(update={"trigger_price": trigger}), active.protection_id)
            self.protection.tick(self.key)
            item = self.store.read(self.key).protections[result["resource_id"]]
            if item.state == "ACTIVE_VERIFIED":
                break
            if item.state != "CLOSED":
                break  # synchronize the physical status/exit; never resubmit an unknown stop
        deadline = time.monotonic() + 120
        while time.monotonic() < deadline:
            self.refresh()
            issues = synchronize(self.store, self.key, self.broker, Decimal("0.05"))
            # Algo status and trade history are separate, non-atomic queries.
            # A stop can fill between them, before its actual order ID appears
            # in the next algo response. Re-query the physical receipts while
            # recovery blocks new risk; never infer an association or book it.
            for _ in range(3):
                if not any(i.startswith("EXTERNAL_FILL_UNALLOCATED:") for i in issues):
                    break
                time.sleep(0.5)
                issues = synchronize(self.store, self.key, self.broker, Decimal("0.05"), recovery=True)
            if any(i.startswith("EXTERNAL_FILL_UNALLOCATED:") for i in issues):
                raise TradingError("TESTNET_EXTERNAL_TRADING_INTERFERENCE", 423)
            self.protection.tick(self.key)
            current = self.store.read(self.key)
            if not current.positions[self.instrument.code()].quantity:
                self.checked("exchange_stop_trigger_and_exit_fill", any(o.source_protection_id and o.state == "FILLED" for o in current.orders.values()))
                self.reconcile()
                account = self.broker.get_account(self.store.read(self.key))
                self.checked("fill_fee_pnl_wallet_reconciliation", abs(self.store.read(self.key).cash - account["cash"]) <= Decimal("0.0000001"))
                return
            time.sleep(1)
        raise TradingError("TESTNET_STOP_TRIGGER_TIMEOUT", 423)

    def recovery_drill(self):
        self.reconcile(resume=True)
        self.open_protected()
        before = self.store.read(self.key)
        self.stop_api()
        old_pid = self.broker.process.pid
        self.gateway.revoke(self.token)
        try:
            self.broker.call("raw", "/fapi/v1/accountConfig", {})
        except TradingError:
            self.checked("old_executor_egress_revoked")
        else:
            raise TradingError("OLD_EGRESS_STILL_ACCESSIBLE", 423)
        self.broker.close()
        self.broker = None
        self.checked("old_executor_process_exited", not Path("/proc", str(old_pid)).exists())
        # Independent official REST channel while the primary API/executor are
        # stopped. This is a testnet operator exercise, not a forged GUI or
        # independent human production approval.
        manual, token, manifest = self.new_signer(manual=True)
        try:
            raw = manual.call("manual_reduce", {"symbol": self.symbol, "side": "SELL", "type": "MARKET",
                "positionSide": "BOTH", "quantity": str(self.quantity), "reduceOnly": "true",
                "newClientOrderId": manifest["manual_id"]})
            order_id = str(raw["orderId"])
            for _ in range(12):
                receipts = manual.call("raw", "/fapi/v1/userTrades", {"symbol": self.symbol, "orderId": order_id})
                fills = [f for f in receipts if str(f["orderId"]) == order_id]
                account = manual.call("raw", "/fapi/v3/account", {})
                positions = manual.call("raw", "/fapi/v3/positionRisk", {})
                if fills and not any(Decimal(p["positionAmt"]) for p in positions):
                    break
                time.sleep(0.5)
            self.checked("official_rest_manual_emergency_exit", bool(fills) and not any(Decimal(p["positionAmt"]) for p in positions))
            self.report["manual_receipt"] = {"order_id": order_id, "fills": fills}
        finally:
            manual.close()
            self.gateway.revoke(token)
        fence = FenceExecutor(**self.command().model_dump(), epoch=self.epoch,
            evidence_ref="runtime:actual gateway revocation and process exit pid=" + str(old_pid))
        self.service.fence_executor(self.principal, fence, fence)
        self.broker, self.token, self.manifest = self.new_signer()
        self.epoch = self.broker.call("claim")
        self.holder = self.manifest["holder"]
        self.executor = ExecutionWorker(self.store, self.broker, self.holder, self.epoch, now, self.health)
        self.protection = ProtectionWorker(self.store, self.broker, self.holder, self.epoch, now)
        self.start_api()
        self.refresh()
        issues = synchronize(self.store, self.key, self.broker, Decimal("0.0000001"), recovery=True)
        self.checked("restart_detects_external_receipts", any(i.startswith("EXTERNAL_FILL_UNALLOCATED:") for i in issues))
        try:
            self.service.run_action(self.principal, self.command(), "resume")
        except TradingError:
            self.checked("premature_resume_rejected")
        else:
            raise TradingError("PREMATURE_RESUME_ACCEPTED", 423)
        fact = ExternalFact(external_id="manual-" + order_id, kind="MANUAL", instrument_key=self.instrument,
            happened_at=datetime.fromtimestamp(min(f["time"] for f in fills)/1000, timezone.utc), received_at=self.store.read(self.key).clock,
            before_quantity=before.positions[self.instrument.code()].quantity, after_quantity="0",
            cash_delta=Decimal(account["totalWalletBalance"]) - before.cash, currency="USDT", rule_version=self.spec.version,
            evidence_ref="binance-userTrades:" + order_id, external_fill_ids=[str(f["id"]) for f in fills])
        self.service.import_external(self.principal, self.command(), fact)
        self.checked("external_fact_increments_owner_epoch", self.store.read(self.key).owner_epochs[self.instrument.code()]
            > before.owner_epochs.get(self.instrument.code(), 0))
        # Confirm cancellation of the still-open physical stop after the manual
        # flat position; importing the fact must not pretend it was canceled.
        known = self.store.read(self.key).protections
        for raw in self.broker.call("raw", "/fapi/v1/openAlgoOrders", {}):
            identifier = raw.get("clientAlgoId")
            if identifier not in known:
                raise TradingError("TESTNET_EXTERNAL_TRADING_INTERFERENCE", 423)
            if raw["algoStatus"] == "NEW":
                self.service.cancel_protection(self.principal, self.command(), identifier)
                self.protection.tick(self.key)
        command = self.command()
        resolve = ResolveExternal(**command.model_dump(), instrument_key=self.instrument, owner_id="p1-owner",
            owner_epoch=self.store.read(self.key).owner_epochs[self.instrument.code()], evidence_ref="binance-userTrades:" + order_id)
        self.service.resolve_external(self.principal, resolve, resolve)
        self.reconcile(resume=True)
        self.checked("external_fact_recovery_without_duplicate_cash")
        self.checked("replacement_executor_new_epoch", self.epoch > before.lease_epoch)

    def storage_drill(self):
        # Actual database audit trigger failure, including a rolled-back intent,
        # not merely an injected in-memory exception.
        self.refresh()
        run = self.store.read(self.key)
        prior = self.broker.call("stats")["write_attempts"]
        with psycopg.connect(self.admin) as connection:
            connection.execute("CREATE FUNCTION trading_live.fail_acceptance_audit() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'acceptance audit unavailable'; END; $$")
            connection.execute("CREATE TRIGGER fail_acceptance BEFORE INSERT ON trading_live.audit_event FOR EACH ROW EXECUTE FUNCTION trading_live.fail_acceptance_audit()")
        try:
            try:
                self.service.run_action(self.principal, self.command(), "stop")
            except TradingError as error:
                self.checked("real_audit_storage_failure_rolls_back", error.code == "STORE_UNAVAILABLE"
                    and self.store.read(self.key).version == run.version)
            else:
                raise TradingError("AUDIT_FAILURE_ACCEPTED", 423)
        finally:
            with psycopg.connect(self.admin) as connection:
                connection.execute("DROP TRIGGER fail_acceptance ON trading_live.audit_event")
        self.checked("audit_failure_no_outbound_write", self.broker.call("stats")["write_attempts"] == prior)
        with psycopg.connect(self.admin) as connection:
            connection.execute("ALTER ROLE p1_live NOLOGIN")
        try:
            try:
                self.store.read(self.key)
            except TradingError as error:
                self.checked("real_database_access_failure", error.code == "STORE_UNAVAILABLE")
            else:
                raise TradingError("DATABASE_FAILURE_NOT_ENFORCED", 423)
            # An actual signer request must fail at the database fence before
            # signature/HTTP, even with valid exchange credentials in memory.
            order = next(iter(run.orders.values()), None)
            try:
                if order:
                    self.broker.submit_order(run, order)
                else:
                    self.broker.call("fence_probe")
            except TradingError as error:
                self.checked("database_failure_stops_signer", error.code == "STORE_UNAVAILABLE")
            else:
                raise TradingError("DATABASE_FAILURE_SIGNATURE_ALLOWED", 423)
            self.checked("database_failure_no_outbound_write", self.broker.call("stats")["write_attempts"] == prior)
        finally:
            with psycopg.connect(self.admin) as connection:
                connection.execute("ALTER ROLE p1_live LOGIN")
        self.reconcile()

    def isolation_drill(self):
        """Revoke a working signed route and replace the actual child process."""
        self.stop_api()
        prior_epoch, old_pid = self.epoch, self.broker.process.pid
        self.broker.call("raw", "/fapi/v1/accountConfig", {})
        self.gateway.revoke(self.token)
        try:
            self.broker.call("raw", "/fapi/v1/accountConfig", {})
        except TradingError:
            self.checked("old_executor_egress_revoked")
        else:
            raise TradingError("OLD_EGRESS_STILL_ACCESSIBLE", 423)
        self.broker.close()
        self.checked("old_executor_process_exited", not Path("/proc", str(old_pid)).exists())
        request = FenceExecutor(**self.command().model_dump(), epoch=prior_epoch,
            evidence_ref="actual revoked gateway and confirmed process exit pid=" + str(old_pid))
        self.service.fence_executor(self.principal, request, request)
        self.broker, self.token, self.manifest = self.new_signer()
        self.epoch = self.broker.call("claim")
        self.holder = self.manifest["holder"]
        self.executor = ExecutionWorker(self.store, self.broker, self.holder, self.epoch, now, self.health)
        self.protection = ProtectionWorker(self.store, self.broker, self.holder, self.epoch, now)
        self.start_api()
        self.reconcile()
        self.checked("replacement_executor_new_epoch", self.epoch > prior_epoch)

    def cleanup(self):
        # Never delete account-wide orders or guess an exit quantity. Only the
        # bounded diagnostic object and locally owned IDs can be acted on.
        if self.broker and hasattr(self, "store"):
            try:
                remote = self.broker.get_positions(self.store.read(self.key))
                position = next((p for p in remote if p.instrument_key == self.instrument and p.quantity), None)
                if position:
                    self.refresh()
                    request = OrderRequest(owner_id="p1-owner", instrument_key=self.instrument,
                        side="SELL" if position.quantity > 0 else "BUY", order_type="MARKET", quantity=abs(position.quantity),
                        reduce_only=True, spec_version=self.spec.version)
                    # Reduce-only still needs proven local quantity. If an
                    # external difference prevents proof, preserve the stop
                    # and report failure rather than editing ledger facts.
                    result = self.service.submit_order(self.principal, self.command(), request)
                    for _ in range(4):
                        self.executor.tick(self.key)
                        if self.store.read(self.key).orders[result["resource_id"]].state == "FILLED":
                            break
                for order in list(self.store.read(self.key).orders.values()):
                    if order.state not in TERMINAL:
                        self.cancel(order.order_id)
                self.refresh()
                synchronize(self.store, self.key, self.broker, Decimal("0.0000001"), recovery=True)
                for item in list(self.store.read(self.key).protections.values()):
                    if item.state != "CLOSED":
                        self.service.cancel_protection(self.principal, self.command(), item.protection_id)
                        self.protection.tick(self.key)
                ordinary = self.broker.call("raw", "/fapi/v1/openOrders", {})
                conditional = self.broker.call("raw", "/fapi/v1/openAlgoOrders", {})
                positions = self.broker.get_positions(self.store.read(self.key))
                self.checked("final_flat_no_ordinary_or_conditional_orders", not ordinary and not conditional and not any(p.quantity for p in positions))
            except (TradingError, OSError) as error:
                self.report["cleanup_error"] = error.code if isinstance(error, TradingError) else "CLEANUP_PROCESS_ERROR"
        self.stop_api()
        if self.broker:
            self.broker.close()
            self.broker = None
        if self.gateway:
            self.gateway.close()
        if self.server:
            self.server.cleanup()
        self.market.client.close() if hasattr(self, "market") else None
        self.err.close()
        self.report["finished_at"] = now()
        self.report["complete"] = bool(self.report.get("scenarios_complete")
            and self.report["checks"].get("final_flat_no_ordinary_or_conditional_orders")
            and not self.report.get("error") and not self.report.get("cleanup_error"))
        private_json(self.directory / "report.json", self.report)

    def run(self, isolation_only=False):
        self.report["suite"] = "isolation-only" if isolation_only else "complete-P1"
        try:
            self.setup()
            if not isolation_only:
                self.ordinary()
                self.protective_exit()
                self.recovery_drill()
            else:
                self.isolation_drill()
            self.storage_drill()
            self.report["scenarios_complete"] = True
        except (TradingError, OSError, KeyError, ValueError, StopIteration) as error:
            self.report["error"] = error.code if isinstance(error, TradingError) else "ACCEPTANCE_SETUP_ERROR"
            if self.broker:
                try:
                    self.report["transport"] = self.broker.call("stats")
                except (TradingError, OSError):
                    pass
            print("FAILED " + self.report["error"], flush=True)
        finally:
            self.cleanup()
        print("REPORT " + str(self.directory / "report.json"), flush=True)
        if not self.report["complete"]:
            raise SystemExit(1)


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument("--signer", action="store_true", help=argparse.SUPPRESS)
    parser.add_argument("--api-profile", help=argparse.SUPPRESS)
    parser.add_argument("--config", default="config/config.toml")
    parser.add_argument("--symbol", default="ETHUSDT", help="Diagnostic object, not an application binding")
    parser.add_argument("--max-notional", default="100", help="Explicit experiment ceiling in USDT, at most 100")
    parser.add_argument("--authorize-testnet-orders", action="store_true")
    parser.add_argument("--cleanup-run", help="Confirm flat venue and cancel only this probe's remaining conditional IDs")
    parser.add_argument("--isolation-only", action="store_true", help="Only read venue state and exercise local isolation/storage")
    args = parser.parse_args()
    if args.signer:
        signer_main()
    elif args.api_profile:
        api_process(args.api_profile)
    elif (args.cleanup_run and not args.authorize_testnet_orders) or (not args.authorize_testnet_orders and not args.isolation_only):
        raise SystemExit("TESTNET_ORDER_AUTHORIZATION_REQUIRED")
    else:
        root = Path.cwd().resolve()
        if args.cleanup_run:
            cleanup_flat_testnet(root, (root / args.config).resolve(), (root / args.cleanup_run).resolve())
        else:
            Acceptance(root, (root / args.config).resolve(), args.symbol, Decimal(args.max_notional)).run(args.isolation_only)


def cleanup_flat_testnet(root, config, directory):
    """Independent operator cleanup; never cancel an unrelated account order."""
    import pgserver
    import tomllib
    from factorforge.trading.adapters.binance.broker import SignedTransport
    if not directory.is_relative_to(root / "runtime"):
        raise SystemExit("TESTNET_RECEIPT_SCOPE_REQUIRED")
    report = json.loads((directory / "report.json").read_text())
    if report.get("scope") != "EXPERIMENT_ONLY" or report.get("venue") != "BINANCE_FUTURES_TESTNET":
        raise SystemExit("TESTNET_RECEIPT_SCOPE_REQUIRED")
    with config.open("rb") as stream:
        private = tomllib.load(stream)
    if private["services"]["exchange_api_url"].rstrip("/") != TESTNET_URL:
        raise SystemExit("TESTNET_ENDPOINT_REQUIRED")
    server = pgserver.get_server(directory / "pgdata", cleanup_mode="stop")
    try:
        database = json.loads((directory / "database-profile.json").read_text()) if (directory / "database-profile.json").exists() else None
        with psycopg.connect(database["live"] if database else server.get_uri()) as connection:
            run = Aggregate.model_validate(connection.execute("SELECT payload FROM trading_live.trading_run").fetchone()[0])
        known = {p.protection_id for p in run.protections.values()}
        with httpx.Client(base_url=TESTNET_URL, timeout=15, trust_env=False, follow_redirects=False) as client:
            credentials = private["credentials"]
            transport = SignedTransport(client, lambda: (credentials["exchange_api_key"], credentials["exchange_api_secret"]),
                now, 5000, None, 6000, 60)
            def fence():
                if any(Decimal(p["positionAmt"]) for p in transport.request("GET", "/fapi/v3/positionRisk", {})):
                    raise TradingError("TESTNET_CLEANUP_REQUIRES_FLAT_ACCOUNT", 423)
            transport.fence = fence
            fence()
            outstanding = transport.request("GET", "/fapi/v1/openAlgoOrders", {})
            for item in outstanding:
                if item.get("clientAlgoId") in known:
                    transport.request("DELETE", "/fapi/v1/algoOrder", {"clientAlgoId": item["clientAlgoId"]}, write=True)
            remaining = transport.request("GET", "/fapi/v1/openAlgoOrders", {})
            print("PROBE_CONDITIONALS_REMAINING " + str(sum(p.get("clientAlgoId") in known for p in remaining)))
    finally:
        server.cleanup()


if __name__ == "__main__":
    main()
