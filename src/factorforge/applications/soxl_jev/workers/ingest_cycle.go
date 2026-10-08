package workers

import (
	"context"
	d "github.com/AceNanako0721/Factorforge/src/factorforge/applications/soxl_jev/domain"
	"github.com/AceNanako0721/Factorforge/src/factorforge/applications/soxl_jev/operations"
	"github.com/AceNanako0721/Factorforge/src/factorforge/applications/soxl_jev/ports"
	"sort"
)

type IngestCycle struct {
	Worker  IngestWorker
	Store   ports.OperationStore
	Sources map[string]ports.MonitorSource
	Search  ports.SearchProvider
}

// A failing source does not erase successful polls from independent sources.
// Recording failure halts this bounded iteration; raw error text never persists.
func (c IngestCycle) Run(ctx context.Context, inputs []d.RawEvidence) error {
	if c.Store == nil || c.Worker.Clock == nil {
		return d.Fail("INGEST_CYCLE_CONFIGURATION_REQUIRED", 503)
	}
	var first error
	record := func(component string, source *string, result error) error {
		return operations.RecordFact(ctx, c.Store, c.Worker.Policy.Binding, "INGEST", component, source, nil, c.Worker.Clock.Now(), result)
	}
	ids := []string{}
	for id := range c.Sources {
		if !d.ValidID(id) {
			return d.Fail("SOURCE_ID_INVALID", 422)
		}
		ids = append(ids, id)
	}
	sort.Strings(ids)
	for _, id := range ids {
		rows, err := c.Sources[id].Poll(ctx, c.Worker.Clock.Now().UTC())
		if e := record("SOURCE", &id, err); e != nil {
			return e
		}
		if err != nil {
			if first == nil {
				first = err
			}
			continue
		}
		for _, row := range rows {
			if row.SourceID != id {
				return d.Fail("SOURCE_BINDING_MISMATCH", 403)
			}
		}
		inputs = append(inputs, rows...)
	}
	process := func(raw d.RawEvidence) (error, bool) {
		receipt, err := c.Worker.Process(ctx, raw)
		// Quarantine is an explicit completed routing outcome, not a healthy
		// admitted signal or proof that extraction/calibration was verified.
		status := err
		if err == nil && receipt.Route == "QUARANTINE" {
			status = d.Fail("INPUT_QUARANTINED", 422)
		}
		if e := record("INGEST", &raw.SourceID, status); e != nil {
			return e, true
		}
		return err, false
	}
	for _, raw := range inputs {
		if ctx.Err() != nil {
			return d.Fail("INGEST_ITERATION_EXPIRED", 503)
		}
		if c.Search != nil {
			found, err := c.Search.Search(ctx, raw, c.Worker.Clock.Now().UTC())
			if e := record("SEARCH", &raw.SourceID, err); e != nil {
				return e
			}
			if err != nil {
				if first == nil {
					first = err
				}
			} else {
				for _, additional := range found {
					if e, fatal := process(additional); fatal {
						return e
					} else if e != nil && first == nil {
						first = e
					}
				}
			}
		}
		if err, fatal := process(raw); fatal {
			return err
		} else if err != nil && first == nil {
			first = err
		}
	}
	return first
}
