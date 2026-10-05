-- Preserve deterministic decimal accumulation and intent ordering while keeping
-- the existing JSONB payload readable by the frozen Python rollback build.
ALTER TABLE __SCHEMA__.trading_run ADD COLUMN IF NOT EXISTS snapshot_text text;
