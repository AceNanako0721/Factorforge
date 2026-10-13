package monitoring

import (
	"context"
	"encoding/json"
	d "github.com/AceNanako0721/Factorforge/src/factorforge/applications/soxl_jev/domain"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"
)

type DailyHTTPOptions struct {
	Kind, Endpoint, Key, Secret, Licence string
	Client                               *http.Client
	MaxBytes                             int
}
type DailyHTTP struct{ options DailyHTTPOptions }

func NewDailyHTTP(o DailyHTTPOptions) (*DailyHTTP, error) {
	u, e := url.Parse(o.Endpoint)
	if e != nil || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || u.Scheme != "https" || o.Client == nil || o.Client.Timeout <= 0 || o.MaxBytes <= 0 || !d.Has([]string{"YAHOO", "ALPACA"}, o.Kind) {
		return nil, d.Fail("BASELINE_SOURCE_CONFIGURATION_REQUIRED", 503)
	}
	if o.Kind == "YAHOO" && o.Licence != "RESEARCH_ONLY" || o.Kind == "ALPACA" && (o.Key == "" || o.Secret == "" || o.Licence != "LICENSED") {
		return nil, d.Fail("BASELINE_SOURCE_NOT_LICENSED", 403)
	}
	client := *o.Client
	client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	o.Client = &client
	return &DailyHTTP{o}, nil
}
func (s *DailyHTTP) License() string { return s.options.Licence }
func (s *DailyHTTP) Bars(ctx context.Context, symbol string, from, to time.Time) ([]d.DailyBar, error) {
	if !d.ValidID(symbol) || !d.UTC(from) || !d.UTC(to) || !from.Before(to) {
		return nil, d.Fail("BASELINE_REQUEST_INVALID", 422)
	}
	o := s.options
	endpoint := strings.TrimRight(o.Endpoint, "/")
	q := url.Values{}
	if o.Kind == "YAHOO" {
		endpoint += "/v8/finance/chart/" + symbol
		q.Set("period1", strconv.FormatInt(from.Unix(), 10))
		q.Set("period2", strconv.FormatInt(to.Add(time.Second).Unix(), 10))
		q.Set("interval", "1d")
	} else {
		endpoint += "/v2/stocks/" + symbol + "/bars"
		q.Set("start", from.Format(time.RFC3339))
		q.Set("end", to.Add(time.Second).Format(time.RFC3339))
		q.Set("timeframe", "1Day")
		q.Set("adjustment", "all")
		q.Set("limit", "10000")
	}
	req, e := http.NewRequestWithContext(ctx, "GET", endpoint+"?"+q.Encode(), nil)
	if e != nil {
		return nil, d.Fail("BASELINE_REQUEST_INVALID", 422)
	}
	if o.Kind == "ALPACA" {
		req.Header.Set("APCA-API-KEY-ID", o.Key)
		req.Header.Set("APCA-API-SECRET-KEY", o.Secret)
	}
	res, e := o.Client.Do(req)
	if e != nil {
		return nil, d.Fail("BASELINE_SOURCE_UNAVAILABLE", 503)
	}
	defer res.Body.Close()
	if res.StatusCode != 200 {
		return nil, d.Fail("BASELINE_SOURCE_UNAVAILABLE", 503)
	}
	raw, e := io.ReadAll(io.LimitReader(res.Body, int64(o.MaxBytes)+1))
	if e != nil || len(raw) > o.MaxBytes {
		return nil, d.Fail("BASELINE_RESPONSE_INVALID", 503)
	}
	return ParseDailyBars(raw, o.Kind, to)
}
func ParseDailyBars(raw []byte, kind string, completedClose time.Time) ([]d.DailyBar, error) {
	out := []d.DailyBar{}
	zone, e := time.LoadLocation("America/New_York")
	if e != nil {
		return nil, e
	}
	date := completedClose.In(zone).Format("2006-01-02")
	invalid := func() ([]d.DailyBar, error) { return nil, d.Fail("BASELINE_BAR_INCOMPLETE", 422) }
	switch kind {
	case "YAHOO":
		var doc struct {
			Chart struct {
				Result []struct {
					Timestamp  []int64 `json:"timestamp"`
					Indicators struct {
						Quote []struct {
							Open, High, Low, Close []*float64
							Volume                 []*int64
						} `json:"quote"`
						AdjClose []struct {
							Values []*float64 `json:"adjclose"`
						} `json:"adjclose"`
					} `json:"indicators"`
				} `json:"result"`
				Error json.RawMessage `json:"error"`
			} `json:"chart"`
		}
		if json.Unmarshal(raw, &doc) != nil || len(doc.Chart.Result) != 1 || len(doc.Chart.Error) > 0 && string(doc.Chart.Error) != "null" {
			return invalid()
		}
		r := doc.Chart.Result[0]
		if len(r.Indicators.Quote) != 1 || len(r.Indicators.AdjClose) != 1 {
			return invalid()
		}
		q, a := r.Indicators.Quote[0], r.Indicators.AdjClose[0].Values
		n := len(r.Timestamp)
		if len(q.Open) != n || len(q.High) != n || len(q.Low) != n || len(q.Close) != n || len(q.Volume) != n || len(a) != n {
			return invalid()
		}
		for i, stamp := range r.Timestamp {
			ds := time.Unix(stamp, 0).In(zone).Format("2006-01-02")
			if ds > date {
				continue
			}
			if q.Open[i] == nil || q.High[i] == nil || q.Low[i] == nil || q.Close[i] == nil || q.Volume[i] == nil || a[i] == nil {
				return invalid()
			}
			out = append(out, d.DailyBar{Date: ds, Open: *q.Open[i], High: *q.High[i], Low: *q.Low[i], Close: *q.Close[i], AdjClose: *a[i], Volume: *q.Volume[i], Final: !time.Unix(stamp, 0).After(completedClose)})
		}
	case "ALPACA":
		var doc struct {
			Bars []struct {
				At     time.Time `json:"t"`
				Open   float64   `json:"o"`
				High   float64   `json:"h"`
				Low    float64   `json:"l"`
				Close  float64   `json:"c"`
				Volume int64     `json:"v"`
			} `json:"bars"`
			Next *string `json:"next_page_token"`
		}
		if json.Unmarshal(raw, &doc) != nil || doc.Next != nil && *doc.Next != "" {
			return invalid()
		}
		for _, b := range doc.Bars {
			ds := b.At.In(zone).Format("2006-01-02")
			if ds > date {
				continue
			}
			out = append(out, d.DailyBar{Date: ds, Open: b.Open, High: b.High, Low: b.Low, Close: b.Close, AdjClose: b.Close, Volume: b.Volume, Final: !b.At.After(completedClose)})
		}
	default:
		return invalid()
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Date < out[j].Date })
	return out, nil
}
