package operations

import (
	"context"
	"errors"
	d "github.com/AceNanako0721/Factorforge/src/factorforge/applications/soxl_jev/domain"
	"github.com/AceNanako0721/Factorforge/src/factorforge/applications/soxl_jev/ports"
	"time"
)

func RecordFact(ctx context.Context, store ports.OperationStore, b d.Binding, kind, component string, source, job *string, at time.Time, result error) error {
	if store == nil {
		return d.Fail("OPERATION_STORE_REQUIRED", 503)
	}
	code := component + "_SUCCEEDED"
	if result != nil {
		code = component + "_UNAVAILABLE"
		var known *d.Error
		if errors.As(result, &known) && d.Codes([]string{known.Code}) {
			code = known.Code
		}
	}
	fact := d.OperationFact{Binding: b, WorkerKind: kind, Component: component, SourceID: source, JobID: job, CheckedAt: at.UTC(), Success: result == nil, Code: code}
	fact.ID = "operation-" + d.Digest(fact)
	return store.RecordOperation(ctx, fact)
}
