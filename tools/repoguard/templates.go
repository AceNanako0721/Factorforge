package repoguard

const expectedConfig = `# Format template only. Copy to config/config.toml; never commit that copy.
schema_version = 1
mode = "mock"

[runtime]
environment = "SIM"
instance_id = "soxl-jev"

[services]
trading_api_url = ""
framework_api_url = ""
exchange_api_url = ""
jev_api_url = ""
search_api_url = ""
database_url = ""
execution_database_url = ""

[credentials]
exchange_api_key = ""
exchange_api_secret = ""
jev_api_key = ""
search_api_key = ""
trading_api_token = ""
framework_api_token = ""
notification_token = ""

[application]
prompt_file = "prompts/prompts.local.json"

[application.read_api]
environment = ""
instance_id = ""
database_url = ""
token = ""
principal_id = ""
authorization_version = ""
original_sources = []
host = ""
port = 0
timeout_seconds = 0
query_default_limit = 0
query_max_limit = 0
query_max_records = 0
query_max_snapshot_bytes = 0
query_max_original_bytes = 0
query_cursor_age_seconds = 0
query_cursor_key = ""


[trading]
adapter = "mock"
allow_live = false
account_id = ""
principal_id = ""
permissions = []
storage_path = ""
account_currency = ""
reconciliation_tolerance = ""
# Populate from registered policy/cost records, never production defaults.
account_policy = {}
cost_model = {}

[trading.runtime_health]
storage_path = ""
clock_probe_url = ""
timeout_seconds = 0

[trading.signed_transport]
recv_window_ms = 0
request_budget = 0
priority_request_reserve = 0
budget_window_seconds = 0
timeout_seconds = 0
poll_seconds = 0
request_weights = {}

[trading.live_readiness]
account_id = ""
approved_endpoint = ""
valid_until = ""
policy_version = ""
account_probe_ref = ""
ordinary_probe_ref = ""
protection_probe_ref = ""
egress_isolation_ref = ""
official_runbook_exercise_ref = ""
risk_calibration_ref = ""
operator_id = ""
reviewer_id = ""
approved_instrument_versions = {}
overlapping_protections = false
atomic_protection_modify = false

# P2 scoped API access only. Exchange/model credentials are not used by P2.
[strategy]
environment = ""
instance_id = ""
public_database_url = ""
worker_database_url = ""
migration_database_url = ""
initial_registry = {}
trading_api_url = ""
trading_api_token = ""
public_token = ""
workload_token = ""
public_identity = {}
workload_identity = {}
public_host = ""
public_port = 0
internal_host = ""
internal_port = 0
public_api_url = ""
internal_api_url = ""
timeout_seconds = 0
candle_interval = ""
history_seconds = 0
worker_poll_seconds = 0
replay_clock = ""
query_default_limit = 0
query_max_limit = 0
query_max_records = 0
query_cursor_age_seconds = 0
query_cursor_key = ""
`
const expectedPrompts = `{
  "schema_version": 1,
  "asset_kind": "EXAMPLE_OR_MOCK",
  "production_ready": false,
  "prompt_version": "",
  "instructions": "",
  "state_template": "",
  "questions": [
    {"id": "direction", "type": "Choice", "instructions": "", "criteria": [], "choices": ["NEGATIVE", "NEUTRAL", "POSITIVE"]},
    {"id": "impact", "type": "Score", "instructions": "", "criteria": [], "scale_ref": ""},
    {"id": "facts", "type": "Noul", "instructions": "", "criteria": []}
  ]
}
`
