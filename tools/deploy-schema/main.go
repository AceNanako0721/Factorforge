// Explicit administrative schema operation; never creates business runs/objects.
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	ip "github.com/AceNanako0721/Factorforge/src/factorforge/applications/soxl_jev/adapters/postgres"
	id "github.com/AceNanako0721/Factorforge/src/factorforge/applications/soxl_jev/domain"
	sp "github.com/AceNanako0721/Factorforge/src/factorforge/strategy/adapters/postgres"
	tp "github.com/AceNanako0721/Factorforge/src/factorforge/trading/adapters/postgres"
	"github.com/jackc/pgx/v5"
	"os"
	"regexp"
	"strconv"
	"strings"
	"time"
)

func main() {
	if run() != nil {
		fmt.Fprintln(os.Stderr, "SCHEMA_OPERATION_FAILED")
		os.Exit(1)
	}
}
func run() error {
	path := flag.String("config", "", "private migration-only JSON")
	flag.Parse()
	b, e := os.ReadFile(*path)
	if e != nil {
		return e
	}
	var c struct {
		Environment string            `json:"environment"`
		InstanceID  string            `json:"instance_id"`
		DSN         string            `json:"dsn"`
		Roles       map[string]string `json:"roles"`
		Version     string            `json:"version"`
	}
	if json.Unmarshal(b, &c) != nil || c.Environment != "SIM" || c.InstanceID == "" || c.Version == "" {
		return fmt.Errorf("invalid migration configuration")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	conn, e := pgx.Connect(ctx, c.DSN)
	if e != nil {
		return e
	}
	defer conn.Close(ctx)
	// Session advisory lock serializes all schema operations across operators.
	var locked bool
	if e = conn.QueryRow(ctx, "SELECT pg_try_advisory_lock(62020001)").Scan(&locked); e != nil || !locked {
		return fmt.Errorf("migration busy")
	}
	if !regexp.MustCompile("^[0-9]+[.][0-9]+[.][0-9]+$").MatchString(c.Version) {
		return fmt.Errorf("invalid schema version")
	}
	var hasHistory bool
	if e = conn.QueryRow(ctx, "SELECT to_regclass('factorforge_deployment.schema_history') IS NOT NULL").Scan(&hasHistory); e != nil {
		return e
	}
	if hasHistory {
		var previous string
		e = conn.QueryRow(ctx, "SELECT version FROM factorforge_deployment.schema_history ORDER BY applied_at DESC LIMIT 1").Scan(&previous)
		if e != nil && e != pgx.ErrNoRows {
			return e
		}
		if e == nil {
			old, next := strings.Split(previous, "."), strings.Split(c.Version, ".")
			if len(old) != 3 {
				return fmt.Errorf("invalid previous version")
			}
			for i := 0; i < 3; i++ {
				a, ea := strconv.Atoi(old[i])
				b, eb := strconv.Atoi(next[i])
				if ea != nil || eb != nil {
					return fmt.Errorf("invalid version")
				}
				if b < a {
					return fmt.Errorf("schema downgrade rejected")
				}
				if b > a {
					break
				}
			}
		}
	}
	for _, fn := range []func() error{func() error { return tp.Initialize(ctx, c.DSN, c.Environment) }, func() error { return sp.Initialize(ctx, c.DSN, c.Environment) }, func() error {
		return ip.Initialize(ctx, c.DSN, id.Binding{Environment: c.Environment, InstanceID: c.InstanceID})
	}, func() error {
		return ip.InitializePipeline(ctx, c.DSN, id.Binding{Environment: c.Environment, InstanceID: c.InstanceID})
	}, func() error { return ip.InitializeProviderControl(ctx, c.DSN) }} {
		if e = fn(); e != nil {
			return e
		}
	}
	tx, e := conn.Begin(ctx)
	if e != nil {
		return e
	}
	defer tx.Rollback(ctx)
	grants := map[string]string{"ff_p1": "factorforge_sim", "ff_p2_public": "factorforge_strategy_sim_public", "ff_p2_worker": "factorforge_strategy_sim_worker", "ff_p3_read": "factorforge_instance_sim_read", "ff_p3_dynamic": "factorforge_pipeline_sim_dynamic", "ff_p3_dynamic_read": "factorforge_pipeline_sim_dynamic_read"}
	for role, grant := range grants {
		pass := c.Roles[role]
		if len(pass) < 32 {
			return fmt.Errorf("missing role password")
		}
		var exists bool
		if e = tx.QueryRow(ctx, "SELECT EXISTS(SELECT 1 FROM pg_roles WHERE rolname=$1)", role).Scan(&exists); e != nil {
			return e
		}
		verb := "CREATE"
		if exists {
			verb = "ALTER"
		}
		if _, e = tx.Exec(ctx, verb+" ROLE "+pgx.Identifier{role}.Sanitize()+" LOGIN NOSUPERUSER NOCREATEDB NOCREATEROLE PASSWORD '"+strings.ReplaceAll(pass, "'", "''")+"'"); e != nil {
			return e
		}
		if _, e = tx.Exec(ctx, "GRANT "+pgx.Identifier{grant}.Sanitize()+" TO "+pgx.Identifier{role}.Sanitize()); e != nil {
			return e
		}
	}
	if _, e = tx.Exec(ctx, "CREATE SCHEMA IF NOT EXISTS factorforge_deployment; CREATE TABLE IF NOT EXISTS factorforge_deployment.schema_history(version text PRIMARY KEY, applied_at timestamptz NOT NULL DEFAULT now()); REVOKE ALL ON SCHEMA factorforge_deployment FROM PUBLIC"); e != nil {
		return e
	}
	if _, e = tx.Exec(ctx, "INSERT INTO factorforge_deployment.schema_history(version) VALUES($1) ON CONFLICT DO NOTHING", c.Version); e != nil {
		return e
	}
	if e = tx.Commit(ctx); e != nil {
		return e
	}
	fmt.Println("Schemas migrated; business runs, objects, grants and reconciliation remain explicit")
	return nil
}
