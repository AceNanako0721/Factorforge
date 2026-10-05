"""Atomic account-coordinated cycles; durable outbox crosses the P1 boundary."""
from datetime import timedelta
from decimal import Decimal

from factorforge.strategy.application.object_service import authorize
from factorforge.strategy.application.case_service import update_cases
from factorforge.strategy.application.learning_service import evaluate_learning
from factorforge.strategy.application.risk_correction import correct_actual_risk
from factorforge.strategy.domain.case import price_at
from factorforge.strategy.domain.events import digest, score_amount,fact_key
from factorforge.strategy.domain.factors import valid_manifest
from factorforge.strategy.domain.models import utc
from factorforge.strategy.domain.learning import activate,monitor
from factorforge.strategy.domain.models import Contribution, DecisionView, TargetOutbox, WorkloadIdentity, ZERO, ONE, ApiScope, StrategyError
from factorforge.strategy.domain.position import level_for, exposure, quantity, shared_projection,existing_risk_scale,reduce_quantity
from factorforge.strategy.domain.regime import update_regime
from factorforge.strategy.domain.sentiment import advance_contributions, append_entry, decay, pool, verify_ledger
from factorforge.strategy.domain.stop import normal_noise, size_and_stop
from factorforge.strategy.domain.time_policy import reserve


def inject_ready(state,obj,at,sample,policy,params):
    for identity,receipt in list(state.receipts.items()):
        score = state.scores[identity]
        if score.object_id != obj.object_id or receipt.state not in {"READY","READY_PENDING_PRICE"} or receipt.eligible_from > at:
            continue
        if sample is None or sample.quality != "VALID":
            continue
        if sample.benchmark is None and not policy.absolute_price_proxy_validated:
            receipt.reason_codes = ["PRICE_PROXY_UNVALIDATED"]
            continue
        event = state.events[score.event_id]
        if event.state != "VERIFIED":
            receipt.state,receipt.reason_codes = "QUARANTINED",["EVENT_INVALID"]
            continue
        assessment = state.prepricing_assessments.get(identity)
        if assessment is None:
            receipt.state,receipt.reason_codes = "QUARANTINED",["PREPRICING_EVIDENCE_UNVERIFIED"]
            continue
        p = Decimal(assessment["fraction"])
        old = next((c for c in state.contributions.values() if c.object_id == obj.object_id and c.event_id == event.event_id),None)
        if old and score.revision_kind == "REVISION":
            if old.state != "ACTIVE" or score.vector.direction != old.direction:
                receipt.state,receipt.reason_codes = "QUARANTINED",["INVALID_CONTRIBUTION_OR_DIRECTION_REVISION"]
                continue
            frozen = state.parameters[old.frozen_parameter_version]
            amount,_,raw = score_amount(score,event,frozen,policy,assessed_prepricing=p)
            # Replay the original elapsed intervals and recorded price consumption.
            # Never reset age/reference/high water or edit historical ledger rows.
            revised = amount
            previous_at = old.effective_at
            for row in state.ledger:
                if row.contribution_id != old.contribution_id:
                    continue
                if row.reason == "TIME":
                    revised = decay(revised,Decimal(str((row.at-previous_at).total_seconds())),old.half_life)
                    previous_at = row.at
                elif row.price_consumption:
                    revised = max(ZERO,revised-row.price_consumption)
                elif row.invalidation and row.start > 0:
                    revised *= max(ZERO,ONE-row.invalidation/row.start)
            view = pool(state,obj.object_id)
            family = sum((c.remaining_amount for c in view.contributions if c.family_id == old.family_id and c != old),ZERO)
            revised = min(revised,max(ZERO,policy.object_cap-view.plus-view.minus+old.remaining_amount),max(ZERO,policy.family_cap-family))
            append_entry(state,old,at,"SCORE_REVISION",revision_delta=revised-old.remaining_amount)
            old.initial_amount,old.score_id = amount,identity
            old.quality = score.vector.quality_score
            receipt.contribution_id = old.contribution_id
            if score.previous_score_id in state.receipts:
                state.receipts[score.previous_score_id].state = "SUPERSEDED"
        elif old:
            receipt.state,receipt.reason_codes = "QUARANTINED",["DUPLICATE_FACT"]
            continue
        else:
            tau = receipt.eligible_from
            reference = price_at(state.samples.get(obj.object_id,[]),tau)
            if reference is None or (tau-reference.available_at).total_seconds() > policy.cycle_seconds:
                tau,reference = max(tau,sample.available_at),sample
                receipt.eligible_from = tau
            if tau > at:
                continue
            amount,h,raw = score_amount(score,event,params,policy,assessed_prepricing=p)
            view = pool(state,obj.object_id)
            family = sum((c.remaining_amount for c in view.contributions if c.family_id == event.family_id),ZERO)
            accepted = min(amount,max(ZERO,policy.family_cap-family),max(ZERO,policy.object_cap-view.plus-view.minus))
            if score.vector.direction == 0:
                accepted = ZERO
            contribution = Contribution(contribution_id="contribution-"+identity,object_id=obj.object_id,event_id=event.event_id,
                family_id=event.family_id,direction=score.vector.direction,initial_amount=accepted,remaining_amount=ZERO,
                effective_at=tau,last_updated_at=tau,half_life=h,eta=params.values["eta"],high_water=ZERO,
                reference_price=reference.price,reference_benchmark=reference.benchmark,frozen_parameter_version=params.version,
                score_id=identity,quality=score.vector.quality_score,fact_weights={fact_key(c):c.weight for c in event.claims})
            state.contributions[contribution.contribution_id] = contribution
            append_entry(state,contribution,tau,"INJECTION",injection=accepted,rejected_amount=raw-accepted)
            after = decay(accepted,Decimal(str((at-tau).total_seconds())),h)
            append_entry(state,contribution,at,"TIME",time_consumption=accepted-after)
            contribution.last_updated_at = at
            receipt.contribution_id = contribution.contribution_id
        receipt.state = "APPLIED"


