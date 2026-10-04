"""Read-only signed account probe restricted to the official futures testnet."""
import argparse
from datetime import datetime, timezone
import json
from pathlib import Path
import tomllib

import httpx

from factorforge.trading.adapters.binance.broker import SignedTransport
from factorforge.trading.adapters.binance.probes import account_probe
from factorforge.trading.domain.errors import TradingError
try:
    from .check_trading_readiness import inspect
except ImportError:
    from check_trading_readiness import inspect


def run_probe(config_path, timeout, recv_window, budget, window, client=None):
    if not inspect(config_path).get("official_futures_testnet_endpoint"):
        raise TradingError("OFFICIAL_TESTNET_ENDPOINT_REQUIRED", 423)
    with Path(config_path).open("rb") as stream:
        config = tomllib.load(stream)
    credentials = config["credentials"]
    owned = client is None
    client = client or httpx.Client(base_url=config["services"]["exchange_api_url"], timeout=timeout,
        trust_env=False, follow_redirects=False)
    try:
        transport = SignedTransport(client, lambda: (credentials["exchange_api_key"], credentials["exchange_api_secret"]),
            lambda: datetime.now(timezone.utc), recv_window, None, budget, window)
        # No execution fence is installed: even an accidental write would fail.
        return {"venue": "BINANCE_FUTURES_TESTNET", "at": datetime.now(timezone.utc).isoformat(),
            "read_only": True, "findings": account_probe(transport), "production_approved": False}
    finally:
        if owned:
            client.close()


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument("--config", default="config/config.toml")
    parser.add_argument("--timeout", type=float, required=True)
    parser.add_argument("--recv-window-ms", type=int, required=True)
    parser.add_argument("--request-budget", type=int, required=True)
    parser.add_argument("--budget-window-seconds", type=float, required=True)
    parser.add_argument("--output", required=True)
    args = parser.parse_args()
    output = Path(args.output).resolve()
    runtime = Path(__file__).resolve().parents[1] / "runtime"
    if not output.is_relative_to(runtime.resolve()) or output.exists() or args.timeout <= 0:
        raise SystemExit("TESTNET_PROBE_OUTPUT_OR_POLICY_INVALID")
    try:
        report = run_probe(args.config, args.timeout, args.recv_window_ms, args.request_budget, args.budget_window_seconds)
    except (TradingError, OSError, KeyError, ValueError) as error:
        raise SystemExit(error.code if isinstance(error, TradingError) else "TESTNET_PRIVATE_CONFIGURATION_INVALID") from None
    output.parent.mkdir(parents=True, exist_ok=True)
    output.write_text(json.dumps(report, indent=2) + "\n", encoding="utf-8")
    print(json.dumps(report, indent=2))


if __name__ == "__main__":
    main()
