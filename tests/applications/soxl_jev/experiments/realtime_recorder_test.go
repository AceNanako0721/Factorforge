package experiments_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	d "github.com/AceNanako0721/Factorforge/src/factorforge/applications/soxl_jev/domain"
	"golang.org/x/net/websocket"
)

// Read-only Polymarket market-channel recorder frozen by
// doc/engineering/P3_POLYMARKET_REALTIME_PROTOCOL.md. No credentials, no orders.

const (
	rtMarketURL     = "wss://ws-subscriptions-clob.polymarket.com/ws/market"
	rtMaxPerConn    = 100
	rtMaxTotalBytes = 2 << 30
)

type rtCategory struct {
	Name    string
	Queries []string
	Limit   int
	Semis   bool
}

var rtCategories = []rtCategory{
	{"broad", []string{"S&P 500", "SPX", "Nasdaq"}, 60, false},
	{"macro", []string{"Fed decision", "CPI", "rate cut"}, 60, false},
	{"semis", nil, 80, true},
}

type rtSelected struct {
	Category, Token, Title string
	End                    time.Time
	Volume                 float64
}

// rtSelect keeps open markets ending within the horizon, highest volume first.
func rtSelect(category rtCategory, markets []pmMarket, now time.Time) []rtSelected {
	out, seen := []rtSelected{}, map[string]bool{}
	for _, m := range markets {
		if m.Token == "" || m.Result != nil || !m.End.After(now) || m.End.After(now.Add(30*24*time.Hour)) || seen[m.Token] {
			continue
		}
		if category.Semis {
			named := false
			for ticker := range pmCompanyNames {
				named = named || pmNameMatch(ticker, m.Title)
			}
			if !named || !pmEarnings.MatchString(m.Title) {
				continue
			}
		}
		seen[m.Token] = true
		out = append(out, rtSelected{category.Name, m.Token, m.Title, m.End, m.Volume})
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Volume > out[j].Volume })
	if len(out) > category.Limit {
		out = out[:category.Limit]
	}
	return out
}

type rtWriter struct {
	mu      sync.Mutex
	dir     string
	hour    string
	file    *os.File
	written int64
	limit   int64
	full    bool
}

