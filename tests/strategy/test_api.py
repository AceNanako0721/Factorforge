import json
from fastapi.testclient import TestClient
import pytest
from factorforge.strategy.api.app import create_app,PREFIX
from factorforge.strategy.domain.models import *


def test_http_identity_schema_and_public_trade_escalation_rejected(harness):
    h = harness
    event,score,_ = h.event(identity=h.public)
    client = TestClient(create_app(h.store,h.clock,{"fixture-public":h.public}))
    assert client.get(PREFIX+"/objects/object-0/pool").status_code == 401
    headers = {"Authorization":"Bearer fixture-public"}
    assert client.get(PREFIX+"/objects/object-0/pool",headers=headers).json()["net"] == "0"
    body = {**h.scommand().model_dump(mode="json"),"score":score.model_dump(mode="json")}
    body["score"]["workload_capability"] = "signal:live"
    response = client.post(PREFIX+"/events/event-a/scores",json=body,headers=headers)
    assert response.status_code == 422 and "input" not in response.text
    assert h.state().audit[-1]["code"] == "INVALID_REQUEST"
    with pytest.raises(ValueError):
        PublicPrincipal(principal_id="fake",instance_id="fixture-instance",environment="LIVE",scopes=["signal:live"])
    assert "signal:" not in json.dumps(PublicPrincipal.model_json_schema())
    with pytest.raises(StrategyError,match="AUTH_LISTENER"):
        create_app(h.store,h.clock,{"wrong-listener":h.identity})


def test_cross_instance_workload_binding_and_decimal_float(harness):
    h = harness
    fake = h.identity.model_copy(update={"instance_id":"other-instance"})
    with pytest.raises(StrategyError):
        h.cycle.tick(fake)
    with pytest.raises(ValueError):
        ScoreVector(direction=1,impact_points=1.0)
    client = TestClient(create_app(h.store,h.clock,{"fixture":h.identity},internal=True,cycle=h.cycle))
    assert client.get(PREFIX+"/health").json()["application_required"] is False
