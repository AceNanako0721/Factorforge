package monitoring

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	d "github.com/AceNanako0721/Factorforge/src/factorforge/applications/soxl_jev/domain"
	"github.com/AceNanako0721/Factorforge/src/factorforge/applications/soxl_jev/ports"
	"net/url"
	"strings"
	"time"
)

type SearchRouterOptions struct {
	Binding       d.Binding
	Policy        d.SearchRoutingPolicy
	Store         ports.SearchStateStore
	Backends      map[string]ports.SearchBackend
	Plans         []SearchPlan
	Fetcher       *Fetcher
	SourcesByHost map[string]d.SourceRegistration
	Clock         func() time.Time
}
type SearchRouter struct{ options SearchRouterOptions }

func NewSearchRouter(o SearchRouterOptions) (*SearchRouter, error) {
	if e := o.Policy.Validate(); e != nil {
		return nil, e
	}
	if !o.Binding.Valid() || o.Store == nil || o.Store.Binding() != o.Binding || o.Clock == nil || o.Fetcher == nil || len(o.Backends) != len(o.Policy.Providers) {
		return nil, d.Fail("SEARCH_CONFIGURATION_REQUIRED", 503)
	}
	for _, b := range o.Policy.Providers {
		if o.Backends[b.ID] == nil {
			return nil, d.Fail("SEARCH_BACKEND_CONFIGURATION_REQUIRED", 503)
		}
	}
	seen := map[string]bool{}
	for _, p := range o.Plans {
		key := p.SourceID + ":" + p.Version
		if !d.ValidID(p.Version) || !d.ValidID(p.SourceID) || !validSearchQuery(p.Query, p.Count) || p.Count > o.Policy.MaxResultURLs || seen[key] {
			return nil, d.Fail("SEARCH_PLAN_INVALID", 422)
		}
		seen[key] = true
	}
	return &SearchRouter{o}, nil
}
func (r *SearchRouter) Search(ctx context.Context, seed d.RawEvidence, cutoff time.Time) ([]d.RawEvidence, error) {
	o := r.options
	if !d.UTC(cutoff) || cutoff.After(o.Clock().UTC()) || !d.UTC(seed.ReceivedAt) || seed.ReceivedAt.After(cutoff) || !d.ValidID(seed.SourceID) {
		return nil, d.Fail("SEARCH_CUTOFF_INVALID", 422)
	}
	work, cancel := context.WithTimeout(ctx, time.Duration(o.Policy.TimeoutSeconds)*time.Second)
	defer cancel()
	result := []d.RawEvidence{}
	seen := map[string]bool{}
	for _, plan := range o.Plans {
		if plan.SourceID != seed.SourceID {
			continue
		}
		key := d.Digest([]any{o.Binding, o.Policy.Version, plan.SourceID, plan.Version, plan.Query, plan.Count})
		urls, e := r.clues(work, key, plan)
		if e != nil {
			return nil, e
		}
		for _, value := range urls {
			u, e := url.Parse(value)
			if e != nil {
				return nil, d.Fail("SEARCH_RESULT_URL_INVALID", 503)
			}
			source, ok := o.SourcesByHost[u.Hostname()]
			// Cached clues never carry forward authorization or source material.
			if !ok || !source.Enabled || !source.LicenceVerified || !source.AllowAnalysis || !d.Has(source.Environments, o.Binding.Environment) || cutoff.Before(source.ValidFrom) || !cutoff.Before(source.ValidUntil) || seen[value] {
				continue
			}
			seen[value] = true
			body, media, e := o.Fetcher.Get(work, value)
			if e != nil {
				return nil, e
			}
			if !d.Has([]string{"text/plain", "text/html", "application/xhtml+xml"}, media) {
				return nil, d.Fail("MISSING_ORIGINAL", 422)
			}
			hash := d.ContentDigest(body)
			result = append(result, d.RawEvidence{EvidenceID: "evidence-" + d.Digest([]string{source.SourceID, value, hash}), SourceID: source.SourceID, URL: value, ContentHash: hash, Content: string(body), ReceivedAt: o.Clock().UTC(), LicenceRef: source.LicenceRef})
		}
	}
	return result, nil
}

