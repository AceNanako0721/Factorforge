package domain

type Route struct {
	Service, Path string
	Query         []string
}

var Routes = map[string]Route{
	"trading.health": {"trading", "/api/v2/trading/health", nil},
	"run":            {"trading", "/api/v2/trading/runs/{run_id}", nil},
	"account":        {"trading", "/api/v2/trading/account", nil},
	"positions":      {"trading", "/api/v2/trading/positions", nil}, "owners": {"trading", "/api/v2/trading/owners", nil}, "protections": {"trading", "/api/v2/trading/protections", nil},
	"points": {"trading", "/api/v2/trading/market/points", nil}, "candles": {"trading", "/api/v2/trading/market/candles", []string{"interval", "start", "end"}},
	"instruments": {"trading", "/api/v2/trading/instruments", nil}, "orders": {"trading", "/api/v2/trading/orders", nil}, "fills": {"trading", "/api/v2/trading/fills", nil}, "income": {"trading", "/api/v2/trading/income", nil}, "trading.targets": {"trading", "/api/v2/trading/targets", nil}, "external-facts": {"trading", "/api/v2/trading/external-facts", nil}, "alerts": {"trading", "/api/v2/trading/alerts", nil}, "trading.audit": {"trading", "/api/v2/trading/audit", nil}, "trading.operations": {"trading", "/api/v2/trading/operational-health", nil},
	"strategy.health": {"strategy", "/api/v2/strategy/health", nil}, "objects": {"strategy", "/api/v2/strategy/objects", []string{"cursor", "limit"}}, "object": {"strategy", "/api/v2/strategy/objects/{object_id}", nil}, "pool": {"strategy", "/api/v2/strategy/objects/{object_id}/pool", nil}, "decisions": {"strategy", "/api/v2/strategy/objects/{object_id}/decisions", nil}, "strategy.targets": {"strategy", "/api/v2/strategy/objects/{object_id}/targets", nil}, "cases": {"strategy", "/api/v2/strategy/objects/{object_id}/cases", nil},
	"events": {"strategy", "/api/v2/strategy/events", []string{"cursor", "limit", "from", "to"}}, "event": {"strategy", "/api/v2/strategy/events/{event_id}", []string{"revision"}}, "scores": {"strategy", "/api/v2/strategy/events/{event_id}/scores", []string{"cursor", "limit", "revision"}}, "ledger": {"strategy", "/api/v2/strategy/objects/{object_id}/ledger", []string{"cursor", "limit", "contribution_id"}}, "attributions": {"strategy", "/api/v2/strategy/cases/{case_id}/attributions", []string{"cursor", "limit"}}, "counterfactuals": {"strategy", "/api/v2/strategy/cases/{case_id}/counterfactuals", []string{"cursor", "limit"}}, "parameters": {"strategy", "/api/v2/strategy/parameters", nil}, "learning": {"strategy", "/api/v2/strategy/learning-decisions", nil}, "validation": {"strategy", "/api/v2/strategy/validation-runs", nil}, "activations": {"strategy", "/api/v2/strategy/parameter-activations", []string{"cursor", "limit", "from", "to"}}, "strategy.audit": {"strategy", "/api/v2/strategy/audit", []string{"cursor", "limit", "from", "to"}},
	"instance.health": {"instances", "/api/v2/instances/{instance_id}/health", nil}, "sources": {"instances", "/api/v2/instances/{instance_id}/sources", []string{"cursor", "limit"}}, "jobs": {"instances", "/api/v2/instances/{instance_id}/analysis-jobs", []string{"cursor", "limit", "queue_kind", "state"}}, "job": {"instances", "/api/v2/instances/{instance_id}/analysis-jobs/{job_id}", nil}, "evidence": {"instances", "/api/v2/instances/{instance_id}/evidence/{evidence_id}", nil}, "budgets": {"instances", "/api/v2/instances/{instance_id}/budgets", nil}, "reports": {"instances", "/api/v2/instances/{instance_id}/reports", []string{"cursor", "limit", "from", "to"}}, "instance.audit": {"instances", "/api/v2/instances/{instance_id}/audit", []string{"cursor", "limit", "from", "to"}},
}
