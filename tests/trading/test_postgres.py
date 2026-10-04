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
