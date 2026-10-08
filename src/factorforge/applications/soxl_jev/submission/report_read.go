package submission

import (
	"context"

	d "github.com/AceNanako0721/Factorforge/src/factorforge/applications/soxl_jev/domain"
	"io"
	"net"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"
)

type ReportReadOptions struct {
	URL, Token                     string
	Binding                        d.Binding
	ObjectID                       string
	MaxBytes, MaxRecords, MaxPages int
	Timeout                        time.Duration
	FixtureOnly                    bool
}
type ReportRead struct {
	options ReportReadOptions
	client  *http.Client
}

func NewReportRead(o ReportReadOptions) (*ReportRead, error) {
	u, e := url.Parse(o.URL)
	if e != nil || u.Host == "" || u.User != nil || u.Path != "" || u.RawQuery != "" || u.Fragment != "" || o.Token == "" || !o.Binding.Valid() || !d.ValidID(o.ObjectID) || o.MaxBytes < 1 || o.MaxRecords < 1 || o.MaxPages < 1 || o.Timeout <= 0 {
		return nil, d.Fail("REPORT_READ_CONFIGURATION_REQUIRED", 503)
	}
	if u.Scheme != "https" {
		ip := net.ParseIP(u.Hostname())
		if u.Scheme != "http" || !o.FixtureOnly || o.Binding.Environment != "SIM" || ip == nil || !ip.IsLoopback() {
			return nil, d.Fail("REPORT_READ_TLS_REQUIRED", 503)
		}
	}
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.Proxy = nil
	return &ReportRead{o, &http.Client{Timeout: o.Timeout, Transport: transport, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}}, nil
}
func (c *ReportRead) read(ctx context.Context, path string, q url.Values, out any) error {
	u := c.options.URL + "/api/v2/strategy" + path
	if len(q) > 0 {
		u += "?" + q.Encode()
	}
	r, err := http.NewRequestWithContext(ctx, "GET", u, nil)
	if err != nil {
		return d.Fail("REPORT_READ_INVALID", 503)
	}
	r.Header.Set("Authorization", "Bearer "+c.options.Token)
	response, err := c.client.Do(r)
	if err != nil {
		return d.Fail("REPORT_SOURCE_UNAVAILABLE", 503)
	}
	defer response.Body.Close()
	if response.StatusCode != 200 {
		return d.Fail("REPORT_SOURCE_REJECTED", response.StatusCode)
	}
	if !strings.HasPrefix(response.Header.Get("Content-Type"), "application/json") {
		return d.Fail("REPORT_SOURCE_INVALID", 503)
	}
	raw, err := io.ReadAll(io.LimitReader(response.Body, int64(c.options.MaxBytes)+1))
	if err != nil || len(raw) > c.options.MaxBytes || d.DecodePrivate(raw, out) != nil {
		return d.Fail("REPORT_SOURCE_INVALID", 503)
	}
	return nil
}

type reportPage struct {
	Items           []map[string]any `json:"items"`
	Cursor          *string          `json:"cursor"`
	SnapshotVersion int              `json:"snapshot_version"`
	ObservedAt      *time.Time       `json:"observed_at"`
}

