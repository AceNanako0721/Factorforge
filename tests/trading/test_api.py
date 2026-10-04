"""HTTP tests install only the trading distribution; no strategy/application package."""
from fastapi.testclient import TestClient

from factorforge.trading.api.app import create_app, PREFIX
from factorforge.trading.domain.models import Principal


def test_api_returns_decimal_strings_and_enforces_bound_identity(harness):
    h = harness
    viewer = Principal(principal_id="viewer", environment="SIM", account_id=h.key.account_id, permissions={"read"})
    client = TestClient(create_app(h.service, {"test-operator": h.principal, "test-viewer": viewer}))
    params = h.key.model_dump()
    assert client.get(PREFIX + "/account", params=params).status_code == 401
    response = client.get(PREFIX + "/account", params=params, headers={"Authorization": "Bearer test-viewer"})
    assert response.status_code == 200 and response.json()["equity"] == "1000"
    response = client.get(PREFIX + "/account", params={**params, "environment": "LIVE"}, headers={"Authorization": "Bearer test-viewer"})
    assert response.status_code == 403
    body = {**h.command().model_dump(mode="json"), "order": h.request().model_dump(mode="json")}
    assert client.post(PREFIX + "/orders", json=body, headers={"Authorization": "Bearer test-viewer"}).status_code == 403
    body["order"]["quantity"] = 1.0
    response = client.post(PREFIX + "/orders", json=body, headers={"Authorization": "Bearer test-operator"})
    assert response.status_code == 422 and "input" not in response.text


def test_http_order_idempotency_and_independent_health(harness):
    h = harness
    client = TestClient(create_app(h.service, {"test-operator": h.principal}))
    assert client.get(PREFIX + "/health").json()["upper_layers_required"] is False
    body = {**h.command().model_dump(mode="json"), "order": h.request().model_dump(mode="json")}
    headers = {"Authorization": "Bearer test-operator"}
    first = client.post(PREFIX + "/orders", json=body, headers=headers)
    assert first.status_code == 202
    second = client.post(PREFIX + "/orders", json=body, headers=headers)
    assert first.json() == second.json()
    body["order"]["side"] = "SELL"
    assert client.post(PREFIX + "/orders", json=body, headers=headers).status_code == 409
