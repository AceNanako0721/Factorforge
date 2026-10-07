// Package domain owns recorded instance facts. It has no provider, network,
// execution or database dependency. A read snapshot is a projection, not a job
// queue and not evidence that a production provider or LIVE stage is admitted.
package domain

import (
	"encoding/json"
	"fmt"
	"regexp"
	"time"
)

const SchemaVersion = "instance-2.0"

type Error struct {
	Code   string
	Status int
}

func (e *Error) Error() string           { return e.Code }
func Fail(code string, status int) error { return &Error{code, status} }

var idPattern = regexp.MustCompile(`^[A-Za-z0-9_.-]{1,128}$`)
var hashPattern = regexp.MustCompile(`^[a-f0-9]{64}$`)
var codePattern = regexp.MustCompile(`^[A-Z][A-Z0-9_]{0,127}$`)
var decimalPattern = regexp.MustCompile(`^(0|[1-9][0-9]*)(\.[0-9]+)?$`)

func ValidID(v string) bool { return idPattern.MatchString(v) }
func Has(values []string, value string) bool {
	for _, v := range values {
		if v == value {
			return true
		}
	}
	return false
}
func Codes(values []string) bool {
	for _, v := range values {
		if !codePattern.MatchString(v) {
			return false
		}
	}
	return true
}

type Binding struct {
	InstanceID  string `json:"instance_id"`
	Environment string `json:"environment"`
}

func (b Binding) Valid() bool {
	return ValidID(b.InstanceID) && Has([]string{"SIM", "LIVE"}, b.Environment)
}

type ReadPrincipal struct {
	PrincipalID          string   `json:"principal_id"`
	Binding              Binding  `json:"binding"`
	Read                 bool     `json:"read"`
	OriginalSources      []string `json:"original_sources"`
	AuthorizationVersion string   `json:"authorization_version"`
}

func (p ReadPrincipal) Valid() bool {
	if !p.Binding.Valid() || !p.Read || !ValidID(p.PrincipalID) || !ValidID(p.AuthorizationVersion) {
		return false
	}
	for _, id := range p.OriginalSources {
		if !ValidID(id) {
			return false
		}
	}
	return true
}

type Capability struct {
	Name        string     `json:"name"`
	State       string     `json:"state"`
	CheckedAt   *time.Time `json:"checked_at"`
	EvidenceRef *string    `json:"evidence_ref"`
}
type Health struct {
	Stage         string       `json:"stage"`
	Degradation   []string     `json:"degradation"`
	RecoveryState string       `json:"recovery_state"`
	CheckedAt     *time.Time   `json:"checked_at"`
	Capabilities  []Capability `json:"capability_states"`
}
type Source struct {
	SourceID          string     `json:"source_id"`
	RegistryVersion   string     `json:"registry_version"`
	LicenceRef        string     `json:"licence_ref"`
	LicenceState      string     `json:"licence_state"`
	AllowOriginal     bool       `json:"allow_original"`
	LicenceValidUntil *time.Time `json:"licence_valid_until"`
	Enabled           bool       `json:"enabled"`
	LastSuccessAt     *time.Time `json:"last_success_at"`
	LastFailureAt     *time.Time `json:"last_failure_at"`
	LastFailureCode   *string    `json:"last_failure_code"`
	OldestPendingAt   *time.Time `json:"oldest_pending_at"`
}
type Job struct {
	JobID         string     `json:"job_id"`
	ObjectID      *string    `json:"object_id"`
	QueueKind     string     `json:"queue_kind"`
	State         string     `json:"state"`
	CreatedAt     time.Time  `json:"created_at"`
	ClaimedAt     *time.Time `json:"claimed_at"`
	StartedAt     *time.Time `json:"started_at"`
	CompletedAt   *time.Time `json:"completed_at"`
	Deadline      time.Time  `json:"deadline"`
	ResolvedModel *string    `json:"resolved_model"`
	ReceiptRef    *string    `json:"receipt_ref"`
	ReasonCodes   []string   `json:"reason_codes"`
}

var JobStates = []string{"QUEUED", "CLAIMED", "RUNNING", "COMPLETED", "ABSTAINED", "EXPIRED", "FAILED"}

type Fact struct {
	ClaimID      string     `json:"claim_id"`
	SubjectID    string     `json:"subject_id"`
	EconomicItem string     `json:"economic_item"`
	FactTime     *time.Time `json:"fact_time"`
	SpanIDs      []string   `json:"span_ids"`
}
type Span struct {
	SpanID      string `json:"span_id"`
	StartOffset int    `json:"start_offset"`
	EndOffset   int    `json:"end_offset"`
	TextHash    string `json:"text_hash"`
	LicenceRef  string `json:"licence_ref"`
}
type Evidence struct {
	EvidenceID          string     `json:"evidence_id"`
	SourceID            string     `json:"source_id"`
	ContentHash         string     `json:"content_hash"`
	LicenceRef          string     `json:"licence_ref"`
	FirstPublicAt       *time.Time `json:"first_public_at"`
	ProviderPublishedAt *time.Time `json:"provider_published_at"`
	ReceivedAt          time.Time  `json:"received_at"`
	ExtractedAt         *time.Time `json:"extracted_at"`
	RevisionOf          *string    `json:"revision_of"`
	ExtractionState     string     `json:"extraction_state"`
	Facts               []Fact     `json:"facts"`
	Spans               []Span     `json:"spans"`
}

