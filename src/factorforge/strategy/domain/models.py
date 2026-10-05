"""Versioned framework records. Unknown safety inputs have no optimistic defaults."""
from datetime import datetime, timezone
from decimal import Decimal
from enum import Enum
from typing import Annotated, Literal

from pydantic import BaseModel, BeforeValidator, ConfigDict, Field, PlainSerializer, model_validator,field_serializer


def decimal(value):
    if not isinstance(value,(str,Decimal)):
        raise ValueError("decimal strings required")
    result = Decimal(value)
    if not result.is_finite():
        raise ValueError("finite decimal required")
    return result


def utc(value):
    if isinstance(value, str):
        value = datetime.fromisoformat(value.replace("Z", "+00:00"))
    if not isinstance(value, datetime) or value.tzinfo is None or value.utcoffset().total_seconds() != 0:
        raise ValueError("UTC timestamp required")
    return value.astimezone(timezone.utc)


D = Annotated[Decimal, BeforeValidator(decimal,json_schema_input_type=str), PlainSerializer(str, return_type=str)]
UTC = Annotated[datetime, BeforeValidator(utc)]
Fraction = Annotated[D, Field(ge=0, le=1)]
ID = Annotated[str, Field(min_length=1, max_length=160)]
TradingID = Annotated[str,Field(min_length=1,max_length=128,pattern=r"^[A-Za-z0-9_.-]+$")]
ZERO = Decimal(0)
ONE = Decimal(1)


class Model(BaseModel):
    model_config = ConfigDict(extra="forbid", validate_assignment=True)

    @field_serializer("scopes","capabilities","object_ids","fill_ids","income_ids","consumed_evidence","source_decisions","calibrations","rubrics","verification_manifests",check_fields=False)
    def ordered_sets(self,value):
        return sorted(value)


class StrategyError(Exception):
    def __init__(self, code, status=422):
        self.code, self.status = code, status
        super().__init__(code)


class ApiScope(str, Enum):
    QUERY = "query"
    RESEARCH = "score:research"
    OBJECT = "object:write"


class WorkloadCapability(str, Enum):
    SIM = "signal:sim"
    LIVE = "signal:live"


class PublicPrincipal(Model):
    principal_id: ID
    instance_id: ID
    environment: Literal["SIM", "LIVE"]
    scopes: set[ApiScope]


class WorkloadIdentity(Model):
    workload_id: ID
    instance_id: ID
    environment: Literal["SIM", "LIVE"]
    object_ids: set[ID]
    capabilities: set[WorkloadCapability]


class RunBinding(Model):
    environment: Literal["SIM", "LIVE"]
    account_id: TradingID
    run_id: TradingID


class InstrumentBinding(Model):
    venue: TradingID
    product: Literal["LINEAR_PERPETUAL"]
    instrument_id: TradingID


class Command(Model):
    schema_version: Literal["strategy-2.0"]
    request_id: ID
    idempotency_key: ID
    expected_version: int = Field(ge=0)
    reason: str = Field(min_length=1, max_length=500)


class ParameterSnapshot(Model):
    version: ID
    parent_version: ID | None = None
    scope: ID
    valid_from: UTC
    values: dict[str, D]
    bounds: dict[str, tuple[D, D]]
    steps: dict[str, D]
    evidence_gates: dict[str, D]
    source_manifest: ID
    applicability: list[ID]
    quality_state: Literal["VALIDATED", "REQUIRED_UNSET", "EXPERIMENT_ONLY"]

    @model_validator(mode="after")
    def registered_bounds(self):
        if any(low > high or key in self.values and not low <= self.values[key] <= high for key,(low,high) in self.bounds.items()):
            raise ValueError("parameter outside registered bounds")
        if any(step <= 0 for step in self.steps.values()):
            raise ValueError("fixed steps must be positive")
        return self


class Level(Model):
    exposure: Fraction
    enter: D = Field(gt=0)
    exit: D = Field(ge=0)

    @model_validator(mode="after")
    def band(self):
        if self.exit >= self.enter:
            raise ValueError("exit must be below enter")
        return self