func (c *ReportRead) pages(ctx context.Context, path string, q url.Values, version int) ([]map[string]any, error) {
	rows := []map[string]any{}
	seen := map[string]bool{}
	for i := 0; i < c.options.MaxPages; i++ {
		var page reportPage
		if err := c.read(ctx, path, q, &page); err != nil {
			return nil, err
		}
		if page.Items == nil || page.SnapshotVersion != version || page.ObservedAt != nil && !d.UTC(*page.ObservedAt) {
			return nil, d.Fail("REPORT_SNAPSHOT_CHANGED", 409)
		}
		rows = append(rows, page.Items...)
		if len(rows) > c.options.MaxRecords {
			return nil, d.Fail("REPORT_RECORD_BUDGET", 503)
		}
		if page.Cursor == nil {
			return rows, nil
		}
		if *page.Cursor == "" || seen[*page.Cursor] {
			return nil, d.Fail("REPORT_CURSOR_INVALID", 409)
		}
		seen[*page.Cursor] = true
		q.Set("cursor", *page.Cursor)
	}
	return nil, d.Fail("REPORT_PAGE_BUDGET", 503)
}
func (c *ReportRead) ReadReport(ctx context.Context, objectID string, from, to, now time.Time) (d.FrameworkReport, error) {
	o := c.options
	var out d.FrameworkReport
	if objectID != o.ObjectID || !d.UTC(from) || !d.UTC(to) || !d.UTC(now) || !from.Before(to) || to.After(now) {
		return out, d.Fail("REPORT_PERIOD_INVALID", 422)
	}
	var health struct {
		Schema              string `json:"schema_version"`
		Environment         string `json:"environment"`
		Listener            string `json:"listener"`
		ApplicationRequired bool   `json:"application_required"`
		LiveReady           bool   `json:"live_ready"`
	}
	if err := c.read(ctx, "/health", nil, &health); err != nil {
		return out, err
	}
	if health.Schema != "strategy-2.0" || health.Environment != o.Binding.Environment || health.Listener != "PUBLIC_RESEARCH" {
		return out, d.Fail("REPORT_BINDING_FORBIDDEN", 403)
	}
	var first reportPage
	if err := c.read(ctx, "/objects", url.Values{"object_id": {objectID}, "limit": {"1"}}, &first); err != nil {
		return out, err
	}
	if len(first.Items) != 1 || text(first.Items[0]["object_id"]) != objectID {
		return out, d.Fail("REPORT_OBJECT_NOT_RECORDED", 404)
	}
	// The directory is side-effect free. Only after it confirms existence is the
	// legacy detail read permitted; binding is checked against the actual object.
	var object map[string]any
	if err := c.read(ctx, "/objects/"+objectID, nil, &object); err != nil {
		return out, err
	}
	if text(object["object_id"]) != objectID || text(object["instance_id"]) != o.Binding.InstanceID || text(object["environment"]) != o.Binding.Environment {
		return out, d.Fail("REPORT_BINDING_FORBIDDEN", 403)
	}
	version := first.SnapshotVersion
	if version < 0 {
		return out, d.Fail("REPORT_SNAPSHOT_CHANGED", 409)
	}
	out = d.FrameworkReport{Binding: o.Binding, ObjectID: objectID, View: d.Report{ReportID: "report-" + d.Digest([]any{o.Binding, objectID, from, to}), RecordedAt: now, PeriodStart: &from, PeriodEnd: &to, State: "RECORDED", FrameworkSnapshotVersion: ptr(strconv.Itoa(version)), ParameterVersions: []string{}, ActivationRefs: []string{}, AttributionRefs: []string{}, ReasonCodes: []string{}}, Learning: []d.LearningFact{}, Changes: []d.ActivationFact{}}
	params := text(object["parameter_version"])
	if !d.ValidID(params) {
		return out, d.Fail("REPORT_PARAMETER_INVALID", 503)
	}
	out.View.ParameterVersions = append(out.View.ParameterVersions, params)
	q := url.Values{"object_id": {objectID}, "from": {from.Format(time.RFC3339Nano)}, "to": {to.Format(time.RFC3339Nano)}}
	activations, err := c.pages(ctx, "/parameter-activations", q, version)
	if err != nil {
		return out, err
	}
	for _, row := range activations {
		if text(row["object_id"]) != objectID {
			return out, d.Fail("REPORT_BINDING_FORBIDDEN", 403)
		}
		id := text(row["activation_id"])
		if id == "" {
			return out, d.Fail("REPORT_ACTIVATION_INVALID", 503)
		}
		state := nullableText(row["state"])
		at, e := nullableAt(row["recorded_at"])
		if e != nil {
			return out, e
		}
		reasons, e := stringsValue(row["reason_codes"])
		if e != nil {
			return out, e
		}
		evidence, e := stringsValue(row["evidence_refs"])
		if e != nil {
			return out, e
		}
		if at != nil && (at.Before(from) || !at.Before(to)) {
			return out, d.Fail("REPORT_SOURCE_INVALID", 503)
		}
		fact := d.ActivationFact{ID: "activation-" + d.Digest(id), SourceRecordRef: id, At: at, State: state, PreviousVersion: nullableText(row["previous_version"]), ParameterVersion: nullableText(row["new_version"]), ReasonCodes: reasons, EvidenceRefs: evidence}
		out.Changes = append(out.Changes, fact)
		out.View.ActivationRefs = append(out.View.ActivationRefs, fact.ID)
		for _, p := range []*string{fact.PreviousVersion, fact.ParameterVersion} {
			if p != nil {
				out.View.ParameterVersions = append(out.View.ParameterVersions, *p)
			}
		}
		out.View.ReasonCodes = append(out.View.ReasonCodes, fact.ReasonCodes...)
		if state != nil && d.Codes([]string{*state}) {
			out.View.ReasonCodes = append(out.View.ReasonCodes, "PARAMETER_"+*state)
		}
	}
	var learning []d.LearningFact
	if err = c.read(ctx, "/learning-decisions", nil, &learning); err != nil {
		return out, err
	}
	if len(learning) > o.MaxRecords {
		return out, d.Fail("REPORT_RECORD_BUDGET", 503)
	}
	for _, row := range learning {
		if row.ObjectID == objectID && !row.At.Before(from) && row.At.Before(to) {
			if !d.UTC(row.At) {
				return out, d.Fail("REPORT_SOURCE_INVALID", 503)
			}
			out.Learning = append(out.Learning, row)
			if row.Reason != nil {
				out.View.ReasonCodes = append(out.View.ReasonCodes, *row.Reason)
			}
		}
	}
	var cases []map[string]any
	if err = c.read(ctx, "/objects/"+objectID+"/cases", nil, &cases); err != nil {
		return out, err
	}
	if len(cases) > o.MaxRecords {
		return out, d.Fail("REPORT_RECORD_BUDGET", 503)
	}
	for _, row := range cases {
		if text(row["object_id"]) != objectID {
			return out, d.Fail("REPORT_BINDING_FORBIDDEN", 403)
		}
		entry, e := nullableAt(row["entry_at"])
		if e != nil || entry == nil {
			return out, d.Fail("REPORT_CASE_TIME_INVALID", 503)
		}
		if entry.Before(from) || !entry.Before(to) {
			continue
		}
		out.TotalCases++
		label := text(row["label_status"])
		if !d.Has([]string{"IMMATURE", "UNKNOWN", "NEUTRAL", "CORRECT", "WRONG"}, label) {
			return out, d.Fail("REPORT_CASE_LABEL_INVALID", 503)
		}
		if label == "UNKNOWN" || label == "IMMATURE" {
			out.UnknownCases++
		}
		id := text(row["case_id"])
		if !d.ValidID(id) {
			return out, d.Fail("REPORT_CASE_INVALID", 503)
		}
		attributions, e := c.pages(ctx, "/cases/"+id+"/attributions", url.Values{"object_id": {objectID}}, version)
		if e != nil {
			return out, e
		}
		for _, record := range attributions {
			ref := text(record["candidate_id"])
			if !d.ValidID(ref) {
				return out, d.Fail("REPORT_ATTRIBUTION_INVALID", 503)
			}
			out.View.AttributionRefs = append(out.View.AttributionRefs, "attribution-"+d.Digest([]any{id, ref, record["record_kind"]}))
		}
	}
	if out.TotalCases == 0 {
		out.View.ReasonCodes = append(out.View.ReasonCodes, "UNKNOWN_RATIO_NOT_RECORDED")
	} else if out.UnknownCases > 0 {
		out.View.ReasonCodes = append(out.View.ReasonCodes, "UNKNOWN_CASE_LABELS_PRESENT")
	}
	if len(out.Learning) == 0 {
		out.View.ReasonCodes = append(out.View.ReasonCodes, "LEARNING_DECISION_NOT_RECORDED")
	}
	out.View.ReasonCodes = append(out.View.ReasonCodes, "FROZEN_CONTROL_DIFFERENCE_NOT_RECORDED")
	// Re-read the side-effect-free version after all legacy arrays. A report is
	// rejected rather than combining parameter/case/attribution snapshots.
	var last reportPage
	if err = c.read(ctx, "/objects", url.Values{"object_id": {objectID}, "limit": {"1"}}, &last); err != nil {
		return out, err
	}
	if last.SnapshotVersion != version {
		return out, d.Fail("REPORT_SNAPSHOT_CHANGED", 409)
	}
	out.View.ParameterVersions = unique(out.View.ParameterVersions)
	out.View.ActivationRefs = unique(out.View.ActivationRefs)
	out.View.AttributionRefs = unique(out.View.AttributionRefs)
	out.View.ReasonCodes = unique(out.View.ReasonCodes)
	if !out.Valid() {
		return out, d.Fail("REPORT_SOURCE_INVALID", 503)
	}
	return out, nil
}
func text(v any) string    { s, _ := v.(string); return s }
func ptr(v string) *string { return &v }
func nullableText(v any) *string {
	if v == nil {
		return nil
	}
	s, ok := v.(string)
	if !ok {
		return ptr("")
	}
	return &s
}
func stringsValue(v any) ([]string, error) {
	out := []string{}
	xs, ok := v.([]any)
	if !ok {
		return nil, d.Fail("REPORT_SOURCE_INVALID", 503)
	}
	for _, x := range xs {
		s, ok := x.(string)
		if !ok {
			return nil, d.Fail("REPORT_SOURCE_INVALID", 503)
		}
		out = append(out, s)
	}
	return out, nil
}
func nullableAt(v any) (*time.Time, error) {
	if v == nil {
		return nil, nil
	}
	raw, ok := v.(string)
	at, e := time.Parse(time.RFC3339Nano, raw)
	if !ok || e != nil || !strings.HasSuffix(raw, "Z") {
		return nil, d.Fail("REPORT_TIME_INVALID", 503)
	}
	return &at, nil
}
func unique(v []string) []string {
	set := map[string]bool{}
	out := []string{}
	for _, s := range v {
		if !set[s] {
			set[s] = true
			out = append(out, s)
		}
	}
	sort.Strings(out)
	return out
}
