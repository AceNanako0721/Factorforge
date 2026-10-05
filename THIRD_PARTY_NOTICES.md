# Adapted source notices

Factorforge retains its MIT license. The following small algorithms are adapted in the strategy package; the complete upstream applications are not bundled.

## Financier

Source: [FusionHub._gate_direction](https://github.com/alexeymozolevsky-max/financier/blob/3708a31d77cb57c0f972828488449efc341dfbf1/engine/app/core/fusion_hub.py).
Pinned commit: `3708a31d77cb57c0f972828488449efc341dfbf1`.
Adaptation: `strategy/domain/position.py:level_for` and its Go translation `strategy/domain/position.go:LevelFor` extend the Decimal entry/retention equality rules to multiple registered levels. No AI, Redis, broker or uncalibrated sizing defaults are included.

Copyright (c) 2026 Alexey Mozolevsky

## timeseriescv

Source: [cross_validation.purge](https://github.com/sam31415/timeseriescv/blob/cb04fb6ea7a0b2c15920ca253f882336fe336ba8/timeseriescv/cross_validation.py).
Pinned commit: `cb04fb6ea7a0b2c15920ca253f882336fe336ba8`.
Adaptation: `strategy/domain/validation.py:purge` uses UTC records instead of pandas/NumPy, adds availability, embargo and group exclusion, and omits the future-training branch to enforce strict forward validation.

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
