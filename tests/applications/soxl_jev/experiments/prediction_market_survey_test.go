package experiments_test

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"

	d "github.com/AceNanako0721/Factorforge/src/factorforge/applications/soxl_jev/domain"
)

// Read-only prediction-market feasibility survey frozen by
// doc/engineering/P3_PREDICTION_MARKET_SURVEY.md. Prediction markets are
// treated as expectation data only; nothing is traded or authenticated.

var pmCompanyNames = map[string][]string{
	"NVDA": {"Nvidia"}, "AVGO": {"Broadcom"}, "AMD": {"AMD", "Advanced Micro Devices"}, "MU": {"Micron"}, "INTC": {"Intel"},
	"QCOM": {"Qualcomm"}, "TXN": {"Texas Instruments"}, "AMAT": {"Applied Materials"}, "LRCX": {"Lam Research"}, "KLAC": {"KLA"},
	"MRVL": {"Marvell"}, "ALAB": {"Astera Labs"}, "CRDO": {"Credo"}, "TER": {"Teradyne"},
}

var (
	pmEarnings = regexp.MustCompile(`(?i)\b(earnings|eps|revenue|guidance|beat|quarter|q[1-4]|data center)\b`)
	pmMention  = regexp.MustCompile(`(?i)\b(say|says|said|mention|mentions)\b`)
	pmMacroLo  = time.Date(2026, 4, 1, 0, 0, 0, 0, time.UTC)
	pmMacroHi  = time.Date(2026, 10, 11, 23, 59, 0, 0, time.UTC)
)

func pmNameMatch(ticker, text string) bool {
	for _, name := range pmCompanyNames[ticker] {
		if regexp.MustCompile(`(?i)\b` + regexp.QuoteMeta(name) + `\b`).MatchString(text) {
			return true
		}
	}
	return false
}

// pmClassify applies the frozen matching rule to one market.
func pmClassify(ticker, title string, end, event time.Time) string {
	if !pmNameMatch(ticker, title) || !pmEarnings.MatchString(title) || end.Before(event.Add(-21*24*time.Hour)) || end.After(event.Add(3*24*time.Hour)) {
		return ""
	}
	if pmMention.MatchString(title) {
		return "MENTION"
	}
	return "OUTCOME"
}

// pmSurprise is the settled outcome minus the pre-event "yes" probability.
func pmSurprise(result bool, price float64) float64 {
	if result {
		return 1 - price
	}
	return -price
}

type pmEvent struct {
	ID, Ticker, Direction string
	Accepted              time.Time
	Abnormal              *float64
}

type pmMarket struct {
	Venue, ID, Series, Title, Kind string
	End                            time.Time
	Volume                         float64
	Result                         *bool
	PreEventPrice                  *float64
	PriceStatus                    string
	Token                          string
}

type pmClient struct {
	t        *testing.T
	dir      string
	client   *http.Client
	gets     int
	last     time.Time
	receipts []map[string]any
}

func (c *pmClient) get(endpoint string) ([]byte, bool) {
	c.t.Helper()
	if c.gets >= 400 {
		c.t.Fatal("PM_SURVEY_GET_BUDGET")
	}
	if wait := 300*time.Millisecond - time.Since(c.last); !c.last.IsZero() && wait > 0 {
		time.Sleep(wait)
	}
	c.last = time.Now()
	c.gets++
	u, err := url.Parse(endpoint)
	if err != nil || u.Scheme != "https" || !d.Has([]string{"gamma-api.polymarket.com", "data-api.polymarket.com", "api.elections.kalshi.com"}, u.Host) {
		c.t.Fatal("PM_SURVEY_URL", endpoint)
	}
	started := time.Now()
	res, err := c.client.Get(endpoint)
	row := map[string]any{"host": u.Host, "path": u.Path, "query": u.RawQuery, "ms": 0}
	if err != nil {
		row["error"] = "DELIVERY_UNKNOWN"
		c.receipts = append(c.receipts, row)
		return nil, false
	}
	raw, err := io.ReadAll(io.LimitReader(res.Body, 8<<20))
	res.Body.Close()
	row["http"], row["bytes"], row["ms"] = res.StatusCode, len(raw), time.Since(started).Milliseconds()
	c.receipts = append(c.receipts, row)
	return raw, err == nil && res.StatusCode == 200
}

