package application

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	d "github.com/AceNanako0721/Factorforge/src/factorforge/strategy/domain"
	"github.com/AceNanako0721/Factorforge/src/factorforge/strategy/ports"
)

// ReadPolicy is operator supplied. No production page size or retention is
// inferred. Version-bound cursors retain no copies of full aggregate snapshots.
type ReadPolicy struct {
	DefaultLimit, MaxLimit int
	MaxRecords             int
	CursorAge              time.Duration
	CursorKey              []byte
}
type ReadFilter struct {
	ObjectID, ContributionID, Cursor string
	From, To                         *time.Time
	Limit, Revision                  int
}
type ReadPage struct {
	Items           []map[string]any `json:"items"`
	Cursor          *string          `json:"cursor"`
	SnapshotVersion int              `json:"snapshot_version"`
	ObservedAt      *time.Time       `json:"observed_at"`
}
type QueryService struct {
	Store  ports.Store
	Clock  ports.Clock
	Policy ReadPolicy
}
type readCursor struct {
	Binding string    `json:"binding"`
	Version int       `json:"version"`
	Offset  int       `json:"offset"`
	Issued  time.Time `json:"issued"`
}
type readRow struct {
	key       string
	at        *time.Time
	value     map[string]any
	objectIDs []string
}

func readError(code string, status int) error { return &d.Error{Code: code, Status: status} }
func (q QueryService) sign(value readCursor) string {
	raw, _ := json.Marshal(value)
	mac := hmac.New(sha256.New, q.Policy.CursorKey)
	mac.Write(raw)
	return base64.RawURLEncoding.EncodeToString(raw) + "." + base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
}
func (q QueryService) cursor(raw string) (readCursor, error) {
	var value readCursor
	// Cursor input is bounded independently of an operator's page policy.
	if len(raw) > 4096 {
		return value, readError("QUERY_CURSOR_INVALID", 409)
	}
	a, b, ok := strings.Cut(raw, ".")
	if !ok {
		return value, readError("QUERY_CURSOR_INVALID", 409)
	}
	payload, err := base64.RawURLEncoding.DecodeString(a)
	if err != nil {
		return value, readError("QUERY_CURSOR_INVALID", 409)
	}
	signature, err := base64.RawURLEncoding.DecodeString(b)
	if err != nil {
		return value, readError("QUERY_CURSOR_INVALID", 409)
	}
	mac := hmac.New(sha256.New, q.Policy.CursorKey)
	mac.Write(payload)
	if !hmac.Equal(signature, mac.Sum(nil)) {
		return value, readError("QUERY_CURSOR_INVALID", 409)
	}
	decoder := json.NewDecoder(bytes.NewReader(payload))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&value) != nil || value.Offset < 0 {
		return value, readError("QUERY_CURSOR_INVALID", 409)
	}
	return value, nil
}

