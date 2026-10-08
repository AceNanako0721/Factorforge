// Generated from the frozen Python P2 records. Native runtime has no Python dependency.
package domain

import "time"

type RunBinding struct {
	Environment string `json:"environment"`
	AccountID   string `json:"account_id"`
	RunID       string `json:"run_id"`
}
type InstrumentBinding struct {
	Venue        string `json:"venue"`
	Product      string `json:"product"`
	InstrumentID string `json:"instrument_id"`
}
type Command struct {
	SchemaVersion   string `json:"schema_version"`
	RequestID       string `json:"request_id"`
	IdempotencyKey  string `json:"idempotency_key"`
	ExpectedVersion int    `json:"expected_version"`
	Reason          string `json:"reason"`
}
type ParameterSnapshot struct {
	Version        string                `json:"version"`
	ParentVersion  *string               `json:"parent_version"`
	Scope          string                `json:"scope"`
	ValidFrom      time.Time             `json:"valid_from"`
	Values         map[string]Decimal    `json:"values"`
	Bounds         map[string][2]Decimal `json:"bounds"`
	Steps          map[string]Decimal    `json:"steps"`
	EvidenceGates  map[string]Decimal    `json:"evidence_gates"`
	SourceManifest string                `json:"source_manifest"`
	Applicability  []string              `json:"applicability"`
	QualityState   string                `json:"quality_state"`
}
type Policy struct {
	Version                     string             `json:"version"`
	QualityState                string             `json:"quality_state"`
	SourceManifest              string             `json:"source_manifest"`
	Calibrations                []string           `json:"calibrations"`
	Rubrics                     []string           `json:"rubrics"`
	ClaimWeights                map[string]Decimal `json:"claim_weights"`
	VerificationManifests       []string           `json:"verification_manifests"`
	Levels                      []Level            `json:"levels"`
	CycleSeconds                int                `json:"cycle_seconds"`
	MaxPriceAgeSeconds          int                `json:"max_price_age_seconds"`
	TargetTTLSeconds            int                `json:"target_ttl_seconds"`
	EventCap                    Decimal            `json:"event_cap"`
	FamilyCap                   Decimal            `json:"family_cap"`
	ObjectCap                   Decimal            `json:"object_cap"`
	CleanupThreshold            Decimal            `json:"cleanup_threshold"`
	EventTTLSeconds             int                `json:"event_ttl_seconds"`
	NumericalTolerance          Decimal            `json:"numerical_tolerance"`
	Epsilon                     Decimal            `json:"epsilon"`
	SigmaRef                    Decimal            `json:"sigma_ref"`
	LiquidityBudget             Decimal            `json:"liquidity_budget"`
	ObjectLossBudget            Decimal            `json:"object_loss_budget"`
	PortfolioGrossLimit         Decimal            `json:"portfolio_gross_limit"`
	PortfolioNetLimit           Decimal            `json:"portfolio_net_limit"`
	PortfolioStressLimit        Decimal            `json:"portfolio_stress_limit"`
	GroupLimits                 map[string]Decimal `json:"group_limits"`
	MicroDistance               Decimal            `json:"micro_distance"`
	MaxStopFraction             Decimal            `json:"max_stop_fraction"`
	FeeRate                     Decimal            `json:"fee_rate"`
	SlippageFraction            Decimal            `json:"slippage_fraction"`
	GapFraction                 Decimal            `json:"gap_fraction"`
	NoiseWindow                 int                `json:"noise_window"`
	NoiseQuantile               Decimal            `json:"noise_quantile"`
	MaxBarGapSeconds            int                `json:"max_bar_gap_seconds"`
	MaxNoiseFraction            Decimal            `json:"max_noise_fraction"`
	CooldownSeconds             int                `json:"cooldown_seconds"`
	MinAdjustment               Decimal            `json:"min_adjustment"`
	WindowSeconds               int                `json:"window_seconds"`
	WindowAnchor                time.Time          `json:"window_anchor"`
	MaxNewRisk                  int                `json:"max_new_risk"`
	MaxLossCases                int                `json:"max_loss_cases"`
	ObservationMultiplier       Decimal            `json:"observation_multiplier"`
	ObservationMinSeconds       int                `json:"observation_min_seconds"`
	ObservationMaxSeconds       int                `json:"observation_max_seconds"`
	LabelSeconds                int                `json:"label_seconds"`
	LabelCostBand               Decimal            `json:"label_cost_band"`
	LabelNoiseBand              Decimal            `json:"label_noise_band"`
	Gamma                       Decimal            `json:"gamma"`
	LifecycleFraction           Decimal            `json:"lifecycle_fraction"`
	RegimeConfirmations         int                `json:"regime_confirmations"`
	RegimeDwellSeconds          int                `json:"regime_dwell_seconds"`
	RegimeEnter                 Decimal            `json:"regime_enter"`
	RegimeExit                  Decimal            `json:"regime_exit"`
	UnknownRegimeMultiplier     Decimal            `json:"unknown_regime_multiplier"`
	Beta                        Decimal            `json:"beta"`
	AbsolutePriceProxyValidated bool               `json:"absolute_price_proxy_validated"`
	HalfLifeBounds              [2]Decimal         `json:"half_life_bounds"`
	EventHalfLifeApproved       bool               `json:"event_half_life_approved"`
}
type ObservedObject struct {
	ObjectID          string            `json:"object_id"`
	InstanceID        string            `json:"instance_id"`
	Environment       string            `json:"environment"`
	TradingRunKey     RunBinding        `json:"trading_run_key"`
	InstrumentKey     InstrumentBinding `json:"instrument_key"`
	OwnerID           string            `json:"owner_id"`
	State             string            `json:"state"`
	ParameterVersion  string            `json:"parameter_version"`
	PriceProxyBinding string            `json:"price_proxy_binding"`
	RegimeBinding     string            `json:"regime_binding"`
	TimePolicyVersion string            `json:"time_policy_version"`
	AggregateVersion  int               `json:"aggregate_version"`
	OwnerEpoch        int               `json:"owner_epoch"`
	Level             int               `json:"level"`
	Direction         int               `json:"direction"`
	LastCycle         *time.Time        `json:"last_cycle"`
	LastCutoff        *time.Time        `json:"last_cutoff"`
	LastAdjustment    *time.Time        `json:"last_adjustment"`
	RecoveryState     string            `json:"recovery_state"`
	ActualQuantity    Decimal           `json:"actual_quantity"`
	PendingQuantity   Decimal           `json:"pending_quantity"`
}
type EvidenceRef struct {
	EvidenceID      string    `json:"evidence_id"`
	ContentHash     string    `json:"content_hash"`
	SourceID        string    `json:"source_id"`
	LicenceRef      string    `json:"licence_ref"`
	FirstPublicAt   time.Time `json:"first_public_at"`
	ReceivedAt      time.Time `json:"received_at"`
	AvailableAt     time.Time `json:"available_at"`
	SpanRefs        []string  `json:"span_refs"`
	VerificationRef string    `json:"verification_ref"`
}
type Claim struct {
	ClaimID              string            `json:"claim_id"`
	NormalizedFact       string            `json:"normalized_fact"`
	SubjectID            string            `json:"subject_id"`
	EconomicItem         string            `json:"economic_item"`
	Period               string            `json:"period"`
	FactTime             time.Time         `json:"fact_time"`
	NumbersWithUnits     map[string]string `json:"numbers_with_units"`
	EvidenceRefs         []string          `json:"evidence_refs"`
	SupersedesClaimID    *string           `json:"supersedes_claim_id"`
	VerifiedAt           time.Time         `json:"verified_at"`
	Weight               Decimal           `json:"weight"`
	VerificationManifest string            `json:"verification_manifest"`
}
type Event struct {
	EventID       string        `json:"event_id"`
	FamilyID      string        `json:"family_id"`
	FactVersion   int           `json:"fact_version"`
	ParentEventID *string       `json:"parent_event_id"`
	Relation      string        `json:"relation"`
	SubjectID     string        `json:"subject_id"`
	EventType     string        `json:"event_type"`
	OccurredAt    time.Time     `json:"occurred_at"`
	FirstPublicAt time.Time     `json:"first_public_at"`
	EvidenceRefs  []EvidenceRef `json:"evidence_refs"`
	Claims        []Claim       `json:"claims"`
	ObjectIDs     []string      `json:"object_ids"`
	State         string        `json:"state"`
	Novelty       Decimal       `json:"novelty"`
}
type ScoreVector struct {
	Direction           int      `json:"direction"`
	ImpactPoints        Decimal  `json:"impact_points"`
	Credibility         *Decimal `json:"credibility"`
	Relevance           *Decimal `json:"relevance"`
	Novelty             *Decimal `json:"novelty"`
	ExpectationCoverage *Decimal `json:"expectation_coverage"`
	PrepricingFraction  *Decimal `json:"prepricing_fraction"`
	ExpectedHalfLife    *Decimal `json:"expected_half_life"`
	QualityScore        *Decimal `json:"quality_score"`
	UnknownFields       []string `json:"unknown_fields"`
}
type ScoreSubmission struct {
	SubmissionID       string      `json:"submission_id"`
	EventID            string      `json:"event_id"`
	FactVersion        int         `json:"fact_version"`
	ObjectID           string      `json:"object_id"`
	ScoreVersion       int         `json:"score_version"`
	PreviousScoreID    *string     `json:"previous_score_id"`
	RevisionKind       string      `json:"revision_kind"`
	Vector             ScoreVector `json:"vector"`
	EvidenceRefs       []string    `json:"evidence_refs"`
	ProducerID         string      `json:"producer_id"`
	ProducerVersion    string      `json:"producer_version"`
	RubricVersion      string      `json:"rubric_version"`
	CalibrationVersion string      `json:"calibration_version"`
	CompletedAt        time.Time   `json:"completed_at"`
	InputManifestHash  string      `json:"input_manifest_hash"`
}
type AdmissionReceipt struct {
	SubmissionID   string     `json:"submission_id"`
	State          string     `json:"state"`
	ContributionID *string    `json:"contribution_id"`
	EligibleFrom   *time.Time `json:"eligible_from"`
	ReasonCodes    []string   `json:"reason_codes"`
	CurrentVersion int        `json:"current_version"`
}
type DecisionView struct {
	DecisionID          string         `json:"decision_id"`
	ObjectID            string         `json:"object_id"`
	CycleID             string         `json:"cycle_id"`
	AvailableCutoff     time.Time      `json:"available_cutoff"`
	PoolPlus            Decimal        `json:"pool_plus"`
	PoolMinus           Decimal        `json:"pool_minus"`
	PoolNet             Decimal        `json:"pool_net"`
	PreviousLevel       int            `json:"previous_level"`
	NewLevel            int            `json:"new_level"`
	RawExposure         Decimal        `json:"raw_exposure"`
	ProjectedExposure   Decimal        `json:"projected_exposure"`
	TargetQuantity      Decimal        `json:"target_quantity"`
	ActualQuantity      Decimal        `json:"actual_quantity"`
	PendingQuantity     Decimal        `json:"pending_quantity"`
	StopPlan            *StopPlan      `json:"stop_plan"`
	ParameterVersion    string         `json:"parameter_version"`
	InputHash           string         `json:"input_hash"`
	ReasonCodes         []string       `json:"reason_codes"`
	SourceEventVersions map[string]int `json:"source_event_versions"`
	InputSnapshot       map[string]any `json:"input_snapshot"`
}
type TradingSnapshot struct {
	RunKey          RunBinding                  `json:"run_key"`
	Version         int                         `json:"version"`
	At              time.Time                   `json:"at"`
	Equity          Decimal                     `json:"equity"`
	PolicyVersion   string                      `json:"policy_version"`
	RiskLocks       []string                    `json:"risk_locks"`
	RunState        string                      `json:"run_state"`
	Actual          map[string]Decimal          `json:"actual"`
	Pending         map[string]Decimal          `json:"pending"`
	OwnerEpochs     map[string]int              `json:"owner_epochs"`
	TargetVersions  map[string]int              `json:"target_versions"`
	SourceDecisions []string                    `json:"source_decisions"`
	Specs           map[string]map[string]any   `json:"specs"`
	Samples         map[string]MarketSample     `json:"samples"`
	Bars            map[string][]Bar            `json:"bars"`
	Fills           []map[string]any            `json:"fills"`
	Incomes         []map[string]any            `json:"incomes"`
	ExternalChange  bool                        `json:"external_change"`
	Protections     map[string][]map[string]any `json:"protections"`
	OtherExposures  []ExposureRisk              `json:"other_exposures"`
	AverageEntries  map[string]*Decimal         `json:"average_entries"`
}
type TargetOutbox struct {
	Decision       DecisionView    `json:"decision"`
	TargetVersion  int             `json:"target_version"`
	ExpiresAt      time.Time       `json:"expires_at"`
	ReservationID  *string         `json:"reservation_id"`
	State          string          `json:"state"`
	OwnerEpoch     int             `json:"owner_epoch"`
	AcceptedAt     *time.Time      `json:"accepted_at"`
	Reason         *string         `json:"reason"`
	CommandPayload *map[string]any `json:"command_payload"`
}
type Reservation struct {
	ReservationID string `json:"reservation_id"`
	ObjectID      string `json:"object_id"`
	WindowID      string `json:"window_id"`
	State         string `json:"state"`
}
type CaseRecord struct {
	CaseID               string              `json:"case_id"`
	ObjectID             string              `json:"object_id"`
	Direction            int                 `json:"direction"`
	RiskLots             []map[string]any    `json:"risk_lots"`
	EventGroups          []string            `json:"event_groups"`
	EntrySnapshot        map[string]any      `json:"entry_snapshot"`
	EntryAt              time.Time           `json:"entry_at"`
	EntryWindow          string              `json:"entry_window"`
	ObservationSeconds   int                 `json:"observation_seconds"`
	ExitAt               *time.Time          `json:"exit_at"`
	ObservationEnd       *time.Time          `json:"observation_end"`
	PNLComponents        map[string]Decimal  `json:"pnl_components"`
	Status               string              `json:"status"`
	LabelStatus          string              `json:"label_status"`
	Labels               map[string]*Decimal `json:"labels"`
	AttributionIDs       []string            `json:"attribution_ids"`
	ParameterDecisionIDs []string            `json:"parameter_decision_ids"`
	FillIDs              []string            `json:"fill_ids"`
	IncomeIDs            []string            `json:"income_ids"`
	LossCounted          bool                `json:"loss_counted"`
}
type StrategyState struct {
	TimeWindows           map[string]TimeWindowPlan   `json:"time_windows,omitempty"`
	InstanceID            string                      `json:"instance_id"`
	Environment           string                      `json:"environment"`
	Version               int                         `json:"version"`
	Objects               Ordered[*ObservedObject]    `json:"objects"`
	Policies              Ordered[*Policy]            `json:"policies"`
	Parameters            Ordered[*ParameterSnapshot] `json:"parameters"`
	Events                Ordered[*Event]             `json:"events"`
	EventVersions         Ordered[*Event]             `json:"event_versions"`
	EventReceived         Ordered[time.Time]          `json:"event_received"`
	Scores                Ordered[*ScoreSubmission]   `json:"scores"`
	Receipts              Ordered[*AdmissionReceipt]  `json:"receipts"`
	ResearchReceipts      Ordered[*AdmissionReceipt]  `json:"research_receipts"`
	PrepricingAssessments Ordered[map[string]any]     `json:"prepricing_assessments"`
	Received              Ordered[time.Time]          `json:"received"`
	Contributions         Ordered[*Contribution]      `json:"contributions"`
	Ledger                []LedgerEntry               `json:"ledger"`
	Samples               Ordered[[]MarketSample]     `json:"samples"`
	Decisions             Ordered[*DecisionView]      `json:"decisions"`
	Outbox                Ordered[*TargetOutbox]      `json:"outbox"`
	Reservations          Ordered[*Reservation]       `json:"reservations"`
	LossCases             Ordered[[]string]           `json:"loss_cases"`
	Cases                 Ordered[*CaseRecord]        `json:"cases"`
	Regimes               Ordered[map[string]any]     `json:"regimes"`
	Attributions          Ordered[map[string]any]     `json:"attributions"`
	Candidates            Ordered[map[string]any]     `json:"candidates"`
	ConsumedEvidence      []string                    `json:"consumed_evidence"`
	LearningDecisions     []map[string]any            `json:"learning_decisions"`
	ValidationRuns        Ordered[map[string]any]     `json:"validation_runs"`
	Counterfactuals       Ordered[map[string]any]     `json:"counterfactuals"`
	FactorManifests       Ordered[map[string]any]     `json:"factor_manifests"`
	SkippedCycles         []map[string]any            `json:"skipped_cycles"`
	Audit                 []map[string]any            `json:"audit"`
	Dedup                 Ordered[map[string]any]     `json:"dedup"`
}
type CreateObject struct {
	Command
	Object     ObservedObject    `json:"object"`
	Policy     Policy            `json:"policy"`
	Parameters ParameterSnapshot `json:"parameters"`
}
type EventCommand struct {
	Command
	Event Event `json:"event"`
}
type ScoreCommand struct {
	Command
	Score ScoreSubmission `json:"score"`
}
type StateCommand struct {
	Command
	State string `json:"state"`
}
type AttributionCandidate struct {
	CandidateID       string   `json:"candidate_id"`
	Category          string   `json:"category"`
	EvidenceRefs      []string `json:"evidence_refs"`
	AlternativeCauses []string `json:"alternative_causes"`
	ProducerVersion   string   `json:"producer_version"`
	Confidence        Decimal  `json:"confidence"`
}
type AttributionCommand struct {
	Command
	Candidate AttributionCandidate `json:"candidate"`
}
type AdvanceClock struct {
	At time.Time `json:"at"`
}
type Problem struct {
	Code          string  `json:"code"`
	Message       string  `json:"message"`
	Field         *string `json:"field"`
	CorrelationID *string `json:"correlation_id"`
	Retryable     bool    `json:"retryable"`
}
type AttributionView struct {
	AttributionCandidate
	CaseID string `json:"case_id"`
	State  string `json:"state"`
}
type LearningDecisionView struct {
	ObjectID  string    `json:"object_id"`
	Parameter string    `json:"parameter"`
	At        time.Time `json:"at"`
	Reason    *string   `json:"reason"`
	Error     string    `json:"error"`
	Neff      string    `json:"neff"`
	Groups    int       `json:"groups"`
}
type ValidationRunView struct {
	Manifest          map[string]any `json:"manifest"`
	Result            map[string]any `json:"result"`
	State             string         `json:"state"`
	ProductionUpgrade bool           `json:"production_upgrade"`
}