func pmTime(v string) (time.Time, bool) {
	for _, layout := range []string{time.RFC3339Nano, "2006-01-02T15:04:05Z", "2006-01-02"} {
		if t, err := time.Parse(layout, v); err == nil {
			return t.UTC(), true
		}
	}
	return time.Time{}, false
}

func pmFloat(v any) float64 {
	switch x := v.(type) {
	case float64:
		return x
	case string:
		f, _ := strconv.ParseFloat(x, 64)
		return f
	case json.Number:
		f, _ := x.Float64()
		return f
	}
	return 0
}

// pmJSONList decodes Polymarket's JSON-encoded string arrays.
func pmJSONList(v any) []string {
	s, ok := v.(string)
	if !ok {
		return nil
	}
	var out []string
	if json.Unmarshal([]byte(s), &out) != nil {
		return nil
	}
	return out
}

// pmPolymarketMarkets flattens search events into markets with resolution.
func pmPolymarketMarkets(raw []byte) []pmMarket {
	var doc struct {
		Events []struct {
			Title   string           `json:"title"`
			Markets []map[string]any `json:"markets"`
		} `json:"events"`
	}
	if json.Unmarshal(raw, &doc) != nil {
		return nil
	}
	out := []pmMarket{}
	for _, e := range doc.Events {
		for _, m := range e.Markets {
			question, _ := m["question"].(string)
			endText, _ := m["endDate"].(string)
			end, ok := pmTime(endText)
			if !ok {
				continue
			}
			market := pmMarket{Venue: "POLYMARKET", ID: fmt.Sprint(m["id"]), Title: strings.TrimSpace(e.Title + " — " + question), End: end, Volume: pmFloat(m["volumeNum"])}
			if market.Volume == 0 {
				market.Volume = pmFloat(m["volume"])
			}
			outcomes, prices, tokens := pmJSONList(m["outcomes"]), pmJSONList(m["outcomePrices"]), pmJSONList(m["clobTokenIds"])
			for i, o := range outcomes {
				if strings.EqualFold(o, "Yes") && i < len(tokens) {
					market.Token = tokens[i]
					if closed, _ := m["closed"].(bool); closed && i < len(prices) {
						switch prices[i] {
						case "1":
							yes := true
							market.Result = &yes
						case "0":
							no := false
							market.Result = &no
						}
					}
				}
			}
			out = append(out, market)
		}
	}
	return out
}

// pmPolymarketPrice reads the price returned for an as_of request.
func pmPolymarketPrice(raw []byte) (float64, bool) {
	var doc map[string]any
	if json.Unmarshal(raw, &doc) != nil {
		return 0, false
	}
	for _, key := range []string{"price", "p"} {
		if v, ok := doc[key]; ok {
			if p := pmFloat(v); p > 0 && p < 1 {
				return p, true
			}
		}
	}
	for _, key := range []string{"history", "data", "prices"} {
		if list, ok := doc[key].([]any); ok && len(list) > 0 {
			if point, ok := list[len(list)-1].(map[string]any); ok {
				for _, k := range []string{"p", "price"} {
					if p := pmFloat(point[k]); p > 0 && p < 1 {
						return p, true
					}
				}
			}
		}
	}
	return 0, false
}

