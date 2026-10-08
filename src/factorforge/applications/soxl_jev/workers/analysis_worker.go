// Package workers coordinates persisted analysis and deterministic submission.
// Model output never becomes an order or a framework clock command.
package workers

import (
	"context"
	"errors"
	a "github.com/AceNanako0721/Factorforge/src/factorforge/applications/soxl_jev/analysis"
	d "github.com/AceNanako0721/Factorforge/src/factorforge/applications/soxl_jev/domain"
	"github.com/AceNanako0721/Factorforge/src/factorforge/applications/soxl_jev/operations"
	"github.com/AceNanako0721/Factorforge/src/factorforge/applications/soxl_jev/ports"
	dto "github.com/AceNanako0721/Factorforge/src/factorforge/strategy/api/dto"
	"time"
)

type AnalysisWorker struct {
	Store       ports.AnalysisStore
	Framework   ports.FrameworkClient
	Provider    ports.JevDecisionProvider
	Clock       ports.Clock
	WorkerID    string
	Lease       time.Duration
	AllowMock   bool
	MaxOutboxes int
	Operations  ports.OperationStore
}

func (w AnalysisWorker) Validate() error {
	if w.Store == nil || w.Framework == nil || w.Provider == nil || w.Clock == nil || !d.ValidID(w.WorkerID) || w.Lease <= 0 || w.MaxOutboxes <= 0 ||
		w.Store.Binding() != w.Framework.Binding() || w.Framework.ResearchOnly() != (w.Store.Kind() == "RESEARCH") {
		return d.Fail("ANALYSIS_WORKER_CONFIGURATION_REQUIRED", 503)
	}
	if w.Store.Kind() == "LIVE" {
		return d.Fail("LIVE_ADMISSION_REQUIRED", 423)
	}
	return nil
}
func (w AnalysisWorker) ProcessOne(ctx context.Context) (bool, error) {
	if err := w.Validate(); err != nil {
		return false, err
	}
	job, err := w.Store.Claim(ctx, w.WorkerID, w.Clock.Now().UTC(), w.Lease)
	if err != nil || job == nil {
		return false, err
	}
	now := w.Clock.Now().UTC()
	reject := func(state, reason string) error {
		return w.Store.Complete(ctx, *job, state, []string{reason}, w.Clock.Now().UTC())
	}
	r := job.Request
	if r.Binding != w.Store.Binding() || r.Routing.Binding != r.Binding || r.ObjectID != r.Routing.ObjectID ||
		r.Routing.ManifestHash != d.Digest(r.Evidence) || r.RequestID != job.JobID || r.Deadline != job.Deadline ||
		job.QueueKind != "RESEARCH" && r.Routing.Route != "TRADING_CANDIDATE" || r.Routing.Route == "QUARANTINE" {
		return true, reject("FAILED", "ROUTING_BINDING_INVALID")
	}
	if !now.Before(job.Deadline) {
		return true, reject("EXPIRED", "TASK_EXPIRED")
	}
	if r.Evidence.Raw.FirstPublicAt == nil {
		return true, reject("ABSTAINED", "PUBLICATION_TIME_UNVERIFIED")
	}
	if r.Event.FirstPublicAt != *r.Evidence.Raw.FirstPublicAt || r.Event.FactVersion < 1 || !d.Has(r.Event.ObjectIDs, r.ObjectID) {
		return true, reject("FAILED", "EVENT_BINDING_INVALID")
	}
	version, err := w.Framework.Version(ctx, r.ObjectID)
	if err != nil {
		return true, reject("FAILED", "FRAMEWORK_UNAVAILABLE")
	}
	eventKey := "event-" + d.Digest([]any{r.Binding, r.Event.EventID, r.Event.FactVersion, r.Event.Relation, r.Evidence.Raw.ContentHash})
	eventCommand := dto.EventCommand{Command: dto.Command{SchemaVersion: "strategy-2.0", RequestID: eventKey, IdempotencyKey: eventKey, ExpectedVersion: version, Reason: "VERIFIED_INSTANCE_EVIDENCE"}, Event: r.Event}
	if w.Framework.ResearchOnly() {
		exists, e := w.Framework.EventExists(ctx, r.Event.EventID, r.ObjectID, r.Event.FactVersion)
		if e != nil || !exists {
			return true, reject("FAILED", "FRAMEWORK_EVENT_NOT_RECORDED")
		}
	} else if _, err = w.Framework.RegisterEvent(ctx, eventCommand, r.Event.FactVersion > 1); err != nil {
		return true, reject("FAILED", "FRAMEWORK_EVENT_NOT_ACCEPTED")
	}
	if job.Candidate == nil {
		if err = w.Store.StartProvider(ctx, *job, w.Clock.Now().UTC()); err != nil {
			return true, err
		}
		until := job.Deadline
		if job.LeaseUntil != nil && job.LeaseUntil.Before(until) {
			until = *job.LeaseUntil
		}
		callCtx, cancel := context.WithDeadline(ctx, until)
		candidate, e := w.Provider.Analyze(callCtx, r)
		cancel()
		if w.Operations != nil {
			kind := "TRADING"
			if job.QueueKind == "RESEARCH" {
				kind = "RESEARCH"
			}
			if recordErr := operations.RecordFact(ctx, w.Operations, r.Binding, kind, "PROVIDER", nil, &job.JobID, w.Clock.Now(), e); recordErr != nil {
				return true, recordErr
			}
		}
		if e != nil {
			return true, reject("FAILED", "PROVIDER_UNAVAILABLE_OR_UNKNOWN")
		}
		if e = w.Store.SaveCandidate(ctx, *job, candidate, w.Clock.Now().UTC()); e != nil {
			return true, e
		}
		job.Candidate = &candidate
	}
	if err = a.Validate(r, *job.Candidate, w.Clock.Now().UTC(), w.AllowMock); err != nil {
		return true, reject("ABSTAINED", "CANDIDATE_NOT_ADMITTED")
	}
	version, err = w.Framework.Version(ctx, r.ObjectID)
	if err != nil {
		return true, reject("FAILED", "FRAMEWORK_UNAVAILABLE")
	}
	candidate := *job.Candidate
	id := "score-" + d.Digest([]any{r.Binding, r.ObjectID, r.Event.EventID, r.Event.FactVersion, r.QuestionSetVersion, r.RubricVersion, r.CalibrationVersion, r.PromptVersion, r.RevisionKind, r.ScoreVersion})
	score := dto.ScoreSubmission{SubmissionID: id, EventID: r.Event.EventID, FactVersion: r.Event.FactVersion, ObjectID: r.ObjectID,
		ScoreVersion: r.ScoreVersion, PreviousScoreID: r.PreviousScoreID, RevisionKind: r.RevisionKind, Vector: candidate.Vector,
		EvidenceRefs: []string{r.Evidence.Raw.EvidenceID}, ProducerID: "soxl-jev-instance", ProducerVersion: candidate.ProducerVersion,
		RubricVersion: r.RubricVersion, CalibrationVersion: r.CalibrationVersion, CompletedAt: candidate.CompletedAt, InputManifestHash: r.Routing.ManifestHash}
	command := dto.ScoreCommand{Command: dto.Command{SchemaVersion: "strategy-2.0", RequestID: id, IdempotencyKey: id, ExpectedVersion: version, Reason: "VALIDATED_INSTANCE_ANALYSIS"}, Score: score}
	row := d.SubmissionOutbox{OutboxID: id, Binding: r.Binding, JobID: job.JobID, QueueKind: job.QueueKind, Command: command,
		CandidateHash: d.Digest(candidate), CreatedAt: w.Clock.Now().UTC(), ExpiresAt: job.Deadline, DeliveryState: "PENDING"}
	return true, w.Store.SaveOutbox(ctx, *job, row, w.Clock.Now().UTC())
}
func (w AnalysisWorker) Dispatch(ctx context.Context) error {
	if err := w.Validate(); err != nil {
		return err
	}
	rows, err := w.Store.Outboxes(ctx, w.MaxOutboxes)
	if err != nil {
		return err
	}
	for _, row := range rows {
		if row.Binding != w.Store.Binding() || row.QueueKind != w.Store.Kind() {
			return d.Fail("OUTBOX_BINDING_FORBIDDEN", 403)
		}
		if !w.Clock.Now().UTC().Before(row.ExpiresAt) {
			if err = w.Store.SetDelivery(ctx, row, "EXPIRED", nil); err != nil {
				return err
			}
			continue
		}
		// A missing response is reconciled against recorded framework receipts before
		// repeating the exact command/key. No new model call or score ID is created.
		receipt, e := w.Framework.Receipt(ctx, row.Command.Score.EventID, row.Command.Score.ObjectID, row.OutboxID)
		if e != nil {
			return e
		}
		if receipt != nil {
			if err = w.Store.SetDelivery(ctx, row, "ACK", receipt); err != nil {
				return err
			}
			continue
		}
		received, e := w.Framework.Submit(ctx, row.Command)
		if w.Operations != nil {
			kind := "TRADING"
			if row.QueueKind == "RESEARCH" {
				kind = "RESEARCH"
			}
			if recordErr := operations.RecordFact(ctx, w.Operations, row.Binding, kind, "SUBMISSION", nil, &row.JobID, w.Clock.Now(), e); recordErr != nil {
				return recordErr
			}
		}
		if e != nil {
			state := "DELIVERY_UNKNOWN"
			var known *d.Error
			if errors.As(e, &known) && known.Status >= 400 && known.Status < 500 {
				state = "REJECTED"
			}
			if err = w.Store.SetDelivery(ctx, row, state, nil); err != nil {
				return err
			}
			continue
		}
		if err = w.Store.SetDelivery(ctx, row, "ACK", &received); err != nil {
			return err
		}
	}
	return nil
}
