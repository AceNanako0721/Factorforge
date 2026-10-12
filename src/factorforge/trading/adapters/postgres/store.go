// Package postgres commits intent, reservation, deduplication, outbox and audit
// in a single account-row transaction. Only the installer owns migrations.
package postgres

import (
	"bytes"
	"context"
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	d "github.com/AceNanako0721/Factorforge/src/factorforge/trading/domain"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	"strings"
)

//go:embed migrations/*.sql
var migrations embed.FS
var Relations = []string{"account_policy_version", "instrument_spec", "market_snapshot_ref", "owner_binding", "target_request", "order", "protection_order", "fill", "income_entry", "position_lot", "position_cycle", "risk_day", "risk_lock", "risk_reservation", "reconciliation_report", "command_dedup", "outbox", "external_fact", "instrument_rule_history", "executor_lease", "runtime_health", "market_trade"}

type Store struct {
	pool                *pgxpool.Pool
	environment, schema string
}

func failure(code string, status int) error { return &d.Error{Code: code, Status: status} }
func schemaFor(environment string) (string, error) {
	if environment != "SIM" && environment != "LIVE" {
		return "", failure("ENVIRONMENT_UNSUPPORTED", 422)
	}
	return "trading_" + strings.ToLower(environment), nil
}
func Open(ctx context.Context, dsn, environment string) (*Store, error) {
	schema, err := schemaFor(environment)
	if err != nil {
		return nil, err
	}
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		return nil, failure("STORE_UNAVAILABLE", 503)
	}
	return &Store{pool, environment, schema}, nil
}
func (s *Store) Close()              { s.pool.Close() }
func (s *Store) Environment() string { return s.environment }
func (s *Store) key(key d.RunKey) error {
	if key.Environment != s.environment {
		return failure("ENVIRONMENT_FORBIDDEN", 403)
	}
	return nil
}
func (s *Store) table(name string) string { return pgx.Identifier{s.schema, name}.Sanitize() }
func sqlError(err error) error {
	if err == nil {
		return nil
	}
	var domain *d.Error
	if errors.As(err, &domain) {
		return err
	}
	return failure("STORE_UNAVAILABLE", 503)
}
func Initialize(ctx context.Context, dsn, environment string) error {
	schema, err := schemaFor(environment)
	if err != nil {
		return err
	}
	connection, err := pgx.Connect(ctx, dsn)
	if err != nil {
		return sqlError(err)
	}
	defer connection.Close(ctx)
	tx, err := connection.Begin(ctx)
	if err != nil {
		return sqlError(err)
	}
	defer tx.Rollback(ctx)
	for _, name := range []string{"001_trading.sql", "002_snapshot_order.sql"} {
		script, err := migrations.ReadFile("migrations/" + name)
		if err != nil {
			return failure("MIGRATION_UNAVAILABLE", 503)
		}
		text := strings.ReplaceAll(strings.ReplaceAll(string(script), "__SCHEMA__", schema), "__ENVIRONMENT__", environment)
		if _, err = tx.Exec(ctx, text); err != nil {
			return sqlError(err)
		}
	}
	for _, name := range Relations {
		table := pgx.Identifier{schema, name}.Sanitize()
		if _, err = tx.Exec(ctx, "CREATE TABLE IF NOT EXISTS "+table+" (account_id text NOT NULL REFERENCES "+schema+".trading_run(account_id),resource_id text NOT NULL,payload jsonb NOT NULL,PRIMARY KEY(account_id,resource_id))"); err != nil {
			return sqlError(err)
		}
	}
	role := "factorforge_" + strings.ToLower(environment)
	var exists bool
	if err = tx.QueryRow(ctx, "SELECT EXISTS(SELECT 1 FROM pg_roles WHERE rolname=$1)", role).Scan(&exists); err != nil {
		return sqlError(err)
	}
	if !exists {
		if _, err = tx.Exec(ctx, "CREATE ROLE "+role+" NOLOGIN"); err != nil {
			return sqlError(err)
		}
	}
	for _, query := range []string{"REVOKE ALL ON ALL TABLES IN SCHEMA " + schema + " FROM PUBLIC", "GRANT USAGE ON SCHEMA " + schema + " TO " + role, "GRANT SELECT,INSERT,UPDATE ON ALL TABLES IN SCHEMA " + schema + " TO " + role, "REVOKE UPDATE,DELETE ON " + schema + ".audit_event FROM " + role} {
		if _, err = tx.Exec(ctx, query); err != nil {
			return sqlError(err)
		}
	}
	return sqlError(tx.Commit(ctx))
}
func (s *Store) VerifyRuntimeRole(ctx context.Context) error {
	var allowed, superuser bool
	role := "factorforge_" + strings.ToLower(s.environment)
	if err := s.pool.QueryRow(ctx, "SELECT pg_has_role(session_user,$1,'USAGE'),rolsuper FROM pg_roles WHERE rolname=session_user", role).Scan(&allowed, &superuser); err != nil {
		return sqlError(err)
	}
	opposite := "trading_live"
	if s.environment == "LIVE" {
		opposite = "trading_sim"
	}
	var exists bool
	if err := s.pool.QueryRow(ctx, "SELECT to_regnamespace($1) IS NOT NULL", opposite).Scan(&exists); err != nil {
		return sqlError(err)
	}
	var cross bool
	if exists {
		if err := s.pool.QueryRow(ctx, "SELECT has_schema_privilege(session_user,$1,'USAGE')", opposite).Scan(&cross); err != nil {
			return sqlError(err)
		}
	}
	if !allowed || superuser || cross {
		return failure("DATABASE_ROLE_NOT_ISOLATED", 503)
	}
	return nil
}

