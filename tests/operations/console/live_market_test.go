package console_test

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/AceNanako0721/Factorforge/src/factorforge/operations/console/adapters"
	"github.com/AceNanako0721/Factorforge/src/factorforge/operations/console/api"
	app "github.com/AceNanako0721/Factorforge/src/factorforge/operations/console/application"
	"github.com/AceNanako0721/Factorforge/src/factorforge/operations/console/config"
	d "github.com/AceNanako0721/Factorforge/src/factorforge/operations/console/domain"
	"golang.org/x/net/websocket"
)

// Frames as the venue sent them on 2026-10-17 (the "P" and "L" keys sit
// next to "p" and "l").
const (
	markFrame  = `{"stream":"soxlusdt@markPrice@1s","data":{"e":"markPriceUpdate","E":1792166165061,"s":"SOXLUSDT","p":"99.90925992","ap":"99.90925992","P":"100.65820564","i":"99.83090582","r":"0.00034033","T":1792166400000,"st":1}}`
	klineFrame = `{"stream":"soxlusdt@kline_5m","data":{"e":"kline","E":1792166165187,"s":"SOXLUSDT","k":{"t":1792166100000,"T":1792166399999,"s":"SOXLUSDT","i":"5m","f":311927582,"L":311930042,"o":"99.80000","c":"99.93000","h":"99.98000","l":"99.73000","v":"34215.74","n":2461,"x":false,"q":"3416828.2207","V":"1","Q":"1","B":"0"}}}`
)

func TestLiveParseKeepsExactKeys(t *testing.T) {
	m, ok := adapters.Parse([]byte(markFrame), "SOXLUSDT")
	if !ok || m.Price != "99.90925992" || m.Kline != nil || !m.At.Equal(time.UnixMilli(1792166165061)) {
		t.Fatal("mark", m, ok)
	}
	k, ok := adapters.Parse([]byte(klineFrame), "SOXLUSDT")
	if !ok || k.Kline == nil || k.Kline.Low != "99.73000" || k.Kline.Close != "99.93000" || k.Kline.Final || !k.Kline.CloseAt.Equal(time.UnixMilli(1792166400000)) || !k.Kline.OpenAt.Equal(time.UnixMilli(1792166100000)) {
		t.Fatal("kline", k.Kline, ok)
	}
	for _, bad := range []string{
		strings.Replace(markFrame, `"s":"SOXLUSDT"`, `"s":"BTCUSDT"`, 1),
		strings.Replace(markFrame, `"p":"99.90925992"`, `"p":"1e2"`, 1),
		strings.Replace(markFrame, `"p":"99.90925992"`, `"p":"0.000"`, 1),
		strings.Replace(markFrame, `"p":"99.90925992",`, ``, 1),
		strings.Replace(markFrame, `markPriceUpdate`, `bookTicker`, 1),
		strings.Replace(klineFrame, `"T":1792166399999`, `"T":1`, 1),
		`{"result":null,"id":1}`,
		`not json`,
	} {
		if _, ok := adapters.Parse([]byte(bad), "SOXLUSDT"); ok {
			t.Fatal("accepted", bad)
		}
	}
}

// fakeSource is a controllable upstream for the hub.
type fakeSource struct {
	mu    sync.Mutex
	runs  atomic.Int64
	live  atomic.Int64
	emit  func(adapters.MarketEvent)
	fail  chan struct{}
	ready chan struct{}
}

