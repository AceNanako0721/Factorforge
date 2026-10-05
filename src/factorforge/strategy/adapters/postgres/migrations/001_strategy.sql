CREATE SCHEMA IF NOT EXISTS __SCHEMA__;
REVOKE ALL ON SCHEMA __SCHEMA__ FROM PUBLIC;
DO $$ BEGIN
  CREATE TYPE __SCHEMA__.api_scope AS ENUM ('query','score:research','object:write');
EXCEPTION WHEN duplicate_object THEN NULL; END $$;
DO $$ BEGIN
  CREATE TYPE __SCHEMA__.workload_capability AS ENUM ('signal:__ENV_LOWER__');
EXCEPTION WHEN duplicate_object THEN NULL; END $$;
CREATE TABLE IF NOT EXISTS __SCHEMA__.public_state (
  instance_id text PRIMARY KEY, payload jsonb NOT NULL);
CREATE TABLE IF NOT EXISTS __SCHEMA__.internal_state (
  instance_id text PRIMARY KEY REFERENCES __SCHEMA__.public_state(instance_id), payload jsonb NOT NULL);
CREATE TABLE IF NOT EXISTS __SCHEMA__.api_key_scope (
  key_id text NOT NULL, scope __SCHEMA__.api_scope NOT NULL, PRIMARY KEY(key_id,scope));
CREATE TABLE IF NOT EXISTS __SCHEMA__.object_owner_binding (
  instance_id text NOT NULL, object_id text NOT NULL, account_id text NOT NULL,
  run_id text NOT NULL, venue text NOT NULL, product text NOT NULL,
  instrument_id text NOT NULL, owner_id text NOT NULL,
  PRIMARY KEY(instance_id,object_id), UNIQUE(account_id,run_id,venue,product,instrument_id));
CREATE TABLE IF NOT EXISTS __SCHEMA__.workload_capability_grant (
  workload_id text NOT NULL, instance_id text NOT NULL, object_id text NOT NULL,
  capability __SCHEMA__.workload_capability NOT NULL,
  PRIMARY KEY(workload_id,instance_id,object_id,capability));
CREATE TABLE IF NOT EXISTS __SCHEMA__.audit_event (
  instance_id text NOT NULL, sequence bigint NOT NULL, payload jsonb NOT NULL,
  PRIMARY KEY(instance_id,sequence));
CREATE TABLE IF NOT EXISTS __SCHEMA__.sentiment_entry (
  instance_id text NOT NULL, sequence bigint NOT NULL, payload jsonb NOT NULL,
  PRIMARY KEY(instance_id,sequence));
REVOKE ALL ON ALL TABLES IN SCHEMA __SCHEMA__ FROM PUBLIC;
CREATE OR REPLACE FUNCTION __SCHEMA__.reject_mutation() RETURNS trigger
LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'immutable strategy record'; END; $$;
DROP TRIGGER IF EXISTS immutable_audit ON __SCHEMA__.audit_event;
CREATE TRIGGER immutable_audit BEFORE UPDATE OR DELETE ON __SCHEMA__.audit_event
FOR EACH ROW EXECUTE FUNCTION __SCHEMA__.reject_mutation();
DROP TRIGGER IF EXISTS immutable_ledger ON __SCHEMA__.sentiment_entry;
CREATE TRIGGER immutable_ledger BEFORE UPDATE OR DELETE ON __SCHEMA__.sentiment_entry
FOR EACH ROW EXECUTE FUNCTION __SCHEMA__.reject_mutation();