func (w *rtWriter) write(record any, at time.Time) bool {
	raw, err := json.Marshal(record)
	if err != nil {
		return false
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.full || w.written+int64(len(raw))+1 > w.limit {
		w.full = true
		return false
	}
	hour := at.UTC().Format("20060102T15")
	if w.file == nil || hour != w.hour {
		if w.file != nil {
			w.file.Close()
		}
		f, err := os.OpenFile(filepath.Join(w.dir, "messages-"+hour+".jsonl"), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0600)
		if err != nil {
			return false
		}
		w.file, w.hour = f, hour
	}
	n, err := w.file.Write(append(raw, '\n'))
	w.written += int64(n)
	return err == nil
}

func (w *rtWriter) close() {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.file != nil {
		w.file.Close()
	}
}

type rtLog struct {
	mu   sync.Mutex
	rows []map[string]any
}

func (l *rtLog) add(row map[string]any) {
	l.mu.Lock()
	l.rows = append(l.rows, row)
	l.mu.Unlock()
}

// rtRun keeps one subscription alive until ctx ends, reconnecting after 5 s
// (or the given pause) and recording every connect, disconnect and gap.
func rtRun(ctx context.Context, conn int, endpoint string, tokens []string, w *rtWriter, log *rtLog, pause time.Duration, giveUp time.Duration) {
	firstFailure := time.Time{}
	for ctx.Err() == nil {
		u, _ := url.Parse(endpoint)
		origin := "https://polymarket.com"
		ws, err := websocket.Dial(u.String(), "", origin)
		if err != nil {
			if firstFailure.IsZero() {
				firstFailure = time.Now()
			}
			log.add(map[string]any{"conn": conn, "event": "dial_failed", "at": time.Now().UTC()})
			if time.Since(firstFailure) > giveUp {
				log.add(map[string]any{"conn": conn, "event": "gave_up", "at": time.Now().UTC()})
				return
			}
			select {
			case <-ctx.Done():
				return
			case <-time.After(pause):
			}
			continue
		}
		firstFailure = time.Time{}
		log.add(map[string]any{"conn": conn, "event": "connected", "at": time.Now().UTC()})
		sub, _ := json.Marshal(map[string]any{"type": "market", "assets_ids": tokens, "custom_feature_enabled": true})
		if websocket.Message.Send(ws, string(sub)) != nil {
			ws.Close()
			continue
		}
		done := make(chan struct{})
		go func() {
			ticker := time.NewTicker(10 * time.Second)
			defer ticker.Stop()
			for {
				select {
				case <-ctx.Done():
					ws.Close()
					return
				case <-done:
					return
				case <-ticker.C:
					if websocket.Message.Send(ws, "PING") != nil {
						ws.Close()
						return
					}
				}
			}
		}()
		for {
			var msg string
			if err := websocket.Message.Receive(ws, &msg); err != nil {
				break
			}
			at := time.Now().UTC()
			if msg == "PONG" {
				continue
			}
			if !w.write(map[string]any{"rx": at.UnixNano(), "conn": conn, "msg": json.RawMessage(rtRawJSON(msg))}, at) && w.full {
				log.add(map[string]any{"conn": conn, "event": "storage_budget_reached", "at": at})
				close(done)
				ws.Close()
				return
			}
		}
		close(done)
		ws.Close()
		log.add(map[string]any{"conn": conn, "event": "disconnected", "at": time.Now().UTC()})
		select {
		case <-ctx.Done():
			return
		case <-time.After(pause):
		}
	}
}

// rtRawJSON preserves valid JSON messages verbatim and quotes anything else.
func rtRawJSON(msg string) []byte {
	if json.Valid([]byte(msg)) {
		return []byte(msg)
	}
	raw, _ := json.Marshal(msg)
	return raw
}

func TestPolymarketRealtimeRecord(t *testing.T) {
	lab := secEvalDir(t, "FACTORFORGE_PM_REALTIME_LAB")
	deadline := time.Date(2026, 10, 15, 21, 0, 0, 0, time.UTC)
	if v := os.Getenv("FACTORFORGE_PM_REALTIME_UNTIL"); v != "" {
		var err error
		if deadline, err = time.Parse(time.RFC3339, v); err != nil {
			t.Fatal("PM_REALTIME_UNTIL")
		}
	}
	if os.Mkdir(lab, 0700) != nil {
		t.Fatal("PM_REALTIME_LAB_EXISTS")
	}
	now := time.Now().UTC()
	c := &pmClient{t: t, dir: lab, client: &http.Client{Timeout: 20 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}}
	selected, tokens := []rtSelected{}, map[string]bool{}
	for _, category := range rtCategories {
		queries := category.Queries
		if category.Semis {
			for _, names := range pmCompanyNames {
				queries = append(queries, names...)
			}
			sort.Strings(queries)
		}
		markets := []pmMarket{}
		for _, q := range queries {
			if raw, ok := c.get("https://gamma-api.polymarket.com/public-search?q=" + url.QueryEscape(q) + "&limit_per_type=50&events_status=active"); ok {
				markets = append(markets, pmPolymarketMarkets(raw)...)
			}
		}
		for _, s := range rtSelect(category, markets, now) {
			if !tokens[s.Token] {
				tokens[s.Token] = true
				selected = append(selected, s)
			}
		}
	}
	if len(selected) == 0 {
		t.Fatal("PM_REALTIME_NOTHING_SELECTED")
	}
	frozenRateWrite(t, lab, "selection.json", map[string]any{"at": now, "until": deadline, "protocol": "doc/engineering/P3_POLYMARKET_REALTIME_PROTOCOL.md", "selected": selected, "catalog_gets": c.gets})
	selRaw, _ := os.ReadFile(filepath.Join(lab, "selection.json"))
	t.Logf("selected=%d selection_sha256=%s", len(selected), d.ContentDigest(selRaw))
	ctx, cancel := context.WithDeadline(context.Background(), deadline)
	defer cancel()
	w, log := &rtWriter{dir: lab, limit: rtMaxTotalBytes}, &rtLog{}
	var wg sync.WaitGroup
	for i := 0; i*rtMaxPerConn < len(selected); i++ {
		chunk := []string{}
		for _, s := range selected[i*rtMaxPerConn : min((i+1)*rtMaxPerConn, len(selected))] {
			chunk = append(chunk, s.Token)
		}
		wg.Add(1)
		go func(conn int, chunk []string) {
			defer wg.Done()
			rtRun(ctx, conn, rtMarketURL, chunk, w, log, 5*time.Second, 30*time.Minute)
		}(i, chunk)
	}
	wg.Wait()
	w.close()
	frozenRateWrite(t, lab, "connections.json", map[string]any{"finished_at": time.Now().UTC(), "bytes": w.written, "storage_budget_reached": w.full, "events": log.rows})
}

func TestPolymarketRealtimeRecorderOffline(t *testing.T) {
	now := time.Date(2026, 10, 12, 4, 0, 0, 0, time.UTC)
	settled := true
	markets := []pmMarket{
		{Token: "a", Title: "Will Nvidia Q3 data center revenue be above $50B?", End: now.Add(40 * 24 * time.Hour), Volume: 9},
		{Token: "b", Title: "Will Nvidia Q3 data center revenue be above $45B?", End: now.Add(20 * 24 * time.Hour), Volume: 5},
		{Token: "c", Title: "Will Micron beat quarterly earnings?", End: now.Add(10 * 24 * time.Hour), Volume: 7},
		{Token: "d", Title: "Nvidia to acquire a startup?", End: now.Add(10 * 24 * time.Hour), Volume: 99},
		{Token: "e", Title: "Will Intel beat earnings?", End: now.Add(-time.Hour), Volume: 50},
		{Token: "f", Title: "Will AMD beat earnings?", End: now.Add(5 * 24 * time.Hour), Volume: 50, Result: &settled},
		{Token: "", Title: "Will KLA beat earnings?", End: now.Add(5 * 24 * time.Hour), Volume: 50},
	}
	got := rtSelect(rtCategory{"semis", nil, 1, true}, markets, now)
	if len(got) != 1 || got[0].Token != "c" {
		t.Fatalf("semis selection %+v", got)
	}
	if got = rtSelect(rtCategory{"semis", nil, 5, true}, markets, now); len(got) != 2 || got[1].Token != "b" {
		t.Fatalf("horizon, settled, unnamed or tokenless markets admitted %+v", got)
	}
	// A local server sends two messages and closes; the recorder reconnects.
	var mu sync.Mutex
	connects, subs := 0, []string{}
	server := httptest.NewServer(websocket.Handler(func(ws *websocket.Conn) {
		var sub string
		websocket.Message.Receive(ws, &sub)
		mu.Lock()
		connects++
		subs = append(subs, sub)
		mu.Unlock()
		websocket.Message.Send(ws, `[{"event_type":"book","asset_id":"c","timestamp":"1"}]`)
		websocket.Message.Send(ws, `not json`)
		ws.Close()
	}))
	defer server.Close()
	dir := t.TempDir()
	w, log := &rtWriter{dir: dir, limit: 1 << 20}, &rtLog{}
	ctx, cancel := context.WithTimeout(context.Background(), 600*time.Millisecond)
	defer cancel()
	rtRun(ctx, 0, "ws"+strings.TrimPrefix(server.URL, "http"), []string{"c"}, w, log, 100*time.Millisecond, time.Minute)
	w.close()
	mu.Lock()
	defer mu.Unlock()
	if connects < 2 || !strings.Contains(subs[0], `"assets_ids":["c"]`) || !strings.Contains(subs[0], `"type":"market"`) {
		t.Fatal("reconnect or subscription", connects, subs)
	}
	files, _ := filepath.Glob(filepath.Join(dir, "messages-*.jsonl"))
	if len(files) != 1 {
		t.Fatal("hourly file", files)
	}
	raw, _ := os.ReadFile(files[0])
	lines := strings.Split(strings.TrimSpace(string(raw)), "\n")
	if len(lines) < 4 || !strings.Contains(lines[0], `"event_type":"book"`) || !strings.Contains(lines[1], `"msg":"not json"`) {
		t.Fatal("messages not recorded verbatim", lines)
	}
	events := map[string]int{}
	for _, r := range log.rows {
		events[fmt.Sprint(r["event"])]++
	}
	if events["connected"] < 2 || events["disconnected"] < 1 {
		t.Fatal("connection log", events)
	}
	small := &rtWriter{dir: t.TempDir(), limit: 10}
	if small.write(map[string]any{"x": "long enough to exceed"}, now) || !small.full {
		t.Fatal("storage budget not enforced")
	}
}
