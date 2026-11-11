CREATE TABLE IF NOT EXISTS __SCHEMA__.stop_directive_archive (
 instance_id text NOT NULL,
 directive_id text NOT NULL,
 object_id text NOT NULL,
 archived_at timestamptz NOT NULL DEFAULT now(),
 payload jsonb NOT NULL,
 PRIMARY KEY(instance_id,directive_id)
);
DO $$ BEGIN
  IF to_regclass('__SCHEMA__.stop_directive_object_idx') IS NULL THEN
    CREATE INDEX stop_directive_object_idx ON __SCHEMA__.stop_directive_archive(instance_id,object_id,directive_id);
  END IF;
END $$;
CREATE TABLE IF NOT EXISTS __SCHEMA__.stop_directive_research_archive (LIKE __SCHEMA__.stop_directive_archive INCLUDING ALL);
DO $$ BEGIN
  IF NOT EXISTS (SELECT 1 FROM pg_trigger WHERE tgrelid='__SCHEMA__.stop_directive_archive'::regclass AND tgname='immutable_stop_directive') THEN
    CREATE TRIGGER immutable_stop_directive BEFORE UPDATE OR DELETE ON __SCHEMA__.stop_directive_archive FOR EACH ROW EXECUTE FUNCTION __SCHEMA__.reject_mutation();
  END IF;
END $$;
DO $$ BEGIN
  IF NOT EXISTS (SELECT 1 FROM pg_trigger WHERE tgrelid='__SCHEMA__.stop_directive_research_archive'::regclass AND tgname='immutable_research_stop_directive') THEN
    CREATE TRIGGER immutable_research_stop_directive BEFORE UPDATE OR DELETE ON __SCHEMA__.stop_directive_research_archive FOR EACH ROW EXECUTE FUNCTION __SCHEMA__.reject_mutation();
  END IF;
END $$;
REVOKE ALL ON __SCHEMA__.stop_directive_archive,__SCHEMA__.stop_directive_research_archive FROM PUBLIC;
