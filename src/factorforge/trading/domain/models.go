// Persisted trading types retain the v2.1.0 JSON field names and nullable values.
// HTTP presence, defaults and validation are handled separately at admission.
package domain

import (
	"encoding/json"
	"github.com/AceNanako0721/Factorforge/src/factorforge/trading/domain/decimal"
	"time"
)

type Object = map[string]json.RawMessage

type InstrumentKey struct {
	Venue        string `json:"venue"`
	Product      string `json:"product"`
	InstrumentID string `json:"instrument_id"`
}

type RunKey struct {
	Environment string `json:"environment"`
	AccountID   string `json:"account_id"`
	RunID       string `json:"run_id"`
}

type Session struct {
	OpensAt  time.Time `json:"opens_at"`
	ClosesAt time.Time `json:"closes_at"`
}

type FxRate struct {
	Currency    string        `json:"currency"`
	Rate        decimal.Value `json:"rate"`
	ObservedAt  time.Time     `json:"observed_at"`
	AvailableAt time.Time     `json:"available_at"`
	SourceID    string        `json:"source_id"`
}

type StressScenario struct {
	ScenarioID   string                 `json:"scenario_id"`
	Shocks       Ordered[decimal.Value] `json:"shocks"`
	LossLimit    decimal.Value          `json:"loss_limit"`
	ExitCostRate decimal.Value          `json:"exit_cost_rate"`
}

type OperationalPolicy struct {
	MinDiskBytes         int64         `json:"min_disk_bytes"`
	MaxClockSkewSeconds  decimal.Value `json:"max_clock_skew_seconds"`
	MaxAuditRecords      int64         `json:"max_audit_records"`
	MaxPendingCommands   int64         `json:"max_pending_commands"`
	MaxCommandAgeSeconds int64         `json:"max_command_age_seconds"`
	LeaseSeconds         int64         `json:"lease_seconds"`
}

type ExternalFact struct {
	ExternalID      string          `json:"external_id"`
	Kind            string          `json:"kind"`
	InstrumentKey   InstrumentKey   `json:"instrument_key"`
	HappenedAt      time.Time       `json:"happened_at"`
	ReceivedAt      time.Time       `json:"received_at"`
	BeforeQuantity  decimal.Value   `json:"before_quantity"`
	AfterQuantity   decimal.Value   `json:"after_quantity"`
	AverageEntry    *decimal.Value  `json:"average_entry"`
	CashDelta       decimal.Value   `json:"cash_delta"`
	Currency        string          `json:"currency"`
	EvidenceRef     string          `json:"evidence_ref"`
	RuleVersion     string          `json:"rule_version"`
	ReplacementSpec *InstrumentSpec `json:"replacement_spec"`
	ExternalFillIDs []string        `json:"external_fill_ids"`
}

type InstrumentSpec struct {
	Key                InstrumentKey      `json:"key"`
	Version            string             `json:"version"`
	ValidFrom          time.Time          `json:"valid_from"`
	PriceTick          decimal.Value      `json:"price_tick"`
	QuantityStep       decimal.Value      `json:"quantity_step"`
	ContractMultiplier decimal.Value      `json:"contract_multiplier"`
	QuoteCurrency      string             `json:"quote_currency"`
	SettlementCurrency string             `json:"settlement_currency"`
	MinNotional        decimal.Value      `json:"min_notional"`
	Capabilities       []string           `json:"capabilities"`
	PriceRoles         []string           `json:"price_roles"`
	Sessions           []Session          `json:"sessions"`
	Halted             bool               `json:"halted"`
	RiskGroup          *string            `json:"risk_group"`
	MarginTiers        [][2]decimal.Value `json:"margin_tiers"`
}

type MarketPoint struct {
	InstrumentKey InstrumentKey `json:"instrument_key"`
	SourceID      string        `json:"source_id"`
	Kind          string        `json:"kind"`
	ObservedAt    time.Time     `json:"observed_at"`
	ReceivedAt    time.Time     `json:"received_at"`
	AvailableAt   time.Time     `json:"available_at"`
	Value         decimal.Value `json:"value"`
	Currency      string        `json:"currency"`
	Quality       string        `json:"quality"`
	SpecVersion   string        `json:"spec_version"`
}

