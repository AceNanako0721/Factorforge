from typing import Literal
from pydantic import Field
from factorforge.strategy.domain.models import (Model, Command, ObservedObject, Policy, ParameterSnapshot, Event,
    ScoreSubmission, ID, UTC, Fraction)


class CreateObject(Command):
    object: ObservedObject
    policy: Policy
    parameters: ParameterSnapshot


class EventCommand(Command):
    event: Event


class ScoreCommand(Command):
    score: ScoreSubmission


class StateCommand(Command):
    state: Literal["ACTIVE","PAUSED","ARCHIVED"]


class AttributionCandidate(Model):
    candidate_id: ID
    category: Literal["DATA_EXECUTION","EXTERNAL_ANOMALY","DEDUP_PREPRICING","DIRECTION_INTENSITY","LIFECYCLE_FULFILLMENT","TIMING","STOP"]
    evidence_refs: list[ID]
    alternative_causes: list[ID]
    producer_version: ID
    confidence: Fraction


class AttributionCommand(Command):
    candidate: AttributionCandidate


class AdvanceClock(Model):
    at: UTC


class Problem(Model):
    code: str
    message: str
    field: str | None = None
    correlation_id: str | None = None
    retryable: bool = False


class AttributionView(AttributionCandidate):
    case_id: ID
    state: Literal["UNVERIFIED"]


class LearningDecisionView(Model):
    object_id: ID
    parameter: ID
    at: UTC
    reason: str | None
    error: str
    neff: str
    groups: int


class ValidationRunView(Model):
    manifest: dict
    result: dict
    state: Literal["RECORDED","RUNNING","COMPLETED","FAILED"]
    production_upgrade: Literal[False]
