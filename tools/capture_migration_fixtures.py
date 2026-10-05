"""Capture synthetic v2.1.0 behavior once; Go tests consume JSON without Python.

Temporary migration oracle, never reads private configuration or accesses a venue.
Retire this helper when the complete Python implementation leaves the active tree.
"""
from datetime import timedelta
from decimal import Decimal
import json
from pathlib import Path
import random
import runpy
import subprocess

from factorforge.strategy.domain.models import Contribution, StrategyState, MarketSample, Bar
from factorforge.strategy.domain import sentiment, position, stop
from factorforge.trading.domain.risk import floor_step

ROOT = Path(__file__).resolve().parents[1]
fixture = runpy.run_path(str(ROOT / "tests/strategy/conftest.py"))
AT, policy, parameters = (fixture[k] for k in ("AT", "policy", "parameters"))
cases = []


def camel(key):
    return "".join(part.title() for part in key.split("_"))


def go_policy(p):
    data = p.model_dump(mode="json")
    data["levels"] = [{camel(k): v for k, v in row.items()} for row in data["levels"]]
    return {camel(k): v for k, v in data.items()}


def add(name, kind, inputs, expected):
    cases.append(dict(name=name, kind=kind, input=inputs, expected=expected))


def capture():
    unchanged = subprocess.run(["git", "diff", "--exit-code", "v2.1.0", "--",
                                ":(glob)src/factorforge/**/*.py", "tests/strategy/conftest.py"],
                               cwd=ROOT, capture_output=True)
    if unchanged.returncode:
        raise ValueError("Oracle Python source differs from the frozen v2.1.0 implementation")
    for module in (sentiment, position, stop):
        if not Path(module.__file__).resolve().is_relative_to(ROOT / "src"):
            raise ValueError("Run capture with PYTHONPATH=src against the verified oracle source")
    rng = random.Random(211)
    for amount in ("0", "1", "0.00000001", "100.1234567890123456789", "99999999.99999999"):
        for seconds in ("0", "0.001", "1", "29.123456789", "3600", "86400"):
            for half in ("60", "3600", "86400", "604800"):
                add(f"decay-{len(cases)}", "decay", dict(amount=amount, seconds=seconds, half_life=half),
                    str(sentiment.decay(Decimal(amount), Decimal(seconds), Decimal(half))))
    for index in range(160):
        amount, seconds, half = (str(Decimal(rng.randrange(1, 10**12)) / 10**6),
                                 str(Decimal(rng.randrange(0, 10**8)) / 1000), str(rng.randrange(1, 604801)))
        add(f"decay-random-{index}", "decay", dict(amount=amount, seconds=seconds, half_life=half),
            str(sentiment.decay(Decimal(amount), Decimal(seconds), Decimal(half))))
    for value in ("-20.1234", "-0.099", "0", "0.099", "20.1234", "12345678901234567890.123456789"):
        for step in ("0.001", "0.1", "3"):
            add(f"floor-{len(cases)}", "floor", dict(value=value, step=step), str(floor_step(Decimal(value), Decimal(step))))
    p = policy()
    for previous in (0, 1, 2):
        for strength in ("0", "2.999", "3", "4.999", "5", "14.999", "15", "19.999", "20", "100"):
            add(f"level-{len(cases)}", "level", dict(strength=strength, previous=previous, policy=go_policy(p)),
                position.level_for(Decimal(strength), previous, p.levels))
    for exposure in ("-0.213456789", "0", "0.213456789"):
        for multiplier in ("1", "0.01", "100"):
            args = tuple(map(Decimal, (exposure, "1000.123", "100.33", multiplier, "0.01")))
            add(f"quantity-{len(cases)}", "quantity", dict(exposure=exposure, equity="1000.123", price="100.33", multiplier=multiplier, step="0.01"), str(position.quantity(*args)))
    for budget in ("0", "1", "5", "100"):
        for weights, caps in (({"a":"1","b":"2","c":"3"},{"a":"0.1","b":"10","c":"10"}),
                              ({"a":"0","b":"2","c":"1"},{"a":"3","b":"0","c":"0.5"})):
            expected = sentiment.waterfill(Decimal(budget), {k:Decimal(v) for k,v in weights.items()}, {k:Decimal(v) for k,v in caps.items()})
            add(f"waterfill-{len(cases)}", "waterfill", dict(budget=budget, weights=weights, caps=caps), {k:str(v) for k,v in expected.items()})
    for rows, base in (([("0","1200","0.05","DEFAULT")],[]),
                       ([("700","1000","0.1","DEFAULT"),("300","200","0.1","DEFAULT")],[]),
                       ([("800","800","0.1","DEFAULT")],[("200","10","DEFAULT")]),
                       ([("-300","-300","0.1","DEFAULT")],[("400","10","DEFAULT")])):
        candidates = [(Decimal(a),Decimal(b),Decimal(c),g) for a,b,c,g in rows]
        fixed = [(Decimal(v),Decimal(s),g) for v,s,g in base]
        inputs = dict(candidates=[dict(Current=a,Desired=b,Stress=c,Group=g) for a,b,c,g in rows],
                      base=[dict(Value=v,Stress=s,Group=g) for v,s,g in base], policy=go_policy(p))
        add(f"projection-{len(cases)}", "projection", inputs, str(position.shared_projection(candidates,fixed,p)))
        value=position.existing_risk_scale(candidates,fixed,p)
        add(f"existing-{len(cases)}", "existing", inputs, str(value) if value is not None else None)
    spec=dict(contract_multiplier="1",quantity_step="0.1",price_tick="0.01",min_notional="1",capabilities=["CONDITIONAL_PROTECTION","MARKET"],price_roles=["MARK","LAST"],version="fixture-spec")
    for q in ("-100", "-1", "0", "1", "100"):
        for noise in (None,"0.000001","0.02","0.2"):
            sized,plan,reason,fraction=stop.size_and_stop(Decimal(q),Decimal("100.123"),spec,Decimal(noise) if noise is not None else None,p,parameters())
            add(f"stop-{len(cases)}", "stop", dict(q=q,price="100.123",noise=noise,policy=go_policy(p),rules=dict(Multiplier="1",QuantityStep="0.1",PriceTick="0.01",MinNotional="1",Capabilities=spec["capabilities"],PriceRoles=spec["price_roles"],Version="fixture-spec"),k_stop="2"),
                dict(quantity=str(sized),reason=reason,fraction=str(fraction),trigger=str(plan.trigger_price) if plan else None))
    for scenario in ("valid","missing","unfinished","future","gap","anomaly"):
        bars=[Bar(close_at=AT+timedelta(seconds=i*60),available_at=AT+timedelta(seconds=i*60),high="101",low="99",close="100",final=True,quality="VALID") for i in range(3)]
        if scenario=="missing":bars=bars[:2]
        elif scenario=="unfinished":bars[-1].final=False
        elif scenario=="future":bars[-1].available_at=AT+timedelta(days=1)
        elif scenario=="gap":bars[-1].close_at=AT+timedelta(seconds=1000);bars[-1].available_at=bars[-1].close_at
        elif scenario=="anomaly":bars[-1].high=Decimal("200")
        now=AT+timedelta(hours=1);value=stop.normal_noise(bars,now,p)
        add("noise-"+scenario,"noise",dict(bars=[{camel(k):v for k,v in b.model_dump(mode="json").items()} for b in bars],at=now.isoformat(),policy=go_policy(p)),str(value) if value is not None else None)
    # Actual source functions create and advance the conservation ledger.
    state=StrategyState(instance_id="migration-fixture",environment="SIM")
    for key,direction,amount in (("a",1,"40"),("b",1,"20"),("c",-1,"10")):
        c=Contribution(contribution_id=key,object_id="object",event_id="event-"+key,family_id="family-"+key,direction=direction,initial_amount=amount,remaining_amount="0",effective_at=AT,last_updated_at=AT,half_life="3600",eta="1",high_water="0",reference_price="100",frozen_parameter_version="fixture",score_id="score-"+key,quality="1")
        state.contributions[key]=c;sentiment.append_entry(state,c,AT,"INJECTION",injection=Decimal(amount))
    for index,price in enumerate(("100","104","102","104","105",None,"106"),1):
        before=dict(contributions=[c.model_dump(mode="json") for c in state.contributions.values()],ledger=[row.model_dump(mode="json") for row in state.ledger])
        now=AT+timedelta(seconds=60*index);sample=MarketSample(available_at=now,price=price,quality="VALID") if price else None
        obj=type("FixtureObject",(),{"object_id":"object"})()
        sentiment.advance_contributions(state,obj,now,sample,p,parameters());view=sentiment.pool(state,"object")
        add(f"advance-{index}","advance",dict(state=before,at=now.isoformat(),sample={camel(k):v for k,v in sample.model_dump(mode="json").items()} if sample else None,policy=go_policy(p),kappa="100"),
            dict(contributions=[c.model_dump(mode="json") for c in state.contributions.values()],ledger=[row.model_dump(mode="json") for row in state.ledger],plus=str(view.plus),minus=str(view.minus),net=str(view.net),quality=str(view.quality)))
    base_commit=subprocess.check_output(["git","rev-parse","v2.1.0^{}"],cwd=ROOT,text=True).strip()
    output=dict(schema_version=1,source_commit=base_commit,fixture_only=True,cases=cases)
    destination=ROOT/"tests/strategy/fixtures/go_migration.json";destination.parent.mkdir(parents=True,exist_ok=True)
    destination.write_text(json.dumps(output,ensure_ascii=False,indent=2)+"\n",encoding="utf-8")
    print(f"Captured {len(cases)} synthetic oracle cases; no configuration or venue access")


if __name__=="__main__":capture()
