"""Deterministic quality checks across quotes of the same instrument."""
def check_quotes(run):
    for code in run.specs:
        bid, ask = run.points.get(code + ":BID"), run.points.get(code + ":ASK")
        if bid and ask and bid.value > ask.value:
            bid.quality = ask.quality = "CONFLICT"
            run.alerts.append({"at": run.clock.isoformat(), "code": "CROSSED_QUOTES", "instrument": code})
