package application

import (
	"context"
	d "github.com/AceNanako0721/Factorforge/src/factorforge/operations/agent/domain"
	"github.com/AceNanako0721/Factorforge/src/factorforge/operations/agent/ports"
	"sync"
	"time"
)

// Supervisor sweeps every registered instance and the host, writes check
// records and opens or extends issues for ANOMALY hits (S6-003, S6-006).
type Supervisor struct {
	Instances []Instance
	Collector Collector
	Store     ports.Store
	Now       func() time.Time
}

// SweepResult is what one sweep found, for the process log.
type SweepResult struct {
	At        time.Time
	Hits      []d.Hit
	NewIssues []string
	Failures  []string
}

// Sweep runs one pass. A store failure for one instance does not stop the
// others; it is listed in Failures by code only.
func (s Supervisor) Sweep(ctx context.Context) SweepResult {
	at := s.Now().UTC()
	res := SweepResult{At: at}
	var mu sync.Mutex
	var wg sync.WaitGroup
	for _, in := range s.Instances {
		wg.Add(1)
		go func(in Instance) {
			defer wg.Done()
			since, err := s.Store.LastSweep(ctx, in.ID)
			if err != nil {
				mu.Lock()
				res.Failures = append(res.Failures, in.ID+":"+d.Code(err))
				mu.Unlock()
				return
			}
			snap := s.Collector.Collect(ctx, in, at, since)
			hits := d.Evaluate(snap)
			news, failures := s.record(ctx, in.ID, at, hits)
			mu.Lock()
			res.Hits = append(res.Hits, hits...)
			res.NewIssues = append(res.NewIssues, news...)
			res.Failures = append(res.Failures, failures...)
			mu.Unlock()
		}(in)
	}
	wg.Wait()
	if s.Collector.Host != nil {
		host := s.Collector.Host.Host(ctx)
		host.At = at
		hits := d.EvaluateHost(&host)
		news, failures := s.record(ctx, "host", at, hits)
		res.Hits = append(res.Hits, hits...)
		res.NewIssues = append(res.NewIssues, news...)
		res.Failures = append(res.Failures, failures...)
	}
	return res
}

func (s Supervisor) record(ctx context.Context, instance string, at time.Time, hits []d.Hit) (news, failures []string) {
	if err := s.Store.RecordCheck(ctx, instance, at, hits); err != nil {
		failures = append(failures, instance+":"+d.Code(err))
	}
	for _, h := range hits {
		if h.Level != d.Anomaly {
			continue
		}
		rule, _ := d.RuleByID(h.Rule)
		issue, created, err := s.Store.Occur(ctx, h, rule.Severity, at)
		if err != nil {
			failures = append(failures, instance+":"+d.Code(err))
			continue
		}
		if created {
			news = append(news, issue.ID)
		}
	}
	return news, failures
}

// Run sweeps every interval until ctx ends; each result goes to report.
func (s Supervisor) Run(ctx context.Context, interval time.Duration, report func(SweepResult)) {
	t := time.NewTicker(interval)
	defer t.Stop()
	for {
		report(s.Sweep(ctx))
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}
