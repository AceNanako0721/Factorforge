"""Capacity, clock, market collection, public rejections and live evidence gates."""
from datetime import timedelta
from decimal import Decimal

from fastapi.testclient import TestClient
import httpx
import pytest

from factorforge.trading.domain.models import OperationalPolicy
from factorforge.trading.adapters.health import OperationalProbe
from factorforge.trading.domain.readiness import LiveReadiness
from factorforge.trading.domain.errors import TradingError
from factorforge.trading.api.app import create_app
from factorforge.trading.adapters.binance.probes import account_probe
from factorforge.trading.workers.market import MarketCollector


@pytest.mark.parametrize("failure,expected", [("disk", "DISK_CAPACITY"), ("clock", "CLOCK_UNVERIFIED"),
    ("audit", "AUDIT_RETENTION_CAPACITY"), ("queue", "EXECUTION_QUEUE_CAPACITY"), ("age", "EXECUTION_QUEUE_AGE")])
def test_real_probe_checks_each_registered_budget(harness, tmp_path, failure, expected):
    h = harness
    h.submit()
    run = h.run()
    run.policy.operational = OperationalPolicy(min_disk_bytes=1 if failure != "disk" else 10**18,
        max_clock_skew_seconds="1", max_audit_records=1 if failure == "audit" else 100,
        max_pending_commands=1 if failure == "queue" else 100, max_command_age_seconds=10, lease_seconds=10)
    if failure == "age":
        run.clock += timedelta(seconds=11)
    issues = OperationalProbe(tmp_path, lambda: None if failure == "clock" else "0").check(run)
    assert any(expected in value for value in issues)


def test_public_invalid_request_is_recorded_without_input_body(harness):
    h = harness
    client = TestClient(create_app(h.service, {"test": h.principal}))
    body = {**h.command().model_dump(mode="json"), "order": h.request().model_dump(mode="json"), "private_payload": "do not copy"}
    response = client.post("/api/v2/trading/orders", json=body, headers={"Authorization": "Bearer test"})
    assert response.status_code == 422
    assert h.run().audit[-1]["action"] == "REQUEST_REJECTED"
    assert "do not copy" not in h.run().model_dump_json() and "private_payload" not in response.text


def test_account_probe_only_queries_and_never_claims_approval():
    calls = []
    class QueryOnly:
        def request(self, method, path, params):
            calls.append((method, path))
            if path.endswith("account"):
                return {"canTrade": True}
            if path.endswith("dual"):
                return {"dualSidePosition": False}
            if path.endswith("multiAssetsMargin"):
                return {"multiAssetsMargin": False}
            return []
    result = account_probe(QueryOnly())
    assert result["execution_approved"] is False and result["protection_submit_verified"] is False
    assert all(method == "GET" for method, path in calls)


def readiness(h):
    return LiveReadiness(account_id=h.key.account_id, approved_endpoint="https://venue.invalid", valid_until=h.run().clock + timedelta(days=1),
        policy_version="policy-test", account_probe_ref="fixture", ordinary_probe_ref="fixture", protection_probe_ref="fixture",
        egress_isolation_ref="fixture", official_runbook_exercise_ref="fixture", risk_calibration_ref="fixture", operator_id="operator",
        reviewer_id="reviewer", approved_instrument_versions={h.instrument.code(): "rules-test"},
        overlapping_protections=True, atomic_protection_modify=False)


@pytest.mark.parametrize("failure", ["sim", "same-reviewer", "expiry", "missing-evidence", "wrong-endpoint", "observe"])
def test_live_admission_requires_independent_bound_evidence(harness, failure):
    h = harness
    run = h.run()
    run.run_key.environment = "LIVE"
    run.policy.operational = OperationalPolicy(min_disk_bytes=1, max_clock_skew_seconds="1", max_audit_records=100,
        max_pending_commands=10, max_command_age_seconds=10, lease_seconds=10)
    for gate in (run.policy.daily_loss, run.policy.drawdown, run.policy.consecutive_loss):
        gate.mode = "ENFORCE"
    record = readiness(h)
    record.require(run, run.clock, "https://venue.invalid")
    endpoint = "https://venue.invalid"
    if failure == "sim":
        run.run_key.environment = "SIM"
    elif failure == "same-reviewer":
        record.reviewer_id = record.operator_id
    elif failure == "expiry":
        record.valid_until = run.clock
    elif failure == "missing-evidence":
        record.protection_probe_ref = ""
    elif failure == "wrong-endpoint":
        endpoint = "https://another.invalid"
    else:
        run.policy.daily_loss.mode = "OBSERVE"
    with pytest.raises(TradingError, match="LIVE_ADMISSION_NOT_VERIFIED"):
        record.require(run, run.clock, endpoint)


