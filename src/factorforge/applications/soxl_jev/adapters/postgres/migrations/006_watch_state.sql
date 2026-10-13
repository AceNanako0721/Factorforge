CREATE TABLE IF NOT EXISTS __SCHEMA__.watch_seen (
 instance_id text NOT NULL,seen_id text NOT NULL,recorded_at timestamptz NOT NULL,
 PRIMARY KEY(instance_id,seen_id)
);
ALTER TABLE __SCHEMA__.watch_seen ENABLE ROW LEVEL SECURITY;
ALTER TABLE __SCHEMA__.watch_seen FORCE ROW LEVEL SECURITY;
DROP POLICY IF EXISTS watch_scope ON __SCHEMA__.watch_seen;
CREATE POLICY watch_scope ON __SCHEMA__.watch_seen USING(EXISTS(
 SELECT 1 FROM __SCHEMA__.worker_grant g WHERE g.authenticated_role=session_user AND g.instance_id=watch_seen.instance_id AND g.worker_kind='INGEST'));
DROP TRIGGER IF EXISTS immutable ON __SCHEMA__.watch_seen;
CREATE TRIGGER immutable BEFORE UPDATE OR DELETE ON __SCHEMA__.watch_seen FOR EACH ROW EXECUTE FUNCTION __SCHEMA__.immutable_evidence();
