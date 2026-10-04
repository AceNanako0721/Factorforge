"""Official protocol mappings are exercised with a fake HTTP transport only."""
from datetime import timedelta
from datetime import datetime, timezone
import httpx

from factorforge.trading.adapters.binance.market import BinanceMarket
from factorforge.trading.adapters.binance.protocol import ordinary_request, protection_request, protection_query, protection_cancel
from factorforge.trading.domain.models import Order


def test_normal_and_conditional_endpoints_and_reduce_only(harness):
    h = harness
    order = Order(order_id="order-test", client_order_id="client-test", request=h.request(), state="RESERVED", created_at=h.run().clock)
    method, path, body = ordinary_request(order)
    assert (method, path) == ("POST", "/fapi/v1/order")
    assert body["newClientOrderId"] == "client-test" and body["quantity"] == "1"
    method, path, body = protection_request(h.instrument, h.plan(), "SELL", "stop-test")
    assert path == "/fapi/v1/algoOrder" and body["reduceOnly"] == "true"
    assert protection_query("123")[0] == "GET"
    assert protection_cancel("123")[0] == "DELETE"


def test_public_rules_require_verified_multiplier_and_parse_kline(harness):
    h = harness
    opening = int(h.run().clock.timestamp() * 1000)

    def response(request):
        if request.url.path.endswith("exchangeInfo"):
            return httpx.Response(200, json={"symbols": [{"symbol": "ALPHAUSD", "status": "TRADING", "contractType": "PERPETUAL",
                "quoteAsset": "USD", "marginAsset": "USD", "orderTypes": ["LIMIT", "MARKET"],
                "filters": [{"filterType": "PRICE_FILTER", "tickSize": "0.01"}, {"filterType": "LOT_SIZE", "stepSize": "0.1"},
                            {"filterType": "MIN_NOTIONAL", "notional": "1"}]}]})
        return httpx.Response(200, json=[[opening, "100", "101", "99", "100", "5", opening + 59999]])

    client = httpx.Client(base_url="https://fixture.invalid", transport=httpx.MockTransport(response))
    adapter = BinanceMarket("", 1, {"ALPHAUSD": "1"}, 50, client=client)
    specs = adapter.instrument_specs()
    assert len(specs) == 1 and "CONDITIONAL_PROTECTION" not in specs[0].capabilities
    candles = adapter.candles(specs[0].key, "1m", h.run().clock, h.run().clock + timedelta(minutes=1))
    assert len(candles) == 1 and candles[0].final
    assert (candles[0].available_at - candles[0].close_at).total_seconds() == 0.05
    assert BinanceMarket("", 1, {}, 50, client=client).instrument_specs() == []


def test_public_last_trades_and_unchanged_rules_keep_original_effective_time(harness):
    h = harness
    now = int(datetime.now(timezone.utc).timestamp() * 1000)
    def response(request):
        if request.url.path.endswith("bookTicker"):
            return httpx.Response(200, json={"bidPrice": "99", "askPrice": "101", "time": now})
        if request.url.path.endswith("premiumIndex"):
            return httpx.Response(200, json={"markPrice": "100", "indexPrice": "100", "time": now})
        if request.url.path.endswith("price"):
            return httpx.Response(200, json={"price": "100", "time": now})
        return httpx.Response(200, json=[{"id": 7, "price": "100", "qty": "2", "time": now}])
    client = httpx.Client(base_url="https://fixture.invalid", transport=httpx.MockTransport(response))
    adapter = BinanceMarket("", 1, {"ALPHAUSD": "1"}, 0, client)
    adapter.specs = dict(h.run().specs)
    points = adapter.latest_points(h.instrument)
    assert {p.kind for p in points} == {"BID", "ASK", "MARK", "INDEX", "LAST"}
    trades = adapter.trades(h.instrument, 10)
    assert trades[0].external_id == "7" and trades[0].quantity == 2