type MarketTrade struct {
	ExternalID    string        `json:"external_id"`
	InstrumentKey InstrumentKey `json:"instrument_key"`
	SourceID      string        `json:"source_id"`
	ObservedAt    time.Time     `json:"observed_at"`
	ReceivedAt    time.Time     `json:"received_at"`
	AvailableAt   time.Time     `json:"available_at"`
	Price         decimal.Value `json:"price"`
	Quantity      decimal.Value `json:"quantity"`
	SpecVersion   string        `json:"spec_version"`
}

type Candle struct {
	InstrumentKey InstrumentKey `json:"instrument_key"`
	Interval      string        `json:"interval"`
	OpenAt        time.Time     `json:"open_at"`
	CloseAt       time.Time     `json:"close_at"`
	AvailableAt   time.Time     `json:"available_at"`
	Open          decimal.Value `json:"open"`
	High          decimal.Value `json:"high"`
	Low           decimal.Value `json:"low"`
	Close         decimal.Value `json:"close"`
	Volume        decimal.Value `json:"volume"`
	SourceID      string        `json:"source_id"`
	Final         bool          `json:"final"`
	Revision      int64         `json:"revision"`
}

type LossGate struct {
	Mode     string         `json:"mode"`
	Amount   *decimal.Value `json:"amount"`
	Fraction *decimal.Value `json:"fraction"`
	Count    *int64         `json:"count"`
}

type AccountPolicy struct {
	Version             string                 `json:"version"`
	ValidFrom           time.Time              `json:"valid_from"`
	RiskDayZone         string                 `json:"risk_day_zone"`
	NotionalLimit       decimal.Value          `json:"notional_limit"`
	MarginLimit         decimal.Value          `json:"margin_limit"`
	TradeLossLimit      decimal.Value          `json:"trade_loss_limit"`
	DailyLoss           LossGate               `json:"daily_loss"`
	Drawdown            LossGate               `json:"drawdown"`
	ConsecutiveLoss     LossGate               `json:"consecutive_loss"`
	BreachAction        string                 `json:"breach_action"`
	RecoveryPolicy      string                 `json:"recovery_policy"`
	MaxMarketAgeSeconds int64                  `json:"max_market_age_seconds"`
	NetNotionalLimit    *decimal.Value         `json:"net_notional_limit"`
	GroupNotionalLimits Ordered[decimal.Value] `json:"group_notional_limits"`
	StressScenarios     []StressScenario       `json:"stress_scenarios"`
	Operational         *OperationalPolicy     `json:"operational"`
}

type SimConfig struct {
	Seed                  int64          `json:"seed"`
	FeeRate               decimal.Value  `json:"fee_rate"`
	SlippageBps           decimal.Value  `json:"slippage_bps"`
	ParticipationRate     decimal.Value  `json:"participation_rate"`
	LatencySeconds        int64          `json:"latency_seconds"`
	MaintenanceMarginRate decimal.Value  `json:"maintenance_margin_rate"`
	OHLCRule              string         `json:"ohlc_rule"`
	Leverage              decimal.Value  `json:"leverage"`
	LiquidationFeeRate    *decimal.Value `json:"liquidation_fee_rate"`
	QueueAheadQuantity    decimal.Value  `json:"queue_ahead_quantity"`
}

type ProtectionPlan struct {
	TriggerKind     string        `json:"trigger_kind"`
	TriggerPrice    decimal.Value `json:"trigger_price"`
	CoveredQuantity decimal.Value `json:"covered_quantity"`
	ExitOrderType   string        `json:"exit_order_type"`
	MaxSlippageBps  decimal.Value `json:"max_slippage_bps"`
	SpecVersion     string        `json:"spec_version"`
}

type OrderRequest struct {
	OwnerID        string          `json:"owner_id"`
	InstrumentKey  InstrumentKey   `json:"instrument_key"`
	Side           string          `json:"side"`
	OrderType      string          `json:"order_type"`
	Quantity       decimal.Value   `json:"quantity"`
	LimitPrice     *decimal.Value  `json:"limit_price"`
	TimeInForce    string          `json:"time_in_force"`
	ReduceOnly     bool            `json:"reduce_only"`
	PositionSide   string          `json:"position_side"`
	SpecVersion    string          `json:"spec_version"`
	ProtectionPlan *ProtectionPlan `json:"protection_plan"`
}

