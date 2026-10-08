package api

import (
	"bytes"
	"encoding/csv"
	"encoding/json"
	"errors"
	app "github.com/AceNanako0721/Factorforge/src/factorforge/applications/console/application"
	d "github.com/AceNanako0721/Factorforge/src/factorforge/applications/console/domain"
	"io"
	"mime"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
)

const Prefix = "/api/v2/console"
const sessionCookie = "__Host-factorforge-session"
const challengeCookie = "__Host-factorforge-challenge"

type Options struct {
	Sessions                   *app.SessionService
	Registry                   *app.SelectionRegistry
	Query                      app.ReadQuery
	Origin, StaticDir          string
	FixtureOnly                bool
	RefreshSeconds, MaxRetries int
}
type Problem struct {
	Code      string `json:"code"`
	Message   string `json:"message"`
	Retryable bool   `json:"retryable"`
}

func respond(w http.ResponseWriter, status int, value any) {
	raw, e := json.Marshal(value)
	if e != nil {
		status = 503
		raw = []byte(`{"code":"RESPONSE_UNAVAILABLE","message":"RESPONSE_UNAVAILABLE","retryable":false}`)
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	w.Write(raw)
}
func reject(w http.ResponseWriter, e error) {
	code, status := "CONSOLE_UNAVAILABLE", 503
	var known *d.Error
	if errors.As(e, &known) {
		code, status = known.Code, known.Status
	}
	if known != nil && status == 429 && known.RetryAfter > 0 {
		seconds := (known.RetryAfter + time.Second - 1) / time.Second
		w.Header().Set("Retry-After", strconv.FormatInt(int64(seconds), 10))
	}
	respond(w, status, Problem{code, code, status == 503})
}
func cookieID(r *http.Request, name string) string {
	c, e := r.Cookie(name)
	if e != nil {
		return ""
	}
	return c.Value
}
func setCookie(w http.ResponseWriter, name, value string, expires time.Time) {
	http.SetCookie(w, &http.Cookie{Name: name, Value: value, Path: "/", HttpOnly: true, Secure: true, SameSite: http.SameSiteStrictMode, Expires: expires})
}
func clearCookie(w http.ResponseWriter, name string) {
	http.SetCookie(w, &http.Cookie{Name: name, Value: "", Path: "/", HttpOnly: true, Secure: true, SameSite: http.SameSiteStrictMode, MaxAge: -1})
}
func peer(r *http.Request) string {
	host, _, e := net.SplitHostPort(r.RemoteAddr)
	if e != nil {
		return r.RemoteAddr
	}
	return host
}
func originAllowed(r *http.Request, origin string) bool {
	return r.Header.Get("Origin") == origin && r.Header.Get("Sec-Fetch-Site") != "cross-site"
}
func New(o Options) (http.Handler, error) {
	origin, e := url.Parse(o.Origin)
	if e != nil || origin.Host == "" || origin.User != nil || origin.Path != "" || origin.RawQuery != "" || origin.Fragment != "" || o.Sessions == nil || o.Registry == nil || !o.Query.Policy.Valid() || o.RefreshSeconds < 1 || o.MaxRetries < 0 {
		return nil, d.Fail("CONSOLE_CONFIGURATION_REQUIRED", 503)
	}
	if origin.Scheme != "https" {
		ip := net.ParseIP(origin.Hostname())
		if origin.Scheme != "http" || !o.FixtureOnly || ip == nil || !ip.IsLoopback() {
			return nil, d.Fail("CONSOLE_TLS_REQUIRED", 503)
		}
	}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /openapi.json", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write(OpenAPI())
	})
	mux.HandleFunc("GET "+Prefix+"/session/challenge", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.RawQuery != "" || r.Header.Get("Sec-Fetch-Site") == "cross-site" {
			reject(w, d.Fail("CSRF_REJECTED", 403))
			return
		}
		id, v, e := o.Sessions.Challenge(peer(r))
		if e != nil {
			reject(w, e)
			return
		}
		clearCookie(w, challengeCookie)
		setCookie(w, challengeCookie, id, v.ExpiresAt)
		respond(w, 200, v)
	})
	mux.HandleFunc("POST "+Prefix+"/session", func(w http.ResponseWriter, r *http.Request) {
		if !originAllowed(r, o.Origin) || r.URL.RawQuery != "" {
			reject(w, d.Fail("CSRF_REJECTED", 403))
			return
		}
		var input struct {
			Username string `json:"username"`
			Password string `json:"password"`
			CSRF     string `json:"csrf"`
		}
		raw, e := io.ReadAll(io.LimitReader(r.Body, int64(o.Query.Policy.MaxBytes)+1))
		if e != nil || len(raw) > o.Query.Policy.MaxBytes || d.Decode(raw, &input) != nil || input.Username == "" || input.Password == "" {
			reject(w, d.Fail("INVALID_REQUEST", 422))
			return
		}
		id, v, e := o.Sessions.Login(cookieID(r, challengeCookie), peer(r), input.Username, input.Password, input.CSRF)
		clearCookie(w, challengeCookie)
		if e != nil {
			reject(w, e)
			return
		}
		old := cookieID(r, sessionCookie)
		if _, previous, err := o.Sessions.Current(old); err == nil {
			_ = o.Sessions.Logout(old, previous.CSRF)
		}
		setCookie(w, sessionCookie, id, v.ExpiresAt)
		respond(w, 200, v)
	})
	mux.HandleFunc("GET "+Prefix+"/session", func(w http.ResponseWriter, r *http.Request) {
		_, v, e := o.Sessions.Current(cookieID(r, sessionCookie))
		if e != nil {
			clearCookie(w, sessionCookie)
			reject(w, e)
			return
		}
		if r.URL.RawQuery != "" {
			reject(w, d.Fail("QUERY_PARAMETER_INVALID", 422))
			return
		}
		respond(w, 200, v)
	})
	mux.HandleFunc("DELETE "+Prefix+"/session", func(w http.ResponseWriter, r *http.Request) {
		if !originAllowed(r, o.Origin) || r.URL.RawQuery != "" {
			reject(w, d.Fail("CSRF_REJECTED", 403))
			return
		}
		if e := o.Sessions.Logout(cookieID(r, sessionCookie), r.Header.Get("X-CSRF-Token")); e != nil {
			reject(w, e)
			return
		}
		clearCookie(w, sessionCookie)
		w.WriteHeader(204)
	})
	authorize := func(w http.ResponseWriter, r *http.Request) (d.User, bool) {
		u, _, e := o.Sessions.Current(cookieID(r, sessionCookie))
		if e != nil {
			clearCookie(w, sessionCookie)
			reject(w, e)
			return d.User{}, false
		}
		return u, true
	}
	mux.HandleFunc("GET "+Prefix+"/selections", func(w http.ResponseWriter, r *http.Request) {
		u, ok := authorize(w, r)
		if !ok {
			return
		}
		q, e := strictQuery(r.URL.RawQuery, []string{"environment"})
		if e != nil {
			reject(w, e)
			return
		}
		environment := q.Get("environment")
		if environment != "" && !d.Has([]string{"SIM", "LIVE"}, environment) {
			reject(w, d.Fail("QUERY_ENVIRONMENT_INVALID", 422))
			return
		}
		respond(w, 200, o.Registry.List(u, environment))
	})
	mux.HandleFunc("GET "+Prefix+"/capabilities", func(w http.ResponseWriter, r *http.Request) {
		u, ok := authorize(w, r)
		if !ok {
			return
		}
		query, e := strictQuery(r.URL.RawQuery, []string{"selection_id"})
		if e != nil {
			reject(w, e)
			return
		}
		var selection *d.Selection
		if id := query.Get("selection_id"); id != "" {
			s, e := o.Registry.Get(u, id)
			if e != nil {
				reject(w, e)
				return
			}
			selection = &s
		}
		pages := map[string]string{}
		if selection != nil {
			ctx, cancel := contextTimeout(r, o.Query.Policy.RequestTimeout)
			defer cancel()
			for _, service := range []string{"trading", "strategy", "instances"} {
				if err := app.Validate(ctx, o.Query.Client, *selection, service, o.Query.Policy.MaxPages); err != nil {
					var known *d.Error
					if errors.As(err, &known) && known.Code == "BINDING_MISMATCH" {
						reject(w, err)
						return
					}
				}
			}
		}
		for page := range app.Views {
			state := "AVAILABLE"
			if selection != nil && selection.ObjectID == "" && d.Has([]string{"events", "event", "sentiment", "cases", "case", "learning"}, page) {
				state = "NOT_APPLICABLE"
			}
			pages[page] = state
		}
		respond(w, 200, struct {
			Schema  string            `json:"schema_version"`
			Pages   map[string]string `json:"pages"`
			Refresh int               `json:"refresh_seconds"`
			Retries int               `json:"max_retries"`
		}{d.SchemaVersion, pages, o.RefreshSeconds, o.MaxRetries})
	})
	routes := map[string]string{"overview": "overview", "market": "market", "events": "events", "events/{event_id}": "event", "evidence/{evidence_id}": "evidence", "sentiment": "sentiment", "decisions": "decisions", "execution": "execution", "cases": "cases", "cases/{case_id}": "case", "learning": "learning", "operations": "operations", "reports": "reports", "audit": "audit"}
	for path, view := range routes {
		mux.HandleFunc("GET "+Prefix+"/"+path, func(w http.ResponseWriter, r *http.Request) {
			u, ok := authorize(w, r)
			if !ok {
				return
			}
			selection, f, e := filter(r, view, false)
			if e != nil {
				reject(w, e)
				return
			}
			id := r.PathValue("event_id")
			if id == "" {
				id = r.PathValue("case_id")
			}
			if id == "" {
				id = r.PathValue("evidence_id")
			}
			ctx, cancel := contextTimeout(r, o.Query.Policy.RequestTimeout)
			defer cancel()
			v, e := o.Query.Read(ctx, u, selection, view, id, f, view == "evidence")
			if e != nil {
				reject(w, e)
				return
			}
			if _, _, e = o.Sessions.Current(cookieID(r, sessionCookie)); e != nil {
				reject(w, e)
				return
			}
			respond(w, 200, v)
		})
	}
	mux.HandleFunc("GET "+Prefix+"/export", func(w http.ResponseWriter, r *http.Request) {
		u, ok := authorize(w, r)
		if !ok {
			return
		}
		if !d.Has(u.Capabilities, "export") {
			reject(w, d.Fail("EXPORT_FORBIDDEN", 403))
			return
		}
		view := r.URL.Query().Get("view")
		if !d.Has([]string{"overview", "market", "events", "sentiment", "decisions", "execution", "cases", "learning", "operations", "reports", "audit"}, view) {
			reject(w, d.Fail("EXPORT_VIEW_INVALID", 422))
			return
		}
		selection, f, e := filter(r, view, true)
		if e != nil {
			reject(w, e)
			return
		}
		format := r.URL.Query().Get("format")
		if format != "json" && format != "csv" {
			reject(w, d.Fail("EXPORT_FORMAT_INVALID", 422))
			return
		}
		ctx, cancel := contextTimeout(r, o.Query.Policy.RequestTimeout)
		defer cancel()
		v, e := o.Query.Read(ctx, u, selection, view, "", f, false)
		if e != nil {
			reject(w, e)
			return
		}
		if _, _, e = o.Sessions.Current(cookieID(r, sessionCookie)); e != nil {
			reject(w, e)
			return
		}
		w.Header().Set("Content-Disposition", "attachment; filename=\"factorforge-"+view+"."+format+"\"")
		if format == "json" {
			respond(w, 200, v)
			return
		}
		var buffer bytes.Buffer
		csvw := csv.NewWriter(&buffer)
		csvw.Write([]string{"selection_id", "binding_version", "generated_at", "source", "state", "field", "value"})
		var flatten func(string, any, PanelContext)
		flatten = func(path string, value any, p PanelContext) {
			switch x := value.(type) {
			case map[string]any:
				keys := []string{}
				for k := range x {
					keys = append(keys, k)
				}
				sort.Strings(keys)
				for _, k := range keys {
					flatten(path+"."+k, x[k], p)
				}
			case []any:
				for i, x := range x {
					flatten(path+"["+strconv.Itoa(i)+"]", x, p)
				}
			default:
				raw, _ := json.Marshal(value)
				text := string(raw)
				if s, ok := value.(string); ok {
					text = s
				}
				if strings.HasPrefix(text, "=") || strings.HasPrefix(text, "+") || strings.HasPrefix(text, "-") || strings.HasPrefix(text, "@") || strings.HasPrefix(text, "\t") || strings.HasPrefix(text, "\r") {
					text = "'" + text
				}
				csvw.Write([]string{v.Selection.ID, v.Selection.BindingVersion, v.GeneratedAt.Format(time.RFC3339Nano), p.Source, p.State, path, text})
			}
		}
		for _, p := range v.Panels {
			var value any
			d.Decode(p.Data.Bytes(), &value)
			flatten("data", value, PanelContext{p.Source, p.State})
		}
		csvw.Flush()
		if csvw.Error() != nil || buffer.Len() > o.Query.Policy.MaxBytes {
			reject(w, d.Fail("EXPORT_BUDGET", 503))
			return
		}
		w.Header().Set("Content-Type", "text/csv; charset=utf-8")
		w.Write(buffer.Bytes())
	})
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) { serveStatic(w, r, o.StaticDir) })
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Referrer-Policy", "no-referrer")
		w.Header().Set("Content-Security-Policy", "default-src 'self'; connect-src 'self'; script-src 'self'; style-src 'self' 'unsafe-inline'; img-src 'self' data:; frame-src 'none'; object-src 'none'; base-uri 'none'; frame-ancestors 'none'")
		if r.Header.Get("Sec-Fetch-Site") == "cross-site" {
			reject(w, d.Fail("CSRF_REJECTED", 403))
			return
		}
		if strings.HasPrefix(r.URL.Path, Prefix+"/") && r.Method != "GET" && !(r.URL.Path == Prefix+"/session" && (r.Method == "POST" || r.Method == "DELETE")) {
			reject(w, d.Fail("READ_ONLY", 405))
			return
		}
		if strings.HasPrefix(r.URL.Path, "/api/") && !strings.HasPrefix(r.URL.Path, Prefix+"/") {
			reject(w, d.Fail("RESOURCE_NOT_FOUND", 404))
			return
		}
		mux.ServeHTTP(w, r)
	}), nil
}

