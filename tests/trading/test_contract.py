"""Contract response types and decimal input schemas must remain inspectable."""
from factorforge.trading.api.app import create_app, PREFIX


def test_contract_has_explicit_money_inputs_and_response_models(harness):
    app = create_app(harness.service, {})
    contract = app.openapi()
    schemas = contract["components"]["schemas"]
    request = next(v for k, v in schemas.items() if k in {"OrderRequest", "OrderRequest-Input"})
    assert request["properties"]["quantity"]["type"] == "string"
    assert request["additionalProperties"] is False
    assert "HTTPBearer" in contract["components"]["securitySchemes"]
    assert "$ref" in contract["paths"][PREFIX + "/orders"]["get"]["responses"]["200"]["content"]["application/json"]["schema"]
