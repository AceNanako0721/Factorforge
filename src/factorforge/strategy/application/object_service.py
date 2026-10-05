from contextlib import contextmanager

from factorforge.strategy.domain.models import PublicPrincipal, WorkloadIdentity, WorkloadCapability, ApiScope, StrategyError,ZERO
from factorforge.strategy.domain.events import digest


def authorize(state,identity,scope,object_id=None):
    if identity.instance_id != state.instance_id or identity.environment != state.environment:
        raise StrategyError("IDENTITY_BINDING_FORBIDDEN",403)
    if isinstance(identity,PublicPrincipal):
        if scope not in identity.scopes:
            raise StrategyError("PUBLIC_SCOPE_FORBIDDEN",403)
    elif isinstance(identity,WorkloadIdentity):
        capability = WorkloadCapability.SIM if state.environment == "SIM" else WorkloadCapability.LIVE
        if capability not in identity.capabilities or object_id and object_id not in identity.object_ids:
            raise StrategyError("WORKLOAD_CAPABILITY_FORBIDDEN",403)
    else:
        raise StrategyError("IDENTITY_UNKNOWN",403)


class ObjectService:
    def __init__(self,store,clock):
        self.store,self.clock = store,clock

    @contextmanager
    def command(self,identity,command,scope,payload,object_id=None):
        with self.store.transaction(identity.instance_id) as state:
            authorize(state,identity,scope,object_id)
            if isinstance(identity,WorkloadIdentity) and object_id and hasattr(self.store,"check_workload"):
                self.store.check_workload(identity,object_id)
            fingerprint = digest({"identity":identity.model_dump(mode="json"),"scope":str(scope),"payload":payload})
            previous = state.dedup.get(command.idempotency_key)
            if previous:
                if previous["hash"] != fingerprint:
                    raise StrategyError("IDEMPOTENCY_CONFLICT",409)
                yield state,previous,True
                return
            if command.expected_version != state.version:
                raise StrategyError("AGGREGATE_VERSION_CONFLICT",409)
            receipt = {"hash":fingerprint,"result":None}
            yield state,receipt,False
            state.version += 1
            state.dedup[command.idempotency_key] = receipt
            state.audit.append({"sequence":len(state.audit)+1,"action":str(scope),"request_id":command.request_id,
                                "identity":identity.model_dump(mode="json"),"at":self.clock.now().isoformat(),"reason":command.reason})

    def create(self,identity,command,obj,policy,params):
        with self.command(identity,command,ApiScope.OBJECT,{"object":obj.model_dump(mode="json"),"policy":policy.model_dump(mode="json"),"parameters":params.model_dump(mode="json")},obj.object_id) as (state,receipt,repeated):
            if not repeated:
                if obj.object_id in state.objects or any(o.owner_id == obj.owner_id and o.trading_run_key == obj.trading_run_key or o.instrument_key == obj.instrument_key and o.trading_run_key == obj.trading_run_key for o in state.objects.values()):
                    raise StrategyError("OBJECT_OWNER_CONFLICT",409)
                if obj.instance_id != state.instance_id or obj.environment != state.environment or obj.trading_run_key.environment != state.environment:
                    raise StrategyError("OBJECT_BINDING_FORBIDDEN",403)
                if obj.parameter_version != params.version or obj.time_policy_version != policy.version or params.scope not in {obj.object_id,"*"}:
                    raise StrategyError("POLICY_BINDING_CONFLICT",409)
                if isinstance(identity,PublicPrincipal) and (state.parameters.get(params.version) != params or state.policies.get(policy.version) != policy):
                    raise StrategyError("OPERATOR_REGISTRY_REQUIRED",403)
                required = {"w","g","h","eta","kappa","k_stop"}
                if params.quality_state == "VALIDATED" and (not required <= params.values.keys() or any(params.values[v] <= 0 for v in required)):
                    raise StrategyError("PARAMETERS_REQUIRED_UNSET",423)
                if state.environment == "LIVE" and (params.quality_state != "VALIDATED" or policy.quality_state != "VALIDATED"):
                    raise StrategyError("PRODUCTION_CALIBRATION_UNSET",423)
                if params.valid_from > self.clock.now():
                    raise StrategyError("PARAMETERS_NOT_YET_AVAILABLE")
                obj.aggregate_version = state.version+1
                obj.owner_epoch,obj.level,obj.direction = 0,0,0
                obj.actual_quantity,obj.pending_quantity = ZERO,ZERO
                obj.last_cycle,obj.last_adjustment,obj.recovery_state = None,None,"RECOVERY_CHECK"
                obj.last_cutoff = None
                state.objects[obj.object_id] = obj.model_copy(deep=True)
                for collection,item in ((state.policies,policy),(state.parameters,params)):
                    if item.version in collection and collection[item.version] != item:
                        raise StrategyError("IMMUTABLE_VERSION_CONFLICT",409)
                    collection[item.version] = item.model_copy(deep=True)
                receipt["result"] = obj.model_dump(mode="json")
            return receipt["result"]

    def set_state(self,identity,command,object_id,new_state):
        with self.command(identity,command,ApiScope.OBJECT,[object_id,new_state],object_id) as (state,receipt,repeated):
            if not repeated:
                obj = state.objects[object_id]
                if new_state == "ARCHIVED" and (obj.actual_quantity != 0 or obj.pending_quantity != 0 or any(c.object_id == object_id and c.status in {"OPEN","OBSERVING"} for c in state.cases.values())):
                    raise StrategyError("OBJECT_HAS_UNSETTLED_CASES",409)
                obj.state,obj.aggregate_version = new_state,state.version+1
                receipt["result"] = obj.model_dump(mode="json")
            return receipt["result"]
