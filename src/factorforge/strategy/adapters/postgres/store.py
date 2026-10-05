"""Instance serialization: ledger, reservations, decisions and outbox commit together.

Public DB credentials can only change the public partition. Workload admission,
parameters, contribution ledger, target outbox and evidence consumption live in a
separate table which the public role cannot write. Audit/ledger are append-only.
"""
from contextlib import contextmanager
from importlib.resources import files

import psycopg
from psycopg import sql
from psycopg.types.json import Jsonb

from factorforge.strategy.domain.models import StrategyState, StrategyError, WorkloadIdentity

PUBLIC_FIELDS = {"version","objects","events","event_versions","event_received","scores","research_receipts","received","dedup","attributions"}


def initialize(dsn,environment):
    if environment not in {"SIM","LIVE"}:
        raise StrategyError("ENVIRONMENT_UNSUPPORTED")
    schema = "strategy_"+environment.lower()
    script = files(__package__).joinpath("migrations/001_strategy.sql").read_text()
    script = script.replace("__SCHEMA__",schema).replace("__ENV_LOWER__",environment.lower())
    with psycopg.connect(dsn) as conn:
        conn.execute(script)
        for kind in ("public","worker"):
            role = f"factorforge_strategy_{environment.lower()}_{kind}"
            if not conn.execute("SELECT 1 FROM pg_roles WHERE rolname=%s",(role,)).fetchone():
                conn.execute(sql.SQL("CREATE ROLE {} NOLOGIN").format(sql.Identifier(role)))
            conn.execute(sql.SQL("GRANT USAGE ON SCHEMA {} TO {}").format(sql.Identifier(schema),sql.Identifier(role)))
            conn.execute(sql.SQL("GRANT SELECT ON ALL TABLES IN SCHEMA {} TO {}").format(sql.Identifier(schema),sql.Identifier(role)))
            conn.execute(sql.SQL("GRANT INSERT,UPDATE ON {}.public_state TO {}").format(sql.Identifier(schema),sql.Identifier(role)))
            conn.execute(sql.SQL("GRANT INSERT ON {}.audit_event TO {}").format(sql.Identifier(schema),sql.Identifier(role)))
            conn.execute(sql.SQL("GRANT INSERT ON {}.object_owner_binding TO {}").format(sql.Identifier(schema),sql.Identifier(role)))
            if kind == "worker":
                conn.execute(sql.SQL("GRANT INSERT,UPDATE ON {}.internal_state TO {}").format(sql.Identifier(schema),sql.Identifier(role)))
                conn.execute(sql.SQL("GRANT INSERT ON {}.sentiment_entry TO {}").format(sql.Identifier(schema),sql.Identifier(role)))
            else:
                conn.execute(sql.SQL("REVOKE ALL ON {}.workload_capability_grant FROM {}").format(sql.Identifier(schema),sql.Identifier(role)))


