CREATE SCHEMA IF NOT EXISTS instance_provider_control;
REVOKE ALL ON SCHEMA instance_provider_control FROM PUBLIC;
CREATE TABLE IF NOT EXISTS instance_provider_control.pool (
 pool_id text PRIMARY KEY, paused boolean NOT NULL DEFAULT false, next_start timestamptz
);
CREATE TABLE IF NOT EXISTS instance_provider_control.policy (
 pool_id text NOT NULL REFERENCES instance_provider_control.pool,
 version text NOT NULL, valid_from timestamptz NOT NULL, valid_until timestamptz NOT NULL,
 payload jsonb NOT NULL, PRIMARY KEY(pool_id,version), CHECK(valid_until>valid_from)
);
CREATE TABLE IF NOT EXISTS instance_provider_control.worker_grant (
 authenticated_role text PRIMARY KEY, pool_id text NOT NULL REFERENCES instance_provider_control.pool,
 instance_id text NOT NULL, environment text NOT NULL CHECK(environment IN ('SIM','LIVE')),
 queue_kind text NOT NULL CHECK(queue_kind IN ('RESEARCH','SIM','LIVE')),
 CHECK(queue_kind='RESEARCH' OR queue_kind=environment)
);
CREATE TABLE IF NOT EXISTS instance_provider_control.call (
 pool_id text NOT NULL, environment text NOT NULL, instance_id text NOT NULL, request_id text NOT NULL,
 request_hash text NOT NULL, request_bytes bigint NOT NULL CHECK(request_bytes>0), queue_kind text NOT NULL,
 policy_version text NOT NULL, started_at timestamptz NOT NULL, finished_at timestamptz,
 state text NOT NULL CHECK(state IN ('STARTED','COMPLETE','UNKNOWN','RATE_LIMITED')),
 PRIMARY KEY(pool_id,environment,instance_id,request_id),
 FOREIGN KEY(pool_id,policy_version) REFERENCES instance_provider_control.policy(pool_id,version)
);
CREATE INDEX IF NOT EXISTS provider_call_active ON instance_provider_control.call(pool_id,queue_kind,state);
CREATE INDEX IF NOT EXISTS provider_call_policy ON instance_provider_control.call(pool_id,policy_version,queue_kind);
REVOKE ALL ON ALL TABLES IN SCHEMA instance_provider_control FROM PUBLIC;

CREATE OR REPLACE FUNCTION instance_provider_control.acquire(
 p_instance text,p_environment text,p_kind text,p_request text,p_hash text,p_bytes bigint,p_deadline timestamptz
) RETURNS text LANGUAGE plpgsql SECURITY DEFINER SET search_path=pg_catalog AS $$
DECLARE g instance_provider_control.worker_grant; p instance_provider_control.pool;
 policy instance_provider_control.policy; t timestamptz; n bigint; part jsonb;
BEGIN
 SELECT * INTO g FROM instance_provider_control.worker_grant WHERE authenticated_role=session_user;
 IF NOT FOUND OR (g.instance_id,g.environment,g.queue_kind) IS DISTINCT FROM (p_instance,p_environment,p_kind)
 OR NOT EXISTS(SELECT 1 FROM pg_roles WHERE rolname=session_user AND rolcanlogin AND NOT rolsuper AND NOT rolbypassrls AND NOT rolcreaterole AND NOT rolcreatedb AND NOT rolreplication)
 OR current_setting('role')<>'none'
 OR has_schema_privilege(session_user,'instance_provider_control','CREATE')
 OR EXISTS(SELECT 1 FROM pg_class relation JOIN pg_namespace n ON n.oid=relation.relnamespace WHERE n.nspname='instance_provider_control' AND relation.relkind IN ('r','p') AND has_table_privilege(session_user,relation.oid,'SELECT,INSERT,UPDATE,DELETE,TRUNCATE,REFERENCES,TRIGGER')) THEN RETURN 'PROVIDER_SCOPE_FORBIDDEN'; END IF;
 SELECT * INTO p FROM instance_provider_control.pool WHERE pool_id=g.pool_id FOR UPDATE;
 IF NOT FOUND THEN RETURN 'PROVIDER_CONFIGURATION_REQUIRED'; END IF;
 t:=clock_timestamp();
 IF p_request IS NULL OR p_request!~'^[A-Za-z0-9_.-]{1,128}$' OR p_hash IS NULL OR p_hash!~'^[0-9a-f]{64}$' OR p_bytes IS NULL OR p_bytes<=0 OR p_deadline IS NULL OR p_deadline<=t THEN RETURN 'PROVIDER_REQUEST_INVALID'; END IF;
 IF EXISTS(SELECT 1 FROM instance_provider_control.call WHERE pool_id=g.pool_id AND environment=g.environment AND instance_id=g.instance_id AND request_id=p_request) THEN RETURN 'PROVIDER_CALL_ALREADY_RESERVED'; END IF;
 IF p.paused THEN RETURN 'PROVIDER_POOL_PAUSED'; END IF;
 SELECT count(*) INTO n FROM instance_provider_control.policy WHERE pool_id=g.pool_id AND valid_from<=t AND valid_until>t;
 IF n<>1 THEN RETURN 'PROVIDER_CONFIGURATION_REQUIRED'; END IF;
 SELECT * INTO policy FROM instance_provider_control.policy WHERE pool_id=g.pool_id AND valid_from<=t AND valid_until>t;
 part:=policy.payload->'partitions'->g.queue_kind;
 IF p_bytes>(policy.payload->>'max_request_bytes')::bigint THEN RETURN 'PROVIDER_REQUEST_BUDGET_EXCEEDED'; END IF;
 IF p.next_start IS NOT NULL AND t<p.next_start THEN RETURN 'PROVIDER_START_INTERVAL_LIMIT'; END IF;
 SELECT count(*) INTO n FROM instance_provider_control.call WHERE pool_id=g.pool_id AND policy_version=policy.version;
 IF n>=(policy.payload->>'max_calls')::bigint THEN RETURN 'PROVIDER_CALL_BUDGET_EXHAUSTED'; END IF;
 SELECT count(*) INTO n FROM instance_provider_control.call WHERE pool_id=g.pool_id AND policy_version=policy.version AND queue_kind=g.queue_kind;
 IF n>=(part->>'max_calls')::bigint THEN RETURN 'PROVIDER_CALL_BUDGET_EXHAUSTED'; END IF;
 SELECT count(*) INTO n FROM instance_provider_control.call WHERE pool_id=g.pool_id AND state IN ('STARTED','UNKNOWN');
 IF n>=(policy.payload->>'max_concurrent')::bigint THEN RETURN 'PROVIDER_CONCURRENCY_LIMIT'; END IF;
 SELECT count(*) INTO n FROM instance_provider_control.call WHERE pool_id=g.pool_id AND queue_kind=g.queue_kind AND state IN ('STARTED','UNKNOWN');
 IF n>=(part->>'max_concurrent')::bigint THEN RETURN 'PROVIDER_CONCURRENCY_LIMIT'; END IF;
 INSERT INTO instance_provider_control.call VALUES(g.pool_id,g.environment,g.instance_id,p_request,p_hash,p_bytes,g.queue_kind,policy.version,t,NULL,'STARTED');
 UPDATE instance_provider_control.pool SET next_start=t+(policy.payload->>'min_start_interval_millis')::bigint*interval '1 millisecond' WHERE pool_id=g.pool_id;
 RETURN 'OK';
