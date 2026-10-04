"""PostgreSQL transactions serialize all writers to one physical account."""
from contextlib import contextmanager
from importlib.resources import files

import psycopg
from psycopg import sql
from psycopg.types.json import Jsonb

from factorforge.trading.domain.errors import TradingError
from factorforge.trading.domain.models import Aggregate

RELATIONS = ("account_policy_version", "instrument_spec", "market_snapshot_ref", "owner_binding",
             "target_request", "order", "protection_order", "fill", "income_entry", "position_lot",
             "position_cycle", "risk_day", "risk_lock", "risk_reservation", "reconciliation_report",
             "command_dedup", "outbox")


def initialize(dsn, environment):
    if environment not in {"SIM", "LIVE"}:
        raise TradingError("ENVIRONMENT_UNSUPPORTED")
    schema = "trading_" + environment.lower()
    script = files(__package__).joinpath("migrations/001_trading.sql").read_text()
    script = script.replace("__SCHEMA__", schema).replace("__ENVIRONMENT__", environment)
    with psycopg.connect(dsn) as connection:
        connection.execute(script)
        for name in RELATIONS:
            connection.execute(sql.SQL("CREATE TABLE IF NOT EXISTS {}.{} (account_id text NOT NULL REFERENCES {}.trading_run(account_id), resource_id text NOT NULL, payload jsonb NOT NULL, PRIMARY KEY(account_id,resource_id))").format(
                sql.Identifier(schema), sql.Identifier(name), sql.Identifier(schema)))
        connection.execute(sql.SQL("REVOKE ALL ON ALL TABLES IN SCHEMA {} FROM PUBLIC").format(sql.Identifier(schema)))
        role = "factorforge_" + environment.lower()
        if not connection.execute("SELECT 1 FROM pg_roles WHERE rolname=%s", (role,)).fetchone():
            connection.execute(sql.SQL("CREATE ROLE {} NOLOGIN").format(sql.Identifier(role)))
        connection.execute(sql.SQL("GRANT USAGE ON SCHEMA {} TO {}").format(sql.Identifier(schema), sql.Identifier(role)))
        connection.execute(sql.SQL("GRANT SELECT, INSERT, UPDATE ON ALL TABLES IN SCHEMA {} TO {}").format(sql.Identifier(schema), sql.Identifier(role)))
        connection.execute(sql.SQL("REVOKE UPDATE, DELETE ON {}.audit_event FROM {}").format(sql.Identifier(schema), sql.Identifier(role)))


