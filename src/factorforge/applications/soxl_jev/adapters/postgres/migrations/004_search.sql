CREATE TABLE IF NOT EXISTS __SCHEMA__.search_state (
 instance_id text PRIMARY KEY, payload bytea NOT NULL
);
ALTER TABLE __SCHEMA__.search_state ENABLE ROW LEVEL SECURITY;
ALTER TABLE __SCHEMA__.search_state FORCE ROW LEVEL SECURITY;
DROP POLICY IF EXISTS search_scope ON __SCHEMA__.search_state;
CREATE POLICY search_scope ON __SCHEMA__.search_state USING(EXISTS(
 SELECT 1 FROM __SCHEMA__.worker_grant g
 WHERE g.authenticated_role=session_user AND g.instance_id=search_state.instance_id
 AND g.environment='__ENV__' AND g.worker_kind='INGEST'
)) WITH CHECK(EXISTS(
 SELECT 1 FROM __SCHEMA__.worker_grant g
 WHERE g.authenticated_role=session_user AND g.instance_id=search_state.instance_id
 AND g.environment='__ENV__' AND g.worker_kind='INGEST'
));
