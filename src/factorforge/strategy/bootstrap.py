"""Load private canonical configuration; framework holds only scoped P1 API access."""
import argparse
from pathlib import Path
import time
import tomllib

from factorforge.strategy.adapters.clock import SystemClock,ReplayClock
from factorforge.strategy.adapters.postgres.store import PostgresStore,initialize
from factorforge.strategy.adapters.trading_v2 import TradingV2
from factorforge.strategy.api.app import create_app
from factorforge.strategy.application.decision_cycle import DecisionCycle
from factorforge.strategy.domain.models import PublicPrincipal,WorkloadIdentity,StrategyState,StrategyError,utc,ParameterSnapshot,Policy
from factorforge.strategy.workers.scheduler import Scheduler
from factorforge.strategy.workers.feedback import FeedbackWorker


def load(path):
    with Path(path).open("rb") as stream:
        return tomllib.load(stream)["strategy"]


def initialize_instance(config):
    """Explicit operator command; migration credential is never used by serving processes."""
    import psycopg
    from psycopg import sql
    identity = WorkloadIdentity.model_validate(config["workload_identity"])
    environment = config["environment"]
    if identity.environment != environment or identity.instance_id != config["instance_id"]:
        raise StrategyError("INITIAL_INSTANCE_BINDING_CONFLICT")
    dsn = config["migration_database_url"]
    initialize(dsn,environment)
    registry = config["initial_registry"]
    state = StrategyState(instance_id=config["instance_id"],environment=environment,
        parameters={v["version"]:ParameterSnapshot.model_validate(v) for v in registry["parameters"]},
        policies={v["version"]:Policy.model_validate(v) for v in registry["policies"]},factor_manifests=registry["factor_manifests"])
    PostgresStore(dsn,environment).create(state)
    with psycopg.connect(dsn) as conn:
        for object_id in sorted(identity.object_ids):
            conn.execute(sql.SQL("INSERT INTO {}.workload_capability_grant VALUES (%s,%s,%s,%s)").format(sql.Identifier("strategy_"+environment.lower())),
                (identity.workload_id,identity.instance_id,object_id,"signal:"+environment.lower()))


def assemble(path,internal):
    config = load(path)
    environment = config["environment"]
    store = PostgresStore(config["worker_database_url"] if internal else config["public_database_url"],environment,"worker" if internal else "public")
    store.verify_runtime_role()
    clock = ReplayClock(utc(config["replay_clock"])) if environment == "SIM" and config.get("replay_clock") else SystemClock()
    identity = WorkloadIdentity.model_validate(config["workload_identity"]) if internal else PublicPrincipal.model_validate(config["public_identity"])
    trading = TradingV2(config["trading_api_url"],config["trading_api_token"],config["timeout_seconds"],config["candle_interval"],config["history_seconds"])
    cycle = DecisionCycle(store,trading,clock)
    return config,store,clock,identity,trading,cycle


def parser():
    result = argparse.ArgumentParser()
    result.add_argument("--config",default="config/config.toml")
    return result


def api_main():
    import uvicorn
    arguments = parser()
    arguments.add_argument("--internal",action="store_true")
    args = arguments.parse_args()
    try:
        config,store,clock,identity,trading,cycle = assemble(args.config,args.internal)
        token = config["workload_token"] if args.internal else config["public_token"]
        if not token:
            raise StrategyError("AUTHENTICATION_CONFIGURATION_REQUIRED")
        app = create_app(store,clock,{token:identity},internal=args.internal,cycle=cycle)
        uvicorn.run(app,host=config["internal_host"] if args.internal else config["public_host"],port=config["internal_port"] if args.internal else config["public_port"],access_log=False)
    except (KeyError,ValueError,StrategyError):
        raise SystemExit("Strategy configuration or authorization is incomplete") from None


def run_worker(feedback=False):
    args = parser().parse_args()
    config,store,clock,identity,trading,cycle = assemble(args.config,True)
    worker = FeedbackWorker(store,trading,clock,identity) if feedback else Scheduler(cycle,identity)
    interval = config["worker_poll_seconds"]
    if not 0 < interval <= 60:
        raise SystemExit("worker_poll_seconds must be between 0 and 60")
    while True:
        try:
            worker.tick()
        except StrategyError as error:
            print(error.code,flush=True)
        time.sleep(interval)


def scheduler_main():
    run_worker()


def feedback_main():
    run_worker(True)
