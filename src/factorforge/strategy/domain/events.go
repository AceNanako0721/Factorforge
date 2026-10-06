package domain

import "time"

func FactKey(c Claim) string {
	return Digest([]any{c.SubjectID, c.EconomicItem, c.Period, c.NormalizedFact, c.NumbersWithUnits})
}
func VerifyEvent(event *Event, previous []*Event, now time.Time) {
	evidence := map[string]EvidenceRef{}
	for _, e := range event.EvidenceRefs {
		evidence[e.EvidenceID] = e
	}
	event.State = "QUARANTINED"
	if len(event.Claims) == 0 || len(evidence) == 0 {
		return
	}
	identity := map[string]bool{}
	for _, c := range event.Claims {
		if c.SubjectID != event.SubjectID || c.VerifiedAt.After(now) {
			return
		}
		for _, id := range c.EvidenceRefs {
			e, ok := evidence[id]
			if !ok || e.AvailableAt.After(c.VerifiedAt) {
				return
			}
		}
		identity[Digest([]string{c.SubjectID, c.EconomicItem, c.Period})] = true
	}
	if len(identity) != 1 {
		return
	}
	seen := map[string]bool{}
	for _, peer := range previous {
		if peer.FamilyID != event.FamilyID {
			continue
		}
		ids := map[string]bool{}
		for _, c := range peer.Claims {
			ids[Digest([]string{c.SubjectID, c.EconomicItem, c.Period})] = true
			seen[FactKey(c)] = true
		}
		if Digest(ids) != Digest(identity) {
			return
		}
	}
	unique := Ordered[Claim]{}
	for _, c := range event.Claims {
		unique.Set(FactKey(c), c)
	}
	m := Math()
	total, novel := Zero(), Zero()
	for _, key := range unique.Keys() {
		c := unique.Value(key)
		total = m.Add(total, c.Weight)
		if !seen[key] {
			novel = m.Add(novel, c.Weight)
		}
	}
	event.Novelty = m.Div(novel, total)
	if m.Err() != nil {
		return
	}
	event.State = "VERIFIED"
	if event.Relation == "RETRACTION" {
		event.State = "RETRACTED"
	}
}
func ScoreAmount(score *ScoreSubmission, event *Event, params *ParameterSnapshot, policy *Policy, p Decimal) (Decimal, Decimal, Decimal, error) {
	h := params.Values["h"]
	if policy.EventHalfLifeApproved {
		if score.Vector.ExpectedHalfLife == nil {
			return Zero(), Zero(), Zero(), &Error{"HALF_LIFE_NOT_VALIDATED", 422}
		}
		h = *score.Vector.ExpectedHalfLife
	}
	if h.Cmp(policy.HalfLifeBounds[0]) < 0 || h.Cmp(policy.HalfLifeBounds[1]) > 0 {
		return Zero(), Zero(), Zero(), &Error{"HALF_LIFE_NOT_VALIDATED", 422}
	}
	v := score.Vector
	if v.Credibility == nil || v.Relevance == nil || v.ExpectationCoverage == nil {
		return Zero(), Zero(), Zero(), &Error{"SCORE_INCOMPLETE", 422}
	}
	m := Math()
	u := m.Mul(m.Sub(One(), *v.ExpectationCoverage), m.Sub(One(), p))
	amount := m.Mul(m.Mul(m.Mul(v.ImpactPoints, *v.Credibility), *v.Relevance), event.Novelty)
	amount = m.Mul(m.Mul(m.Mul(amount, u), params.Values["w"]), params.Values["g"])
	return Min(policy.EventCap, amount), h, amount, m.Err()
}
func Prepricing(direction int, current, pre, benchmark, beta, rho Decimal) (Decimal, error) {
	m := Math()
	move := m.Sub(m.Ln(m.Div(current, pre)), m.Mul(beta, benchmark))
	v := Min(One(), m.Div(Max(Zero(), m.Mul(Int(direction), move)), rho))
	return v, m.Err()
}
func AssessPrepricing(score *ScoreSubmission, manifest map[string]any, p *Policy, now time.Time) (assessment map[string]any, reason string) {
	reason = "PREPRICING_EVIDENCE_UNVERIFIED"
	required := []string{"validated", "input_manifest_hash", "calibration_version", "verification_manifest", "available_at", "training_end", "pre_at", "price_available_at", "pre_price", "available_price", "benchmark_return", "beta", "rho", "expectation_coverage", "expectation_evidence_refs", "price_evidence_refs", "coverage_mode", "licence_refs"}
	for _, key := range required {
		if _, ok := manifest[key]; !ok {
			return
		}
	}
	if !Flag(manifest["validated"]) {
		return
	}
	err := Guard(func() error {
		if Text(manifest["input_manifest_hash"]) != score.InputManifestHash || Text(manifest["calibration_version"]) != score.CalibrationVersion || !Has(p.VerificationManifests, Text(manifest["verification_manifest"])) || len(Strings(manifest["licence_refs"])) == 0 || At(manifest["available_at"]).After(now) || At(manifest["training_end"]).After(At(manifest["pre_at"])) || At(manifest["pre_at"]).After(At(manifest["price_available_at"])) || At(manifest["price_available_at"]).After(score.CompletedAt) || Amount(manifest["beta"]).Cmp(p.Beta) != 0 || score.Vector.ExpectationCoverage == nil || Amount(manifest["expectation_coverage"]).Cmp(*score.Vector.ExpectationCoverage) != 0 {
			reason = "PREPRICING_PROVENANCE_OR_TIME_INVALID"
			return nil
		}
		expected, prices := Strings(manifest["expectation_evidence_refs"]), Strings(manifest["price_evidence_refs"])
		if len(prices) == 0 || score.Vector.ExpectationCoverage.Sign() > 0 && len(expected) == 0 {
			return nil
		}
		if Text(manifest["coverage_mode"]) == "EXPECTATION_COVERED" && Subset(prices, expected) {
			assessment = map[string]any{"fraction": "0", "manifest": Clone(manifest), "reason": "PRICE_ALREADY_COVERED_BY_EXPECTATION"}
			reason = ""
			return nil
		}
		overlap := false
		for _, id := range expected {
			overlap = overlap || Has(prices, id)
		}
		if Text(manifest["coverage_mode"]) != "UNCOVERED_PRICE" || overlap {
			reason = "PREPRICING_EVIDENCE_OVERLAP"
			return nil
		}
		pre, current, rho := Amount(manifest["pre_price"]), Amount(manifest["available_price"]), Amount(manifest["rho"])
		if Min(pre, current, rho).Sign() <= 0 {
			reason = "PREPRICING_CALIBRATION_INVALID"
			return nil
		}
		benchmark := Zero()
		if manifest["benchmark_return"] == nil {
			if !p.AbsolutePriceProxyValidated {
				reason = "PREPRICING_BENCHMARK_UNKNOWN"
				return nil
			}
		} else {
			benchmark = Amount(manifest["benchmark_return"])
		}
		v, e := Prepricing(score.Vector.Direction, current, pre, benchmark, p.Beta, rho)
		if e != nil {
			return e
		}
		assessment = map[string]any{"fraction": v.String(), "manifest": Clone(manifest), "reason": "VERIFIED_CAUSAL_PRICE_WINDOW"}
		reason = ""
		return nil
	})
	if err != nil {
		assessment = nil
		reason = "PREPRICING_CALIBRATION_INVALID"
	}
	return
}
func ValidManifest(manifest map[string]any, purpose string, at time.Time) bool {
	for _, key := range []string{"validated", "purpose", "available_at", "missing_policy", "validation_manifest", "dependencies"} {
		if _, ok := manifest[key]; !ok {
			return false
		}
	}
	valid := false
	_ = Guard(func() error {
		valid = Flag(manifest["validated"]) && Text(manifest["purpose"]) == purpose && Text(manifest["validation_manifest"]) != "" && Has([]string{"BLOCK", "UNKNOWN", "VALIDATED_PROXY"}, Text(manifest["missing_policy"])) && !At(manifest["available_at"]).After(at)
		return nil
	})
	return valid
}
