package application

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	d "github.com/AceNanako0721/Factorforge/src/factorforge/applications/console/domain"
	"github.com/AceNanako0721/Factorforge/src/factorforge/applications/console/ports"
	"strconv"
	"strings"
	"sync"
	"time"
)

var Views = map[string][]string{
	"overview": {"account", "positions", "protections", "run", "object", "pool", "decisions", "instance.health"},
	"market":   {"candles", "points", "instruments"}, "events": {"events"}, "event": {"event", "scores"}, "evidence": {"evidence"},
	"sentiment": {"pool", "ledger"}, "decisions": {"decisions", "strategy.targets", "trading.targets", "positions"},
	"execution": {"positions", "orders", "fills", "protections", "income", "owners", "external-facts"},
	"cases":     {"cases"}, "case": {"cases", "attributions", "counterfactuals"},
	"learning":   {"parameters", "learning", "activations", "validation"},
	"operations": {"trading.operations", "alerts", "object", "instance.health", "sources", "jobs", "budgets"},
	"reports":    {"reports"}, "audit": {"trading.audit", "strategy.audit", "instance.audit"},
}

type Filter struct {
	Source, Cursor, Interval, Start, End, From, To, Contribution, Queue, State string
	Revision, Limit                                                            int
}
type Panel struct {
	Source          string     `json:"source"`
	State           string     `json:"state"`
	Code            *string    `json:"code"`
	FetchedAt       time.Time  `json:"fetched_at"`
	ObservedAt      *string    `json:"observed_at"`
	SourceVersion   *string    `json:"source_version"`
	SnapshotVersion *string    `json:"snapshot_version"`
	Data            d.SafeDTO  `json:"data"`
	Cursor          *string    `json:"cursor"`
	RetryAt         *time.Time `json:"retry_at"`
}
type Response struct {
	SchemaVersion string      `json:"schema_version"`
	Selection     d.Selection `json:"selection"`
	GeneratedAt   time.Time   `json:"generated_at"`
	Panels        []Panel     `json:"panels"`
}
type ReadQuery struct {
	Client   ports.ReadClient
	Registry *SelectionRegistry
	Policy   d.Policy
	Now      func() time.Time
}
type pageCursor struct {
	Binding, Resource, Digest, Lower string
	Offset                           int
	Issued                           time.Time
}

