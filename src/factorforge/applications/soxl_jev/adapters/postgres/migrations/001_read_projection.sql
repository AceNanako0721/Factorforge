-- Read projection only: no analysis queue, provider credentials or raw prompts.
-- Actual ingest/analysis ownership and tables are implemented in their slice.
CREATE SCHEMA IF NOT EXISTS __SCHEMA__;
CREATE TABLE IF NOT EXISTS __SCHEMA__.instance_read_snapshot (
    instance_id text PRIMARY KEY,
    environment text NOT NULL CHECK (environment = '__ENV__'),
    version bigint NOT NULL CHECK (version >= 0),
    snapshot bytea NOT NULL
);
CREATE TABLE IF NOT EXISTS __SCHEMA__.raw_evidence_content (
    instance_id text NOT NULL REFERENCES __SCHEMA__.instance_read_snapshot(instance_id),
    evidence_id text NOT NULL,
    content_hash text NOT NULL CHECK (content_hash ~ '^[a-f0-9]{64}$'),
    content bytea NOT NULL,
    PRIMARY KEY (instance_id, evidence_id)
);
CREATE TABLE IF NOT EXISTS __SCHEMA__.instance_read_grant (
    authenticated_role text PRIMARY KEY,
    instance_id text NOT NULL REFERENCES __SCHEMA__.instance_read_snapshot(instance_id),
    environment text NOT NULL CHECK (environment = '__ENV__')
);
ALTER TABLE __SCHEMA__.instance_read_grant ENABLE ROW LEVEL SECURITY;
ALTER TABLE __SCHEMA__.instance_read_grant FORCE ROW LEVEL SECURITY;
DROP POLICY IF EXISTS read_own_grant ON __SCHEMA__.instance_read_grant;
CREATE POLICY read_own_grant ON __SCHEMA__.instance_read_grant FOR SELECT
    USING (authenticated_role = session_user);
ALTER TABLE __SCHEMA__.instance_read_snapshot ENABLE ROW LEVEL SECURITY;
ALTER TABLE __SCHEMA__.instance_read_snapshot FORCE ROW LEVEL SECURITY;
DROP POLICY IF EXISTS read_bound_snapshot ON __SCHEMA__.instance_read_snapshot;
CREATE POLICY read_bound_snapshot ON __SCHEMA__.instance_read_snapshot FOR SELECT
    USING (EXISTS(SELECT 1 FROM __SCHEMA__.instance_read_grant g
      WHERE g.authenticated_role=session_user AND g.instance_id=instance_read_snapshot.instance_id));
ALTER TABLE __SCHEMA__.raw_evidence_content ENABLE ROW LEVEL SECURITY;
ALTER TABLE __SCHEMA__.raw_evidence_content FORCE ROW LEVEL SECURITY;
DROP POLICY IF EXISTS read_bound_original ON __SCHEMA__.raw_evidence_content;
CREATE POLICY read_bound_original ON __SCHEMA__.raw_evidence_content FOR SELECT
    USING (EXISTS(SELECT 1 FROM __SCHEMA__.instance_read_grant g
      WHERE g.authenticated_role=session_user AND g.instance_id=raw_evidence_content.instance_id));
CREATE OR REPLACE FUNCTION __SCHEMA__.monotonic_read_snapshot() RETURNS trigger
LANGUAGE plpgsql AS $$ BEGIN
  IF NEW.instance_id<>OLD.instance_id OR NEW.environment<>OLD.environment OR NEW.version<>OLD.version+1
    THEN RAISE EXCEPTION 'INSTANCE_VERSION_CONFLICT'; END IF;
  RETURN NEW;
END $$;
DROP TRIGGER IF EXISTS monotonic_read_snapshot ON __SCHEMA__.instance_read_snapshot;
CREATE TRIGGER monotonic_read_snapshot BEFORE UPDATE ON __SCHEMA__.instance_read_snapshot
    FOR EACH ROW EXECUTE FUNCTION __SCHEMA__.monotonic_read_snapshot();
CREATE OR REPLACE FUNCTION __SCHEMA__.immutable_original() RETURNS trigger
LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'ORIGINAL_IMMUTABLE'; END $$;
DROP TRIGGER IF EXISTS immutable_original ON __SCHEMA__.raw_evidence_content;
-- Explicit administrative retention may remove content; the metadata remains.
CREATE TRIGGER immutable_original BEFORE UPDATE ON __SCHEMA__.raw_evidence_content
    FOR EACH ROW EXECUTE FUNCTION __SCHEMA__.immutable_original();
