# Adapted source notices

Factorforge retains its MIT license. The following small algorithms are adapted in the strategy package; the complete upstream applications are not bundled.

## Financier

Source: [FusionHub._gate_direction](https://github.com/alexeymozolevsky-max/financier/blob/3708a31d77cb57c0f972828488449efc341dfbf1/engine/app/core/fusion_hub.py).
Pinned commit: `3708a31d77cb57c0f972828488449efc341dfbf1`.
Adaptation: `strategy/domain/position.go:LevelFor` extends the Decimal entry/retention equality rules to multiple registered levels. The earlier Python adaptation remains in Git history. No AI, Redis, broker or uncalibrated sizing defaults are included.

Copyright (c) 2026 Alexey Mozolevsky

## timeseriescv

Source: [cross_validation.purge](https://github.com/sam31415/timeseriescv/blob/cb04fb6ea7a0b2c15920ca253f882336fe336ba8/timeseriescv/cross_validation.py).
Pinned commit: `cb04fb6ea7a0b2c15920ca253f882336fe336ba8`.
Adaptation: `strategy/domain/validation.go:Purge` uses UTC records instead of pandas/NumPy, adds availability, embargo and group exclusion, and omits the future-training branch to enforce strict forward validation. The earlier Python adaptation remains in Git history.

Copyright (c) 2018 Samuel Monnier

## MIT terms applying to both adaptations

Permission is hereby granted, free of charge, to any person obtaining a copy
of this software and associated documentation files (the "Software"), to deal
in the Software without restriction, including without limitation the rights
to use, copy, modify, merge, publish, distribute, sublicense, and/or sell
copies of the Software, and to permit persons to whom the Software is
furnished to do so, subject to the following conditions:

The above copyright notice and this permission notice shall be included in all
copies or substantial portions of the Software.

THE SOFTWARE IS PROVIDED "AS IS", WITHOUT WARRANTY OF ANY KIND, EXPRESS OR
IMPLIED, INCLUDING BUT NOT LIMITED TO THE WARRANTIES OF MERCHANTABILITY,
FITNESS FOR A PARTICULAR PURPOSE AND NONINFRINGEMENT. IN NO EVENT SHALL THE
AUTHORS OR COPYRIGHT HOLDERS BE LIABLE FOR ANY CLAIM, DAMAGES OR OTHER
LIABILITY, WHETHER IN AN ACTION OF CONTRACT, TORT OR OTHERWISE, ARISING FROM,
OUT OF OR IN CONNECTION WITH THE SOFTWARE OR THE USE OR OTHER DEALINGS IN THE
SOFTWARE.

## cockroachdb/apd Go dependency

Dependency: `github.com/cockroachdb/apd/v3 v3.2.3`, pinned by go.mod/go.sum.
Source commit: `6d9c587326e78bcfbea630bf52893ff45f6f9ed5`.
Source: [apd v3.2.3](https://github.com/cockroachdb/apd/tree/6d9c587326e78bcfbea630bf52893ff45f6f9ed5).
License: [Apache 2.0](https://github.com/cockroachdb/apd/blob/6d9c587326e78bcfbea630bf52893ff45f6f9ed5/LICENSE).
Copyright 2016 The Cockroach Authors.

Factorforge's `trading/domain/decimal/value.go` calls the library and adds finite
string validation, immutable values, operation-local rounding and checked Exp
work precision. It does not copy apd source. Retain this dependency's license
when distributing binaries or vendored source. Factorforge source remains MIT.

## Native P1 infrastructure dependencies

These modules are called through their public APIs; their applications or source
are not copied into Factorforge. Exact versions and transitive dependencies are
pinned in go.mod/go.sum. Preserve their licenses when distributing binaries.

| Module | Version and used files | License and copyright |
| --- | --- | --- |
| jackc/pgx/v5 | [v5.11.0](https://github.com/jackc/pgx/tree/v5.11.0), pgxpool/pool.go, tx.go, pgconn | MIT; Copyright (c) 2013-2021 Jack Christensen |
| pelletier/go-toml/v2 | [v2.4.3](https://github.com/pelletier/go-toml/tree/v2.4.3), unmarshaler.go, marshaler.go | MIT; Copyright (c) 2021-2023 Thomas Pelletier |
| landlock-lsm/go-landlock | [v0.10.1](https://github.com/landlock-lsm/go-landlock/tree/v0.10.1), landlock path rules and strict thread restrictions | MIT; Copyright (c) 2021 Günther Noack |

The MIT terms reproduced above also apply to these dependencies. Indirect
modules retain their own licenses, including pgpassfile, pgservicefile, puddle,
Go x/sync, x/sys, x/text, and libcap/psx; consult their LICENSE files when bundling.

## Native repository validation dependencies

These libraries are called through their public APIs and pinned by go.mod/go.sum.
No application or copied implementation from them is included.

| Module | Version and use | License |
| --- | --- | --- |
| golang.org/x/net | [v0.59.0](https://pkg.go.dev/golang.org/x/net@v0.59.0/html), html/parse.go HTML5 document validation; html/token.go original-byte review views in the application CLI | BSD 3-Clause; The Go Authors |
| santhosh-tekuri/jsonschema/v6 | [v6.0.2](https://github.com/santhosh-tekuri/jsonschema/tree/v6.0.2), compiler.go and validator.go for offline Draft 2020-12 | [Apache 2.0](https://github.com/santhosh-tekuri/jsonschema/blob/v6.0.2/LICENSE) |
| dlclark/regexp2 | [v1.11.5](https://github.com/dlclark/regexp2/tree/v1.11.5), regexp.go ECMAScript lookahead compatibility for existing decimal schemas | [MIT](https://github.com/dlclark/regexp2/blob/v1.11.5/LICENSE); Doug Clark |

The OpenAPI Initiative's official dated OpenAPI 3.1 meta-schema is bundled
unchanged in contracts/meta for offline validation. Its source URL, SHA-256,
scope and Apache 2.0 license are included there. Distributions must retain the
licenses of these dependencies and the bundled schema. Factorforge remains MIT.

## shadcn-admin

The console adapts the local layout, button variants and read-table lifecycle from [satnaing/shadcn-admin](https://github.com/satnaing/shadcn-admin/tree/e16c87f213a5ba5e45964e9b67c792105ec74d26), commit `e16c87f213a5ba5e45964e9b67c792105ec74d26`:

- `src/components/layout/authenticated-layout.tsx`: shell/skip-to-main/sidebar organisation; local Go session replaces Clerk.
- `src/components/ui/button.tsx`: Radix Slot and CVA variants; Factorforge styles and reduced variants.
- `src/features/tasks/components/tasks-table.tsx`: TanStack filtering/sorting/pagination/column visibility; no editable demo/bulk deletion.

MIT License

Copyright (c) 2024 Sat Naing

Permission is hereby granted, free of charge, to any person obtaining a copy
of this software and associated documentation files (the "Software"), to deal
in the Software without restriction, including without limitation the rights
to use, copy, modify, merge, publish, distribute, sublicense, and/or sell
copies of the Software, and to permit persons to whom the Software is
furnished to do so, subject to the following conditions:

The above copyright notice and this permission notice shall be included in all
copies or substantial portions of the Software.

THE SOFTWARE IS PROVIDED "AS IS", WITHOUT WARRANTY OF ANY KIND, EXPRESS OR
IMPLIED, INCLUDING BUT NOT LIMITED TO THE WARRANTIES OF MERCHANTABILITY,
FITNESS FOR A PARTICULAR PURPOSE AND NONINFRINGEMENT. IN NO EVENT SHALL THE
AUTHORS OR COPYRIGHT HOLDERS BE LIABLE FOR ANY CLAIM, DAMAGES OR OTHER
LIABILITY, WHETHER IN AN ACTION OF CONTRACT, TORT OR OTHERWISE, ARISING FROM,
OUT OF OR IN CONNECTION WITH THE SOFTWARE OR THE USE OR OTHER DEALINGS IN THE
SOFTWARE.

## Lightweight Charts

The console uses the public API of `lightweight-charts@5.2.1` (Apache-2.0), with its installed licence retained in distributions. The baseline API/examples reference is TradingView commit `6777212d1d62eb4611fb2b98ef06062ddf9a9ef3`; it is not a floating source/build dependency. TradingView attribution remains enabled in the chart and linked on the page. See [upstream licence](https://github.com/tradingview/lightweight-charts/blob/6777212d1d62eb4611fb2b98ef06062ddf9a9ef3/LICENSE).

## FreqUI

The baseline references FreqUI `2e6a0907ed2e990b9e905331888dfa9824024d2d` for the distinction between frequent market refresh and slower trade state refresh. FreqUI is GPL-3.0. No FreqUI code, translations, styles or data models are copied or adapted into this MIT project.

## Search harness implementation references

The Go search adapters reference protocol and control-flow patterns from Oh My Pi (MIT), pinned at `579da1d661c5cb8d43bc2ddd429ab72e67165ad8`, and OpenCode (MIT), pinned at `388406238bd5ca15564a762840a2362c3a45bd9c`. No upstream code is copied or adapted, and neither harness is a runtime dependency. Exact files, probe evidence and limits are recorded in [P3 search review](doc/engineering/P3_SEARCH_UPSTREAM_REVIEW.md). Codex (Apache-2.0) and Gemini CLI (Apache-2.0) are comparison references only, with no copied implementation or authenticated service integration.