class PostgresStore:
    def __init__(self, dsn, environment):
        if environment not in {"SIM", "LIVE"}:
            raise TradingError("ENVIRONMENT_UNSUPPORTED")
        self.dsn, self.environment = dsn, environment
        self.schema = "trading_" + environment.lower()

    def verify_runtime_role(self):
        """A migration/superuser credential must never be accepted by the API/worker."""
        role = "factorforge_" + self.environment.lower()
        opposite = "trading_live" if self.environment == "SIM" else "trading_sim"
        try:
            with psycopg.connect(self.dsn) as connection:
                allowed, superuser = connection.execute("SELECT pg_has_role(current_user,%s,'USAGE'),rolsuper FROM pg_roles WHERE rolname=current_user", (role,)).fetchone()
                exists = connection.execute("SELECT to_regnamespace(%s)", (opposite,)).fetchone()[0]
                cross_access = exists and connection.execute("SELECT has_schema_privilege(current_user,%s,'USAGE')", (opposite,)).fetchone()[0]
                if not allowed or superuser or cross_access:
                    raise TradingError("DATABASE_ROLE_NOT_ISOLATED", 503)
        except psycopg.Error:
            raise TradingError("STORE_UNAVAILABLE", 503) from None

    def _key(self, key):
        if key.environment != self.environment:
            raise TradingError("ENVIRONMENT_FORBIDDEN", 403)

    def _statement(self, text):
        return sql.SQL(text).format(sql.Identifier(self.schema))

    def _load(self, connection, key, lock=False):
        self._key(key)
        query = "SELECT payload FROM {}.trading_run WHERE account_id=%s AND run_id=%s"
        if lock:
            query += " FOR UPDATE"
        row = connection.execute(self._statement(query), (key.account_id, key.run_id)).fetchone()
        if not row:
            raise TradingError("RUN_NOT_FOUND", 404)
        return Aggregate.model_validate(row[0])

    def create(self, aggregate):
        self._key(aggregate.run_key)
        try:
            with psycopg.connect(self.dsn) as connection:
                connection.execute(self._statement("INSERT INTO {}.trading_run(account_id,run_id,environment,version,payload) VALUES(%s,%s,%s,%s,%s)"),
                                   (aggregate.run_key.account_id, aggregate.run_key.run_id, self.environment,
                                    aggregate.version, Jsonb(aggregate.model_dump(mode="json"))))
                self._persist_records(connection, aggregate)
        except psycopg.errors.UniqueViolation:
            raise TradingError("ACCOUNT_ALREADY_BOUND", 409) from None
        except psycopg.Error:
            raise TradingError("STORE_UNAVAILABLE", 503) from None

    def read(self, key):
        try:
            with psycopg.connect(self.dsn) as connection:
                return self._load(connection, key)
        except psycopg.Error:
            raise TradingError("STORE_UNAVAILABLE", 503) from None

    def bound_run(self, account_id):
        from factorforge.trading.domain.models import RunKey
        try:
            with psycopg.connect(self.dsn) as connection:
                row = connection.execute(self._statement("SELECT run_id FROM {}.trading_run WHERE account_id=%s"), (account_id,)).fetchone()
                return RunKey(environment=self.environment, account_id=account_id, run_id=row[0]) if row else None
        except psycopg.Error:
            raise TradingError("STORE_UNAVAILABLE", 503) from None

    @contextmanager
    def transaction(self, key):
        try:
            with psycopg.connect(self.dsn) as connection:
                run = self._load(connection, key, lock=True)
                yield run
                self._persist_records(connection, run)
                connection.execute(self._statement("UPDATE {}.trading_run SET version=%s,payload=%s WHERE account_id=%s"),
                                   (run.version, Jsonb(run.model_dump(mode="json")), key.account_id))
        except psycopg.Error:
            # Intent, audit, reservations and outbox roll back together on any SQL error.
            raise TradingError("STORE_UNAVAILABLE", 503) from None

    def _persist_records(self, connection, run):
        data = run.model_dump(mode="json")
        old_audit = connection.execute(self._statement("SELECT sequence,payload FROM {}.audit_event WHERE account_id=%s ORDER BY sequence"),
                                       (run.run_key.account_id,)).fetchall()
        if any(sequence > len(run.audit) or run.audit[sequence - 1] != payload for sequence, payload in old_audit):
            raise TradingError("AUDIT_MUTATION_FORBIDDEN", 503)
        mapping = {
            "account_policy_version": {run.policy.version: data["policy"]},
            "instrument_spec": data["specs"], "market_snapshot_ref": data["points"],
            "owner_binding": {k: {"owner_id": v, "epoch": run.owner_epochs.get(k, 0)} for k, v in run.owners.items()},
            "target_request": data["targets"], "order": data["orders"],
            "protection_order": data["protections"], "fill": data["fills"],
            "income_entry": data["incomes"], "position_lot": data["positions"],
            "position_cycle": {"account": {"consecutive_losses": run.consecutive_losses}},
            "risk_day": {run.risk_day: {"start_equity": str(run.day_start_equity), "external_flow": str(run.day_external_flow)}},
            "risk_lock": {k: {"active": True} for k in run.risk_locks},
            "risk_reservation": {k: {"notional": str(o.reserved_notional)} for k, o in run.orders.items()},
            "reconciliation_report": {str(run.version): {"issues": run.recovery_issues, "state": run.state}},
            "command_dedup": data["dedup"],
            "outbox": {item["command_id"]: item for item in data["outbox"]},
        }
        for table, rows in mapping.items():
            query = sql.SQL("INSERT INTO {}.{} (account_id,resource_id,payload) VALUES(%s,%s,%s) ON CONFLICT(account_id,resource_id) DO UPDATE SET payload=excluded.payload").format(
                sql.Identifier(self.schema), sql.Identifier(table))
            for identifier, value in rows.items():
                connection.execute(query, (run.run_key.account_id, identifier, Jsonb(value)))
        for event in run.audit:
            connection.execute(self._statement("INSERT INTO {}.audit_event(account_id,sequence,payload) VALUES(%s,%s,%s) ON CONFLICT DO NOTHING"),
                               (run.run_key.account_id, event["sequence"], Jsonb(event)))
