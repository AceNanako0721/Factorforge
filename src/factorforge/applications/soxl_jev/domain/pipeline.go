package domain

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	dto "github.com/AceNanako0721/Factorforge/src/factorforge/strategy/api/dto"
	"time"
)

// Private pipeline records never pass directly into the public read API.
type RawEvidence struct {
	EvidenceID    string     `json:"evidence_id"`
	SourceID      string     `json:"source_id"`
	URL           string     `json:"url"`
	ContentHash   string     `json:"content_hash"`
	Content       string     `json:"content"`
	FirstPublicAt *time.Time `json:"first_public_at"`
	PublishedAt   *time.Time `json:"provider_published_at"`
	ReceivedAt    time.Time  `json:"received_at"`
	RevisionOf    *string    `json:"revision_of"`
	LicenceRef    string     `json:"licence_ref"`
}
type ExtractedEvidence struct {
	Raw                  RawEvidence `json:"raw"`
	ExtractorID          string      `json:"extractor_id"`
	ExtractorVersion     string      `json:"extractor_version"`
	CompletedAt          time.Time   `json:"completed_at"`
	Claims               []dto.Claim `json:"claims"`
	Spans                []Span      `json:"spans"`
	Complete             bool        `json:"complete"`
	VerificationManifest string      `json:"verification_manifest"`
}
type SourceRegistration struct {
	AllowOriginal   bool          `json:"allow_original"`
	SourceID        string        `json:"source_id"`
	Version         string        `json:"version"`
	LicenceRef      string        `json:"licence_ref"`
	LicenceVerified bool          `json:"licence_verified"`
	AllowAnalysis   bool          `json:"allow_analysis"`
	AllowProvider   bool          `json:"allow_provider"`
	Environments    []string      `json:"environments"`
	Enabled         bool          `json:"enabled"`
	ValidFrom       time.Time     `json:"valid_from"`
	ValidUntil      time.Time     `json:"valid_until"`
	MaxAge          time.Duration `json:"max_age"`
}
type EntityMapping struct {
	Version    string    `json:"version"`
	SubjectID  string    `json:"subject_id"`
	ObjectID   string    `json:"object_id"`
	ValidFrom  time.Time `json:"valid_from"`
	ValidUntil time.Time `json:"valid_until"`
}
type RoutingPolicy struct {
	Version             string   `json:"version"`
	Binding             Binding  `json:"binding"`
	ObjectID            string   `json:"object_id"`
	EventTypes          []string `json:"event_types"`
	CalibrationVersion  string   `json:"calibration_version"`
	CalibrationVerified bool     `json:"calibration_verified"`
	ProviderVerified    bool     `json:"provider_verified"`
}
type RoutingReceipt struct {
	RoutingID       string    `json:"routing_id"`
	Binding         Binding   `json:"binding"`
	ObjectID        string    `json:"object_id"`
	EvidenceID      string    `json:"evidence_id"`
	ContentHash     string    `json:"content_hash"`
	ClaimIDs        []string  `json:"claim_ids"`
	ManifestHash    string    `json:"input_manifest_hash"`
	RegistryVersion string    `json:"source_registry_version"`
	MappingVersion  string    `json:"entity_mapping_version"`
	PolicyVersion   string    `json:"policy_version"`
	Route           string    `json:"route"`
	ReasonCodes     []string  `json:"reason_codes"`
	AvailableAt     time.Time `json:"available_at"`
	EvaluatedAt     time.Time `json:"evaluated_at"`
}
type AnalysisRequest struct {
	RequestID          string            `json:"request_id"`
	Binding            Binding           `json:"binding"`
	ObjectID           string            `json:"object_id"`
	Evidence           ExtractedEvidence `json:"evidence"`
	Routing            RoutingReceipt    `json:"routing"`
	Event              dto.Event         `json:"event"`
	Deadline           time.Time         `json:"deadline"`
	QuestionSetVersion string            `json:"question_set_version"`
	PromptVersion      string            `json:"prompt_version_ref"`
	RubricVersion      string            `json:"rubric_version"`
	CalibrationVersion string            `json:"calibration_version"`
	ModelVersion       string            `json:"model_version"`
	PreviousScoreID    *string           `json:"previous_score_id"`
	ScoreVersion       int               `json:"score_version"`
	RevisionKind       string            `json:"revision_kind"`
}
type AnalysisCandidate struct {
	ProviderOutput      json.RawMessage `json:"provider_output"`
	ProviderTraceID     *string         `json:"provider_trace_id"`
	LatencyMilliseconds *int64          `json:"latency_milliseconds"`
	Cost                *string         `json:"cost"`
	RequestID           string          `json:"request_id"`
	ManifestHash        string          `json:"input_manifest_hash"`
	ResolvedModel       string          `json:"resolved_model_version"`
	CompletedAt         time.Time       `json:"completed_at"`
	Vector              dto.ScoreVector `json:"vector"`
	SupportedClaims     map[string]bool `json:"supported_claims"`
	RubricVersion       string          `json:"rubric_version"`
	CalibrationVersion  string          `json:"calibration_version"`
	ProducerVersion     string          `json:"producer_version"`
	Mock                bool            `json:"example_or_mock"`
	Abstained           bool            `json:"abstained"`
	ReasonCodes         []string        `json:"reason_codes"`
}
type PipelineJob struct {
	ClaimedAt    *time.Time         `json:"claimed_at"`
	StartedAt    *time.Time         `json:"started_at"`
	CompletedAt  *time.Time         `json:"completed_at"`
	ReceiptRef   *string            `json:"receipt_ref"`
	JobID        string             `json:"job_id"`
	Binding      Binding            `json:"binding"`
	QueueKind    string             `json:"queue_kind"`
	BudgetBucket string             `json:"budget_bucket"`
	Request      AnalysisRequest    `json:"request"`
	State        string             `json:"state"`
	CreatedAt    time.Time          `json:"created_at"`
	Deadline     time.Time          `json:"deadline"`
	ClaimedBy    string             `json:"claimed_by"`
	LeaseUntil   *time.Time         `json:"lease_until"`
	Attempt      int                `json:"attempt"`
	Candidate    *AnalysisCandidate `json:"candidate"`
	ReasonCodes  []string           `json:"reason_codes"`
}
type SubmissionOutbox struct {
	OutboxID      string                `json:"outbox_id"`
	Binding       Binding               `json:"binding"`
	JobID         string                `json:"job_id"`
	QueueKind     string                `json:"queue_kind"`
	Command       dto.ScoreCommand      `json:"command"`
	CandidateHash string                `json:"candidate_hash"`
	CreatedAt     time.Time             `json:"created_at"`
	ExpiresAt     time.Time             `json:"expires_at"`
	DeliveryState string                `json:"delivery_state"`
	Receipt       *dto.AdmissionReceipt `json:"receipt"`
}

func Digest(value any) string {
	raw, _ := json.Marshal(value)
	return ContentDigest(raw)
}
func ContentDigest(raw []byte) string {
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
}
func UTC(t time.Time) bool { _, offset := t.Zone(); return !t.IsZero() && offset == 0 }
