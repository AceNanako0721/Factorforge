"""Read-only target-account probes; findings do not grant execution permission."""
from factorforge.trading.domain.errors import TradingError


def require_account_configuration(transport):
    """V3 balances omit canTrade; verify it at the account configuration API."""
    configuration = transport.request("GET", "/fapi/v1/accountConfig", {})
    if not isinstance(configuration, dict) or configuration.get("canTrade") is not True:
        raise TradingError("VENUE_ACCOUNT_CANNOT_TRADE", 423)
    if (configuration.get("dualSidePosition") is not False
            or configuration.get("multiAssetsMargin") is not False):
        raise TradingError("TARGET_ACCOUNT_MODE_UNVERIFIED", 423)
    return configuration


def account_probe(transport):
    transport.request("GET", "/fapi/v3/account", {})
    require_account_configuration(transport)
    mode = transport.request("GET", "/fapi/v1/positionSide/dual", {})
    multi = transport.request("GET", "/fapi/v1/multiAssetsMargin", {})
    ordinary = transport.request("GET", "/fapi/v1/openOrders", {})
    conditional = transport.request("GET", "/fapi/v1/openAlgoOrders", {})
    if (mode.get("dualSidePosition") is not False or multi.get("multiAssetsMargin") is not False):
        raise TradingError("TARGET_ACCOUNT_MODE_UNVERIFIED", 423)
    return {"account_readable": True, "account_trading_enabled": True, "one_way": True, "single_asset": True,
            "ordinary_open_count": len(ordinary), "conditional_open_count": len(conditional),
            "execution_approved": False, "protection_submit_verified": False}
