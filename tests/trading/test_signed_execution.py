"""Signed protocol and fencing faults use synthetic secrets and a fake venue."""
from datetime import timedelta
from decimal import Decimal
from urllib.parse import parse_qs
from hashlib import sha256
import hmac

import httpx
import pytest

from conftest import Harness
from factorforge.trading.adapters.binance.broker import SignedTransport, BinanceBroker
from factorforge.trading.domain.errors import TradingError, AmbiguousResult
from factorforge.trading.domain.lease import acquire, assert_lease, isolate
from factorforge.trading.workers.executor import ExecutionWorker
from factorforge.trading.workers.protection import ProtectionWorker
from factorforge.trading.domain.models import Protection


def transport(handler, clock, fence=lambda: None, budget=100):
    client = httpx.Client(base_url="https://venue.invalid", transport=httpx.MockTransport(handler))
    return SignedTransport(client, lambda: ("synthetic-key", "synthetic-secret"), clock, 5000, fence, budget, 60)


def test_signature_matches_wire_and_write_timeout_is_not_retried(harness):
    sent = []
    def handler(request):
        raw = request.url.query.decode()
        payload, signature = raw.rsplit("&signature=", 1)
        assert hmac.compare_digest(signature, hmac.new(b"synthetic-secret", payload.encode(), sha256).hexdigest())
        assert request.headers["X-MBX-APIKEY"] == "synthetic-key"
        sent.append(request)
        raise httpx.ReadTimeout("synthetic response lost")
    signer = transport(handler, lambda: harness.run().clock)
    with pytest.raises(AmbiguousResult):
        signer.request("POST", "/fapi/v1/order", {"newClientOrderId": "stable"}, write=True)
    assert len(sent) == 1
    with pytest.raises(TradingError, match="VENUE_QUERY_UNAVAILABLE"):
        signer.request("GET", "/fapi/v1/order", {})


@pytest.mark.parametrize("status", [429, 500, 503])
def test_http_failure_after_write_keeps_ambiguous_semantics(harness, status):
    signer = transport(lambda request: httpx.Response(status, json={"msg": "private response"}), lambda: harness.run().clock)
    with pytest.raises(AmbiguousResult):
        signer.request("POST", "/fapi/v1/order", {}, write=True)


def test_rate_budget_and_missing_fence_prevent_outbound(harness):
    calls = []
    signer = transport(lambda request: calls.append(request) or httpx.Response(200, json={}), lambda: harness.run().clock,
                       fence=None, budget=1)
    with pytest.raises(TradingError, match="FENCE_REQUIRED"):
        signer.request("POST", "/fapi/v1/order", {}, write=True)
    assert not calls
    signer.request("GET", "/fapi/v3/account", {})
    with pytest.raises(TradingError, match="RATE_LIMITED"):
        signer.request("GET", "/fapi/v3/account", {})
    assert len(calls) == 1


def test_cancel_fill_race_preserves_final_fill_and_fee(harness):
    h = harness
    receipt = h.submit()
    local = h.run().orders[receipt["resource_id"]]
    paths = []
    def handler(request):
        paths.append(request.url.path)
        if request.url.path.endswith("userTrades"):
            return httpx.Response(200, json=[{"id": 7, "orderId": 42, "qty": "1", "price": "100", "commission": "0.1",
                "commissionAsset": "USD", "side": "BUY", "time": int(h.run().clock.timestamp()*1000)}])
        return httpx.Response(200, json={"symbol": "ALPHAUSD", "clientOrderId": local.client_order_id, "orderId": 42,
                                       "executedQty": "1", "status": "CANCELED"})
    broker = BinanceBroker(transport(handler, lambda: h.run().clock), {"ordinary_and_conditional_verified": True})
    result, fills = broker.cancel_order(h.run(), local)
    # Adapter contract is independent from actual worker environment gating.
    from factorforge.trading.domain.accounting import apply_fill
    with h.store.transaction(h.key) as run:
        run.orders[local.order_id].external_order_id = result.external_order_id
        apply_fill(run, local.order_id, fills[0])
    assert h.run().orders[local.order_id].state == "FILLED"
    assert h.run().cash == Decimal("999.9")
    assert paths == ["/fapi/v1/order", "/fapi/v1/userTrades"]


