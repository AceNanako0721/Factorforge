// Package postgres keeps the operations agent's records in its own schema.
// Records are appended or advanced by state; nothing is deleted (S6-007).
package postgres

import (
	"context"
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	d "github.com/AceNanako0721/Factorforge/src/factorforge/operations/agent/domain"
	"github.com/AceNanako0721/Factorforge/src/factorforge/operations/agent/ports"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"regexp"
	"sort"
	"strings"
	"time"
)

//go:embed migrations/*.sql
var migrations embed.FS

var schemaName = regexp.MustCompile(`^[a-z][a-z0-9_]{0,40}$`)

// MergeGap is the longest gap over which consecutive sweeps with the same
// hits still merge into one check record, and after which a returning hit
// counts as a new occurrence of its issue.
const MergeGap = 30 * time.Minute

type Store struct {
	pool   *pgxpool.Pool
	schema string
}

// Open connects and applies the migrations.
func Open(ctx context.Context, dsn, schema string) (*Store, error) {
	if !schemaName.MatchString(schema) {
		return nil, d.Fail("SCHEMA_INVALID")
	}
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		return nil, d.Fail("STORE_UNAVAILABLE")
	}
	s := &Store{pool: pool, schema: schema}
	entries, err := migrations.ReadDir("migrations")
	if err != nil {
		pool.Close()
		return nil, d.Fail("MIGRATION_UNAVAILABLE")
	}
	// The supervisor and every MCP process open the store; one advisory
	// lock serializes their migrations.
	tx, err := pool.Begin(ctx)
	if err != nil {
		pool.Close()
		return nil, d.Fail("STORE_UNAVAILABLE")
	}
	defer tx.Rollback(ctx)
	if _, err := tx.Exec(ctx, "SELECT pg_advisory_xact_lock(hashtext($1))", "factorforge-ops-agent:"+schema); err != nil {
		pool.Close()
		return nil, d.Fail("MIGRATION_FAILED")
	}
	for _, e := range entries {
		script, _ := migrations.ReadFile("migrations/" + e.Name())
		if _, err := tx.Exec(ctx, strings.ReplaceAll(string(script), "__SCHEMA__", schema)); err != nil {
			pool.Close()
			return nil, d.Fail("MIGRATION_FAILED")
		}
	}
	if err := tx.Commit(ctx); err != nil {
		pool.Close()
		return nil, d.Fail("MIGRATION_FAILED")
	}
	return s, nil
}

func (s *Store) Close() { s.pool.Close() }

func (s *Store) t(name string) string { return s.schema + "." + name }

func fail(err error) error {
	if err == nil {
		return nil
	}
	var de *d.Error
	if errors.As(err, &de) {
		return de
	}
	if errors.Is(err, pgx.ErrNoRows) {
		return d.Fail("NOT_FOUND")
	}
	return d.Fail("STORE_UNAVAILABLE")
}

func hitKey(hits []d.Hit) string {
	keys := make([]string, len(hits))
	for i, h := range hits {
		keys[i] = string(h.Level) + ":" + h.Source()
	}
	sort.Strings(keys)
	return strings.Join(keys, "|")
}

func js(v any) []byte {
	b, _ := json.Marshal(v)
	if string(b) == "null" {
		return []byte("[]")
	}
	return b
}

func (s *Store) RecordCheck(ctx context.Context, instance string, at time.Time, hits []d.Hit) error {
	key := hitKey(hits)
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return fail(err)
	}
	defer tx.Rollback(ctx)
	var id int64
	var to time.Time
	var version, last string
	err = tx.QueryRow(ctx, "SELECT id, to_at, rules_version, hit_key FROM "+s.t("check_record")+" WHERE instance=$1 ORDER BY to_at DESC, id DESC LIMIT 1 FOR UPDATE", instance).Scan(&id, &to, &version, &last)
	if err == nil && version == d.RulesVersion && last == key && at.Sub(to) <= MergeGap && !at.Before(to) {
		_, err = tx.Exec(ctx, "UPDATE "+s.t("check_record")+" SET to_at=$2, sweeps=sweeps+1, hits=$3 WHERE id=$1", id, at, js(hits))
	} else if err == nil || errors.Is(err, pgx.ErrNoRows) {
		_, err = tx.Exec(ctx, "INSERT INTO "+s.t("check_record")+" (instance, from_at, to_at, sweeps, rules_version, hit_key, hits) VALUES ($1,$2,$2,1,$3,$4,$5)", instance, at, d.RulesVersion, key, js(hits))
	}
	if err != nil {
		return fail(err)
	}
	return fail(tx.Commit(ctx))
}

