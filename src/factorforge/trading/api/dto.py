"""Strict HTTP requests; no untyped business dictionaries at the boundary."""
from typing import Literal

from pydantic import Field

from factorforge.trading.domain.models import (
    Model, AccountPolicy, SimConfig, RunKey, ID, UTC, D, Command, OrderRequest,
    InstrumentSpec, InstrumentKey, MarketPoint, MarketTrade, Candle, ProtectionPlan, Income, ExternalFact, FxRate,
)


class CreateRun(Model):
    schema_version: Literal["trading-2.0"]
    request_id: ID
    idempotency_key: ID
    run_key: RunKey
    reason: str = Field(min_length=1, max_length=500)
    expires_at_utc: UTC
    execution_mode: Literal["REPLAY", "SHADOW"]
    initial_cash: D = Field(gt=0)
    currency: ID
    clock: UTC
    account_policy: AccountPolicy
    sim_config: SimConfig


class SubmitOrder(Command):
    order: OrderRequest


class RegisterSpec(Command):
    spec: InstrumentSpec


class SetProtection(Command):
    instrument_key: InstrumentKey
    plan: ProtectionPlan


class RecordIncome(Command):
    income: Income


class ReplayFrame(Model):
    at: UTC
    instrument_key: InstrumentKey
    points: list[MarketPoint] = Field(min_length=1)
    liquidity: D = Field(ge=0)
    candle: Candle | None = None


class AdvanceReplay(Command):
    frame: ReplayFrame


class TargetRequest(Command):
    owner_id: ID
    instrument_key: InstrumentKey
    target_version: int = Field(gt=0)
    target_quantity: D
    policy_version: ID
    spec_version: ID
    source_decision_id: ID
    protection_plan: ProtectionPlan | None = None
    owner_epoch: int = Field(ge=0)


class ImportExternal(Command):
    fact: ExternalFact


class RegisterFx(Command):
    rate: FxRate


class ResolveExternal(Command):
    instrument_key: InstrumentKey
    owner_id: ID
    owner_epoch: int = Field(ge=0)
    evidence_ref: str = Field(min_length=1)


class FenceExecutor(Command):
    epoch: int = Field(gt=0)
    evidence_ref: str = Field(min_length=1)


class MarketSnapshot(Command):
    at: UTC
    points: list[MarketPoint] = Field(min_length=1)
    candles: list[Candle] = Field(default_factory=list)
    trades: list[MarketTrade] = Field(default_factory=list)
