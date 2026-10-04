"""Configuration is private; each process is bound to exactly one environment."""
import argparse
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
        if config["runtime"]["environment"] != "SIM" or config["trading"]["allow_live"]:
            raise TradingError("LIVE_CAPABILITIES_UNVERIFIED", 423)
        if config["trading"]["adapter"] != "mock":
            raise TradingError("EXECUTION_ADAPTER_UNVERIFIED", 423)
        if not config["services"]["database_url"] or not config["credentials"]["trading_api_token"]:
            raise TradingError("PRIVATE_CONFIGURATION_REQUIRED", 503)
        if not config["trading"]["account_id"] or not config["trading"]["principal_id"]:
            raise TradingError("ACCOUNT_BINDING_REQUIRED", 503)
        return config
    except (OSError, KeyError, ValueError):
        raise TradingError("PRIVATE_CONFIGURATION_INVALID", 503) from None


def assemble(config, recover=False):
    store = PostgresStore(config["services"]["database_url"], "SIM")
    store.verify_runtime_role()
    principal = Principal(principal_id=config["trading"]["principal_id"], environment="SIM",
                          account_id=config["trading"]["account_id"], permissions=set(config["trading"]["permissions"]))
    service = TradingService(store, SimBroker())
    tokens = {config["credentials"]["trading_api_token"]: principal}
    if recover:
        bound = store.bound_run(principal.account_id)
        if bound:
            with store.transaction(bound) as run:
                run.state = "RECOVERY_CHECK"
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
            initialize(config["services"]["database_url"], "SIM")
            print("SIM schema initialized; configure the isolated runtime role before starting")
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
    args = parser.parse_args()
    try:
        config = load_config(args.config)
        store, _, _ = assemble(config)
        worker = ExecutionWorker(store, SimBroker())
        key = RunKey(environment="SIM", account_id=config["trading"]["account_id"], run_id=args.run_id)
        while True:
            worker.tick(key)
            if args.once:
                break
            time.sleep(0.25)
    except TradingError as error:
        raise SystemExit(error.code) from None


def live_main():
    # No secret-loading or outgoing signed request happens before this admission gate.
    raise SystemExit("LIVE_CAPABILITIES_UNVERIFIED: account probes and recovery exercises required")
