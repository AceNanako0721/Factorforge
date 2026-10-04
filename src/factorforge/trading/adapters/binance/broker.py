"""Signed USD-M protocol adapter; writes require an independent execution fence.

The adapter has no retry loop. A missing write response is UNKNOWN, including
HTTP 5xx. Secrets and raw venue bodies never appear in public errors or logs.
"""
from datetime import datetime, timedelta, timezone
from decimal import Decimal
from hashlib import sha256
import hmac
import time
from threading import Lock
from urllib.parse import urlencode

import httpx

from factorforge.trading.adapters.binance.protocol import ordinary_request, cancel_request, query_request, protection_request
from factorforge.trading.domain.errors import TradingError, AmbiguousResult
from factorforge.trading.domain.models import Fill, Position, Income, InstrumentKey


class SignedTransport:
    def __init__(self, client, secret_provider, clock, recv_window, fence, request_budget, budget_window_seconds,
                 request_weights=None, priority_request_reserve=0):
        if (recv_window <= 0 or recv_window > 60000 or request_budget <= 0 or budget_window_seconds <= 0
                or not 0 <= priority_request_reserve < request_budget):
            raise TradingError("SIGNED_TRANSPORT_POLICY_INVALID")
        self.client, self.secret_provider, self.clock = client, secret_provider, clock
        self.recv_window, self.fence = recv_window, fence
        self.budget, self.window, self.started, self.used, self.blocked_until = request_budget, budget_window_seconds, time.monotonic(), 0, 0
        self.weights = request_weights
        self.budget_lock = Lock()
        self.priority_reserve = priority_request_reserve
        self.last_rejection_code = None

    def _reserve(self, method, path, priority):
        with self.budget_lock:
            now = time.monotonic()
            if now < self.blocked_until:
                raise TradingError("VENUE_RATE_LIMITED", 429)
            if now - self.started >= self.window:
                self.started, self.used = now, 0
            weight = self.weights.get(method + " " + path) if self.weights is not None else 1
            if not isinstance(weight, int) or weight <= 0:
                raise TradingError("VENUE_REQUEST_WEIGHT_UNVERIFIED", 423)
            if self.used + weight > self.budget - (0 if priority else self.priority_reserve):
                raise TradingError("VENUE_RATE_LIMITED", 429)
            self.used += weight

    def request(self, method, path, params, *, write=False):
        priority = path == "/fapi/v1/algoOrder" or method == "DELETE" or params.get("reduceOnly") == "true"
        if write:
            if self.fence is None:
                raise TradingError("EXECUTOR_FENCE_REQUIRED", 423)
            self.fence()  # checked again at the final boundary before signature/HTTP
        api_key, signing_secret = self.secret_provider()
        if not api_key or not signing_secret:
            raise TradingError("EXECUTION_CREDENTIALS_REQUIRED", 503)
        self._reserve(method, path, priority)
        pairs = [(k, str(v)) for k, v in sorted(params.items())]
        pairs += [("recvWindow", str(self.recv_window)), ("timestamp", str(int(self.clock().timestamp() * 1000)))]
        payload = urlencode(pairs)
        signature = hmac.new(signing_secret.encode(), payload.encode(), sha256).hexdigest()
        try:
            response = self.client.request(method, path + "?" + payload + "&signature=" + signature,
                                           headers={"X-MBX-APIKEY": api_key})
            reported = response.headers.get("X-MBX-USED-WEIGHT-1M")
            if reported and reported.isdigit():
                with self.budget_lock:
                    self.used = max(self.used, int(reported))
            if response.status_code in {418, 429}:
                retry_after = response.headers.get("Retry-After", str(self.window))
                try:
                    self.blocked_until = time.monotonic() + max(0, float(retry_after))
                except ValueError:
                    self.blocked_until = time.monotonic() + self.window
            if response.status_code >= 500 or response.status_code in {408, 418, 429}:
                if write:
                    raise AmbiguousResult()
                raise TradingError("VENUE_UNAVAILABLE", 503)
            if response.status_code >= 400:
                try:
                    code = response.json().get("code")
                except ValueError:
                    code = None
                self.last_rejection_code = code if isinstance(code, int) else None
                if code in {-2011, -2013}:
                    raise TradingError("ORDER_NOT_FOUND", 404)
                if write and code in {-1000, -1001, -1006, -1007}:
                    raise AmbiguousResult()
                raise TradingError("VENUE_REQUEST_REJECTED", 422)
            return response.json()
        except (httpx.HTTPError, ValueError):
            if write:
                raise AmbiguousResult() from None
            raise TradingError("VENUE_QUERY_UNAVAILABLE", 503) from None


