from concurrent.futures import ThreadPoolExecutor
from datetime import timedelta
import psycopg
from psycopg.conninfo import make_conninfo
import pytest

from factorforge.strategy.adapters.postgres.store import initialize,PostgresStore
from factorforge.strategy.application.decision_cycle import DecisionCycle
from factorforge.strategy.application.score_admission import ScoreAdmission
from factorforge.strategy.domain.models import StrategyError,StrategyState
from conftest import Harness

pytestmark = pytest.mark.postgres


@pytest.fixture
def database(tmp_path):
    import pgserver
    server = pgserver.get_server(tmp_path/"pgdata",cleanup_mode="stop")
    dsn = server.get_uri()
    initialize(dsn,"SIM")
    initialize(dsn,"LIVE")
    yield dsn
    server.cleanup()


class FixtureStore(PostgresStore):
    def grant_fixture(self,identity,object_id):
        with psycopg.connect(self.dsn) as conn:
            conn.execute("INSERT INTO strategy_sim.workload_capability_grant VALUES (%s,%s,%s,'signal:sim') ON CONFLICT DO NOTHING",(identity.workload_id,identity.instance_id,object_id))


def test_database_restart_unknown_target_reservation_and_elapsed_decay(database):
    h = Harness(FixtureStore(database,"SIM"))
    h.event()
    h.cycle.tick(h.identity)
    restarted = PostgresStore(database,"SIM")
    cycle = DecisionCycle(restarted,h.trading,h.clock)
    cycle.dispatch(h.identity)
    assert next(iter(restarted.read(h.identity.instance_id).outbox.values())).state == "ACK"
    h.frame("100",3600)
    cycle.tick(h.identity)
    state = restarted.read(h.identity.instance_id)
    assert len(state.contributions) == 1 and state.skipped_cycles[0]["count"] == 59
    assert abs(next(iter(state.contributions.values())).remaining_amount-30) < state.policies["fixture-policy"].numerical_tolerance
    assert len(state.reservations) == 1


def test_audit_failure_rolls_back_ledger_reservation_and_outbox(database):
    h = Harness(FixtureStore(database,"SIM"))
    h.event()
    with psycopg.connect(database) as conn:
        conn.execute("CREATE FUNCTION strategy_sim.inject_failure() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'fixture audit unavailable'; END; $$")
        conn.execute("CREATE TRIGGER injected_failure BEFORE INSERT ON strategy_sim.audit_event FOR EACH ROW EXECUTE FUNCTION strategy_sim.inject_failure()")
    with pytest.raises(StrategyError,match="STORE_UNAVAILABLE"):
        h.cycle.tick(h.identity)
    assert not h.state().contributions and not h.state().outbox and not h.state().reservations


def test_physical_public_workload_and_sim_live_roles(database):
    h = Harness(FixtureStore(database,"SIM"))
    public = make_conninfo(database,options="-c role=factorforge_strategy_sim_public")
    worker = make_conninfo(database,options="-c role=factorforge_strategy_sim_worker")
    PostgresStore(public,"SIM","public").verify_runtime_role()
    PostgresStore(worker,"SIM","worker").verify_runtime_role()
    for statement in ("INSERT INTO strategy_sim.workload_capability_grant VALUES ('fake','fixture-instance','object-0','signal:sim')",
        "UPDATE strategy_sim.internal_state SET payload='{}'::jsonb", "SELECT * FROM strategy_live.public_state", "UPDATE strategy_sim.audit_event SET payload='{}'::jsonb"):
        with psycopg.connect(public) as conn:
            with pytest.raises(psycopg.errors.InsufficientPrivilege):
                conn.execute(statement)
    with psycopg.connect(worker) as conn:
        with pytest.raises(psycopg.errors.InvalidTextRepresentation):
            conn.execute("INSERT INTO strategy_sim.api_key_scope VALUES ('fake','signal:sim')")
    with pytest.raises(StrategyError,match="DATABASE_ROLE_NOT_ISOLATED"):
        PostgresStore(database,"SIM").verify_runtime_role()
    event,score,_ = h.event(identity=h.public)
    public_service = ScoreAdmission(PostgresStore(public,"SIM","public"),h.clock)
    revised = score.model_copy(update={"submission_id":"public-second","producer_version":"fixture-v2"})
    assert public_service.submit(h.public,h.scommand(),revised)["state"] == "RESEARCH_ONLY"


def test_owner_binding_is_unique_across_instances_and_initial_audit_is_persisted(database):
    h = Harness(FixtureStore(database,"SIM"))
    store = PostgresStore(database,"SIM")
    store.create(StrategyState(instance_id="second-instance",environment="SIM",audit=[{"action":"INSTANCE_CREATED"}]))
    assert store.read("second-instance").audit == [{"action":"INSTANCE_CREATED"}]
    duplicate = h.state().objects["object-0"].model_copy(update={"instance_id":"second-instance","object_id":"other-object","owner_id":"other-owner"})
    with pytest.raises(StrategyError,match="OBJECT_OWNER_CONFLICT"):
        with store.transaction("second-instance") as state:
            state.objects[duplicate.object_id] = duplicate
    assert not store.read("second-instance").objects
    with pytest.raises(StrategyError,match="OBJECT_BINDING_CHANGED"):
        with store.transaction(h.identity.instance_id) as state:
            state.objects["object-0"].owner_id = "changed-owner"
    assert h.state().objects["object-0"].owner_id == "owner-0"
