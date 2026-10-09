package domain

import (
	"math"
	"strings"
	"time"
)

// Every budget is supplied by a versioned private policy, never a vendor default.
type SearchRoutingPolicy struct {
	Version         string                `json:"version"`
	TimeoutSeconds  int64                 `json:"timeout_seconds"`
	LeaseSeconds    int64                 `json:"lease_seconds"`
	CacheTTLSeconds int64                 `json:"cache_ttl_seconds"`
	MaxCacheEntries int                   `json:"max_cache_entries"`
	MaxResultURLs   int                   `json:"max_result_urls"`
	Providers       []SearchBackendPolicy `json:"providers"`
}
type SearchBackendPolicy struct {
	ID                      string    `json:"id"`
	Kind                    string    `json:"kind"`
	Version                 string    `json:"version"`
	WindowAnchor            time.Time `json:"window_anchor"`
	WindowSeconds           int64     `json:"window_seconds"`
	MaxRequests             int64     `json:"max_requests"`
	MinIntervalMilliseconds int64     `json:"min_interval_milliseconds"`
	TimeoutSeconds          int64     `json:"timeout_seconds"`
	CooldownSeconds         int64     `json:"cooldown_seconds"`
}

func (p SearchRoutingPolicy) Validate() error {
	if !ValidID(p.Version) || p.MaxCacheEntries <= 0 || p.MaxResultURLs < 1 || p.MaxResultURLs > 20 || len(p.Providers) == 0 || len(p.Providers) > 3 {
		return Fail("SEARCH_POLICY_REQUIRED", 503)
	}
	for _, n := range []int64{p.TimeoutSeconds, p.LeaseSeconds, p.CacheTTLSeconds} {
		if n <= 0 || n > math.MaxInt64/int64(time.Second) {
			return Fail("SEARCH_POLICY_INVALID", 422)
		}
	}
	if p.LeaseSeconds <= p.TimeoutSeconds {
		return Fail("SEARCH_LEASE_INVALID", 422)
	}
	ids, kinds := map[string]bool{}, map[string]bool{}
	for _, b := range p.Providers {
		if !ValidID(b.ID) || !ValidID(b.Version) || !Has([]string{"BRAVE", "EXA", "PARALLEL"}, b.Kind) || ids[b.ID] || kinds[b.Kind] || !UTC(b.WindowAnchor) || b.WindowAnchor.Nanosecond() != 0 || b.MaxRequests <= 0 || b.MinIntervalMilliseconds <= 0 || b.MinIntervalMilliseconds > math.MaxInt64/int64(time.Millisecond) {
			return Fail("SEARCH_BACKEND_POLICY_INVALID", 422)
		}
		ids[b.ID] = true
		kinds[b.Kind] = true
		for _, n := range []int64{b.WindowSeconds, b.TimeoutSeconds, b.CooldownSeconds} {
			if n <= 0 || n > math.MaxInt64/int64(time.Second) {
				return Fail("SEARCH_BACKEND_POLICY_INVALID", 422)
			}
		}
		if b.TimeoutSeconds > p.TimeoutSeconds {
			return Fail("SEARCH_BACKEND_TIMEOUT_INVALID", 422)
		}
	}
	return nil
}

type SearchBudgetState struct {
	LastCode      string    `json:"last_code"`
	Kind          string    `json:"kind"`
	WindowAnchor  time.Time `json:"window_anchor"`
	WindowSeconds int64     `json:"window_seconds"`
	WindowStart   time.Time `json:"window_start"`
	UsedRequests  int64     `json:"used_requests"`
	BlockedUntil  time.Time `json:"blocked_until"`
	NextRequestAt time.Time `json:"next_request_at"`
}
type SearchCacheEntry struct {
	ProviderID string    `json:"provider_id"`
	URLs       []string  `json:"urls"`
	ExpiresAt  time.Time `json:"expires_at"`
}
type SearchPending struct {
	AttemptID  string    `json:"attempt_id"`
	ProviderID string    `json:"provider_id"`
	ExpiresAt  time.Time `json:"expires_at"`
}

// SearchState contains only local accounting and URL clues. No search snippets,
// prompt, credential or original body may enter this private mutable record.
type SearchState struct {
	SchemaVersion int                          `json:"schema_version"`
	Binding       Binding                      `json:"binding"`
	Providers     map[string]SearchBudgetState `json:"providers"`
	Cache         map[string]SearchCacheEntry  `json:"cache"`
	Pending       map[string]SearchPending     `json:"pending"`
}

func NewSearchState(b Binding) SearchState {
	return SearchState{1, b, map[string]SearchBudgetState{}, map[string]SearchCacheEntry{}, map[string]SearchPending{}}
}
func (s SearchState) Validate(b Binding) error {
	if s.SchemaVersion != 1 || s.Binding != b || !b.Valid() || s.Providers == nil || s.Cache == nil || s.Pending == nil {
		return Fail("SEARCH_STATE_INVALID", 503)
	}
	for id, p := range s.Providers {
		if !ValidID(id) || !Has([]string{"BRAVE", "EXA", "PARALLEL"}, p.Kind) || p.LastCode != "" && (!strings.HasPrefix(p.LastCode, "SEARCH_") || !Codes([]string{p.LastCode})) || !UTC(p.WindowAnchor) || !UTC(p.WindowStart) || p.WindowSeconds <= 0 || p.WindowSeconds > math.MaxInt64/int64(time.Second) || p.WindowStart.Before(p.WindowAnchor) || p.UsedRequests < 0 || (!p.BlockedUntil.IsZero() && !UTC(p.BlockedUntil)) || (!p.NextRequestAt.IsZero() && !UTC(p.NextRequestAt)) {
			return Fail("SEARCH_STATE_INVALID", 503)
		}
	}
	for key, c := range s.Cache {
		if !hashPattern.MatchString(key) || !ValidID(c.ProviderID) || !UTC(c.ExpiresAt) || len(c.URLs) > 20 {
			return Fail("SEARCH_STATE_INVALID", 503)
		}
		for _, u := range c.URLs {
			if !SearchURLValid(u) {
				return Fail("SEARCH_STATE_INVALID", 503)
			}
		}
	}
	for key, p := range s.Pending {
		if !hashPattern.MatchString(key) || !ValidID(p.ProviderID) || !ValidID(p.AttemptID) || !UTC(p.ExpiresAt) {
			return Fail("SEARCH_STATE_INVALID", 503)
		}
	}
	return nil
}
func SearchURLValid(value string) bool {
	if len(value) > 4096 || strings.ContainsAny(value, "\r\n\t #") {
		return false
	}
	rest := strings.TrimPrefix(value, "https://")
	if rest == value {
		rest = strings.TrimPrefix(value, "http://")
		if rest == value {
			return false
		}
	}
	host := rest
	if end := strings.IndexAny(rest, "/?"); end >= 0 {
		host = rest[:end]
	}
	return host != "" && !strings.ContainsAny(host, "@?#")
}