def test_collector_runs_through_http_and_does_not_match_orders(harness):
    h = harness
    api = TestClient(create_app(h.service, {"test": h.principal}), headers={"Authorization": "Bearer test"})
    from datetime import datetime, timezone
    class Public:
        def instrument_specs(self):
            return list(h.run().specs.values())
        def latest_points(self, key):
            at = datetime.now(timezone.utc)
            return [p.model_copy(update={"observed_at": at, "received_at": at, "available_at": at}) for p in h.run().points.values()]
        def candles(self, key, interval, start, end):
            return []
        def trades(self, key, limit):
            return []
    collector = MarketCollector(Public(), api, h.key.model_dump(mode="json"), trade_limit=10)
    at = h.run().clock
    assert collector.collect("ALPHAUSD", "1m", at, at + timedelta(minutes=1)) == (4, 0)
    assert h.run().clock > at and not h.run().fills


def test_api_profile_physically_omits_signing_secrets_and_rejects_canonical_execution_config(tmp_path):
    import tomllib
    from pathlib import Path
    from tools.prepare_trading_profile import prepare, serialize
    from factorforge.trading.bootstrap import load_config
    root = Path(__file__).resolve().parents[2]
    value = tomllib.loads((root / "config/config.example.toml").read_text())
    value["services"]["database_url"] = "fixture-database"
    value["services"]["execution_database_url"] = "fixture-execution-login"
    value["credentials"].update(exchange_api_key="test-key", exchange_api_secret="test-secret", trading_api_token="test")
    value["trading"].update(account_id="account-test", principal_id="tester")
    value["trading"]["account_policy"] = {"stress_scenarios": [{"scenario_id": "exit-test",
        "moves": {"TEST:ALPHAUSD": "-0.1"}, "exit_cost_fraction": "0.01"}]}
    from datetime import datetime, timezone
    value["trading"]["live_readiness"]["valid_until"] = datetime(2030, 1, 1, tzinfo=timezone.utc)
    canonical, profile = tmp_path / "source.toml", tmp_path / "api.toml"
    canonical.write_text(serialize(value), encoding="utf-8")
    prepare(canonical, profile)
    text = profile.read_text()
    parsed = tomllib.loads(text)
    assert parsed["trading"] == value["trading"]
    assert "test-key" not in text and "test-secret" not in text and "fixture-execution-login" not in text
    assert load_config(profile)["credentials"] == {"trading_api_token": "test"}
    with pytest.raises(TradingError, match="EXECUTION_CREDENTIALS_IN_API_PROFILE"):
        load_config(canonical)
    with pytest.raises(ValueError, match="PROFILE_ALREADY_EXISTS"):
        prepare(canonical, profile)


def test_market_trade_receipt_dedup_and_protection_query_use_typed_http_response(harness):
    from factorforge.trading.api.dto import MarketSnapshot
    from factorforge.trading.domain.models import MarketTrade
    from factorforge.trading.application.market import ingest_snapshot
    h = harness
    at = h.run().clock
    trade = MarketTrade(external_id="public-trade", instrument_key=h.instrument, source_id="fixture", observed_at=at,
        received_at=at, available_at=at, price="100", quantity="1", spec_version="rules-test")
    def ingest(item):
        body = MarketSnapshot(**h.command().model_dump(), at=at, points=list(h.run().points.values()), trades=[item])
        return ingest_snapshot(h.service, h.principal, body)
    ingest(trade)
    ingest(trade)
    assert len(h.run().market_trades) == 1 and not h.run().fills
    with pytest.raises(TradingError, match="MARKET_TRADE_ID_CONFLICT"):
        ingest(trade.model_copy(update={"price": Decimal("101")}))
    h.submit()
    h.dispatch()
    h.frame("100")
    api = TestClient(create_app(h.service, {"test": h.principal}), headers={"Authorization": "Bearer test"})
    params = h.key.model_dump(mode="json")
    response = api.get("/api/v2/trading/market/trades", params={**params, "venue": "TEST", "instrument_id": "ALPHAUSD"})
    assert response.status_code == 200 and response.json()["items"][0]["quantity"] == "1"
    protection = next(iter(h.run().protections.values()))
    response = api.get("/api/v2/trading/protections/" + protection.protection_id, params=params)
    assert response.status_code == 200 and response.json()["cancel_requested"] is False


