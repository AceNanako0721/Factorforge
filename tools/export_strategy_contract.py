"""Generate both P2 identity surfaces without accessing private configuration."""
import json
from pathlib import Path
from factorforge.strategy.adapters.memory import MemoryStore
from factorforge.strategy.adapters.clock import SystemClock
from factorforge.strategy.api.app import create_app
from factorforge.strategy.application.decision_cycle import DecisionCycle
from factorforge.strategy.domain.models import PublicPrincipal,WorkloadIdentity


def main():
    destination = Path(__file__).resolve().parents[1]/"contracts/v2/strategy"
    destination.mkdir(parents=True,exist_ok=True)
    for name,internal in (("openapi.json",False),("workload.openapi.json",True)):
        store,clock = MemoryStore(),SystemClock()
        app = create_app(store,clock,{},internal=internal,cycle=DecisionCycle(store,None,clock) if internal else None)
        (destination/name).write_text(json.dumps(app.openapi(),ensure_ascii=False,indent=2)+"\n",encoding="utf-8")
    (destination/"identity.schemas.json").write_text(json.dumps({"public":PublicPrincipal.model_json_schema(),"workload":WorkloadIdentity.model_json_schema()},indent=2)+"\n")
    print("Exported strategy-2.0 public and workload contracts")


if __name__ == "__main__":
    main()