class Policy(Model):
    """All numerical safety/calibration choices must be supplied with provenance."""
    version: ID
    quality_state: Literal["VALIDATED", "REQUIRED_UNSET", "EXPERIMENT_ONLY"]
    source_manifest: ID
    calibrations: set[ID]
    rubrics: set[ID]
    claim_weights: dict[ID,D]
    verification_manifests: set[ID]
    levels: list[Level] = Field(min_length=1)
    cycle_seconds: int = Field(gt=0)
    max_price_age_seconds: int = Field(gt=0)
    target_ttl_seconds: int = Field(gt=0)
    event_cap: D = Field(gt=0)
    family_cap: D = Field(gt=0)
    object_cap: D = Field(gt=0)
    cleanup_threshold: D = Field(ge=0)
    event_ttl_seconds: int = Field(gt=0)
    numerical_tolerance: D = Field(gt=0)
    epsilon: D = Field(gt=0)
    sigma_ref: D = Field(gt=0)
    liquidity_budget: D = Field(ge=0)
    object_loss_budget: D = Field(gt=0)
    portfolio_gross_limit: D = Field(gt=0)
    portfolio_net_limit: D = Field(gt=0)
    portfolio_stress_limit: D = Field(gt=0)
    group_limits: dict[ID, D]
    micro_distance: D = Field(gt=0)
    max_stop_fraction: D = Field(gt=0)
    fee_rate: D = Field(ge=0)
    slippage_fraction: D = Field(ge=0)
    gap_fraction: D = Field(ge=0)
    noise_window: int = Field(ge=2)
    noise_quantile: Fraction
    max_bar_gap_seconds: int = Field(gt=0)
    max_noise_fraction: D = Field(gt=0)
    cooldown_seconds: int = Field(ge=0)
    min_adjustment: D = Field(ge=0)
    window_seconds: int = Field(gt=0)
    window_anchor: UTC
    max_new_risk: int = Field(ge=0)
    max_loss_cases: int = Field(ge=0)
    observation_multiplier: D = Field(gt=0)
    observation_min_seconds: int = Field(gt=0)
    observation_max_seconds: int = Field(gt=0)
    label_seconds: int = Field(gt=0)
    label_cost_band: D = Field(ge=0)
    label_noise_band: D = Field(ge=0)
    gamma: D = Field(gt=0)
    lifecycle_fraction: D = Field(gt=0, lt=1)
    regime_confirmations: int = Field(gt=0)
    regime_dwell_seconds: int = Field(ge=0)
    regime_enter: D = Field(gt=0)
    regime_exit: D = Field(ge=0)
    unknown_regime_multiplier: Fraction
    beta: D
    absolute_price_proxy_validated: bool
    half_life_bounds: tuple[D, D]
    event_half_life_approved: bool

    @model_validator(mode="after")
    def ordered(self):
        for before, after in zip(self.levels, self.levels[1:]):
            if after.enter <= before.enter or after.exit <= before.exit or after.exposure <= before.exposure:
                raise ValueError("levels must increase")
        if self.regime_exit >= self.regime_enter or self.observation_min_seconds > self.observation_max_seconds:
            raise ValueError("invalid registered bands")
        if not 0 < self.half_life_bounds[0] <= self.half_life_bounds[1]:
            raise ValueError("invalid half life bounds")
        return self


class ObservedObject(Model):
    object_id: ID
    instance_id: ID
    environment: Literal["SIM", "LIVE"]
    trading_run_key: RunBinding
    instrument_key: InstrumentBinding
    owner_id: TradingID
    state: Literal["ACTIVE", "PAUSED", "ARCHIVED"] = "ACTIVE"
    parameter_version: ID
    price_proxy_binding: ID
    regime_binding: ID
    time_policy_version: ID
    aggregate_version: int = 0
    owner_epoch: int = 0
    level: int = 0
    direction: int = 0
    last_cycle: UTC | None = None
    last_cutoff: UTC | None = None
    last_adjustment: UTC | None = None
    recovery_state: str = "RECOVERY_CHECK"
    actual_quantity: D = ZERO
    pending_quantity: D = ZERO


class EvidenceRef(Model):
    evidence_id: ID
    content_hash: str = Field(pattern=r"^[a-f0-9]{64}$")
    source_id: ID
    licence_ref: ID
    first_public_at: UTC
    received_at: UTC
    available_at: UTC
    span_refs: list[ID] = Field(min_length=1)
    verification_ref: ID

    @model_validator(mode="after")
    def times(self):
        if self.available_at < max(self.first_public_at, self.received_at):
            raise ValueError("evidence available before receipt/publication")
        return self


class Claim(Model):
    claim_id: ID
    normalized_fact: ID
    subject_id: ID
    economic_item: ID
    period: ID
    fact_time: UTC
    numbers_with_units: dict[str, str]
    evidence_refs: list[ID] = Field(min_length=1)
    supersedes_claim_id: ID | None = None
    verified_at: UTC
    weight: D = Field(gt=0)
    verification_manifest: ID


