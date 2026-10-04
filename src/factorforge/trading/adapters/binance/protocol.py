"""Pure ordinary/conditional protocol mapping; no signed network execution."""
from factorforge.trading.domain.errors import TradingError

ORDINARY = "/fapi/v1/order"
CONDITIONAL = "/fapi/v1/algoOrder"


def ordinary_request(order):
    req = order.request
    result = {"symbol": req.instrument_key.instrument_id, "side": req.side, "type": req.order_type,
              "quantity": str(req.quantity), "positionSide": "BOTH", "newClientOrderId": order.client_order_id}
    if req.reduce_only:
        result["reduceOnly"] = "true"
    if req.order_type == "LIMIT":
        result["price"] = str(req.limit_price)
        result["timeInForce"] = {"GTC": "GTC", "IOC": "IOC", "POST_ONLY": "GTX"}[req.time_in_force]
    return "POST", ORDINARY, result


def cancel_request(order):
    return "DELETE", ORDINARY, {"symbol": order.request.instrument_key.instrument_id, "origClientOrderId": order.client_order_id}


def query_request(order):
    return "GET", ORDINARY, {"symbol": order.request.instrument_key.instrument_id, "origClientOrderId": order.client_order_id}


def protection_request(key, plan, close_side, client_id):
    if close_side not in {"BUY", "SELL"}:
        raise TradingError("PROTECTION_SIDE_INVALID")
    return "POST", CONDITIONAL, {"algoType": "CONDITIONAL", "symbol": key.instrument_id, "side": close_side,
            "type": "STOP_MARKET", "quantity": str(plan.covered_quantity), "triggerPrice": str(plan.trigger_price),
            "workingType": "MARK_PRICE" if plan.trigger_kind == "MARK" else "CONTRACT_PRICE",
            "reduceOnly": "true", "positionSide": "BOTH", "clientAlgoId": client_id}


def protection_query(external_id):
    return "GET", CONDITIONAL, {"algoId": external_id}


def protection_cancel(external_id):
    return "DELETE", CONDITIONAL, {"algoId": external_id}
