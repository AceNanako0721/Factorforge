"""CLI commands are authenticated HTTP clients, never alternate execution paths."""
import argparse
import json
from pathlib import Path

import httpx

from factorforge.trading.bootstrap import load_config


def main():
    parser = argparse.ArgumentParser(prog="factorforge-trading")
    parser.add_argument("--config", default="config/config.toml")
    parser.add_argument("command", choices=["sim-create", "order-submit", "order-cancel", "protection-set",
                                           "account-show", "market-read", "reconcile", "stop", "resume",
                                           "external-import", "external-resolve", "executor-fence", "fx-register", "fills-show", "targets-show"])
    parser.add_argument("--request", help="JSON request file for write commands")
    parser.add_argument("--run-id")
    parser.add_argument("--resource-id")
    args = parser.parse_args()
    config = load_config(args.config)
    base = config["services"]["trading_api_url"].rstrip("/") + "/api/v2/trading"
    if not config["services"]["trading_api_url"]:
        parser.error("private trading_api_url is required")
    routes = {"sim-create": "/runs", "order-submit": "/orders", "protection-set": "/protections",
              "external-import": "/external-facts", "external-resolve": "/external-facts/resolve",
              "executor-fence": "/executors/fence", "fx-register": "/fx"}
    headers = {"Authorization": "Bearer " + config["credentials"]["trading_api_token"]}
    try:
        with httpx.Client(timeout=15, headers=headers, follow_redirects=False, trust_env=False) as client:
            if args.command in {"account-show", "market-read", "fills-show", "targets-show"}:
                if not args.run_id:
                    parser.error("--run-id is required")
                route = {"account-show": "/account", "market-read": "/market/points", "fills-show": "/fills", "targets-show": "/targets"}[args.command]
                response = client.get(base + route, params={"environment": config["runtime"]["environment"], "account_id": config["trading"]["account_id"], "run_id": args.run_id})
            else:
                if not args.request:
                    parser.error("--request is required")
                body = json.loads(Path(args.request).read_text())
                if args.command == "order-cancel":
                    if not args.resource_id:
                        parser.error("--resource-id is required")
                    route = "/orders/" + args.resource_id + "/cancel"
                elif args.command in {"reconcile", "stop", "resume"}:
                    route = "/runs/" + body["run_key"]["run_id"] + "/" + args.command
                else:
                    route = routes[args.command]
                response = client.post(base + route, json=body)
            print(json.dumps(response.json(), ensure_ascii=False, indent=2))
            if response.is_error:
                raise SystemExit(1)
    except (httpx.HTTPError, ValueError, KeyError, OSError):
        raise SystemExit("TRADING_API_REQUEST_FAILED") from None