// Trace uses only Store.Read and authorization reads. It never calls admission,
// advances a clock, runs the decision
// cycle, trading, or a write transaction, including when rejecting a cursor.
func (q QueryService) Trace(ctx context.Context, identity d.Identity, resource, id string, filter ReadFilter) (ReadPage, error) {
	empty := ReadPage{Items: []map[string]any{}}
	if identity == nil {
		return empty, readError("AUTHENTICATION_REQUIRED", 401)
	}
	state, err := q.Store.Read(ctx, identity.Instance())
	if err != nil {
		return empty, err
	}
	if err = Authorize(state, identity, d.Query, filter.ObjectID); err != nil {
		return empty, err
	}
	if filter.ObjectID != "" && state.Objects.Value(filter.ObjectID) == nil {
		return empty, readError("OBJECT_NOT_FOUND", 404)
	}
	if q.Policy.DefaultLimit <= 0 || q.Policy.MaxLimit < q.Policy.DefaultLimit || q.Policy.MaxRecords < q.Policy.MaxLimit || q.Policy.CursorAge <= 0 || len(q.Policy.CursorKey) == 0 {
		return empty, readError("QUERY_POLICY_REQUIRED", 503)
	}
	limit := filter.Limit
	if limit == 0 {
		limit = q.Policy.DefaultLimit
	}
	if limit < 1 || limit > q.Policy.MaxLimit || filter.Revision < 0 || filter.From != nil && filter.To != nil && filter.From.After(*filter.To) {
		return empty, readError("QUERY_FILTER_INVALID", 422)
	}
	// Revalidate aggregate object bindings and workload grants on every page.
	allowed := map[string]bool{}
	for _, obj := range state.Objects.Values() {
		if obj.InstanceID != state.InstanceID || obj.Environment != state.Environment {
			return empty, readError("OBJECT_BINDING_CHANGED", 503)
		}
		if w := identity.Worker(); w != nil {
			if !d.Has(w.ObjectIDs, obj.ObjectID) {
				continue
			}
			if err = q.Store.CheckWorkload(ctx, *w, obj.ObjectID); err != nil {
				return empty, err
			}
		}
		allowed[obj.ObjectID] = true
	}
	if filter.Revision > 0 && resource != "event" || filter.ContributionID != "" && resource != "ledger" {
		return empty, readError("QUERY_FILTER_INVALID", 422)
	}
	if resource == "ledger" && state.Objects.Value(id) != nil && !allowed[id] {
		return empty, readError("QUERY_OBJECT_FORBIDDEN", 403)
	}
	if resource == "attributions" || resource == "counterfactuals" {
		if record := state.Cases.Value(id); record != nil && !allowed[record.ObjectID] {
			return empty, readError("QUERY_OBJECT_FORBIDDEN", 403)
		}
	}
	if resource == "event" || resource == "scores" {
		if event := state.Events.Value(id); event != nil {
			visible := false
			for _, objectID := range event.ObjectIDs {
				visible = visible || allowed[objectID]
			}
			if !visible {
				return empty, readError("QUERY_OBJECT_FORBIDDEN", 403)
			}
		}
	}
	counts := map[string]int{"objects": state.Objects.Len(), "events": state.EventVersions.Len(), "event": state.EventVersions.Len(), "scores": state.Scores.Len(), "ledger": len(state.Ledger), "attributions": state.Attributions.Len(), "counterfactuals": state.Counterfactuals.Len(), "parameter-activations": state.Candidates.Len() + len(state.Audit), "audit": len(state.Audit)}
	if counts[resource] > q.Policy.MaxRecords {
		return empty, readError("QUERY_RESOURCE_LIMIT", 503)
	}
	rows, err := traceRows(state, resource, id, filter)
	if err != nil {
		return empty, err
	}
	visible := []readRow{}
	for _, row := range rows {
		matches := len(row.objectIDs) == 0 && filter.ObjectID == "" && identity.Worker() == nil
		ids := []string{}
		for _, objectID := range row.objectIDs {
			if allowed[objectID] {
				ids = append(ids, objectID)
				if filter.ObjectID == "" || objectID == filter.ObjectID {
					matches = true
				}
			}
		}
		if !matches {
			continue
		}
		if filter.From != nil && (row.at == nil || row.at.Before(*filter.From)) {
			continue
		}
		if filter.To != nil && (row.at == nil || row.at.After(*filter.To)) {
			continue
		}
		if _, ok := row.value["object_ids"]; ok {
			row.value["object_ids"] = ids
		}
		visible = append(visible, row)
	}
	sort.Slice(visible, func(i, j int) bool { return visible[i].key < visible[j].key })
	bound := filter
	bound.Cursor = ""
	bound.Limit = limit
	binding := d.Digest([]any{identity.Payload(), resource, id, bound})
	position := 0
	issued := q.Clock.Now()
	if filter.Cursor != "" {
		cursor, e := q.cursor(filter.Cursor)
		if e != nil {
			return empty, e
		}
		if cursor.Binding != binding || cursor.Version != state.Version || issued.Before(cursor.Issued) || issued.Sub(cursor.Issued) > q.Policy.CursorAge {
			return empty, readError("QUERY_CURSOR_STALE", 409)
		}
		position = cursor.Offset
		issued = cursor.Issued
	}
	if position > len(visible) {
		return empty, readError("QUERY_CURSOR_INVALID", 409)
	}
	end := position + limit
	if end < position || end > len(visible) {
		end = len(visible)
	}
	page := ReadPage{Items: []map[string]any{}, SnapshotVersion: state.Version}
	for _, row := range visible[position:end] {
		page.Items = append(page.Items, row.value)
		if row.at != nil && (page.ObservedAt == nil || row.at.After(*page.ObservedAt)) {
			page.ObservedAt = row.at
		}
	}
	if end < len(visible) {
		page.Cursor = d.Ptr(q.sign(readCursor{binding, state.Version, end, issued}))
	}
	return page, nil
}

