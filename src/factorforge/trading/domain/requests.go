package domain

import (
	"github.com/AceNanako0721/Factorforge/src/factorforge/trading/domain/decimal"
	"time"
)

type CreateRun struct {
	SchemaVersion  string        `json:"schema_version"`
	RequestID      string        `json:"request_id"`
	IdempotencyKey string        `json:"idempotency_key"`
	RunKey         RunKey        `json:"run_key"`
	Reason         string        `json:"reason"`
	ExpiresAtUTC   time.Time     `json:"expires_at_utc"`
	ExecutionMode  string        `json:"execution_mode"`
	InitialCash    decimal.Value `json:"initial_cash"`
	Currency       string        `json:"currency"`
	Clock          time.Time     `json:"clock"`
	AccountPolicy  AccountPolicy `json:"account_policy"`
	SimConfig      SimConfig     `json:"sim_config"`
}
type SubmitOrder struct {
	Command
	Order OrderRequest `json:"order"`
}
type RegisterSpec struct {
	Command
	Spec InstrumentSpec `json:"spec"`
}
type SetProtection struct {
	Command
	InstrumentKey InstrumentKey  `json:"instrument_key"`
	Plan          ProtectionPlan `json:"plan"`
}
type RecordIncome struct {
	Command
	Income Income `json:"income"`
}
type ReplayFrame struct {
	At            time.Time     `json:"at"`
	InstrumentKey InstrumentKey `json:"instrument_key"`
	Points        []MarketPoint `json:"points"`
	Liquidity     decimal.Value `json:"liquidity"`
	Candle        *Candle       `json:"candle"`
}
type AdvanceReplay struct {
	Command
	Frame ReplayFrame `json:"frame"`
}
type TargetRequest struct {
	Command
	OwnerID          string          `json:"owner_id"`
	InstrumentKey    InstrumentKey   `json:"instrument_key"`
	TargetVersion    int64           `json:"target_version"`
	TargetQuantity   decimal.Value   `json:"target_quantity"`
	PolicyVersion    string          `json:"policy_version"`
	SpecVersion      string          `json:"spec_version"`
	SourceDecisionID string          `json:"source_decision_id"`
	ProtectionPlan   *ProtectionPlan `json:"protection_plan"`
	OwnerEpoch       int64           `json:"owner_epoch"`
}
type ImportExternal struct {
	Command
	Fact ExternalFact `json:"fact"`
}
type RegisterFX struct {
	Command
	Rate FxRate `json:"rate"`
}
type ResolveExternal struct {
	Command
	InstrumentKey InstrumentKey `json:"instrument_key"`
	OwnerID       string        `json:"owner_id"`
	OwnerEpoch    int64         `json:"owner_epoch"`
	EvidenceRef   string        `json:"evidence_ref"`
}
type FenceExecutor struct {
	Command
	Epoch       int64  `json:"epoch"`
	EvidenceRef string `json:"evidence_ref"`
}
type MarketSnapshot struct {
	Command
	At      time.Time     `json:"at"`
	Points  []MarketPoint `json:"points"`
	Candles []Candle      `json:"candles"`
	Trades  []MarketTrade `json:"trades"`
}
