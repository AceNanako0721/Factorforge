-- v2.10.0 (P3 design 29.1): read-only DYNAMIC_READ identity for the instance
-- read API. It may only SELECT dynamic records and state of its own instance.
ALTER TABLE __SCHEMA__.worker_grant DROP CONSTRAINT IF EXISTS worker_grant_worker_kind_check;
ALTER TABLE __SCHEMA__.worker_grant ADD CONSTRAINT worker_grant_worker_kind_check CHECK(worker_kind IN ('INGEST','RESEARCH','TRADING','DYNAMIC','DYNAMIC_READ'));
DROP POLICY IF EXISTS dynamic_read_scope ON __SCHEMA__.dynamic_record;
CREATE POLICY dynamic_read_scope ON __SCHEMA__.dynamic_record FOR SELECT USING(EXISTS(
 SELECT 1 FROM __SCHEMA__.worker_grant g WHERE g.authenticated_role=session_user AND g.instance_id=dynamic_record.instance_id AND g.worker_kind='DYNAMIC_READ'));
DROP POLICY IF EXISTS dynamic_read_scope ON __SCHEMA__.dynamic_state;
CREATE POLICY dynamic_read_scope ON __SCHEMA__.dynamic_state FOR SELECT USING(EXISTS(
 SELECT 1 FROM __SCHEMA__.worker_grant g WHERE g.authenticated_role=session_user AND g.instance_id=dynamic_state.instance_id AND g.worker_kind='DYNAMIC_READ'));
