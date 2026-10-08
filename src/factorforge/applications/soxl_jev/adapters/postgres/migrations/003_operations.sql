CREATE TABLE IF NOT EXISTS __SCHEMA__.operation_fact (
 instance_id text NOT NULL,operation_id text NOT NULL,worker_kind text NOT NULL CHECK(worker_kind IN('INGEST','RESEARCH','TRADING')),
 checked_at timestamptz NOT NULL,payload bytea NOT NULL,PRIMARY KEY(instance_id,operation_id)
);
CREATE TABLE IF NOT EXISTS __SCHEMA__.framework_report (
 instance_id text NOT NULL,report_id text NOT NULL,recorded_at timestamptz NOT NULL,payload bytea NOT NULL,PRIMARY KEY(instance_id,report_id)
);
ALTER TABLE __SCHEMA__.operation_fact ENABLE ROW LEVEL SECURITY;
ALTER TABLE __SCHEMA__.operation_fact FORCE ROW LEVEL SECURITY;
DROP POLICY IF EXISTS operation_read ON __SCHEMA__.operation_fact;
CREATE POLICY operation_read ON __SCHEMA__.operation_fact FOR SELECT USING(__SCHEMA__.scope_allows(instance_id,worker_kind));
DROP POLICY IF EXISTS operation_write ON __SCHEMA__.operation_fact;
CREATE POLICY operation_write ON __SCHEMA__.operation_fact FOR INSERT WITH CHECK(EXISTS(
 SELECT 1 FROM __SCHEMA__.worker_grant g WHERE g.authenticated_role=session_user AND g.instance_id=operation_fact.instance_id AND g.worker_kind=operation_fact.worker_kind));
ALTER TABLE __SCHEMA__.framework_report ENABLE ROW LEVEL SECURITY;
ALTER TABLE __SCHEMA__.framework_report FORCE ROW LEVEL SECURITY;
DROP POLICY IF EXISTS report_scope ON __SCHEMA__.framework_report;
CREATE POLICY report_scope ON __SCHEMA__.framework_report USING(EXISTS(
 SELECT 1 FROM __SCHEMA__.worker_grant g WHERE g.authenticated_role=session_user AND g.instance_id=framework_report.instance_id AND g.worker_kind='INGEST'));
DROP TRIGGER IF EXISTS immutable ON __SCHEMA__.operation_fact;
CREATE TRIGGER immutable BEFORE UPDATE OR DELETE ON __SCHEMA__.operation_fact FOR EACH ROW EXECUTE FUNCTION __SCHEMA__.immutable_evidence();
DROP TRIGGER IF EXISTS immutable ON __SCHEMA__.framework_report;
CREATE TRIGGER immutable BEFORE UPDATE OR DELETE ON __SCHEMA__.framework_report FOR EACH ROW EXECUTE FUNCTION __SCHEMA__.immutable_evidence();
