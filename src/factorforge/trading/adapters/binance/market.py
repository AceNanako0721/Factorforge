"""USD-M public REST mapping. No key, signing secret or strategy dependency."""
from datetime import datetime, timedelta, timezone
from decimal import Decimal
from hashlib import sha256
import json
import time

import httpx

from factorforge.trading.domain.errors import TradingError
from factorforge.trading.domain.models import InstrumentKey, InstrumentSpec, MarketPoint, Candle


class BinanceMarket:
    def __init__(self, base_url, timeout, verified_multipliers, publication_delay_ms, client=None):
        # No multiplier or data-publication assumption is silently guessed.
        self.client = client or httpx.Client(base_url=base_url, timeout=timeout, trust_env=False, follow_redirects=False)
        self.multipliers, self.publication_delay = verified_multipliers, publication_delay_ms
        self.specs = {}

    def _get(self, path, params=None):
        try:
            result = self.client.get(path, params=params)
            result.raise_for_status()
            return result.json()
        except (httpx.HTTPError, ValueError):
            raise TradingError("VENUE_MARKET_UNAVAILABLE", 503) from None

    def instrument_specs(self):
        payload = self._get("/fapi/v1/exchangeInfo")
        observed = datetime.now(timezone.utc)
        results = []
        for item in payload.get("symbols", []):
            if item.get("contractType") != "PERPETUAL" or item.get("status") != "TRADING":
                continue
            multiplier = self.multipliers.get(item["symbol"])
            if multiplier is None:
                continue
            filters = {f["filterType"]: f for f in item["filters"]}
            needed = {"PRICE_FILTER", "LOT_SIZE", "MIN_NOTIONAL"}
            if not needed <= filters.keys():
                continue
            digest = sha256(json.dumps({"filters": filters, "multiplier": multiplier}, sort_keys=True).encode()).hexdigest()[:24]
            key = InstrumentKey(venue="BINANCE", product="LINEAR_PERPETUAL", instrument_id=item["symbol"])
            spec = InstrumentSpec(key=key, version="bn-" + digest, valid_from=observed,
                                  price_tick=filters["PRICE_FILTER"]["tickSize"], quantity_step=filters["LOT_SIZE"]["stepSize"],
                                  contract_multiplier=multiplier, quote_currency=item["quoteAsset"], settlement_currency=item["marginAsset"],
                                  min_notional=filters["MIN_NOTIONAL"]["notional"],
                                  capabilities=set(item.get("orderTypes", [])) & {"LIMIT", "MARKET"},
                                  price_roles={"BID", "ASK", "MARK", "LAST", "INDEX"})
            self.specs[key.code()] = spec
            results.append(spec)
        return results

    def latest_points(self, key):
        if key.code() not in self.specs:
            raise TradingError("INSTRUMENT_RULES_UNVERIFIED", 423)
        book = self._get("/fapi/v1/ticker/bookTicker", {"symbol": key.instrument_id})
        mark = self._get("/fapi/v1/premiumIndex", {"symbol": key.instrument_id})
        received = datetime.now(timezone.utc)
        spec = self.specs[key.code()]
        result = []
        for kind, raw, stamp in (("BID", book["bidPrice"], book["time"]), ("ASK", book["askPrice"], book["time"]),
                                 ("MARK", mark["markPrice"], mark["time"]), ("INDEX", mark["indexPrice"], mark["time"])):
            observed = datetime.fromtimestamp(stamp / 1000, timezone.utc)
            result.append(MarketPoint(instrument_key=key, source_id="binance-public", kind=kind,
                                     observed_at=observed, received_at=received, available_at=max(received, observed),
                                     value=raw, currency=spec.quote_currency, quality="VALID", spec_version=spec.version))
        return result

    def candles(self, key, interval, start, end):
        if key.code() not in self.specs:
            raise TradingError("INSTRUMENT_RULES_UNVERIFIED", 423)
        result, cursor = [], int(start.timestamp() * 1000)
        end_ms = int(end.timestamp() * 1000)
        now = datetime.now(timezone.utc)
        while cursor < end_ms:
            rows = self._get("/fapi/v1/klines", {"symbol": key.instrument_id, "interval": interval,
                                              "startTime": cursor, "endTime": end_ms - 1, "limit": 1000})
            if not rows:
                break
            for row in rows:
                close = datetime.fromtimestamp((row[6] + 1) / 1000, timezone.utc)
                if close > end:
                    continue
                result.append(Candle(instrument_key=key, interval=interval,
                                     open_at=datetime.fromtimestamp(row[0] / 1000, timezone.utc), close_at=close,
                                     available_at=close + timedelta(milliseconds=self.publication_delay),
                                     open=row[1], high=row[2], low=row[3], close=row[4], volume=row[5],
                                     source_id="binance-public", final=close <= now, revision=0))
            next_cursor = rows[-1][6] + 1
            if next_cursor <= cursor:
                raise TradingError("VENUE_PAGINATION_CONFLICT", 503)
            cursor = next_cursor
        return result

    def subscribe_updates(self, key, polling_seconds=None):
        if polling_seconds is None or polling_seconds <= 0:
            raise TradingError("POLLING_POLICY_REQUIRED")
        while True:
            yield self.latest_points(key)
            time.sleep(polling_seconds)