func digest(v any) string {
	raw, _ := json.Marshal(v)
	hash := sha256.Sum256(raw)
	return hex.EncodeToString(hash[:])
}
func (q ReadQuery) sign(c pageCursor) string {
	raw, _ := json.Marshal(c)
	mac := hmac.New(sha256.New, q.Policy.CursorKey)
	mac.Write(raw)
	return base64.RawURLEncoding.EncodeToString(raw) + "." + base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
}
func (q ReadQuery) cursor(token, binding, resource string) (pageCursor, error) {
	bad := func() (pageCursor, error) { return pageCursor{}, d.Fail("QUERY_CURSOR_INVALID", 409) }
	parts := strings.Split(token, ".")
	if len(parts) != 2 || len(token) > 16384 {
		return bad()
	}
	raw, e := base64.RawURLEncoding.DecodeString(parts[0])
	sig, e2 := base64.RawURLEncoding.DecodeString(parts[1])
	mac := hmac.New(sha256.New, q.Policy.CursorKey)
	mac.Write(raw)
	var c pageCursor
	if e != nil || e2 != nil || !hmac.Equal(mac.Sum(nil), sig) || d.Decode(raw, &c) != nil || c.Binding != binding || c.Resource != resource || c.Offset < 0 || c.Issued.After(q.Now()) || q.Now().Sub(c.Issued) > q.Policy.CursorTTL {
		return bad()
	}
	return c, nil
}
func code(err error) (string, int) {
	var e *d.Error
	if errors.As(err, &e) {
		return e.Code, e.Status
	}
	return "LOWER_UNAVAILABLE", 503
}
func (q ReadQuery) Read(ctx context.Context, u d.User, selection, view, id string, f Filter, allowOriginal bool) (Response, error) {
	if q.Client == nil || q.Registry == nil || q.Now == nil || !q.Policy.Valid() {
		return Response{}, d.Fail("CONSOLE_NOT_CONFIGURED", 503)
	}
	s, e := q.Registry.Get(u, selection)
	if e != nil {
		return Response{}, e
	}
	resources, ok := Views[view]
	if !ok || id != "" && !d.ID(id) {
		return Response{}, d.Fail("QUERY_VIEW_INVALID", 422)
	}
	if f.Source != "" && !d.Has(resources, f.Source) || f.Cursor != "" && f.Source == "" {
		return Response{}, d.Fail("QUERY_PARAMETER_INVALID", 422)
	}
	if f.Limit == 0 {
		f.Limit = q.Policy.DefaultLimit
	}
	if f.Limit < 1 || f.Limit > q.Policy.MaxLimit {
		return Response{}, d.Fail("QUERY_LIMIT_INVALID", 422)
	}
	if view == "market" && (f.Interval == "" || f.Start == "" || f.End == "") {
		return Response{}, d.Fail("QUERY_RANGE_REQUIRED", 422)
	}
	out := Response{SchemaVersion: d.SchemaVersion, Selection: s, GeneratedAt: q.Now().UTC(), Panels: []Panel{}}
	selected := resources
	if f.Source != "" {
		selected = []string{f.Source}
	}
	services := map[string]error{}
	for _, r := range selected {
		services[d.Routes[r].Service] = nil
	}
	var mu sync.Mutex
	var wg sync.WaitGroup
	serviceNames := make([]string, 0, len(services))
	for svc := range services {
		serviceNames = append(serviceNames, svc)
	}
	for _, svc := range serviceNames {
		wg.Add(1)
		go func(service string) {
			defer wg.Done()
			err := Validate(ctx, q.Client, s, service, q.Policy.MaxPages)
			mu.Lock()
			services[service] = err
			mu.Unlock()
		}(svc)
	}
	wg.Wait()
	for _, err := range services {
		if err != nil {
			c, _ := code(err)
			if c == "BINDING_MISMATCH" {
				return Response{}, err
			}
		}
	}
	panels := make([]Panel, len(selected))
	var hard error
	for i, r := range selected {
		wg.Add(1)
		go func(index int, resource string) {
			defer wg.Done()
			p := Panel{Source: resource, FetchedAt: q.Now().UTC(), State: "AVAILABLE"}
			route := d.Routes[resource]
			if err := services[route.Service]; err != nil {
				p.RetryAt = retryAt(err, q.Now())
				c, status := code(err)
				p.Code = &c
				p.State = "UNAVAILABLE"
				if c == "READ_NOT_APPLICABLE" {
					p.State = "NOT_APPLICABLE"
				}
				if c == "READ_NOT_CONFIGURED" {
					p.State = "NOT_CONFIGURED"
				}
				if status == 401 || status == 403 {
					p.State = "FORBIDDEN"
				}
				panels[index] = p
				return
			}
			filter := map[string]string{}
			for k, v := range map[string]string{"interval": f.Interval, "start": f.Start, "end": f.End, "from": f.From, "to": f.To, "contribution_id": f.Contribution, "queue_kind": f.Queue, "state": f.State} {
				if v != "" && d.Has(route.Query, k) {
					filter[k] = v
				}
			}
			if d.Has(route.Query, "limit") {
				filter["limit"] = strconv.Itoa(f.Limit)
			}
			if f.Revision > 0 && d.Has(route.Query, "revision") {
				filter["revision"] = strconv.Itoa(f.Revision)
			}
			fixed := f
			fixed.Cursor = ""
			binding := digest([]any{s, u.ID, u.AuthorizationVersion, view, id, fixed})
			var cur pageCursor
			if f.Cursor != "" {
				var err error
				cur, err = q.cursor(f.Cursor, binding, resource)
				if err != nil {
					mu.Lock()
					hard = err
					mu.Unlock()
					return
				}
				if cur.Lower != "" {
					filter["cursor"] = cur.Lower
				}
			}
			raw, err := q.Client.Get(ctx, s, resource, id, filter)
			if err == nil && len(raw) > q.Policy.MaxBytes {
				err = d.Fail("LOWER_RESPONSE_BUDGET", 503)
			}
			if err == nil {
				value, e := d.ProjectBound(route.Service, route.Path, raw, allowOriginal && view == "evidence", s.ObjectID)
				err = e
				if err == nil {
					safe := value.Bytes()
					var node any
					if d.Decode(safe, &node) != nil {
						err = d.Fail("LOWER_RESPONSE_INVALID", 503)
					} else {
						var rows []any
						var object map[string]any
						switch x := node.(type) {
						case []any:
							rows = x
						case map[string]any:
							object = x
							if xs, ok := x["items"].([]any); ok {
								rows = xs
							}
							for k, target := range map[string]**string{"observed_at": &p.ObservedAt, "source_version": &p.SourceVersion, "snapshot_version": &p.SnapshotVersion} {
								if v, ok := x[k].(string); ok {
									copy := v
									*target = &copy
								} else if n, ok := x[k].(json.Number); ok && k == "snapshot_version" {
									copy := n.String()
									*target = &copy
								}
							}
						}
						if object != nil {
							if env, ok := object["environment"].(string); ok && env != s.Environment {
								err = d.Fail("BINDING_MISMATCH", 409)
							}
							if inst, ok := object["instance_id"].(string); ok && inst != s.InstanceID {
								err = d.Fail("BINDING_MISMATCH", 409)
							}
						}
						if rows != nil {
							if len(rows) > q.Policy.MaxRecords {
								err = d.Fail("QUERY_RECORD_BUDGET", 503)
							}
							snap := digest(raw)
							offset := cur.Offset
							if cur.Digest != "" && cur.Digest != snap {
								err = d.Fail("QUERY_SNAPSHOT_CHANGED", 409)
							}
							if offset > len(rows) {
								err = d.Fail("QUERY_CURSOR_INVALID", 409)
							}
							if err == nil {
								// Detail access to a case is restricted to the selected object's list.
								if view == "case" && resource == "cases" {
									matched := []any{}
									for _, row := range rows {
										if x, ok := row.(map[string]any); ok && x["case_id"] == id {
											matched = append(matched, row)
										}
									}
									rows = matched
									if len(rows) != 1 {
										err = d.Fail("CASE_SCOPE_FORBIDDEN", 403)
									}
								}
								if err == nil {
									end := offset + f.Limit
									if end > len(rows) {
										end = len(rows)
									}
									next := ""
									if end < len(rows) {
										next = q.sign(pageCursor{Binding: binding, Resource: resource, Digest: snap, Offset: end, Issued: q.Now().UTC()})
									} else if object != nil {
										if lower, ok := object["cursor"].(string); ok && lower != "" {
											next = q.sign(pageCursor{Binding: binding, Resource: resource, Lower: lower, Issued: q.Now().UTC()})
										}
									}
									if next != "" {
										p.Cursor = &next
									}
									if len(rows) == 0 {
										p.State = "EMPTY"
									}
									caseID := ""
									if view == "case" && resource == "cases" {
										caseID = id
									}
									value, e = value.Window(offset, f.Limit, caseID)
									err = e
								}
							}
						}
						p.Data = value
					}
				}
			}
			if err != nil {
				p.RetryAt = retryAt(err, q.Now())
				p.Data = d.SafeDTO{}
				p.Cursor = nil
				c, status := code(err)
				p.State = "UNAVAILABLE"
				p.Code = &c
				if status == 401 || status == 403 {
					p.State = "FORBIDDEN"
				}
				if c == "LOWER_NOT_FOUND" {
					p.State = "MISSING"
				}
				if status == 409 {
					mu.Lock()
					hard = err
					mu.Unlock()
				}
			}
			panels[index] = p
		}(i, r)
	}
	wg.Wait()
	if hard != nil {
		return Response{}, hard
	}
	if _, e = q.Registry.Get(u, selection); e != nil {
		return Response{}, e
	}
	out.Panels = panels
	return out, nil
}

func retryAt(err error, now time.Time) *time.Time {
	var e *d.Error
	if errors.As(err, &e) && e.Status == 429 && e.RetryAfter > 0 {
		at := now.UTC().Add(e.RetryAfter)
		return &at
	}
	return nil
}
