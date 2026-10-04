"""Authenticated replay ingestion. Future/unclosed bars never reach completed data."""
from factorforge.trading.application.commands import TradingService, require
from factorforge.trading.domain.accounting import equity
from factorforge.trading.domain.errors import TradingError
from factorforge.trading.domain.risk import risk_day, assess_loss_gates


def advance(service: TradingService, principal, command, frame):
    require(principal, command.run_key, "sim:write")
    if command.run_key.environment != "SIM":
        raise TradingError("SIM_ENDPOINT_FORBIDDEN", 403)
    with service.command(principal, command, "REPLAY_FRAME", frame, "sim:write") as (run, response, repeated):
        if not repeated:
            if frame.at <= run.clock:
                raise TradingError("CLOCK_MUST_ADVANCE")
            if frame.at > command.expires_at_utc:
                raise TradingError("REQUEST_EXPIRED", 409)
            # A new risk day needs the equity at the previous boundary. It never releases a lock.
            new_day = risk_day(frame.at, run.policy.risk_day_zone)
            if new_day != run.risk_day:
                run.day_start_equity = equity(run)
                run.day_external_flow = run.day_external_flow * 0
                run.risk_day = new_day
            run.clock = frame.at
            if service.health:
                run.health_issues, run.health_checked_at = service.health.check(run), run.clock
            spec = run.specs.get(frame.instrument_key.code())
            if not spec:
                raise TradingError("INSTRUMENT_RULES_UNVERIFIED", 423)
            for point in frame.points:
                if point.instrument_key != frame.instrument_key or point.available_at > frame.at:
                    raise TradingError("FUTURE_OR_MISMATCHED_MARKET_POINT")
                run.points[frame.instrument_key.code() + ":" + point.kind] = point
            if frame.candle:
                candle = frame.candle
                if candle.instrument_key != frame.instrument_key or candle.available_at > frame.at:
                    raise TradingError("FUTURE_OR_MISMATCHED_CANDLE")
                if candle.final:
                    old = next((c for c in run.candles if c.instrument_key == candle.instrument_key
                                and c.interval == candle.interval and c.open_at == candle.open_at), None)
                    if old and candle.revision <= old.revision:
                        if old != candle:
                            raise TradingError("CANDLE_REVISION_CONFLICT", 409)
                    else:
                        if old:
                            run.candles.remove(old)
                        run.candles.append(candle)
            from factorforge.trading.domain.market import check_quotes
            check_quotes(run)
            assess_loss_gates(run)
            if service.simulator is None:
                raise TradingError("SIMULATION_ADAPTER_REQUIRED", 503)
            service.simulator.advance_frame(run, frame.instrument_key, frame.liquidity, frame.candle)
            from factorforge.trading.application.emergency import apply_breach_action
            apply_breach_action(run)
            response.update(resource_id=run.run_key.run_id, state=run.state)
    return response
