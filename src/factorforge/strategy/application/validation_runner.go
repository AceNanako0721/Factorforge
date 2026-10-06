package application

import (
	"context"
	"errors"
	d "github.com/AceNanako0721/Factorforge/src/factorforge/strategy/domain"
)

type Evaluator func(context.Context, map[string][]map[string]any, string, map[string]any) (any, error)

func (s Service) ValidateResearch(ctx context.Context, identity d.Identity, manifest map[string]any, records []map[string]any, evaluate Evaluator) (map[string]any, error) {
	if d.Number(manifest["embargo_seconds"]) < d.Number(manifest["label_span_seconds"])+d.Number(manifest["impact_span_seconds"])+d.Number(manifest["release_delay_seconds"]) {
		return nil, &d.Error{Code: "PURGE_EMBARGO_SPAN_TOO_SHORT", Status: 422}
	}
	err := s.Store.Transaction(ctx, identity.Instance(), func(state *d.StrategyState) error {
		if err := Authorize(state, identity, d.Query, ""); err != nil {
			return err
		}
		row, e := d.RegisterRun(state, manifest, map[string]any{"attempted_at": d.ISO(s.Clock.Now())})
		if e != nil {
			return e
		}
		row["state"] = "RUNNING"
		state.Version++
		return nil
	})
	if err != nil {
		return nil, err
	}
	outcome := map[string]any{}
	status := "COMPLETED"
	err = d.Guard(func() error {
		windows := d.Object(manifest["windows"])
		if d.At(windows["test_end"]).After(s.Clock.Now()) {
			return &d.Error{Code: "VALIDATION_DATA_NOT_YET_AVAILABLE", Status: 422}
		}
		for _, r := range records {
			if d.At(r["available_at"]).After(s.Clock.Now()) {
				return &d.Error{Code: "VALIDATION_DATA_NOT_YET_AVAILABLE", Status: 422}
			}
		}
		parts, e := d.Split(records, d.At(windows["train_end"]), d.At(windows["validation_end"]), d.At(windows["test_end"]), d.Number(manifest["embargo_seconds"]))
		if e != nil {
			return e
		}
		for _, part := range parts {
			if len(part) == 0 {
				return &d.Error{Code: "INDEPENDENT_PARTITIONS_EMPTY", Status: 422}
			}
		}
		results := map[string]any{}
		for _, ablation := range d.Strings(manifest["ablations"]) {
			result, e := evaluate(ctx, parts, ablation, manifest)
			if e != nil {
				return e
			}
			results[ablation] = result
		}
		ids := map[string][]string{}
		for k, part := range parts {
			ids[k] = []string{}
			for _, r := range part {
				ids[k] = append(ids[k], d.Text(r["id"]))
			}
		}
		outcome = map[string]any{"partitions": ids, "ablations": results, "fixed_external_outputs": true, "production_upgrade": false}
		return nil
	})
	if err != nil {
		status = "FAILED"
		code := "EVALUATOR_FAILED"
		var known *d.Error
		if errors.As(err, &known) {
			code = known.Code
		}
		outcome = map[string]any{"failure_code": code, "production_upgrade": false}
	}
	err = s.Store.Transaction(ctx, identity.Instance(), func(state *d.StrategyState) error {
		row := state.ValidationRuns.Value(d.Text(manifest["run_id"]))
		row["result"] = outcome
		row["state"] = status
		state.Audit = append(state.Audit, map[string]any{"action": "VALIDATION_FINISHED", "run_id": manifest["run_id"], "state": status, "at": d.ISO(s.Clock.Now())})
		state.Version++
		return nil
	})
	return outcome, err
}
