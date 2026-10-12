-- Accepted candles and all their revisions remain immutable outside the run.
CREATE TABLE IF NOT EXISTS __SCHEMA__.market_candle (
    account_id text NOT NULL REFERENCES __SCHEMA__.trading_run(account_id),
    instrument_key text NOT NULL,
    interval text NOT NULL,
    open_at timestamptz NOT NULL,
    revision bigint NOT NULL CHECK (revision >= 0),
    payload jsonb NOT NULL,
    PRIMARY KEY (account_id, instrument_key, interval, open_at, revision)
);
DROP TRIGGER IF EXISTS immutable_candle ON __SCHEMA__.market_candle;
CREATE TRIGGER immutable_candle BEFORE UPDATE OR DELETE ON __SCHEMA__.market_candle
FOR EACH ROW EXECUTE FUNCTION __SCHEMA__.reject_audit_mutation();
