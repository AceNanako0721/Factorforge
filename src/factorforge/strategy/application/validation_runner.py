"""Reserve a sealed evaluation before running; failures and attempts are durable."""
from factorforge.strategy.application.object_service import authorize
from factorforge.strategy.domain.models import ApiScope,StrategyError,utc
from factorforge.strategy.domain.validation import split,register_run


class ValidationRunner:
    def __init__(self,store,clock):
        self.store,self.clock = store,clock

    def run(self,identity,manifest,records,evaluate):
        spans = manifest["label_span_seconds"]+manifest["impact_span_seconds"]+manifest["release_delay_seconds"]
        if manifest["embargo_seconds"] < spans:
            raise StrategyError("PURGE_EMBARGO_SPAN_TOO_SHORT")
        with self.store.transaction(identity.instance_id) as state:
            authorize(state,identity,ApiScope.QUERY)
            register_run(state,manifest,{"attempted_at":self.clock.now().isoformat()})
            state.validation_runs[manifest["run_id"]]["state"] = "RUNNING"
            state.version += 1
        try:
            windows = manifest["windows"]
            if utc(windows["test_end"]) > self.clock.now() or any(utc(r["available_at"]) > self.clock.now() for r in records):
                raise StrategyError("VALIDATION_DATA_NOT_YET_AVAILABLE")
            partitions = split(records,utc(windows["train_end"]),utc(windows["validation_end"]),utc(windows["test_end"]),manifest["embargo_seconds"])
            if not all(partitions.values()):
                raise StrategyError("INDEPENDENT_PARTITIONS_EMPTY")
            # The evaluator receives forward-isolated records and a fixed preregistered
            # ablation. Its execution adapter must use P1 SIM, not synthetic real fills.
            results = {}
            for ablation in manifest["ablations"]:
                results[ablation] = evaluate(partitions,ablation,manifest)
            outcome = {"partitions":{k:[r["id"] for r in v] for k,v in partitions.items()},"ablations":results,
                       "fixed_external_outputs":True,"production_upgrade":False}
            status = "COMPLETED"
        except Exception as error:
            outcome = {"failure_code":error.code if isinstance(error,StrategyError) else "EVALUATOR_FAILED","production_upgrade":False}
            status = "FAILED"
        with self.store.transaction(identity.instance_id) as state:
            row = state.validation_runs[manifest["run_id"]]
            row["result"],row["state"] = outcome,status
            state.audit.append({"action":"VALIDATION_FINISHED","run_id":manifest["run_id"],"state":status,"at":self.clock.now().isoformat()})
            state.version += 1
        return outcome
