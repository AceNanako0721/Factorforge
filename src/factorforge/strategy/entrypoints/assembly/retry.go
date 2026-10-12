package assembly

import (
	"context"
	"fmt"
	"io"
	"math/rand/v2"
	"time"

	d "github.com/AceNanako0721/Factorforge/src/factorforge/strategy/domain"
)

// RetryTick retries a whole workload tick so state and trading snapshots are
// freshly read. Other errors retain their original single-attempt behavior.
func RetryTick(ctx context.Context, tick func(context.Context) error, stderr io.Writer, wait func(context.Context, time.Duration) error) error {
	code := ""
	for attempt := 0; attempt < 3; attempt++ {
		if err := ctx.Err(); err != nil {
			return err
		}
		err := tick(ctx)
		if err == nil {
			if attempt > 0 {
				fmt.Fprintln(stderr, "RETRIED", code, attempt)
			}
			return nil
		}
		code = Code(err)
		if attempt == 2 || !d.Has([]string{"CYCLE_SNAPSHOT_CONFLICT", "FEEDBACK_SNAPSHOT_CONFLICT", "TRADING_SNAPSHOT_CONFLICT"}, code) {
			return err
		}
		if err = wait(ctx, time.Duration(200+rand.IntN(601))*time.Millisecond); err != nil {
			return err
		}
	}
	return nil
}

func retryWait(ctx context.Context, delay time.Duration) error {
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}
