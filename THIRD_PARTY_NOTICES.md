# Adapted source notices

Factorforge retains its MIT license. The following small algorithms are adapted in the strategy package; the complete upstream applications are not bundled.

## Financier

Source: [FusionHub._gate_direction](https://github.com/alexeymozolevsky-max/financier/blob/3708a31d77cb57c0f972828488449efc341dfbf1/engine/app/core/fusion_hub.py).
Pinned commit: `3708a31d77cb57c0f972828488449efc341dfbf1`.
Adaptation: `strategy/domain/position.py:level_for` extends the Decimal entry/retention equality rules to multiple registered levels. No AI, Redis, broker or uncalibrated sizing defaults are included.

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
