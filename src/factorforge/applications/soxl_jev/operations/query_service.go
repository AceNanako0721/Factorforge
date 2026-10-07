// Package operations reads recorded instance state. This service intentionally
// has no signal dispatcher, provider, budget writer or lower-layer client.
package operations

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	d "github.com/AceNanako0721/Factorforge/src/factorforge/applications/soxl_jev/domain"
	"github.com/AceNanako0721/Factorforge/src/factorforge/applications/soxl_jev/ports"
	"github.com/AceNanako0721/Factorforge/src/factorforge/applications/soxl_jev/reports"
	"io"
	"sort"
	"strings"
	"time"
	"unicode/utf8"
)

type ReadPolicy struct {
	DefaultLimit, MaxLimit, MaxRecords, MaxSnapshotBytes, MaxOriginalBytes int
	CursorAge                                                              time.Duration
	CursorKey                                                              []byte
}

func (p ReadPolicy) Valid() bool {
	return p.DefaultLimit > 0 && p.MaxLimit >= p.DefaultLimit && p.MaxRecords >= p.MaxLimit && p.MaxSnapshotBytes > 0 && p.MaxOriginalBytes > 0 && p.CursorAge > 0 && len(p.CursorKey) >= 32
}

type Filter struct {
	Cursor    string     `json:"cursor"`
	Limit     int        `json:"limit"`
	QueueKind string     `json:"queue_kind"`
	State     string     `json:"state"`
	From      *time.Time `json:"from"`
	To        *time.Time `json:"to"`
}
type Metadata struct {
	SchemaVersion   string     `json:"schema_version"`
	Environment     string     `json:"environment"`
	InstanceID      string     `json:"instance_id"`
	SourceVersion   string     `json:"source_version"`
	ObservedAt      *time.Time `json:"observed_at"`
	SnapshotVersion string     `json:"snapshot_version"`
}
type Record struct {
	Metadata
	Data any `json:"data"`
}
type Page struct {
	Metadata
	Items  []any   `json:"items"`
	Cursor *string `json:"cursor"`
}
type InstanceHealth struct {
	d.Health
	LiveReady            bool     `json:"live_ready"`
	ReadinessReasonCodes []string `json:"readiness_reason_codes"`
}
type EvidenceView struct {
	d.Evidence
	LicenceState    string  `json:"licence_state"`
	RawText         *string `json:"raw_text"`
	RedactionReason *string `json:"redaction_reason"`
}
type BudgetView struct {
	d.Budget
	RecordStatus string `json:"record_status"`
}
type QueryService struct {
	Store  ports.ReadStore
	Clock  ports.Clock
	Policy ReadPolicy
}
type cursor struct {
	Binding string    `json:"binding"`
	Version int64     `json:"version"`
	Offset  int       `json:"offset"`
	Issued  time.Time `json:"issued"`
}

