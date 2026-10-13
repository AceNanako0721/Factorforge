CREATE TABLE IF NOT EXISTS __SCHEMA__.factor_observation_archive (
 instance_id text NOT NULL, factor_id text NOT NULL, observation_id text NOT NULL,
 archived_at timestamptz NOT NULL, payload jsonb NOT NULL,
 PRIMARY KEY(instance_id,factor_id,observation_id));
REVOKE ALL ON __SCHEMA__.factor_observation_archive FROM PUBLIC;
DROP TRIGGER IF EXISTS immutable_factor_observation_archive ON __SCHEMA__.factor_observation_archive;
CREATE TRIGGER immutable_factor_observation_archive BEFORE UPDATE OR DELETE ON __SCHEMA__.factor_observation_archive
FOR EACH ROW EXECUTE FUNCTION __SCHEMA__.reject_mutation();
