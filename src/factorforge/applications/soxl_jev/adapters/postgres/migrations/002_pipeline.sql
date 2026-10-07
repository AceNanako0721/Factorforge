-- Private pipeline schemas are separate from the published read projection.
-- Administrative initialization binds login roles; workers cannot alter grants.
CREATE SCHEMA IF NOT EXISTS __SCHEMA__;
CREATE TABLE IF NOT EXISTS __SCHEMA__.worker_grant (
 authenticated_role text PRIMARY KEY, instance_id text NOT NULL,
 environment text NOT NULL CHECK(environment='__ENV__'),
 worker_kind text NOT NULL CHECK(worker_kind IN ('INGEST','RESEARCH','TRADING'))
);
CREATE TABLE IF NOT EXISTS __SCHEMA__.budget (
 instance_id text NOT NULL, queue_kind text NOT NULL CHECK(queue_kind IN ('RESEARCH','__ENV__')),
 bucket text NOT NULL, max_jobs bigint NOT NULL CHECK(max_jobs>0),
 max_concurrent bigint NOT NULL CHECK(max_concurrent>0), used_jobs bigint NOT NULL CHECK(used_jobs>=0),
 valid_from timestamptz NOT NULL, valid_until timestamptz NOT NULL CHECK(valid_until>valid_from),
 policy_ref text NOT NULL, PRIMARY KEY(instance_id,queue_kind,bucket)
);
CREATE TABLE IF NOT EXISTS __SCHEMA__.raw_evidence_manifest (
 instance_id text NOT NULL,evidence_id text NOT NULL,content_hash text NOT NULL,payload bytea NOT NULL,
 PRIMARY KEY(instance_id,evidence_id)
);
CREATE TABLE IF NOT EXISTS __SCHEMA__.routing_receipt (
 instance_id text NOT NULL,routing_id text NOT NULL,payload bytea NOT NULL,
 PRIMARY KEY(instance_id,routing_id)
);
CREATE TABLE IF NOT EXISTS __SCHEMA__.app_research_analysis_job (
 instance_id text NOT NULL,job_id text NOT NULL,budget_bucket text NOT NULL,
 state text NOT NULL CHECK(state IN('QUEUED','CLAIMED','RUNNING','COMPLETED','ABSTAINED','EXPIRED','FAILED')),
 created_at timestamptz NOT NULL,deadline timestamptz NOT NULL,claimed_by text,
 lease_until timestamptz,candidate_present boolean NOT NULL DEFAULT false,payload bytea NOT NULL,
 PRIMARY KEY(instance_id,job_id)
);
CREATE TABLE IF NOT EXISTS __SCHEMA__.app_trading_analysis_job
 (LIKE __SCHEMA__.app_research_analysis_job INCLUDING ALL);
CREATE TABLE IF NOT EXISTS __SCHEMA__.research_submission_outbox (
 instance_id text NOT NULL,outbox_id text NOT NULL,job_id text NOT NULL,
 candidate_hash text NOT NULL,state text NOT NULL,expires_at timestamptz NOT NULL,payload bytea NOT NULL,
 PRIMARY KEY(instance_id,outbox_id),UNIQUE(instance_id,job_id)
);
CREATE TABLE IF NOT EXISTS __SCHEMA__.trading_submission_outbox
 (LIKE __SCHEMA__.research_submission_outbox INCLUDING ALL);

CREATE OR REPLACE FUNCTION __SCHEMA__.scope_allows(id text,kind text) RETURNS boolean
 LANGUAGE sql STABLE AS $$ SELECT EXISTS(SELECT 1 FROM __SCHEMA__.worker_grant g
 WHERE g.authenticated_role=session_user AND g.instance_id=id AND
 (g.worker_kind='INGEST' OR g.worker_kind=kind)) $$;