// pmKalshiClose takes the last hourly candle's close; cents are converted.
func pmKalshiClose(raw []byte) (float64, bool) {
	var doc struct {
		Candles []map[string]any `json:"candlesticks"`
	}
	if json.Unmarshal(raw, &doc) != nil || len(doc.Candles) == 0 {
		return 0, false
	}
	c := doc.Candles[len(doc.Candles)-1]
	read := func(group string) float64 {
		if g, ok := c[group].(map[string]any); ok {
			for _, k := range []string{"close_dollars", "close"} {
				if v := pmFloat(g[k]); v > 0 {
					if v > 1 {
						v /= 100
					}
					return v
				}
			}
		}
		return 0
	}
	if p := read("price"); p > 0 && p < 1 {
		return p, true
	}
	bid, ask := read("yes_bid"), read("yes_ask")
	if bid > 0 && ask > 0 && ask < 1 {
		return (bid + ask) / 2, true
	}
	return 0, false
}

func pmLoadEvents(t *testing.T, root string) []pmEvent {
	t.Helper()
	out := []pmEvent{}
	for _, lab := range []string{"sec-direct-eval-pilot-20261012", "guidance-pilot-20261012"} {
		dir := filepath.Join(root, "runtime", lab)
		var samples struct {
			Samples []secEvalSample `json:"samples"`
		}
		financialRead(t, dir, "samples.json", &samples)
		var prices struct {
			Rows []struct {
				ID       string   `json:"id"`
				Abnormal *float64 `json:"abnormal_return"`
			} `json:"rows"`
		}
		financialRead(t, dir, "prices.json", &prices)
		var evaluation struct {
			Rows []struct {
				ID      string            `json:"id"`
				Answers map[string]string `json:"answers"`
			} `json:"rows"`
		}
		financialRead(t, dir, "evaluation.json", &evaluation)
		ret, dir2 := map[string]*float64{}, map[string]string{}
		for _, r := range prices.Rows {
			ret[r.ID] = r.Abnormal
		}
		for _, r := range evaluation.Rows {
			dir2[r.ID] = r.Answers["direction"]
		}
		for _, s := range samples.Samples {
			if d.Has(s.Items, "2.02") {
				out = append(out, pmEvent{ID: lab + "/" + s.ID, Ticker: s.Ticker, Accepted: s.AcceptedAt, Abnormal: ret[s.ID], Direction: dir2[s.ID]})
			}
		}
	}
	return out
}