// Nullable budget fields mean no recorded measurement, never a synthetic zero.
type Budget struct {
	QueueKind        string     `json:"queue_kind"`
	PolicyVersion    *string    `json:"policy_version"`
	Limit            *string    `json:"limit"`
	Consumed         *string    `json:"consumed"`
	ReservedCapacity *int       `json:"reserved_capacity"`
	UpstreamLimit    *string    `json:"upstream_limit"`
	Unit             *string    `json:"unit"`
	WindowStart      *time.Time `json:"window_start"`
	WindowEnd        *time.Time `json:"window_end"`
	ObservedAt       *time.Time `json:"observed_at"`
}
type Report struct {
	ReportID                 string     `json:"report_id"`
	RecordedAt               time.Time  `json:"recorded_at"`
	PeriodStart              *time.Time `json:"period_start"`
	PeriodEnd                *time.Time `json:"period_end"`
	State                    string     `json:"state"`
	FrameworkSnapshotVersion *string    `json:"framework_snapshot_version"`
	ParameterVersions        []string   `json:"parameter_versions"`
	ActivationRefs           []string   `json:"activation_refs"`
	AttributionRefs          []string   `json:"attribution_refs"`
	ReasonCodes              []string   `json:"reason_codes"`
}
type Audit struct {
	AuditID    string    `json:"audit_id"`
	RecordedAt time.Time `json:"recorded_at"`
	Action     string    `json:"action"`
	Code       *string   `json:"code"`
	JobID      *string   `json:"job_id"`
	EvidenceID *string   `json:"evidence_id"`
	ReceiptRef *string   `json:"receipt_ref"`
}
type Snapshot struct {
	Binding       Binding    `json:"binding"`
	Version       int64      `json:"version"`
	SourceVersion string     `json:"source_version"`
	Health        *Health    `json:"health"`
	Sources       []Source   `json:"sources"`
	Jobs          []Job      `json:"jobs"`
	Evidence      []Evidence `json:"evidence"`
	Budgets       []Budget   `json:"budgets"`
	Reports       []Report   `json:"reports"`
	Audit         []Audit    `json:"audit"`
}

func (s Snapshot) RecordCount() int {
	n := len(s.Sources) + len(s.Jobs) + len(s.Evidence) + len(s.Budgets) + len(s.Reports) + len(s.Audit)
	if s.Health != nil {
		n += 1 + len(s.Health.Capabilities)
	}
	for _, e := range s.Evidence {
		n += len(e.Facts) + len(e.Spans)
	}
	return n
}