func newFakeSource() *fakeSource {
	return &fakeSource{fail: make(chan struct{}, 1), ready: make(chan struct{}, 8)}
}
func (f *fakeSource) Run(ctx context.Context, instrument, interval string, _ time.Duration, emit func(adapters.MarketEvent)) (int, error) {
	f.runs.Add(1)
	f.live.Add(1)
	defer f.live.Add(-1)
	f.mu.Lock()
	f.emit = emit
	f.mu.Unlock()
	f.ready <- struct{}{}
	select {
	case <-ctx.Done():
		return 0, ctx.Err()
	case <-f.fail:
		return 0, errors.New("upstream closed")
	}
}
func (f *fakeSource) send(e adapters.MarketEvent) {
	f.mu.Lock()
	emit := f.emit
	f.mu.Unlock()
	emit(e)
}
func waitFor(t *testing.T, what string, ok func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for !ok() {
		if time.Now().After(deadline) {
			t.Fatal("timeout waiting for", what)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func TestLiveHubSharesSnapshotsStaleReconnectAndIdle(t *testing.T) {
	src := newFakeSource()
	hub := &app.LiveHub{Source: src, Interval: "5m", Stale: 80 * time.Millisecond, Idle: 50 * time.Millisecond, MaxStreams: 2, MaxAge: time.Hour, Backoff: func(int) time.Duration { return 10 * time.Millisecond }}
	a, e := hub.Subscribe("SOXLUSDT")
	if e != nil {
		t.Fatal(e)
	}
	b, e := hub.Subscribe("SOXLUSDT")
	if e != nil {
		t.Fatal(e)
	}
	if _, e := hub.Subscribe("SOXLUSDT"); e == nil || e.(*d.Error).Code != "LIVE_STREAM_BUDGET" {
		t.Fatal("stream budget", e)
	}
	<-src.ready
	if src.runs.Load() != 1 || a.Snapshot().State != "CONNECTING" {
		t.Fatal("one shared upstream", src.runs.Load(), a.Snapshot())
	}
	src.send(adapters.MarketEvent{At: time.Now(), Price: "101.5"})
	src.send(adapters.MarketEvent{At: time.Now(), Kline: &adapters.LiveKline{Open: "1", High: "2", Low: "1", Close: "2"}})
	for _, s := range []*app.LiveSubscription{a, b} {
		snap := s.Snapshot()
		if snap.State != "LIVE" || snap.Mark.Price != "101.5" || snap.Kline.Close != "2" || snap.MarkSeq != 1 || snap.KlineSeq != 1 {
			t.Fatal("snapshot", snap)
		}
	}
	// A reader that never drains still holds a single pending notice.
	if len(a.C) != 1 || cap(a.C) != 1 {
		t.Fatal("latest-value slot", len(a.C))
	}
	waitFor(t, "stale", func() bool { return a.Snapshot().State == "STALE" })
	src.fail <- struct{}{}
	waitFor(t, "unavailable", func() bool {
		s := a.Snapshot()
		return s.State == "UNAVAILABLE" && s.Code == "LIVE_UPSTREAM_UNAVAILABLE" || src.runs.Load() == 2
	})
	<-src.ready
	if src.runs.Load() != 2 || a.Snapshot().Mark.Price != "101.5" {
		t.Fatal("reconnect keeps the last value", src.runs.Load())
	}
	src.send(adapters.MarketEvent{At: time.Now(), Price: "102"})
	if s := b.Snapshot(); s.State != "LIVE" || s.Mark.Price != "102" || s.MarkSeq != 2 {
		t.Fatal("after reconnect", s)
	}
	a.Close()
	a.Close()
	c, e := hub.Subscribe("SOXLUSDT")
	if e != nil {
		t.Fatal("closing twice must free one slot only once", e)
	}
	c.Close()
	b.Close()
	waitFor(t, "idle close", func() bool { return src.live.Load() == 0 })
	if d, e := hub.Subscribe("SOXLUSDT"); e != nil || d.Snapshot().State != "CONNECTING" {
		t.Fatal("fresh feed after idle", e)
	} else {
		d.Close()
	}
}

// MarketStream against a local TLS push server built from the same library.
func TestMarketStreamSubscribesAndCountsRejected(t *testing.T) {
	var path, query string
	server := httptest.NewTLSServer(websocket.Handler(func(c *websocket.Conn) {
		path, query = c.Request().URL.Path, c.Request().URL.RawQuery
		for _, f := range []string{markFrame, `{"bad":1}`, klineFrame} {
			websocket.Message.Send(c, f)
		}
	}))
	defer server.Close()
	tlsConfig := server.Client().Transport.(*http.Transport).TLSClientConfig
	stream := adapters.MarketStream{URL: "wss" + strings.TrimPrefix(server.URL, "https"), Origin: "https://console.example.invalid", TLS: tlsConfig, ReadTimeout: 5 * time.Second}
	events := []adapters.MarketEvent{}
	dropped, e := stream.Run(context.Background(), "SOXLUSDT", "5m", time.Hour, func(m adapters.MarketEvent) { events = append(events, m) })
	if e == nil || dropped != 1 || len(events) != 2 || events[0].Price != "99.90925992" || events[1].Kline == nil {
		t.Fatal("stream", dropped, e, events)
	}
	if path != "/market/stream" || query != "streams=soxlusdt@markPrice@1s/soxlusdt@kline_5m" {
		t.Fatal("subscription", path, query)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, e := stream.Run(ctx, "SOXLUSDT", "5m", time.Hour, func(adapters.MarketEvent) {}); e == nil {
		t.Fatal("cancelled run")
	}
}

func TestLiveMarketConfiguration(t *testing.T) {
	ok := config.LiveMarket{Enabled: true, URL: "wss://fstream.example.invalid", Interval: "5m", StaleSeconds: 5, HeartbeatSeconds: 15, IdleSeconds: 60, MaxStreams: 16}
	if !ok.Valid() || !(config.LiveMarket{}).Valid() {
		t.Fatal("valid blocks rejected")
	}
	for _, mutate := range []func(*config.LiveMarket){
		func(l *config.LiveMarket) { l.URL = "ws://fstream.example.invalid" },
		func(l *config.LiveMarket) { l.URL = "wss://fstream.example.invalid/market" },
		func(l *config.LiveMarket) { l.URL = "wss://fstream.example.invalid?x=1" },
		func(l *config.LiveMarket) { l.Interval = "7m" },
		func(l *config.LiveMarket) { l.StaleSeconds = 0 },
		func(l *config.LiveMarket) { l.MaxStreams = 0 },
	} {
		bad := ok
		mutate(&bad)
		if bad.Valid() {
			t.Fatal("accepted", bad)
		}
	}
}

type sseEvent struct{ Name, Data string }

func readEvents(t *testing.T, body io.Reader, out chan<- sseEvent) {
	scanner := bufio.NewScanner(body)
	var ev sseEvent
	for scanner.Scan() {
		line := scanner.Text()
		switch {
		case strings.HasPrefix(line, "event: "):
			ev.Name = strings.TrimPrefix(line, "event: ")
		case strings.HasPrefix(line, "data: "):
			ev.Data = strings.TrimPrefix(line, "data: ")
		case line == "":
			out <- ev
			ev = sseEvent{}
		}
	}
	close(out)
}
func next(t *testing.T, events <-chan sseEvent, name string) map[string]any {
	t.Helper()
	timeout := time.After(5 * time.Second)
	for {
		select {
		case ev, ok := <-events:
			if !ok {
				t.Fatal("stream closed before", name)
			}
			if ev.Name == name {
				var v map[string]any
				if json.Unmarshal([]byte(ev.Data), &v) != nil {
					t.Fatal("event data", ev.Data)
				}
				return v
			}
		case <-timeout:
			t.Fatal("timeout waiting for event", name)
		}
	}
}

// T4-17 / S4-019: the reference push reaches only an authorised session,
// outlives the server's read and write timeouts, and ends with the session.
func TestT417LivePushAuthorisationAndSessionEnd(t *testing.T) {
	w := world(t)
	src := newFakeSource()
	key := w.Selection.InstrumentKey
	hub := &app.LiveHub{Venue: key.Venue, Product: key.Product, Source: src, Interval: "5m", Stale: time.Hour, Idle: time.Millisecond, MaxStreams: 1, MaxAge: time.Hour}
	var current atomic.Value
	server := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { current.Load().(http.Handler).ServeHTTP(w, r) }))
	server.Config.ReadTimeout, server.Config.WriteTimeout = 300*time.Millisecond, 300*time.Millisecond
	server.StartTLS()
	defer server.Close()
	handler, e := api.New(api.Options{Sessions: w.Sessions, Registry: w.Registry, Query: query(w), Origin: server.URL, RefreshSeconds: 30, Live: hub, LiveHeartbeat: 100 * time.Millisecond})
	if e != nil {
		t.Fatal(e)
	}
	if _, e := api.New(api.Options{Sessions: w.Sessions, Registry: w.Registry, Query: query(w), Origin: server.URL, RefreshSeconds: 30, Live: hub}); e == nil {
		t.Fatal("live hub without heartbeat")
	}
	current.Store(handler)
	client := server.Client()
	client.Jar, _ = cookiejar.New(nil)
	get := func(path string) *http.Response {
		t.Helper()
		resp, e := client.Get(server.URL + api.Prefix + path)
		if e != nil {
			t.Fatal(e)
		}
		return resp
	}
	if resp := get("/live?selection_id=" + w.Selection.ID); resp.StatusCode != 401 {
		t.Fatal("anonymous live", resp.StatusCode)
	}
	login := func() {
		t.Helper()
		resp := get("/session/challenge")
		var ch d.Challenge
		json.NewDecoder(resp.Body).Decode(&ch)
		resp.Body.Close()
		body, _ := json.Marshal(map[string]string{"username": "fixture-user", "password": "fixture-pass", "csrf": ch.CSRF})
		r, _ := http.NewRequest("POST", server.URL+api.Prefix+"/session", bytes.NewReader(body))
		r.Header.Set("Origin", server.URL)
		r.Header.Set("Content-Type", "application/json")
		resp, e := client.Do(r)
		if e != nil || resp.StatusCode != 200 {
			t.Fatal("login", e)
		}
		resp.Body.Close()
	}
	login()
	for path, code := range map[string]int{"/live": 422, "/live?selection_id=other": 403, "/live?selection_id=" + w.Selection.ID + "&x=1": 422} {
		if resp := get(path); resp.StatusCode != code {
			t.Fatal(path, resp.StatusCode)
		}
	}
	// Without a hub the endpoint is not applicable.
	plain, e := api.New(api.Options{Sessions: w.Sessions, Registry: w.Registry, Query: query(w), Origin: server.URL, RefreshSeconds: 30})
	if e != nil {
		t.Fatal(e)
	}
	other, e := api.New(api.Options{Sessions: w.Sessions, Registry: w.Registry, Query: query(w), Origin: server.URL, RefreshSeconds: 30, Live: &app.LiveHub{Venue: "BINANCE", Product: key.Product, Source: src, MaxStreams: 1}, LiveHeartbeat: time.Second})
	if e != nil {
		t.Fatal(e)
	}
	for _, h := range []http.Handler{plain, other} {
		current.Store(h)
		if resp := get("/live?selection_id=" + w.Selection.ID); resp.StatusCode != 404 {
			t.Fatal("not applicable", resp.StatusCode)
		}
	}
	current.Store(handler)
	resp := get("/live?selection_id=" + w.Selection.ID)
	defer resp.Body.Close()
	if resp.StatusCode != 200 || resp.Header.Get("Content-Type") != "text/event-stream" || resp.Header.Get("Cache-Control") != "no-store" {
		t.Fatal("stream", resp.StatusCode, resp.Header)
	}
	events := make(chan sseEvent, 64)
	go readEvents(t, resp.Body, events)
	if v := next(t, events, "state"); v["state"] != "CONNECTING" || v["at"] != nil {
		t.Fatal("first state", v)
	}
	if over := get("/live?selection_id=" + w.Selection.ID); over.StatusCode != 429 {
		t.Fatal("stream budget", over.StatusCode)
	}
	<-src.ready
	// Past the server read/write timeouts the stream must still deliver.
	time.Sleep(400 * time.Millisecond)
	src.send(adapters.MarketEvent{At: time.UnixMilli(1792166165061), Price: "99.9"})
	if v := next(t, events, "mark"); v["price"] != "99.9" || v["at"] != "2026-10-16T15:56:05.061Z" {
		t.Fatal("mark", v)
	}
	open := time.UnixMilli(1792166100000)
	src.send(adapters.MarketEvent{At: open, Kline: &adapters.LiveKline{OpenAt: open, CloseAt: open.Add(5 * time.Minute), Open: "99.8", High: "99.98", Low: "99.73", Close: "99.93"}})
	if v := next(t, events, "kline"); v["close"] != "99.93" || v["final"] != false || v["close_at"] != "2026-10-16T16:00:00Z" {
		t.Fatal("kline", v)
	}
	if v := next(t, events, "state"); v["state"] != "LIVE" {
		t.Fatal("heartbeat state", v)
	}
	w.Sessions.Revoke(w.User.ID)
	if v := next(t, events, "end"); v["code"] != "SESSION_ENDED" {
		t.Fatal("end", v)
	}
	waitFor(t, "stream closed", func() bool {
		select {
		case _, ok := <-events:
			return !ok
		default:
			return false
		}
	})
	resp.Body.Close()
	waitFor(t, "upstream released", func() bool { return src.live.Load() == 0 })

}
