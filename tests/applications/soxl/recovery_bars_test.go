package soxl_test

import (
	"context"
	"testing"
	"time"

	"github.com/AceNanako0721/Factorforge/src/factorforge/applications/soxl/submission"
	"github.com/AceNanako0721/Factorforge/src/factorforge/applications/soxl/workers"
)

// rangeTrading serves 5-minute closes from a series, honouring the query
// window like P1 does.
type rangeTrading struct {
	*fakeTrading
	series []submission.CandleRead
	starts []time.Time
}

func (r *rangeTrading) Candles(_ context.Context, _, _, _ string, start, end time.Time) ([]submission.CandleRead, error) {
	r.starts = append(r.starts, start)
	out := []submission.CandleRead{}
	for _, b := range r.series {
		if !b.CloseAt.Before(start) && !b.CloseAt.After(end) {
			out = append(out, b)
		}
	}
	return out, nil
}

// After a 3-hour outage the manager folds every missed close into best_close,
// so a +1R move during the outage still starts the trailing stop.
func TestPositionManagerRecoversClosesMissedDuringOutage(t *testing.T) {
	m, store, _, tr, step, at := openLongBatch(t)
	series := &rangeTrading{fakeTrading: tr}
	m.Trading = series
	var st workers.PositionState
	_, _ = store.LoadState(context.Background(), "POSITION", &st)
	last := st.Batch.LastBarAt
	// Outage: 36 closes, peaking at 110 (> +1R with R = 6%) two hours ago.
	for i := 1; i <= 36; i++ {
		close := "101"
		if i == 12 {
			close = "110"
		}
		series.series = append(series.series, bar(last.Add(time.Duration(i)*5*time.Minute), close))
	}
	_ = at
	step(3 * time.Hour)
	_, _ = store.LoadState(context.Background(), "POSITION", &st)
	if st.Batch.BestClose != 110 {
		t.Fatal("missed close not recovered", st.Batch.BestClose, series.starts[len(series.starts)-1], last)
	}
}
