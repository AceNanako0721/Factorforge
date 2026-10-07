# Offline OpenAPI meta-schema

`openapi-3.1-2025-09-15.json` is the unchanged official schema downloaded from
[OpenAPI 3.1 schema, 2025-09-15](https://spec.openapis.org/oas/3.1/schema/2025-09-15).
SHA-256: `d0a3955182364c7b5fdebfd0583ecad259a870b4a2fe86a1b0fe8785f8224fed`.
Its Apache 2.0 license is retained in LICENSE from the OAI specification project.

The Go checker validates the OpenAPI structure against this local resource.
It separately compiles all current component schemas with Draft 2020-12;
the OpenAPI meta-schema deliberately leaves Schema Object validation open.
Unknown resources, network resolution and file paths outside contracts are
refused. No remote download occurs during validation or CI.
