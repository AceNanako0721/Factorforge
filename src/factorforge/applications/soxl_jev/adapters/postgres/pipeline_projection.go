package postgres

import (
	"bytes"
	"context"
	"encoding/json"
	d "github.com/AceNanako0721/Factorforge/src/factorforge/applications/soxl_jev/domain"
	"github.com/jackc/pgx/v5"
	"sort"
	"strconv"
	"time"
)

type ProjectionOptions struct {
	Version              int64
	SourceVersion, Stage string
	Sources              []d.SourceRegistration
	FixtureOnly          bool
	Now                  time.Time
	MaxRecords, MaxBytes int
}

// Projection reads recorded pipeline facts with the ingestor's fixed scope.
// Original bytes are kept separate and never inserted into public snapshot JSON.
func (s *PipelineStore) Projection(ctx context.Context, o ProjectionOptions) (d.Snapshot, map[string][]byte, error) {
	state := d.Snapshot{Binding: s.binding, Version: o.Version, SourceVersion: o.SourceVersion, Sources: []d.Source{}, Jobs: []d.Job{}, Evidence: []d.Evidence{}, Budgets: []d.Budget{}, Reports: []d.Report{}, Audit: []d.Audit{}}
	originals := map[string][]byte{}
	if s.kind != "INGEST" || !d.UTC(o.Now) || o.MaxRecords <= 0 || o.MaxBytes <= 0 {
		return state, nil, d.Fail("INSTANCE_PROJECTION_FORBIDDEN", 403)
	}
	if err := s.VerifyRole(ctx); err != nil {
		return state, nil, err
	}
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if err != nil {
		return state, nil, safe(err)
	}
	defer tx.Rollback(ctx)
	count := 0
	records := func(table, order string, process func([]byte) error) error {
		rows, err := tx.Query(ctx, "SELECT payload FROM "+s.schema+"."+table+" WHERE instance_id=$1 ORDER BY "+order+" LIMIT $2", s.binding.InstanceID, o.MaxRecords+1)
		if err != nil {
			return safe(err)
		}
		defer rows.Close()
		for rows.Next() {
			count++
			if count > o.MaxRecords {
				return d.Fail("INSTANCE_PROJECTION_RECORD_BUDGET_EXCEEDED", 503)
			}
			var raw []byte
			if err = rows.Scan(&raw); err != nil {
				return safe(err)
			}
			if err = process(raw); err != nil {
				return err
			}
		}
		return safe(rows.Err())
	}
	for _, source := range o.Sources {
		status := "UNKNOWN"
		if source.LicenceVerified {
			status = "VERIFIED"
		}
		if o.Now.Before(source.ValidFrom) {
			status = "DENIED"
		}
		if !o.Now.Before(source.ValidUntil) {
			status = "EXPIRED"
		}
		until := source.ValidUntil
		state.Sources = append(state.Sources, d.Source{SourceID: source.SourceID, RegistryVersion: source.Version, LicenceRef: source.LicenceRef, LicenceState: status, AllowOriginal: source.AllowOriginal && status == "VERIFIED", LicenceValidUntil: &until, Enabled: source.Enabled})
	}
	err = records("raw_evidence_manifest", "evidence_id", func(raw []byte) error {
		var e d.ExtractedEvidence
		if err := s.decode(raw, &e); err != nil {
			return err
		}
		status := "INCOMPLETE"
		if e.Complete {
			status = "COMPLETE"
		}
		at := e.CompletedAt
		facts := []d.Fact{}
		spans := []string{}
		for _, span := range e.Spans {
			spans = append(spans, span.SpanID)
		}
		for _, claim := range e.Claims {
			factAt := claim.FactTime
			facts = append(facts, d.Fact{ClaimID: claim.ClaimID, SubjectID: claim.SubjectID, EconomicItem: claim.EconomicItem, FactTime: &factAt, SpanIDs: spans})
		}
		state.Evidence = append(state.Evidence, d.Evidence{EvidenceID: e.Raw.EvidenceID, SourceID: e.Raw.SourceID, ContentHash: e.Raw.ContentHash, LicenceRef: e.Raw.LicenceRef, FirstPublicAt: e.Raw.FirstPublicAt, ProviderPublishedAt: e.Raw.PublishedAt, ReceivedAt: e.Raw.ReceivedAt, ExtractedAt: &at, RevisionOf: e.Raw.RevisionOf, ExtractionState: status, Facts: facts, Spans: e.Spans})
		originals[e.Raw.EvidenceID] = []byte(e.Raw.Content)
		return nil
	})
	if err != nil {
		return state, nil, err
	}
	for _, table := range []string{"app_research_analysis_job", "app_trading_analysis_job"} {
		err = records(table, "created_at,job_id", func(raw []byte) error {
			var j d.PipelineJob
			if err := s.decode(raw, &j); err != nil {
				return err
			}
			if j.Binding != s.binding {
				return d.Fail("PIPELINE_RECORD_BINDING_INVALID", 503)
			}
			object := j.Request.ObjectID
			var model *string
			if j.Candidate != nil {
				resolved := j.Candidate.ResolvedModel
				model = &resolved
			}
			state.Jobs = append(state.Jobs, d.Job{JobID: j.JobID, ObjectID: &object, QueueKind: j.QueueKind, State: j.State, CreatedAt: j.CreatedAt, ClaimedAt: j.ClaimedAt, StartedAt: j.StartedAt, CompletedAt: j.CompletedAt, Deadline: j.Deadline, ResolvedModel: model, ReceiptRef: j.ReceiptRef, ReasonCodes: j.ReasonCodes})
			if d.Has([]string{"QUEUED", "CLAIMED", "RUNNING"}, j.State) {
				for i := range state.Sources {
					source := &state.Sources[i]
					if source.SourceID == j.Request.Evidence.Raw.SourceID && (source.OldestPendingAt == nil || j.CreatedAt.Before(*source.OldestPendingAt)) {
						at := j.CreatedAt
						source.OldestPendingAt = &at
					}
				}
			}
			evidenceID := j.Request.Evidence.Raw.EvidenceID
			var code *string
			if len(j.ReasonCodes) > 0 {
				value := j.ReasonCodes[0]
				code = &value
			}
			changedAt := j.CreatedAt
			for _, recorded := range []*time.Time{j.ClaimedAt, j.StartedAt, j.CompletedAt} {
				if recorded != nil {
					changedAt = *recorded
				}
			}
			state.Audit = append(state.Audit, d.Audit{AuditID: "activity-" + d.Digest([]any{j.JobID, j.State, j.Attempt, j.ReceiptRef}), RecordedAt: changedAt, Action: "PIPELINE_STATE_RECORDED", Code: code, JobID: &j.JobID, EvidenceID: &evidenceID, ReceiptRef: j.ReceiptRef})
			return nil
		})
		if err != nil {
			return state, nil, err
		}
	}
	latest := map[string]d.OperationFact{}
	err = records("operation_fact", "checked_at,operation_id", func(raw []byte) error {
		var fact d.OperationFact
		if s.decode(raw, &fact) != nil || !fact.Valid() || fact.Binding != s.binding || fact.CheckedAt.After(o.Now) {
			return d.Fail("OPERATION_RECORD_INVALID", 503)
		}
		key := fact.WorkerKind + ":" + fact.Component
		if fact.SourceID != nil {
			key += ":" + *fact.SourceID
		}
		latest[key] = fact
		if fact.SourceID != nil && fact.Component == "SOURCE" {
			for i := range state.Sources {
				source := &state.Sources[i]
				if source.SourceID == *fact.SourceID {
					at := fact.CheckedAt
					if fact.Success {
						source.LastSuccessAt = &at
					} else {
						code := fact.Code
						source.LastFailureAt = &at
						source.LastFailureCode = &code
					}
				}
			}
		}
		code := fact.Code
		state.Audit = append(state.Audit, d.Audit{AuditID: fact.ID, RecordedAt: fact.CheckedAt, Action: fact.Component + "_CHECK", Code: &code, JobID: fact.JobID})
		return nil
	})
	if err != nil {
		return state, nil, err
	}
	err = records("framework_report", "recorded_at,report_id", func(raw []byte) error {
		var report d.FrameworkReport
		if s.decode(raw, &report) != nil || !report.Valid() || report.Binding != s.binding {
			return d.Fail("REPORT_RECORD_INVALID", 503)
		}
		state.Reports = append(state.Reports, report.View)
		return nil
	})
	if err != nil {
		return state, nil, err
	}
	rows, err := tx.Query(ctx, "SELECT queue_kind,policy_ref,max_jobs,used_jobs,max_concurrent,valid_from,valid_until FROM "+s.schema+".budget WHERE instance_id=$1 AND valid_from<=$2 AND valid_until>$2 ORDER BY queue_kind,bucket LIMIT $3", s.binding.InstanceID, o.Now, o.MaxRecords+1)
	if err != nil {
		return state, nil, safe(err)
	}
	seen := map[string]bool{}
	for rows.Next() {
		var kind, policy string
		var max, used, concurrency int64
		var from, to time.Time
		if err = rows.Scan(&kind, &policy, &max, &used, &concurrency, &from, &to); err != nil {
			rows.Close()
			return state, nil, safe(err)
		}
		if seen[kind] {
			rows.Close()
			return state, nil, d.Fail("INSTANCE_ACTIVE_BUDGET_AMBIGUOUS", 503)
		}
		seen[kind] = true
		// pgx may scan timestamptz in the host's local zone. Public DTOs always
		// encode UTC regardless of database/session/host timezone.
		from = from.UTC()
		to = to.UTC()
		limit, consumed, unit := strconv.FormatInt(max, 10), strconv.FormatInt(used, 10), "jobs"
		reserved := int(concurrency)
		at := o.Now
		state.Budgets = append(state.Budgets, d.Budget{QueueKind: kind, PolicyVersion: &policy, Limit: &limit, Consumed: &consumed, ReservedCapacity: &reserved, Unit: &unit, WindowStart: &from, WindowEnd: &to, ObservedAt: &at})
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return state, nil, safe(err)
	}
	provider := "UNKNOWN"
	if o.FixtureOnly {
		provider = "EXAMPLE_OR_MOCK"
	}
	at := o.Now
	state.Health = &d.Health{Stage: o.Stage, Degradation: []string{"LIVE_ADMISSION_UNRESOLVED"}, RecoveryState: "NOT_RECORDED", CheckedAt: &at, Capabilities: []d.Capability{{Name: "provider", State: provider}, {Name: "production_calibration", State: "UNKNOWN"}, {Name: "live_admission", State: "UNAVAILABLE"}}}
	keys := []string{}
	for key := range latest {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		fact := latest[key]
		if !fact.Success {
			code := fact.Component + "_DEGRADED"
			if !d.Has(state.Health.Degradation, code) {
				state.Health.Degradation = append(state.Health.Degradation, code)
			}
		}
	}
	if state.RecordCount() > o.MaxRecords {
		return state, nil, d.Fail("INSTANCE_PROJECTION_RECORD_BUDGET_EXCEEDED", 503)
	}
	if err = state.Validate(); err != nil {
		return state, nil, err
	}
	raw, err := s.encode(state)
	if err != nil || len(raw) > o.MaxBytes {
		return state, nil, d.Fail("INSTANCE_PROJECTION_BYTE_BUDGET_EXCEEDED", 503)
	}
	if err = s.VerifyRole(ctx); err != nil {
		return state, nil, err
	}
	return state, originals, safe(tx.Commit(ctx))
}

