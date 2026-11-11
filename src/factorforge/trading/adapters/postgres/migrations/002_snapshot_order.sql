-- Preserve deterministic decimal accumulation and intent ordering while keeping
-- the existing JSONB payload readable by the frozen Python rollback build.
DO $$ BEGIN
  IF NOT EXISTS (SELECT 1 FROM pg_attribute WHERE attrelid='__SCHEMA__.trading_run'::regclass AND attname='snapshot_text' AND NOT attisdropped) THEN
    ALTER TABLE __SCHEMA__.trading_run ADD COLUMN snapshot_text text;
  END IF;
END $$;