type Command struct {
	SchemaVersion   string    `json:"schema_version"`
	RequestID       string    `json:"request_id"`
	IdempotencyKey  string    `json:"idempotency_key"`
	RunKey          RunKey    `json:"run_key"`
	ExpectedVersion int64     `json:"expected_version"`
	Reason          string    `json:"reason"`
	ExpiresAtUTC    time.Time `json:"expires_at_utc"`
}

type Principal struct {
	PrincipalID string   `json:"principal_id"`
	Environment string   `json:"environment"`
	AccountID   string   `json:"account_id"`
	Permissions []string `json:"permissions"`
}

type Order struct {
	OrderID            string         `json:"order_id"`
	ClientOrderID      string         `json:"client_order_id"`
	ExternalOrderID    *string        `json:"external_order_id"`
	Request            OrderRequest   `json:"request"`
	State              string         `json:"state"`
	FilledQuantity     decimal.Value  `json:"filled_quantity"`
	AverageFillPrice   *decimal.Value `json:"average_fill_price"`
	ReservedNotional   decimal.Value  `json:"reserved_notional"`
	CreatedAt          time.Time      `json:"created_at"`
	LastFillAt         *time.Time     `json:"last_fill_at"`
	TargetVersion      *int64         `json:"target_version"`
	SourceProtectionID *string        `json:"source_protection_id"`
}

type Fill struct {
	ExternalFillID  string        `json:"external_fill_id"`
	ExternalOrderID string        `json:"external_order_id"`
	InstrumentKey   InstrumentKey `json:"instrument_key"`
	Side            string        `json:"side"`
	Quantity        decimal.Value `json:"quantity"`
	Price           decimal.Value `json:"price"`
	Fee             decimal.Value `json:"fee"`
	FeeCurrency     string        `json:"fee_currency"`
	HappenedAt      time.Time     `json:"happened_at"`
	ReceivedAt      time.Time     `json:"received_at"`
}

type Position struct {
	InstrumentKey   InstrumentKey  `json:"instrument_key"`
	OwnerID         string         `json:"owner_id"`
	Quantity        decimal.Value  `json:"quantity"`
	AverageEntry    *decimal.Value `json:"average_entry"`
	CycleNet        decimal.Value  `json:"cycle_net"`
	ProtectionState string         `json:"protection_state"`
}

type Protection struct {
	ProtectionID    string         `json:"protection_id"`
	InstrumentKey   InstrumentKey  `json:"instrument_key"`
	OwnerID         string         `json:"owner_id"`
	Plan            ProtectionPlan `json:"plan"`
	State           string         `json:"state"`
	VerifiedAt      *time.Time     `json:"verified_at"`
	ExternalID      *string        `json:"external_id"`
	ReplacesID      *string        `json:"replaces_id"`
	ExitOrderID     *string        `json:"exit_order_id"`
	CancelRequested bool           `json:"cancel_requested"`
}

type Income struct {
	ExternalID    string         `json:"external_id"`
	Kind          string         `json:"kind"`
	Amount        decimal.Value  `json:"amount"`
	Currency      string         `json:"currency"`
	HappenedAt    time.Time      `json:"happened_at"`
	InstrumentKey *InstrumentKey `json:"instrument_key"`
	EvidenceRef   *string        `json:"evidence_ref"`
}

type OutboxItem struct {
	CommandID     string    `json:"command_id"`
	Kind          string    `json:"kind"`
	OrderID       string    `json:"order_id"`
	PrincipalID   string    `json:"principal_id"`
	ExpiresAt     time.Time `json:"expires_at"`
	ExecutorEpoch int64     `json:"executor_epoch"`
	State         string    `json:"state"`
	ClaimedBy     *string   `json:"claimed_by"`
}

