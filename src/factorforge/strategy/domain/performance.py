"""Real-cost performance and registered-frequency uncertainty; unknown stays null."""
from decimal import Decimal
from factorforge.strategy.domain.models import ZERO


def performance(equity,fees,turnover,risk_used,annual_periods=None):
    if len(equity) < 2 or any(v <= 0 for v in equity):
        return {"net_return":None,"drawdown":None,"sharpe":None,"fees":str(fees),"turnover":str(turnover),"risk_utilization":str(risk_used)}
    returns = [b/a-1 for a,b in zip(equity,equity[1:])]
    mean = sum(returns,ZERO)/len(returns)
    variance = sum(((r-mean)**2 for r in returns),ZERO)/len(returns)
    covariance = sum(((a-mean)*(b-mean) for a,b in zip(returns,returns[1:])),ZERO)/len(returns)
    adjusted_variance = max(ZERO,variance+2*covariance)
    sharpe = mean/adjusted_variance.sqrt()*annual_periods.sqrt() if annual_periods and adjusted_variance > 0 else None
    peak,drawdown = equity[0],ZERO
    for value in equity:
        peak = max(peak,value)
        drawdown = max(drawdown,1-value/peak)
    return {"net_return":str(equity[-1]/equity[0]-1),"drawdown":str(drawdown),"sharpe":str(sharpe) if sharpe is not None else None,
            "fees":str(fees),"turnover":str(turnover),"risk_utilization":str(risk_used),"autocorrelation_adjusted":True}


def uncertainty(groups,values):
    grouped = {}
    for group,value in zip(groups,values):
        if value is not None:
            grouped.setdefault(group,[]).append(value)
    units = [sum(v,ZERO)/len(v) for v in grouped.values()]
    if len(units) < 2:
        return {"independent_groups":len(units),"mean":None,"standard_error":None}
    mean = sum(units,ZERO)/len(units)
    variance = sum(((v-mean)**2 for v in units),ZERO)/(len(units)-1)
    return {"independent_groups":len(units),"mean":str(mean),"standard_error":str((variance/len(units)).sqrt())}
