"""Real PostgreSQL evidence: restart, transaction rollback and role isolation."""
from concurrent.futures import ThreadPoolExecutor
from decimal import Decimal

import psycopg
from psycopg.conninfo import make_conninfo
import pytest

from conftest import Harness
from factorforge.trading.adapters.postgres.store import PostgresStore, initialize
from factorforge.trading.application.commands import TradingService
from factorforge.trading.adapters.sim.broker import SimBroker
from factorforge.trading.domain.errors import TradingError


def test_target_continues_after_real_database_service_restart(postgres):
    from factorforge.trading.api.dto import TargetRequest
    from factorforge.trading.application.targets import set_target
    from factorforge.trading.workers.executor import ExecutionWorker
    h = Harness(PostgresStore(postgres, "SIM"))
    body = TargetRequest(**h.command().model_dump(mode="python"), owner_id="owner-test", instrument_key=h.instrument,
        target_version=1, target_quantity="1", policy_version="policy-test", spec_version="rules-test",
        source_decision_id="fixture", protection_plan=h.plan(), owner_epoch=0)
    set_target(h.service, h.principal, body)
    h.dispatch()
    h.frame("100")
    body = TargetRequest(**h.command().model_dump(mode="python"), owner_id="owner-test", instrument_key=h.instrument,
        target_version=2, target_quantity="-1", policy_version="policy-test", spec_version="rules-test",
        source_decision_id="fixture", protection_plan=h.plan(stop="110"), owner_epoch=0)
    set_target(h.service, h.principal, body)
    h.dispatch()
    h.frame("100")
    assert h.run().positions[h.instrument.code()].quantity == 0
    restarted_store = PostgresStore(postgres, "SIM")
    ExecutionWorker(restarted_store, SimBroker()).tick(h.key)
    h.frame("100")
    assert restarted_store.read(h.key).positions[h.instrument.code()].quantity == -1
    assert len(restarted_store.read(h.key).orders) == 3


def test_database_lease_race_and_outbound_fence_reject_old_holder(postgres):
    from datetime import timedelta
    import httpx
    from factorforge.trading.domain.lease import acquire, assert_lease, isolate
    from factorforge.trading.adapters.binance.broker import SignedTransport
    h = Harness(PostgresStore(postgres, "SIM"))
    now = h.run().clock
    def contend(holder):
        try:
            with PostgresStore(postgres, "SIM").transaction(h.key) as run:
                return holder, acquire(run, holder, now, 1)
        except TradingError:
            return None
    with ThreadPoolExecutor(max_workers=2) as pool:
        results = list(pool.map(contend, ("worker-a", "worker-b")))
    winners = [item for item in results if item]
    assert len(winners) == 1
    old, epoch = winners[0]
    now += timedelta(seconds=2)
    with h.store.transaction(h.key) as run:
        with pytest.raises(TradingError, match="NOT_ISOLATED"):
            acquire(run, "replacement", now, 10)
        isolate(run, epoch, "fake egress gateway blocked and old process terminated")
        fresh = acquire(run, "replacement", now, 10)
    requests = []
    def sign(holder, generation):
        client = httpx.Client(base_url="https://venue.invalid", transport=httpx.MockTransport(
            lambda req: requests.append(req) or httpx.Response(200, json={})))
        return SignedTransport(client, lambda: ("test-key", "test-secret"), lambda: now, 5000,
            lambda: assert_lease(PostgresStore(postgres, "SIM").read(h.key), holder, generation, now), 10, 60)
    with pytest.raises(TradingError, match="LEASE_LOST"):
        sign(old, epoch).request("POST", "/fapi/v1/order", {}, write=True)
    sign("replacement", fresh).request("POST", "/fapi/v1/order", {}, write=True)
    assert len(requests) == 1