func (s *Store) LastSweep(ctx context.Context, instance string) (time.Time, error) {
	var at *time.Time
	err := s.pool.QueryRow(ctx, "SELECT max(to_at) FROM "+s.t("check_record")+" WHERE instance=$1", instance).Scan(&at)
	if err != nil {
		return time.Time{}, fail(err)
	}
	if at == nil {
		return time.Time{}, nil
	}
	return at.UTC(), nil
}

func (s *Store) Checks(ctx context.Context, instance string, since time.Time, onlyHits bool, limit int) ([]d.Check, error) {
	q := "SELECT id, instance, from_at, to_at, sweeps, rules_version, hits FROM " + s.t("check_record") + " WHERE to_at >= $1 AND ($2 = '' OR instance = $2) AND (NOT $3 OR hit_key <> '') ORDER BY to_at DESC, id DESC LIMIT $4"
	rows, err := s.pool.Query(ctx, q, since, instance, onlyHits, limit)
	if err != nil {
		return nil, fail(err)
	}
	defer rows.Close()
	var out []d.Check
	for rows.Next() {
		var c d.Check
		var hits []byte
		if err := rows.Scan(&c.ID, &c.Instance, &c.From, &c.To, &c.Sweeps, &c.RulesVersion, &hits); err != nil {
			return nil, fail(err)
		}
		_ = json.Unmarshal(hits, &c.Hits)
		out = append(out, c)
	}
	return out, fail(rows.Err())
}

const issueCols = "id, title, severity, state, source, rule, instances, first_seen, last_seen, occurrences, fix_version, verified, legacy_id"

func scanIssue(row pgx.Row) (d.Issue, error) {
	var i d.Issue
	var inst []byte
	err := row.Scan(&i.ID, &i.Title, &i.Severity, &i.State, &i.Source, &i.Rule, &inst, &i.FirstSeen, &i.LastSeen, &i.Occurrences, &i.FixVersion, &i.Verified, &i.Legacy)
	_ = json.Unmarshal(inst, &i.Instances)
	return i, err
}

func (s *Store) insertIssue(ctx context.Context, tx pgx.Tx, i d.Issue) (d.Issue, error) {
	var seq int64
	if err := tx.QueryRow(ctx, "SELECT nextval('"+s.t("issue_seq")+"')").Scan(&seq); err != nil {
		return d.Issue{}, err
	}
	i.ID = fmt.Sprintf("I-%04d", seq)
	_, err := tx.Exec(ctx, "INSERT INTO "+s.t("issue")+" ("+issueCols+") VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13)",
		i.ID, i.Title, i.Severity, i.State, i.Source, i.Rule, js(i.Instances), i.FirstSeen, i.LastSeen, i.Occurrences, i.FixVersion, i.Verified, i.Legacy)
	return i, err
}

func (s *Store) entry(ctx context.Context, tx pgx.Tx, e d.IssueEntry) error {
	var ev []byte
	if e.Evidence != nil {
		ev, _ = json.Marshal(e.Evidence)
	}
	_, err := tx.Exec(ctx, "INSERT INTO "+s.t("issue_entry")+" (issue, at, actor, kind, from_state, to_state, body, owner_words, evidence) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9)",
		e.Issue, e.At, e.Actor, e.Kind, e.From, e.To, e.Text, e.Owner, ev)
	return err
}

func (s *Store) Occur(ctx context.Context, h d.Hit, severity string, at time.Time) (d.Issue, bool, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return d.Issue{}, false, fail(err)
	}
	defer tx.Rollback(ctx)
	i, err := scanIssue(tx.QueryRow(ctx, "SELECT "+issueCols+" FROM "+s.t("issue")+" WHERE source=$1 AND state<>'CLOSED' ORDER BY first_seen LIMIT 1 FOR UPDATE", h.Source()))
	created := false
	switch {
	case err == nil:
		if at.Sub(i.LastSeen) > MergeGap {
			i.Occurrences++
			if err = s.entry(ctx, tx, d.IssueEntry{Issue: i.ID, At: at, Actor: "supervisor", Kind: "OCCURRENCE", Text: h.Summary, Evidence: h.Evidence}); err != nil {
				return d.Issue{}, false, fail(err)
			}
		}
		if at.After(i.LastSeen) {
			i.LastSeen = at
		}
		_, err = tx.Exec(ctx, "UPDATE "+s.t("issue")+" SET last_seen=$2, occurrences=$3 WHERE id=$1", i.ID, i.LastSeen, i.Occurrences)
	case errors.Is(err, pgx.ErrNoRows):
		created = true
		if severity == "" {
			severity = "MEDIUM"
		}
		i, err = s.insertIssue(ctx, tx, d.Issue{Title: h.Rule + " " + h.Instance + "：" + h.Summary, Severity: severity, State: d.Open, Source: h.Source(), Rule: h.Rule, Instances: []string{h.Instance}, FirstSeen: at, LastSeen: at, Occurrences: 1})
		if err == nil {
			err = s.entry(ctx, tx, d.IssueEntry{Issue: i.ID, At: at, Actor: "supervisor", Kind: "OCCURRENCE", Text: h.Summary, Evidence: h.Evidence})
		}
	}
	if err != nil {
		return d.Issue{}, false, fail(err)
	}
	return i, created, fail(tx.Commit(ctx))
}

