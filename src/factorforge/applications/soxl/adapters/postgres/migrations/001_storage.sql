-- Instance storage: judgment and stop-management records plus the login
-- bindings that scope them. Administrative initialization binds login roles;
-- workers cannot alter grants.
-- Every instance of an environment shares this schema and initialization runs
-- while their workers write, so row security, policies, triggers and indexes
-- are created only when missing: altering them takes table locks that block
-- or deadlock those writers.
CREATE SCHEMA IF NOT EXISTS __SCHEMA__;
CREATE TABLE IF NOT EXISTS __SCHEMA__.worker_grant (
 authenticated_role text PRIMARY KEY, instance_id text NOT NULL,
 environment text NOT NULL CHECK(environment='__ENV__'),
 worker_kind text NOT NULL CHECK(worker_kind IN ('DYNAMIC','DYNAMIC_READ'))
);
DO $$ BEGIN
  IF NOT (SELECT relrowsecurity AND relforcerowsecurity FROM pg_class WHERE oid='__SCHEMA__.worker_grant'::regclass) THEN
    ALTER TABLE __SCHEMA__.worker_grant ENABLE ROW LEVEL SECURITY;
    ALTER TABLE __SCHEMA__.worker_grant FORCE ROW LEVEL SECURITY;
  END IF;
  IF NOT EXISTS (SELECT 1 FROM pg_policies WHERE schemaname='__SCHEMA__' AND tablename='worker_grant' AND policyname='own_grant') THEN
    CREATE POLICY own_grant ON __SCHEMA__.worker_grant FOR SELECT USING(authenticated_role=session_user);
  END IF;
END $$;

CREATE OR REPLACE FUNCTION __SCHEMA__.immutable_record() RETURNS trigger
 LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'RECORD_IMMUTABLE'; END $$;

-- Records are append-only; state rows advance only by version compare-and-set.
CREATE TABLE IF NOT EXISTS __SCHEMA__.dynamic_record (
 instance_id text NOT NULL,record_kind text NOT NULL CHECK(record_kind IN ('NEWS','JUDGMENT','ACTION','NOISE','STOP_EVENT','VARIANT')),
 record_id text NOT NULL,recorded_at timestamptz NOT NULL,payload jsonb NOT NULL,
 PRIMARY KEY(instance_id,record_kind,record_id)
);
DO $$ BEGIN
  IF to_regclass('__SCHEMA__.dynamic_record_time') IS NULL THEN
    CREATE INDEX dynamic_record_time ON __SCHEMA__.dynamic_record(instance_id,record_kind,recorded_at);
  END IF;
END $$;
CREATE TABLE IF NOT EXISTS __SCHEMA__.dynamic_state (
 instance_id text NOT NULL,state_key text NOT NULL CHECK(state_key IN ('INJECTION','POSITION','NEWS_SOURCES','FOLLOW')),
 version bigint NOT NULL CHECK(version>0),updated_at timestamptz NOT NULL,payload jsonb NOT NULL,
 PRIMARY KEY(instance_id,state_key)
);
DO $$ BEGIN
  IF NOT (SELECT relrowsecurity AND relforcerowsecurity FROM pg_class WHERE oid='__SCHEMA__.dynamic_record'::regclass) THEN
    ALTER TABLE __SCHEMA__.dynamic_record ENABLE ROW LEVEL SECURITY;
    ALTER TABLE __SCHEMA__.dynamic_record FORCE ROW LEVEL SECURITY;
  END IF;
  IF NOT EXISTS (SELECT 1 FROM pg_policies WHERE schemaname='__SCHEMA__' AND tablename='dynamic_record' AND policyname='dynamic_scope') THEN
    CREATE POLICY dynamic_scope ON __SCHEMA__.dynamic_record USING(EXISTS(
     SELECT 1 FROM __SCHEMA__.worker_grant g WHERE g.authenticated_role=session_user AND g.instance_id=dynamic_record.instance_id AND g.worker_kind='DYNAMIC'));
  END IF;
  IF NOT EXISTS (SELECT 1 FROM pg_policies WHERE schemaname='__SCHEMA__' AND tablename='dynamic_record' AND policyname='dynamic_read_scope') THEN
    CREATE POLICY dynamic_read_scope ON __SCHEMA__.dynamic_record FOR SELECT USING(EXISTS(
     SELECT 1 FROM __SCHEMA__.worker_grant g WHERE g.authenticated_role=session_user AND g.instance_id=dynamic_record.instance_id AND g.worker_kind='DYNAMIC_READ'));
  END IF;
  IF NOT EXISTS (SELECT 1 FROM pg_trigger WHERE tgrelid='__SCHEMA__.dynamic_record'::regclass AND tgname='immutable') THEN
    CREATE TRIGGER immutable BEFORE UPDATE OR DELETE ON __SCHEMA__.dynamic_record FOR EACH ROW EXECUTE FUNCTION __SCHEMA__.immutable_record();
  END IF;
END $$;
DO $$ BEGIN
  IF NOT (SELECT relrowsecurity AND relforcerowsecurity FROM pg_class WHERE oid='__SCHEMA__.dynamic_state'::regclass) THEN
    ALTER TABLE __SCHEMA__.dynamic_state ENABLE ROW LEVEL SECURITY;
    ALTER TABLE __SCHEMA__.dynamic_state FORCE ROW LEVEL SECURITY;
  END IF;
  IF NOT EXISTS (SELECT 1 FROM pg_policies WHERE schemaname='__SCHEMA__' AND tablename='dynamic_state' AND policyname='dynamic_scope') THEN
    CREATE POLICY dynamic_scope ON __SCHEMA__.dynamic_state USING(EXISTS(
     SELECT 1 FROM __SCHEMA__.worker_grant g WHERE g.authenticated_role=session_user AND g.instance_id=dynamic_state.instance_id AND g.worker_kind='DYNAMIC'));
  END IF;
  IF NOT EXISTS (SELECT 1 FROM pg_policies WHERE schemaname='__SCHEMA__' AND tablename='dynamic_state' AND policyname='dynamic_read_scope') THEN
    CREATE POLICY dynamic_read_scope ON __SCHEMA__.dynamic_state FOR SELECT USING(EXISTS(
     SELECT 1 FROM __SCHEMA__.worker_grant g WHERE g.authenticated_role=session_user AND g.instance_id=dynamic_state.instance_id AND g.worker_kind='DYNAMIC_READ'));
  END IF;
END $$;