class DecisionCycle:
    def __init__(self,store,trading,clock):
        self.store,self.trading,self.clock = store,trading,clock

    def tick(self,identity,at=None):
        if not isinstance(identity,WorkloadIdentity):
            raise StrategyError("WORKLOAD_IDENTITY_REQUIRED",403)
        at = at or self.clock.now()
        if at > self.clock.now():
            raise StrategyError("CYCLE_TIME_NOT_YET_AVAILABLE")
        before = self.store.read(identity.instance_id)
        authorize(before,identity,ApiScope.QUERY)
        objects = [o for o in before.objects.values() if o.object_id in identity.object_ids]
        if hasattr(self.store,"check_workload"):
            for obj in objects:
                self.store.check_workload(identity,obj.object_id)
        groups = {}
        for obj in objects:
            groups.setdefault(digest(obj.trading_run_key),[]).append(obj)
        snapshots = {key:self.trading.snapshot(sorted(group,key=lambda o:o.object_id)) for key,group in groups.items()}
        if any(s.at > at for s in snapshots.values()):
            raise StrategyError("FUTURE_TRADING_SNAPSHOT")
        with self.store.transaction(identity.instance_id) as state:
            if state.version != before.version:
                raise StrategyError("CYCLE_SNAPSHOT_CONFLICT",409)
            outputs = []
            for key,group in sorted(groups.items()):
                snap = snapshots[key]
                due,base,rows = [],list(snap.other_exposures),[]
                for obj_before in sorted(group,key=lambda o:o.object_id):
                    obj = state.objects[obj_before.object_id]
                    policy = state.policies[obj.time_policy_version]
                    scheduled = policy.window_anchor+timedelta(seconds=int((at-policy.window_anchor).total_seconds()//policy.cycle_seconds)*policy.cycle_seconds)
                    if obj.last_cutoff and at < obj.last_cutoff:
                        raise StrategyError("CLOCK_REWIND")
                    params = state.parameters[obj.parameter_version]
                    verify_ledger(state,policy.numerical_tolerance)
                    actual,pending = snap.actual[obj.object_id],snap.pending[obj.object_id]
                    obj.actual_quantity,obj.pending_quantity = actual,pending
                    epoch = snap.owner_epochs[obj.object_id]
                    obj.recovery_state = "NORMAL" if epoch == obj.owner_epoch and not snap.external_change and snap.run_state == "NORMAL" else "RECOVERY_REQUIRED"
                    sample = snap.samples.get(obj.object_id)
                    if sample and (at-sample.available_at).total_seconds() > policy.max_price_age_seconds:
                        sample = sample.model_copy(update={"quality":"STALE"})
                    bindings = state.factor_manifests.get(obj.price_proxy_binding,{})
                    if sample is not None:
                        observations = sorted([v for v in bindings.get("observations",[]) if utc(v["available_at"]) <= at],key=lambda v:utc(v["available_at"]))
                        if valid_manifest(bindings,"PRICE_PROXY",at) and observations:
                            v = observations[-1]
                            sample = sample.model_copy(update={"available_at":max(sample.available_at,utc(v["available_at"])),"sigma":Decimal(v["sigma"]) if v.get("sigma") else None,
                                "liquidity":Decimal(v["liquidity"]) if v.get("liquidity") else None,
                                "benchmark":Decimal(v["benchmark"]) if v.get("benchmark") else None})
                        samples = state.samples.setdefault(obj.object_id,[])
                        if not samples or samples[-1].available_at != sample.available_at:
                            samples.append(sample)
                    if not {"w","g","h","eta","kappa","k_stop"} <= params.values.keys():
                        if obj.last_cycle and (at-obj.last_cycle).total_seconds() < policy.cycle_seconds:
                            continue
                        # A research object can exist before calibration. Record the
                        # unknown state; do not synthesize risk/observation constants.
                        advance_contributions(state,obj,at,None,policy,params)
                        view = pool(state,obj.object_id)
                        identity_key = "decision-"+digest([state.instance_id,state.environment,obj.trading_run_key.model_dump(),obj.object_id,scheduled.isoformat()])[:24]
                        decision = DecisionView(decision_id=identity_key,object_id=obj.object_id,cycle_id=f"{obj.object_id}:{at.isoformat()}",available_cutoff=at,
                            pool_plus=view.plus,pool_minus=view.minus,pool_net=view.net,previous_level=obj.level,new_level=obj.level,raw_exposure=ZERO,projected_exposure=ZERO,
                            target_quantity=actual+pending,actual_quantity=actual,pending_quantity=pending,stop_plan=None,parameter_version=obj.parameter_version,
                            input_hash=digest([snap.model_dump(mode="json"),params.model_dump(mode="json")]),reason_codes=["PARAMETERS_REQUIRED_UNSET"])
                        state.decisions[identity_key] = decision
                        obj.last_cycle,obj.last_cutoff = scheduled,at
                        outputs.append(decision)
                        if sample:
                            value = (actual+pending)*sample.price*Decimal(snap.specs[obj.object_id]["contract_multiplier"])
                            base.append((value,abs(value),snap.specs[obj.object_id].get("risk_group") or "DEFAULT"))
                        continue
                    update_cases(state,obj,snap,at,policy,params)
                    corrected = correct_actual_risk(state,obj,snap,at)
                    if corrected:
                        outputs.append(corrected)
                        # The urgent reduction is only a target until P1 fills
                        # it. Reserve this object's observed exposure while
                        # projecting other objects in the same account.
                        if sample:
                            spec=snap.specs[obj.object_id]
                            value=(actual+pending)*sample.price*Decimal(spec["contract_multiplier"])
                            base.append((value,abs(value)*policy.max_stop_fraction,spec.get("risk_group") or "DEFAULT"))
                        continue
                    if obj.last_cycle and at < obj.last_cycle:
                        raise StrategyError("CLOCK_REWIND")
                    if obj.last_cycle and (at-obj.last_cycle).total_seconds() < policy.cycle_seconds:
                        if sample:
                            spec = snap.specs[obj.object_id]
                            value = (actual+pending)*sample.price*Decimal(spec["contract_multiplier"])
                            base.append((value,abs(value)*policy.max_stop_fraction,(spec.get("risk_group") or "DEFAULT")))
                        continue
                    if obj.last_cycle:
                        missed = int((scheduled-obj.last_cycle).total_seconds()//policy.cycle_seconds)-1
                        if missed:
                            state.skipped_cycles.append({"object_id":obj.object_id,"from":obj.last_cycle.isoformat(),"to":at.isoformat(),"count":missed,"state":"SKIPPED"})
                    activate(state,obj,at)
                    monitor(state,obj,at)
                    params = state.parameters[obj.parameter_version]
                    for c in state.contributions.values():
                        if c.object_id == obj.object_id and c.state == "ACTIVE" and state.events[c.event_id].state == "RETRACTED":
                            append_entry(state,c,at,"RETRACTED",invalidation=c.remaining_amount)
                            c.state = "INVALID"
                        elif c.object_id == obj.object_id and c.state == "ACTIVE" and c.fact_weights and state.events[c.event_id].relation == "CORRECTION":
                            current_keys = {fact_key(claim) for claim in state.events[c.event_id].claims}
                            removed = {key:weight for key,weight in c.fact_weights.items() if key not in current_keys}
                            if removed:
                                fraction = sum(removed.values(),ZERO)/sum(c.fact_weights.values(),ZERO)
                                append_entry(state,c,at,"CLAIM_INVALIDATED",invalidation=c.remaining_amount*fraction)
                                c.fact_weights = {key:weight for key,weight in c.fact_weights.items() if key in current_keys}
                                if not c.fact_weights:
                                    c.state = "INVALID"
                    advance_contributions(state,obj,at,sample,policy,params)
                    inject_ready(state,obj,at,sample,policy,params)
                    view = pool(state,obj.object_id)
                    regime = update_regime(state.regimes.get(obj.object_id,{}),state.samples.get(obj.object_id,[]),at,policy,state.factor_manifests.get(obj.regime_binding,{}))
                    state.regimes[obj.object_id] = regime
                    evaluate_learning(state,obj,at)
                    level = level_for(abs(view.net),obj.level,policy.levels)
                    raw = exposure(view,level,sample,snap.equity,policy)
                    if any(v.get("state") == "UNKNOWN" for v in regime.values()):
                        raw *= policy.unknown_regime_multiplier
                    target,stop,stress = actual,None,policy.max_stop_fraction
                    reasons = []
                    if sample is None or sample.quality != "VALID":
                        reasons.append("PRICE_UNKNOWN")
                    else:
                        spec = snap.specs[obj.object_id]
                        if spec["settlement_currency"] != bindings.get("account_currency"):
                            raw = ZERO
                            reasons.append("FX_OR_CURRENCY_UNKNOWN")
                        target = quantity(raw,snap.equity,sample.price,Decimal(spec["contract_multiplier"]),Decimal(spec["quantity_step"]))
                        if target < 0 and "SHORT" not in spec["capabilities"]:
                            target = ZERO
                            reasons.append("SHORT_UNAVAILABLE")
                        if view.net*actual < 0 or view.net*pending < 0:
                            target = ZERO
                            reasons.append("FLATTEN_BEFORE_REVERSE")
                        if target:
                            target,stop,stop_reason,stress = size_and_stop(target,sample.price,spec,normal_noise(snap.bars[obj.object_id],at,policy),policy,params)
                            reasons.append(stop_reason)
                        if actual and target*actual > 0 and abs(target) <= abs(actual):
                            # Existing protection belongs to P1; reduced targets do not replace/widen it.
                            stop = None
                        elif actual and stop:
                            active = [p for p in snap.protections.get(obj.object_id,[]) if p["state"] == "ACTIVE_VERIFIED"]
                            if not active:
                                target,stop = actual,None
                                reasons.append("EXISTING_PROTECTION_UNVERIFIED")
                            else:
                                tightest = max(Decimal(p["plan"]["trigger_price"]) for p in active) if actual > 0 else min(Decimal(p["plan"]["trigger_price"]) for p in active)
                                stop.trigger_price = max(stop.trigger_price,tightest) if actual > 0 else min(stop.trigger_price,tightest)
                                if actual*(sample.price-stop.trigger_price) <= 0:
                                    target,stop = actual,None
                                    reasons.append("EXISTING_PROTECTION_CROSSED")
                    blocked = obj.state != "ACTIVE" or obj.recovery_state != "NORMAL" or snap.risk_locks or policy.quality_state != "VALIDATED" or params.quality_state != "VALIDATED"
                    unknown_delivery = any(i.decision.object_id == obj.object_id and i.state in {"PENDING","DELIVERY_UNKNOWN"} for i in state.outbox.values())
                    if (blocked or unknown_delivery) and abs(target) > abs(actual):
                        target,stop = actual,None
                        reasons.append("RECOVERY_OR_PAUSE_OR_DELIVERY_BLOCK")
                    stop_now = any(f.get("protection_exit") and f["owner_id"] == obj.owner_id and (utc(f["happened_at"]) > obj.last_cutoff if obj.last_cutoff else utc(f["happened_at"]) >= at) for f in snap.fills)
                    if stop_now and abs(target) > abs(actual):
                        target,stop = actual,None
                        reasons.append("STOP_CYCLE_NO_REOPEN")
                    increasing = abs(target) > abs(actual+pending)
                    if increasing and (abs(target-actual-pending) < policy.min_adjustment or obj.last_adjustment and (at-obj.last_adjustment).total_seconds() < policy.cooldown_seconds):
                        target,stop = actual+pending,None
                        reasons.append("ADJUSTMENT_COOLDOWN")
                    decision_id = "decision-"+digest([state.instance_id,state.environment,obj.trading_run_key.model_dump(),obj.object_id,scheduled.isoformat()])[:24]
                    due.append((obj,policy,sample,target,stop,stress,view,level,raw,reasons,decision_id))
                    if sample:
                        mult = Decimal(snap.specs[obj.object_id]["contract_multiplier"])
                        rows.append(((actual+pending)*sample.price*mult,target*sample.price*mult,stress,(snap.specs[obj.object_id].get("risk_group") or "DEFAULT")))
                scales = [existing_risk_scale(rows,base,p) for _,p,*_ in due]
                unresolved = any(scale is None for scale in scales)
                risk_scale = min((scale for scale in scales if scale is not None),default=ONE)
                if risk_scale<ONE and not unresolved:
                    corrected_due,corrected_rows = [],[]
                    for obj,policy,sample,target,stop,stress,view,level,raw,reasons,decision_id in due:
                        if sample and sample.quality=="VALID":
                            spec=snap.specs[obj.object_id]
                            current=obj.actual_quantity+obj.pending_quantity
                            target=reduce_quantity(current,target,risk_scale,Decimal(spec["quantity_step"]))
                            if abs(target)<=abs(obj.actual_quantity):
                                stop=None
                            elif stop:
                                stop.covered_quantity=abs(target)
                            reasons.append("ACCOUNT_EXISTING_RISK_REDUCED")
                        corrected_due.append((obj,policy,sample,target,stop,stress,view,level,raw,reasons,decision_id))
                        if sample:
                            spec=snap.specs[obj.object_id]
                            multiplier=Decimal(spec["contract_multiplier"])
                            corrected_rows.append(((obj.actual_quantity+obj.pending_quantity)*sample.price*multiplier,target*sample.price*multiplier,stress,spec.get("risk_group") or "DEFAULT"))
                    due,rows=corrected_due,corrected_rows
                    # Quantity steps can make a continuously feasible hedge
                    # infeasible. Keep known exposure/protection in that case;
                    # do not send a net-risk-increasing "reduction" or claim success.
                    if any(existing_risk_scale(rows,base,p)!=ONE for _,p,*_ in due):
                        unresolved=True
                        restored=[]
                        for obj,policy,sample,target,stop,stress,view,level,raw,reasons,decision_id in due:
                            target,stop=obj.actual_quantity+obj.pending_quantity,None
                            if "ACCOUNT_EXISTING_RISK_REDUCED" in reasons:
                                reasons.remove("ACCOUNT_EXISTING_RISK_REDUCED")
                            reasons.append("ACCOUNT_QUANTITY_STEP_RECONCILIATION_REQUIRED")
                            restored.append((obj,policy,sample,target,stop,stress,view,level,raw,reasons,decision_id))
                        due=restored
                alpha = ZERO if unresolved else min((shared_projection(rows,base,p) for _,p,*_ in due),default=ONE)
                for obj,policy,sample,target,stop,stress,view,level,raw,reasons,decision_id in due:
                    actual,pending = obj.actual_quantity,obj.pending_quantity
                    if unresolved:
                        reasons.append("ACCOUNT_RISK_RECONCILIATION_REQUIRED")
                    if alpha < 1 and abs(target) > abs(actual+pending):
                        spec = snap.specs[obj.object_id]
                        step = Decimal(spec["quantity_step"])
                        target = quantity((actual+pending+alpha*(target-actual-pending))*sample.price*Decimal(spec["contract_multiplier"])/snap.equity,snap.equity,sample.price,Decimal(spec["contract_multiplier"]),step)
                        reasons.append("ACCOUNT_ALPHA_PROJECTED")
                        if stop and target:
                            stop.covered_quantity = abs(target)
                    reservation = None
                    if abs(target) > abs(actual+pending):
                        try:
                            reservation = reserve(state,obj,at,policy,decision_id)
                        except StrategyError as error:
                            target,stop = actual+pending,None
                            reasons.append(error.code)
                    if target == 0:
                        stop = None
                    inputs = {"pool":view.model_dump(mode="json"),"snapshot":snap.model_dump(mode="json"),"policy":policy.version,"parameter_version":obj.parameter_version,"regime":state.regimes[obj.object_id],
                        "prepricing":{c.score_id:state.prepricing_assessments[c.score_id] for c in view.contributions if c.score_id in state.prepricing_assessments}}
                    decision = DecisionView(decision_id=decision_id,object_id=obj.object_id,cycle_id=f"{obj.object_id}:{scheduled.isoformat()}",available_cutoff=at,
                        pool_plus=view.plus,pool_minus=view.minus,pool_net=view.net,previous_level=obj.level,new_level=level,
                        raw_exposure=raw,projected_exposure=target*sample.price*Decimal(snap.specs[obj.object_id]["contract_multiplier"])/snap.equity if sample and snap.equity > 0 else ZERO,
                        target_quantity=target,actual_quantity=actual,pending_quantity=pending,stop_plan=stop,parameter_version=obj.parameter_version,
                        input_hash=digest(inputs),input_snapshot=inputs,reason_codes=reasons or ["TARGET_COMPUTED"],
                        source_event_versions={c.event_id:state.events[c.event_id].fact_version for c in view.contributions if c.remaining_amount > 0})
                    state.decisions[decision_id] = decision
                    can_reduce = target*actual >= 0 and abs(target) <= abs(actual) and obj.owner_epoch == snap.owner_epochs[obj.object_id]
                    unknown = any(i.decision.object_id == obj.object_id and i.state in {"PENDING","DELIVERY_UNKNOWN"} for i in state.outbox.values())
                    if sample and sample.quality == "VALID" and (obj.recovery_state == "NORMAL" or can_reduce) and (not unknown or can_reduce):
                        version = max([snap.target_versions[obj.object_id]]+[i.target_version for i in state.outbox.values() if i.decision.object_id == obj.object_id])+1
                        state.outbox[decision_id] = TargetOutbox(decision=decision,target_version=version,expires_at=at+timedelta(seconds=policy.target_ttl_seconds),reservation_id=reservation.reservation_id if reservation else None,state="PENDING",owner_epoch=obj.owner_epoch)
                    obj.last_cycle,obj.last_cutoff,obj.level,obj.direction = scheduled,at,level,1 if view.net > 0 else -1 if view.net < 0 else 0
                    verify_ledger(state,policy.numerical_tolerance)
                    outputs.append(decision)
            state.version += 1
            state.audit.append({"action":"CYCLE_COMMITTED","at":at.isoformat(),"decisions":[d.decision_id for d in outputs],"snapshot_version":before.version})
            return outputs

    def dispatch(self,identity):
        before = self.store.read(identity.instance_id)
        authorize(before,identity,ApiScope.QUERY)
        for key,item in before.outbox.items():
            if item.state not in {"PENDING","DELIVERY_UNKNOWN"}:
                continue
            obj = before.objects[item.decision.object_id]
            authorize(before,identity,ApiScope.QUERY,obj.object_id)
            if hasattr(self.store,"check_workload"):
                self.store.check_workload(identity,obj.object_id)
            now = self.clock.now()
            snapshot = self.trading.snapshot([obj])
            increase = abs(item.decision.target_quantity) > abs(snapshot.actual[obj.object_id]+snapshot.pending[obj.object_id])
            revoked = obj.state != "ACTIVE" or snapshot.risk_locks or snapshot.run_state != "NORMAL" or snapshot.owner_epochs[obj.object_id] != item.owner_epoch
            revoked = revoked or any(before.events[event_id].state == "RETRACTED" and before.events[event_id].fact_version > version for event_id,version in item.decision.source_event_versions.items())
            if key in snapshot.source_decisions:
                status,reason = "ACK",None
            elif increase and revoked:
                status = "EXPIRED" if item.state == "PENDING" else "DELIVERY_UNKNOWN"
                reason = "RISK_REVOKED_BEFORE_DELIVERY"
            elif now >= item.expires_at:
                status,reason = "EXPIRED","TARGET_EXPIRED"
            else:
                # Persist uncertainty BEFORE network I/O. Crash/retry queries P1 first.
                with self.store.transaction(identity.instance_id) as state:
                    if state.outbox[key].state not in {"PENDING","DELIVERY_UNKNOWN"}:
                        continue
                    if state.outbox[key].command_payload is None:
                        state.outbox[key].command_payload = self.trading.prepare(obj,item,snapshot)
                    item.command_payload = state.outbox[key].command_payload
                    state.outbox[key].state = "DELIVERY_UNKNOWN"
                    if item.reservation_id:
                        state.reservations[item.reservation_id].state = "UNKNOWN"
                try:
                    if hasattr(self.store,"check_workload"):
                        self.store.check_workload(identity,obj.object_id)
                    self.trading.deliver(obj,item,snapshot)
                    status,reason = "ACK",None
                except StrategyError as error:
                    status = "DELIVERY_UNKNOWN" if error.status >= 500 else "REJECTED"
                    reason = error.code
            with self.store.transaction(identity.instance_id) as state:
                row = state.outbox[key]
                row.state,row.reason = status,reason
                if status == "ACK":
                    row.accepted_at = now
                    state.objects[obj.object_id].last_adjustment = now
                if item.reservation_id:
                    state.reservations[item.reservation_id].state = "ACCEPTED" if status == "ACK" else "RELEASED" if status == "REJECTED" or status == "EXPIRED" and item.state == "PENDING" else "UNKNOWN"
                state.audit.append({"action":"TARGET_DELIVERY","decision_id":key,"state":status,"reason":reason,"at":now.isoformat()})
                state.version += 1
