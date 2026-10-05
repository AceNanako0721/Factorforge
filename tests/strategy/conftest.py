"""Explicit SIM fixture constants. None of these values are production calibration."""
from datetime import datetime,timedelta,timezone
from decimal import Decimal

from fastapi.testclient import TestClient
import pytest

from factorforge.trading.adapters.memory import MemoryStore as TradingMemory
from factorforge.trading.adapters.sim.broker import SimBroker
from factorforge.trading.application.commands import TradingService
from factorforge.trading.api.app import create_app as trading_app
from factorforge.trading.domain.models import Principal, RunKey
from factorforge.trading.workers.executor import ExecutionWorker
from factorforge.strategy.adapters.clock import ReplayClock
from factorforge.strategy.adapters.memory import MemoryStore
from factorforge.strategy.adapters.trading_v2 import TradingV2
from factorforge.strategy.application.object_service import ObjectService
from factorforge.strategy.application.event_service import EventService
from factorforge.strategy.application.score_admission import ScoreAdmission
from factorforge.strategy.application.decision_cycle import DecisionCycle
from factorforge.strategy.domain.models import *

AT = datetime(2026,1,5,tzinfo=timezone.utc)


def policy(**changes):
    data = dict(version="fixture-policy",quality_state="VALIDATED",source_manifest="fixture-only",
        calibrations={"fixture-calibration"},rubrics={"fixture-rubric"},claim_weights={"earnings":"1"},verification_manifests={"fixture-verified"},levels=[dict(exposure="0.1",enter="5",exit="3"),dict(exposure="0.2",enter="20",exit="15")],
        cycle_seconds=60,max_price_age_seconds=120,target_ttl_seconds=120,event_cap="100",family_cap="200",object_cap="300",cleanup_threshold="0.001",event_ttl_seconds=86400,
        numerical_tolerance="0.0000000001",epsilon="0.0000000001",sigma_ref="0.01",liquidity_budget="1000",object_loss_budget="50",
        portfolio_gross_limit="900",portfolio_net_limit="900",portfolio_stress_limit="100",group_limits={"DEFAULT":"900"},
        micro_distance="0.01",max_stop_fraction="0.1",fee_rate="0.001",slippage_fraction="0",gap_fraction="0",
        noise_window=2,noise_quantile="0.5",max_bar_gap_seconds=120,max_noise_fraction="0.2",cooldown_seconds=0,min_adjustment="0",
        window_seconds=3600,window_anchor=AT,max_new_risk=5,max_loss_cases=2,observation_multiplier="1",observation_min_seconds=60,
        observation_max_seconds=3600,label_seconds=120,label_cost_band="0.001",label_noise_band="0.001",gamma="0.001",lifecycle_fraction="0.5",
        regime_confirmations=2,regime_dwell_seconds=60,regime_enter="2",regime_exit="1",unknown_regime_multiplier="0.5",beta="0",absolute_price_proxy_validated=True,
        half_life_bounds=("1","86400"),event_half_life_approved=False)
    data.update(changes)
    return Policy(**data)


def parameters(**changes):
    data = dict(version="fixture-parameters",scope="*",valid_from=AT,values={"w":"1","g":"1","h":"3600","eta":"1","eta_anchor":"1","kappa":"100","k_stop":"2"},
        bounds={k:("0.1","10000") for k in ("w","h","eta","kappa","k_stop")},steps={k:"0.1" for k in ("w","h","eta","kappa","k_stop")},
        evidence_gates={"min_groups":"3","min_neff":"2","min_repeats":"3","min_confidence":"0.8","deadband":"0.01","scale":"1","forget_seconds":"86400",
            "cooldown_seconds":"60","drift_budget":"10","ci_low":"0.1","ci_high":"0.9","minimum_improvement":"0.01","opposite_threshold":"0.5"},
        source_manifest="fixture-only",applicability=["fixture"],quality_state="VALIDATED")
    data.update(changes)
    return ParameterSnapshot(**data)


