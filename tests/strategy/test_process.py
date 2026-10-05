"""Installed P2 API/CLI processes and PostgreSQL roles, with no application package."""
import json
from pathlib import Path
import socket
import subprocess
import sys
import time
import httpx
import psycopg
from psycopg.conninfo import make_conninfo
import pytest

from factorforge.strategy.adapters.postgres.store import PostgresStore,initialize
from factorforge.strategy.domain.models import StrategyState
from conftest import Harness,AT


def toml_value(value):
    if isinstance(value,dict):
        return "{"+", ".join(json.dumps(k)+" = "+toml_value(v) for k,v in value.items())+"}"
    if isinstance(value,list):
        return "["+", ".join(toml_value(v) for v in value)+"]"
    return json.dumps(value)


@pytest.mark.postgres
def test_installed_api_cli_restart_and_public_score(database,tmp_path):
    # Only wheel-installed entry points qualify for this test.
    executable = Path(sys.executable).parent
    if not (executable/"strategy-api").exists():
        pytest.fail("Install the built strategy wheel before process validation")
    state_store = PostgresStore(database,"SIM")
    h = Harness()
    state_store.create(h.state())
    with psycopg.connect(database) as conn:
        conn.execute("INSERT INTO strategy_sim.workload_capability_grant VALUES ('fixture-worker','fixture-instance','object-0','signal:sim')")
    with socket.socket() as sock:
        sock.bind(("127.0.0.1",0))
        port = sock.getsockname()[1]
    base = f"http://127.0.0.1:{port}"
    config = dict(environment="SIM",public_database_url=make_conninfo(database,options="-c role=factorforge_strategy_sim_public"),
        public_identity=h.public.model_dump(mode="json"),public_token="fixture-process",trading_api_url="http://127.0.0.1:1",trading_api_token="fixture-unused",
        timeout_seconds=1,candle_interval="1m",history_seconds=1000,replay_clock=h.clock.now().isoformat(),public_host="127.0.0.1",public_port=port,public_api_url=base)
    path = tmp_path/"config.toml"
    path.write_text("[strategy]\n"+"\n".join(k+" = "+toml_value(v) for k,v in config.items()))
    def start():
        proc = subprocess.Popen([str(executable/"strategy-api"),"--config",str(path)],cwd=tmp_path,stdout=subprocess.DEVNULL,stderr=subprocess.PIPE)
        for _ in range(80):
            if proc.poll() is not None:
                pytest.fail("P2 API exited before readiness: "+proc.stderr.read().decode())
            try:
                if httpx.get(base+"/api/v2/strategy/health",timeout=1).status_code == 200:
                    return proc
            except httpx.HTTPError:
                time.sleep(0.05)
        proc.terminate()
        proc.wait(timeout=10)
        pytest.fail("P2 API did not become ready")
    def cli(*arguments):
        return subprocess.run([str(executable/"factorforge-strategy"),"--config",str(path),*arguments],cwd=tmp_path,text=True,capture_output=True,check=True)
    server = start()
    try:
        assert json.loads(cli("show-pool","--object-id","object-0").stdout)["net"] == "0"
        event,score,_ = h.event(identity=h.public)
        event_body = {**h.scommand().model_dump(mode="json"),"expected_version":state_store.read("fixture-instance").version,"event":event.model_dump(mode="json")}
        payload = tmp_path/"event.json"
        payload.write_text(json.dumps(event_body))
        cli("create-event","--file",str(payload))
        score_body = {**h.scommand().model_dump(mode="json"),"expected_version":state_store.read("fixture-instance").version,"score":score.model_dump(mode="json")}
        payload.write_text(json.dumps(score_body))
        assert json.loads(cli("submit-score","--event-id",event.event_id,"--file",str(payload)).stdout)["state"] == "RESEARCH_ONLY"
        server.terminate()
        server.wait(timeout=10)
        server.stderr.close()
        server = start()
        assert json.loads(cli("show-pool","--object-id","object-0").stdout)["net"] == "0"
        assert state_store.read("fixture-instance").research_receipts
    finally:
        server.terminate()
        server.wait(timeout=10)
        server.stderr.close()
