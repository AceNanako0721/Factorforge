"""Read-only target-account probes; findings do not grant execution permission."""
from factorforge.trading.domain.errors import TradingError


def account_probe(transport):
    account = transport.request("GET", "/fapi/v3/account", {})
    mode = transport.request("GET", "/fapi/v1/positionSide/dual", {})
    multi = transport.request("GET", "/fapi/v1/multiAssetsMargin", {})
    ordinary = transport.request("GET", "/fapi/v1/openOrders", {})
    conditional = transport.request("GET", "/fapi/v1/openAlgoOrders", {})
    if (account.get("canTrade") is not True or mode.get("dualSidePosition") is not False
            or multi.get("multiAssetsMargin") is not False):
        raise TradingError("TARGET_ACCOUNT_MODE_UNVERIFIED", 423)
    return {"account_readable": True, "one_way": True, "single_asset": True,
            "ordinary_open_count": len(ordinary), "conditional_open_count": len(conditional),
            "execution_approved": False, "protection_submit_verified": False}
