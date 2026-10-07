// Package api serves distinct public research and internal workload listeners.
package api

import (
	"crypto/subtle"
	"errors"
	app "github.com/AceNanako0721/Factorforge/src/factorforge/strategy/application"
	d "github.com/AceNanako0721/Factorforge/src/factorforge/strategy/domain"
	"github.com/AceNanako0721/Factorforge/src/factorforge/strategy/ports"
	"net/http"
	"strings"
	"time"
)

const Prefix = "/api/v2/strategy"

type Options struct {
	Store      ports.Store
	Clock      ports.Clock
	Tokens     map[string]d.Identity
	Internal   bool
	Cycle      *app.DecisionCycle
	ReadPolicy app.ReadPolicy
}
type endpoint func(http.ResponseWriter, *http.Request, d.Identity) (any, int, error)

func New(options Options) (http.Handler, error) {
	for token, identity := range options.Tokens {
		if token == "" || identity == nil || options.Internal != (identity.Worker() != nil) {
			return nil, &d.Error{Code: "AUTH_LISTENER_IDENTITY_TYPE_MISMATCH", Status: 422}
		}
	}
	if len(options.Tokens) == 0 {
		return nil, &d.Error{Code: "AUTHENTICATION_CONFIGURATION_REQUIRED", Status: 503}
	}
	mux := http.NewServeMux()
	service := app.Service{Store: options.Store, Clock: options.Clock}
	respond := func(w http.ResponseWriter, status int, value any) {
		raw, err := d.Marshal(value)
		if err != nil {
			status = 503
			raw = []byte(`{"code":"RESPONSE_UNAVAILABLE","message":"RESPONSE_UNAVAILABLE","field":null,"correlation_id":null,"retryable":false}`)
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_, _ = w.Write(raw)
	}
	reject := func(w http.ResponseWriter, r *http.Request, identity d.Identity, err error) {
		code, status := "RUNTIME_UNAVAILABLE", 503
		var known *d.Error
		if errors.As(err, &known) {
			code, status = known.Code, known.Status
		}
		if identity != nil {
			_ = options.Store.Transaction(r.Context(), identity.Instance(), func(s *d.StrategyState) error {
				row := map[string]any{"action": "REQUEST_REJECTED", "code": code, "at": d.ISO(options.Clock.Now())}
				if code != "INVALID_REQUEST" {
					row["actor"] = identity.Payload()
				}
				s.Audit = append(s.Audit, row)
				s.Version++
				return nil
			})
		}
		var correlation *string
		if code != "INVALID_REQUEST" && r.Header.Get("X-Request-ID") != "" {
			correlation = d.Ptr(r.Header.Get("X-Request-ID"))
		}
		respond(w, status, d.Problem{Code: code, Message: code, CorrelationID: correlation})
	}
	registerWithAudit := func(method, path string, fn endpoint, audit bool) {
		mux.HandleFunc(method+" "+Prefix+path, func(w http.ResponseWriter, r *http.Request) {
			var identity d.Identity
			header := r.Header.Get("Authorization")
			scheme, credential, found := strings.Cut(header, " ")
			if found && strings.EqualFold(scheme, "Bearer") {
				for token, p := range options.Tokens {
					if subtle.ConstantTimeCompare([]byte(token), []byte(credential)) == 1 {
						identity = p
						break
					}
				}
			}
			if identity == nil {
				reject(w, r, nil, &d.Error{Code: "AUTHENTICATION_REQUIRED", Status: 401})
				return
			}
			var value any
			var status int
			err := d.Guard(func() error { v, s, e := fn(w, r, identity); value, status = v, s; return e })
			if err != nil {
				if !audit {
					identity = nil
				}
				reject(w, r, identity, err)
				return
			}
			respond(w, status, value)
		})
	}
	register := func(method, path string, fn endpoint) { registerWithAudit(method, path, fn, true) }
	trace := app.QueryService{Store: options.Store, Clock: options.Clock, Policy: options.ReadPolicy}
	for _, route := range []struct{ path, resource, id string }{
		{"/objects", "objects", ""}, {"/events", "events", ""}, {"/events/{event_id}", "event", "event_id"},
		{"/events/{event_id}/scores", "scores", "event_id"}, {"/objects/{object_id}/ledger", "ledger", "object_id"},
		{"/cases/{case_id}/attributions", "attributions", "case_id"}, {"/cases/{case_id}/counterfactuals", "counterfactuals", "case_id"},
		{"/parameter-activations", "parameter-activations", ""}, {"/audit", "audit", ""},
	} {
		registerWithAudit("GET", route.path, func(_ http.ResponseWriter, r *http.Request, p d.Identity) (any, int, error) {
			filter, err := readFilter(r)
			if err != nil {
				return nil, 0, err
			}
			page, err := trace.Trace(r.Context(), p, route.resource, r.PathValue(route.id), filter)
			return page, 200, err
		}, false)
	}
	query := func(r *http.Request, identity d.Identity, id string) (*d.StrategyState, error) {
		s, err := options.Store.Read(r.Context(), identity.Instance())
		if err != nil {
			return nil, err
		}
		if err = app.Authorize(s, identity, d.Query, id); err != nil {
			return nil, err
		}
		if id != "" && s.Objects.Value(id) == nil {
			return nil, &d.Error{Code: "OBJECT_NOT_FOUND", Status: 404}
		}
		return s, nil
	}
	mux.HandleFunc("GET "+Prefix+"/health", func(w http.ResponseWriter, r *http.Request) {
		listener := "PUBLIC_RESEARCH"
		if options.Internal {
			listener = "WORKLOAD"
		}
		respond(w, 200, map[string]any{"schema_version": "strategy-2.0", "application_required": false, "environment": options.Store.Environment(), "listener": listener, "live_ready": false})
	})
	mux.HandleFunc("GET /openapi.json", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(OpenAPI(options.Internal))
	})
	register("POST", "/objects", func(_ http.ResponseWriter, r *http.Request, p d.Identity) (any, int, error) {
		var body d.CreateObject
		if err := d.Decode(r.Body, &body); err != nil {
			return nil, 0, err
		}
		v, e := service.Create(r.Context(), p, body)
		return v, 201, e
	})
	register("GET", "/objects/{object_id}", func(_ http.ResponseWriter, r *http.Request, p d.Identity) (any, int, error) {
		s, e := query(r, p, r.PathValue("object_id"))
		if e != nil {
			return nil, 0, e
		}
		return s.Objects.Value(r.PathValue("object_id")), 200, nil
	})
	register("PATCH", "/objects/{object_id}/state", func(_ http.ResponseWriter, r *http.Request, p d.Identity) (any, int, error) {
		var body d.StateCommand
		if err := d.Decode(r.Body, &body); err != nil {
			return nil, 0, err
		}
		id := r.PathValue("object_id")
		if _, err := query(r, p, id); err != nil {
			return nil, 0, err
		}
		v, e := service.SetState(r.Context(), p, body, id)
		return v, 200, e
	})
	event := func(revision bool) endpoint {
		return func(_ http.ResponseWriter, r *http.Request, p d.Identity) (any, int, error) {
			var body d.EventCommand
			if err := d.Decode(r.Body, &body); err != nil {
				return nil, 0, err
			}
			if revision && r.PathValue("event_id") != body.Event.EventID {
				return nil, 0, &d.Error{Code: "EVENT_PATH_MISMATCH", Status: 422}
			}
			v, e := service.RegisterEvent(r.Context(), p, body)
			status := 201
			if revision {
				status = 200
			}
			return v, status, e
		}
	}
	register("POST", "/events", event(false))
	register("POST", "/events/{event_id}/revisions", event(true))
	score := func(revision bool) endpoint {
		return func(_ http.ResponseWriter, r *http.Request, p d.Identity) (any, int, error) {
			var body d.ScoreCommand
			if err := d.Decode(r.Body, &body); err != nil {
				return nil, 0, err
			}
			if revision {
				if body.Score.PreviousScoreID == nil || *body.Score.PreviousScoreID != r.PathValue("score_id") || body.Score.EventID != r.PathValue("event_id") || body.Score.RevisionKind != "REVISION" {
					return nil, 0, &d.Error{Code: "SCORE_PATH_MISMATCH", Status: 422}
				}
			} else if body.Score.EventID != r.PathValue("event_id") {
				return nil, 0, &d.Error{Code: "EVENT_PATH_MISMATCH", Status: 422}
			}
			v, e := service.Submit(r.Context(), p, body)
			return v, 200, e
		}
	}
	register("POST", "/events/{event_id}/scores", score(false))
	register("POST", "/events/{event_id}/scores/{score_id}/revisions", score(true))
	register("GET", "/objects/{object_id}/pool", func(_ http.ResponseWriter, r *http.Request, p d.Identity) (any, int, error) {
		s, e := query(r, p, r.PathValue("object_id"))
		if e != nil {
			return nil, 0, e
		}
		v, e := s.Pool(r.PathValue("object_id"))
		return v, 200, e
	})
	for _, resource := range []string{"decisions", "targets", "cases"} {
		name := resource
		register("GET", "/objects/{object_id}/"+name, func(_ http.ResponseWriter, r *http.Request, p d.Identity) (any, int, error) {
			id := r.PathValue("object_id")
			s, e := query(r, p, id)
			if e != nil {
				return nil, 0, e
			}
			rows := []any{}
			switch name {
			case "decisions":
				for _, v := range s.Decisions.Values() {
					if v.ObjectID == id {
						rows = append(rows, v)
					}
				}
			case "targets":
				for _, v := range s.Outbox.Values() {
					if v.Decision.ObjectID == id {
						rows = append(rows, v)
					}
				}
			case "cases":
				for _, v := range s.Cases.Values() {
					if v.ObjectID == id {
						rows = append(rows, v)
					}
				}
			}
			return rows, 200, nil
		})
	}
	register("POST", "/cases/{case_id}/attribution-candidates", func(_ http.ResponseWriter, r *http.Request, p d.Identity) (any, int, error) {
		var body d.AttributionCommand
		if err := d.Decode(r.Body, &body); err != nil {
			return nil, 0, err
		}
		v, e := service.Attribution(r.Context(), p, body, r.PathValue("case_id"))
		return v, 200, e
	})
	for _, resource := range []string{"parameters", "learning-decisions", "validation-runs"} {
		name := resource
		register("GET", "/"+name, func(_ http.ResponseWriter, r *http.Request, p d.Identity) (any, int, error) {
			s, e := query(r, p, "")
			if e != nil {
				return nil, 0, e
			}
			switch name {
			case "parameters":
				return s.Parameters.Values(), 200, nil
			case "learning-decisions":
				return s.LearningDecisions, 200, nil
			default:
				return s.ValidationRuns.Values(), 200, nil
			}
		})
	}
	if options.Internal && options.Cycle != nil {
		register("POST", "/clock", func(_ http.ResponseWriter, r *http.Request, p d.Identity) (any, int, error) {
			var body d.AdvanceClock
			if err := d.Decode(r.Body, &body); err != nil {
				return nil, 0, err
			}
			clock, ok := options.Clock.(interface{ Advance(time.Time) error })
			if options.Store.Environment() != "SIM" || !ok || body.At.Before(options.Clock.Now()) {
				return nil, 0, &d.Error{Code: "REPLAY_CLOCK_UNAVAILABLE_OR_REWIND", Status: 403}
			}
			if err := clock.Advance(body.At); err != nil {
				return nil, 0, err
			}
			v, e := options.Cycle.Tick(r.Context(), p, nil)
			if e != nil {
				return nil, 0, e
			}
			if e = options.Cycle.Dispatch(r.Context(), p); e != nil {
				return nil, 0, e
			}
			return v, 200, nil
		})
	}
	return mux, nil
}