type Target struct {
	OwnerID          string          `json:"owner_id"`
	InstrumentKey    InstrumentKey   `json:"instrument_key"`
	TargetVersion    int64           `json:"target_version"`
	TargetQuantity   decimal.Value   `json:"target_quantity"`
	OwnerEpoch       int64           `json:"owner_epoch"`
	State            string          `json:"state"`
	ExpiresAt        *time.Time      `json:"expires_at"`
	PolicyVersion    *string         `json:"policy_version"`
	SpecVersion      *string         `json:"spec_version"`
	ProtectionPlan   *ProtectionPlan `json:"protection_plan"`
	SourceDecisionID *string         `json:"source_decision_id"`
	Reasons          []string        `json:"reasons"`
	Generation       int64           `json:"generation"`
}

type Aggregate struct {
	RunKey                 RunKey                   `json:"run_key"`
	ExecutionMode          string                   `json:"execution_mode"`
	Version                int64                    `json:"version"`
	State                  string                   `json:"state"`
	Policy                 AccountPolicy            `json:"policy"`
	SimConfig              SimConfig                `json:"sim_config"`
	InitialCash            decimal.Value            `json:"initial_cash"`
	Currency               string                   `json:"currency"`
	Cash                   decimal.Value            `json:"cash"`
	Clock                  time.Time                `json:"clock"`
	RiskDay                string                   `json:"risk_day"`
	DayStartEquity         decimal.Value            `json:"day_start_equity"`
	DayExternalFlow        decimal.Value            `json:"day_external_flow"`
	PeakEquity             decimal.Value            `json:"peak_equity"`
	ConsecutiveLosses      int64                    `json:"consecutive_losses"`
	RiskLocks              []string                 `json:"risk_locks"`
	WouldTrigger           []string                 `json:"would_trigger"`
	RecoveryIssues         []string                 `json:"recovery_issues"`
	ExecutorEpoch          int64                    `json:"executor_epoch"`
	Specs                  Ordered[*InstrumentSpec] `json:"specs"`
	Points                 Ordered[*MarketPoint]    `json:"points"`
	Candles                []Candle                 `json:"candles"`
	Positions              Ordered[*Position]       `json:"positions"`
	Orders                 Ordered[*Order]          `json:"orders"`
	Protections            Ordered[*Protection]     `json:"protections"`
	Fills                  Ordered[*Fill]           `json:"fills"`
	Incomes                Ordered[*Income]         `json:"incomes"`
	Owners                 Ordered[string]          `json:"owners"`
	OwnerEpochs            Ordered[int64]           `json:"owner_epochs"`
	Targets                Ordered[*Target]         `json:"targets"`
	Outbox                 []OutboxItem             `json:"outbox"`
	Dedup                  Ordered[Object]          `json:"dedup"`
	Audit                  []Object                 `json:"audit"`
	ExternalFacts          Ordered[*ExternalFact]   `json:"external_facts"`
	FXRates                Ordered[*FxRate]         `json:"fx_rates"`
	ConvertedFees          Ordered[decimal.Value]   `json:"converted_fees"`
	ConvertedIncome        Ordered[decimal.Value]   `json:"converted_income"`
	CashBalances           Ordered[decimal.Value]   `json:"cash_balances"`
	FXRevaluation          decimal.Value            `json:"fx_revaluation"`
	RuleHistory            []InstrumentSpec         `json:"rule_history"`
	QueueRemaining         Ordered[decimal.Value]   `json:"queue_remaining"`
	HealthIssues           []string                 `json:"health_issues"`
	HealthCheckedAt        *time.Time               `json:"health_checked_at"`
	Alerts                 []Object                 `json:"alerts"`
	EmergencyOrders        Ordered[string]          `json:"emergency_orders"`
	LeaseHolder            *string                  `json:"lease_holder"`
	LeaseUntil             *time.Time               `json:"lease_until"`
	LeaseEpoch             int64                    `json:"lease_epoch"`
	IsolatedEpoch          int64                    `json:"isolated_epoch"`
	IsolationEvidence      *string                  `json:"isolation_evidence"`
	RejectedRequests       []Object                 `json:"rejected_requests"`
	MarketTrades           Ordered[*MarketTrade]    `json:"market_trades"`
	FactsStartAt           *time.Time               `json:"facts_start_at"`
	VenueReconciledVersion *int64                   `json:"venue_reconciled_version"`
	VenueFactsCursorAt     *time.Time               `json:"venue_facts_cursor_at"`
}
