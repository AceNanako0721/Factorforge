package postgres

import (
	"context"
	_ "embed"
	"encoding/json"
	"strings"
	"time"

	d "github.com/AceNanako0721/Factorforge/src/factorforge/applications/soxl_jev/domain"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

//go:embed migrations/005_provider_control.sql
var providerControlSQL string

func InitializeProviderControl(ctx context.Context, dsn string) error {
	conn, err := pgx.Connect(ctx, dsn)
	if err != nil {
		return providerUnavailable()
	}
	defer conn.Close(ctx)
	tx, err := conn.Begin(ctx)
	if err != nil {
		return providerUnavailable()
	}
	defer tx.Rollback(ctx)
	if _, err = tx.Exec(ctx, providerControlSQL); err != nil {
		return providerUnavailable()
	}
	if err = tx.Commit(ctx); err != nil {
		return providerUnavailable()
	}
	return nil
}

// Administrative provisioning never resets counters or changes an existing
// policy, grant, pause or uncertain call. All sharing instances use this DB.
func ConfigureProviderPool(ctx context.Context, dsn string, policy d.ProviderPoolPolicy) error {
	if err := policy.Validate(); err != nil {
		return err
	}
	conn, err := pgx.Connect(ctx, dsn)
	if err != nil {
		return providerUnavailable()
	}
	defer conn.Close(ctx)
	tx, err := conn.Begin(ctx)
	if err != nil {
		return providerUnavailable()
	}
	defer tx.Rollback(ctx)
	if _, err = tx.Exec(ctx, "INSERT INTO instance_provider_control.pool(pool_id) VALUES($1) ON CONFLICT DO NOTHING", policy.PoolID); err != nil {
		return providerUnavailable()
	}
	var pool string
	if err = tx.QueryRow(ctx, "SELECT pool_id FROM instance_provider_control.pool WHERE pool_id=$1 FOR UPDATE", policy.PoolID).Scan(&pool); err != nil {
		return providerUnavailable()
	}
	raw, err := json.Marshal(policy)
	if err != nil {
		return d.Fail("PROVIDER_POLICY_INVALID", 422)
	}
	var conflict bool
	if err = tx.QueryRow(ctx, "SELECT EXISTS(SELECT 1 FROM instance_provider_control.policy WHERE pool_id=$1 AND (version=$2 OR valid_from<$4 AND valid_until>$3))", policy.PoolID, policy.Version, policy.ValidFrom, policy.ValidUntil).Scan(&conflict); err != nil {
		return providerUnavailable()
	}
	if conflict {
		var same bool
		if err = tx.QueryRow(ctx, "SELECT EXISTS(SELECT 1 FROM instance_provider_control.policy WHERE pool_id=$1 AND version=$2 AND payload=$3::jsonb)", policy.PoolID, policy.Version, raw).Scan(&same); err != nil {
			return providerUnavailable()
		}
		if !same {
			return d.Fail("PROVIDER_POLICY_IMMUTABLE", 409)
		}
	} else if _, err = tx.Exec(ctx, "INSERT INTO instance_provider_control.policy VALUES($1,$2,$3,$4,$5)", policy.PoolID, policy.Version, policy.ValidFrom, policy.ValidUntil, raw); err != nil {
		return providerUnavailable()
	}
	if err = tx.Commit(ctx); err != nil {
		return providerUnavailable()
	}
	return nil
}

func GrantProviderAccess(ctx context.Context, dsn string, grant d.ProviderGrant) error {
	if !grant.Valid() {
		return d.Fail("PROVIDER_GRANT_INVALID", 422)
	}
	conn, err := pgx.Connect(ctx, dsn)
	if err != nil {
		return providerUnavailable()
	}
	defer conn.Close(ctx)
	tx, err := conn.Begin(ctx)
	if err != nil {
		return providerUnavailable()
	}
	defer tx.Rollback(ctx)
	var allowed bool
	if err = tx.QueryRow(ctx, "SELECT EXISTS(SELECT 1 FROM pg_roles WHERE rolname=$1 AND rolcanlogin AND NOT rolsuper AND NOT rolbypassrls AND NOT rolcreaterole AND NOT rolcreatedb AND NOT rolreplication)", grant.Login).Scan(&allowed); err != nil {
		return providerUnavailable()
	}
	if !allowed {
		return d.Fail("PROVIDER_GRANT_INVALID", 422)
	}
	if _, err = tx.Exec(ctx, "INSERT INTO instance_provider_control.worker_grant VALUES($1,$2,$3,$4,$5) ON CONFLICT DO NOTHING", grant.Login, grant.PoolID, grant.Binding.InstanceID, grant.Binding.Environment, grant.QueueKind); err != nil {
		return providerUnavailable()
	}
	var same bool
	if err = tx.QueryRow(ctx, "SELECT EXISTS(SELECT 1 FROM instance_provider_control.worker_grant WHERE authenticated_role=$1 AND pool_id=$2 AND instance_id=$3 AND environment=$4 AND queue_kind=$5)", grant.Login, grant.PoolID, grant.Binding.InstanceID, grant.Binding.Environment, grant.QueueKind).Scan(&same); err != nil {
		return providerUnavailable()
	}
	if !same {
		return d.Fail("PROVIDER_GRANT_IMMUTABLE", 409)
	}
	login := pgx.Identifier{grant.Login}.Sanitize()
	for _, sql := range []string{"GRANT USAGE ON SCHEMA instance_provider_control TO " + login, "GRANT EXECUTE ON FUNCTION instance_provider_control.acquire(text,text,text,text,text,bigint,timestamptz),instance_provider_control.finish(text,text,text) TO " + login} {
		if _, err = tx.Exec(ctx, sql); err != nil {
			return providerUnavailable()
		}
	}
	if err = tx.Commit(ctx); err != nil {
		return providerUnavailable()
	}
	return nil
}

type ProviderControl struct {
	pool    *pgxpool.Pool
	binding d.Binding
	kind    string
}

func OpenProviderControl(ctx context.Context, dsn string, binding d.Binding, kind string) (*ProviderControl, error) {
	if !binding.Valid() || !d.Has([]string{"RESEARCH", binding.Environment}, kind) {
		return nil, d.Fail("PROVIDER_SCOPE_FORBIDDEN", 403)
	}
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		return nil, providerUnavailable()
	}
	s := &ProviderControl{pool: pool, binding: binding, kind: kind}
	// Opening does not reserve or spend. The function performs full authoritative
	// binding/role verification on every subsequent request and completion.
	var code string
	err = pool.QueryRow(ctx, "SELECT instance_provider_control.acquire($1,$2,$3,'','',0,clock_timestamp())", binding.InstanceID, binding.Environment, kind).Scan(&code)
	if err != nil {
		pool.Close()
		return nil, providerUnavailable()
	}
	if code != "PROVIDER_REQUEST_INVALID" {
		pool.Close()
		return nil, d.Fail("PROVIDER_SCOPE_FORBIDDEN", 403)
	}
	return s, nil
}
func (s *ProviderControl) Close() { s.pool.Close() }
func (s *ProviderControl) Acquire(ctx context.Context, binding d.Binding, id, hash string, size int, deadline time.Time) error {
	if binding != s.binding || !d.ValidID(id) || !d.UTC(deadline) || size <= 0 {
		return d.Fail("PROVIDER_REQUEST_INVALID", 422)
	}
	var code string
	if err := s.pool.QueryRow(ctx, "SELECT instance_provider_control.acquire($1,$2,$3,$4,$5,$6,$7)", binding.InstanceID, binding.Environment, s.kind, id, hash, size, deadline).Scan(&code); err != nil {
		return providerUnavailable()
	}
	return providerCode(code)
}
func (s *ProviderControl) Finish(ctx context.Context, id, hash, outcome string) error {
	var code string
	if err := s.pool.QueryRow(ctx, "SELECT instance_provider_control.finish($1,$2,$3)", id, hash, outcome).Scan(&code); err != nil {
		return providerUnavailable()
	}
	return providerCode(code)
}
func providerUnavailable() error { return d.Fail("PROVIDER_CONTROL_UNAVAILABLE", 503) }
func providerCode(code string) error {
	if code == "OK" {
		return nil
	}
	if !d.Has([]string{"PROVIDER_SCOPE_FORBIDDEN", "PROVIDER_CONFIGURATION_REQUIRED", "PROVIDER_REQUEST_INVALID", "PROVIDER_CALL_ALREADY_RESERVED", "PROVIDER_POOL_PAUSED", "PROVIDER_REQUEST_BUDGET_EXCEEDED", "PROVIDER_START_INTERVAL_LIMIT", "PROVIDER_CALL_BUDGET_EXHAUSTED", "PROVIDER_CONCURRENCY_LIMIT", "PROVIDER_RECEIPT_FORBIDDEN", "PROVIDER_RECEIPT_IMMUTABLE"}, code) {
		return providerUnavailable()
	}
	status := 429
	if strings.Contains(code, "FORBIDDEN") {
		status = 403
	} else if code == "PROVIDER_CONFIGURATION_REQUIRED" {
		status = 503
	} else if code == "PROVIDER_REQUEST_INVALID" {
		status = 422
	} else if strings.Contains(code, "IMMUTABLE") || code == "PROVIDER_CALL_ALREADY_RESERVED" {
		status = 409
	}
	return d.Fail(code, status)
}