def test_lease_expiry_cannot_replace_unisolated_signer(harness):
    h = harness
    at = h.run().clock
    with h.store.transaction(h.key) as run:
        first = acquire(run, "old-worker", at, 1)
    with h.store.transaction(h.key) as run:
        with pytest.raises(TradingError, match="PREVIOUS_EXECUTOR_NOT_ISOLATED"):
            acquire(run, "new-worker", at + timedelta(seconds=2), 1)
    calls = []
    now = at + timedelta(seconds=2)
    old = transport(lambda req: calls.append(req) or httpx.Response(200, json={}), lambda: now,
                    fence=lambda: assert_lease(h.store.read(h.key), "old-worker", first, now))
    with pytest.raises(TradingError, match="LEASE_LOST"):
        old.request("POST", "/fapi/v1/order", {}, write=True)
    assert not calls
    with h.store.transaction(h.key) as run:
        isolate(run, first, "test egress denied and process stopped")
        second = acquire(run, "new-worker", now, 5)
    new = transport(lambda req: calls.append(req) or httpx.Response(200, json={}), lambda: now,
                    fence=lambda: assert_lease(h.store.read(h.key), "new-worker", second, now))
    new.request("POST", "/fapi/v1/order", {}, write=True)
    with pytest.raises(TradingError, match="LEASE_LOST"):
        old.request("POST", "/fapi/v1/order", {}, write=True)
    assert len(calls) == 1 and second > first


@pytest.mark.parametrize("overlap,atomic,success", [(True, False, True), (False, False, False), (False, True, True)])
def test_protection_confirmation_precedes_cancel_and_atomic_support_is_required(harness, overlap, atomic, success):
    h = harness
    h.submit()
    h.dispatch()
    h.frame("100")
    old = next(iter(h.run().protections.values()))
    replacement = Protection(protection_id="new-protection", instrument_key=h.instrument, owner_id="owner-test",
        plan=h.plan(stop="95"), state="PENDING", replaces_id=old.protection_id)
    with h.store.transaction(h.key) as run:
        run.protections[replacement.protection_id] = replacement
    actions = []
    class Channel:
        environment = "SIM"
        def get_capabilities(self):
            return {"overlapping_protections": overlap, "atomic_protection_modify": atomic}
        def submit_protection(self, run, item):
            actions.append("submit")
            return item
        def query_protection(self, run, identifier):
            actions.append("confirm")
            if identifier == old.protection_id:
                return old.model_copy(update={"state": "CLOSED" if "cancel" in actions else "ACTIVE_VERIFIED"})
            return replacement.model_copy(update={"state": "ACTIVE_VERIFIED", "verified_at": run.clock})
        def cancel_protection(self, run, identifier):
            assert h.run().protections["new-protection"].state == "ACTIVE_VERIFIED"
            actions.append("cancel")
        def replace_protection_atomic(self, run, old, item):
            actions.append("atomic")
            return item.model_copy(update={"state": "ACTIVE_VERIFIED", "verified_at": run.clock})
    ProtectionWorker(h.store, Channel()).tick(h.key)
    if success:
        assert h.run().protections[old.protection_id].state == "CLOSED"
        assert actions == (["submit", "confirm", "cancel", "confirm"] if overlap else ["atomic"])
    else:
        assert h.run().protections[old.protection_id].state == "ACTIVE_VERIFIED"
        assert h.run().protections["new-protection"].state == "UNKNOWN"
        assert not actions


def test_recovery_query_failure_keeps_recovery_check(harness):
    from factorforge.trading.application.recovery import recover_from_broker
    from factorforge.trading.adapters.sim.broker import SimBroker
    class Down(SimBroker):
        def get_account(self, run):
            raise TradingError("VENUE_UNAVAILABLE", 503)
    with pytest.raises(TradingError):
        recover_from_broker(harness.store, harness.key, Down(), Decimal("0.01"))
    assert harness.run().state == "RECOVERY_CHECK"


