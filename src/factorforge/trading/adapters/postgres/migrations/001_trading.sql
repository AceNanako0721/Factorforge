-- Run with a schema-owner/migration role, never an API role.
-- __SCHEMA__ and __ENVIRONMENT__ are fixed enums chosen by the installer.
CREATE SCHEMA IF NOT EXISTS __SCHEMA__;
REVOKE ALL ON SCHEMA __SCHEMA__ FROM PUBLIC;
CREATE TABLE IF NOT EXISTS __SCHEMA__.trading_run (
    account_id text PRIMARY KEY,
    run_id text NOT NULL,
    environment text NOT NULL CHECK (environment = '__ENVIRONMENT__'),
    version bigint NOT NULL CHECK (version >= 0),
    payload jsonb NOT NULL
);
CREATE TABLE IF NOT EXISTS __SCHEMA__.audit_event (
    account_id text NOT NULL REFERENCES __SCHEMA__.trading_run(account_id),
    sequence bigint NOT NULL,
    payload jsonb NOT NULL,
    PRIMARY KEY (account_id, sequence)
);
CREATE OR REPLACE FUNCTION __SCHEMA__.reject_audit_mutation() RETURNS trigger
LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'immutable audit'; END; $$;
DROP TRIGGER IF EXISTS immutable_audit ON __SCHEMA__.audit_event;
CREATE TRIGGER immutable_audit BEFORE UPDATE OR DELETE ON __SCHEMA__.audit_event
FOR EACH ROW EXECUTE FUNCTION __SCHEMA__.reject_audit_mutation();
-- Domain records are kept as versioned JSON, in separate inspectable relations.
-- Whole-account row locking makes their updates atomic with the account snapshot.