ALTER TABLE __SCHEMA__.worker_grant ENABLE ROW LEVEL SECURITY;
ALTER TABLE __SCHEMA__.worker_grant FORCE ROW LEVEL SECURITY;
DROP POLICY IF EXISTS own_grant ON __SCHEMA__.worker_grant;
CREATE POLICY own_grant ON __SCHEMA__.worker_grant FOR SELECT USING(authenticated_role=session_user);
ALTER TABLE __SCHEMA__.raw_evidence_manifest ENABLE ROW LEVEL SECURITY;
ALTER TABLE __SCHEMA__.raw_evidence_manifest FORCE ROW LEVEL SECURITY;
DROP POLICY IF EXISTS scope ON __SCHEMA__.raw_evidence_manifest;
CREATE POLICY scope ON __SCHEMA__.raw_evidence_manifest USING(__SCHEMA__.scope_allows(instance_id,'RESEARCH') OR __SCHEMA__.scope_allows(instance_id,'TRADING'));
ALTER TABLE __SCHEMA__.routing_receipt ENABLE ROW LEVEL SECURITY;
ALTER TABLE __SCHEMA__.routing_receipt FORCE ROW LEVEL SECURITY;
DROP POLICY IF EXISTS scope ON __SCHEMA__.routing_receipt;
CREATE POLICY scope ON __SCHEMA__.routing_receipt USING(__SCHEMA__.scope_allows(instance_id,'RESEARCH') OR __SCHEMA__.scope_allows(instance_id,'TRADING'));
ALTER TABLE __SCHEMA__.budget ENABLE ROW LEVEL SECURITY;
ALTER TABLE __SCHEMA__.budget FORCE ROW LEVEL SECURITY;
DROP POLICY IF EXISTS scope ON __SCHEMA__.budget;
CREATE POLICY scope ON __SCHEMA__.budget USING(__SCHEMA__.scope_allows(instance_id,CASE WHEN queue_kind='RESEARCH' THEN 'RESEARCH' ELSE 'TRADING' END));
ALTER TABLE __SCHEMA__.app_research_analysis_job ENABLE ROW LEVEL SECURITY;
ALTER TABLE __SCHEMA__.app_research_analysis_job FORCE ROW LEVEL SECURITY;
DROP POLICY IF EXISTS scope ON __SCHEMA__.app_research_analysis_job;
CREATE POLICY scope ON __SCHEMA__.app_research_analysis_job USING(__SCHEMA__.scope_allows(instance_id,'RESEARCH'));
ALTER TABLE __SCHEMA__.app_trading_analysis_job ENABLE ROW LEVEL SECURITY;
ALTER TABLE __SCHEMA__.app_trading_analysis_job FORCE ROW LEVEL SECURITY;
DROP POLICY IF EXISTS scope ON __SCHEMA__.app_trading_analysis_job;
CREATE POLICY scope ON __SCHEMA__.app_trading_analysis_job USING(__SCHEMA__.scope_allows(instance_id,'TRADING'));
ALTER TABLE __SCHEMA__.research_submission_outbox ENABLE ROW LEVEL SECURITY;
ALTER TABLE __SCHEMA__.research_submission_outbox FORCE ROW LEVEL SECURITY;
DROP POLICY IF EXISTS scope ON __SCHEMA__.research_submission_outbox;
CREATE POLICY scope ON __SCHEMA__.research_submission_outbox USING(__SCHEMA__.scope_allows(instance_id,'RESEARCH'));
ALTER TABLE __SCHEMA__.trading_submission_outbox ENABLE ROW LEVEL SECURITY;
ALTER TABLE __SCHEMA__.trading_submission_outbox FORCE ROW LEVEL SECURITY;
DROP POLICY IF EXISTS scope ON __SCHEMA__.trading_submission_outbox;
CREATE POLICY scope ON __SCHEMA__.trading_submission_outbox USING(__SCHEMA__.scope_allows(instance_id,'TRADING'));
CREATE OR REPLACE FUNCTION __SCHEMA__.immutable_evidence() RETURNS trigger
 LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'PIPELINE_EVIDENCE_IMMUTABLE'; END $$;
