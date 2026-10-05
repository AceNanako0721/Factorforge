package domain

import (
	"github.com/AceNanako0721/Factorforge/src/factorforge/trading/api/dto"
	"time"
)

type Decimal = dto.Decimal
type Error struct {
	Code   string
	Status int
}

func (e *Error) Error() string { return e.Code }

// These are typed inputs to the migrated numerical functions, not a replacement
// for the complete persisted StrategyState or public API request validation.
type Level struct{ Exposure, Enter, Exit Decimal }
type NumericalPolicy struct {
	Levels                                                                         []Level
	CleanupThreshold, NumericalTolerance, Epsilon, SigmaRef, LiquidityBudget       Decimal
	ObjectLossBudget, PortfolioGrossLimit, PortfolioNetLimit, PortfolioStressLimit Decimal
	GroupLimits                                                                    map[string]Decimal
	MicroDistance, MaxStopFraction, FeeRate, SlippageFraction, GapFraction         Decimal
	NoiseQuantile, MaxNoiseFraction, Beta                                          Decimal
	NoiseWindow, MaxBarGapSeconds, EventTTLSeconds                                 int
	AbsolutePriceProxyValidated                                                    bool
}
type Contribution struct {
	ContributionID         string             `json:"contribution_id"`
	ObjectID               string             `json:"object_id"`
	EventID                string             `json:"event_id"`
	FamilyID               string             `json:"family_id"`
	Direction              int                `json:"direction"`
	InitialAmount          Decimal            `json:"initial_amount"`
	RemainingAmount        Decimal            `json:"remaining_amount"`
	EffectiveAt            time.Time          `json:"effective_at"`
	LastUpdatedAt          time.Time          `json:"last_updated_at"`
	HalfLife               Decimal            `json:"half_life"`
	Eta                    Decimal            `json:"eta"`
	HighWater              Decimal            `json:"high_water"`
	ReferencePrice         Decimal            `json:"reference_price"`
	ReferenceBenchmark     *Decimal           `json:"reference_benchmark"`
	FrozenParameterVersion string             `json:"frozen_parameter_version"`
	ScoreID                string             `json:"score_id"`
	Quality                Decimal            `json:"quality"`
	PriceConsumed          Decimal            `json:"price_consumed"`
	State                  string             `json:"state"`
	FactWeights            map[string]Decimal `json:"fact_weights"`
}
type LedgerEntry struct {
	Sequence         int       `json:"sequence"`
	ContributionID   string    `json:"contribution_id"`
	At               time.Time `json:"at"`
	Start            Decimal   `json:"start"`
	Injection        Decimal   `json:"injection"`
	RevisionDelta    Decimal   `json:"revision_delta"`
	TimeConsumption  Decimal   `json:"time_consumption"`
	PriceConsumption Decimal   `json:"price_consumption"`
	Invalidation     Decimal   `json:"invalidation"`
	RejectedAmount   Decimal   `json:"rejected_amount"`
	End              Decimal   `json:"end"`
	Reason           string    `json:"reason"`
	Budget           Decimal   `json:"budget"`
	DeltaHighWater   Decimal   `json:"delta_high_water"`
}
type SentimentState struct {
	Contributions []Contribution
	Ledger        []LedgerEntry
}
type PoolView struct {
	ObjectID                  string
	Plus, Minus, Net, Quality Decimal
	Contributions             []Contribution
	LedgerVersion             int
}
type MarketSample struct {
	AvailableAt                 time.Time
	Price                       Decimal
	Benchmark, Sigma, Liquidity *Decimal
	Quality                     string
}
type Bar struct {
	CloseAt, AvailableAt time.Time
	High, Low, Close     Decimal
	Final                bool
	Quality              string
}
type ProductRules struct {
	Multiplier, QuantityStep, PriceTick, MinNotional Decimal
	Capabilities, PriceRoles                         []string
	Version                                          string
}
type StopPlan struct {
	TriggerKind                                   string
	TriggerPrice, CoveredQuantity, MaxSlippageBPS Decimal
	ExitOrderType, SpecVersion                    string
}
type StopResult struct {
	Quantity Decimal
	Plan     *StopPlan
	Reason   string
	Fraction Decimal
}
type Candidate struct {
	Current, Desired, Stress Decimal
	Group                    string
}
type BaseRisk struct {
	Value, Stress Decimal
	Group         string
}

func constant(s string) Decimal {
	v, err := dto.ParseDecimal(s)
	if err != nil {
		panic("invalid internal numeric literal")
	}
	return v
}
func has(values []string, value string) bool {
	for _, v := range values {
		if v == value {
			return true
		}
	}
	return false
}
