-- Public trades are a bounded projection: keep the configured window and
-- delete older rows through a definer function so the API role never gains
-- table-level DELETE (v2.21.2, design 25). The observed_at column is added
-- after the generic relation exists; Initialize applies this file last.
DO $$
BEGIN
    -- Only the first schema run backfills; later runs must not scan the
    -- bounded-but-large projection again.
    IF NOT EXISTS (
        SELECT 1 FROM information_schema.columns
        WHERE table_schema = '__SCHEMA__' AND table_name = 'market_trade' AND column_name = 'observed_at'
    ) THEN
        ALTER TABLE __SCHEMA__.market_trade ADD COLUMN observed_at timestamptz;
        UPDATE __SCHEMA__.market_trade SET observed_at = (payload->>'observed_at')::timestamptz;
        ALTER TABLE __SCHEMA__.market_trade ALTER COLUMN observed_at SET NOT NULL;
    END IF;
END $$;
CREATE INDEX IF NOT EXISTS market_trade_observed_at ON __SCHEMA__.market_trade (observed_at);
-- Hourly pruning creates dead tuples continuously; the default 20% scale
-- factor would vacuum only after millions of deletions. Keep the projection
-- tight with per-table thresholds instead of waiting for a bulk vacuum.
ALTER TABLE __SCHEMA__.market_trade SET (
    autovacuum_vacuum_scale_factor = 0.01,
    autovacuum_vacuum_insert_scale_factor = 0.01,
    autovacuum_analyze_scale_factor = 0.02
);
CREATE OR REPLACE FUNCTION __SCHEMA__.prune_market_trade(before timestamptz, batch integer)
RETURNS bigint
LANGUAGE plpgsql
SECURITY DEFINER
SET search_path = pg_catalog, __SCHEMA__
AS $$
DECLARE
    removed bigint;
BEGIN
    IF before IS NULL OR batch IS NULL OR batch < 1 OR batch > 10000 THEN
        RAISE EXCEPTION 'PRUNE_REQUEST_INVALID';
    END IF;
    -- Safety floor: even a mistaken caller cannot delete the last seven days.
    IF before > now() - interval '7 days' THEN
        RAISE EXCEPTION 'PRUNE_RETENTION_FLOOR';
    END IF;
    DELETE FROM market_trade
    WHERE ctid IN (
        SELECT ctid FROM market_trade
        WHERE observed_at < before
        ORDER BY observed_at
        LIMIT batch
    );
    GET DIAGNOSTICS removed = ROW_COUNT;
    RETURN removed;
END;
$$;
REVOKE ALL ON FUNCTION __SCHEMA__.prune_market_trade(timestamptz, integer) FROM PUBLIC;
