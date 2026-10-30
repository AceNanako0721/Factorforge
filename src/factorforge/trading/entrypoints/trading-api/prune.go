package main

import (
	"context"
	"fmt"
	"time"

	"github.com/AceNanako0721/Factorforge/src/factorforge/trading/adapters/postgres"
	d "github.com/AceNanako0721/Factorforge/src/factorforge/trading/domain"
)

// pruneMarketTrades keeps the public-trade projection inside the registered
// window (v2.21.0, design 26). It runs hourly and never blocks the API; a
// failure is reported and retried on the next tick.
func pruneMarketTrades(ctx context.Context, store *postgres.Store, days int) {
	if days <= 0 {
		return
	}
	ticker := time.NewTicker(time.Hour)
	defer ticker.Stop()
	for {
		pruneOnce(ctx, store, days)
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

func pruneOnce(ctx context.Context, store *postgres.Store, days int) {
	cutoff := time.Now().UTC().AddDate(0, 0, -days)
	var total int64
	for i := 0; i < 50; i++ {
		n, err := store.PruneMarketTrades(ctx, cutoff, 5000)
		if err != nil {
			code := "STORE_UNAVAILABLE"
			if known, ok := err.(*d.Error); ok {
				code = known.Code
			}
			fmt.Printf("PRUNE_FAILED %s\n", code)
			return
		}
		total += n
		if n < 5000 {
			break
		}
	}
	if total > 0 {
		fmt.Printf("Pruned %d market trades before %s\n", total, cutoff.Format(time.RFC3339))
	}
}
