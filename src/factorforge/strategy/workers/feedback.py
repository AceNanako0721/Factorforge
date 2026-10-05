from factorforge.strategy.application.object_service import authorize
from factorforge.strategy.application.case_service import update_cases
from factorforge.strategy.domain.models import ApiScope
from factorforge.strategy.domain.events import digest
from factorforge.strategy.application.risk_correction import correct_actual_risk
from factorforge.strategy.application.decision_cycle import DecisionCycle


class FeedbackWorker:
    def __init__(self,store,trading,clock,identity):
        self.store,self.trading,self.clock,self.identity = store,trading,clock,identity

    def tick(self):
        before = self.store.read(self.identity.instance_id)
        authorize(before,self.identity,ApiScope.QUERY)
        groups = {}
        for obj in before.objects.values():
            if obj.object_id in self.identity.object_ids:
                if hasattr(self.store,"check_workload"):
                    self.store.check_workload(self.identity,obj.object_id)
                groups.setdefault(digest(obj.trading_run_key),[]).append(obj)
        snapshots = {key:self.trading.snapshot(group) for key,group in groups.items()}
        with self.store.transaction(self.identity.instance_id) as state:
            if state.version != before.version:
                from factorforge.strategy.domain.models import StrategyError
                raise StrategyError("FEEDBACK_SNAPSHOT_CONFLICT",409)
            for key,objects in groups.items():
                for previous in objects:
                    obj = state.objects[previous.object_id]
                    update_cases(state,obj,snapshots[key],self.clock.now(),state.policies[obj.time_policy_version],state.parameters[obj.parameter_version])
                    obj.actual_quantity = snapshots[key].actual[obj.object_id]
                    obj.pending_quantity = snapshots[key].pending[obj.object_id]
                    correct_actual_risk(state,obj,snapshots[key],self.clock.now())
            state.version += 1
        DecisionCycle(self.store,self.trading,self.clock).dispatch(self.identity)