class Event(Model):
    event_id: ID
    family_id: ID
    fact_version: int = Field(gt=0)
    parent_event_id: ID | None = None
    relation: Literal["NEW", "CONFIRMATION", "NEW_FACT", "CORRECTION", "RETRACTION"]
    subject_id: ID
    event_type: ID
    occurred_at: UTC
    first_public_at: UTC
    evidence_refs: list[EvidenceRef]
    claims: list[Claim]
    object_ids: set[ID]
    state: Literal["VERIFIED", "QUARANTINED", "RETRACTED"] = "QUARANTINED"
    novelty: Fraction = ZERO


class ScoreVector(Model):
    direction: Literal[-1, 0, 1]
    impact_points: D = Field(ge=0)
    credibility: Fraction | None = None
    relevance: Fraction | None = None
    novelty: Fraction | None = None
    expectation_coverage: Fraction | None = None
    prepricing_fraction: Fraction | None = None
    expected_half_life: D | None = Field(default=None, gt=0)
    quality_score: Fraction | None = None
    unknown_fields: list[str] = Field(default_factory=list)


class ScoreSubmission(Model):
    submission_id: ID
    event_id: ID
    fact_version: int = Field(gt=0)
    object_id: ID
    score_version: int = Field(gt=0)
    previous_score_id: ID | None = None
    revision_kind: Literal["INITIAL", "REVISION"]
    vector: ScoreVector
    evidence_refs: list[ID]
    producer_id: ID
    producer_version: ID
    rubric_version: ID
    calibration_version: ID
    completed_at: UTC
    input_manifest_hash: str = Field(pattern=r"^[a-f0-9]{64}$")


class AdmissionReceipt(Model):
    submission_id: ID
    state: Literal["DRAFT", "RESEARCH_ONLY", "QUARANTINED", "READY", "READY_PENDING_PRICE", "APPLIED", "SUPERSEDED"]
    contribution_id: ID | None = None
    eligible_from: UTC | None = None
    reason_codes: list[str]
    current_version: int


class Contribution(Model):
    contribution_id: ID
    object_id: ID
    event_id: ID
    family_id: ID
    direction: Literal[-1, 0, 1]
    initial_amount: D = Field(ge=0)
    remaining_amount: D = Field(ge=0)
    effective_at: UTC
    last_updated_at: UTC
    half_life: D = Field(gt=0)
    eta: D = Field(gt=0)
    high_water: D = Field(ge=0)
    reference_price: D = Field(gt=0)
    reference_benchmark: D | None = None
    frozen_parameter_version: ID
    score_id: ID
    quality: Fraction
    price_consumed: D = ZERO
    state: Literal["ACTIVE", "INVALID", "EXPIRED"] = "ACTIVE"
    fact_weights: dict[str,D] = Field(default_factory=dict)


class LedgerEntry(Model):
    sequence: int
    contribution_id: ID
    at: UTC
    start: D
    injection: D = ZERO
    revision_delta: D = ZERO
    time_consumption: D = ZERO
    price_consumption: D = ZERO
    invalidation: D = ZERO
    rejected_amount: D = ZERO
    end: D
    reason: ID
    budget: D = ZERO
    delta_high_water: D = ZERO


class PoolView(Model):
    object_id: ID
    plus: D
    minus: D
    net: D
    contributions: list[Contribution]
    ledger_version: int
    quality: Fraction


class StopPlan(Model):
    trigger_kind: Literal["MARK", "LAST"]
    trigger_price: D = Field(gt=0)
    covered_quantity: D = Field(gt=0)
    exit_order_type: Literal["MARKET"] = "MARKET"
    max_slippage_bps: D = Field(ge=0)
    spec_version: ID


class DecisionView(Model):
    decision_id: ID
    object_id: ID
    cycle_id: ID
    available_cutoff: UTC
    pool_plus: D
    pool_minus: D
    pool_net: D
    previous_level: int
    new_level: int
    raw_exposure: D
    projected_exposure: D
    target_quantity: D
    actual_quantity: D
    pending_quantity: D
    stop_plan: StopPlan | None
    parameter_version: ID
    input_hash: ID
    reason_codes: list[str]
    source_event_versions: dict[str,int] = Field(default_factory=dict)
    input_snapshot: dict = Field(default_factory=dict)


class MarketSample(Model):
    available_at: UTC
    price: D = Field(gt=0)
    benchmark: D | None = Field(default=None, gt=0)
    sigma: D | None = Field(default=None, gt=0)
    liquidity: D | None = Field(default=None, ge=0)
    quality: Literal["VALID", "UNKNOWN", "STALE"]


class Bar(Model):
    close_at: UTC
    available_at: UTC
    high: D = Field(gt=0)
    low: D = Field(gt=0)
    close: D = Field(gt=0)
    final: bool
    quality: Literal["VALID", "UNKNOWN", "CORPORATE_ACTION", "ANOMALY"]