def test_healthy_replay_checks_the_advanced_frame_clock_before_matching(harness, tmp_path):
    h = harness
    with h.store.transaction(h.key) as run:
        run.policy.operational = OperationalPolicy(min_disk_bytes=1, max_clock_skew_seconds="1", max_audit_records=100,
            max_pending_commands=100, max_command_age_seconds=100, lease_seconds=10)
    h.service.health = OperationalProbe(tmp_path, lambda: "0")
    receipt = h.submit()
    h.dispatch()
    h.frame("100")
    assert h.run().orders[receipt["resource_id"]].state == "FILLED"
    assert h.run().health_checked_at == h.run().clock


def test_dedicated_protection_lane_runs_while_ordinary_lane_is_blocked():
    from threading import Event
    from factorforge.trading.workers.protection_pool import ProtectionPool
    protect_ran, ordinary_waiting, release = Event(), Event(), Event()
    class Protection:
        def tick(self, key):
            if ordinary_waiting.is_set():
                protect_ran.set()
    from threading import Thread
    def ordinary():
        ordinary_waiting.set()
        release.wait(2)
    thread = Thread(target=ordinary)
    thread.start()
    try:
        assert ordinary_waiting.wait(1)
        with ProtectionPool(Protection(), None, 0.01) as pool:
            assert protect_ran.wait(1)
            pool.check()
    finally:
        release.set()
        thread.join()


def test_crossed_public_quotes_are_visible_and_block_new_risk(harness):
    from factorforge.trading.api.dto import MarketSnapshot
    from factorforge.trading.application.market import ingest_snapshot
    h = harness
    points = list(h.run().points.values())
    for point in points:
        if point.kind == "BID":
            point.value = Decimal("101")
    ingest_snapshot(h.service, h.principal, MarketSnapshot(**h.command().model_dump(), at=h.run().clock, points=points))
    assert h.run().points[h.instrument.code() + ":ASK"].quality == "CONFLICT"
    with pytest.raises(TradingError, match="MARKET_DATA_UNUSABLE"):
        h.submit()
    api = TestClient(create_app(h.service, {"test": h.principal}), headers={"Authorization": "Bearer test"})
    for endpoint in ("alerts", "audit", "operational-health"):
        response = api.get("/api/v2/trading/" + endpoint, params=h.key.model_dump(mode="json"))
        assert response.status_code == 200
    assert any(e["code"] == "CROSSED_QUOTES" for e in api.get("/api/v2/trading/alerts", params=h.key.model_dump(mode="json")).json()["items"])


def test_testnet_account_probe_refuses_production_before_any_network(tmp_path):
    from tools.probe_binance_testnet import run_probe
    config = tmp_path / "config.toml"
    calls = []
    client = httpx.Client(base_url="https://fapi.binance.com", transport=httpx.MockTransport(
        lambda request: calls.append(request) or httpx.Response(200, json={})))
    for endpoint in ("https://fapi.binance.com", "https://demo-fapi.binance.com:invalid", "https://demo-fapi.binance.com/foreign"):
        config.write_text('[services]\nexchange_api_url="' + endpoint + '"\n', encoding="utf-8")
        with pytest.raises(TradingError, match="OFFICIAL_TESTNET_ENDPOINT_REQUIRED"):
            run_probe(config, 1, 5000, 1000, 60, client)
    assert not calls


def test_testnet_account_probe_has_no_write_fence_and_only_makes_read_queries(tmp_path):
    from tools.probe_binance_testnet import run_probe
    config = tmp_path / "config.toml"
    config.write_text('[services]\nexchange_api_url="https://demo-fapi.binance.com"\n'
        '[credentials]\nexchange_api_key="test-key"\nexchange_api_secret="test-secret"\n', encoding="utf-8")
    calls = []
    def response(request):
        calls.append(request.method)
        if request.url.path.endswith("account"):
            data = {"canTrade": True}
        elif request.url.path.endswith("dual"):
            data = {"dualSidePosition": False}
        elif request.url.path.endswith("multiAssetsMargin"):
            data = {"multiAssetsMargin": False}
        else:
            data = []
        return httpx.Response(200, json=data)
    client = httpx.Client(base_url="https://demo-fapi.binance.com", transport=httpx.MockTransport(response))
    report = run_probe(config, 1, 5000, 1000, 60, client)
    assert calls == ["GET"] * 5 and report["production_approved"] is False
    assert report["findings"]["execution_approved"] is False