func TestPredictionMarketSurvey(t *testing.T) {
	lab := secEvalDir(t, "FACTORFORGE_PM_SURVEY_LAB")
	root := filepath.Clean(filepath.Join(lab, "..", ".."))
	events := pmLoadEvents(t, root)
	if os.Mkdir(lab, 0700) != nil {
		t.Fatal("PM_SURVEY_LAB_EXISTS")
	}
	frozenRateWrite(t, lab, "started.json", map[string]any{"at": time.Now().UTC(), "protocol": "doc/engineering/P3_PREDICTION_MARKET_SURVEY.md", "events": len(events), "max_gets": 400, "retries": 0, "trading": false})
	c := &pmClient{t: t, dir: lab, client: &http.Client{Timeout: 20 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}}

	// Polymarket: one search per company name, including closed events.
	poly := map[string][]pmMarket{}
	for ticker, names := range pmCompanyNames {
		for _, name := range names {
			raw, ok := c.get("https://gamma-api.polymarket.com/public-search?q=" + url.QueryEscape(name) + "&limit_per_type=50&keep_closed_markets=1&events_status=closed")
			if ok {
				poly[ticker] = append(poly[ticker], pmPolymarketMarkets(raw)...)
			}
		}
	}
	// Kalshi: company series whose title names a surveyed company.
	kalshi := map[string][]pmMarket{}
	for _, category := range []string{"Companies", "Financials"} {
		raw, ok := c.get("https://api.elections.kalshi.com/trade-api/v2/series?category=" + category)
		if !ok {
			continue
		}
		var doc struct {
			Series []struct{ Ticker, Title string } `json:"series"`
		}
		if json.Unmarshal(raw, &doc) != nil {
			continue
		}
		for _, s := range doc.Series {
			for ticker := range pmCompanyNames {
				if !pmNameMatch(ticker, s.Title) {
					continue
				}
				q := fmt.Sprintf("https://api.elections.kalshi.com/trade-api/v2/markets?series_ticker=%s&status=settled&min_close_ts=%d&max_close_ts=%d&limit=1000", url.QueryEscape(s.Ticker), pmMacroLo.Add(-30*24*time.Hour).Unix(), pmMacroHi.Unix())
				mraw, ok := c.get(q)
				if !ok {
					continue
				}
				var markets struct {
					Markets []map[string]any `json:"markets"`
				}
				if json.Unmarshal(mraw, &markets) != nil {
					continue
				}
				for _, m := range markets.Markets {
					closeText, _ := m["close_time"].(string)
					end, ok := pmTime(closeText)
					if !ok {
						continue
					}
					title := strings.TrimSpace(fmt.Sprint(m["title"]) + " " + fmt.Sprint(m["yes_sub_title"]))
					market := pmMarket{Venue: "KALSHI", ID: fmt.Sprint(m["ticker"]), Series: s.Ticker, Title: title, End: end, Volume: pmFloat(m["volume"])}
					switch m["result"] {
					case "yes":
						yes := true
						market.Result = &yes
					case "no":
						no := false
						market.Result = &no
					}
					kalshi[ticker] = append(kalshi[ticker], market)
				}
			}
		}
	}
	// Match markets to earnings events and read the pre-event price.
	rows, matchedEvents := []map[string]any{}, 0
	seen := map[string]bool{}
	for _, e := range events {
		row := map[string]any{"event": e.ID, "ticker": e.Ticker, "accepted_at": e.Accepted, "jev_direction": e.Direction, "abnormal_return": e.Abnormal}
		markets := []pmMarket{}
		for _, m := range append(append([]pmMarket{}, poly[e.Ticker]...), kalshi[e.Ticker]...) {
			key := m.Venue + "/" + m.ID
			if seen[e.ID+key] {
				continue
			}
			if kind := pmClassify(e.Ticker, m.Title, m.End, e.Accepted); kind != "" {
				seen[e.ID+key] = true
				m.Kind = kind
				at := e.Accepted.Add(-time.Hour)
				m.PriceStatus = "PRICE_UNAVAILABLE"
				if kind == "OUTCOME" {
					if m.Venue == "POLYMARKET" && m.Token != "" {
						if raw, ok := c.get(fmt.Sprintf("https://data-api.polymarket.com/v2/prices-history?token_id=%s&as_of=%d", url.QueryEscape(m.Token), at.Unix())); ok {
							if p, ok := pmPolymarketPrice(raw); ok {
								m.PreEventPrice, m.PriceStatus = &p, "OK"
							}
						}
					}
					if m.Venue == "KALSHI" {
						if raw, ok := c.get(fmt.Sprintf("https://api.elections.kalshi.com/trade-api/v2/series/%s/markets/%s/candlesticks?start_ts=%d&end_ts=%d&period_interval=60", url.PathEscape(m.Series), url.PathEscape(m.ID), at.Add(-6*time.Hour).Unix(), at.Unix())); ok {
							if p, ok := pmKalshiClose(raw); ok {
								m.PreEventPrice, m.PriceStatus = &p, "OK"
							}
						}
					}
				}
				markets = append(markets, m)
			}
		}
		if len(markets) > 0 {
			matchedEvents++
		}
		row["markets"] = markets
		rows = append(rows, row)
	}
	// Macro coverage: count settled Fed/CPI markets in the window and their volume.
	macro := map[string]any{}
	for _, series := range []string{"KXFED", "KXCPI"} {
		raw, ok := c.get(fmt.Sprintf("https://api.elections.kalshi.com/trade-api/v2/markets?series_ticker=%s&status=settled&min_close_ts=%d&max_close_ts=%d&limit=1000", series, pmMacroLo.Unix(), pmMacroHi.Unix()))
		entry := map[string]any{"http_ok": ok}
		if ok {
			var doc struct {
				Markets []map[string]any `json:"markets"`
			}
			if json.Unmarshal(raw, &doc) == nil {
				vols, eventsSeen := []float64{}, map[string]bool{}
				for _, m := range doc.Markets {
					vols = append(vols, pmFloat(m["volume"]))
					eventsSeen[fmt.Sprint(m["event_ticker"])] = true
				}
				sort.Float64s(vols)
				entry["markets"], entry["events"] = len(vols), len(eventsSeen)
				if len(vols) > 0 {
					entry["median_volume"], entry["max_volume"] = vols[len(vols)/2], vols[len(vols)-1]
				}
			}
		}
		macro["KALSHI/"+series] = entry
	}
	for _, q := range []string{"Fed decision", "CPI"} {
		raw, ok := c.get("https://gamma-api.polymarket.com/public-search?q=" + url.QueryEscape(q) + "&limit_per_type=50&keep_closed_markets=1&events_status=closed")
		count, vols := 0, []float64{}
		if ok {
			for _, m := range pmPolymarketMarkets(raw) {
				if !m.End.Before(pmMacroLo) && !m.End.After(pmMacroHi) {
					count++
					vols = append(vols, m.Volume)
				}
			}
		}
		sort.Float64s(vols)
		entry := map[string]any{"http_ok": ok, "markets_in_window": count}
		if len(vols) > 0 {
			entry["median_volume"] = vols[len(vols)/2]
		}
		macro["POLYMARKET/"+q] = entry
	}
	summary := pmSummarize(events, rows)
	summary["events_with_any_market"], summary["macro"], summary["gets"] = matchedEvents, macro, c.gets
	frozenRateWrite(t, lab, "report.json", map[string]any{"at": time.Now().UTC(), "summary": summary, "rows": rows, "receipts": c.receipts, "trading": false})
	t.Logf("prediction-market survey: %s", mustNewsJSON(summary))
}