END $$;

CREATE OR REPLACE FUNCTION instance_provider_control.finish(p_request text,p_hash text,p_outcome text)
RETURNS text LANGUAGE plpgsql SECURITY DEFINER SET search_path=pg_catalog AS $$
DECLARE g instance_provider_control.worker_grant; c instance_provider_control.call;
BEGIN
 SELECT * INTO g FROM instance_provider_control.worker_grant WHERE authenticated_role=session_user;
 IF NOT FOUND OR NOT EXISTS(SELECT 1 FROM pg_roles WHERE rolname=session_user AND rolcanlogin AND NOT rolsuper AND NOT rolbypassrls AND NOT rolcreaterole AND NOT rolcreatedb AND NOT rolreplication)
 OR current_setting('role')<>'none'
 OR has_schema_privilege(session_user,'instance_provider_control','CREATE')
 OR EXISTS(SELECT 1 FROM pg_class relation JOIN pg_namespace n ON n.oid=relation.relnamespace WHERE n.nspname='instance_provider_control' AND relation.relkind IN ('r','p') AND has_table_privilege(session_user,relation.oid,'SELECT,INSERT,UPDATE,DELETE,TRUNCATE,REFERENCES,TRIGGER')) THEN RETURN 'PROVIDER_SCOPE_FORBIDDEN'; END IF;
 PERFORM 1 FROM instance_provider_control.pool WHERE pool_id=g.pool_id FOR UPDATE;
 SELECT * INTO c FROM instance_provider_control.call WHERE pool_id=g.pool_id AND environment=g.environment AND instance_id=g.instance_id AND request_id=p_request;
 IF NOT FOUND OR c.request_hash IS DISTINCT FROM p_hash OR c.queue_kind<>g.queue_kind THEN RETURN 'PROVIDER_RECEIPT_FORBIDDEN'; END IF;
 IF p_outcome IS NULL OR p_outcome NOT IN ('COMPLETE','UNKNOWN','RATE_LIMITED') THEN RETURN 'PROVIDER_REQUEST_INVALID'; END IF;
 IF c.state=p_outcome THEN RETURN 'OK'; END IF;
 IF c.state<>'STARTED' THEN RETURN 'PROVIDER_RECEIPT_IMMUTABLE'; END IF;
 UPDATE instance_provider_control.call SET state=p_outcome,finished_at=clock_timestamp() WHERE pool_id=g.pool_id AND environment=g.environment AND instance_id=g.instance_id AND request_id=p_request;
 IF p_outcome='RATE_LIMITED' THEN UPDATE instance_provider_control.pool SET paused=true WHERE pool_id=g.pool_id; END IF;
 RETURN 'OK';
END $$;
REVOKE ALL ON FUNCTION instance_provider_control.acquire(text,text,text,text,text,bigint,timestamptz),instance_provider_control.finish(text,text,text) FROM PUBLIC;