func (q QueryService) token(v cursor) string {
	raw, _ := json.Marshal(v)
	mac := hmac.New(sha256.New, q.Policy.CursorKey)
	mac.Write(raw)
	return base64.RawURLEncoding.EncodeToString(raw) + "." + base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
}
func (q QueryService) parse(raw string) (cursor, error) {
	var result cursor
	bad := func() (cursor, error) { return cursor{}, d.Fail("QUERY_CURSOR_INVALID", 409) }
	parts := strings.Split(raw, ".")
	if len(parts) != 2 || len(raw) > 4096 {
		return bad()
	}
	body, err := base64.RawURLEncoding.DecodeString(parts[0])
	if err != nil {
		return bad()
	}
	sig, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return bad()
	}
	mac := hmac.New(sha256.New, q.Policy.CursorKey)
	mac.Write(body)
	if !hmac.Equal(mac.Sum(nil), sig) {
		return bad()
	}
	dec := json.NewDecoder(bytes.NewReader(body))
	dec.DisallowUnknownFields()
	if dec.Decode(&result) != nil || dec.Decode(new(any)) != io.EOF || result.Offset < 0 || result.Issued.IsZero() {
		return bad()
	}
	return result, nil
}
func latest(values ...*time.Time) *time.Time {
	var result *time.Time
	for _, v := range values {
		if v != nil && (result == nil || v.After(*result)) {
			copy := *v
			result = &copy
		}
	}
	return result
}
func at(v time.Time) *time.Time  { return &v }
func reason(code string) *string { return &code }
func (q QueryService) Read(ctx context.Context, p d.ReadPrincipal, resource, id string, f Filter) (any, error) {
	if q.Store == nil || q.Clock == nil {
		return nil, d.Fail("INSTANCE_QUERY_NOT_CONFIGURED", 503)
	}
	if !p.Valid() || p.Binding != q.Store.Binding() {
		return nil, d.Fail("INSTANCE_SCOPE_FORBIDDEN", 403)
	}
	if !q.Policy.Valid() {
		return nil, d.Fail("QUERY_POLICY_REQUIRED", 503)
	}
	if !d.Has([]string{"health", "sources", "analysis-jobs", "analysis-job", "evidence", "budgets", "reports", "audit"}, resource) {
		return nil, d.Fail("RESOURCE_NOT_FOUND", 404)
	}
	if f.Limit == 0 {
		f.Limit = q.Policy.DefaultLimit
	}
	if f.Limit < 1 || f.Limit > q.Policy.MaxLimit {
		return nil, d.Fail("QUERY_LIMIT_INVALID", 422)
	}
	if f.QueueKind != "" && !d.Has([]string{"RESEARCH", p.Binding.Environment}, f.QueueKind) {
		return nil, d.Fail("INSTANCE_SCOPE_FORBIDDEN", 403)
	}
	if f.State != "" && !d.Has(d.JobStates, f.State) {
		return nil, d.Fail("QUERY_STATE_INVALID", 422)
	}
	if f.From != nil && f.To != nil && f.From.After(*f.To) {
		return nil, d.Fail("QUERY_RANGE_INVALID", 422)
	}
	if (f.QueueKind != "" || f.State != "") && resource != "analysis-jobs" || (f.From != nil || f.To != nil) && resource != "reports" && resource != "audit" || f.Cursor != "" && !d.Has([]string{"sources", "analysis-jobs", "reports", "audit"}, resource) {
		return nil, d.Fail("QUERY_PARAMETER_INVALID", 422)
	}
	s, err := q.Store.Read(ctx, q.Policy.MaxSnapshotBytes)
	if err != nil {
		return nil, err
	}
	if s.Binding != p.Binding {
		return nil, d.Fail("INSTANCE_SCOPE_FORBIDDEN", 403)
	}
	if err = s.Validate(); err != nil {
		return nil, err
	}
	if s.RecordCount() > q.Policy.MaxRecords {
		return nil, d.Fail("QUERY_RESOURCE_LIMIT", 503)
	}
	meta := Metadata{SchemaVersion: d.SchemaVersion, Environment: s.Binding.Environment, InstanceID: s.Binding.InstanceID, SourceVersion: s.SourceVersion, SnapshotVersion: d.SnapshotVersion(s.Version)}
	if resource == "health" {
		if s.Health == nil {
			return nil, d.Fail("HEALTH_NOT_RECORDED", 404)
		}
		h := *s.Health
		h.Degradation = reports.Strings(h.Degradation)
		if h.Capabilities == nil {
			h.Capabilities = []d.Capability{}
		}
		meta.ObservedAt = h.CheckedAt
		// This slice supplies read infrastructure, not runtime/stage admission.
		return Record{meta, InstanceHealth{h, false, []string{"INSTANCE_RUNTIME_ADMISSION_NOT_IMPLEMENTED"}}}, nil
	}
	if resource == "analysis-job" {
		for _, v := range s.Jobs {
			if v.JobID == id {
				v.ReasonCodes = reports.Strings(v.ReasonCodes)
				meta.ObservedAt = latest(at(v.CreatedAt), v.ClaimedAt, v.StartedAt, v.CompletedAt)
				return Record{meta, v}, nil
			}
		}
		return nil, d.Fail("ANALYSIS_JOB_NOT_FOUND", 404)
	}
	if resource == "evidence" {
		var e *d.Evidence
		for i := range s.Evidence {
			if s.Evidence[i].EvidenceID == id {
				e = &s.Evidence[i]
				break
			}
		}
		if e == nil {
			return nil, d.Fail("EVIDENCE_NOT_FOUND", 404)
		}
		v := EvidenceView{Evidence: *e, LicenceState: "UNKNOWN", RedactionReason: reason("LICENCE_NOT_VERIFIED")}
		v.Facts = append([]d.Fact{}, e.Facts...)
		if v.Facts == nil {
			v.Facts = []d.Fact{}
		}
		if v.Spans == nil {
			v.Spans = []d.Span{}
		}
		for i := range v.Facts {
			v.Facts[i].SpanIDs = reports.Strings(v.Facts[i].SpanIDs)
		}
		var source *d.Source
		for i := range s.Sources {
			if s.Sources[i].SourceID == e.SourceID {
				source = &s.Sources[i]
				break
			}
		}
		if source != nil {
			v.LicenceState = source.LicenceState
		}
		now := q.Clock.Now()
		allowed := source != nil && source.LicenceState == "VERIFIED" && source.LicenceRef == e.LicenceRef && source.AllowOriginal && (source.LicenceValidUntil == nil || source.LicenceValidUntil.After(now))
		if allowed && !d.Has(p.OriginalSources, e.SourceID) {
			v.RedactionReason = reason("ORIGINAL_SCOPE_FORBIDDEN")
			allowed = false
		}
		if allowed {
			raw, e2 := q.Store.Original(ctx, e.EvidenceID, s.Version, q.Policy.MaxOriginalBytes)
			if e2 != nil {
				return nil, e2
			}
			if raw == nil {
				v.RedactionReason = reason("ORIGINAL_NOT_RECORDED")
			} else {
				digest := sha256.Sum256(raw)
				if len(raw) > q.Policy.MaxOriginalBytes || !utf8.Valid(raw) || hex.EncodeToString(digest[:]) != e.ContentHash {
					return nil, d.Fail("ORIGINAL_INTEGRITY_INVALID", 503)
				}
				text := string(raw)
				v.RawText = &text
				v.RedactionReason = nil
			}
		}
		meta.ObservedAt = latest(at(e.ReceivedAt), e.ExtractedAt)
		return Record{meta, v}, nil
	}
	if resource == "budgets" {
		views := []BudgetView{}
		for _, kind := range []string{"RESEARCH", "SIM", "LIVE"} {
			view := BudgetView{Budget: d.Budget{QueueKind: kind}, RecordStatus: "NOT_RECORDED"}
			if kind != "RESEARCH" && kind != p.Binding.Environment {
				view.RecordStatus = "NOT_APPLICABLE"
			} else {
				for _, v := range s.Budgets {
					if v.QueueKind == kind {
						view.Budget = v
						view.RecordStatus = "RECORDED"
						meta.ObservedAt = latest(meta.ObservedAt, v.ObservedAt)
					}
				}
			}
			views = append(views, view)
		}
		return Record{meta, views}, nil
	}
	type row struct {
		id   string
		data any
		time *time.Time
	}
	rows := []row{}
	switch resource {
	case "sources":
		for _, v := range s.Sources {
			rows = append(rows, row{v.SourceID, v, latest(v.LastSuccessAt, v.LastFailureAt)})
		}
	case "analysis-jobs":
		for _, v := range s.Jobs {
			if (f.QueueKind == "" || f.QueueKind == v.QueueKind) && (f.State == "" || f.State == v.State) {
				v.ReasonCodes = reports.Strings(v.ReasonCodes)
				rows = append(rows, row{v.JobID, v, latest(at(v.CreatedAt), v.ClaimedAt, v.StartedAt, v.CompletedAt)})
			}
		}
	case "reports":
		for _, v := range reports.Read(s.Reports, f.From, f.To) {
			rows = append(rows, row{v.ReportID, v, at(v.RecordedAt)})
		}
	case "audit":
		for _, v := range s.Audit {
			if reports.Within(v.RecordedAt, f.From, f.To) {
				rows = append(rows, row{v.AuditID, v, at(v.RecordedAt)})
			}
		}
	}
	sort.Slice(rows, func(i, j int) bool { return rows[i].id < rows[j].id })
	// Include the entire effective authorization and filter, not the token.
	bound := p
	bound.OriginalSources = append([]string(nil), p.OriginalSources...)
	sort.Strings(bound.OriginalSources)
	filter := f
	filter.Cursor = ""
	fingerprint, _ := json.Marshal([]any{bound, resource, id, filter})
	hash := sha256.Sum256(fingerprint)
	binding := hex.EncodeToString(hash[:])
	issued := q.Clock.Now()
	offset := 0
	if f.Cursor != "" {
		c, e := q.parse(f.Cursor)
		if e != nil {
			return nil, e
		}
		if c.Binding != binding || c.Version != s.Version || issued.Before(c.Issued) || issued.Sub(c.Issued) > q.Policy.CursorAge || c.Offset > len(rows) {
			return nil, d.Fail("QUERY_CURSOR_INVALID", 409)
		}
		issued = c.Issued
		offset = c.Offset
	}
	end := len(rows)
	if f.Limit < len(rows)-offset {
		end = offset + f.Limit
	}
	items := []any{}
	for _, v := range rows[offset:end] {
		items = append(items, v.data)
		meta.ObservedAt = latest(meta.ObservedAt, v.time)
	}
	var next *string
	if end < len(rows) {
		str := q.token(cursor{binding, s.Version, end, issued})
		next = &str
	}
	return Page{meta, items, next}, nil
}