class Harness:
    def __init__(self,store=None,objects=1):
        self.trading_store = TradingMemory()
        self.trading_service = TradingService(self.trading_store,SimBroker())
        self.trading_principal = Principal(principal_id="p2-fixture",environment="SIM",account_id="p2-account",permissions={"read","run:create","order:write","market:write","protection:write","sim:write","income:write","target:write"})
        self.client = TestClient(trading_app(self.trading_service,{"fixture-trading":self.trading_principal}))
        self.headers = {"Authorization":"Bearer fixture-trading"}
        self.key = dict(environment="SIM",account_id="p2-account",run_id="p2-run")
        self.counter = 0
        gate = dict(mode="OBSERVE",amount="1")
        create = dict(schema_version="trading-2.0",request_id="fixture-run",idempotency_key="fixture-run",run_key=self.key,reason="SIM fixture",expires_at_utc=(AT+timedelta(days=30)).isoformat(),execution_mode="REPLAY",initial_cash="1000",currency="USD",clock=AT.isoformat(),
            account_policy=dict(version="fixture-risk",valid_from=AT.isoformat(),risk_day_zone="UTC",notional_limit="900",margin_limit="900",trade_loss_limit="100",daily_loss=gate,
                drawdown=dict(mode="OBSERVE",amount="100"),consecutive_loss=dict(mode="OBSERVE",count=3),breach_action="KEEP_PROTECTION",recovery_policy="MANUAL_RECONCILE",max_market_age_seconds=120),
            sim_config=dict(seed=7,fee_rate="0.001",slippage_bps="0",participation_rate="1",latency_seconds=0,maintenance_margin_rate="0.05",ohlc_rule="CONSERVATIVE"))
        self.post("/runs",create)
        self.instruments = []
        for index in range(objects):
            instrument = dict(venue="TEST",product="LINEAR_PERPETUAL",instrument_id=f"ASSET{index}USD")
            self.instruments.append(instrument)
            self.post("/instruments",{**self.command(),"spec":dict(key=instrument,version="fixture-spec",valid_from=AT.isoformat(),price_tick="0.01",quantity_step="0.1",contract_multiplier="1",quote_currency="USD",settlement_currency="USD",min_notional="1",capabilities=["MARKET","LIMIT","SHORT","REDUCE_ONLY","CONDITIONAL_PROTECTION"],price_roles=["MARK","LAST","BID","ASK"])})
            self.frame("100",1,index)
        self.clock = ReplayClock(self.trading_store.read(RunKey(**self.key)).clock)
        state = StrategyState(instance_id="fixture-instance",environment="SIM")
        self.store = store or MemoryStore(state)
        if store is not None:
            store.create(state)
        self.identity = WorkloadIdentity(workload_id="fixture-worker",instance_id=state.instance_id,environment="SIM",object_ids={f"object-{i}" for i in range(objects)},capabilities={WorkloadCapability.SIM})
        self.public = PublicPrincipal(principal_id="fixture-public",instance_id=state.instance_id,environment="SIM",scopes=set(ApiScope))
        self.trading = TradingV2("", "fixture-trading",5,"1m",86400,client=self.client)
        self.cycle = DecisionCycle(self.store,self.trading,self.clock)
        for index,instrument in enumerate(self.instruments):
            obj = ObservedObject(object_id=f"object-{index}",instance_id=state.instance_id,environment="SIM",trading_run_key=self.key,instrument_key=instrument,owner_id=f"owner-{index}",parameter_version="fixture-parameters",price_proxy_binding="fixture-price",regime_binding="fixture-regime",time_policy_version="fixture-policy")
            if store is not None and hasattr(store,"grant_fixture"):
                store.grant_fixture(self.identity,obj.object_id)
            ObjectService(self.store,self.clock).create(self.identity,self.scommand(),obj,policy(),parameters())
        with self.store.transaction(state.instance_id) as s:
            s.factor_manifests["fixture-price"] = {"validated":True,"purpose":"PRICE_PROXY","available_at":AT.isoformat(),"missing_policy":"UNKNOWN","validation_manifest":"fixture-only","dependencies":[],"account_currency":"USD","observations":[{"available_at":AT.isoformat(),"sigma":"0.01","liquidity":"1000"}]}
        self.seed_bars()

    def post(self,path,body):
        response = self.client.post("/api/v2/trading"+path,json=body,headers=self.headers)
        assert response.status_code < 300,response.text
        return response.json()

    def command(self):
        self.counter += 1
        run = self.trading_store.read(RunKey(**self.key))
        return dict(schema_version="trading-2.0",request_id=f"trading-{self.counter}",idempotency_key=f"trading-{self.counter}",run_key=self.key,expected_version=run.version,reason="SIM fixture",expires_at_utc=(AT+timedelta(days=30)).isoformat())

    def frame(self,price,seconds=60,index=0):
        run = self.trading_store.read(RunKey(**self.key))
        at = run.clock+timedelta(seconds=seconds)
        instrument = self.instruments[index]
        points = [dict(instrument_key=instrument,source_id="fixture",kind=kind,observed_at=at.isoformat(),received_at=at.isoformat(),available_at=at.isoformat(),value=price,currency="USD",quality="VALID",spec_version="fixture-spec") for kind in ("BID","ASK","MARK","LAST")]
        self.post("/simulation/frames",{**self.command(),"frame":dict(at=at.isoformat(),instrument_key=instrument,points=points,liquidity="100")})
        if hasattr(self,"clock"):
            self.clock.at = at

    def seed_bars(self):
        run = self.trading_store.read(RunKey(**self.key))
        candles = []
        for instrument in self.instruments:
            for offset in (180,120,60):
                end = run.clock-timedelta(seconds=offset)
                candles.append(dict(instrument_key=instrument,interval="1m",open_at=(end-timedelta(seconds=60)).isoformat(),close_at=end.isoformat(),available_at=end.isoformat(),open="100",high="101",low="99",close="100",volume="100",source_id="fixture",final=True,revision=0))
        self.post("/market/snapshots",{**self.command(),"at":run.clock.isoformat(),"points":[p.model_dump(mode="json") for p in run.points.values()],"candles":candles})

    def dispatch_trading(self):
        worker = ExecutionWorker(self.trading_store,SimBroker())
        for _ in range(20):
            if not worker.tick(RunKey(**self.key)):
                break

    def state(self):
        return self.store.read(self.identity.instance_id)

    def scommand(self):
        self.counter += 1
        return Command(schema_version="strategy-2.0",request_id=f"framework-{self.counter}",idempotency_key=f"framework-{self.counter}",expected_version=self.state().version,reason="SIM fixture")

    def event(self,name="event-a",direction=1,impact="60",object_id="object-0",identity=None,family=None,preprice_changes=None):
        now = self.clock.now()
        evidence = EvidenceRef(evidence_id="evidence-"+name,content_hash="a"*64,source_id="fixture",licence_ref="fixture-license",first_public_at=now,received_at=now,available_at=now,span_refs=["span-1"],verification_ref="fixture-verified")
        claim = Claim(claim_id="claim-"+name,normalized_fact=name,subject_id="fixture-subject",economic_item="earnings",period="fixture-period",fact_time=now,numbers_with_units={"amount":"1 USD"},evidence_refs=[evidence.evidence_id],verified_at=now,weight="1",verification_manifest="fixture-rubric")
        event = Event(event_id=name,family_id=family or "family-"+name,fact_version=1,relation="NEW",subject_id=claim.subject_id,event_type="fixture-type",occurred_at=now,first_public_at=now,evidence_refs=[evidence],claims=[claim],object_ids={object_id})
        EventService(self.store,self.clock).register(identity or self.identity,self.scommand(),event)
        score = ScoreSubmission(submission_id="score-"+name,event_id=name,fact_version=1,object_id=object_id,score_version=1,revision_kind="INITIAL",
            vector=ScoreVector(direction=direction,impact_points=impact,credibility="1",relevance="1",novelty="1",expectation_coverage="0",prepricing_fraction="0",expected_half_life="3600",quality_score="1"),
            evidence_refs=[evidence.evidence_id],producer_id="fixture",producer_version="fixture-v1",rubric_version="fixture-rubric",calibration_version="fixture-calibration",completed_at=now,input_manifest_hash="b"*64)
        # Independently registered, explicit fixture proof. Its zero price move
        # cannot be assumed merely because the producer proposes p=0.
        with self.store.transaction(self.identity.instance_id) as state:
            proof = dict(validated=True,input_manifest_hash=score.input_manifest_hash,
                calibration_version="fixture-calibration",verification_manifest="fixture-verified",available_at=now.isoformat(),
                training_end=AT.isoformat(),pre_at=now.isoformat(),price_available_at=now.isoformat(),pre_price="100",available_price="100",
                benchmark_return="0",beta="0",rho="0.1",expectation_coverage="0",expectation_evidence_refs=[],price_evidence_refs=["fixture-price-before-score"],
                coverage_mode="UNCOVERED_PRICE",licence_refs=["fixture-license"])
            proof.update(preprice_changes or {})
            score.vector.expectation_coverage = proof["expectation_coverage"]
            state.factor_manifests["prepricing:"+score.input_manifest_hash] = proof
        receipt = ScoreAdmission(self.store,self.clock).submit(identity or self.identity,self.scommand(),score)
        return event,score,receipt


@pytest.fixture
def harness():
    return Harness()


@pytest.fixture
def database(tmp_path):
    from factorforge.strategy.adapters.postgres.store import initialize
    import pgserver
    server = pgserver.get_server(tmp_path/"pgdata",cleanup_mode="stop")
    dsn = server.get_uri()
    initialize(dsn,"SIM")
    initialize(dsn,"LIVE")
    yield dsn
    server.cleanup()
