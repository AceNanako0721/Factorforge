"""Public market collection uses the authenticated trading API, never the DB."""
from datetime import datetime, timedelta, timezone
from decimal import Decimal
from hashlib import sha256
import argparse
import time
import httpx

from factorforge.trading.adapters.binance.market import BinanceMarket
from factorforge.trading.domain.errors import TradingError


class MarketCollector:
    def __init__(self, market, client, run_key, trade_limit):
        self.market, self.client, self.key, self.trade_limit = market, client, run_key, trade_limit

    def _post(self, path, payload):
        response = self.client.post("/api/v2/trading" + path, json=payload)
        if response.status_code >= 400:
            raise TradingError("COLLECTOR_API_REJECTED", response.status_code)
        return response.json()

    def command(self, identity, at):
        response = self.client.get("/api/v2/trading/runs/" + self.key["run_id"], params=self.key)
        if response.status_code >= 400:
            raise TradingError("COLLECTOR_API_REJECTED", response.status_code)
        run = response.json()
        return {"schema_version": "trading-2.0", "request_id": identity, "idempotency_key": identity,
            "run_key": self.key, "expected_version": run["aggregate_version"], "reason": "public market collection",
            "expires_at_utc": (at + timedelta(minutes=1)).isoformat()}

    def collect(self, instrument_id, interval, start, end):
        specs = self.market.instrument_specs()
        spec = next((s for s in specs if s.key.instrument_id == instrument_id), None)
        if spec is None:
            raise TradingError("COLLECTOR_INSTRUMENT_UNVERIFIED", 423)
        response = self.client.get("/api/v2/trading/instruments", params=self.key)
        if response.status_code >= 400:
            raise TradingError("COLLECTOR_API_REJECTED", response.status_code)
        from factorforge.trading.domain.models import InstrumentSpec
        for raw in response.json()["items"]:
            prior = InstrumentSpec.model_validate(raw)
            if prior.key == spec.key and prior.version == spec.version:
                # Keep already registered capability evidence. Public prices
                # neither grant account permissions nor broaden capabilities.
                excluded = {"valid_from", "capabilities", "price_roles"}
                if prior.model_dump(exclude=excluded) != spec.model_dump(exclude=excluded):
                    raise TradingError("COLLECTOR_RULES_CONFLICT", 409)
                spec = prior
                if hasattr(self.market, "specs"):
                    self.market.specs[spec.key.code()] = spec
                break
        # Use the configured run clock; collectors do not advance a replay spec into the past.
        at = datetime.now(timezone.utc)
        # Each acquisition is a new observation, even when economic rules match.
        identity = "rules-" + sha256((spec.version + at.isoformat()).encode()).hexdigest()[:24]
        self._post("/instruments", {**self.command(identity, at), "spec": spec.model_dump(mode="json")})
        points = self.market.latest_points(spec.key)
        trades = self.market.trades(spec.key, self.trade_limit)
        bars = [c for c in self.market.candles(spec.key, interval, start, end) if c.final and c.available_at <= at]
        at = max([at] + [p.available_at for p in points] + [t.available_at for t in trades])
        digest = sha256((spec.version + at.isoformat()).encode()).hexdigest()[:24]
        self._post("/market/snapshots", {**self.command("market-" + digest, at), "at": at.isoformat(),
            "points": [p.model_dump(mode="json") for p in points], "candles": [c.model_dump(mode="json") for c in bars],
            "trades": [t.model_dump(mode="json") for t in trades]})
        return len(points), len(bars)


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument("--config", default="config/config.toml")
    parser.add_argument("--run-id", required=True)
    parser.add_argument("--instrument", required=True)
    parser.add_argument("--interval", required=True)
    parser.add_argument("--lookback-seconds", type=int, required=True)
    parser.add_argument("--poll-seconds", type=float, required=True)
    parser.add_argument("--multiplier", required=True)
    parser.add_argument("--publication-delay-ms", type=int, required=True)
    parser.add_argument("--trade-limit", type=int, required=True)
    parser.add_argument("--once", action="store_true")
    args = parser.parse_args()
    if (args.poll_seconds <= 0 or args.lookback_seconds <= 0 or args.publication_delay_ms < 0
            or Decimal(args.multiplier) <= 0 or not 1 <= args.trade_limit <= 1000):
        raise SystemExit("COLLECTOR_POLICY_INVALID")
    from factorforge.trading.bootstrap import load_config
    config = load_config(args.config)
    if not config["services"]["exchange_api_url"] or not config["services"]["trading_api_url"]:
        raise SystemExit("COLLECTOR_ENDPOINTS_REQUIRED")
    market = BinanceMarket(config["services"]["exchange_api_url"], 10, {args.instrument: args.multiplier}, args.publication_delay_ms)
    with httpx.Client(base_url=config["services"]["trading_api_url"], trust_env=False,
        headers={"Authorization": "Bearer " + config["credentials"]["trading_api_token"]}) as client:
        collector = MarketCollector(market, client, {"environment": config["runtime"]["environment"], "account_id": config["trading"]["account_id"], "run_id": args.run_id}, args.trade_limit)
        while True:
            end = datetime.now(timezone.utc)
            collector.collect(args.instrument, args.interval, end - timedelta(seconds=args.lookback_seconds), end)
            if args.once:
                break
            time.sleep(args.poll_seconds)