// pmSummarize counts coverage, liquidity and surprise-sign agreement.
func pmSummarize(events []pmEvent, rows []map[string]any) map[string]any {
	outcomeEvents, mentionEvents, priced, signN, signAgree := 0, 0, 0, 0, 0
	jevN, jevAgree := 0, 0
	vols := []float64{}
	for i, row := range rows {
		markets, _ := row["markets"].([]pmMarket)
		e := events[i]
		hasOutcome, hasMention := false, false
		sum, n := 0.0, 0
		for _, m := range markets {
			if m.Kind == "MENTION" {
				hasMention = true
				continue
			}
			hasOutcome = true
			vols = append(vols, m.Volume)
			if m.PreEventPrice != nil && m.Result != nil {
				sum += pmSurprise(*m.Result, *m.PreEventPrice)
				n++
			}
		}
		if hasOutcome {
			outcomeEvents++
		}
		if hasMention {
			mentionEvents++
		}
		if n > 0 {
			priced++
			if e.Abnormal != nil && *e.Abnormal != 0 && sum != 0 {
				signN++
				if sum*(*e.Abnormal) > 0 {
					signAgree++
				}
			}
		}
		if e.Abnormal != nil && *e.Abnormal != 0 {
			if sign, ok := secDirectionSign(e.Direction); ok {
				jevN++
				if sign*(*e.Abnormal) > 0 {
					jevAgree++
				}
			}
		}
	}
	sort.Float64s(vols)
	summary := map[string]any{"earnings_events": len(events), "events_with_outcome_market": outcomeEvents, "events_with_mention_market_only_or_also": mentionEvents,
		"events_with_pre_event_price_and_result": priced, "surprise_sign": map[string]int{"n": signN, "agree": signAgree}, "jev_text_direction_sign": map[string]int{"n": jevN, "agree": jevAgree}, "accuracy_claimed": false}
	if len(vols) > 0 {
		summary["outcome_market_volume_median"], summary["outcome_market_volume_max"] = vols[len(vols)/2], vols[len(vols)-1]
	}
	return summary
}

