"""Distinct listeners and identity types: a public key cannot submit a trade signal."""
import hmac
from typing import Annotated

from fastapi import FastAPI, Depends, Request
from fastapi.exceptions import RequestValidationError
from fastapi.responses import JSONResponse
from fastapi.security import HTTPBearer, HTTPAuthorizationCredentials

from factorforge.strategy.api.dto import CreateObject, EventCommand, ScoreCommand, StateCommand, AttributionCommand, AdvanceClock, Problem
from factorforge.strategy.api.dto import AttributionView,LearningDecisionView,ValidationRunView
from factorforge.strategy.application.object_service import ObjectService,authorize
from factorforge.strategy.application.event_service import EventService
from factorforge.strategy.application.score_admission import ScoreAdmission
from factorforge.strategy.application.case_service import CaseService
from factorforge.strategy.domain.models import (PublicPrincipal,WorkloadIdentity,ApiScope,ObservedObject,Event,
    AdmissionReceipt,PoolView,DecisionView,CaseRecord,ParameterSnapshot,TargetOutbox,StrategyError)
from factorforge.strategy.domain.sentiment import pool

PREFIX = "/api/v2/strategy"


def create_app(store,clock,tokens,internal=False,cycle=None):
    expected_type = WorkloadIdentity if internal else PublicPrincipal
    if any(not isinstance(p,expected_type) for p in tokens.values()):
        raise StrategyError("AUTH_LISTENER_IDENTITY_TYPE_MISMATCH")
    app = FastAPI(title="Factorforge Strategy "+("Workload" if internal else "Research"),version="strategy-2.0",docs_url=None,redoc_url=None,
        responses={status:{"model":Problem} for status in (401,403,404,409,422,423,503)})
    object_service,event_service,score_service,case_service = (cls(store,clock) for cls in (ObjectService,EventService,ScoreAdmission,CaseService))

    @app.exception_handler(StrategyError)
    async def problem(request,error):
        principal = getattr(request.state,"principal",None)
        if principal:
            try:
                with store.transaction(principal.instance_id) as state:
                    state.audit.append({"action":"REQUEST_REJECTED","code":error.code,"at":clock.now().isoformat(),"actor":principal.model_dump(mode="json")})
                    state.version += 1
            except StrategyError:
                pass
        return JSONResponse(status_code=error.status,content={"code":error.code,"message":error.code,"field":None,"correlation_id":request.headers.get("x-request-id"),"retryable":False})

    @app.exception_handler(RequestValidationError)
    async def invalid(request,error):
        principal = getattr(request.state,"principal",None)
        if principal:
            try:
                with store.transaction(principal.instance_id) as state:
                    state.audit.append({"action":"REQUEST_REJECTED","code":"INVALID_REQUEST","at":clock.now().isoformat()})
                    state.version += 1
            except StrategyError:
                pass
        return JSONResponse(status_code=422,content={"code":"INVALID_REQUEST","message":"INVALID_REQUEST","field":None,"correlation_id":None,"retryable":False})

    def identity(request:Request,credentials:Annotated[HTTPAuthorizationCredentials|None,Depends(HTTPBearer(auto_error=False))]):
        if credentials:
            for token,principal in tokens.items():
                if token and hmac.compare_digest(token,credentials.credentials):
                    request.state.principal = principal
                    return principal
        raise StrategyError("AUTHENTICATION_REQUIRED",401)
    Auth = Depends(identity)

    def query(principal,object_id=None):
        state = store.read(principal.instance_id)
        authorize(state,principal,ApiScope.QUERY,object_id)
        if object_id and object_id not in state.objects:
            raise StrategyError("OBJECT_NOT_FOUND",404)
        return state

    @app.get(PREFIX+"/health")
    def health():
        return {"schema_version":"strategy-2.0","application_required":False,"environment":store.environment,"listener":"WORKLOAD" if internal else "PUBLIC_RESEARCH","live_ready":False}

    @app.post(PREFIX+"/objects",response_model=ObservedObject,status_code=201)
    def create(body:CreateObject,principal=Auth):
        return object_service.create(principal,body,body.object,body.policy,body.parameters)

    @app.get(PREFIX+"/objects/{object_id}",response_model=ObservedObject)
    def object_view(object_id:str,principal=Auth):
        return query(principal,object_id).objects[object_id]

    @app.patch(PREFIX+"/objects/{object_id}/state",response_model=ObservedObject)
    def change_state(object_id:str,body:StateCommand,principal=Auth):
        query(principal,object_id)
        return object_service.set_state(principal,body,object_id,body.state)

    @app.post(PREFIX+"/events",response_model=Event,status_code=201)
    def event(body:EventCommand,principal=Auth):
        return event_service.register(principal,body,body.event)

    @app.post(PREFIX+"/events/{event_id}/revisions",response_model=Event)
    def revision(event_id:str,body:EventCommand,principal=Auth):
        if event_id != body.event.event_id:
            raise StrategyError("EVENT_PATH_MISMATCH")
        return event_service.register(principal,body,body.event)

    @app.post(PREFIX+"/events/{event_id}/scores",response_model=AdmissionReceipt)
    def score(event_id:str,body:ScoreCommand,principal=Auth):
        if event_id != body.score.event_id:
            raise StrategyError("EVENT_PATH_MISMATCH")
        return score_service.submit(principal,body,body.score)

    @app.post(PREFIX+"/events/{event_id}/scores/{score_id}/revisions",response_model=AdmissionReceipt)
    def score_revision(event_id:str,score_id:str,body:ScoreCommand,principal=Auth):
        if body.score.previous_score_id != score_id or body.score.event_id != event_id or body.score.revision_kind != "REVISION":
            raise StrategyError("SCORE_PATH_MISMATCH")
        return score_service.submit(principal,body,body.score)

    @app.get(PREFIX+"/objects/{object_id}/pool",response_model=PoolView)
    def pool_view(object_id:str,principal=Auth):
        return pool(query(principal,object_id),object_id)

    @app.get(PREFIX+"/objects/{object_id}/decisions",response_model=list[DecisionView])
    def decisions(object_id:str,principal=Auth):
        return [d for d in query(principal,object_id).decisions.values() if d.object_id == object_id]

    @app.get(PREFIX+"/objects/{object_id}/targets",response_model=list[TargetOutbox])
    def targets(object_id:str,principal=Auth):
        return [d for d in query(principal,object_id).outbox.values() if d.decision.object_id == object_id]

    @app.get(PREFIX+"/objects/{object_id}/cases",response_model=list[CaseRecord])
    def cases(object_id:str,principal=Auth):
        return [c for c in query(principal,object_id).cases.values() if c.object_id == object_id]

    @app.post(PREFIX+"/cases/{case_id}/attribution-candidates",response_model=AttributionView)
    def attribution(case_id:str,body:AttributionCommand,principal=Auth):
        return case_service.candidate(principal,body,case_id,body.candidate.model_dump(mode="json"))

    @app.get(PREFIX+"/parameters",response_model=list[ParameterSnapshot])
    def parameters(principal=Auth):
        return list(query(principal).parameters.values())

    @app.get(PREFIX+"/learning-decisions",response_model=list[LearningDecisionView])
    def learning(principal=Auth):
        return query(principal).learning_decisions

    @app.get(PREFIX+"/validation-runs",response_model=list[ValidationRunView])
    def validation(principal=Auth):
        return list(query(principal).validation_runs.values())

    if internal and cycle:
        @app.post(PREFIX+"/clock",response_model=list[DecisionView])
        def advance(body:AdvanceClock,principal=Auth):
            if store.environment != "SIM" or not hasattr(clock,"at") or body.at < clock.now():
                raise StrategyError("REPLAY_CLOCK_UNAVAILABLE_OR_REWIND",403)
            clock.at = body.at
            result = cycle.tick(principal)
            cycle.dispatch(principal)
            return result
    return app
