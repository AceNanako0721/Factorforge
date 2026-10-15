CREATE TABLE IF NOT EXISTS __SCHEMA__.stop_directive_archive (
 instance_id text NOT NULL,
 directive_id text NOT NULL,
 object_id text NOT NULL,
 archived_at timestamptz NOT NULL DEFAULT now(),
 payload jsonb NOT NULL,
 PRIMARY KEY(instance_id,directive_id)
);
CREATE INDEX IF NOT EXISTS stop_directive_object_idx ON __SCHEMA__.stop_directive_archive(instance_id,object_id,directive_id);
CREATE TABLE IF NOT EXISTS __SCHEMA__.stop_directive_research_archive (LIKE __SCHEMA__.stop_directive_archive INCLUDING ALL);
DROP TRIGGER IF EXISTS immutable_stop_directive ON __SCHEMA__.stop_directive_archive;
CREATE TRIGGER immutable_stop_directive BEFORE UPDATE OR DELETE ON __SCHEMA__.stop_directive_archive FOR EACH ROW EXECUTE FUNCTION __SCHEMA__.reject_mutation();
DROP TRIGGER IF EXISTS immutable_research_stop_directive ON __SCHEMA__.stop_directive_research_archive;
CREATE TRIGGER immutable_research_stop_directive BEFORE UPDATE OR DELETE ON __SCHEMA__.stop_directive_research_archive FOR EACH ROW EXECUTE FUNCTION __SCHEMA__.reject_mutation();
REVOKE ALL ON __SCHEMA__.stop_directive_archive,__SCHEMA__.stop_directive_research_archive FROM PUBLIC;
