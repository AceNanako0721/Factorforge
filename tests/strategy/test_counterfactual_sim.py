from datetime import timedelta
from decimal import Decimal
from fastapi.testclient import TestClient
from factorforge.trading.api.app import create_app
from factorforge.trading.domain.models import Principal,RunKey
from factorforge.trading.adapters.sim.broker import SimBroker
from factorforge.trading.workers.executor import ExecutionWorker
from factorforge.strategy.adapters.trading_v2 import TradingV2
from factorforge.strategy.application.case_service import CounterfactualRunner
from factorforge.strategy.domain.events import digest
from conftest import AT


def test_counterfactual_uses_p1_actual_sim_fills_and_costs_without_touching_baseline(harness):
    h = harness
    baseline_run = h.trading_store.read(RunKey(**h.key))
    frozen = baseline_run.model_dump(mode="json")
    binding = dict(environment="SIM",account_id="scenario-account",run_id="scenario-run")
    principal = h.trading_principal.model_copy(update={"account_id":binding["account_id"]})
    client = TestClient(create_app(h.trading_service,{"scenario-token":principal}))
    class DrivenSimulation(TradingV2):
        def _request(self,method,path,**kwargs):
            result = super()._request(method,path,**kwargs)
            if method == "PUT" and "/targets/" in path:
                worker = ExecutionWorker(h.trading_store,SimBroker())
                for _ in range(10):
                    if not worker.tick(RunKey(**binding)):
                        break
            return result
    trading = DrivenSimulation("","scenario-token",5,"1m",1000,client=client)
    create = dict(schema_version="trading-2.0",request_id="scenario-create",idempotency_key="scenario-create",run_key=binding,reason="registered same-budget fixture",expires_at_utc=(AT+timedelta(days=1)).isoformat(),execution_mode="REPLAY",initial_cash="1000",currency="USD",clock=AT.isoformat(),account_policy=baseline_run.policy.model_dump(mode="json"),sim_config=baseline_run.sim_config.model_dump(mode="json"))
    def command(name):
        return dict(schema_version="trading-2.0",request_id=name,idempotency_key=name,run_key=binding,expected_version=0,reason="registered fixture",expires_at_utc=(AT+timedelta(days=1)).isoformat())
    instrument = h.instruments[0]
    spec = next(iter(baseline_run.specs.values())).model_dump(mode="json")
    def frame(at,price):
        return dict(at=at.isoformat(),instrument_key=instrument,liquidity="100",points=[dict(instrument_key=instrument,source_id="fixture",kind=kind,observed_at=at.isoformat(),received_at=at.isoformat(),available_at=at.isoformat(),value=price,currency="USD",quality="VALID",spec_version="fixture-spec") for kind in ("BID","ASK","MARK","LAST")])
    first,second = frame(AT+timedelta(seconds=1),"100"),frame(AT+timedelta(seconds=61),"101")
    target = dict(**command("scenario-target"),owner_id="scenario-owner",instrument_key=instrument,target_version=1,target_quantity="1",owner_epoch=0,policy_version="fixture-risk",spec_version="fixture-spec",source_decision_id="scenario-decision",protection_plan=dict(trigger_kind="MARK",trigger_price="98",covered_quantity="1",exit_order_type="MARKET",max_slippage_bps="0",spec_version="fixture-spec"))
    operations = [dict(method="POST",path="/instruments",body={**command("scenario-spec"),"spec":spec}),dict(method="POST",path="/simulation/frames",body={**command("scenario-first"),"frame":first}),dict(method="PUT",path="/owners/scenario-owner/targets/"+instrument["instrument_id"],body=target),dict(method="POST",path="/simulation/frames",body={**command("scenario-second"),"frame":second})]
    baseline = dict(run_id=h.key["run_id"],information_manifest="fixture-info",cost_manifest="fixture-cost",latency_manifest="fixture-latency",liquidity_manifest="fixture-liquidity",hard_risk_manifest="fixture-hard",risk_budget="2",quantity=Decimal(2),distance=Decimal(1),account_policy=create["account_policy"],sim_config=create["sim_config"],replay_frames=[first,second],approved_operation_hashes={"WIDER_STOP_LOWER_QUANTITY":digest(operations)})
    scenario = {**baseline,"scenario_id":"scenario-run","create_run":create,"operations":operations,"changed_factor":"WIDER_STOP_LOWER_QUANTITY","changed_fields":["WIDER_STOP_LOWER_QUANTITY"],"quantity":Decimal(1),"distance":Decimal(2)}
    result = CounterfactualRunner(trading).run(baseline,scenario)
    assert Decimal(result["result"]["fees"]) > 0
    assert h.trading_store.read(RunKey(**binding)).fills
    assert h.trading_store.read(RunKey(**h.key)).model_dump(mode="json") == frozen
