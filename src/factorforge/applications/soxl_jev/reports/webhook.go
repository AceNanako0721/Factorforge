package reports

import (
	"bytes"
	"context"
	"encoding/json"
	d "github.com/AceNanako0721/Factorforge/src/factorforge/applications/soxl_jev/domain"
	"io"
	"net/http"
	"net/url"
)

type Webhook struct {
	Endpoint string
	Client   *http.Client
	MaxBytes int
}

func (w Webhook) Deliver(ctx context.Context, id string, n d.WatchNotification) error {
	if w.Endpoint == "" {
		return nil
	}
	u, e := url.Parse(w.Endpoint)
	if e != nil || u.Scheme != "https" || u.Host == "" || u.User != nil || u.Fragment != "" || w.Client == nil || w.Client.Timeout <= 0 || w.MaxBytes <= 0 || !n.Valid() {
		return d.Fail("WATCH_WEBHOOK_CONFIGURATION_REQUIRED", 503)
	}
	raw, e := json.Marshal(n)
	if e != nil || len(raw) > w.MaxBytes {
		return d.Fail("WATCH_WEBHOOK_BUDGET_EXCEEDED", 422)
	}
	req, e := http.NewRequestWithContext(ctx, "POST", w.Endpoint, bytes.NewReader(raw))
	if e != nil {
		return d.Fail("WATCH_WEBHOOK_INVALID", 422)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Idempotency-Key", id)
	client := *w.Client
	client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	res, e := client.Do(req)
	if e != nil {
		return d.Fail("WATCH_WEBHOOK_DELIVERY_UNKNOWN", 503)
	}
	defer res.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(res.Body, int64(w.MaxBytes)))
	if res.StatusCode < 200 || res.StatusCode >= 300 {
		return d.Fail("WATCH_WEBHOOK_REJECTED", 503)
	}
	return nil
}
