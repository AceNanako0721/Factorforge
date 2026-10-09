package domain

import (
	"math"
	"time"
)

type ProviderPartition struct {
	MaxCalls      int64 `json:"max_calls"`
	MaxConcurrent int64 `json:"max_concurrent"`
}

// Limits are explicit operator assets. A successful local policy validation
// does not certify the supplier account, token rate, billing or LIVE capacity.
type ProviderPoolPolicy struct {
	PoolID                 string                       `json:"pool_id"`
	Version                string                       `json:"version"`
	ValidFrom              time.Time                    `json:"valid_from"`
	ValidUntil             time.Time                    `json:"valid_until"`
	MaxCalls               int64                        `json:"max_calls"`
	MaxConcurrent          int64                        `json:"max_concurrent"`
	MinStartIntervalMillis int64                        `json:"min_start_interval_millis"`
	MaxRequestBytes        int                          `json:"max_request_bytes"`
	Partitions             map[string]ProviderPartition `json:"partitions"`
}

func (p ProviderPoolPolicy) Validate() error {
	invalid := func() error { return Fail("PROVIDER_POLICY_INVALID", 422) }
	if !ValidID(p.PoolID) || !ValidID(p.Version) || !UTC(p.ValidFrom) || !UTC(p.ValidUntil) || !p.ValidUntil.After(p.ValidFrom) ||
		p.MaxCalls <= 0 || p.MaxConcurrent <= 0 || p.MinStartIntervalMillis <= 0 || p.MinStartIntervalMillis > math.MaxInt64/int64(time.Millisecond) ||
		p.MaxRequestBytes <= 0 || len(p.Partitions) != 3 {
		return invalid()
	}
	calls, concurrent := p.MaxCalls, p.MaxConcurrent
	for _, kind := range []string{"RESEARCH", "SIM", "LIVE"} {
		part, ok := p.Partitions[kind]
		if !ok || part.MaxCalls < 0 || part.MaxConcurrent < 0 || (part.MaxCalls == 0) != (part.MaxConcurrent == 0) || part.MaxCalls > calls || part.MaxConcurrent > concurrent {
			return invalid()
		}
		calls, concurrent = calls-part.MaxCalls, concurrent-part.MaxConcurrent
	}
	return nil
}

type ProviderGrant struct {
	Login     string  `json:"login"`
	Binding   Binding `json:"binding"`
	QueueKind string  `json:"queue_kind"`
	PoolID    string  `json:"pool_id"`
}

func (g ProviderGrant) Valid() bool {
	return ValidID(g.Login) && g.Binding.Valid() && Has([]string{"RESEARCH", g.Binding.Environment}, g.QueueKind) && ValidID(g.PoolID)
}