// PublishPipeline is a separate operator operation. The read API and analysis
// workers never hold this publication connection or call Initialize/Publish.
func PublishPipeline(ctx context.Context, dsn string, state d.Snapshot, originals map[string][]byte, expected *int64) error {
	if err := state.Validate(); err != nil {
		return err
	}
	payload, err := json.Marshal(state)
	if err != nil {
		return d.Fail("INSTANCE_RECORD_INVALID", 422)
	}
	name, err := schema(state.Binding)
	if err != nil {
		return err
	}
	conn, err := pgx.Connect(ctx, dsn)
	if err != nil {
		return safe(err)
	}
	defer conn.Close(ctx)
	tx, err := conn.Begin(ctx)
	if err != nil {
		return safe(err)
	}
	defer tx.Rollback(ctx)
	var tagRows int64
	if expected == nil {
		if state.Version != 0 {
			return d.Fail("INSTANCE_VERSION_CONFLICT", 409)
		}
		tag, err := tx.Exec(ctx, "INSERT INTO "+name+".instance_read_snapshot VALUES($1,$2,$3,$4) ON CONFLICT DO NOTHING", state.Binding.InstanceID, state.Binding.Environment, state.Version, payload)
		if err != nil {
			return safe(err)
		}
		tagRows = tag.RowsAffected()
	} else {
		if *expected < 0 || state.Version != *expected+1 {
			return d.Fail("INSTANCE_VERSION_CONFLICT", 409)
		}
		tag, err := tx.Exec(ctx, "UPDATE "+name+".instance_read_snapshot SET version=$3,snapshot=$4 WHERE instance_id=$1 AND environment=$2 AND version=$5", state.Binding.InstanceID, state.Binding.Environment, state.Version, payload, *expected)
		if err != nil {
			return safe(err)
		}
		tagRows = tag.RowsAffected()
	}
	if tagRows != 1 {
		return d.Fail("INSTANCE_VERSION_CONFLICT", 409)
	}
	for _, e := range state.Evidence {
		content, ok := originals[e.EvidenceID]
		if !ok || d.ContentDigest(content) != e.ContentHash {
			return d.Fail("INSTANCE_ORIGINAL_MANIFEST_INVALID", 422)
		}
		if _, err = tx.Exec(ctx, "INSERT INTO "+name+".raw_evidence_content VALUES($1,$2,$3,$4) ON CONFLICT DO NOTHING", state.Binding.InstanceID, e.EvidenceID, e.ContentHash, content); err != nil {
			return safe(err)
		}
		var stored []byte
		var hash string
		if err = tx.QueryRow(ctx, "SELECT content_hash,content FROM "+name+".raw_evidence_content WHERE instance_id=$1 AND evidence_id=$2", state.Binding.InstanceID, e.EvidenceID).Scan(&hash, &stored); err != nil {
			return safe(err)
		}
		if hash != e.ContentHash || !bytes.Equal(stored, content) {
			return d.Fail("INSTANCE_ORIGINAL_IMMUTABLE", 409)
		}
	}
	return safe(tx.Commit(ctx))
}
