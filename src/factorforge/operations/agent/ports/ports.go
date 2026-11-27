// Package ports names what the operations agent needs from the outside: the
// lower layers' public reads, the host's read-only probes and its own store.
package ports

import (
	"context"
	"encoding/json"
	d "github.com/AceNanako0721/Factorforge/src/factorforge/operations/agent/domain"
	"time"
)

// Lower reads one fixed public route of one registered instance.
type Lower interface {
	Get(ctx context.Context, instance, route string, query map[string]string) (json.RawMessage, error)
}

// Host probes the registered units and the host itself; it never changes
// anything.
type Host interface {
	Units(ctx context.Context, units []string, since time.Time) []d.Unit
	MainRevision(ctx context.Context) string
	Host(ctx context.Context) d.HostSnapshot
}

// IssueFilter selects issues.
type IssueFilter struct {
	States   []string
	Instance string
	Limit    int
}

// Store keeps the agent's own records.
type Store interface {
	// RecordCheck writes one sweep: merged into the instance's latest record
	// when both are clean under the same rules version.
	RecordCheck(ctx context.Context, instance string, at time.Time, hits []d.Hit) error
	LastSweep(ctx context.Context, instance string) (time.Time, error)
	Checks(ctx context.Context, instance string, since time.Time, onlyHits bool, limit int) ([]d.Check, error)

	// Occur opens an issue for a hit's source or appends an occurrence to
	// the open one; it returns the issue and whether it is new.
	Occur(ctx context.Context, h d.Hit, severity string, at time.Time) (d.Issue, bool, error)
	OpenIssue(ctx context.Context, i d.Issue, first d.IssueEntry) (d.Issue, error)
	Issue(ctx context.Context, id string) (d.Issue, []d.IssueEntry, error)
	Issues(ctx context.Context, f IssueFilter) ([]d.Issue, error)
	// Append adds a history line; a STATE line also moves the issue, a FIX
	// line sets the fix version, a VERIFY line marks it verified. The
	// issue's state must still be expect (optimistic check).
	Append(ctx context.Context, e d.IssueEntry, expect string, fixVersion string) error

	AddReport(ctx context.Context, r d.Report) (int64, error)
	Reports(ctx context.Context, limit int) ([]d.Report, error)
	AddDecision(ctx context.Context, o d.OwnerDecision) (int64, error)
	Decisions(ctx context.Context, limit int) ([]d.OwnerDecision, error)
	AddSegment(ctx context.Context, s d.Segment) (d.Segment, error)
	Segments(ctx context.Context, instance string) ([]d.Segment, error)
	AddAudit(ctx context.Context, a d.Audit) error
}
