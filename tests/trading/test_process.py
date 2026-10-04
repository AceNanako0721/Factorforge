"""Start the installed server and CLI against a real isolated PostgreSQL instance."""
import json
from pathlib import Path
import socket
import subprocess
import sys
import time

import httpx
from psycopg.conninfo import make_conninfo
import pytest

from conftest import Harness
from factorforge.trading.adapters.postgres.store import PostgresStore

pytestmark = pytest.mark.postgres


def test_standalone_api_cli_worker_and_restart(postgres, tmp_path):
    h = Harness(PostgresStore(postgres, "SIM"))
    h.principal.permissions |= {"external:import", "external:resolve", "executor:fence"}
    root = Path(__file__).resolve().parents[2]
    with socket.socket() as sock:
        sock.bind(("127.0.0.1", 0))
        port = sock.getsockname()[1]
    base = "http://127.0.0.1:" + str(port)
    config = (root / "config/config.example.toml").read_text()
    private_values = {"database_url": make_conninfo(postgres, options="-c role=factorforge_sim"),
                      "trading_api_url": base, "trading_api_token": "process-test", "account_id": h.key.account_id,
                      "principal_id": h.principal.principal_id}
    for key, value in private_values.items():
        config = config.replace("\n" + key + ' = ""', "\n" + key + " = " + json.dumps(value))
    config = config.replace("permissions = []", "permissions = " + json.dumps(sorted(h.principal.permissions)))
    private_path = tmp_path / "config.toml"
    private_path.write_text(config)
    executable = Path(sys.executable).parent
    server = subprocess.Popen([str(executable / "trading-api"), "--config", str(private_path), "--port", str(port)],
                              cwd=tmp_path, stdout=subprocess.DEVNULL, stderr=subprocess.PIPE)
    headers = {"Authorization": "Bearer process-test"}

    def cli(command, body=None):
        args = [str(executable / "factorforge-trading"), "--config", str(private_path), command]
        if body is not None:
            request = tmp_path / "request.json"
            request.write_text(json.dumps(body))
            args.extend(["--request", str(request)])
        else:
            args.extend(["--run-id", h.key.run_id])
        return subprocess.run(args, cwd=tmp_path, capture_output=True, text=True, check=True)

    try:
        with httpx.Client(base_url=base, timeout=2, trust_env=False, headers=headers) as client:
            for _ in range(80):
                if server.poll() is not None:
                    pytest.fail("standalone API exited before readiness")
                try:
                    if client.get("/api/v2/trading/health").status_code == 200:
                        break
                except httpx.HTTPError:
                    time.sleep(0.05)
            else:
                pytest.fail("standalone API did not become ready")
            assert h.run().state == "RECOVERY_CHECK"
            cli("reconcile", h.command().model_dump(mode="json"))
            cli("resume", h.command().model_dump(mode="json"))
            body = {**h.command().model_dump(mode="json"), "order": h.request().model_dump(mode="json")}
            receipt = json.loads(cli("order-submit", body).stdout)
            subprocess.run([str(executable / "execution-sim"), "--config", str(private_path), "--run-id", h.key.run_id, "--once"],
                           cwd=tmp_path, capture_output=True, check=True)
            h.frame("100")
            assert h.run().orders[receipt["resource_id"]].state == "FILLED"
            assert json.loads(cli("account-show").stdout)["equity"] == "999.900"
            assert json.loads(cli("order-submit", body).stdout) == receipt
            # Rehearse the official-channel recovery sequence with synthetic
            # manual facts, across the actual API/CLI/DB process boundary.
            cli("stop", h.command().model_dump(mode="json"))
            from factorforge.trading.domain.models import ExternalFact
            fact = ExternalFact(external_id="manual-drill", kind="MANUAL", instrument_key=h.instrument,
                happened_at=h.run().clock, received_at=h.run().clock, before_quantity="1", after_quantity="0",
                cash_delta="-2", currency="USD", evidence_ref="LOCAL_SIM_DRILL_ONLY", rule_version="rules-test")
            cli("external-import", {**h.command().model_dump(mode="json"), "fact": fact.model_dump(mode="json")})
            with pytest.raises(subprocess.CalledProcessError):
                cli("resume", h.command().model_dump(mode="json"))
            cli("external-resolve", {**h.command().model_dump(mode="json"), "instrument_key": h.instrument.model_dump(mode="json"),
                "owner_id": "owner-test", "owner_epoch": 1, "evidence_ref": "LOCAL_SIM_DRILL_ONLY"})
            cli("reconcile", h.command().model_dump(mode="json"))
            cli("resume", h.command().model_dump(mode="json"))
            assert h.run().state == "NORMAL" and h.run().positions[h.instrument.code()].quantity == 0
    finally:
        server.terminate()
        server.wait(timeout=10)
        server.stderr.close()
