"""No venue credentials: kernel boundaries, revocation and recovery receipts."""
from datetime import timedelta
from decimal import Decimal
import base64
import os
from pathlib import Path
import socket
import subprocess
import sys
from threading import Thread

import pytest

from factorforge.trading.adapters.isolation import RevocableEgress, TESTNET_HOST
from factorforge.trading.application.venue import synchronize
from factorforge.trading.domain.errors import TradingError
from factorforge.trading.domain.external import import_external
from factorforge.trading.domain.models import ExternalFact, Fill
from factorforge.trading.workers.protection import ProtectionWorker
from test_feedback import Facts
from factorforge.trading.adapters.sim.broker import SimBroker
from factorforge.trading.adapters.binance.broker import BinanceBroker
from factorforge.trading.domain.accounting import apply_fill


def test_revocation_closes_established_tunnel_and_denies_future_connections(tmp_path, monkeypatch):
    if not hasattr(socket, "AF_UNIX") or os.name != "posix":
        pytest.skip("Linux Unix-socket gateway")
    peer, venue = socket.socketpair()
    monkeypatch.setattr(socket, "create_connection", lambda *args, **kwargs: venue)
    gateway = RevocableEgress(tmp_path / "gate.sock")
    token = gateway.issue()
    def connect(host=TESTNET_HOST):
        client = socket.socket(socket.AF_UNIX)
        client.settimeout(3)
        client.connect(gateway.path)
        authorization = base64.b64encode(("executor:" + token).encode()).decode()
        client.sendall((f"CONNECT {host}:443 HTTP/1.1\r\nProxy-Authorization: Basic {authorization}\r\n\r\n").encode())
        return client
    try:
        rejected = connect("fapi.binance.com")
        assert b"403" in rejected.recv(1024)
        rejected.close()
        assert gateway.allowed_connections == 0
        client = connect()
        assert b"200" in client.recv(1024)
        client.sendall(b"authenticated-existing-tunnel")
        peer.settimeout(3)
        assert peer.recv(1024) == b"authenticated-existing-tunnel"
        gateway.revoke(token)
        assert client.recv(1024) == b""
        rejected = connect()
        assert b"403" in rejected.recv(1024)
        rejected.close()
        client.close()
    finally:
        gateway.close()
        peer.close()


def test_landlock_denies_secret_and_proc_with_same_unix_owner(tmp_path):
    if sys.platform != "linux":
        pytest.skip("Linux Landlock")
    secret = tmp_path / "private" / "config.toml"
    secret.parent.mkdir()
    secret.write_text("synthetic credential")
    script = '''
import sys
from factorforge.trading.adapters.isolation import restrict_filesystem
from factorforge.trading.domain.errors import TradingError
try:
    restrict_filesystem(['/usr','/lib','/lib64',sys.prefix,sys.argv[2]])
except TradingError:
    raise SystemExit(77)
for path in (sys.argv[1], '/proc/self/environ'):
    try:
        open(path, 'rb').read()
    except PermissionError:
        continue
    raise SystemExit(1)
'''
    result = subprocess.run([sys.executable, "-c", script, str(secret), str(Path(__file__).resolve().parents[2]/"src")],
                            capture_output=True, text=True, timeout=10)
    if result.returncode == 77:
        pytest.skip("Kernel lacks Landlock; acceptance entrypoint still fails closed")
    assert result.returncode == 0, result.stderr


