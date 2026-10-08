// Package postgres provides the physically separated SIM/LIVE and public/worker
// stores. Append-only audit and contribution rows commit with the durable outbox.
package postgres

import (
	"bytes"
	"context"
	"embed"
	"encoding/json"
	"errors"
	d "github.com/AceNanako0721/Factorforge/src/factorforge/strategy/domain"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	"strings"
)

//go:embed migrations/001_strategy.sql
var migrations embed.FS
var publicFields = []string{"version", "objects", "events", "event_versions", "event_received", "scores", "research_receipts", "received", "dedup", "attributions"}

type Store struct {
	pool                      *pgxpool.Pool
	environment, kind, schema string
}

func failure(code string, status int) error { return &d.Error{Code: code, Status: status} }
func schema(environment string) (string, error) {
	if !d.Has([]string{"SIM", "LIVE"}, environment) {
		return "", failure("ENVIRONMENT_UNSUPPORTED", 422)
	}
	return "strategy_" + strings.ToLower(environment), nil
}
func dbError(err error) error {
	if err == nil {
		return nil
	}
	var known *d.Error
	if errors.As(err, &known) {
		return err
	}
	var pg *pgconn.PgError
	if errors.As(err, &pg) && pg.Code == "23505" {
		return failure("OBJECT_OWNER_CONFLICT", 409)
	}
	return failure("STORE_UNAVAILABLE", 503)
}
func Open(ctx context.Context, dsn, environment, kind string) (*Store, error) {
	name, err := schema(environment)
	if err != nil {
		return nil, err
	}
	if !d.Has([]string{"public", "worker"}, kind) {
		return nil, failure("DATABASE_BINDING_INVALID", 422)
	}
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		return nil, dbError(err)
	}
	return &Store{pool: pool, environment: environment, kind: kind, schema: name}, nil
}
func (s *Store) Close()                   { s.pool.Close() }
func (s *Store) Environment() string      { return s.environment }
func (s *Store) table(name string) string { return pgx.Identifier{s.schema, name}.Sanitize() }
func Initialize(ctx context.Context, dsn, environment string) error {
	name, err := schema(environment)
	if err != nil {
		return err
	}
	conn, err := pgx.Connect(ctx, dsn)
	if err != nil {
		return dbError(err)
	}
	defer conn.Close(ctx)
	tx, err := conn.Begin(ctx)
	if err != nil {
		return dbError(err)
	}
	defer tx.Rollback(ctx)
	script, err := migrations.ReadFile("migrations/001_strategy.sql")
	if err != nil {
		return failure("MIGRATION_UNAVAILABLE", 503)
	}
	text := strings.ReplaceAll(strings.ReplaceAll(string(script), "__SCHEMA__", name), "__ENV_LOWER__", strings.ToLower(environment))
	if _, err = tx.Exec(ctx, text); err != nil {
		return dbError(err)
	}
	for _, kind := range []string{"public", "worker"} {
		roleName := "factorforge_strategy_" + strings.ToLower(environment) + "_" + kind
		role := pgx.Identifier{roleName}.Sanitize()
		var exists bool
		if err = tx.QueryRow(ctx, "SELECT EXISTS(SELECT 1 FROM pg_roles WHERE rolname=$1)", roleName).Scan(&exists); err != nil {
			return dbError(err)
		}
		if !exists {
			if _, err = tx.Exec(ctx, "CREATE ROLE "+role+" NOLOGIN"); err != nil {
				return dbError(err)
			}
		}
		statements := []string{"GRANT USAGE ON SCHEMA " + name + " TO " + role, "GRANT SELECT ON ALL TABLES IN SCHEMA " + name + " TO " + role, "GRANT INSERT,UPDATE ON " + name + ".public_state TO " + role, "GRANT INSERT ON " + name + ".audit_event TO " + role, "GRANT INSERT ON " + name + ".object_owner_binding TO " + role}
		if kind == "worker" {
			statements = append(statements, "GRANT INSERT,UPDATE ON "+name+".internal_state TO "+role, "GRANT INSERT ON "+name+".sentiment_entry TO "+role)
		} else {
			statements = append(statements, "REVOKE ALL ON "+name+".workload_capability_grant FROM "+role)
		}
		for _, stmt := range statements {
			if _, err = tx.Exec(ctx, stmt); err != nil {
				return dbError(err)
			}
		}
	}
	return dbError(tx.Commit(ctx))
}
func (s *Store) VerifyRuntimeRole(ctx context.Context) error {
	role := "factorforge_strategy_" + strings.ToLower(s.environment) + "_" + s.kind
	opposite := "strategy_live"
	if s.environment == "LIVE" {
		opposite = "strategy_sim"
	}
	var allowed, super, cross bool
	err := s.pool.QueryRow(ctx, "SELECT pg_has_role(session_user,$1,'USAGE'),rolsuper,CASE WHEN to_regnamespace($2) IS NULL THEN false ELSE has_schema_privilege(session_user,$2,'USAGE') END FROM pg_roles WHERE rolname=session_user", role, opposite).Scan(&allowed, &super, &cross)
	if err != nil {
		return dbError(err)
	}
	if !allowed || super || cross {
		return failure("DATABASE_ROLE_NOT_ISOLATED", 503)
	}
	if s.kind == "public" {
		var update bool
		if err = s.pool.QueryRow(ctx, "SELECT has_table_privilege(session_user,$1,'UPDATE')", s.schema+".internal_state").Scan(&update); err != nil {
			return dbError(err)
		}
		if update {
			return failure("DATABASE_CAPABILITIES_NOT_SEPARATED", 503)
		}
	}
	return nil
}
func (s *Store) CheckWorkload(ctx context.Context, w d.WorkloadIdentity, objectID string) error {
	if s.kind != "worker" || w.Environment != s.environment {
		return failure("WORKLOAD_DATABASE_ROLE_REQUIRED", 403)
	}
	var exists bool
	// Commands check grants while holding the aggregate row transaction. A
	// separate short connection preserves a fresh authorization read without
	// waiting for a pool slot occupied by other transactions on that same row.
	connection, err := pgx.ConnectConfig(ctx, s.pool.Config().ConnConfig.Copy())
	if err != nil {
		return dbError(err)
	}
	defer connection.Close(ctx)
	err = connection.QueryRow(ctx, "SELECT EXISTS(SELECT 1 FROM "+s.table("workload_capability_grant")+" WHERE workload_id=$1 AND instance_id=$2 AND object_id=$3 AND capability=$4)", w.WorkloadID, w.InstanceID, objectID, "signal:"+strings.ToLower(s.environment)).Scan(&exists)
	if err != nil {
		return dbError(err)
	}
	if !exists {
		return failure("WORKLOAD_GRANT_MISSING", 403)
	}
	return nil
}
func (s *Store) GrantWorkload(ctx context.Context, w d.WorkloadIdentity) error {
	if s.kind != "worker" || w.Environment != s.environment {
		return failure("ENVIRONMENT_FORBIDDEN", 403)
	}
	for _, id := range d.Unique(w.ObjectIDs) {
		if _, err := s.pool.Exec(ctx, "INSERT INTO "+s.table("workload_capability_grant")+" VALUES($1,$2,$3,$4)", w.WorkloadID, w.InstanceID, id, "signal:"+strings.ToLower(s.environment)); err != nil {
			return dbError(err)
		}
	}
	return nil
}
func partition(state *d.StrategyState) ([]byte, []byte, error) {
	data := d.Map(state)
	public, internal := map[string]any{}, map[string]any{}
	for k, v := range data {
		if d.Has(publicFields, k) {
			public[k] = v
		} else if k != "audit" && k != "ledger" {
			internal[k] = v
		}
	}
	p, e := json.Marshal(public)
	if e != nil {
		return nil, nil, e
	}
	i, e := json.Marshal(internal)
	return p, i, e
}
func (s *Store) binding(ctx context.Context, tx pgx.Tx, state *d.StrategyState, persist bool) error {
	for _, obj := range state.Objects.Values() {
		b := []string{obj.TradingRunKey.AccountID, obj.TradingRunKey.RunID, obj.InstrumentKey.Venue, obj.InstrumentKey.Product, obj.InstrumentKey.InstrumentID, obj.OwnerID}
		if persist {
			_, err := tx.Exec(ctx, "INSERT INTO "+s.table("object_owner_binding")+" VALUES($1,$2,$3,$4,$5,$6,$7,$8) ON CONFLICT(instance_id,object_id) DO NOTHING", state.InstanceID, obj.ObjectID, b[0], b[1], b[2], b[3], b[4], b[5])
			if err != nil {
				return err
			}
		}
		var a [6]string
		err := tx.QueryRow(ctx, "SELECT account_id,run_id,venue,product,instrument_id,owner_id FROM "+s.table("object_owner_binding")+" WHERE instance_id=$1 AND object_id=$2", state.InstanceID, obj.ObjectID).Scan(&a[0], &a[1], &a[2], &a[3], &a[4], &a[5])
		status := 503
		if persist {
			status = 409
		}
		if err != nil || d.Digest(a) != d.Digest(b) {
			return failure("OBJECT_BINDING_CHANGED", status)
		}
	}
	return nil
}
func (s *Store) load(ctx context.Context, tx pgx.Tx, id string, lock bool) (*d.StrategyState, error) {
	suffix := ""
	if lock {
		suffix = " FOR UPDATE"
	}
	var public, internal []byte
	if err := tx.QueryRow(ctx, "SELECT payload FROM "+s.table("public_state")+" WHERE instance_id=$1"+suffix, id).Scan(&public); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, failure("INSTANCE_NOT_FOUND", 404)
		}
		return nil, err
	}
	if err := tx.QueryRow(ctx, "SELECT payload FROM "+s.table("internal_state")+" WHERE instance_id=$1", id).Scan(&internal); err != nil {
		return nil, err
	}
	data := map[string]json.RawMessage{}
	if json.Unmarshal(public, &data) != nil {
		return nil, failure("STORE_STATE_INVALID", 503)
	}
	other := map[string]json.RawMessage{}
	if json.Unmarshal(internal, &other) != nil {
		return nil, failure("STORE_STATE_INVALID", 503)
	}
	for k, v := range other {
		data[k] = v
	}
	raw, err := json.Marshal(data)
	if err != nil {
		return nil, err
	}
	state := &d.StrategyState{}
	if d.DecodeJSON(raw, state) != nil {
		return nil, failure("STORE_STATE_INVALID", 503)
	}
	if state.InstanceID != id || state.Environment != s.environment {
		return nil, failure("ENVIRONMENT_FORBIDDEN", 403)
	}
	if err = s.binding(ctx, tx, state, false); err != nil {
		return nil, err
	}
	for _, name := range []string{"audit_event", "sentiment_entry"} {
		rows, err := tx.Query(ctx, "SELECT payload FROM "+s.table(name)+" WHERE instance_id=$1 ORDER BY sequence", id)
		if err != nil {
			return nil, err
		}
		for rows.Next() {
			var raw []byte
			if err = rows.Scan(&raw); err != nil {
				rows.Close()
				return nil, err
			}
			if name == "audit_event" {
				var v map[string]any
				if d.RawDecode(bytes.NewReader(raw), &v) != nil {
					rows.Close()
					return nil, failure("STORE_STATE_INVALID", 503)
				}
				state.Audit = append(state.Audit, v)
			} else {
				var v d.LedgerEntry
				if d.DecodeJSON(raw, &v) != nil {
					rows.Close()
					return nil, failure("STORE_STATE_INVALID", 503)
				}
				state.Ledger = append(state.Ledger, v)
			}
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return nil, err
		}
	}
	return state, nil
}
func (s *Store) Create(ctx context.Context, state *d.StrategyState) error {
	if state.Environment != s.environment || s.kind != "worker" {
		return failure("ENVIRONMENT_FORBIDDEN", 403)
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return dbError(err)
	}
	defer tx.Rollback(ctx)
	public, internal, err := partition(state)
	if err != nil {
		return dbError(err)
	}
	if _, err = tx.Exec(ctx, "INSERT INTO "+s.table("public_state")+" VALUES($1,$2)", state.InstanceID, public); err != nil {
		return dbError(err)
	}
	if _, err = tx.Exec(ctx, "INSERT INTO "+s.table("internal_state")+" VALUES($1,$2)", state.InstanceID, internal); err != nil {
		return dbError(err)
	}
	if err = s.binding(ctx, tx, state, true); err != nil {
		return dbError(err)
	}
	if err = s.appendRows(ctx, tx, state.InstanceID, nil, state.Audit, nil, state.Ledger); err != nil {
		return dbError(err)
	}
	return dbError(tx.Commit(ctx))
}
func (s *Store) Read(ctx context.Context, id string) (*d.StrategyState, error) {
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if err != nil {
		return nil, dbError(err)
	}
	defer tx.Rollback(ctx)
	state, err := s.load(ctx, tx, id, false)
	if err != nil {
		return nil, dbError(err)
	}
	if err = tx.Commit(ctx); err != nil {
		return nil, dbError(err)
	}
	return state, nil
}
func (s *Store) appendRows(ctx context.Context, tx pgx.Tx, id string, oldAudit, newAudit []map[string]any, oldLedger, newLedger []d.LedgerEntry) error {
	if len(newAudit) < len(oldAudit) || len(newLedger) < len(oldLedger) || d.Digest(newAudit[:len(oldAudit)]) != d.Digest(oldAudit) || d.Digest(newLedger[:len(oldLedger)]) != d.Digest(oldLedger) {
		return failure("APPEND_ONLY_MUTATION_FORBIDDEN", 503)
	}
	for index, row := range newAudit[len(oldAudit):] {
		raw, err := d.Marshal(row)
		if err != nil {
			return err
		}
		if _, err = tx.Exec(ctx, "INSERT INTO "+s.table("audit_event")+" VALUES($1,$2,$3)", id, len(oldAudit)+index+1, raw); err != nil {
			return err
		}
	}
	for index, row := range newLedger[len(oldLedger):] {
		raw, err := d.Marshal(row)
		if err != nil {
			return err
		}
		if _, err = tx.Exec(ctx, "INSERT INTO "+s.table("sentiment_entry")+" VALUES($1,$2,$3)", id, len(oldLedger)+index+1, raw); err != nil {
			return err
		}
	}
	return nil
}
func (s *Store) Transaction(ctx context.Context, id string, fn func(*d.StrategyState) error) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return dbError(err)
	}
	defer tx.Rollback(ctx)
	state, err := s.load(ctx, tx, id, true)
	if err != nil {
		return dbError(err)
	}
	before := d.Clone(state)
	if err = d.Guard(func() error { return fn(state) }); err != nil {
		return err
	}
	public, internal, err := partition(state)
	if err != nil {
		return dbError(err)
	}
	if err = s.binding(ctx, tx, state, true); err != nil {
		return dbError(err)
	}
	if s.kind == "public" {
		_, oldInternal, err := partition(before)
		if err != nil {
			return dbError(err)
		}
		if !bytes.Equal(internal, oldInternal) || d.Digest(state.Ledger) != d.Digest(before.Ledger) {
			return failure("PUBLIC_INTERNAL_STATE_FORBIDDEN", 403)
		}
	} else {
		if _, err = tx.Exec(ctx, "UPDATE "+s.table("internal_state")+" SET payload=$1 WHERE instance_id=$2", internal, id); err != nil {
			return dbError(err)
		}
	}
	if _, err = tx.Exec(ctx, "UPDATE "+s.table("public_state")+" SET payload=$1 WHERE instance_id=$2", public, id); err != nil {
		return dbError(err)
	}
	if err = d.PreserveWindowPlans(before, state); err != nil {
		return err
	}
	if err = s.appendRows(ctx, tx, id, before.Audit, state.Audit, before.Ledger, state.Ledger); err != nil {
		return dbError(err)
	}
	return dbError(tx.Commit(ctx))
}
