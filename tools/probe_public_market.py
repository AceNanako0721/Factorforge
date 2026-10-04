"""Read-only operator probe: no credentials, signed requests or order endpoints."""
import argparse
from concurrent.futures import ThreadPoolExecutor
from datetime import datetime, timezone
import json
from pathlib import Path
from urllib.parse import urlparse

import httpx


def probe(base_url, instrument, timeout):
    requests = [("/fapi/v1/time", {}), ("/fapi/v1/exchangeInfo", {}),
        ("/fapi/v1/ticker/bookTicker", {"symbol": instrument}),
        ("/fapi/v1/premiumIndex", {"symbol": instrument}),
        ("/fapi/v2/ticker/price", {"symbol": instrument}),
        ("/fapi/v1/trades", {"symbol": instrument, "limit": 2}),
        ("/fapi/v1/klines", {"symbol": instrument, "interval": "1m", "limit": 2})]
    def get(item):
        path, params = item
        try:
            response = httpx.get(base_url.rstrip("/") + path, params=params, timeout=timeout,
                trust_env=False, follow_redirects=False)
            result = {"path": path, "http_status": response.status_code, "verified": False}
            if response.status_code == 200:
                payload = response.json()
                result.update(verified=True, shape=type(payload).__name__,
                    count=len(payload) if isinstance(payload, list) else None)
            return result
        except (httpx.HTTPError, ValueError):
            return {"path": path, "verified": False, "error": "PUBLIC_QUERY_UNAVAILABLE"}
    with ThreadPoolExecutor(max_workers=len(requests)) as pool:
        results = list(pool.map(get, requests))
    return {"at": datetime.now(timezone.utc).isoformat(), "instrument": instrument,
            "read_only": True, "execution_verified": False, "results": results}


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument("--base-url", required=True)
    parser.add_argument("--instrument", required=True)
    parser.add_argument("--timeout", type=float, required=True)
    parser.add_argument("--output", required=True)
    parser.add_argument("--validate-adapter", action="store_true")
    parser.add_argument("--multiplier")
    parser.add_argument("--publication-delay-ms", type=int)
    args = parser.parse_args()
    url = urlparse(args.base_url)
    if url.scheme != "https" or url.username or url.password or args.timeout <= 0:
        raise SystemExit("PUBLIC_PROBE_POLICY_INVALID")
    output = Path(args.output).resolve()
    runtime = Path(__file__).resolve().parents[1] / "runtime"
    if not output.is_relative_to(runtime.resolve()) or output.exists():
        raise SystemExit("PROBE_OUTPUT_MUST_BE_NEW_IGNORED_RUNTIME_FILE")
    result = probe(args.base_url, args.instrument, args.timeout)
    if args.validate_adapter:
        if args.multiplier is None or args.publication_delay_ms is None:
            raise SystemExit("ADAPTER_PROBE_REQUIRES_EXPLICIT_RULE_ASSUMPTIONS")
        from datetime import timedelta
        from decimal import Decimal
        from factorforge.trading.adapters.binance.market import BinanceMarket
        if Decimal(args.multiplier) <= 0 or args.publication_delay_ms < 0:
            raise SystemExit("ADAPTER_PROBE_RULE_ASSUMPTIONS_INVALID")
        market = BinanceMarket(args.base_url, args.timeout, {args.instrument: args.multiplier}, args.publication_delay_ms)
        try:
            spec = next(s for s in market.instrument_specs() if s.key.instrument_id == args.instrument)
            now = datetime.now(timezone.utc)
            points = market.latest_points(spec.key)
            trades = market.trades(spec.key, 2)
            bars = market.candles(spec.key, "1m", now - timedelta(minutes=3), now)
            result["adapter"] = {"decimal_rules_parsed": True, "price_roles": sorted(p.kind for p in points),
                "trade_count": len(trades), "completed_candles": sum(c.final for c in bars),
                "assumptions": "EXPERIMENT_ONLY_NOT_ACCOUNT_APPROVAL"}
        finally:
            market.client.close()
    output.parent.mkdir(parents=True, exist_ok=True)
    output.write_text(json.dumps(result, indent=2) + "\n", encoding="utf-8")
    print(json.dumps(result, indent=2))


if __name__ == "__main__":
    main()
