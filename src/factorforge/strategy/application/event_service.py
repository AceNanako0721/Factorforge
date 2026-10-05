from factorforge.strategy.application.object_service import ObjectService
from factorforge.strategy.domain.events import verify_event,fact_key
from factorforge.strategy.domain.models import ApiScope, StrategyError, WorkloadIdentity
from factorforge.strategy.domain.sentiment import append_entry


class EventService(ObjectService):
    def register(self,identity,command,event):
        with self.command(identity,command,ApiScope.RESEARCH,event.model_dump(mode="json")) as (state,receipt,repeated):
            if not repeated:
                if not event.object_ids <= state.objects.keys():
                    raise StrategyError("OBJECT_NOT_FOUND",404)
                if isinstance(identity,WorkloadIdentity):
                    if not event.object_ids <= identity.object_ids:
                        raise StrategyError("WORKLOAD_OBJECT_SCOPE_FORBIDDEN",403)
                    if hasattr(self.store,"check_workload"):
                        for object_id in event.object_ids:
                            self.store.check_workload(identity,object_id)
                previous = state.events.get(event.event_id)
                if previous:
                    if event.fact_version != previous.fact_version+1 or event.relation == "NEW":
                        raise StrategyError("FACT_VERSION_CONFLICT",409)
                elif event.fact_version != 1:
                    raise StrategyError("FACT_VERSION_CONFLICT",409)
                if event.relation == "NEW_FACT" and (event.parent_event_id not in state.events or event.parent_event_id == event.event_id):
                    raise StrategyError("NEW_FACT_PARENT_REQUIRED")
                for peer in state.events.values():
                    if peer.family_id != event.family_id and any(c.normalized_fact == other.normalized_fact and c.subject_id == other.subject_id and c.economic_item == other.economic_item and c.period == other.period for c in event.claims for other in peer.claims):
                        raise StrategyError("FACT_FAMILY_CONFLICT",409)
                event = verify_event(event,list(state.events.values()),self.clock.now())
                policies = [state.policies[state.objects[o].time_policy_version] for o in event.object_ids]
                if not policies or any(any(c.verification_manifest not in p.rubrics or p.claim_weights.get(c.economic_item) != c.weight for c in event.claims)
                        or any(e.verification_ref not in p.verification_manifests for e in event.evidence_refs) for p in policies):
                    event.state = "QUARANTINED"
                if previous and event.relation in {"CONFIRMATION","CORRECTION"}:
                    event.novelty = previous.novelty
                    if event.relation == "CONFIRMATION" and {fact_key(c) for c in event.claims} != {fact_key(c) for c in previous.claims}:
                        event.state = "QUARANTINED"
                state.events[event.event_id] = event
                version_key = f"{event.event_id}:{event.fact_version}"
                state.event_versions[version_key] = event.model_copy(deep=True)
                state.event_received[version_key] = self.clock.now()
                if event.state == "RETRACTED" and isinstance(identity,WorkloadIdentity):
                    # Retraction records do not require an executable capability; they only remove risk evidence.
                    for c in state.contributions.values():
                        if c.event_id == event.event_id and c.state == "ACTIVE":
                            append_entry(state,c,self.clock.now(),"RETRACTED",invalidation=c.remaining_amount)
                            c.state = "INVALID"
                receipt["result"] = event.model_dump(mode="json")
            return receipt["result"]