func storedTime(value any) *time.Time {
	if value == nil || d.Text(value) == "" {
		return nil
	}
	var at time.Time
	if d.Guard(func() error { at = d.At(value); return nil }) != nil {
		return nil
	}
	return &at
}

// Free dictionaries are never copied recursively into a public response.
func scalarFields(value map[string]any, names ...string) map[string]any {
	out := map[string]any{}
	for _, key := range names {
		switch v := value[key].(type) {
		case string, bool, json.Number, int, int64, float64:
			out[key] = v
		case nil:
			out[key] = nil
		}
	}
	return out
}
func references(value any) []string { return d.Unique(d.Strings(value)) }
func scalarReference(value any) any {
	if s, ok := value.(string); ok {
		return s
	}
	return nil
}
func eventView(event *d.Event) map[string]any {
	claims := []map[string]any{}
	for _, claim := range event.Claims {
		claims = append(claims, map[string]any{"claim_id": claim.ClaimID, "normalized_fact": claim.NormalizedFact, "subject_id": claim.SubjectID, "economic_item": claim.EconomicItem, "period": claim.Period, "fact_time": claim.FactTime, "verified_at": claim.VerifiedAt, "evidence_refs": claim.EvidenceRefs, "supersedes_claim_id": claim.SupersedesClaimID})
	}
	evidence := []map[string]any{}
	for _, ref := range event.EvidenceRefs {
		evidence = append(evidence, map[string]any{"evidence_id": ref.EvidenceID, "content_hash": ref.ContentHash, "source_id": ref.SourceID, "licence_ref": ref.LicenceRef, "first_public_at": ref.FirstPublicAt, "received_at": ref.ReceivedAt, "available_at": ref.AvailableAt, "verification_ref": ref.VerificationRef, "span_refs": ref.SpanRefs})
	}
	return map[string]any{"event_id": event.EventID, "family_id": event.FamilyID, "fact_version": event.FactVersion, "parent_event_id": event.ParentEventID, "relation": event.Relation, "subject_id": event.SubjectID, "event_type": event.EventType, "occurred_at": event.OccurredAt, "first_public_at": event.FirstPublicAt, "evidence_refs": evidence, "claims": claims, "object_ids": event.ObjectIDs, "state": event.State, "novelty": event.Novelty}
}
func traceRows(s *d.StrategyState, resource, id string, f ReadFilter) ([]readRow, error) {
	rows := []readRow{}
	add := func(key string, at *time.Time, value map[string]any, ids ...string) {
		rows = append(rows, readRow{key, at, value, ids})
	}
	caseObject := ""
	if resource == "attributions" || resource == "counterfactuals" {
		c := s.Cases.Value(id)
		if c == nil {
			return nil, readError("CASE_NOT_FOUND", 404)
		}
		caseObject = c.ObjectID
		if f.ObjectID != "" && f.ObjectID != caseObject {
			return nil, readError("QUERY_OBJECT_FORBIDDEN", 403)
		}
	}
	switch resource {
	case "objects":
		for _, o := range s.Objects.Values() {
			add(o.ObjectID, nil, map[string]any{"object_id": o.ObjectID, "state": o.State, "instrument_key": o.InstrumentKey, "parameter_version": o.ParameterVersion, "aggregate_version": o.AggregateVersion, "owner_epoch": o.OwnerEpoch, "recovery_state": o.RecoveryState}, o.ObjectID)
		}
	case "events", "event", "scores":
		if id != "" && s.Events.Value(id) == nil {
			return nil, readError("EVENT_NOT_FOUND", 404)
		}
		if resource == "scores" {
			for _, score := range s.Scores.Values() {
				if score.EventID != id {
					continue
				}
				view := map[string]any{"submission_id": score.SubmissionID, "event_id": score.EventID, "fact_version": score.FactVersion, "object_id": score.ObjectID, "score_version": score.ScoreVersion, "previous_score_id": score.PreviousScoreID, "revision_kind": score.RevisionKind, "vector": score.Vector, "evidence_refs": score.EvidenceRefs, "producer_id": score.ProducerID, "producer_version": score.ProducerVersion, "rubric_version": score.RubricVersion, "calibration_version": score.CalibrationVersion, "completed_at": score.CompletedAt, "admission_receipt": nil}
				if receipt := s.Receipts.Value(score.SubmissionID); receipt != nil {
					view["admission_receipt"] = receipt
				} else if receipt = s.ResearchReceipts.Value(score.SubmissionID); receipt != nil {
					view["admission_receipt"] = receipt
				}
				add(score.SubmissionID, &score.CompletedAt, view, score.ObjectID)
			}
		} else {
			found := false
			for _, event := range s.EventVersions.Values() {
				if id != "" && event.EventID != id || f.Revision > 0 && event.FactVersion != f.Revision {
					continue
				}
				found = true
				add(event.EventID+":"+fmt.Sprintf("%020d", event.FactVersion), &event.FirstPublicAt, eventView(event), event.ObjectIDs...)
			}
			if resource == "event" && !found {
				return nil, readError("EVENT_REVISION_NOT_RECORDED", 404)
			}
		}
	case "ledger":
		if s.Objects.Value(id) == nil {
			return nil, readError("OBJECT_NOT_FOUND", 404)
		}
		if f.ObjectID != "" && f.ObjectID != id {
			return nil, readError("QUERY_OBJECT_FORBIDDEN", 403)
		}
		for _, entry := range s.Ledger {
			contribution := s.Contributions.Value(entry.ContributionID)
			if contribution == nil || contribution.ObjectID != id || f.ContributionID != "" && entry.ContributionID != f.ContributionID {
				continue
			}
			add(fmt.Sprintf("%020d", entry.Sequence), &entry.At, d.Map(entry), id)
		}
	case "attributions":
		for key, record := range rangeEntries(s.Attributions) {
			if d.Text(record["case_id"]) != id {
				continue
			}
			view := scalarFields(record, "candidate_id", "case_id", "category", "state", "confidence", "calibration_manifest", "eligible")
			view["evidence_refs"] = references(record["evidence_refs"])
			view["categories"] = references(record["categories"])
			view["components"] = scalarFields(d.Object(record["components"]), "data", "label", "isolation", "stability")
			if strings.HasPrefix(key, "verified-") {
				view["record_kind"] = "VERIFIED"
			} else {
				view["record_kind"] = "CANDIDATE"
			}
			add(key, storedTime(record["at"]), view, caseObject)
		}
	case "counterfactuals":
		for key, record := range rangeEntries(s.Counterfactuals) {
			if d.Text(record["case_id"]) != id {
				continue
			}
			view := scalarFields(record, "scenario_id", "case_id", "state", "changed_factor", "learning_frozen", "research_manifest", "baseline_run_id")
			view["result"] = scalarFields(d.Object(record["result"]), "fees", "net_pnl", "gross_pnl", "equity_change", "risk_used")
			if values := d.Strings(record["interval"]); len(values) == 2 {
				view["interval"] = values
			}
			add(key, storedTime(record["at"]), view, caseObject)
		}
	case "parameter-activations":
		// Persisted transition times are the only source. A current parameter
		// difference or untimed history string cannot establish a past activation.
		for key, c := range rangeEntries(s.Candidates) {
			objectID := d.Text(c["object_id"])
			if s.Objects.Value(objectID) == nil {
				continue
			}
			found := false
			for index, audit := range s.Audit {
				if d.Text(audit["candidate_id"]) != key {
					continue
				}
				action := d.Text(audit["action"])
				state := ""
				if action == "PARAMETER_ACTIVATED" {
					state = "PUBLISHED"
				} else if action == "PARAMETER_ROLLED_BACK" {
					state = "ROLLED_BACK"
				} else if action == "PARAMETER_FROZEN" {
					state = "FROZEN"
				}
				if state == "" {
					continue
				}
				at := storedTime(audit["at"])
				if at == nil {
					continue
				}
				found = true
				old, newVersion := scalarReference(c["parent_version"]), scalarReference(c["activated_version"])
				if state == "ROLLED_BACK" {
					old, newVersion = newVersion, old
				}
				view := map[string]any{"activation_id": key + ":" + strconv.Itoa(index), "object_id": objectID, "previous_version": old, "new_version": newVersion, "state": state, "recorded_at": at, "effective_at": at, "evidence_refs": references(c["proposal_evidence"]), "reason_codes": []string{}, "source_record_id": "audit:" + strconv.Itoa(index), "record_status": "RECORDED"}
				add(view["activation_id"].(string), at, view, objectID)
			}
			history := d.Strings(c["history"])
			if d.Text(c["state"]) == "REJECTED" && len(history) > 0 && history[len(history)-1] == "REJECTED" {
				if at := storedTime(c["approved_at"]); at != nil {
					found = true
					add(key+":rejected", at, map[string]any{"activation_id": key + ":rejected", "object_id": objectID, "previous_version": scalarReference(c["parent_version"]), "new_version": nil, "state": "REJECTED", "recorded_at": at, "effective_at": nil, "evidence_refs": references(c["proposal_evidence"]), "reason_codes": []string{}, "source_record_id": key, "record_status": "RECORDED"}, objectID)
				}
			}
			if !found {
				add(key, nil, map[string]any{"activation_id": key, "object_id": objectID, "previous_version": nil, "new_version": nil, "state": nil, "recorded_at": nil, "effective_at": nil, "evidence_refs": []string{}, "reason_codes": []string{"NOT_RECORDED"}, "source_record_id": key, "record_status": "NOT_RECORDED"}, objectID)
			}
		}
	case "audit":
		for index, audit := range s.Audit {
			view := scalarFields(audit, "action", "code", "request_id", "decision_id", "candidate_id", "version", "state")
			view["source_record_id"] = "audit:" + strconv.Itoa(index)
			view["recorded_at"] = storedTime(audit["at"])
			ids := []string{}
			if objectID := d.Text(audit["object_id"]); s.Objects.Value(objectID) != nil {
				ids = append(ids, objectID)
			}
			if c := s.Candidates.Value(d.Text(audit["candidate_id"])); c != nil {
				ids = append(ids, d.Text(c["object_id"]))
			}
			if decision := s.Decisions.Value(d.Text(audit["decision_id"])); decision != nil {
				ids = append(ids, decision.ObjectID)
			}
			add(fmt.Sprintf("%020d", index), storedTime(audit["at"]), view, ids...)
		}
	default:
		return nil, readError("QUERY_RESOURCE_NOT_FOUND", 404)
	}
	return rows, nil
}
func rangeEntries[T any](values d.Ordered[T]) map[string]T {
	out := map[string]T{}
	for _, key := range values.Keys() {
		out[key] = values.Value(key)
	}
	return out
}
