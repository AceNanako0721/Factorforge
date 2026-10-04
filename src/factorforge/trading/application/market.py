"""Persist public market snapshots without inventing executable liquidity."""
from factorforge.trading.domain.errors import TradingError
from factorforge.trading.domain.accounting import equity
from factorforge.trading.domain.risk import risk_day, assess_loss_gates
from factorforge.trading.application.emergency import apply_breach_action


def ingest_snapshot(service, principal, body):
    with service.command(principal, body, "MARKET_SNAPSHOT", body, "market:write") as (run, receipt, repeated):
        if not repeated:
            if body.at < run.clock or body.at > body.expires_at_utc:
                raise TradingError("MARKET_CLOCK_INVALID")
            day = risk_day(body.at, run.policy.risk_day_zone)
            if day != run.risk_day:
                run.day_start_equity, run.day_external_flow, run.risk_day = equity(run), run.day_external_flow * 0, day
            run.clock = body.at
            for point in body.points:
                spec = run.specs.get(point.instrument_key.code())
                if not spec or point.spec_version != spec.version or point.available_at > body.at:
                    raise TradingError("MARKET_POINT_RULE_OR_TIME_MISMATCH")
                old = run.points.get(point.instrument_key.code() + ":" + point.kind)
                if old and point.observed_at < old.observed_at:
                    raise TradingError("MARKET_POINT_OUT_OF_ORDER", 409)
                run.points[point.instrument_key.code() + ":" + point.kind] = point
            for candle in body.candles:
                if candle.instrument_key.code() not in run.specs:
                    raise TradingError("CANDLE_RULES_UNVERIFIED", 423)
                if not candle.final or candle.available_at > body.at:
                    raise TradingError("CANDLE_NOT_AVAILABLE")
                prior = next((c for c in run.candles if c.instrument_key == candle.instrument_key
                    and c.interval == candle.interval and c.open_at == candle.open_at), None)
                if prior and prior != candle and prior.revision >= candle.revision:
                    raise TradingError("CANDLE_REVISION_CONFLICT", 409)
                if prior is None or prior != candle:
                    if prior:
                        run.candles.remove(prior)
                    run.candles.append(candle)
            for trade in body.trades:
                spec = run.specs.get(trade.instrument_key.code())
                if not spec or trade.spec_version != spec.version or trade.available_at > run.clock:
                    raise TradingError("TRADE_RULE_OR_TIME_MISMATCH", 423)
                identity = trade.instrument_key.code() + ":" + trade.external_id
                prior = run.market_trades.get(identity)
                immutable = {"external_id", "instrument_key", "source_id", "observed_at", "price", "quantity"}
                if prior and prior.model_dump(include=immutable) != trade.model_dump(include=immutable):
                    raise TradingError("MARKET_TRADE_ID_CONFLICT", 409)
                if prior is None:
                    run.market_trades[identity] = trade
            for key in {c.instrument_key.code() for c in body.candles}:
                intervals = {c.interval for c in body.candles if c.instrument_key.code() == key}
                for interval in intervals:
                    bars = sorted((c for c in run.candles if c.instrument_key.code() == key and c.interval == interval), key=lambda c: c.open_at)
                    if any(a.close_at != b.open_at for a, b in zip(bars, bars[1:])):
                        for name, point in run.points.items():
                            if point.instrument_key.code() == key:
                                point.quality = "MISSING"
                        run.alerts.append({"at": run.clock.isoformat(), "code": "CANDLE_GAP", "instrument": key})
            try:
                from factorforge.trading.domain.market import check_quotes
                check_quotes(run)
                assess_loss_gates(run)
            except TradingError:
                run.health_issues = sorted(set(run.health_issues + ["ACCOUNT_MARK_UNAVAILABLE"]))
            apply_breach_action(run)
            receipt.update(resource_id=run.run_key.run_id, state=run.state)
    return receipt