class PostgresStore:
    def __init__(self,dsn,environment,kind="worker"):
        if environment not in {"SIM","LIVE"} or kind not in {"public","worker"}:
            raise StrategyError("DATABASE_BINDING_INVALID")
        self.dsn,self.environment,self.kind = dsn,environment,kind
        self.schema = "strategy_"+environment.lower()

    def statement(self,text):
        return sql.SQL(text).format(sql.Identifier(self.schema))

    def verify_runtime_role(self):
        role = f"factorforge_strategy_{self.environment.lower()}_{self.kind}"
        opposite = "strategy_live" if self.environment == "SIM" else "strategy_sim"
        with psycopg.connect(self.dsn) as conn:
            allowed,superuser = conn.execute("SELECT pg_has_role(current_user,%s,'USAGE'),rolsuper FROM pg_roles WHERE rolname=current_user",(role,)).fetchone()
            cross = conn.execute("SELECT to_regnamespace(%s)",(opposite,)).fetchone()[0]
            if not allowed or superuser or cross and conn.execute("SELECT has_schema_privilege(current_user,%s,'USAGE')",(opposite,)).fetchone()[0]:
                raise StrategyError("DATABASE_ROLE_NOT_ISOLATED",503)
            if self.kind == "public" and conn.execute("SELECT has_table_privilege(current_user,%s,'UPDATE')",(self.schema+".internal_state",)).fetchone()[0]:
                raise StrategyError("DATABASE_CAPABILITIES_NOT_SEPARATED",503)

    def check_workload(self,identity,object_id):
        if not isinstance(identity,WorkloadIdentity) or self.kind != "worker":
            raise StrategyError("WORKLOAD_DATABASE_ROLE_REQUIRED",403)
        with psycopg.connect(self.dsn) as conn:
            row = conn.execute(self.statement("SELECT 1 FROM {}.workload_capability_grant WHERE workload_id=%s AND instance_id=%s AND object_id=%s AND capability=%s"),
                (identity.workload_id,identity.instance_id,object_id,"signal:"+self.environment.lower())).fetchone()
            if not row:
                raise StrategyError("WORKLOAD_GRANT_MISSING",403)

    def _load(self,conn,instance_id,lock=False):
        public = conn.execute(self.statement("SELECT payload FROM {}.public_state WHERE instance_id=%s"+(" FOR UPDATE" if lock else "")),(instance_id,)).fetchone()
        internal = conn.execute(self.statement("SELECT payload FROM {}.internal_state WHERE instance_id=%s"), (instance_id,)).fetchone()
        if not public or not internal:
            raise StrategyError("INSTANCE_NOT_FOUND",404)
        state = StrategyState.model_validate({**public[0],**internal[0]})
        for obj in state.objects.values():
            row = conn.execute(self.statement("SELECT account_id,run_id,venue,product,instrument_id,owner_id FROM {}.object_owner_binding WHERE instance_id=%s AND object_id=%s"),(instance_id,obj.object_id)).fetchone()
            if row != self._binding(obj):
                raise StrategyError("OBJECT_BINDING_CHANGED",503)
        state.audit = [r[0] for r in conn.execute(self.statement("SELECT payload FROM {}.audit_event WHERE instance_id=%s ORDER BY sequence"),(instance_id,)).fetchall()]
        state.ledger = [r[0] for r in conn.execute(self.statement("SELECT payload FROM {}.sentiment_entry WHERE instance_id=%s ORDER BY sequence"),(instance_id,)).fetchall()]
        return StrategyState.model_validate(state.model_dump(mode="json"))

    def create(self,state):
        if state.environment != self.environment or self.kind != "worker":
            raise StrategyError("ENVIRONMENT_FORBIDDEN",403)
        public,internal = self._partition(state)
        with psycopg.connect(self.dsn) as conn:
            conn.execute(self.statement("INSERT INTO {}.public_state VALUES (%s,%s)"),(state.instance_id,Jsonb(public)))
            conn.execute(self.statement("INSERT INTO {}.internal_state VALUES (%s,%s)"),(state.instance_id,Jsonb(internal)))
            self._persist_bindings(conn,state)
            for name,rows in (("audit_event",state.audit),("sentiment_entry",state.ledger)):
                for index,row in enumerate(rows,start=1):
                    payload = row.model_dump(mode="json") if hasattr(row,"model_dump") else row
                    conn.execute(sql.SQL("INSERT INTO {}.{} VALUES (%s,%s,%s)").format(sql.Identifier(self.schema),sql.Identifier(name)),(state.instance_id,index,Jsonb(payload)))

    def _binding(self,obj):
        return (obj.trading_run_key.account_id,obj.trading_run_key.run_id,obj.instrument_key.venue,obj.instrument_key.product,obj.instrument_key.instrument_id,obj.owner_id)

    def _persist_bindings(self,conn,state):
        for obj in state.objects.values():
            conn.execute(self.statement("INSERT INTO {}.object_owner_binding VALUES (%s,%s,%s,%s,%s,%s,%s,%s) ON CONFLICT(instance_id,object_id) DO NOTHING"),
                (state.instance_id,obj.object_id,*self._binding(obj)))
            row = conn.execute(self.statement("SELECT account_id,run_id,venue,product,instrument_id,owner_id FROM {}.object_owner_binding WHERE instance_id=%s AND object_id=%s"),(state.instance_id,obj.object_id)).fetchone()
            if row != self._binding(obj):
                raise StrategyError("OBJECT_BINDING_CHANGED",409)

    def _partition(self,state):
        data = state.model_dump(mode="json")
        return ({k:v for k,v in data.items() if k in PUBLIC_FIELDS},
                {k:v for k,v in data.items() if k not in PUBLIC_FIELDS|{"audit","ledger"}})

    def read(self,instance_id):
        try:
            with psycopg.connect(self.dsn) as conn:
                conn.execute("SET TRANSACTION ISOLATION LEVEL REPEATABLE READ READ ONLY")
                return self._load(conn,instance_id)
        except psycopg.Error:
            raise StrategyError("STORE_UNAVAILABLE",503) from None

    @contextmanager
    def transaction(self,instance_id):
        try:
            with psycopg.connect(self.dsn) as conn:
                state = self._load(conn,instance_id,True)
                before = state.model_copy(deep=True)
                yield state
                public,internal = self._partition(state)
                self._persist_bindings(conn,state)
                _,old_internal = self._partition(before)
                if self.kind == "public":
                    if internal != old_internal or state.ledger != before.ledger:
                        raise StrategyError("PUBLIC_INTERNAL_STATE_FORBIDDEN",403)
                else:
                    conn.execute(self.statement("UPDATE {}.internal_state SET payload=%s WHERE instance_id=%s"),(Jsonb(internal),instance_id))
                conn.execute(self.statement("UPDATE {}.public_state SET payload=%s WHERE instance_id=%s"),(Jsonb(public),instance_id))
                for name,old,new in (("audit_event",before.audit,state.audit),("sentiment_entry",before.ledger,state.ledger)):
                    if new[:len(old)] != old:
                        raise StrategyError("APPEND_ONLY_MUTATION_FORBIDDEN",503)
                    for index,row in enumerate(new[len(old):],start=len(old)+1):
                        payload = row.model_dump(mode="json") if hasattr(row,"model_dump") else row
                        conn.execute(sql.SQL("INSERT INTO {}.{} VALUES (%s,%s,%s)").format(sql.Identifier(self.schema),sql.Identifier(name)),(instance_id,index,Jsonb(payload)))
        except psycopg.errors.UniqueViolation:
            raise StrategyError("OBJECT_OWNER_CONFLICT",409) from None
        except psycopg.Error:
            raise StrategyError("STORE_UNAVAILABLE",503) from None
