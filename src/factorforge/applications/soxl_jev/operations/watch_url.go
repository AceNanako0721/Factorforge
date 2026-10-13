package operations

import (
	d "github.com/AceNanako0721/Factorforge/src/factorforge/applications/soxl_jev/domain"
	"net/url"
	"strings"
)

func CanonicalWatchURL(raw string) (string, error) {
	u, e := url.Parse(raw)
	if e != nil || u.Host == "" || u.User != nil || !d.Has([]string{"http", "https"}, u.Scheme) {
		return "", d.Fail("WATCH_URL_INVALID", 422)
	}
	u.Fragment = ""
	u.Host = strings.ToLower(u.Host)
	if u.Path == "" {
		u.Path = "/"
	}
	if u.Scheme == "https" && u.Port() == "443" || u.Scheme == "http" && u.Port() == "80" {
		u.Host = u.Hostname()
	}
	q := u.Query()
	for key := range q {
		if strings.HasPrefix(strings.ToLower(key), "utm_") || d.Has([]string{"fbclid", "gclid"}, strings.ToLower(key)) {
			q.Del(key)
		}
	}
	u.RawQuery = q.Encode()
	return u.String(), nil
}
