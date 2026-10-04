# Factorforge Core API contracts (design v1.1)

`openapi/v1.json` and `schemas/*.json` are the canonical v1.1 interface definitions. The OpenAPI file references the schemas by relative path. `doc/v1.0/api/openapi-v1.json` is the preserved v1.0 design artifact and is not used by these checks. The v2.0 three-layer documents are in `doc/v2.0`; these v1 contracts are retained for historical validation and do not implement the v2.0 layer interfaces.

`schemas/api-scope.json` defines the complete public `ApiScope` type used by API key create, read, and permission update. `schemas/workload-capability.json` defines the separate internal `WorkloadCapability` type. The two enums are disjoint; internal capabilities are absent from OpenAPI authorization scopes and cannot be written to the `key_scope` PostgreSQL enum column. The database role and internal grant-table design are specified in the detailed design. `POST /analysis/jobs` only enters the bounded research queue; the trading queue is fed by a versioned internal dispatcher.

Run locally:

```bash
python -m pip install -r contracts/requirements.txt
python contracts/check_contract.py
```

The check validates OpenAPI 3.1, validates JSON Schema 2020-12 examples, detects changes that break the frozen v1.1 surface, and generates/imports server function stubs in a temporary directory. The stub check verifies code generation from operation IDs; it does not implement routes. After deliberate API version review, refresh the baseline with `python contracts/check_contract.py --write-baseline` and review the baseline diff.

The remaining generic `Record` query projections must receive specific schemas before their server routes are implemented. A Python DTO must be generated from these schemas or pass a field-by-field contract test. Schema validity alone does not establish evidence truth, provider provenance, SIM/LIVE eligibility, or trading authorization; those are application checks described in the detailed design.
