package strategy_test

import (
	"context"
	"testing"

	pg "github.com/AceNanako0721/Factorforge/src/factorforge/strategy/adapters/postgres"
	tradingpg "github.com/AceNanako0721/Factorforge/src/factorforge/trading/adapters/postgres"
)

// Pools are capped unless the DSN sets pool_max_conns, so every service
// process on a host together stays under the server's max_connections.
// Pools connect lazily, so no server is needed.
func TestStorePoolsAreCappedByDefault(t *testing.T) {
	ctx := context.Background()
	dsn := "postgres://cap@127.0.0.1:1/cap"
	strategy, err := pg.Open(ctx, dsn, "SIM", "worker")
	if err != nil {
		t.Fatal(err)
	}
	defer strategy.Close()
	trading, err := tradingpg.Open(ctx, dsn, "SIM")
	if err != nil {
		t.Fatal(err)
	}
	defer trading.Close()
	if strategy.MaxConns() != pg.DefaultMaxConns || trading.MaxConns() != tradingpg.DefaultMaxConns || pg.DefaultMaxConns != 3 {
		t.Fatal("default cap", strategy.MaxConns(), trading.MaxConns())
	}
	explicit, err := pg.Open(ctx, dsn+"?pool_max_conns=7", "SIM", "worker")
	if err != nil {
		t.Fatal(err)
	}
	defer explicit.Close()
	if explicit.MaxConns() != 7 {
		t.Fatal("explicit pool_max_conns not kept", explicit.MaxConns())
	}
}
