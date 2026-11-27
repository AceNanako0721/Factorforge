-- Operations agent records (S6-003, S6-006, S6-007). __SCHEMA__ is replaced
-- by the configured schema name.
CREATE SCHEMA IF NOT EXISTS __SCHEMA__;

CREATE TABLE IF NOT EXISTS __SCHEMA__.check_record (
  id            bigserial PRIMARY KEY,
  instance      text        NOT NULL,
  from_at       timestamptz NOT NULL,
  to_at         timestamptz NOT NULL,
  sweeps        integer     NOT NULL,
  rules_version text        NOT NULL,
  hit_key       text        NOT NULL,
  hits          jsonb       NOT NULL
);
CREATE INDEX IF NOT EXISTS check_record_instance ON __SCHEMA__.check_record (instance, to_at DESC);

CREATE SEQUENCE IF NOT EXISTS __SCHEMA__.issue_seq;
CREATE TABLE IF NOT EXISTS __SCHEMA__.issue (
  id          text PRIMARY KEY,
  title       text        NOT NULL,
  severity    text        NOT NULL,
  state       text        NOT NULL,
  source      text        NOT NULL,
  rule        text        NOT NULL DEFAULT '',
  instances   jsonb       NOT NULL,
  first_seen  timestamptz NOT NULL,
  last_seen   timestamptz NOT NULL,
  occurrences integer     NOT NULL,
  fix_version text        NOT NULL DEFAULT '',
  verified    boolean     NOT NULL DEFAULT false,
  legacy_id   text        NOT NULL DEFAULT ''
);
CREATE INDEX IF NOT EXISTS issue_open_source ON __SCHEMA__.issue (source) WHERE state <> 'CLOSED';

CREATE TABLE IF NOT EXISTS __SCHEMA__.issue_entry (
  id          bigserial PRIMARY KEY,
  issue       text        NOT NULL REFERENCES __SCHEMA__.issue (id),
  at          timestamptz NOT NULL,
  actor       text        NOT NULL,
  kind        text        NOT NULL,
  from_state  text        NOT NULL DEFAULT '',
  to_state    text        NOT NULL DEFAULT '',
  body        text        NOT NULL DEFAULT '',
  owner_words text        NOT NULL DEFAULT '',
  evidence    jsonb
);
CREATE INDEX IF NOT EXISTS issue_entry_issue ON __SCHEMA__.issue_entry (issue, id);

CREATE TABLE IF NOT EXISTS __SCHEMA__.report (
  id       bigserial PRIMARY KEY,
  at       timestamptz NOT NULL,
  actor    text        NOT NULL,
  summary  text        NOT NULL,
  findings jsonb       NOT NULL,
  handled  jsonb       NOT NULL,
  issues   jsonb       NOT NULL
);

CREATE TABLE IF NOT EXISTS __SCHEMA__.owner_decision (
  id        bigserial PRIMARY KEY,
  at        timestamptz NOT NULL,
  actor     text        NOT NULL,
  words     text        NOT NULL,
  summary   text        NOT NULL,
  instances jsonb       NOT NULL,
  issues    jsonb       NOT NULL
);

CREATE SEQUENCE IF NOT EXISTS __SCHEMA__.exclusion_seq;
CREATE TABLE IF NOT EXISTS __SCHEMA__.exclusion (
  id       text PRIMARY KEY,
  instance text        NOT NULL,
  from_at  timestamptz NOT NULL,
  to_at    timestamptz NOT NULL,
  reason   text        NOT NULL,
  issue    text        NOT NULL DEFAULT ''
);

CREATE TABLE IF NOT EXISTS __SCHEMA__.tool_audit (
  id      bigserial PRIMARY KEY,
  at      timestamptz NOT NULL,
  profile text        NOT NULL,
  tool    text        NOT NULL,
  args    text        NOT NULL,
  result  text        NOT NULL
);
