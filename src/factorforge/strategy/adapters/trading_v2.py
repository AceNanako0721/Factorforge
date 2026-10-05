"""P1 public HTTP contract only; no execution implementation or exchange secrets."""
from datetime import timedelta
from decimal import Decimal

import httpx

from factorforge.trading.api.dto import TargetRequest
from factorforge.strategy.domain.models import TradingSnapshot, MarketSample, Bar, ZERO, StrategyError, utc

PREFIX = "/api/v2/trading"
TERMINAL = {"FILLED","CANCELED","CANCELLED","REJECTED","EXPIRED"}


class TradingV2:
    def __init__(self, url, token, timeout_seconds, candle_interval, history_seconds, client=None):
        self.client = client or httpx.Client(base_url=url,timeout=timeout_seconds)
        self.headers = {"Authorization":"Bearer "+token}
        self.candle_interval, self.history_seconds = candle_interval, history_seconds

    def _request(self, method, path, **kwargs):
        try:
            response = self.client.request(method,PREFIX+path,headers=self.headers,**kwargs)
        except (httpx.HTTPError, OSError):
            raise StrategyError("TRADING_DELIVERY_UNKNOWN",503) from None
        if response.status_code >= 400:
            # Only stable problem codes; never signed URLs, credentials or raw account responses.
            try:
                code = response.json().get("code","TRADING_REJECTED")
            except ValueError:
                code = "TRADING_UNAVAILABLE"
            raise StrategyError(code,response.status_code)
        return response.json()

    def snapshot(self, objects):
        binding = objects[0].trading_run_key
        if any(o.trading_run_key != binding for o in objects):
            raise StrategyError("MIXED_ACCOUNT_SNAPSHOT")
        params = binding.model_dump()
        for _ in range(3):
            run = self._request("GET","/runs/"+binding.run_id,params={"environment":binding.environment,"account_id":binding.account_id})
            account = self._request("GET","/account",params=params)
            pages = {name:self._request("GET","/"+name,params=params) for name in ("instruments","positions","orders","targets","owners","protections","market/points","fills","income","external-facts","audit")}
            end = self._request("GET","/runs/"+binding.run_id,params={"environment":binding.environment,"account_id":binding.account_id})
            if run["aggregate_version"] == end["aggregate_version"] and all(p["snapshot_version"] == run["aggregate_version"] for p in pages.values()):
                break
        else:
            raise StrategyError("TRADING_SNAPSHOT_CONFLICT",409)
        at = utc(account["observed_at"])
        actual,pending,epochs,versions,specs,samples,bars,protections,averages = {},{},{},{},{},{},{},{},{}
        orders = {o["external_order_id"]:o for o in pages["orders"]["items"] if o["external_order_id"]}
        for obj in objects:
            identity = obj.instrument_key.model_dump()
            spec = next((s for s in pages["instruments"]["items"] if s["key"] == identity),None)
            if spec is None:
                raise StrategyError("INSTRUMENT_NOT_REGISTERED",409)
            specs[obj.object_id] = spec
            positions = [p for p in pages["positions"]["items"] if p["instrument_key"] == identity]
            if any(p["owner_id"] != obj.owner_id and Decimal(p["quantity"]) != 0 for p in positions):
                raise StrategyError("OWNER_CONFLICT",409)
            actual[obj.object_id] = sum((Decimal(p["quantity"]) for p in positions if p["owner_id"] == obj.owner_id),ZERO)
            averages[obj.object_id] = next((p["average_entry"] for p in positions if p["owner_id"] == obj.owner_id),None)
            pending[obj.object_id] = sum((Decimal(o["remaining_quantity"])*(1 if o["side"] == "BUY" else -1) for o in pages["orders"]["items"] if o["instrument_key"] == identity and o["owner_id"] == obj.owner_id and o["state"] not in TERMINAL),ZERO)
            targets = [t for t in pages["targets"]["items"] if t["instrument_key"] == identity]
            owner = next(o for o in pages["owners"]["items"] if o["instrument_key"] == identity)
            if owner["owner_id"] not in {None,obj.owner_id}:
                raise StrategyError("OWNER_CONFLICT",409)
            epochs[obj.object_id] = owner["owner_epoch"]
            versions[obj.object_id] = max((t["target_version"] for t in targets),default=0)
            protections[obj.object_id] = [p for p in pages["protections"]["items"] if p["instrument_key"] == identity and p["owner_id"] == obj.owner_id]
            prices = [p for p in pages["market/points"]["items"] if p["instrument_key"] == identity and p["kind"] == "MARK" and utc(p["available_at"]) <= at]
            if prices:
                point = max(prices,key=lambda p:p["available_at"])
                samples[obj.object_id] = MarketSample(available_at=point["available_at"],price=point["value"],quality=point["quality"] if point["quality"] in {"VALID","STALE"} else "UNKNOWN")
            page = self._request("GET","/market/candles",params={**params,"venue":obj.instrument_key.venue,"instrument_id":obj.instrument_key.instrument_id,"interval":self.candle_interval,"start":at-timedelta(seconds=self.history_seconds),"end":at})
            if page["snapshot_version"] != run["aggregate_version"]:
                raise StrategyError("TRADING_SNAPSHOT_CONFLICT",409)
            bars[obj.object_id] = [Bar(close_at=c["close_at"],available_at=c["available_at"],high=c["high"],low=c["low"],close=c["close"],final=c["final"],quality="VALID") for c in page["items"]]
            # Volatility/liquidity/benchmark are registered independent factor observations,
            # injected by the framework binding, never fabricated from missing P1 fields.
        exits = {p["exit_order_id"] for p in pages["protections"]["items"] if p["exit_order_id"]}
        fills = []
        for fact in pages["fills"]["items"]:
            order = orders.get(fact["external_order_id"],{})
            instrument = fact["instrument_key"]
            code = ":".join(instrument[k] for k in ("venue","product","instrument_id"))
            origin = next((a["request_id"] for a in pages["audit"]["items"] if a["action"] == "TARGET_SET" and a["detail"].get("resource_id") == code and a["detail"].get("target_version") == order.get("target_version")),None)
            fills.append({**fact,"owner_id":order.get("owner_id"),"protection_exit":order.get("order_id") in exits,"source_decision_id":origin})
        other = []
        locks = list(account["risk_locks"])
        managed = [o.instrument_key.model_dump() for o in objects]
        for spec in pages["instruments"]["items"]:
            instrument = spec["key"]
            if instrument in managed:
                continue
            held = sum((Decimal(p["quantity"]) for p in pages["positions"]["items"] if p["instrument_key"] == instrument),ZERO)
            queued = [Decimal(o["remaining_quantity"])*(1 if o["side"] == "BUY" else -1) for o in pages["orders"]["items"] if o["instrument_key"] == instrument and o["state"] not in TERMINAL]
            price = next((Decimal(p["value"]) for p in pages["market/points"]["items"] if p["instrument_key"] == instrument and p["kind"] == "MARK" and p["quality"] == "VALID"),None)
            if held or queued:
                if price is None or spec["settlement_currency"] != account["currency"]:
                    locks.append("UNPRICED_EXTERNAL_EXPOSURE")
                else:
                    # Reserve each pending side independently; opposite unknown orders cannot net away gross risk.
                    for q in [held]+queued:
                        value = q*price*Decimal(spec["contract_multiplier"])
                        other.append((value,abs(value),spec.get("risk_group") or "DEFAULT"))
        decisions = {t["source_decision_id"] for t in pages["targets"]["items"] if t["source_decision_id"]}
        decisions.update(a["request_id"] for a in pages["audit"]["items"] if a["action"] == "TARGET_SET")
        return TradingSnapshot(run_key=binding,version=run["aggregate_version"],at=at,equity=account["equity"],policy_version=account["policy_version"],risk_locks=locks,run_state=run["state"],actual=actual,pending=pending,owner_epochs=epochs,target_versions=versions,specs=specs,samples=samples,bars=bars,fills=fills,incomes=pages["income"]["items"],source_decisions=decisions,external_change=run["state"] != "NORMAL",protections=protections,other_exposures=other,average_entries=averages)

    def prepare(self,obj,item,snapshot):
        decision = item.decision
        body = TargetRequest(schema_version="trading-2.0",request_id=decision.decision_id,idempotency_key=decision.decision_id,
            run_key=obj.trading_run_key.model_dump(),expected_version=snapshot.version,reason="strategy cycle "+decision.cycle_id,
            expires_at_utc=item.expires_at,owner_id=obj.owner_id,instrument_key=obj.instrument_key.model_dump(),
            target_version=item.target_version,target_quantity=decision.target_quantity,owner_epoch=item.owner_epoch,
            policy_version=snapshot.policy_version,spec_version=snapshot.specs[obj.object_id]["version"],
            source_decision_id=decision.decision_id,protection_plan=decision.stop_plan.model_dump(mode="json") if decision.stop_plan else None)
        return body.model_dump(mode="json")

    def deliver(self,obj,item,snapshot):
        body = item.command_payload or self.prepare(obj,item,snapshot)
        return self._request("PUT",f"/owners/{obj.owner_id}/targets/{obj.instrument_key.instrument_id}",json=body)

    def simulate(self,scenario):
        """Run a declared scenario through P1's isolated SIM HTTP API.

        The operator supplies a SIM-only client and deterministic replay command
        sequence with registered identical hard policy/cost/latency/liquidity.
        Execution/advancing fills remains owned by the P1 worker.
        """
        if scenario["create_run"]["run_key"]["environment"] != "SIM":
            raise StrategyError("COUNTERFACTUAL_MUST_BE_SIM",403)
        result = self._request("POST","/runs",json=scenario["create_run"])
        for operation in scenario["operations"]:
            if operation["body"]["run_key"] != scenario["create_run"]["run_key"]:
                raise StrategyError("COUNTERFACTUAL_OPERATION_RUN_MISMATCH",403)
            binding = scenario["create_run"]["run_key"]
            run = self._request("GET","/runs/"+binding["run_id"],params={"environment":"SIM","account_id":binding["account_id"]})
            body = {**operation["body"],"expected_version":run["aggregate_version"]}
            result = self._request(operation["method"],operation["path"],json=body)
            if operation["method"] == "PUT" and "/targets/" in operation["path"]:
                import time
                deadline = time.monotonic()+min(60,float(self.client.timeout.read or 5)) if hasattr(self.client,"timeout") else time.monotonic()+5
                while True:
                    orders = self._request("GET","/orders",params=scenario["create_run"]["run_key"])
                    unfinished = [o for o in orders["items"] if o["state"] in {"RESERVED","DISPATCHING","UNKNOWN"}]
                    if not unfinished:
                        break
                    if time.monotonic() >= deadline:
                        raise StrategyError("COUNTERFACTUAL_EXECUTION_UNCONFIRMED",503)
                    time.sleep(0.05)
        account = self._request("GET","/account",params=scenario["create_run"]["run_key"])
        return {"run":scenario["create_run"]["run_key"],"last_receipt":result,"equity":account["equity"],"fees":account["fees"],"realized_pnl":account["realized_pnl"],"risk_locks":account["risk_locks"]}