func TestPredictionMarketSurveyOfflineBoundaries(t *testing.T) {
	event := time.Date(2026, 8, 26, 20, 20, 0, 0, time.UTC)
	cases := map[string]string{
		"Will Nvidia beat quarterly earnings estimates?":               "OUTCOME",
		"Nvidia Q2 data center revenue above $40B?":                    "OUTCOME",
		"What will Nvidia say during its earnings call? — Hyperscaler": "MENTION",
		"DOJ fines Nvidia over Groq deal in 2026?":                     "",
		"Will Intel beat earnings?":                                    "",
		"Will KLAC or KLA Corporation beat earnings?":                  "",
	}
	for title, want := range cases {
		ticker := "NVDA"
		if strings.Contains(title, "KLA") {
			ticker = "INTC"
		}
		if got := pmClassify(ticker, title, event.Add(-time.Hour), event); got != want {
			t.Fatal("classification", title, got)
		}
	}
	if pmClassify("NVDA", "Will Nvidia beat earnings?", event.Add(-22*24*time.Hour), event) != "" || pmClassify("NVDA", "Will Nvidia beat earnings?", event.Add(4*24*time.Hour), event) != "" {
		t.Fatal("time window not applied")
	}
	if !pmNameMatch("KLAC", "Will KLA beat?") || pmNameMatch("KLAC", "Will Klarna beat?") || pmNameMatch("MU", "Will MU beat?") {
		t.Fatal("word-boundary name match")
	}
	if pmSurprise(true, 0.97) <= 0 || pmSurprise(false, 0.97) >= 0 {
		t.Fatal("surprise sign")
	}
	raw := []byte(`{"events":[{"title":"Nvidia earnings","markets":[{"id":"1","question":"Will Nvidia beat?","endDate":"2026-08-26T20:00:00Z","closed":true,"outcomes":"[\"Yes\",\"No\"]","outcomePrices":"[\"1\",\"0\"]","clobTokenIds":"[\"tok-yes\",\"tok-no\"]","volumeNum":12345.6}]}]}`)
	markets := pmPolymarketMarkets(raw)
	if len(markets) != 1 || markets[0].Token != "tok-yes" || markets[0].Result == nil || !*markets[0].Result || markets[0].Volume != 12345.6 {
		t.Fatalf("polymarket parsing %+v", markets)
	}
	if p, ok := pmPolymarketPrice([]byte(`{"history":[{"t":1,"p":0.41},{"t":2,"p":0.43}]}`)); !ok || p != 0.43 {
		t.Fatal("price history", p)
	}
	if _, ok := pmPolymarketPrice([]byte(`{"history":[]}`)); ok {
		t.Fatal("empty price accepted")
	}
	if p, ok := pmKalshiClose([]byte(`{"candlesticks":[{"price":{"close":62}},{"price":{"close":64}}]}`)); !ok || p != 0.64 {
		t.Fatal("kalshi cents", p)
	}
	if p, ok := pmKalshiClose([]byte(`{"candlesticks":[{"price":{},"yes_bid":{"close_dollars":"0.40"},"yes_ask":{"close_dollars":"0.44"}}]}`)); !ok || p < 0.419 || p > 0.421 {
		t.Fatal("kalshi bid/ask", p)
	}
	up, down := 0.02, -0.03
	yes, no := true, false
	pa, pb := 0.6, 0.7
	events := []pmEvent{{ID: "a", Abnormal: &up, Direction: "POSITIVE"}, {ID: "b", Abnormal: &down, Direction: "POSITIVE"}}
	rows := []map[string]any{{"markets": []pmMarket{{Kind: "OUTCOME", Volume: 10, Result: &yes, PreEventPrice: &pa}}}, {"markets": []pmMarket{{Kind: "OUTCOME", Volume: 30, Result: &no, PreEventPrice: &pb}, {Kind: "MENTION"}}}}
	s := pmSummarize(events, rows)
	if fmt.Sprint(s["surprise_sign"]) != "map[agree:2 n:2]" || fmt.Sprint(s["jev_text_direction_sign"]) != "map[agree:1 n:2]" || s["events_with_mention_market_only_or_also"] != 1 {
		t.Fatal("summary", s)
	}
}
