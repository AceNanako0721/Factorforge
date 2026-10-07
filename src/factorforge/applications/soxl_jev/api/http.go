// Package api exposes only S3-018 GET projections. Public callers cannot write
// a route, claim analysis work, spend a provider budget or submit a score.
package api

import (
	"crypto/subtle"
	"encoding/json"
	"errors"
	d "github.com/AceNanako0721/Factorforge/src/factorforge/applications/soxl_jev/domain"
	"github.com/AceNanako0721/Factorforge/src/factorforge/applications/soxl_jev/operations"
	"net/http"
	"strings"
)

const Prefix = "/api/v2/instances/{instance_id}"

type Options struct {
	Query  operations.QueryService
	Tokens map[string]d.ReadPrincipal
}
type Problem struct {
	Code      string `json:"code"`
	Message   string `json:"message"`
	Retryable bool   `json:"retryable"`
}

func New(o Options) (http.Handler, error) {
	if o.Query.Store == nil || o.Query.Clock == nil || len(o.Tokens) == 0 {
		return nil, d.Fail("INSTANCE_API_NOT_CONFIGURED", 503)
	}
	tokens := map[string]d.ReadPrincipal{}
	for token, p := range o.Tokens {
		if token == "" || !p.Valid() || p.Binding != o.Query.Store.Binding() {
			return nil, d.Fail("INSTANCE_IDENTITY_INVALID", 503)
		}
		p.OriginalSources = append([]string(nil), p.OriginalSources...)
		tokens[token] = p
	}
	respond := func(w http.ResponseWriter, status int, value any) {
		raw, err := json.Marshal(value)
		if err != nil {
			status = 503
			raw = []byte(`{"code":"RESPONSE_UNAVAILABLE","message":"RESPONSE_UNAVAILABLE","retryable":false}`)
		}
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.WriteHeader(status)
		_, _ = w.Write(raw)
	}
	reject := func(w http.ResponseWriter, err error) {
		code, status := "INSTANCE_QUERY_UNAVAILABLE", 503
		var known *d.Error
		if errors.As(err, &known) {
			code, status = known.Code, known.Status
		}
		respond(w, status, Problem{code, code, status == 503})
	}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /openapi.json", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(OpenAPI())
	})
	for _, route := range []struct{ path, resource, id string }{{"/health", "health", ""}, {"/sources", "sources", ""}, {"/analysis-jobs", "analysis-jobs", ""}, {"/analysis-jobs/{job_id}", "analysis-job", "job_id"}, {"/evidence/{evidence_id}", "evidence", "evidence_id"}, {"/budgets", "budgets", ""}, {"/reports", "reports", ""}, {"/audit", "audit", ""}} {
		mux.HandleFunc("GET "+Prefix+route.path, func(w http.ResponseWriter, r *http.Request) {
			var p *d.ReadPrincipal
			scheme, credential, found := strings.Cut(r.Header.Get("Authorization"), " ")
			if found && strings.EqualFold(scheme, "Bearer") {
				for token, v := range tokens {
					if subtle.ConstantTimeCompare([]byte(token), []byte(credential)) == 1 {
						copy := v
						p = &copy
						break
					}
				}
			}
			if p == nil {
				reject(w, d.Fail("AUTHENTICATION_REQUIRED", 401))
				return
			}
			if r.PathValue("instance_id") != p.Binding.InstanceID {
				reject(w, d.Fail("INSTANCE_SCOPE_FORBIDDEN", 403))
				return
			}
			id := r.PathValue(route.id)
			if route.id != "" && !d.ValidID(id) {
				reject(w, d.Fail("QUERY_ID_INVALID", 422))
				return
			}
			filter, err := readFilter(r.URL.RawQuery, route.resource)
			if err != nil {
				reject(w, err)
				return
			}
			value, err := o.Query.Read(r.Context(), *p, route.resource, id, filter)
			if err != nil {
				reject(w, err)
				return
			}
			respond(w, 200, value)
		})
	}
	return mux, nil
}