def test_explicit_venue_rejection_is_not_unknown_but_timeout_code_is(harness):
    h = harness
    receipt = h.submit()
    order = h.run().orders[receipt["resource_id"]]
    broker = BinanceBroker(transport(lambda req: httpx.Response(400, json={"code": -1013}), lambda: h.run().clock),
                           {"ordinary_and_conditional_verified": True})
    assert broker.submit_order(h.run(), order)[0].state == "REJECTED"
    broker = BinanceBroker(transport(lambda req: httpx.Response(400, json={"code": -1007}), lambda: h.run().clock),
                           {"ordinary_and_conditional_verified": True})
    with pytest.raises(AmbiguousResult):
        broker.submit_order(h.run(), order)


def test_concurrent_ordinary_requests_cannot_consume_protection_reserve(harness):
    from concurrent.futures import ThreadPoolExecutor
    calls = []
    client = httpx.Client(base_url="https://venue.invalid", transport=httpx.MockTransport(
        lambda request: calls.append(request.url.path) or httpx.Response(200, json={})))
    signer = SignedTransport(client, lambda: ("synthetic-key", "synthetic-secret"), lambda: harness.run().clock,
        5000, lambda: None, 2, 60, priority_request_reserve=1)
    def ordinary(_):
        try:
            signer.request("GET", "/fapi/v1/order", {})
            return True
        except TradingError:
            return False
    with ThreadPoolExecutor(max_workers=4) as pool:
        results = list(pool.map(ordinary, range(4)))
    assert sum(results) == 1
    signer.request("POST", "/fapi/v1/algoOrder", {}, write=True)
    assert calls == ["/fapi/v1/order", "/fapi/v1/algoOrder"]


def test_signed_protection_worker_confirms_physical_cover_on_first_poll(harness):
    from factorforge.trading.adapters.memory import MemoryStore
    h = harness
    h.submit()
    h.dispatch()
    h.frame("100")
    run = h.run()
    run.run_key.environment, run.execution_mode = "LIVE", "LIVE"
    item = next(iter(run.protections.values()))
    item.state, item.verified_at = "PENDING", None
    store = MemoryStore("LIVE")
    store.create(run)
    key, at = run.run_key, run.clock
    with store.transaction(key) as current:
        epoch = acquire(current, "fixture-signer", at, 10)
    response = {"algoId": 42, "clientAlgoId": item.protection_id, "symbol": "ALPHAUSD",
        "algoType": "CONDITIONAL", "orderType": "STOP_MARKET", "positionSide": "BOTH", "side": "SELL",
        "workingType": "MARK_PRICE", "reduceOnly": True, "quantity": "1", "triggerPrice": "90",
        "algoStatus": "NEW", "actualOrderId": "0"}
    calls = []
    def handler(request):
        calls.append(request.method)
        assert parse_qs(request.url.query.decode())["clientAlgoId"] == [item.protection_id]
        return httpx.Response(200, json=response)
    signer = transport(handler, lambda: at, fence=lambda: assert_lease(store.read(key), "fixture-signer", epoch, at))
    broker = BinanceBroker(signer, {"ordinary_and_conditional_verified": True})
    broker.execution_admitted = True  # synthetic fixture, not an account approval
    assert ProtectionWorker(store, broker, "fixture-signer", epoch, lambda: at).tick(key)
    saved = store.read(key).protections[item.protection_id]
    assert calls == ["POST", "GET"] and saved.state == "ACTIVE_VERIFIED" and saved.external_id == "42"
    assert saved.exit_order_id is None
    for field, wrong in (("side", "BUY"), ("workingType", "CONTRACT_PRICE"), ("orderType", "TAKE_PROFIT_MARKET"),
                         ("positionSide", "LONG"), ("algoId", 43), ("quantity", "0.5")):
        original = response[field]
        response[field] = wrong
        with pytest.raises(AmbiguousResult):
            broker.query_protection(store.read(key), item.protection_id)
        response[field] = original