// Validate on publication and load. Unknown JSON fields are rejected by the
// store before this check; private URLs, provider input and free dictionaries
// are not part of the projection schema in the first place.
func (s Snapshot) Validate() error {
	bad := func() error { return Fail("INSTANCE_RECORD_INVALID", 503) }
	if !s.Binding.Valid() || s.Version < 0 || !ValidID(s.SourceVersion) {
		return bad()
	}
	seen := map[string]bool{}
	unique := func(kind, id string) bool {
		k := kind + ":" + id
		if !ValidID(id) || seen[k] {
			return false
		}
		seen[k] = true
		return true
	}
	queue := func(v string) bool { return v == "RESEARCH" || v == s.Binding.Environment }
	if h := s.Health; h != nil {
		if !Has([]string{"R0", "R1", "R2", "R3", "R4"}, h.Stage) || !Codes(h.Degradation) || !codePattern.MatchString(h.RecoveryState) {
			return bad()
		}
		if (s.Binding.Environment == "SIM" && Has([]string{"R3", "R4"}, h.Stage)) || (s.Binding.Environment == "LIVE" && Has([]string{"R0", "R1", "R2"}, h.Stage)) {
			return bad()
		}
		for _, c := range h.Capabilities {
			if !unique("capability", c.Name) || !Has([]string{"UNKNOWN", "UNAVAILABLE", "NOT_CONFIGURED", "EXAMPLE_OR_MOCK", "VERIFIED"}, c.State) {
				return bad()
			}
			if c.State == "VERIFIED" && (c.CheckedAt == nil || c.EvidenceRef == nil || !ValidID(*c.EvidenceRef)) {
				return bad()
			}
		}
	}
	for _, v := range s.Sources {
		if !unique("source", v.SourceID) || !ValidID(v.RegistryVersion) || !ValidID(v.LicenceRef) || !Has([]string{"VERIFIED", "UNKNOWN", "EXPIRED", "DENIED"}, v.LicenceState) {
			return bad()
		}
		if v.LastFailureCode != nil && !codePattern.MatchString(*v.LastFailureCode) {
			return bad()
		}
	}
	for _, v := range s.Jobs {
		if !unique("job", v.JobID) || !queue(v.QueueKind) || !Has(JobStates, v.State) || !Codes(v.ReasonCodes) || v.Deadline.Before(v.CreatedAt) {
			return bad()
		}
		if v.ObjectID != nil && !ValidID(*v.ObjectID) {
			return bad()
		}
		if v.CompletedAt != nil && v.CompletedAt.Before(v.CreatedAt) {
			return bad()
		}
		for _, ref := range []*string{v.ResolvedModel, v.ReceiptRef} {
			if ref != nil && !ValidID(*ref) {
				return bad()
			}
		}
	}
	for _, v := range s.Evidence {
		if !unique("evidence", v.EvidenceID) || !ValidID(v.SourceID) || !ValidID(v.LicenceRef) || !hashPattern.MatchString(v.ContentHash) || !Has([]string{"COMPLETE", "INCOMPLETE", "FAILED", "UNKNOWN"}, v.ExtractionState) {
			return bad()
		}
		spans := map[string]bool{}
		for _, span := range v.Spans {
			if !ValidID(span.SpanID) || spans[span.SpanID] || span.StartOffset < 0 || span.EndOffset <= span.StartOffset || !hashPattern.MatchString(span.TextHash) || !ValidID(span.LicenceRef) {
				return bad()
			}
			spans[span.SpanID] = true
		}
		claims := map[string]bool{}
		for _, f := range v.Facts {
			if !ValidID(f.ClaimID) || claims[f.ClaimID] || !ValidID(f.SubjectID) || !ValidID(f.EconomicItem) {
				return bad()
			}
			claims[f.ClaimID] = true
			for _, id := range f.SpanIDs {
				if !spans[id] {
					return bad()
				}
			}
		}
	}
	for _, v := range s.Budgets {
		if !unique("budget", v.QueueKind) || !queue(v.QueueKind) {
			return bad()
		}
		for _, n := range []*string{v.Limit, v.Consumed, v.UpstreamLimit} {
			if n != nil && !decimalPattern.MatchString(*n) {
				return bad()
			}
		}
		if v.ReservedCapacity != nil && *v.ReservedCapacity < 0 {
			return bad()
		}
		if v.WindowStart != nil && v.WindowEnd != nil && !v.WindowStart.Before(*v.WindowEnd) {
			return bad()
		}
		for _, ref := range []*string{v.PolicyVersion, v.Unit} {
			if ref != nil && !ValidID(*ref) {
				return bad()
			}
		}
	}
	for _, v := range s.Reports {
		if !unique("report", v.ReportID) || !Has([]string{"RECORDED", "UNKNOWN", "INCOMPLETE"}, v.State) || !Codes(v.ReasonCodes) {
			return bad()
		}
		if v.FrameworkSnapshotVersion != nil && !ValidID(*v.FrameworkSnapshotVersion) {
			return bad()
		}
		for _, refs := range [][]string{v.ParameterVersions, v.ActivationRefs, v.AttributionRefs} {
			for _, ref := range refs {
				if !ValidID(ref) {
					return bad()
				}
			}
		}
	}
	for _, v := range s.Audit {
		if !unique("audit", v.AuditID) || !codePattern.MatchString(v.Action) {
			return bad()
		}
		if v.Code != nil && !codePattern.MatchString(*v.Code) {
			return bad()
		}
		for _, ref := range []*string{v.JobID, v.EvidenceID, v.ReceiptRef} {
			if ref != nil && !ValidID(*ref) {
				return bad()
			}
		}
	}
	// RFC3339 UTC is the only published time domain, including nested records.
	raw, err := json.Marshal(s)
	if err != nil {
		return bad()
	}
	var values any
	if json.Unmarshal(raw, &values) != nil {
		return bad()
	}
	var times func(any) bool
	times = func(v any) bool {
		switch x := v.(type) {
		case map[string]any:
			for k, item := range x {
				if k == "deadline" || len(k) > 3 && k[len(k)-3:] == "_at" || k == "window_start" || k == "window_end" || k == "period_start" || k == "period_end" || k == "fact_time" || k == "licence_valid_until" {
					if item == nil {
						continue
					}
					str, ok := item.(string)
					if !ok {
						return false
					}
					at, e := time.Parse(time.RFC3339Nano, str)
					if e != nil || at.IsZero() || str[len(str)-1] != 'Z' {
						return false
					}
				}
				if !times(item) {
					return false
				}
			}
		case []any:
			for _, item := range x {
				if !times(item) {
					return false
				}
			}
		}
		return true
	}
	if !times(values) {
		return bad()
	}
	return nil
}
func SnapshotVersion(v int64) string { return fmt.Sprint(v) }