type PanelContext struct{ Source, State string }

func strictQuery(raw string, allowed []string) (url.Values, error) {
	q, e := url.ParseQuery(raw)
	if e != nil {
		return nil, d.Fail("QUERY_PARAMETER_INVALID", 422)
	}
	for k, v := range q {
		if !d.Has(allowed, k) || len(v) != 1 || v[0] == "" {
			return nil, d.Fail("QUERY_PARAMETER_INVALID", 422)
		}
	}
	return q, nil
}
func filter(r *http.Request, view string, export bool) (string, app.Filter, error) {
	keys := []string{"selection_id", "source", "cursor", "limit"}
	switch view {
	case "market":
		keys = append(keys, "interval", "start", "end")
	case "events", "reports", "audit", "learning":
		keys = append(keys, "from", "to")
	case "event":
		keys = append(keys, "revision")
	case "sentiment":
		keys = append(keys, "contribution_id")
	case "operations":
		keys = append(keys, "queue_kind", "state")
	}
	if export {
		keys = append(keys, "view", "format")
	}
	v, e := strictQuery(r.URL.RawQuery, keys)
	if e != nil {
		return "", app.Filter{}, e
	}
	f := app.Filter{Source: v.Get("source"), Cursor: v.Get("cursor"), Interval: v.Get("interval"), Start: v.Get("start"), End: v.Get("end"), From: v.Get("from"), To: v.Get("to"), Contribution: v.Get("contribution_id"), Queue: v.Get("queue_kind"), State: v.Get("state")}
	if !d.ID(v.Get("selection_id")) {
		return "", f, d.Fail("SELECTION_REQUIRED", 422)
	}
	for key, target := range map[string]*int{"limit": &f.Limit, "revision": &f.Revision} {
		if raw := v.Get(key); raw != "" {
			n, e := strconv.Atoi(raw)
			if e != nil || n < 1 {
				return "", f, d.Fail("QUERY_PARAMETER_INVALID", 422)
			}
			*target = n
		}
	}
	times := map[string]time.Time{}
	for _, k := range []string{"start", "end", "from", "to"} {
		if s := v.Get(k); s != "" {
			at, e := time.Parse(time.RFC3339Nano, s)
			if e != nil || at.IsZero() || !strings.HasSuffix(s, "Z") {
				return "", f, d.Fail("QUERY_TIME_INVALID", 422)
			}
			times[k] = at
		}
	}
	for _, pair := range [][2]string{{"start", "end"}, {"from", "to"}} {
		a, aok := times[pair[0]]
		b, bok := times[pair[1]]
		if aok && bok && !a.Before(b) {
			return "", f, d.Fail("QUERY_RANGE_INVALID", 422)
		}
	}
	return v.Get("selection_id"), f, nil
}
func serveStatic(w http.ResponseWriter, r *http.Request, root string) {
	if r.Method != "GET" && r.Method != "HEAD" {
		reject(w, d.Fail("READ_ONLY", 405))
		return
	}
	if strings.HasPrefix(r.URL.Path, "/api") || root == "" {
		reject(w, d.Fail("RESOURCE_NOT_FOUND", 404))
		return
	}
	path := r.URL.Path
	name := ""
	if d.Has([]string{"/", "/login", "/overview", "/market", "/events", "/sentiment", "/decisions", "/execution", "/cases", "/learning", "/operations", "/reports", "/audit"}, path) {
		name = "index.html"
	} else if strings.HasPrefix(path, "/assets/") {
		name = strings.TrimPrefix(path, "/")
		if strings.Contains(name, "..") || strings.HasSuffix(name, ".map") {
			reject(w, d.Fail("RESOURCE_NOT_FOUND", 404))
			return
		}
	} else if d.Has([]string{"/THIRD_PARTY_NOTICES.txt", "/lightweight-charts.LICENSE.txt"}, path) {
		name = strings.TrimPrefix(path, "/")
	} else {
		reject(w, d.Fail("RESOURCE_NOT_FOUND", 404))
		return
	}
	base, e := filepath.EvalSymlinks(root)
	if e != nil {
		reject(w, d.Fail("WEB_NOT_BUILT", 503))
		return
	}
	file := filepath.Join(base, filepath.FromSlash(name))
	actual, e := filepath.EvalSymlinks(file)
	relative, e2 := filepath.Rel(base, actual)
	if e != nil || e2 != nil || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		reject(w, d.Fail("RESOURCE_NOT_FOUND", 404))
		return
	}
	info, e := os.Stat(actual)
	if e != nil || !info.Mode().IsRegular() {
		reject(w, d.Fail("RESOURCE_NOT_FOUND", 404))
		return
	}
	kind := mime.TypeByExtension(filepath.Ext(name))
	if kind != "" {
		w.Header().Set("Content-Type", kind)
	}
	http.ServeFile(w, r, actual)
}
