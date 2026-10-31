package postgres

import (
	"context"
	"embed"
	"errors"
	d "github.com/AceNanako0721/Factorforge/src/factorforge/applications/soxl/domain"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"strings"
)

//go:embed migrations/001_storage.sql
var storageFiles embed.FS

// Store is one login identity's view of the instance storage. The DYNAMIC
// identity writes records and state; DYNAMIC_READ only reads them.
type Store struct {
	pool     *pgxpool.Pool
	binding  d.Binding
	kind     string
	schema   string
	maxBytes int
}

var storeKinds = []string{"DYNAMIC", "DYNAMIC_READ"}

// The schema and role names are fixed identifiers of the storage layout.
func storageSchema(b d.Binding) (string, error) {
	if !b.Valid() {
		return "", d.Fail("STORAGE_BINDING_INVALID", 422)
	}
	return "instance_pipeline_" + strings.ToLower(b.Environment), nil
}
func storageRole(b d.Binding, kind string) string {
	return "factorforge_pipeline_" + strings.ToLower(b.Environment) + "_" + strings.ToLower(kind)
}
func safe(err error) error {
	if err == nil {
		return nil
	}
	var e *d.Error
	if errors.As(err, &e) {
		return e
	}
	return d.Fail("INSTANCE_STORE_UNAVAILABLE", 503)
}

// InitializeStorage is an explicit administrative provisioning operation.
// No worker or public HTTP API receives this connection or calls this method.
func InitializeStorage(ctx context.Context, dsn string, b d.Binding) error {
	schema, err := storageSchema(b)
	if err != nil {
		return err
	}
	conn, err := pgx.Connect(ctx, dsn)
	if err != nil {
		return safe(err)
	}
	defer conn.Close(ctx)
	tx, err := conn.Begin(ctx)
	if err != nil {
		return safe(err)
	}
	defer tx.Rollback(ctx)
	raw, err := storageFiles.ReadFile("migrations/001_storage.sql")
	if err != nil {
		return d.Fail("MIGRATION_UNAVAILABLE", 503)
	}
	if _, err = tx.Exec(ctx, strings.ReplaceAll(strings.ReplaceAll(string(raw), "__SCHEMA__", schema), "__ENV__", b.Environment)); err != nil {
		return safe(err)
	}
	for _, kind := range storeKinds {
		role := storageRole(b, kind)
		var exists bool
		if err = tx.QueryRow(ctx, "SELECT EXISTS(SELECT 1 FROM pg_roles WHERE rolname=$1)", role).Scan(&exists); err != nil {
			return safe(err)
		}
		if !exists {
			if _, err = tx.Exec(ctx, "CREATE ROLE "+pgx.Identifier{role}.Sanitize()+" NOLOGIN"); err != nil {
				return safe(err)
			}
		}
		grants := []string{
			"REVOKE ALL ON ALL TABLES IN SCHEMA " + schema + " FROM " + role,
			"GRANT USAGE ON SCHEMA " + schema + " TO " + role,
		}
		if kind == "DYNAMIC" {
			grants = append(grants,
				"GRANT SELECT ON "+schema+".worker_grant TO "+role,
				"GRANT SELECT,INSERT ON "+schema+".dynamic_record TO "+role,
				"GRANT SELECT,INSERT,UPDATE ON "+schema+".dynamic_state TO "+role)
		} else {
			grants = append(grants, "GRANT SELECT ON "+schema+".worker_grant,"+schema+".dynamic_record,"+schema+".dynamic_state TO "+role)
		}
		for _, sql := range grants {
			if _, err = tx.Exec(ctx, sql); err != nil {
				return safe(err)
			}
		}
	}
	return safe(tx.Commit(ctx))
}

// GrantStorage binds a login role to one identity of this instance.
func GrantStorage(ctx context.Context, dsn string, b d.Binding, login, kind string) error {
	schema, err := storageSchema(b)
	if err != nil {
		return err
	}
	if !d.ValidID(login) || !d.Has(storeKinds, kind) {
		return d.Fail("STORAGE_GRANT_INVALID", 422)
	}
	conn, err := pgx.Connect(ctx, dsn)
	if err != nil {
		return safe(err)
	}
	defer conn.Close(ctx)
	tx, err := conn.Begin(ctx)
	if err != nil {
		return safe(err)
	}
	defer tx.Rollback(ctx)
	if _, err = tx.Exec(ctx, "INSERT INTO "+schema+".worker_grant VALUES($1,$2,$3,$4) ON CONFLICT DO NOTHING", login, b.InstanceID, b.Environment, kind); err != nil {
		return safe(err)
	}
	var instance, env, k string
	if err = tx.QueryRow(ctx, "SELECT instance_id,environment,worker_kind FROM "+schema+".worker_grant WHERE authenticated_role=$1", login).Scan(&instance, &env, &k); err != nil {
		return safe(err)
	}
	if instance != b.InstanceID || env != b.Environment || k != kind {
		return d.Fail("STORAGE_GRANT_IMMUTABLE", 409)
	}
	if _, err = tx.Exec(ctx, "GRANT "+pgx.Identifier{storageRole(b, kind)}.Sanitize()+" TO "+pgx.Identifier{login}.Sanitize()); err != nil {
		return safe(err)
	}
	return safe(tx.Commit(ctx))
}

