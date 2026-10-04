"""Synthetic accounts and market facts only. No private config is loaded."""
from datetime import datetime, timedelta, timezone
from decimal import Decimal

import pytest

from factorforge.trading.adapters.memory import MemoryStore
from factorforge.trading.adapters.sim.broker import SimBroker
from factorforge.trading.api.dto import CreateRun, ReplayFrame
from factorforge.trading.application.commands import TradingService
from factorforge.trading.application.replay import advance
from factorforge.trading.domain.models import (
    Principal, RunKey, AccountPolicy, LossGate, SimConfig, InstrumentKey, InstrumentSpec,
    MarketPoint, Command, OrderRequest, ProtectionPlan,
)
from factorforge.trading.workers.executor import ExecutionWorker

AT = datetime(2026, 1, 5, tzinfo=timezone.utc)


class Harness:
    def __init__(self, store=None, mode="REPLAY", gate_mode="OBSERVE"):
        self.store = store or MemoryStore()
        self.service = TradingService(self.store, SimBroker())
        self.principal = Principal(principal_id="tester", environment="SIM", account_id="account-test",
                                   permissions={"read", "run:create", "order:write", "market:write", "protection:write",
                                                "sim:write", "income:write", "target:write", "run:stop", "run:resume", "run:reconcile"})
        self.key = RunKey(environment="SIM", account_id="account-test", run_id="run-test")
        gate = LossGate(mode=gate_mode, amount="1")
        policy = AccountPolicy(version="policy-test", valid_from=AT, risk_day_zone="UTC",
                               notional_limit="900", margin_limit="900", trade_loss_limit="100",
                               daily_loss=gate, drawdown=LossGate(mode=gate_mode, amount="100"),
                               consecutive_loss=LossGate(mode=gate_mode, count=3),
                               breach_action="KEEP_PROTECTION", recovery_policy="MANUAL_RECONCILE",
                               max_market_age_seconds=120)
        config = SimConfig(seed=7, fee_rate="0.001", slippage_bps="0", participation_rate="1",
                           latency_seconds=0, maintenance_margin_rate="0.05", ohlc_rule="CONSERVATIVE")
        body = CreateRun(schema_version="trading-2.0", request_id="create-test", idempotency_key="create-test",
                         run_key=self.key, reason="fixture", expires_at_utc=AT + timedelta(days=30),
                         execution_mode=mode, initial_cash="1000", currency="USD", clock=AT, account_policy=policy, sim_config=config)
        self.service.create_run(self.principal, body)
        self.instrument = InstrumentKey(venue="TEST", product="LINEAR_PERPETUAL", instrument_id="ALPHAUSD")
        spec = InstrumentSpec(key=self.instrument, version="rules-test", valid_from=AT, price_tick="0.01",
                              quantity_step="0.1", contract_multiplier="1", quote_currency="USD", settlement_currency="USD",
                              min_notional="1", capabilities={"MARKET", "LIMIT", "SHORT", "REDUCE_ONLY", "CONDITIONAL_PROTECTION"},
                              price_roles={"MARK", "LAST", "BID", "ASK"})
        self.counter = 0
        self.service.register_spec(self.principal, self.command(), spec)
        self.frame("100", seconds=1)

    def run(self):
        return self.store.read(self.key)

    def command(self, identity=None):
        self.counter += 1
        identity = identity or "request-" + str(self.counter)
        return Command(schema_version="trading-2.0", request_id=identity, idempotency_key=identity,
                       run_key=self.key, expected_version=self.run().version, reason="fixture",
                       expires_at_utc=AT + timedelta(days=30))

    def plan(self, quantity="1", stop="90"):
        return ProtectionPlan(trigger_kind="MARK", trigger_price=stop, covered_quantity=quantity,
                              exit_order_type="MARKET", max_slippage_bps="0", spec_version="rules-test")

    def request(self, quantity="1", side="BUY", reduce_only=False, owner="owner-test", order_type="MARKET", price=None):
        return OrderRequest(owner_id=owner, instrument_key=self.instrument, side=side, order_type=order_type,
                            quantity=quantity, reduce_only=reduce_only, spec_version="rules-test", limit_price=price,
                            protection_plan=None if reduce_only else self.plan(quantity, "90" if side == "BUY" else "110"))

    def submit(self, request=None, command=None):
        return self.service.submit_order(self.principal, command or self.command(), request or self.request())

    def dispatch(self, broker=None):
        worker = ExecutionWorker(self.store, broker or SimBroker())
        for _ in range(20):
            if not worker.tick(self.key):
                break

    def frame(self, price, seconds=1, liquidity="100", candle=None):
        at = self.run().clock + timedelta(seconds=seconds)
        points = [MarketPoint(instrument_key=self.instrument, source_id="fixture", kind=kind, observed_at=at,
                              received_at=at, available_at=at, value=price, currency="USD", quality="VALID", spec_version="rules-test")
                  for kind in ("BID", "ASK", "MARK", "LAST")]
        frame = ReplayFrame(at=at, instrument_key=self.instrument, points=points, liquidity=liquidity, candle=candle)
        return advance(self.service, self.principal, self.command(), frame)


@pytest.fixture
def harness():
    return Harness()


@pytest.fixture
def postgres(tmp_path):
    from factorforge.trading.adapters.postgres.store import initialize
    pgserver = pytest.importorskip("pgserver")
    server = pgserver.get_server(tmp_path / "pgdata", cleanup_mode="stop")
    dsn = server.get_uri()
    initialize(dsn, "SIM")
    initialize(dsn, "LIVE")
    yield dsn
    server.cleanup()
