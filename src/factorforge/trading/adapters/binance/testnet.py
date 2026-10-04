"""Bounded capability probes, separate from production LIVE admission.

Only the official futures testnet is reachable. A probe never fills in a
LiveReadiness record and never enables the normal execution-live entrypoint.
"""
from datetime import datetime, timezone
from decimal import Decimal
import json
from pathlib import Path
import subprocess
import sys
import tomllib

import httpx

from factorforge.trading.adapters.binance.broker import BinanceBroker, SignedTransport
from factorforge.trading.adapters.binance.protocol import ordinary_request, protection_request
from factorforge.trading.adapters.isolation import TESTNET_URL, tunnel_transport
from factorforge.trading.adapters.postgres.store import PostgresStore
from factorforge.trading.domain.errors import TradingError, AmbiguousResult
from factorforge.trading.domain.lease import acquire, assert_lease
from factorforge.trading.domain.models import Aggregate, Fill, Income, Order, Position, Protection, RunKey

READ_PATHS = {"/fapi/v3/account", "/fapi/v1/accountConfig", "/fapi/v3/positionRisk", "/fapi/v1/openOrders",
    "/fapi/v1/openAlgoOrders", "/fapi/v1/order", "/fapi/v1/algoOrder", "/fapi/v1/userTrades", "/fapi/v1/income"}


class ProbeTransport(SignedTransport):
    def __init__(self, client, secrets, clock, manifest):
        self.manifest = manifest
        self.store = PostgresStore(manifest["dsn"], "LIVE")
        self.key = RunKey.model_validate(manifest["key"])
        self.epoch = None
        self.drop_next_response = False
        self.write_attempts = 0
        # Probe-only window covers TLS/gateway setup within the 15s HTTP bound;
        # this value is never copied into production transport policy.
        super().__init__(client, secrets, clock, 20000, self.require, 6000, 60, priority_request_reserve=100)

    def require(self):
        if str(self.client.base_url).rstrip("/") != TESTNET_URL:
            raise TradingError("TESTNET_ENDPOINT_REQUIRED", 423)
        if self.clock() >= datetime.fromisoformat(self.manifest["expires_at"]):
            raise TradingError("TESTNET_PERMIT_EXPIRED", 423)
        if not self.manifest.get("manual"):
            assert_lease(self.store.read(self.key), self.manifest["holder"], self.epoch, self.clock())

    def request(self, method, path, params, *, write=False):
        if write:
            self.require()
            if params.get("symbol") not in {None, self.manifest["symbol"]}:
                raise TradingError("TESTNET_INSTRUMENT_SCOPE", 403)
            if self.manifest.get("manual"):
                if not (method == "POST" and path == "/fapi/v1/order" and params.get("reduceOnly") == "true"
                        and params.get("type") == "MARKET" and params.get("newClientOrderId") == self.manifest["manual_id"]):
                    raise TradingError("TESTNET_MANUAL_REDUCE_ONLY", 403)
                actual = next((p for p in super().request("GET", "/fapi/v3/positionRisk", {})
                    if p["symbol"] == self.manifest["symbol"] and p["positionSide"] == "BOTH"), None)
                quantity = Decimal(actual["positionAmt"]) if actual else Decimal(0)
                if (not quantity or Decimal(params["quantity"]) > abs(quantity)
                        or params["side"] != ("SELL" if quantity > 0 else "BUY")):
                    raise TradingError("TESTNET_REDUCTION_NOT_PROVEN", 423)
            else:
                run = self.store.read(self.key)
                if run.policy.version != "EXPERIMENT_ONLY" or run.policy.notional_limit != Decimal(self.manifest["max_notional"]):
                    raise TradingError("TESTNET_POLICY_SCOPE", 423)
                expected = []
                for order in run.orders.values():
                    if method == "POST" and path == "/fapi/v1/order":
                        expected.append(ordinary_request(order)[2])
                    if method == "DELETE" and path == "/fapi/v1/order":
                        expected.append({"symbol": order.request.instrument_key.instrument_id,
                                         "origClientOrderId": order.client_order_id})
                for protection in run.protections.values():
                    if protection.instrument_key.instrument_id != self.manifest["symbol"]:
                        continue
                    position = run.positions.get(protection.instrument_key.code())
                    if method == "POST" and path == "/fapi/v1/algoOrder" and position and position.quantity:
                        expected.append(protection_request(protection.instrument_key, protection.plan,
                            "SELL" if position.quantity > 0 else "BUY", protection.protection_id)[2])
                    if method == "DELETE" and path == "/fapi/v1/algoOrder":
                        expected.append({"clientAlgoId": protection.protection_id})
                if params not in expected:
                    raise TradingError("TESTNET_INTENT_NOT_REGISTERED", 403)
            if "quantity" in params and not 0 < Decimal(params["quantity"]) <= Decimal(self.manifest["max_quantity"]):
                raise TradingError("TESTNET_QUANTITY_LIMIT", 423)
            self.write_attempts += 1
        elif method != "GET" or path not in READ_PATHS:
            raise TradingError("TESTNET_READ_SCOPE", 403)
        result = super().request(method, path, params, write=write)
        if write and method == "POST" and path == "/fapi/v1/order" and self.drop_next_response:
            self.drop_next_response = False
            raise AmbiguousResult()  # response really arrived, deliberately lost at the receiver
        return result


class ProbeBroker(BinanceBroker):
    def _require_write(self):
        self.transport.require()