class BinanceBroker:
    environment = "LIVE"

    def __init__(self, transport, verified_capabilities=None):
        self.transport, self.capabilities = transport, verified_capabilities or {}

    def get_capabilities(self):
        return {"environment": "LIVE", "live_ready": False, "overlapping_protections": False,
                "atomic_protection_modify": False, **self.capabilities}

    def _require_write(self):
        if not self.capabilities.get("ordinary_and_conditional_verified"):
            raise TradingError("LIVE_CAPABILITIES_UNVERIFIED", 423)

    def _order_result(self, run, order, payload):
        if payload.get("symbol") != order.request.instrument_key.instrument_id or payload.get("clientOrderId") != order.client_order_id:
            raise AmbiguousResult()
        mapped = {"NEW": "ACKNOWLEDGED", "PARTIALLY_FILLED": "PARTIALLY_FILLED", "FILLED": "FILLED",
                  "CANCELED": "CANCELED", "EXPIRED": "CANCELED", "EXPIRED_IN_MATCH": "CANCELED", "REJECTED": "REJECTED"}
        state = mapped.get(payload.get("status"))
        if state is None:
            raise AmbiguousResult()
        result = order.model_copy(deep=True)
        result.external_order_id, result.state = str(payload["orderId"]), state
        result.filled_quantity = Decimal(payload.get("executedQty", "0"))
        fills = self._fills(run, order.request.instrument_key, result.external_order_id)
        return result, fills

    def submit_order(self, run, order):
        self._require_write()
        method, path, params = ordinary_request(order)
        try:
            payload = self.transport.request(method, path, params, write=True)
        except TradingError as error:
            if error.code != "VENUE_REQUEST_REJECTED":
                raise
            return order.model_copy(update={"state": "REJECTED"}), []
        return self._decode(run, order, payload)

    def _decode(self, run, order, payload):
        try:
            return self._order_result(run, order, payload)
        except (KeyError, ValueError, TypeError, ArithmeticError):
            raise AmbiguousResult() from None

    def query_order(self, run, client_order_id):
        order = next((o for o in run.orders.values() if o.client_order_id == client_order_id), None)
        if order is None:
            raise TradingError("ORDER_NOT_FOUND", 404)
        if order.source_protection_id:
            raw = self.transport.request("GET", "/fapi/v1/order", {
                "symbol": order.request.instrument_key.instrument_id, "orderId": order.external_order_id})
            if str(raw.get("orderId")) != order.external_order_id or raw.get("symbol") != order.request.instrument_key.instrument_id:
                raise AmbiguousResult()
            # A conditional order supplied this physical ordinary order ID;
            # its venue client ID differs from our stable local fact ID.
            return self._decode(run, order, {**raw, "clientOrderId": order.client_order_id})
        method, path, params = query_request(order)
        return self._decode(run, order, self.transport.request(method, path, params))

    def cancel_order(self, run, order):
        self._require_write()
        method, path, params = cancel_request(order)
        return self._decode(run, order, self.transport.request(method, path, params, write=True))

    def _fills(self, run, key, order_id=None):
        rows = []
        now = self.transport.clock()
        start = run.venue_facts_cursor_at or run.facts_start_at
        windows = [(None, None)]
        if order_id is None and start:
            # Fail explicitly when the provider's retained history cannot
            # prove completeness; do not silently fill the gap with zero.
            if (now - start).total_seconds() > 90 * 86400:
                raise TradingError("VENUE_HISTORY_ARCHIVE_REQUIRED", 423)
            windows = []
            while start <= now:
                end = min(start + timedelta(days=7) - timedelta(milliseconds=1), now)
                windows.append((start, end))
                start = end + timedelta(milliseconds=1)
        for opening, closing in windows:
            params = {"symbol": key.instrument_id, "limit": 1000}
            if order_id is not None:
                params["orderId"] = order_id
            if opening:
                params.update(startTime=int(opening.timestamp() * 1000), endTime=int(closing.timestamp() * 1000))
            cursor = None
            while True:
                page = self.transport.request("GET", "/fapi/v1/userTrades", params)
                if not isinstance(page, list):
                    raise TradingError("VENUE_FILL_FORMAT_INVALID", 503)
                beyond = False
                for item in page:
                    if order_id is not None and str(item["orderId"]) != str(order_id):
                        continue  # a venue must not allocate another order's receipts to this intent
                    at = datetime.fromtimestamp(int(item["time"]) / 1000, timezone.utc)
                    if closing and at > closing:
                        beyond = True
                        continue
                    if (opening and at < opening) or (run.facts_start_at and at < run.facts_start_at):
                        continue
                    rows.append(Fill(external_fill_id=str(item["id"]), external_order_id=str(item["orderId"]), instrument_key=key,
                        side=item["side"], quantity=item["qty"], price=item["price"], fee=item["commission"],
                        fee_currency=item["commissionAsset"], happened_at=at, received_at=max(at, now)))
                if len(page) < 1000 or beyond:
                    break
                following = int(page[-1]["id"]) + 1
                if cursor is not None and following <= cursor:
                    raise TradingError("VENUE_PAGINATION_CONFLICT", 503)
                params.pop("startTime", None)
                params.pop("endTime", None)
                cursor, params["fromId"] = following, following
        return rows

    def list_fills(self, run):
        return [fill for spec in run.specs.values() for fill in self._fills(run, spec.key)]

    def list_open_orders(self, run):
        raw = self.transport.request("GET", "/fapi/v1/openOrders", {})
        results = []
        for item in raw:
            local = next((o for o in run.orders.values() if o.client_order_id == item["clientOrderId"]), None)
            if local is None:
                local = next((o for o in run.orders.values() if o.source_protection_id and o.external_order_id == str(item["orderId"])
                    and o.request.instrument_key.instrument_id == item["symbol"]), None)
            if local and local.source_protection_id:
                item = {**item, "clientOrderId": local.client_order_id}
            if local is None:
                if any(p.exit_order_id == str(item["orderId"]) and p.instrument_key.instrument_id == item["symbol"]
                       for p in run.protections.values()):
                    continue  # the fact synchronizer creates its local exit order
                raise TradingError("EXTERNAL_OPEN_ORDER_UNALLOCATED", 423)
            results.append(self._order_result(run, local, item)[0])
        return results

    def get_positions(self, run):
        raw = self.transport.request("GET", "/fapi/v3/positionRisk", {})
        result = []
        for item in raw:
            if item.get("positionSide") != "BOTH":
                raise TradingError("HEDGE_MODE_UNVERIFIED", 423)
            key = InstrumentKey(venue="BINANCE", product="LINEAR_PERPETUAL", instrument_id=item["symbol"])
            amount = Decimal(item["positionAmt"])
            result.append(Position(instrument_key=key, owner_id=run.owners.get(key.code(), "UNALLOCATED"), quantity=amount,
                average_entry=Decimal(item["entryPrice"]) if amount else None, protection_state="UNKNOWN" if amount else "CLOSED"))
        return result

    def get_account(self, run):
        from factorforge.trading.adapters.binance.probes import require_account_configuration
        raw = self.transport.request("GET", "/fapi/v3/account", {})
        require_account_configuration(self.transport)
        return {"equity": Decimal(raw["totalMarginBalance"]), "available_margin": Decimal(raw["availableBalance"]),
                "cash": Decimal(raw["totalWalletBalance"]), "observed_at": self.transport.clock()}

    def get_income(self, run):
        kinds = {"FUNDING_FEE": "FUNDING", "TRANSFER": "TRANSFER", "INSURANCE_CLEAR": "SETTLEMENT",
                 "DELIVERED_SETTELMENT": "SETTLEMENT"}
        rows, page = [], 1
        start = run.venue_facts_cursor_at or run.facts_start_at or min((o.created_at for o in run.orders.values()), default=run.clock)
        while True:
            raw = self.transport.request("GET", "/fapi/v1/income", {"startTime": int(start.timestamp() * 1000),
                "endTime": int(self.transport.clock().timestamp() * 1000), "page": page, "limit": 1000})
            for item in raw:
                kind = item["incomeType"]
                if kind in {"COMMISSION", "REALIZED_PNL"}:
                    continue  # these already belong to the fill ledger
                if kind not in kinds:
                    raise TradingError("VENUE_INCOME_TYPE_UNVERIFIED", 423)
                key = InstrumentKey(venue="BINANCE", product="LINEAR_PERPETUAL", instrument_id=item["symbol"]) if item.get("symbol") else None
                rows.append(Income(external_id=kind + "-" + str(item["tranId"]), kind=kinds[kind], amount=item["income"],
                    currency=item["asset"], happened_at=datetime.fromtimestamp(int(item["time"]) / 1000, timezone.utc),
                    instrument_key=key, evidence_ref="binance-income:" + str(item["tranId"])))
            if len(raw) < 1000:
                break
            page += 1
        return rows

    def submit_protection(self, run, protection):
        self._require_write()
        position = run.positions[protection.instrument_key.code()]
        method, path, params = protection_request(protection.instrument_key, protection.plan,
            "SELL" if position.quantity > 0 else "BUY", protection.protection_id)
        raw = self.transport.request(method, path, params, write=True)
        try:
            return protection.model_copy(update={"external_id": str(raw["algoId"]), "state": "PENDING"})
        except (KeyError, ValueError, TypeError):
            raise AmbiguousResult() from None

    def _protection(self, run, identifier):
        if identifier in run.protections:
            return run.protections[identifier]
        candidates = [p for p in run.protections.values() if p.external_id == identifier]
        if len(candidates) != 1:
            raise TradingError("PROTECTION_NOT_FOUND_OR_AMBIGUOUS", 404)
        return candidates[0]

    def query_protection(self, run, identifier):
        item = self._protection(run, identifier)
        # Venue algo IDs are symbol-scoped; our client ID is account-unique.
        params = {"clientAlgoId": item.protection_id}
        raw = self.transport.request("GET", "/fapi/v1/algoOrder", params)
        try:
            position = run.positions.get(item.instrument_key.code())
            if (raw.get("clientAlgoId") != item.protection_id or raw.get("symbol") != item.instrument_key.instrument_id
                or raw.get("algoType") != "CONDITIONAL" or raw.get("orderType") != "STOP_MARKET"
                or raw.get("positionSide") != "BOTH"
                or raw.get("workingType") != ("MARK_PRICE" if item.plan.trigger_kind == "MARK" else "CONTRACT_PRICE")
                or (position and position.quantity and raw.get("side") != ("SELL" if position.quantity > 0 else "BUY"))
                or str(raw.get("reduceOnly")).lower() != "true" or Decimal(raw["quantity"]) != item.plan.covered_quantity
                or Decimal(raw["triggerPrice"]) != item.plan.trigger_price
                or (item.external_id is not None and str(raw["algoId"]) != item.external_id)):
                raise AmbiguousResult()
            states = {"NEW": "ACTIVE_VERIFIED", "TRIGGERED": "TRIGGERED", "FINISHED": "CLOSED",
                      "CANCELED": "CLOSED", "REJECTED": "CLOSED", "EXPIRED": "CLOSED"}
            exit_id = raw.get("actualOrderId")
            return item.model_copy(update={"external_id": str(raw["algoId"]), "state": states.get(raw.get("algoStatus"), "UNKNOWN"),
                "exit_order_id": str(exit_id) if exit_id not in {None, "", 0, "0"} else None,
                "verified_at": self.transport.clock()})
        except (KeyError, ValueError, TypeError, ArithmeticError):
            raise AmbiguousResult() from None

    def cancel_protection(self, run, identifier):
        self._require_write()
        item = self._protection(run, identifier)
        params = {"clientAlgoId": item.protection_id}
        return self.transport.request("DELETE", "/fapi/v1/algoOrder", params, write=True)
