from factorforge.strategy.application.object_service import ObjectService
from factorforge.strategy.domain.events import digest,assess_prepricing
from factorforge.strategy.domain.models import AdmissionReceipt, ApiScope, WorkloadIdentity, StrategyError


class ScoreAdmission(ObjectService):
    def submit(self,identity,command,score):
        with self.command(identity,command,ApiScope.RESEARCH,score.model_dump(mode="json"),score.object_id) as (state,receipt,repeated):
            if not repeated:
                if score.event_id not in state.events or score.object_id not in state.objects:
                    raise StrategyError("EVENT_OR_OBJECT_NOT_FOUND",404)
                event,obj = state.events[score.event_id],state.objects[score.object_id]
                if score.object_id not in event.object_ids or score.fact_version != event.fact_version:
                    raise StrategyError("FACT_VERSION_CONFLICT",409)
                if score.submission_id in state.scores:
                    if state.scores[score.submission_id] != score:
                        raise StrategyError("SCORE_ID_CONFLICT",409)
                    receipt["result"] = (state.receipts.get(score.submission_id) or state.research_receipts[score.submission_id]).model_dump(mode="json")
                    return receipt["result"]
                canonical = score.model_dump(mode="json",exclude={"submission_id"})
                duplicate = next((key for key,s in state.scores.items() if (key in state.receipts) == isinstance(identity,WorkloadIdentity) and digest(s.model_dump(mode="json",exclude={"submission_id"})) == digest(canonical)),None)
                if duplicate:
                    receipt["result"] = (state.receipts.get(duplicate) or state.research_receipts[duplicate]).model_dump(mode="json")
                    return receipt["result"]
                prior = state.scores.get(score.previous_score_id)
                same_fact = [s for s in state.scores.values() if s.event_id == score.event_id and s.object_id == score.object_id and s.submission_id in state.receipts and state.receipts[s.submission_id].state not in {"DRAFT","RESEARCH_ONLY","QUARANTINED"}]
                if score.revision_kind == "REVISION":
                    if not prior or prior.object_id != score.object_id or prior.event_id != score.event_id or score.score_version != prior.score_version+1 or prior in same_fact and prior != same_fact[-1]:
                        raise StrategyError("SCORE_REVISION_CONFLICT",409)
                elif same_fact:
                    raise StrategyError("EXPLICIT_REVISION_REQUIRED",409)
                policy,params = state.policies[obj.time_policy_version],state.parameters[obj.parameter_version]
                vector = score.vector
                required = ("credibility","relevance","novelty","expectation_coverage","prepricing_fraction","expected_half_life","quality_score")
                missing = [name for name in required if getattr(vector,name) is None]
                reasons = []
                if missing or vector.unknown_fields:
                    status,reasons = "DRAFT",["UNKNOWN:"+name for name in sorted(set(missing+vector.unknown_fields))]
                elif not isinstance(identity,WorkloadIdentity):
                    status,reasons = "RESEARCH_ONLY",["PUBLIC_RESEARCH_IDENTITY"]
                elif event.state != "VERIFIED" or not set(score.evidence_refs) <= {e.evidence_id for e in event.evidence_refs} or not score.evidence_refs:
                    status,reasons = "QUARANTINED",["INVALID_EVIDENCE"]
                elif score.calibration_version not in policy.calibrations or score.rubric_version not in policy.rubrics:
                    status,reasons = "QUARANTINED",["UNCALIBRATED_SCORE"]
                elif not policy.half_life_bounds[0] <= vector.expected_half_life <= policy.half_life_bounds[1]:
                    status,reasons = "QUARANTINED",["HALF_LIFE_NOT_VALIDATED"]
                elif policy.quality_state != "VALIDATED" or params.quality_state != "VALIDATED":
                    status,reasons = "RESEARCH_ONLY",["PARAMETERS_UNVALIDATED"]
                else:
                    assessment,error = assess_prepricing(score,state.factor_manifests.get("prepricing:"+score.input_manifest_hash,{}),policy,self.clock.now())
                    if error:
                        status,reasons = "QUARANTINED",[error]
                    else:
                        status = "READY_PENDING_PRICE"
                        state.prepricing_assessments[score.submission_id] = assessment
                now = self.clock.now()
                eligible = max([now,score.completed_at]+[e.available_at for e in event.evidence_refs]+[c.verified_at for c in event.claims])
                admission = AdmissionReceipt(submission_id=score.submission_id,state=status,eligible_from=eligible,
                    reason_codes=reasons,current_version=state.version+1)
                state.scores[score.submission_id] = score
                (state.receipts if isinstance(identity,WorkloadIdentity) else state.research_receipts)[score.submission_id] = admission
                state.received[score.submission_id] = now
                receipt["result"] = admission.model_dump(mode="json")
            return receipt["result"]
