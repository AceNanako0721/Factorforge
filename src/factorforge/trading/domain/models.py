"""Wire-safe models. Money is Decimal internally and JSON strings externally."""
from datetime import datetime, timezone
from decimal import Decimal
from typing import Annotated, Literal

from pydantic import BaseModel, BeforeValidator, ConfigDict, Field, model_validator


def decimal_value(value):
    if not isinstance(value, (str, Decimal)):
        raise ValueError("decimal values must be strings")
    number = Decimal(value)
    if not number.is_finite():
        raise ValueError("decimal values must be finite")
    return number


def utc_value(value):
    if isinstance(value, str):
        value = datetime.fromisoformat(value.replace("Z", "+00:00"))
    if not isinstance(value, datetime) or value.tzinfo is None:
        raise ValueError("timestamps must include a timezone")
    return value.astimezone(timezone.utc)


D = Annotated[Decimal, BeforeValidator(decimal_value, json_schema_input_type=str)]
UTC = Annotated[datetime, BeforeValidator(utc_value)]
ID = Annotated[str, Field(min_length=1, max_length=128, pattern=r"^[A-Za-z0-9_.-]+$")]
ZERO = Decimal("0")
TERMINAL = {"FILLED", "CANCELED", "REJECTED"}


class Model(BaseModel):
    model_config = ConfigDict(extra="forbid", validate_assignment=True)


class InstrumentKey(Model):
    venue: ID
    product: Literal["LINEAR_PERPETUAL"]
    instrument_id: ID

    def code(self):
        return f"{self.venue}:{self.product}:{self.instrument_id}"


class RunKey(Model):
    environment: Literal["SIM", "LIVE"]
    account_id: ID
    run_id: ID


class InstrumentSpec(Model):
    key: InstrumentKey
    version: ID
    valid_from: UTC
    price_tick: D = Field(gt=0)
    quantity_step: D = Field(gt=0)
    contract_multiplier: D = Field(gt=0)
    quote_currency: ID
    settlement_currency: ID
    min_notional: D = Field(ge=0)
    capabilities: set[str]
    price_roles: set[str]


class MarketPoint(Model):
    instrument_key: InstrumentKey
    source_id: ID
    kind: Literal["BID", "ASK", "LAST", "MARK", "INDEX", "REFERENCE", "FX"]
    observed_at: UTC
    received_at: UTC
    available_at: UTC
    value: D = Field(gt=0)
    currency: ID
    quality: Literal["VALID", "STALE", "MISSING", "CONFLICT"]
    spec_version: ID

    @model_validator(mode="after")
    def causal(self):
        if self.available_at < self.observed_at or self.available_at < self.received_at:
            raise ValueError("availability cannot precede observation or receipt")
        return self


class Candle(Model):
    instrument_key: InstrumentKey
    interval: ID
    open_at: UTC
    close_at: UTC
    available_at: UTC
    open: D = Field(gt=0)
    high: D = Field(gt=0)
    low: D = Field(gt=0)
    close: D = Field(gt=0)
    volume: D = Field(ge=0)
    source_id: ID
    final: bool
    revision: int = Field(ge=0)

    @model_validator(mode="after")
    def valid_bar(self):
        if self.high < max(self.open, self.close, self.low) or self.low > min(self.open, self.close):
            raise ValueError("invalid OHLC bounds")
        if self.close_at <= self.open_at or (self.final and self.available_at < self.close_at):
            raise ValueError("invalid candle timing")
        return self


class LossGate(Model):
    mode: Literal["OBSERVE", "ENFORCE"]
    amount: D | None = Field(default=None, gt=0)
    fraction: D | None = Field(default=None, gt=0, le=1)
    count: int | None = Field(default=None, gt=0)

    @model_validator(mode="after")
    def threshold_required(self):
        if self.amount is None and self.fraction is None and self.count is None:
            raise ValueError("a protection threshold is required")
        return self


class AccountPolicy(Model):
    version: ID
    valid_from: UTC
    risk_day_zone: str
    notional_limit: D = Field(gt=0)
    margin_limit: D = Field(gt=0)
    trade_loss_limit: D = Field(gt=0)
    daily_loss: LossGate
    drawdown: LossGate
    consecutive_loss: LossGate
    breach_action: Literal["KEEP_PROTECTION", "ORDERLY_REDUCE", "EXIT_WHEN_TRADABLE"]
    recovery_policy: Literal["MANUAL_RECONCILE"]
    max_market_age_seconds: int = Field(gt=0)

    @model_validator(mode="after")
    def usable_gates(self):
        from zoneinfo import ZoneInfo, ZoneInfoNotFoundError
        try:
            ZoneInfo(self.risk_day_zone)
        except ZoneInfoNotFoundError:
            raise ValueError("unknown risk-day timezone") from None
        if any(g.count is not None or (g.amount is None and g.fraction is None) for g in (self.daily_loss, self.drawdown)):
            raise ValueError("loss/drawdown require amount or fraction thresholds")
        if self.consecutive_loss.count is None or self.consecutive_loss.amount is not None or self.consecutive_loss.fraction is not None:
            raise ValueError("consecutive loss requires a count threshold")
        return self