type reader interface {
	QueryRow(context.Context, string, ...any) pgx.Row
}

func loginAllowed(ctx context.Context, connection reader) error {
	var canLogin bool
	if err := connection.QueryRow(ctx, "SELECT rolcanlogin FROM pg_roles WHERE rolname=current_user").Scan(&canLogin); err != nil || !canLogin {
		return failure("STORE_UNAVAILABLE", 503)
	}
	return nil
}

func (s *Store) load(ctx context.Context, connection reader, key d.RunKey, lock bool) (*d.Aggregate, bool, error) {
	if err := s.key(key); err != nil {
		return nil, false, err
	}
	if err := loginAllowed(ctx, connection); err != nil {
		return nil, false, err
	}
	query := "SELECT snapshot_text IS NOT NULL AND snapshot_text::jsonb=payload, CASE WHEN snapshot_text IS NOT NULL AND snapshot_text::jsonb=payload THEN snapshot_text ELSE payload::text END FROM " + s.table("trading_run") + " WHERE account_id=$1 AND run_id=$2"
	if lock {
		query += " FOR UPDATE"
	}
	var ordered bool
	var data []byte
	if err := connection.QueryRow(ctx, query, key.AccountID, key.RunID).Scan(&ordered, &data); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, false, failure("RUN_NOT_FOUND", 404)
		}
		return nil, false, sqlError(err)
	}
	var run d.Aggregate
	if json.Unmarshal(data, &run) != nil || d.Validate(run) != nil {
		return nil, false, failure("STORE_SNAPSHOT_INVALID", 503)
	}
	return &run, ordered, nil
}
func (s *Store) Read(ctx context.Context, key d.RunKey) (*d.Aggregate, error) {
	run, _, err := s.load(ctx, s.pool, key, false)
	return run, err
}
func (s *Store) BoundRun(ctx context.Context, account string) (*d.RunKey, error) {
	if err := loginAllowed(ctx, s.pool); err != nil {
		return nil, err
	}
	var id string
	if err := s.pool.QueryRow(ctx, "SELECT run_id FROM "+s.table("trading_run")+" WHERE account_id=$1", account).Scan(&id); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, nil
		}
		return nil, sqlError(err)
	}
	return &d.RunKey{Environment: s.environment, AccountID: account, RunID: id}, nil
}
func (s *Store) Create(ctx context.Context, run *d.Aggregate) error {
	if err := s.key(run.RunKey); err != nil {
		return err
	}
	if d.Validate(run) != nil {
		return failure("STORE_SNAPSHOT_INVALID", 503)
	}
	data, err := json.Marshal(run)
	if err != nil {
		return failure("STORE_SNAPSHOT_INVALID", 503)
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return sqlError(err)
	}
	defer tx.Rollback(ctx)
	if err = loginAllowed(ctx, tx); err != nil {
		return err
	}
	_, err = tx.Exec(ctx, "INSERT INTO "+s.table("trading_run")+"(account_id,run_id,environment,version,payload,snapshot_text) VALUES($1,$2,$3,$4,$5::jsonb,$6)", run.RunKey.AccountID, run.RunKey.RunID, s.environment, run.Version, data, string(data))
	var pg *pgconn.PgError
	if errors.As(err, &pg) && pg.Code == "23505" {
		return failure("ACCOUNT_ALREADY_BOUND", 409)
	}
	if err != nil {
		return sqlError(err)
	}
	if err = s.persist(ctx, tx, run, nil); err != nil {
		return err
	}
	return sqlError(tx.Commit(ctx))
}
func (s *Store) Transaction(ctx context.Context, key d.RunKey, apply func(*d.Aggregate) error) error {
	if err := s.key(key); err != nil {
		return err
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return sqlError(err)
	}
	defer tx.Rollback(ctx)
	run, ordered, err := s.load(ctx, tx, key, true)
	if err != nil {
		return err
	}
	// Projections are rewritten only where this transaction changed them. A
	// payload changed outside the ordered snapshot falls back to a full rewrite.
	var prior *state
	if ordered {
		prior = capture(run)
	}
	if err = apply(run); err != nil {
		return err
	}
	if d.Validate(run) != nil {
		return failure("STORE_SNAPSHOT_INVALID", 503)
	}
	data, err := json.Marshal(run)
	if err != nil {
		return failure("STORE_SNAPSHOT_INVALID", 503)
	}
	if err = s.persist(ctx, tx, run, prior); err != nil {
		return err
	}
	_, err = tx.Exec(ctx, "UPDATE "+s.table("trading_run")+" SET version=$1,payload=$2::jsonb,snapshot_text=$3 WHERE account_id=$4", run.Version, data, string(data), key.AccountID)
	if err != nil {
		return sqlError(err)
	}
	return sqlError(tx.Commit(ctx))
}
func raw(value any) json.RawMessage { data, _ := json.Marshal(value); return data }

