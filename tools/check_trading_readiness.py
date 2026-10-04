"""Show missing private configuration fields without printing their values."""
import argparse
import json
from pathlib import Path
import tomllib
from urllib.parse import urlparse


def inspect(path):
    try:
        config = tomllib.loads(Path(path).read_text(encoding="utf-8"))
    except (OSError, ValueError):
        return {"configuration_parseable": False}
    services, credentials, trading = (config.get(k, {}) for k in ("services", "credentials", "trading"))
    if not all(isinstance(section, dict) for section in (services, credentials, trading)):
        return {"configuration_parseable": False}
    try:
        endpoint = urlparse(services.get("exchange_api_url", ""))
        official = endpoint.scheme == "https" and endpoint.hostname == "demo-fapi.binance.com" \
            and endpoint.username is None and endpoint.password is None and endpoint.path in {"", "/"} \
            and not endpoint.query and not endpoint.fragment and endpoint.port in {None, 443}
    except (ValueError, TypeError, AttributeError):
        official = False
    return {"configuration_parseable": True,
        "official_futures_testnet_endpoint": official,
        "api_key_present": bool(credentials.get("exchange_api_key")),
        "api_secret_present": bool(credentials.get("exchange_api_secret")),
        "execution_database_present": bool(services.get("execution_database_url")),
        "account_binding_present": bool(trading.get("account_id")),
        "signed_transport_policy_present": bool(trading.get("signed_transport")),
        "readiness_record_present": bool(trading.get("live_readiness"))}


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument("--config", default="config/config.toml")
    args = parser.parse_args()
    print(json.dumps(inspect(args.config), indent=2))


if __name__ == "__main__":
    main()