// Reserve before dispatch, then complete with a compare-and-set on the lease.
// Failures and lost acknowledgements consume the local allowance conservatively.
func (r *SearchRouter) clues(ctx context.Context, key string, plan SearchPlan) ([]string, error) {
	o := r.options
	for _, provider := range o.Policy.Providers {
		if ctx.Err() != nil {
			return nil, d.Fail("SEARCH_CANCELLED", 503)
		}
		token := make([]byte, 16)
		if _, e := rand.Read(token); e != nil {
			return nil, d.Fail("SEARCH_ATTEMPT_UNAVAILABLE", 503)
		}
		attempt := hex.EncodeToString(token)
		now := o.Clock().UTC()
		outcome := ""
		var cached []string
		e := o.Store.MutateSearchState(ctx, func(s *d.SearchState) error {
			if e := s.Validate(o.Binding); e != nil {
				return e
			}
			for k, c := range s.Cache {
				if !now.Before(c.ExpiresAt) {
					delete(s.Cache, k)
				}
			}
			for k, p := range s.Pending {
				if !now.Before(p.ExpiresAt) {
					delete(s.Pending, k)
				}
			}
			if c, ok := s.Cache[key]; ok {
				outcome = "CACHE"
				cached = append([]string{}, c.URLs...)
				return nil
			}
			if _, ok := s.Pending[key]; ok {
				outcome = "SEARCH_IN_PROGRESS"
				return nil
			}
			if len(s.Cache)+len(s.Pending) >= o.Policy.MaxCacheEntries {
				outcome = "SEARCH_CACHE_CAPACITY"
				return nil
			}
			if now.Before(provider.WindowAnchor) {
				outcome = "SKIP"
				return nil
			}
			window := time.Unix(provider.WindowAnchor.Unix()+(now.Unix()-provider.WindowAnchor.Unix())/provider.WindowSeconds*provider.WindowSeconds, 0).UTC()
			budget, ok := s.Providers[provider.ID]
			if ok && (budget.Kind != provider.Kind || !budget.WindowAnchor.Equal(provider.WindowAnchor) || budget.WindowSeconds != provider.WindowSeconds) {
				return d.Fail("SEARCH_WINDOW_POLICY_CHANGED", 409)
			}
			if !ok {
				budget = d.SearchBudgetState{Kind: provider.Kind, WindowAnchor: provider.WindowAnchor, WindowSeconds: provider.WindowSeconds, WindowStart: window}
			}
			if now.Before(budget.WindowStart) {
				return d.Fail("SEARCH_CLOCK_ROLLBACK", 503)
			}
			if window.After(budget.WindowStart) {
				budget.WindowStart = window
				budget.UsedRequests = 0
			}
			if budget.UsedRequests >= provider.MaxRequests || now.Before(budget.BlockedUntil) || now.Before(budget.NextRequestAt) {
				s.Providers[provider.ID] = budget
				outcome = "SKIP"
				return nil
			}
			budget.UsedRequests++
			budget.NextRequestAt = now.Add(time.Duration(provider.MinIntervalMilliseconds) * time.Millisecond)
			s.Providers[provider.ID] = budget
			s.Pending[key] = d.SearchPending{AttemptID: attempt, ProviderID: provider.ID, ExpiresAt: now.Add(time.Duration(o.Policy.LeaseSeconds) * time.Second)}
			outcome = "CLAIM"
			return nil
		})
		if e != nil {
			return nil, e
		}
		switch outcome {
		case "CACHE":
			return cached, nil
		case "SEARCH_IN_PROGRESS", "SEARCH_CACHE_CAPACITY":
			return nil, d.Fail(outcome, 503)
		case "SKIP":
			continue
		case "CLAIM":
		default:
			return nil, d.Fail("SEARCH_STATE_INVALID", 503)
		}
		call, cancel := context.WithTimeout(ctx, time.Duration(provider.TimeoutSeconds)*time.Second)
		reply, queryErr := o.Backends[provider.ID].Query(call, ports.SearchQuery{Query: plan.Query, Count: plan.Count, Key: key})
		cancel()
		if queryErr == nil {
			if len(reply.URLs) > plan.Count {
				queryErr = d.Fail("SEARCH_RESPONSE_INVALID", 503)
			}
			for _, value := range reply.URLs {
				if !d.SearchURLValid(value) {
					queryErr = d.Fail("SEARCH_RESULT_URL_INVALID", 503)
				}
			}
		}
		finished := o.Clock().UTC()
		accepted := false
		e = o.Store.MutateSearchState(ctx, func(s *d.SearchState) error {
			if e := s.Validate(o.Binding); e != nil {
				return e
			}
			pending, ok := s.Pending[key]
			if !ok || pending.AttemptID != attempt || pending.ProviderID != provider.ID || !finished.Before(pending.ExpiresAt) {
				return nil
			}
			budget, ok := s.Providers[provider.ID]
			if !ok {
				return d.Fail("SEARCH_STATE_INVALID", 503)
			}
			pause := reply.PauseUntil
			budget.LastCode = "SEARCH_OK"
			if !pause.IsZero() && !d.UTC(pause) {
				return d.Fail("SEARCH_RESPONSE_INVALID", 503)
			}
			if queryErr != nil {
				budget.LastCode = "SEARCH_UNAVAILABLE"
				var outcome *d.Error
				if errors.As(queryErr, &outcome) && strings.HasPrefix(outcome.Code, "SEARCH_") && d.Codes([]string{outcome.Code}) {
					budget.LastCode = outcome.Code
				}
				cooldown := finished.Add(time.Duration(provider.CooldownSeconds) * time.Second)
				if cooldown.After(pause) {
					pause = cooldown
				}
				var failure *d.Error
				if errors.As(queryErr, &failure) && failure.Code == "SEARCH_QUOTA_EXHAUSTED" && reply.PauseUntil.IsZero() {
					end := budget.WindowStart.Add(time.Duration(provider.WindowSeconds) * time.Second)
					if end.After(pause) {
						pause = end
					}
				}
			}
			if pause.After(budget.BlockedUntil) {
				budget.BlockedUntil = pause
			}
			s.Providers[provider.ID] = budget
			delete(s.Pending, key)
			accepted = true
			if queryErr == nil {
				s.Cache[key] = d.SearchCacheEntry{ProviderID: provider.ID, URLs: append([]string{}, reply.URLs...), ExpiresAt: finished.Add(time.Duration(o.Policy.CacheTTLSeconds) * time.Second)}
			}
			return nil
		})
		if e != nil {
			return nil, e
		}
		if !accepted {
			return nil, d.Fail("SEARCH_LEASE_EXPIRED", 503)
		}
		if queryErr == nil {
			return reply.URLs, nil
		}
		if ctx.Err() != nil {
			return nil, d.Fail("SEARCH_CANCELLED", 503)
		}
	}
	return nil, d.Fail("SEARCH_BACKENDS_UNAVAILABLE", 503)
}