def test_manual_receipts_imported_once_allow_venue_recovery(harness):
    h = harness
    h.submit()
    h.dispatch()
    h.frame("100")
    original = next(iter(h.run().orders.values()))
    external = Fill(external_fill_id="manual-receipt", external_order_id="manual-order", instrument_key=h.instrument,
        side="SELL", quantity="1", price="99", fee="0.1", fee_currency="USD",
        happened_at=h.run().clock, received_at=h.run().clock)
    cash = h.run().cash - Decimal("1.1")
    channel = Facts(h, original, [external], Decimal("0"), cash, state="FILLED")
    issues = synchronize(h.store, h.key, channel, Decimal("0"), recovery=True)
    assert any(i.startswith("EXTERNAL_FILL_UNALLOCATED:") for i in issues)
    fact = ExternalFact(external_id="manual-adjustment", kind="MANUAL", instrument_key=h.instrument,
        happened_at=h.run().clock, received_at=h.run().clock, before_quantity="1", after_quantity="0",
        cash_delta="-1.1", currency="USD", evidence_ref="official receipt", rule_version="rules-test",
        external_fill_ids=[external.external_fill_id])
    with h.store.transaction(h.key) as run:
        assert import_external(run, fact)
        assert not import_external(run, fact)
        run.recovery_issues = [i for i in run.recovery_issues if not i.startswith("EXTERNAL_OWNERSHIP:")]
    assert synchronize(h.store, h.key, channel, Decimal("0"), recovery=True) == []
    assert synchronize(h.store, h.key, channel, Decimal("0"), recovery=True) == []
    assert h.run().cash == cash and external.external_fill_id not in h.run().fills
    with h.store.transaction(h.key) as run:
        with pytest.raises(TradingError, match="ALREADY_ACCOUNTED"):
            import_external(run, fact.model_copy(update={"external_id": "second-import", "before_quantity": Decimal("0")}))


def test_definitive_stop_rejection_keeps_old_coverage(harness):
    h = harness
    h.submit()
    h.dispatch()
    h.frame("100")
    old = next(iter(h.run().protections.values()))
    with h.store.transaction(h.key) as run:
        run.protections[old.protection_id].state = "ACTIVE_VERIFIED"
        replacement = old.model_copy(update={"protection_id": "replacement", "state": "PENDING",
            "replaces_id": old.protection_id, "plan": old.plan.model_copy(update={"trigger_price": Decimal("91")})})
        run.protections[replacement.protection_id] = replacement
    # Persist a pending intent with an existing cover and a definitive channel
    # rejection; no network or production admission is needed for this case.
    class Rejected(SimBroker):
        def get_capabilities(self):
            return {"overlapping_protections": True}
        def submit_protection(self, run, protection):
            raise TradingError("VENUE_REQUEST_REJECTED", 422)
    ProtectionWorker(h.store, Rejected()).tick(h.key)
    assert h.run().protections["replacement"].state == "CLOSED"
    assert h.run().protections[old.protection_id].state == "ACTIVE_VERIFIED"
    assert h.run().positions[h.instrument.code()].protection_state == "ACTIVE_VERIFIED"
    assert h.run().audit[-1]["action"] == "PROTECTION_REJECTED"


def test_order_scoped_receipts_reject_server_filter_leakage(harness):
    h = harness
    class Unfiltered:
        clock = lambda self: h.run().clock
        def request(self, method, path, params):
            return [{"id": "trade-" + order, "orderId": order, "time": int(h.run().clock.timestamp()*1000),
                "side": "BUY", "qty": "1", "price": "100", "commission": "0", "commissionAsset": "USD"}
                for order in ("requested", "foreign")]
    result = BinanceBroker(Unfiltered())._fills(h.run(), h.instrument, "requested")
    assert len(result) == 1 and result[0].external_order_id == "requested"


def test_external_receipt_cannot_later_be_assigned_and_booked_as_local_fill(harness):
    h = harness
    receipt = h.submit()
    fact = ExternalFact(external_id="manual-open", kind="MANUAL", instrument_key=h.instrument,
        happened_at=h.run().clock, received_at=h.run().clock, before_quantity="0", after_quantity="1", average_entry="100",
        cash_delta="-0.1", currency="USD", evidence_ref="official execution", rule_version="rules-test",
        external_fill_ids=["manual-buy"])
    fill = Fill(external_fill_id="manual-buy", external_order_id="venue-manual", instrument_key=h.instrument,
        side="BUY", quantity="1", price="100", fee="0.1", fee_currency="USD", happened_at=h.run().clock, received_at=h.run().clock)
    with h.store.transaction(h.key) as run:
        import_external(run, fact)
        order = run.orders[receipt["resource_id"]]
        order.external_order_id = fill.external_order_id
        with pytest.raises(TradingError, match="EXTERNAL_FILL_ALREADY_ACCOUNTED"):
            apply_fill(run, order.order_id, fill)
        assert run.positions[h.instrument.code()].quantity == 1 and run.cash == Decimal("999.9")