def test_external_import_remains_blocked_after_database_restart(postgres):
    from factorforge.trading.domain.models import ExternalFact
    h = Harness(PostgresStore(postgres, "SIM"))
    h.principal.permissions.add("external:import")
    h.submit()
    h.dispatch()
    h.frame("100")
    fact = ExternalFact(external_id="external-official", kind="ADL", instrument_key=h.instrument, happened_at=h.run().clock,
        received_at=h.run().clock, before_quantity="1", after_quantity="0", cash_delta="-1", currency="USD",
        evidence_ref="fixture official receipt", rule_version="rules-test")
    h.service.import_external(h.principal, h.command(), fact)
    restarted = PostgresStore(postgres, "SIM")
    assert restarted.read(h.key).owner_epochs[h.instrument.code()] == 1
    assert restarted.read(h.key).state == "RECOVERY_CHECK"
    with psycopg.connect(postgres) as connection:
        assert connection.execute("SELECT count(*) FROM trading_sim.external_fact").fetchone()[0] == 1

pytestmark = pytest.mark.postgres


def test_postgres_persists_completed_idempotency_across_connections(postgres):
    h = Harness(PostgresStore(postgres, "SIM"))
    command, request = h.command("persisted-request"), h.request()
    first = h.submit(request, command)
    h.dispatch()
    h.frame("100")
    restarted = TradingService(PostgresStore(postgres, "SIM"), SimBroker())
    assert restarted.submit_order(h.principal, command, request) == first
    with psycopg.connect(postgres) as connection:
        assert connection.execute('SELECT count(*) FROM trading_sim."order"').fetchone()[0] == 1
        assert connection.execute('SELECT count(*) FROM trading_sim.outbox').fetchone()[0] == 1
        assert connection.execute('SELECT count(*) FROM trading_sim.fill').fetchone()[0] == 1


def test_audit_failure_rolls_back_order_reservation_and_outbox(postgres):
    h = Harness(PostgresStore(postgres, "SIM"))
    before = h.run()
    with psycopg.connect(postgres) as connection:
        connection.execute("CREATE FUNCTION trading_sim.fail_audit() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'injected audit failure'; END; $$")
        connection.execute("CREATE TRIGGER fail_audit BEFORE INSERT ON trading_sim.audit_event FOR EACH ROW EXECUTE FUNCTION trading_sim.fail_audit()")
    with pytest.raises(TradingError, match="STORE_UNAVAILABLE"):
        h.submit()
    after = h.run()
    assert after.version == before.version
    assert not after.orders and not after.outbox
    with psycopg.connect(postgres) as connection:
        assert connection.execute('SELECT count(*) FROM trading_sim."order"').fetchone()[0] == 0
        assert connection.execute('SELECT count(*) FROM trading_sim.risk_reservation').fetchone()[0] == 0


def test_database_roles_cannot_cross_sim_live_or_edit_audit(postgres):
    h = Harness(PostgresStore(postgres, "SIM"))
    restricted = make_conninfo(postgres, options="-c role=factorforge_sim")
    PostgresStore(restricted, "SIM").verify_runtime_role()
    with pytest.raises(TradingError, match="DATABASE_ROLE_NOT_ISOLATED"):
        PostgresStore(postgres, "SIM").verify_runtime_role()
    assert PostgresStore(restricted, "SIM").read(h.key).run_key == h.key
    with psycopg.connect(restricted) as connection:
        with pytest.raises(psycopg.errors.InsufficientPrivilege):
            connection.execute("SELECT * FROM trading_live.trading_run")
    with psycopg.connect(restricted) as connection:
        with pytest.raises(psycopg.errors.InsufficientPrivilege):
            connection.execute("UPDATE trading_sim.audit_event SET payload='{}'::jsonb")
    with psycopg.connect(postgres) as connection:
        with pytest.raises(psycopg.Error, match="immutable audit"):
            connection.execute("DELETE FROM trading_sim.audit_event")


def test_account_writers_cannot_double_reserve_budget(postgres):
    h = Harness(PostgresStore(postgres, "SIM"))
    request = h.request(quantity="6")

    def attempt(index):
        for _ in range(3):
            try:
                return h.submit(request, h.command("parallel-" + str(index)))
            except TradingError as error:
                if error.code == "VERSION_CONFLICT":
                    continue
                return error.code
        return "VERSION_CONFLICT"

    with ThreadPoolExecutor(max_workers=2) as pool:
        results = list(pool.map(attempt, (1, 2)))
    assert sum(isinstance(r, dict) for r in results) == 1
    assert "ACCOUNT_NOTIONAL_LIMIT" in results
    assert sum(o.reserved_notional for o in h.run().orders.values()) == Decimal("600")