func (s *Store) OpenIssue(ctx context.Context, i d.Issue, first d.IssueEntry) (d.Issue, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return d.Issue{}, fail(err)
	}
	defer tx.Rollback(ctx)
	if i, err = s.insertIssue(ctx, tx, i); err != nil {
		return d.Issue{}, fail(err)
	}
	first.Issue = i.ID
	if err = s.entry(ctx, tx, first); err != nil {
		return d.Issue{}, fail(err)
	}
	return i, fail(tx.Commit(ctx))
}

func (s *Store) Issue(ctx context.Context, id string) (d.Issue, []d.IssueEntry, error) {
	i, err := scanIssue(s.pool.QueryRow(ctx, "SELECT "+issueCols+" FROM "+s.t("issue")+" WHERE id=$1", id))
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return d.Issue{}, nil, d.Fail("ISSUE_UNKNOWN")
		}
		return d.Issue{}, nil, fail(err)
	}
	rows, err := s.pool.Query(ctx, "SELECT at, actor, kind, from_state, to_state, body, owner_words, evidence FROM "+s.t("issue_entry")+" WHERE issue=$1 ORDER BY id", id)
	if err != nil {
		return d.Issue{}, nil, fail(err)
	}
	defer rows.Close()
	var out []d.IssueEntry
	for rows.Next() {
		e := d.IssueEntry{Issue: id}
		var ev []byte
		if err := rows.Scan(&e.At, &e.Actor, &e.Kind, &e.From, &e.To, &e.Text, &e.Owner, &ev); err != nil {
			return d.Issue{}, nil, fail(err)
		}
		if ev != nil {
			_ = json.Unmarshal(ev, &e.Evidence)
		}
		out = append(out, e)
	}
	return i, out, fail(rows.Err())
}

func (s *Store) Issues(ctx context.Context, f ports.IssueFilter) ([]d.Issue, error) {
	states := f.States
	if len(states) == 0 {
		states = []string{d.Open, d.Watch, d.Fixing, d.Verifying}
	}
	limit := f.Limit
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	rows, err := s.pool.Query(ctx, "SELECT "+issueCols+" FROM "+s.t("issue")+" WHERE state = ANY($1) AND ($2 = '' OR instances ? $2) ORDER BY last_seen DESC LIMIT $3", states, f.Instance, limit)
	if err != nil {
		return nil, fail(err)
	}
	defer rows.Close()
	var out []d.Issue
	for rows.Next() {
		i, err := scanIssue(rows)
		if err != nil {
			return nil, fail(err)
		}
		out = append(out, i)
	}
	return out, fail(rows.Err())
}

func (s *Store) Append(ctx context.Context, e d.IssueEntry, expect, fixVersion string) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return fail(err)
	}
	defer tx.Rollback(ctx)
	var state string
	if err := tx.QueryRow(ctx, "SELECT state FROM "+s.t("issue")+" WHERE id=$1 FOR UPDATE", e.Issue).Scan(&state); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return d.Fail("ISSUE_UNKNOWN")
		}
		return fail(err)
	}
	if state != expect {
		return d.Fail("ISSUE_CHANGED")
	}
	if err := s.entry(ctx, tx, e); err != nil {
		return fail(err)
	}
	switch e.Kind {
	case "STATE", "FIX":
		if _, err := tx.Exec(ctx, "UPDATE "+s.t("issue")+" SET state=$2, fix_version=CASE WHEN $3<>'' THEN $3 ELSE fix_version END, verified=CASE WHEN $2='FIXING' THEN false ELSE verified END WHERE id=$1", e.Issue, e.To, fixVersion); err != nil {
			return fail(err)
		}
	case "VERIFY":
		if _, err := tx.Exec(ctx, "UPDATE "+s.t("issue")+" SET verified=true WHERE id=$1", e.Issue); err != nil {
			return fail(err)
		}
	}
	return fail(tx.Commit(ctx))
}