class TradingSnapshot(Model):
    run_key: RunBinding
    version: int
    at: UTC
    equity: D
    policy_version: ID
    risk_locks: list[str]
    run_state: str
    actual: dict[str, D]
    pending: dict[str, D]
    owner_epochs: dict[str, int]
    target_versions: dict[str, int]
    source_decisions: set[str]
    specs: dict[str, dict]
    samples: dict[str, MarketSample]
    bars: dict[str, list[Bar]]
    fills: list[dict]
    incomes: list[dict]
    external_change: bool
    protections: dict[str, list[dict]] = Field(default_factory=dict)
    other_exposures: list[tuple[D,D,str]] = Field(default_factory=list)
    average_entries: dict[str,D | None] = Field(default_factory=dict)


class TargetOutbox(Model):
    decision: DecisionView
    target_version: int
    expires_at: UTC
    reservation_id: ID | None
    state: Literal["PENDING", "DELIVERY_UNKNOWN", "ACK", "REJECTED", "EXPIRED"]
    owner_epoch: int
    accepted_at: UTC | None = None
    reason: str | None = None
    command_payload: dict | None = None


class Reservation(Model):
    reservation_id: ID
    object_id: ID
    window_id: ID
    state: Literal["RESERVED", "ACCEPTED", "UNKNOWN", "RELEASED"]


class CaseRecord(Model):
    case_id: ID
    object_id: ID
    direction: Literal[-1, 1]
    risk_lots: list[dict]
    event_groups: list[ID]
    entry_snapshot: dict
    entry_at: UTC
    entry_window: ID
    observation_seconds: int
    exit_at: UTC | None = None
    observation_end: UTC | None = None
    pnl_components: dict[str, D]
    status: Literal["OPEN", "OBSERVING", "MATURE", "CENSORED"]
    label_status: Literal["IMMATURE", "UNKNOWN", "NEUTRAL", "CORRECT", "WRONG"]
    labels: dict[str, D | None] = Field(default_factory=dict)
    attribution_ids: list[ID] = Field(default_factory=list)
    parameter_decision_ids: list[ID] = Field(default_factory=list)
    fill_ids: set[ID] = Field(default_factory=set)
    income_ids: set[ID] = Field(default_factory=set)
    loss_counted: bool = False


class StrategyState(Model):
    instance_id: ID
    environment: Literal["SIM", "LIVE"]
    version: int = 0
    objects: dict[str, ObservedObject] = Field(default_factory=dict)
    policies: dict[str, Policy] = Field(default_factory=dict)
    parameters: dict[str, ParameterSnapshot] = Field(default_factory=dict)
    events: dict[str, Event] = Field(default_factory=dict)
    event_versions: dict[str, Event] = Field(default_factory=dict)
    event_received: dict[str,UTC] = Field(default_factory=dict)
    scores: dict[str, ScoreSubmission] = Field(default_factory=dict)
    receipts: dict[str, AdmissionReceipt] = Field(default_factory=dict)
    research_receipts: dict[str, AdmissionReceipt] = Field(default_factory=dict)
    prepricing_assessments: dict[str, dict] = Field(default_factory=dict)
    received: dict[str, UTC] = Field(default_factory=dict)
    contributions: dict[str, Contribution] = Field(default_factory=dict)
    ledger: list[LedgerEntry] = Field(default_factory=list)
    samples: dict[str, list[MarketSample]] = Field(default_factory=dict)
    decisions: dict[str, DecisionView] = Field(default_factory=dict)
    outbox: dict[str, TargetOutbox] = Field(default_factory=dict)
    reservations: dict[str, Reservation] = Field(default_factory=dict)
    loss_cases: dict[str, set[str]] = Field(default_factory=dict)
    cases: dict[str, CaseRecord] = Field(default_factory=dict)
    regimes: dict[str, dict] = Field(default_factory=dict)
    attributions: dict[str, dict] = Field(default_factory=dict)
    candidates: dict[str, dict] = Field(default_factory=dict)
    consumed_evidence: set[str] = Field(default_factory=set)
    learning_decisions: list[dict] = Field(default_factory=list)
    validation_runs: dict[str, dict] = Field(default_factory=dict)
    counterfactuals: dict[str,dict] = Field(default_factory=dict)
    factor_manifests: dict[str, dict] = Field(default_factory=dict)
    skipped_cycles: list[dict] = Field(default_factory=list)
    audit: list[dict] = Field(default_factory=list)
    dedup: dict[str, dict] = Field(default_factory=dict)
