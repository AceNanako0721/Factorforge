"""Parameter-specific objective sensitivity. Missing identification yields no error."""
from decimal import Decimal


def sensitivity(parameter,case,counterfactual=None):
    labels = case.labels
    if case.status != "MATURE" or case.label_status in {"UNKNOWN","IMMATURE"}:
        return None
    if parameter == "w" and labels.get("direction_y") is not None and labels.get("predicted") is not None:
        return case.direction*labels["direction_y"]-abs(labels["predicted"])
    if parameter == "h" and labels.get("h_proxy") is not None and labels.get("h_hat") is not None:
        return labels["h_proxy"]-labels["h_hat"]
    if parameter in {"kappa","eta"} and labels.get("post_fulfillment") is not None:
        return -labels["post_fulfillment"]
    if parameter == "k_stop" and counterfactual and all(counterfactual.get(k) for k in ("verified","same_budget","one_factor","same_information_cost_latency_liquidity","tail_not_worse")) and not counterfactual.get("ohlc_ambiguous"):
        return Decimal(counterfactual["utility_improvement"])
    return None