def signer_main():
    manifest = json.loads(sys.stdin.readline())
    if manifest["endpoint"] != TESTNET_URL:
        raise SystemExit("TESTNET_ENDPOINT_REQUIRED")
    with Path(manifest["config"]).open("rb") as source:
        private = tomllib.load(source)
    if private["services"]["exchange_api_url"].rstrip("/") != TESTNET_URL:
        raise SystemExit("TESTNET_ENDPOINT_REQUIRED")
    credentials = private["credentials"]
    clock = lambda: datetime.now(timezone.utc)
    with httpx.Client(base_url=TESTNET_URL, transport=tunnel_transport(manifest["gateway"], manifest["token"]),
            timeout=15, trust_env=False, follow_redirects=False) as client:
        transport = ProbeTransport(client, lambda: (credentials["exchange_api_key"], credentials["exchange_api_secret"]), clock, manifest)
        transport.store.verify_runtime_role()
        broker = ProbeBroker(transport, {"overlapping_protections": True, "probe_only": True, "live_ready": False})
        for line in sys.stdin:
            try:
                message = json.loads(line)
                operation, args = message["operation"], message.get("args", [])
                if operation == "claim":
                    with transport.store.transaction(transport.key) as run:
                        transport.epoch = acquire(run, manifest["holder"], clock(), run.policy.operational.lease_seconds)
                        run.version += 1
                    result = transport.epoch
                elif operation == "drop_response":
                    transport.drop_next_response, result = True, True
                elif operation == "stats":
                    result = {"write_attempts": transport.write_attempts, "last_rejection_code": transport.last_rejection_code}
                elif operation == "fence_probe":
                    transport.require()
                    result = True
                elif operation == "raw":
                    result = transport.request("GET", args[0], args[1])
                elif operation == "manual_reduce":
                    transport.require()
                    result = transport.request("POST", "/fapi/v1/order", args[0], write=True)
                elif operation in {"submit_order", "cancel_order", "query_order", "submit_protection", "cancel_protection",
                        "query_protection", "list_open_orders", "list_fills", "get_income", "get_positions", "get_account"}:
                    if transport.epoch is not None:
                        with transport.store.transaction(transport.key) as run:
                            acquire(run, manifest["holder"], clock(), run.policy.operational.lease_seconds)
                    run = Aggregate.model_validate(args[0])
                    if run.run_key != transport.key:
                        raise TradingError("TESTNET_ACCOUNT_SCOPE", 403)
                    converted = [run]
                    if len(args) > 1:
                        converted.append(Order.model_validate(args[1]) if operation in {"submit_order", "cancel_order"}
                            else Protection.model_validate(args[1]) if operation == "submit_protection" else args[1])
                    result = getattr(broker, operation)(*converted)
                else:
                    raise TradingError("TESTNET_OPERATION_FORBIDDEN", 403)
                print(json.dumps({"ok": _wire(result)}), flush=True)
            except AmbiguousResult:
                print(json.dumps({"error": "AMBIGUOUS_RESULT"}), flush=True)
            except (TradingError, httpx.HTTPError, ValueError, KeyError, TypeError, OSError) as error:
                print(json.dumps({"error": error.code if isinstance(error, TradingError) else "TESTNET_PROCESS_ERROR"}), flush=True)


def _wire(value):
    if hasattr(value, "model_dump"):
        return value.model_dump(mode="json")
    if isinstance(value, Decimal):
        return str(value)
    if isinstance(value, datetime):
        return value.isoformat()
    if isinstance(value, dict):
        return {k: _wire(v) for k, v in value.items()}
    if isinstance(value, (tuple, list)):
        return [_wire(v) for v in value]
    return value


class RemoteProbeBroker:
    environment, execution_admitted = "LIVE", True

    def __init__(self, manifest, stderr):
        self.process = subprocess.Popen(["unshare", "--user", "--map-root-user", "--net", sys.executable,
            "-m", "factorforge.trading.bootstrap_testnet", "--signer"], stdin=subprocess.PIPE,
            stdout=subprocess.PIPE, stderr=stderr, text=True, bufsize=1)
        self.process.stdin.write(json.dumps(manifest) + "\n")
        self.process.stdin.flush()

    def call(self, operation, *args):
        self.process.stdin.write(json.dumps({"operation": operation, "args": _wire(args)}) + "\n")
        self.process.stdin.flush()
        line = self.process.stdout.readline()
        if not line:
            raise TradingError("TESTNET_SIGNER_STOPPED", 503)
        result = json.loads(line)
        if "error" in result:
            if result["error"] == "AMBIGUOUS_RESULT":
                raise AmbiguousResult()
            raise TradingError(result["error"], 423)
        return result["ok"]

    def get_capabilities(self):
        return {"overlapping_protections": True, "atomic_protection_modify": False, "live_ready": False, "probe_only": True}

    def __getattr__(self, operation):
        def invoke(*args):
            raw = self.call(operation, *args)
            if operation in {"submit_order", "cancel_order", "query_order"}:
                return Order.model_validate(raw[0]), [Fill.model_validate(f) for f in raw[1]]
            if operation in {"submit_protection", "query_protection"}:
                return Protection.model_validate(raw)
            types = {"list_open_orders": Order, "list_fills": Fill, "get_income": Income, "get_positions": Position}
            if operation in types:
                return [types[operation].model_validate(v) for v in raw]
            if operation == "get_account":
                return {k: datetime.fromisoformat(v) if k == "observed_at" else Decimal(v) for k, v in raw.items()}
            return raw
        return invoke

    def close(self):
        self.process.terminate()
        self.process.wait(timeout=10)
        self.process.stdin.close()
        self.process.stdout.close()