// state is the projection of one aggregate: relation rows and audit events.
type state struct {
	records map[string]map[string]json.RawMessage
	audit   []json.RawMessage
}

func capture(run *d.Aggregate) *state {
	data := map[string]json.RawMessage{}
	encoded, _ := json.Marshal(run)
	json.Unmarshal(encoded, &data)
	records := map[string]map[string]json.RawMessage{}
	mapField := func(table, field string) {
		var values map[string]json.RawMessage
		json.Unmarshal(data[field], &values)
		records[table] = values
	}
	for table, field := range map[string]string{"instrument_spec": "specs", "market_snapshot_ref": "points", "target_request": "targets", "order": "orders", "protection_order": "protections", "fill": "fills", "income_entry": "incomes", "position_lot": "positions", "command_dedup": "dedup", "external_fact": "external_facts", "market_trade": "market_trades"} {
		mapField(table, field)
	}
	records["account_policy_version"] = map[string]json.RawMessage{run.Policy.Version: data["policy"]}
	owners := map[string]json.RawMessage{}
	for _, key := range run.Owners.Keys() {
		owners[key] = raw(map[string]any{"owner_id": run.Owners.Value(key), "epoch": run.OwnerEpochs.Value(key)})
	}
	records["owner_binding"] = owners
	records["position_cycle"] = map[string]json.RawMessage{"account": raw(map[string]any{"consecutive_losses": run.ConsecutiveLosses})}
	records["risk_day"] = map[string]json.RawMessage{run.RiskDay: raw(map[string]any{"start_equity": run.DayStartEquity, "external_flow": run.DayExternalFlow})}
	locks := map[string]json.RawMessage{}
	for _, key := range run.RiskLocks {
		locks[key] = raw(map[string]bool{"active": true})
	}
	records["risk_lock"] = locks
	reservations := map[string]json.RawMessage{}
	for _, order := range run.Orders.Values() {
		reservations[order.OrderID] = raw(map[string]any{"notional": order.ReservedNotional})
	}
	records["risk_reservation"] = reservations
	records["reconciliation_report"] = map[string]json.RawMessage{fmt.Sprint(run.Version): raw(map[string]any{"issues": run.RecoveryIssues, "state": run.State})}
	outbox := map[string]json.RawMessage{}
	for _, item := range run.Outbox {
		outbox[item.CommandID] = raw(item)
	}
	records["outbox"] = outbox
	history := map[string]json.RawMessage{}
	for i, spec := range run.RuleHistory {
		history[fmt.Sprint(i)] = raw(spec)
	}
	records["instrument_rule_history"] = history
	lease := map[string]json.RawMessage{}
	for _, field := range []string{"lease_holder", "lease_until", "lease_epoch", "isolated_epoch", "isolation_evidence"} {
		lease[field] = data[field]
	}
	records["executor_lease"] = map[string]json.RawMessage{"account": raw(lease)}
	records["runtime_health"] = map[string]json.RawMessage{"current": raw(map[string]any{"issues": run.HealthIssues, "checked_at": run.HealthCheckedAt})}
	audit := make([]json.RawMessage, len(run.Audit))
	for i, event := range run.Audit {
		audit[i] = raw(event)
	}
	return &state{records, audit}
}