// OpenStore connects with the given login and verifies that its grants are
// exactly those of the requested identity.
func OpenStore(ctx context.Context, dsn string, b d.Binding, kind string, maxBytes int) (*Store, error) {
	schema, err := storageSchema(b)
	if err != nil {
		return nil, err
	}
	if !d.Has(storeKinds, kind) || maxBytes <= 0 {
		return nil, d.Fail("STORAGE_CONFIGURATION_REQUIRED", 503)
	}
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		return nil, safe(err)
	}
	s := &Store{pool: pool, binding: b, kind: kind, schema: schema, maxBytes: maxBytes}
	if err = s.VerifyRole(ctx); err != nil {
		s.Close()
		return nil, err
	}
	return s, nil
}
func (s *Store) Close()             { s.pool.Close() }
func (s *Store) Binding() d.Binding { return s.binding }
func (s *Store) VerifyRole(ctx context.Context) error {
	var allowed bool
	err := s.pool.QueryRow(ctx, `SELECT r.rolcanlogin AND NOT r.rolsuper AND NOT r.rolbypassrls
 AND NOT r.rolcreaterole AND NOT r.rolcreatedb AND NOT r.rolreplication
 AND pg_has_role(session_user,$1,'MEMBER')
 AND NOT EXISTS(SELECT 1 FROM pg_roles p WHERE p.rolname LIKE 'factorforge_pipeline_%'
   AND p.rolname<>$1 AND pg_has_role(session_user,p.oid,'MEMBER'))
 AND NOT EXISTS(SELECT 1 FROM pg_namespace n WHERE n.nspname NOT IN ('pg_catalog','information_schema')
   AND n.nspname NOT LIKE 'pg_toast%' AND n.nspname NOT LIKE 'pg_temp%' AND has_schema_privilege(session_user,n.oid,'CREATE'))
 AND NOT EXISTS(SELECT 1 FROM pg_class c JOIN pg_namespace n ON n.oid=c.relnamespace
   WHERE n.nspname NOT IN('pg_catalog','information_schema') AND n.nspname NOT LIKE 'pg_toast%'
   AND c.relkind IN('r','p','v','m','f') AND n.nspname<>$2
   AND (has_any_column_privilege(session_user,c.oid,'SELECT,INSERT,UPDATE')
     OR has_table_privilege(session_user,c.oid,'DELETE,TRUNCATE,TRIGGER')))
 FROM pg_roles r WHERE r.rolname=session_user`, storageRole(s.binding, s.kind), s.schema).Scan(&allowed)
	if err != nil {
		return safe(err)
	}
	if !allowed {
		return d.Fail("STORAGE_ROLE_NOT_ISOLATED", 403)
	}
	err = s.pool.QueryRow(ctx, "SELECT EXISTS(SELECT 1 FROM "+s.schema+".worker_grant WHERE authenticated_role=session_user AND instance_id=$1 AND environment=$2 AND worker_kind=$3)", s.binding.InstanceID, s.binding.Environment, s.kind).Scan(&allowed)
	if err != nil {
		return safe(err)
	}
	if !allowed {
		return d.Fail("STORAGE_SCOPE_FORBIDDEN", 403)
	}
	// Only the privileges of the identity may exist on each table.
	for _, table := range []string{"worker_grant", "dynamic_record", "dynamic_state"} {
		var read, write, insert, remove bool
		err = s.pool.QueryRow(ctx, "SELECT has_any_column_privilege(session_user,$1,'SELECT'),has_any_column_privilege(session_user,$1,'UPDATE'),has_any_column_privilege(session_user,$1,'INSERT'),has_table_privilege(session_user,$1,'DELETE,TRUNCATE,TRIGGER')", s.schema+"."+table).Scan(&read, &write, &insert, &remove)
		if err != nil {
			return safe(err)
		}
		writer := s.kind == "DYNAMIC"
		writeAllowed := writer && table == "dynamic_state"
		insertAllowed := writer && table != "worker_grant"
		if remove || !read || write && !writeAllowed || insert && !insertAllowed {
			return d.Fail("STORAGE_ROLE_NOT_ISOLATED", 403)
		}
	}
	return nil
}
