package application

import (
	"context"
	d "github.com/AceNanako0721/Factorforge/src/factorforge/operations/agent/domain"
	"github.com/AceNanako0721/Factorforge/src/factorforge/operations/agent/ports"
	"strings"
	"time"
	"unicode/utf8"
)

// Records changes the agent's own records on behalf of one profile
// (S6-006, S6-007, S6-009). Every change is audited.
type Records struct {
	Store   ports.Store
	Profile string
	Now     func() time.Time
}

const maxText = 4000

func text(s string) (string, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return "", d.Fail("TEXT_REQUIRED")
	}
	if utf8.RuneCountInString(s) > maxText || !utf8.ValidString(s) {
		return "", d.Fail("TEXT_TOO_LONG")
	}
	return s, nil
}

func (r Records) audit(ctx context.Context, tool, args string, err error) {
	result := "OK"
	if err != nil {
		result = d.Code(err)
	}
	if utf8.RuneCountInString(args) > 300 {
		args = string([]rune(args)[:300])
	}
	_ = r.Store.AddAudit(ctx, d.Audit{At: r.Now().UTC(), Profile: r.Profile, Tool: tool, Args: args, Result: result})
}

// OpenIssue opens an issue by hand (manager only).
func (r Records) OpenIssue(ctx context.Context, title, severity string, instances []string, note string) (iss d.Issue, err error) {
	defer func() { r.audit(ctx, "open_issue", title, err) }()
	if r.Profile != d.Manager {
		return d.Issue{}, d.Fail("PROFILE_FORBIDDEN")
	}
	if title, err = text(title); err != nil {
		return
	}
	if note, err = text(note); err != nil {
		return
	}
	if !d.Has(d.Severities, severity) {
		return d.Issue{}, d.Fail("SEVERITY_INVALID")
	}
	for _, i := range instances {
		if !d.ID(i) {
			return d.Issue{}, d.Fail("INSTANCE_INVALID")
		}
	}
	at := r.Now().UTC()
	return r.Store.OpenIssue(ctx, d.Issue{Title: title, Severity: severity, State: d.Open, Source: d.Manager, Instances: instances, FirstSeen: at, LastSeen: at, Occurrences: 1},
		d.IssueEntry{At: at, Actor: r.Profile, Kind: "NOTE", Text: note})
}

// Note appends evidence or a note to an issue (both profiles).
func (r Records) Note(ctx context.Context, id, note string, evidence map[string]any) (err error) {
	defer func() { r.audit(ctx, "add_issue_note", id, err) }()
	if note, err = text(note); err != nil {
		return
	}
	i, _, err := r.Store.Issue(ctx, id)
	if err != nil {
		return err
	}
	if i.State == d.Closed {
		return d.Fail("ISSUE_CLOSED")
	}
	kind := "NOTE"
	if len(evidence) > 0 {
		kind = "EVIDENCE"
	}
	return r.Store.Append(ctx, d.IssueEntry{Issue: id, At: r.Now().UTC(), Actor: r.Profile, Kind: kind, Text: note, Evidence: evidence}, i.State, "")
}

// Move changes an issue's state under the registered transitions. Closing
// carries the owner's words (S6-006).
func (r Records) Move(ctx context.Context, id, to, note, owner string) (err error) {
	defer func() { r.audit(ctx, "move_issue", id+"→"+to, err) }()
	i, _, err := r.Store.Issue(ctx, id)
	if err != nil {
		return err
	}
	owner = strings.TrimSpace(owner)
	e, err := d.Move(i, to, r.Profile, strings.TrimSpace(note), owner, r.Now().UTC())
	if err != nil {
		return err
	}
	return r.Store.Append(ctx, e, i.State, "")
}

// Fixed records the fix version and moves the issue to VERIFYING
// (developer only).
func (r Records) Fixed(ctx context.Context, id, version, note string) (err error) {
	defer func() { r.audit(ctx, "set_issue_fixed", id+" "+version, err) }()
	if r.Profile != d.Developer {
		return d.Fail("PROFILE_FORBIDDEN")
	}
	if !d.ID(version) {
		return d.Fail("VERSION_INVALID")
	}
	i, _, err := r.Store.Issue(ctx, id)
	if err != nil {
		return err
	}
	e, err := d.Move(i, d.Verifying, r.Profile, strings.TrimSpace(note), "", r.Now().UTC())
	if err != nil {
		return err
	}
	e.Kind = "FIX"
	return r.Store.Append(ctx, e, i.State, version)
}

// Verify records that a fixed issue no longer occurs (manager only); the
// issue stays VERIFYING until the owner's words close it.
func (r Records) Verify(ctx context.Context, id, note string) (err error) {
	defer func() { r.audit(ctx, "verify_issue", id, err) }()
	if r.Profile != d.Manager {
		return d.Fail("PROFILE_FORBIDDEN")
	}
	if note, err = text(note); err != nil {
		return
	}
	i, _, err := r.Store.Issue(ctx, id)
	if err != nil {
		return err
	}
	if i.State != d.Verifying {
		return d.Fail("ISSUE_NOT_VERIFYING")
	}
	return r.Store.Append(ctx, d.IssueEntry{Issue: id, At: r.Now().UTC(), Actor: r.Profile, Kind: "VERIFY", Text: note}, i.State, "")
}

// Report writes a patrol report.
func (r Records) Report(ctx context.Context, summary string, findings, handled, issues []string) (id int64, err error) {
	defer func() { r.audit(ctx, "write_report", summary, err) }()
	if summary, err = text(summary); err != nil {
		return
	}
	return r.Store.AddReport(ctx, d.Report{At: r.Now().UTC(), Actor: r.Profile, Summary: summary, Findings: findings, Handled: handled, Issues: issues})
}

// Decision records the owner's words.
func (r Records) Decision(ctx context.Context, words, summary string, instances, issues []string) (id int64, err error) {
	defer func() { r.audit(ctx, "record_owner_decision", summary, err) }()
	if words, err = text(words); err != nil {
		return
	}
	if summary, err = text(summary); err != nil {
		return
	}
	return r.Store.AddDecision(ctx, d.OwnerDecision{At: r.Now().UTC(), Actor: r.Profile, Words: words, Summary: summary, Instances: instances, Issues: issues})
}

// Segment registers an exclusion window.
func (r Records) Segment(ctx context.Context, instance string, from, to time.Time, reason, issue string) (s d.Segment, err error) {
	defer func() { r.audit(ctx, "add_exclusion", instance, err) }()
	if instance != "*" && !d.ID(instance) || !to.After(from) {
		return d.Segment{}, d.Fail("SEGMENT_INVALID")
	}
	if reason, err = text(reason); err != nil {
		return
	}
	return r.Store.AddSegment(ctx, d.Segment{Instance: instance, From: from.UTC(), To: to.UTC(), Reason: reason, Issue: issue})
}
