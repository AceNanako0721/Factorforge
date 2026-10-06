package application

import (
	"context"
	"fmt"
	d "github.com/AceNanako0721/Factorforge/src/factorforge/strategy/domain"
	"github.com/AceNanako0721/Factorforge/src/factorforge/strategy/ports"
)

func Authorize(s *d.StrategyState, identity d.Identity, scope d.ApiScope, objectID string) error {
	if identity == nil || identity.Instance() != s.InstanceID || identity.Env() != s.Environment {
		return &d.Error{Code: "IDENTITY_BINDING_FORBIDDEN", Status: 403}
	}
	if worker := identity.Worker(); worker != nil {
		capability := d.SignalSIM
		if s.Environment == "LIVE" {
			capability = d.SignalLIVE
		}
		allowed := false
		for _, v := range worker.Capabilities {
			allowed = allowed || v == capability
		}
		if !allowed || objectID != "" && !d.Has(worker.ObjectIDs, objectID) {
			return &d.Error{Code: "WORKLOAD_CAPABILITY_FORBIDDEN", Status: 403}
		}
		return nil
	}
	public, ok := identity.(d.PublicPrincipal)
	if !ok {
		return &d.Error{Code: "IDENTITY_UNKNOWN", Status: 403}
	}
	for _, v := range public.Scopes {
		if v == scope {
			return nil
		}
	}
	return &d.Error{Code: "PUBLIC_SCOPE_FORBIDDEN", Status: 403}
}

type Service struct {
	Store ports.Store
	Clock ports.Clock
}