func (s *Store) AddReport(ctx context.Context, r d.Report) (int64, error) {
	var id int64
	err := s.pool.QueryRow(ctx, "INSERT INTO "+s.t("report")+" (at, actor, summary, findings, handled, issues) VALUES ($1,$2,$3,$4,$5,$6) RETURNING id", r.At, r.Actor, r.Summary, js(r.Findings), js(r.Handled), js(r.Issues)).Scan(&id)
	return id, fail(err)
}

func (s *Store) Reports(ctx context.Context, limit int) ([]d.Report, error) {
	rows, err := s.pool.Query(ctx, "SELECT id, at, actor, summary, findings, handled, issues FROM "+s.t("report")+" ORDER BY id DESC LIMIT $1", limit)
	if err != nil {
		return nil, fail(err)
	}
	defer rows.Close()
	var out []d.Report
	for rows.Next() {
		var r d.Report
		var a, b, c []byte
		if err := rows.Scan(&r.ID, &r.At, &r.Actor, &r.Summary, &a, &b, &c); err != nil {
			return nil, fail(err)
		}
		_, _, _ = json.Unmarshal(a, &r.Findings), json.Unmarshal(b, &r.Handled), json.Unmarshal(c, &r.Issues)
		out = append(out, r)
	}
	return out, fail(rows.Err())
}

func (s *Store) AddDecision(ctx context.Context, o d.OwnerDecision) (int64, error) {
	var id int64
	err := s.pool.QueryRow(ctx, "INSERT INTO "+s.t("owner_decision")+" (at, actor, words, summary, instances, issues) VALUES ($1,$2,$3,$4,$5,$6) RETURNING id", o.At, o.Actor, o.Words, o.Summary, js(o.Instances), js(o.Issues)).Scan(&id)
	return id, fail(err)
}

func (s *Store) Decisions(ctx context.Context, limit int) ([]d.OwnerDecision, error) {
	rows, err := s.pool.Query(ctx, "SELECT id, at, actor, words, summary, instances, issues FROM "+s.t("owner_decision")+" ORDER BY id DESC LIMIT $1", limit)
	if err != nil {
		return nil, fail(err)
	}
	defer rows.Close()
	var out []d.OwnerDecision
	for rows.Next() {
		var o d.OwnerDecision
		var a, b []byte
		if err := rows.Scan(&o.ID, &o.At, &o.Actor, &o.Words, &o.Summary, &a, &b); err != nil {
			return nil, fail(err)
		}
		_, _ = json.Unmarshal(a, &o.Instances), json.Unmarshal(b, &o.Issues)
		out = append(out, o)
	}
	return out, fail(rows.Err())
}

func (s *Store) AddSegment(ctx context.Context, g d.Segment) (d.Segment, error) {
	var seq int64
	if err := s.pool.QueryRow(ctx, "SELECT nextval('"+s.t("exclusion_seq")+"')").Scan(&seq); err != nil {
		return d.Segment{}, fail(err)
	}
	g.ID = fmt.Sprintf("X-%04d", seq)
	_, err := s.pool.Exec(ctx, "INSERT INTO "+s.t("exclusion")+" (id, instance, from_at, to_at, reason, issue) VALUES ($1,$2,$3,$4,$5,$6)", g.ID, g.Instance, g.From, g.To, g.Reason, g.Issue)
	return g, fail(err)
}

func (s *Store) Segments(ctx context.Context, instance string) ([]d.Segment, error) {
	rows, err := s.pool.Query(ctx, "SELECT id, instance, from_at, to_at, reason, issue FROM "+s.t("exclusion")+" WHERE $1 = '' OR instance = $1 OR instance = '*' ORDER BY from_at", instance)
	if err != nil {
		return nil, fail(err)
	}
	defer rows.Close()
	var out []d.Segment
	for rows.Next() {
		var g d.Segment
		if err := rows.Scan(&g.ID, &g.Instance, &g.From, &g.To, &g.Reason, &g.Issue); err != nil {
			return nil, fail(err)
		}
		out = append(out, g)
	}
	return out, fail(rows.Err())
}

func (s *Store) AddAudit(ctx context.Context, a d.Audit) error {
	_, err := s.pool.Exec(ctx, "INSERT INTO "+s.t("tool_audit")+" (at, profile, tool, args, result) VALUES ($1,$2,$3,$4,$5)", a.At, a.Profile, a.Tool, a.Args, a.Result)
	return fail(err)
}
