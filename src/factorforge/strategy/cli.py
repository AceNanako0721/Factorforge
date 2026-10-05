"""Manual SIM/research client; payload files contain generic DTOs, not prompts."""
import argparse
import json
from pathlib import Path

import httpx

from factorforge.strategy.bootstrap import load,initialize_instance
from factorforge.strategy.api.app import PREFIX


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument("--config",default="config/config.toml")
    parser.add_argument("--internal",action="store_true")
    parser.add_argument("command",choices=("initialize","create-object","create-event","submit-score","advance-clock","show-pool","show-target","show-case"))
    parser.add_argument("--file")
    parser.add_argument("--object-id")
    parser.add_argument("--event-id")
    args = parser.parse_args()
    config = load(args.config)
    if args.command == "initialize":
        initialize_instance(config)
        print("Initialized strategy registry and separately authorized workload grants")
        return
    if args.internal and config["environment"] != "SIM":
        raise SystemExit("Manual workload CLI is SIM only")
    binding = "internal" if args.internal else "public"
    url = config[binding+"_api_url"]
    token = config["workload_token"] if args.internal else config["public_token"]
    reads = {"show-pool":"pool","show-target":"targets","show-case":"cases"}
    paths = {"create-object":"/objects","create-event":"/events","submit-score":f"/events/{args.event_id}/scores","advance-clock":"/clock"}
    if args.command in reads:
        method,path,payload = "GET",f"/objects/{args.object_id}/"+reads[args.command],None
    else:
        if not args.file:
            parser.error("--file required")
        method,path,payload = "POST",paths[args.command],json.loads(Path(args.file).read_text())
    with httpx.Client(base_url=url,timeout=config["timeout_seconds"]) as client:
        response = client.request(method,PREFIX+path,headers={"Authorization":"Bearer "+token},json=payload)
        if response.status_code >= 400:
            raise SystemExit(response.json().get("code","STRATEGY_REQUEST_FAILED"))
        print(json.dumps(response.json(),ensure_ascii=False,indent=2))