func (s Service) Command(ctx context.Context, identity d.Identity, command d.Command, scope d.ApiScope, payload any, objectID string, fn func(*d.StrategyState) (any, error)) (any, error) {
	var result any
	err := s.Store.Transaction(ctx, identity.Instance(), func(state *d.StrategyState) error {
		if err := Authorize(state, identity, scope, objectID); err != nil {
			return err
		}
		if w := identity.Worker(); w != nil && objectID != "" {
			if err := s.Store.CheckWorkload(ctx, *w, objectID); err != nil {
				return err
			}
		}
		hash := d.Digest(map[string]any{"identity": identity.Payload(), "scope": d.ScopeName(scope), "payload": payload})
		if old, ok := state.Dedup.Get(command.IdempotencyKey); ok {
			if d.Text(old["hash"]) != hash {
				return &d.Error{Code: "IDEMPOTENCY_CONFLICT", Status: 409}
			}
			result = d.Clone(old["result"])
			return nil
		}
		if command.ExpectedVersion != state.Version {
			return &d.Error{Code: "AGGREGATE_VERSION_CONFLICT", Status: 409}
		}
		value, err := fn(state)
		if err != nil {
			return err
		}
		result = d.JSONValue(value)
		state.Version++
		state.Dedup.Set(command.IdempotencyKey, map[string]any{"hash": hash, "result": result})
		state.Audit = append(state.Audit, map[string]any{"sequence": len(state.Audit) + 1, "action": d.ScopeName(scope), "request_id": command.RequestID, "identity": identity.Payload(), "at": d.ISO(s.Clock.Now()), "reason": command.Reason})
		return nil
	})
	return result, err
}
func (s Service) Create(ctx context.Context, identity d.Identity, request d.CreateObject) (any, error) {
	obj, p, params := request.Object, request.Policy, request.Parameters
	return s.Command(ctx, identity, request.Command, d.ObjectWrite, map[string]any{"object": obj, "policy": p, "parameters": params}, obj.ObjectID, func(state *d.StrategyState) (any, error) {
		if _, ok := state.Objects.Get(obj.ObjectID); ok {
			return nil, &d.Error{Code: "OBJECT_OWNER_CONFLICT", Status: 409}
		}
		for _, other := range state.Objects.Values() {
			if other.TradingRunKey == obj.TradingRunKey && (other.OwnerID == obj.OwnerID || other.InstrumentKey == obj.InstrumentKey) {
				return nil, &d.Error{Code: "OBJECT_OWNER_CONFLICT", Status: 409}
			}
		}
		if obj.InstanceID != state.InstanceID || obj.Environment != state.Environment || obj.TradingRunKey.Environment != state.Environment {
			return nil, &d.Error{Code: "OBJECT_BINDING_FORBIDDEN", Status: 403}
		}
		if obj.ParameterVersion != params.Version || obj.TimePolicyVersion != p.Version || !d.Has([]string{obj.ObjectID, "*"}, params.Scope) {
			return nil, &d.Error{Code: "POLICY_BINDING_CONFLICT", Status: 409}
		}
		if identity.Worker() == nil && (d.Digest(state.Parameters.Value(params.Version)) != d.Digest(params) || d.Digest(state.Policies.Value(p.Version)) != d.Digest(p)) {
			return nil, &d.Error{Code: "OPERATOR_REGISTRY_REQUIRED", Status: 403}
		}
		if params.QualityState == "VALIDATED" {
			for _, key := range []string{"w", "g", "h", "eta", "kappa", "k_stop"} {
				if params.Values[key].Sign() <= 0 {
					return nil, &d.Error{Code: "PARAMETERS_REQUIRED_UNSET", Status: 423}
				}
			}
		}
		if state.Environment == "LIVE" && (params.QualityState != "VALIDATED" || p.QualityState != "VALIDATED") {
			return nil, &d.Error{Code: "PRODUCTION_CALIBRATION_UNSET", Status: 423}
		}
		if params.ValidFrom.After(s.Clock.Now()) {
			return nil, &d.Error{Code: "PARAMETERS_NOT_YET_AVAILABLE", Status: 422}
		}
		obj.AggregateVersion = state.Version + 1
		obj.OwnerEpoch = 0
		obj.Level = 0
		obj.Direction = 0
		obj.ActualQuantity = d.Zero()
		obj.PendingQuantity = d.Zero()
		obj.LastCycle = nil
		obj.LastCutoff = nil
		obj.LastAdjustment = nil
		obj.RecoveryState = "RECOVERY_CHECK"
		if v, ok := state.Policies.Get(p.Version); ok && d.Digest(v) != d.Digest(p) {
			return nil, &d.Error{Code: "IMMUTABLE_VERSION_CONFLICT", Status: 409}
		}
		if v, ok := state.Parameters.Get(params.Version); ok && d.Digest(v) != d.Digest(params) {
			return nil, &d.Error{Code: "IMMUTABLE_VERSION_CONFLICT", Status: 409}
		}
		state.Objects.Set(obj.ObjectID, d.Clone(&obj))
		state.Policies.Set(p.Version, d.Clone(&p))
		state.Parameters.Set(params.Version, d.Clone(&params))
		return obj, nil
	})
}
func (s Service) SetState(ctx context.Context, identity d.Identity, request d.StateCommand, id string) (any, error) {
	return s.Command(ctx, identity, request.Command, d.ObjectWrite, []string{id, request.State}, id, func(state *d.StrategyState) (any, error) {
		obj := state.Objects.Value(id)
		if obj == nil {
			return nil, &d.Error{Code: "OBJECT_NOT_FOUND", Status: 404}
		}
		if request.State == "ARCHIVED" {
			blocked := obj.ActualQuantity.Sign() != 0 || obj.PendingQuantity.Sign() != 0
			for _, c := range state.Cases.Values() {
				blocked = blocked || c.ObjectID == id && d.Has([]string{"OPEN", "OBSERVING"}, c.Status)
			}
			if blocked {
				return nil, &d.Error{Code: "OBJECT_HAS_UNSETTLED_CASES", Status: 409}
			}
		}
		obj.State = request.State
		obj.AggregateVersion = state.Version + 1
		return obj, nil
	})
}
func (s Service) RegisterEvent(ctx context.Context, identity d.Identity, request d.EventCommand) (any, error) {
	event := d.Clone(request.Event)
	return s.Command(ctx, identity, request.Command, d.Research, event, "", func(state *d.StrategyState) (any, error) {
		if !d.Subset(event.ObjectIDs, state.Objects.Keys()) {
			return nil, &d.Error{Code: "OBJECT_NOT_FOUND", Status: 404}
		}
		if w := identity.Worker(); w != nil {
			if !d.Subset(event.ObjectIDs, w.ObjectIDs) {
				return nil, &d.Error{Code: "WORKLOAD_OBJECT_SCOPE_FORBIDDEN", Status: 403}
			}
			for _, id := range event.ObjectIDs {
				if e := s.Store.CheckWorkload(ctx, *w, id); e != nil {
					return nil, e
				}
			}
		}
		previous := state.Events.Value(event.EventID)
		if previous != nil {
			if event.FactVersion != previous.FactVersion+1 || event.Relation == "NEW" {
				return nil, &d.Error{Code: "FACT_VERSION_CONFLICT", Status: 409}
			}
		} else if event.FactVersion != 1 {
			return nil, &d.Error{Code: "FACT_VERSION_CONFLICT", Status: 409}
		}
		if event.Relation == "NEW_FACT" && (event.ParentEventID == nil || state.Events.Value(*event.ParentEventID) == nil || *event.ParentEventID == event.EventID) {
			return nil, &d.Error{Code: "NEW_FACT_PARENT_REQUIRED", Status: 422}
		}
		for _, peer := range state.Events.Values() {
			if peer.FamilyID != event.FamilyID {
				for _, c := range event.Claims {
					for _, other := range peer.Claims {
						if c.NormalizedFact == other.NormalizedFact && c.SubjectID == other.SubjectID && c.EconomicItem == other.EconomicItem && c.Period == other.Period {
							return nil, &d.Error{Code: "FACT_FAMILY_CONFLICT", Status: 409}
						}
					}
				}
			}
		}
		d.VerifyEvent(&event, state.Events.Values(), s.Clock.Now())
		if len(event.ObjectIDs) == 0 {
			event.State = "QUARANTINED"
		}
		for _, id := range event.ObjectIDs {
			p := state.Policies.Value(state.Objects.Value(id).TimePolicyVersion)
			for _, c := range event.Claims {
				if !d.Has(p.Rubrics, c.VerificationManifest) || p.ClaimWeights[c.EconomicItem].Cmp(c.Weight) != 0 {
					event.State = "QUARANTINED"
				}
			}
			for _, e := range event.EvidenceRefs {
				if !d.Has(p.VerificationManifests, e.VerificationRef) {
					event.State = "QUARANTINED"
				}
			}
		}
		if previous != nil && d.Has([]string{"CONFIRMATION", "CORRECTION"}, event.Relation) {
			event.Novelty = previous.Novelty
			if event.Relation == "CONFIRMATION" {
				keys, prior := []string{}, []string{}
				for _, c := range event.Claims {
					keys = append(keys, d.FactKey(c))
				}
				for _, c := range previous.Claims {
					prior = append(prior, d.FactKey(c))
				}
				if d.Digest(d.Unique(keys)) != d.Digest(d.Unique(prior)) {
					event.State = "QUARANTINED"
				}
			}
		}
		state.Events.Set(event.EventID, &event)
		version := fmt.Sprintf("%s:%d", event.EventID, event.FactVersion)
		state.EventVersions.Set(version, d.Clone(&event))
		state.EventReceived.Set(version, s.Clock.Now())
		if event.State == "RETRACTED" && identity.Worker() != nil {
			for _, c := range state.Contributions.Values() {
				if c.EventID == event.EventID && c.State == "ACTIVE" {
					if err := state.Append(c, d.LedgerEntry{At: s.Clock.Now(), Reason: "RETRACTED", Invalidation: c.RemainingAmount}); err != nil {
						return nil, err
					}
					c.State = "INVALID"
				}
			}
		}
		return event, nil
	})
}
func (s Service) Submit(ctx context.Context, identity d.Identity, request d.ScoreCommand) (any, error) {
	score := d.Clone(request.Score)
	return s.Command(ctx, identity, request.Command, d.Research, score, score.ObjectID, func(state *d.StrategyState) (any, error) {
		event, obj := state.Events.Value(score.EventID), state.Objects.Value(score.ObjectID)
		if event == nil || obj == nil {
			return nil, &d.Error{Code: "EVENT_OR_OBJECT_NOT_FOUND", Status: 404}
		}
		if !d.Has(event.ObjectIDs, score.ObjectID) || score.FactVersion != event.FactVersion {
			return nil, &d.Error{Code: "FACT_VERSION_CONFLICT", Status: 409}
		}
		receiptFor := func(id string) *d.AdmissionReceipt {
			if r := state.Receipts.Value(id); r != nil {
				return r
			}
			return state.ResearchReceipts.Value(id)
		}
		if old := state.Scores.Value(score.SubmissionID); old != nil {
			if d.Digest(old) != d.Digest(score) {
				return nil, &d.Error{Code: "SCORE_ID_CONFLICT", Status: 409}
			}
			return receiptFor(score.SubmissionID), nil
		}
		canonical := d.Map(score)
		delete(canonical, "submission_id")
		for _, id := range state.Scores.Keys() {
			old := state.Scores.Value(id)
			_, internal := state.Receipts.Get(id)
			other := d.Map(old)
			delete(other, "submission_id")
			if internal == (identity.Worker() != nil) && d.Digest(other) == d.Digest(canonical) {
				return receiptFor(id), nil
			}
		}
		var prior *d.ScoreSubmission
		if score.PreviousScoreID != nil {
			prior = state.Scores.Value(*score.PreviousScoreID)
		}
		same := []*d.ScoreSubmission{}
		for _, v := range state.Scores.Values() {
			r := state.Receipts.Value(v.SubmissionID)
			if v.EventID == score.EventID && v.ObjectID == score.ObjectID && r != nil && !d.Has([]string{"DRAFT", "RESEARCH_ONLY", "QUARANTINED"}, r.State) {
				same = append(same, v)
			}
		}
		if score.RevisionKind == "REVISION" {
			inSame := false
			for _, v := range same {
				inSame = inSame || v == prior
			}
			if prior == nil || prior.ObjectID != score.ObjectID || prior.EventID != score.EventID || score.ScoreVersion != prior.ScoreVersion+1 || inSame && prior != same[len(same)-1] {
				return nil, &d.Error{Code: "SCORE_REVISION_CONFLICT", Status: 409}
			}
		} else if len(same) > 0 {
			return nil, &d.Error{Code: "EXPLICIT_REVISION_REQUIRED", Status: 409}
		}
		p, params := state.Policies.Value(obj.TimePolicyVersion), state.Parameters.Value(obj.ParameterVersion)
		v := score.Vector
		missing := []string{}
		for name, value := range map[string]*d.Decimal{"credibility": v.Credibility, "relevance": v.Relevance, "novelty": v.Novelty, "expectation_coverage": v.ExpectationCoverage, "prepricing_fraction": v.PrepricingFraction, "expected_half_life": v.ExpectedHalfLife, "quality_score": v.QualityScore} {
			if value == nil {
				missing = append(missing, name)
			}
		}
		status := ""
		reasons := []string{}
		evidenceIDs := []string{}
		for _, e := range event.EvidenceRefs {
			evidenceIDs = append(evidenceIDs, e.EvidenceID)
		}
		switch {
		case len(missing) > 0 || len(v.UnknownFields) > 0:
			status = "DRAFT"
			for _, name := range d.Unique(append(missing, v.UnknownFields...)) {
				reasons = append(reasons, "UNKNOWN:"+name)
			}
		case identity.Worker() == nil:
			status = "RESEARCH_ONLY"
			reasons = []string{"PUBLIC_RESEARCH_IDENTITY"}
		case event.State != "VERIFIED" || !d.Subset(score.EvidenceRefs, evidenceIDs) || len(score.EvidenceRefs) == 0:
			status = "QUARANTINED"
			reasons = []string{"INVALID_EVIDENCE"}
		case !d.Has(p.Calibrations, score.CalibrationVersion) || !d.Has(p.Rubrics, score.RubricVersion):
			status = "QUARANTINED"
			reasons = []string{"UNCALIBRATED_SCORE"}
		case v.ExpectedHalfLife.Cmp(p.HalfLifeBounds[0]) < 0 || v.ExpectedHalfLife.Cmp(p.HalfLifeBounds[1]) > 0:
			status = "QUARANTINED"
			reasons = []string{"HALF_LIFE_NOT_VALIDATED"}
		case p.QualityState != "VALIDATED" || params.QualityState != "VALIDATED":
			status = "RESEARCH_ONLY"
			reasons = []string{"PARAMETERS_UNVALIDATED"}
		default:
			assessment, reason := d.AssessPrepricing(&score, state.FactorManifests.Value("prepricing:"+score.InputManifestHash), p, s.Clock.Now())
			if reason != "" {
				status = "QUARANTINED"
				reasons = []string{reason}
			} else {
				status = "READY_PENDING_PRICE"
				state.PrepricingAssessments.Set(score.SubmissionID, assessment)
			}
		}
		now := s.Clock.Now()
		eligible := now
		if score.CompletedAt.After(eligible) {
			eligible = score.CompletedAt
		}
		for _, e := range event.EvidenceRefs {
			if e.AvailableAt.After(eligible) {
				eligible = e.AvailableAt
			}
		}
		for _, c := range event.Claims {
			if c.VerifiedAt.After(eligible) {
				eligible = c.VerifiedAt
			}
		}
		receipt := &d.AdmissionReceipt{SubmissionID: score.SubmissionID, State: status, EligibleFrom: d.Ptr(eligible), ReasonCodes: reasons, CurrentVersion: state.Version + 1}
		state.Scores.Set(score.SubmissionID, &score)
		if identity.Worker() != nil {
			state.Receipts.Set(score.SubmissionID, receipt)
		} else {
			state.ResearchReceipts.Set(score.SubmissionID, receipt)
		}
		state.Received.Set(score.SubmissionID, now)
		return receipt, nil
	})
}
func (s Service) Attribution(ctx context.Context, identity d.Identity, request d.AttributionCommand, caseID string) (any, error) {
	candidate := d.Map(request.Candidate)
	return s.Command(ctx, identity, request.Command, d.Research, []any{caseID, candidate}, "", func(state *d.StrategyState) (any, error) {
		if state.Cases.Value(caseID) == nil {
			return nil, &d.Error{Code: "CASE_NOT_FOUND", Status: 404}
		}
		id := request.Candidate.CandidateID
		if _, ok := state.Attributions.Get(id); ok {
			return nil, &d.Error{Code: "ATTRIBUTION_ID_CONFLICT", Status: 409}
		}
		candidate["case_id"] = caseID
		candidate["state"] = "UNVERIFIED"
		state.Attributions.Set(id, candidate)
		return candidate, nil
	})
}
