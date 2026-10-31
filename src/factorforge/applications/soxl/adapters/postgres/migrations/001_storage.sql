-- Instance storage: judgment and stop-management records plus the login
-- bindings that scope them. Administrative initialization binds login roles;
-- workers cannot alter grants.
CREATE SCHEMA IF NOT EXISTS __SCHEMA__;
CREATE TABLE IF NOT EXISTS __SCHEMA__.worker_grant (
 authenticated_role text PRIMARY KEY, instance_id text NOT NULL,
 environment text NOT NULL CHECK(environment='__ENV__'),
 worker_kind text NOT NULL CHECK(worker_kind IN ('DYNAMIC','DYNAMIC_READ'))
);
ALTER TABLE __SCHEMA__.worker_grant ENABLE ROW LEVEL SECURITY;
ALTER TABLE __SCHEMA__.worker_grant FORCE ROW LEVEL SECURITY;
DROP POLICY IF EXISTS own_grant ON __SCHEMA__.worker_grant;
CREATE POLICY own_grant ON __SCHEMA__.worker_grant FOR SELECT USING(authenticated_role=session_user);

CREATE OR REPLACE FUNCTION __SCHEMA__.immutable_record() RETURNS trigger
 LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'RECORD_IMMUTABLE'; END $$;

-- Records are append-only; state rows advance only by version compare-and-set.
CREATE TABLE IF NOT EXISTS __SCHEMA__.dynamic_record (
 instance_id text NOT NULL,record_kind text NOT NULL CHECK(record_kind IN ('NEWS','JUDGMENT','ACTION','NOISE','STOP_EVENT','VARIANT')),
 record_id text NOT NULL,recorded_at timestamptz NOT NULL,payload jsonb NOT NULL,
 PRIMARY KEY(instance_id,record_kind,record_id)
);
CREATE INDEX IF NOT EXISTS dynamic_record_time ON __SCHEMA__.dynamic_record(instance_id,record_kind,recorded_at);
CREATE TABLE IF NOT EXISTS __SCHEMA__.dynamic_state (
 instance_id text NOT NULL,state_key text NOT NULL CHECK(state_key IN ('INJECTION','POSITION','NEWS_SOURCES','FOLLOW')),
 version bigint NOT NULL CHECK(version>0),updated_at timestamptz NOT NULL,payload jsonb NOT NULL,
 PRIMARY KEY(instance_id,state_key)
);
ALTER TABLE __SCHEMA__.dynamic_record ENABLE ROW LEVEL SECURITY;
ALTER TABLE __SCHEMA__.dynamic_record FORCE ROW LEVEL SECURITY;
DROP POLICY IF EXISTS dynamic_scope ON __SCHEMA__.dynamic_record;
CREATE POLICY dynamic_scope ON __SCHEMA__.dynamic_record USING(EXISTS(
 SELECT 1 FROM __SCHEMA__.worker_grant g WHERE g.authenticated_role=session_user AND g.instance_id=dynamic_record.instance_id AND g.worker_kind='DYNAMIC'));
DROP POLICY IF EXISTS dynamic_read_scope ON __SCHEMA__.dynamic_record;
CREATE POLICY dynamic_read_scope ON __SCHEMA__.dynamic_record FOR SELECT USING(EXISTS(
 SELECT 1 FROM __SCHEMA__.worker_grant g WHERE g.authenticated_role=session_user AND g.instance_id=dynamic_record.instance_id AND g.worker_kind='DYNAMIC_READ'));
DROP TRIGGER IF EXISTS immutable ON __SCHEMA__.dynamic_record;
CREATE TRIGGER immutable BEFORE UPDATE OR DELETE ON __SCHEMA__.dynamic_record FOR EACH ROW EXECUTE FUNCTION __SCHEMA__.immutable_record();
ALTER TABLE __SCHEMA__.dynamic_state ENABLE ROW LEVEL SECURITY;
ALTER TABLE __SCHEMA__.dynamic_state FORCE ROW LEVEL SECURITY;
DROP POLICY IF EXISTS dynamic_scope ON __SCHEMA__.dynamic_state;
CREATE POLICY dynamic_scope ON __SCHEMA__.dynamic_state USING(EXISTS(
 SELECT 1 FROM __SCHEMA__.worker_grant g WHERE g.authenticated_role=session_user AND g.instance_id=dynamic_state.instance_id AND g.worker_kind='DYNAMIC'));
DROP POLICY IF EXISTS dynamic_read_scope ON __SCHEMA__.dynamic_state;
CREATE POLICY dynamic_read_scope ON __SCHEMA__.dynamic_state FOR SELECT USING(EXISTS(
 SELECT 1 FROM __SCHEMA__.worker_grant g WHERE g.authenticated_role=session_user AND g.instance_id=dynamic_state.instance_id AND g.worker_kind='DYNAMIC_READ'));