// persist writes the projections of run. With prior (the state loaded in the
// same locked transaction) only changed rows are upserted and only new audit
// events are verified against storage and appended; without it every row is
// written and every stored audit event is compared.
func (s *Store) persist(ctx context.Context, tx pgx.Tx, run *d.Aggregate, prior *state) error {
	current := capture(run)
	if prior == nil {
		if err := s.verifyAudit(ctx, tx, run); err != nil {
			return err
		}
	} else {
		if len(current.audit) < len(prior.audit) {
			return failure("AUDIT_MUTATION_FORBIDDEN", 503)
		}
		for i := range prior.audit {
			if !bytes.Equal(prior.audit[i], current.audit[i]) {
				return failure("AUDIT_MUTATION_FORBIDDEN", 503)
			}
		}
		var count, last int
		if err := tx.QueryRow(ctx, "SELECT count(*),COALESCE(max(sequence),0) FROM "+s.table("audit_event")+" WHERE account_id=$1", run.RunKey.AccountID).Scan(&count, &last); err != nil {
			return sqlError(err)
		}
		if count != len(prior.audit) || last != len(prior.audit) {
			return failure("AUDIT_MUTATION_FORBIDDEN", 503)
		}
	}
	for table, values := range current.records {
		for identity, value := range values {
			if prior != nil {
				if old, ok := prior.records[table][identity]; ok && bytes.Equal(old, value) {
					continue
				}
			}
			if _, err := tx.Exec(ctx, "INSERT INTO "+s.table(table)+"(account_id,resource_id,payload) VALUES($1,$2,$3::jsonb) ON CONFLICT(account_id,resource_id) DO UPDATE SET payload=excluded.payload", run.RunKey.AccountID, identity, []byte(value)); err != nil {
				return sqlError(err)
			}
		}
	}
	first := 0
	if prior != nil {
		first = len(prior.audit)
	}
	for i := first; i < len(current.audit); i++ {
		if _, err := tx.Exec(ctx, "INSERT INTO "+s.table("audit_event")+"(account_id,sequence,payload) VALUES($1,$2,$3::jsonb) ON CONFLICT DO NOTHING", run.RunKey.AccountID, i+1, []byte(current.audit[i])); err != nil {
			return sqlError(err)
		}
	}
	return nil
}

func (s *Store) verifyAudit(ctx context.Context, tx pgx.Tx, run *d.Aggregate) error {
	rows, err := tx.Query(ctx, "SELECT sequence,payload FROM "+s.table("audit_event")+" WHERE account_id=$1 ORDER BY sequence", run.RunKey.AccountID)
	if err != nil {
		return sqlError(err)
	}
	for rows.Next() {
		var sequence int
		var data []byte
		if err = rows.Scan(&sequence, &data); err != nil {
			rows.Close()
			return sqlError(err)
		}
		var prior d.Object
		if json.Unmarshal(data, &prior) != nil || sequence > len(run.Audit) || !equalJSON(prior, run.Audit[sequence-1]) {
			rows.Close()
			return failure("AUDIT_MUTATION_FORBIDDEN", 503)
		}
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return sqlError(err)
	}
	return nil
}

func equalJSON(a, b any) bool {
	x, _ := json.Marshal(a)
	y, _ := json.Marshal(b)
	var left, right any
	aDecoder := json.NewDecoder(bytes.NewReader(x))
	aDecoder.UseNumber()
	aDecoder.Decode(&left)
	bDecoder := json.NewDecoder(bytes.NewReader(y))
	bDecoder.UseNumber()
	bDecoder.Decode(&right)
	x, _ = json.Marshal(left)
	y, _ = json.Marshal(right)
	return string(x) == string(y)
}
