package api

import (
	app "github.com/AceNanako0721/Factorforge/src/factorforge/strategy/application"
	d "github.com/AceNanako0721/Factorforge/src/factorforge/strategy/domain"
	"net/http"
	"net/url"
	"strconv"
	"time"
)

func readFilter(r *http.Request) (app.ReadFilter, error) {
	f := app.ReadFilter{}
	invalid := func() (app.ReadFilter, error) { return f, &d.Error{Code: "QUERY_FILTER_INVALID", Status: 422} }
	values, err := url.ParseQuery(r.URL.RawQuery)
	if err != nil {
		return invalid()
	}
	for key, v := range values {
		if len(v) != 1 || v[0] == "" || !d.Has([]string{"object_id", "contribution_id", "cursor", "limit", "revision", "from", "to"}, key) {
			return invalid()
		}
	}
	f.ObjectID = values.Get("object_id")
	f.ContributionID = values.Get("contribution_id")
	f.Cursor = values.Get("cursor")
	for key, target := range map[string]*int{"limit": &f.Limit, "revision": &f.Revision} {
		if value := values.Get(key); value != "" {
			*target, err = strconv.Atoi(value)
			if err != nil || *target < 1 {
				return invalid()
			}
		}
	}
	for key, target := range map[string]**time.Time{"from": &f.From, "to": &f.To} {
		if value := values.Get(key); value != "" {
			var at time.Time
			if d.Guard(func() error { at = d.At(value); return nil }) != nil {
				return invalid()
			}
			*target = &at
		}
	}
	return f, nil
}
