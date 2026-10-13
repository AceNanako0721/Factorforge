package monitoring

import (
	"context"
	"encoding/json"
	d "github.com/AceNanako0721/Factorforge/src/factorforge/applications/soxl_jev/domain"
	p "github.com/AceNanako0721/Factorforge/src/factorforge/applications/soxl_jev/ports"
	"io"
	"math"
	"net/http"
	"net/url"
	"strings"
	"time"
)

type WatchMoves struct {
	Daily    p.DailyBarSource
	Quotes   p.QuoteSource
	Clock    p.Clock
	Calendar p.SessionHistory
}

func (s WatchMoves) Moves(ctx context.Context, items []d.WatchItem, at time.Time) ([]d.AbnormalMove, error) {
	if s.Daily == nil || s.Quotes == nil || s.Calendar == nil {
		return nil, d.Fail("WATCH_MOVE_CONFIGURATION_REQUIRED", 503)
	}
	out := []d.AbnormalMove{}
	zone, e := time.LoadLocation("America/New_York")
	if e != nil {
		return nil, e
	}
	date := at.In(zone).Format("2006-01-02")
	dayStart, e := time.ParseInLocation("2006-01-02", date, zone)
	if e != nil {
		return nil, e
	}
	sessions, e := s.Calendar.CompletedSessions(ctx, dayStart.UTC(), 21)
	if e != nil {
		return nil, e
	}
	from, e := time.ParseInLocation("2006-01-02", sessions[0].Start.In(zone).Format("2006-01-02"), zone)
	if e != nil {
		return nil, e
	}
	for _, item := range items {
		bars, e := s.Daily.Bars(ctx, item.Symbol, from.UTC(), at)
		if e != nil {
			return nil, e
		}
		used := []d.DailyBar{}
		for _, b := range bars {
			if b.Date < at.In(zone).Format("2006-01-02") && b.Final {
				used = append(used, b)
			}
		}
		if len(used) < 21 {
			return nil, d.Fail("WATCH_VOLATILITY_DATA_MISSING", 422)
		}
		used = used[len(used)-21:]
		for i, b := range used {
			if b.Date != sessions[i].Start.In(zone).Format("2006-01-02") {
				return nil, d.Fail("WATCH_VOLATILITY_DATA_MISSING", 422)
			}
		}
		var mean, m2 float64
		for i := 1; i < len(used); i++ {
			if used[i-1].AdjClose <= 0 || used[i].AdjClose <= 0 || !d.Finite(used[i].AdjClose) || used[i].Date <= used[i-1].Date {
				return nil, d.Fail("WATCH_MOVE_INVALID", 422)
			}
			r := used[i].AdjClose/used[i-1].AdjClose - 1
			delta := r - mean
			mean += delta / float64(i)
			m2 += delta * (r - mean)
		}
		q, e := s.Quotes.Quote(ctx, item.Symbol, at)
		if e != nil {
			return nil, e
		}
		cutoff := at
		if s.Clock != nil {
			cutoff = s.Clock.Now().UTC()
		}
		if !d.Finite(q.Price) || !d.Finite(q.PreviousClose) || q.Price <= 0 || q.PreviousClose <= 0 || !d.UTC(q.At) || q.At.After(cutoff) || q.At.In(zone).Format("2006-01-02") != at.In(zone).Format("2006-01-02") {
			return nil, d.Fail("WATCH_QUOTE_UNAVAILABLE", 422)
		}
		out = append(out, d.AbnormalMove{Symbol: item.Symbol, IntradayReturn: q.Price/q.PreviousClose - 1, DailyVol20: math.Sqrt(m2 / 19)})
	}
	return out, nil
}

// Quote metadata is regular-session market price, distinct from adjusted daily
// closes. Alpaca snapshot and Yahoo chart stay behind the quote port.
func (s *DailyHTTP) Quote(ctx context.Context, symbol string, at time.Time) (d.QuoteObservation, error) {
	o := s.options
	empty := d.QuoteObservation{}
	endpoint := strings.TrimRight(o.Endpoint, "/")
	if o.Kind == "YAHOO" {
		endpoint += "/v8/finance/chart/" + url.PathEscape(symbol) + "?interval=1m&range=1d"
	} else {
		endpoint += "/v2/stocks/" + url.PathEscape(symbol) + "/snapshot"
	}
	req, e := http.NewRequestWithContext(ctx, "GET", endpoint, nil)
	if e != nil {
		return empty, d.Fail("WATCH_QUOTE_INVALID", 422)
	}
	if o.Kind == "ALPACA" {
		req.Header.Set("APCA-API-KEY-ID", o.Key)
		req.Header.Set("APCA-API-SECRET-KEY", o.Secret)
	}
	res, e := o.Client.Do(req)
	if e != nil {
		return empty, d.Fail("WATCH_QUOTE_UNAVAILABLE", 503)
	}
	defer res.Body.Close()
	raw, e := io.ReadAll(io.LimitReader(res.Body, int64(o.MaxBytes)+1))
	if e != nil || len(raw) > o.MaxBytes || res.StatusCode != 200 {
		return empty, d.Fail("WATCH_QUOTE_UNAVAILABLE", 503)
	}
	if o.Kind == "YAHOO" {
		var doc struct {
			Chart struct {
				Result []struct {
					Meta struct {
						Price    float64 `json:"regularMarketPrice"`
						Previous float64 `json:"previousClose"`
						At       int64   `json:"regularMarketTime"`
					} `json:"meta"`
				} `json:"result"`
			} `json:"chart"`
		}
		if json.Unmarshal(raw, &doc) != nil || len(doc.Chart.Result) != 1 {
			return empty, d.Fail("WATCH_QUOTE_INVALID", 503)
		}
		m := doc.Chart.Result[0].Meta
		return d.QuoteObservation{Price: m.Price, PreviousClose: m.Previous, At: time.Unix(m.At, 0).UTC()}, nil
	}
	var doc struct {
		Latest struct {
			Price float64   `json:"p"`
			At    time.Time `json:"t"`
		} `json:"latestTrade"`
		Previous struct {
			Close float64 `json:"c"`
		} `json:"prevDailyBar"`
	}
	if json.Unmarshal(raw, &doc) != nil {
		return empty, d.Fail("WATCH_QUOTE_INVALID", 503)
	}
	return d.QuoteObservation{Price: doc.Latest.Price, PreviousClose: doc.Previous.Close, At: doc.Latest.At.UTC()}, nil
}
