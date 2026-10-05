package workers

import (
	"context"
	"errors"
	a "github.com/AceNanako0721/Factorforge/src/factorforge/trading/application"
	d "github.com/AceNanako0721/Factorforge/src/factorforge/trading/domain"
	"github.com/AceNanako0721/Factorforge/src/factorforge/trading/domain/decimal"
	"github.com/AceNanako0721/Factorforge/src/factorforge/trading/ports"
	"strconv"
)

type Feedback struct {
	Store     ports.Store
	Broker    ports.Broker
	Tolerance decimal.Value
}

func (w *Feedback) Tick(ctx context.Context, key d.RunKey) (bool, error) {
	_, err := a.Synchronize(ctx, w.Store, key, w.Broker, w.Tolerance, false)
	if err == nil {
		return true, nil
	}
	var failure *d.Error
	if errors.As(err, &failure) && failure.Code == "VENUE_SNAPSHOT_CHANGED" {
		return false, nil
	}
	if errors.As(err, &failure) && failure.Code == "EXECUTOR_ENVIRONMENT_MISMATCH" {
		return false, err
	}
	err = w.Store.Transaction(ctx, key, func(run *d.Aggregate) error {
		run.State = "RECOVERY_CHECK"
		run.VenueReconciledVersion = nil
		run.Alerts = append(run.Alerts, d.Alert("VENUE_FEEDBACK_UNAVAILABLE", run.Clock))
		run.Version++
		a.Audit(run, "VENUE_FEEDBACK_UNAVAILABLE", d.Principal{PrincipalID: "venue-reader", Environment: key.Environment, AccountID: key.AccountID, Permissions: []string{}}, "feedback-"+strconv.FormatInt(run.Version, 10), a.Response{"state": run.State})
		return nil
	})
	return false, err
}