class SimConfig(Model):
    seed: int
    fee_rate: D = Field(ge=0, lt=1)
    slippage_bps: D = Field(ge=0, lt=10000)
    participation_rate: D = Field(gt=0, le=1)
    latency_seconds: int = Field(ge=0)
    maintenance_margin_rate: D = Field(gt=0, lt=1)
    ohlc_rule: Literal["CONSERVATIVE", "REJECT_AMBIGUOUS"]


class ProtectionPlan(Model):
    trigger_kind: Literal["MARK", "LAST"]
    trigger_price: D = Field(gt=0)
    covered_quantity: D = Field(gt=0)
    exit_order_type: Literal["MARKET"]
    max_slippage_bps: D = Field(ge=0, lt=10000)
    spec_version: ID


class OrderRequest(Model):
    owner_id: ID
    instrument_key: InstrumentKey
    side: Literal["BUY", "SELL"]
    order_type: Literal["MARKET", "LIMIT"]
    quantity: D = Field(gt=0)
    limit_price: D | None = Field(default=None, gt=0)
    time_in_force: Literal["GTC", "IOC", "POST_ONLY"] = "GTC"
    reduce_only: bool = False
    position_side: Literal["BOTH"] = "BOTH"
    spec_version: ID
    protection_plan: ProtectionPlan | None = None

    @model_validator(mode="after")
    def limit_required(self):
        if self.order_type == "LIMIT" and self.limit_price is None:
            raise ValueError("limit_price is required")
        if self.order_type == "MARKET" and self.limit_price is not None:
            raise ValueError("market orders cannot have a limit price")
        return self


class Command(Model):
    schema_version: Literal["trading-2.0"]
    request_id: ID
    idempotency_key: ID
    run_key: RunKey
    expected_version: int = Field(ge=0)
    reason: str = Field(min_length=1, max_length=500)
    expires_at_utc: UTC


class Principal(Model):
    principal_id: ID
    environment: Literal["SIM", "LIVE"]
    account_id: ID
    permissions: set[str]


class Order(Model):
    order_id: ID
    client_order_id: ID
    external_order_id: str | None = None
    request: OrderRequest
    state: str
    filled_quantity: D = ZERO
    average_fill_price: D | None = None
    reserved_notional: D = ZERO
    created_at: UTC
    last_fill_at: UTC | None = None
    target_version: int | None = None

    @property
    def remaining(self):
        return max(ZERO, self.request.quantity - self.filled_quantity)


class Fill(Model):
    external_fill_id: ID
    external_order_id: ID
    instrument_key: InstrumentKey
    side: Literal["BUY", "SELL"]
    quantity: D = Field(gt=0)
    price: D = Field(gt=0)
    fee: D = Field(ge=0)
    fee_currency: ID
    happened_at: UTC
    received_at: UTC


class Position(Model):
    instrument_key: InstrumentKey
    owner_id: ID
    quantity: D = ZERO
    average_entry: D | None = None
    cycle_net: D = ZERO
    protection_state: str = "CLOSED"


class Protection(Model):
    protection_id: ID
    instrument_key: InstrumentKey
    owner_id: ID
    plan: ProtectionPlan
    state: str
    verified_at: UTC | None = None


class Income(Model):
    external_id: ID
    kind: Literal["FUNDING", "TRANSFER", "CORPORATE_ACTION"]
    amount: D
    currency: ID
    happened_at: UTC


class OutboxItem(Model):
    command_id: ID
    kind: Literal["SUBMIT", "CANCEL"]
    order_id: ID
    principal_id: ID
    expires_at: UTC
    executor_epoch: int
    state: Literal["PENDING", "DISPATCHING", "DONE", "UNKNOWN"] = "PENDING"


class Target(Model):
    owner_id: ID
    instrument_key: InstrumentKey
    target_version: int
    target_quantity: D
    owner_epoch: int
    state: str


class Aggregate(Model):
    run_key: RunKey
    execution_mode: Literal["REPLAY", "SHADOW"]
    version: int = 0
    state: str = "NORMAL"
    policy: AccountPolicy
    sim_config: SimConfig
    initial_cash: D
    currency: ID
    cash: D
    clock: UTC
    risk_day: str
    day_start_equity: D
    day_external_flow: D = ZERO
    peak_equity: D
    consecutive_losses: int = 0
    risk_locks: list[str] = Field(default_factory=list)
    would_trigger: list[str] = Field(default_factory=list)
    recovery_issues: list[str] = Field(default_factory=list)
    executor_epoch: int = 1
    specs: dict[str, InstrumentSpec] = Field(default_factory=dict)
    points: dict[str, MarketPoint] = Field(default_factory=dict)
    candles: list[Candle] = Field(default_factory=list)
    positions: dict[str, Position] = Field(default_factory=dict)
    orders: dict[str, Order] = Field(default_factory=dict)
    protections: dict[str, Protection] = Field(default_factory=dict)
    fills: dict[str, Fill] = Field(default_factory=dict)
    incomes: dict[str, Income] = Field(default_factory=dict)
    owners: dict[str, str] = Field(default_factory=dict)
    owner_epochs: dict[str, int] = Field(default_factory=dict)
    targets: dict[str, Target] = Field(default_factory=dict)
    outbox: list[OutboxItem] = Field(default_factory=list)
    dedup: dict[str, dict] = Field(default_factory=dict)
    audit: list[dict] = Field(default_factory=list)
