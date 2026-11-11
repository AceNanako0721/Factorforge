CREATE TABLE IF NOT EXISTS __SCHEMA__.command_dedup_archive (
 instance_id text NOT NULL, idempotency_key text NOT NULL,
 archived_at timestamptz NOT NULL, payload jsonb NOT NULL,
 PRIMARY KEY(instance_id,idempotency_key));
REVOKE ALL ON __SCHEMA__.command_dedup_archive FROM PUBLIC;
DO $$ BEGIN
  IF NOT EXISTS (SELECT 1 FROM pg_trigger WHERE tgrelid='__SCHEMA__.command_dedup_archive'::regclass AND tgname='immutable_command_dedup_archive') THEN
    CREATE TRIGGER immutable_command_dedup_archive BEFORE UPDATE OR DELETE ON __SCHEMA__.command_dedup_archive
    FOR EACH ROW EXECUTE FUNCTION __SCHEMA__.reject_mutation();
  END IF;
END $$;
