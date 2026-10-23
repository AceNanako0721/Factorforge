-- v2.13.0 (P3 design 34.2): the judgment follower keeps its cursor under the
-- FOLLOW state key; 007 only admitted INJECTION, POSITION and NEWS_SOURCES.
ALTER TABLE __SCHEMA__.dynamic_state DROP CONSTRAINT IF EXISTS dynamic_state_state_key_check;
ALTER TABLE __SCHEMA__.dynamic_state ADD CONSTRAINT dynamic_state_state_key_check CHECK(state_key IN ('INJECTION','POSITION','NEWS_SOURCES','FOLLOW'));
