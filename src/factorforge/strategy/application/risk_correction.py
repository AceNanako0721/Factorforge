"""Immediate actual-fill risk correction, independent of the regular decision gate."""
from datetime import timedelta
from factorforge.strategy.domain.stop import actual_risk_target
from factorforge.strategy.domain.sentiment import pool
from factorforge.strategy.domain.events import digest
from factorforge.strategy.domain.models import DecisionView,TargetOutbox,ZERO


def correct_actual_risk(state,obj,snapshot,at):
    policy = state.policies[obj.time_policy_version]
    target = actual_risk_target(obj,snapshot,policy)
    if target is None or snapshot.owner_epochs[obj.object_id] != obj.owner_epoch:
        return None
    if any(i.decision.object_id == obj.object_id and i.state in {"PENDING","DELIVERY_UNKNOWN","ACK"} and abs(i.decision.target_quantity) <= abs(target) and i.target_version >= snapshot.target_versions[obj.object_id] for i in state.outbox.values()):
        return None
    view = pool(state,obj.object_id)
    identity = "decision-"+digest([state.instance_id,state.environment,obj.object_id,"fill-risk",snapshot.version])[:24]
    inputs = {"pool":view.model_dump(mode="json"),"snapshot":snapshot.model_dump(mode="json"),"policy":policy.version,"parameter_version":obj.parameter_version}
    decision = DecisionView(decision_id=identity,object_id=obj.object_id,cycle_id="fill-risk:"+str(snapshot.version),available_cutoff=at,
        pool_plus=view.plus,pool_minus=view.minus,pool_net=view.net,previous_level=obj.level,new_level=obj.level,raw_exposure=ZERO,projected_exposure=ZERO,
        target_quantity=target,actual_quantity=snapshot.actual[obj.object_id],pending_quantity=snapshot.pending[obj.object_id],stop_plan=None,
        parameter_version=obj.parameter_version,input_hash=digest(inputs),input_snapshot=inputs,reason_codes=["ACTUAL_FILL_STOP_BUDGET_REDUCTION"])
    version = max([snapshot.target_versions[obj.object_id]]+[i.target_version for i in state.outbox.values() if i.decision.object_id == obj.object_id])+1
    state.decisions[identity] = decision
    state.outbox[identity] = TargetOutbox(decision=decision,target_version=version,expires_at=at+timedelta(seconds=policy.target_ttl_seconds),reservation_id=None,state="PENDING",owner_epoch=obj.owner_epoch)
    return decision