DROP TRIGGER IF EXISTS immutable ON __SCHEMA__.raw_evidence_manifest;
CREATE TRIGGER immutable BEFORE UPDATE OR DELETE ON __SCHEMA__.raw_evidence_manifest FOR EACH ROW EXECUTE FUNCTION __SCHEMA__.immutable_evidence();
DROP TRIGGER IF EXISTS immutable ON __SCHEMA__.routing_receipt;
CREATE TRIGGER immutable BEFORE UPDATE OR DELETE ON __SCHEMA__.routing_receipt FOR EACH ROW EXECUTE FUNCTION __SCHEMA__.immutable_evidence();

-- Worker identities may advance delivery state, but may not rewrite the
-- evidence, command, deadline, budget partition or an already saved candidate.
CREATE OR REPLACE FUNCTION __SCHEMA__.immutable_job() RETURNS trigger
 LANGUAGE plpgsql AS $$ DECLARE old_body jsonb; new_body jsonb;
 BEGIN
 old_body := convert_from(OLD.payload,'UTF8')::jsonb;
 new_body := convert_from(NEW.payload,'UTF8')::jsonb;
 IF (NEW.instance_id,NEW.job_id,NEW.budget_bucket,NEW.created_at,NEW.deadline)
 IS DISTINCT FROM (OLD.instance_id,OLD.job_id,OLD.budget_bucket,OLD.created_at,OLD.deadline)
 OR new_body->'request' IS DISTINCT FROM old_body->'request'
 OR new_body->'binding' IS DISTINCT FROM old_body->'binding'
 OR new_body->'queue_kind' IS DISTINCT FROM old_body->'queue_kind'
 OR (old_body->'candidate'<>'null'::jsonb AND new_body->'candidate' IS DISTINCT FROM old_body->'candidate')
 THEN RAISE EXCEPTION 'PIPELINE_JOB_IMMUTABLE'; END IF;
 RETURN NEW; END $$;
DROP TRIGGER IF EXISTS immutable ON __SCHEMA__.app_research_analysis_job;
CREATE TRIGGER immutable BEFORE UPDATE ON __SCHEMA__.app_research_analysis_job FOR EACH ROW EXECUTE FUNCTION __SCHEMA__.immutable_job();
DROP TRIGGER IF EXISTS immutable ON __SCHEMA__.app_trading_analysis_job;
CREATE TRIGGER immutable BEFORE UPDATE ON __SCHEMA__.app_trading_analysis_job FOR EACH ROW EXECUTE FUNCTION __SCHEMA__.immutable_job();
CREATE OR REPLACE FUNCTION __SCHEMA__.immutable_outbox() RETURNS trigger
 LANGUAGE plpgsql AS $$ DECLARE old_body jsonb; new_body jsonb;
 BEGIN
 old_body := convert_from(OLD.payload,'UTF8')::jsonb;
 new_body := convert_from(NEW.payload,'UTF8')::jsonb;
 IF (NEW.instance_id,NEW.outbox_id,NEW.job_id,NEW.candidate_hash,NEW.expires_at)
 IS DISTINCT FROM (OLD.instance_id,OLD.outbox_id,OLD.job_id,OLD.candidate_hash,OLD.expires_at)
 OR (new_body-'delivery_state'-'receipt') IS DISTINCT FROM (old_body-'delivery_state'-'receipt')
 OR (OLD.state NOT IN ('PENDING','DELIVERY_UNKNOWN') AND NEW.payload IS DISTINCT FROM OLD.payload)
 THEN RAISE EXCEPTION 'PIPELINE_OUTBOX_IMMUTABLE'; END IF;
 RETURN NEW; END $$;
DROP TRIGGER IF EXISTS immutable ON __SCHEMA__.research_submission_outbox;
CREATE TRIGGER immutable BEFORE UPDATE ON __SCHEMA__.research_submission_outbox FOR EACH ROW EXECUTE FUNCTION __SCHEMA__.immutable_outbox();
DROP TRIGGER IF EXISTS immutable ON __SCHEMA__.trading_submission_outbox;
CREATE TRIGGER immutable BEFORE UPDATE ON __SCHEMA__.trading_submission_outbox FOR EACH ROW EXECUTE FUNCTION __SCHEMA__.immutable_outbox();
