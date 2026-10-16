package monitoring

import (
	"context"
	"encoding/json"
	"strconv"
	"time"

	d "github.com/AceNanako0721/Factorforge/src/factorforge/applications/soxl_jev/domain"
)

// MinuteKlines reads public 1-minute candles from the registered exchange
// source for the normal-noise table only (S3-026); they never form a signal.
type MinuteKlines struct {
	Fetcher *Fetcher
	BaseURL string // e.g. https://fapi.binance.com
	Symbol  string
}

// Range returns completed 1-minute bars whose close falls in (start, end].
func (k MinuteKlines) Range(ctx context.Context, start, end time.Time) ([]d.MinuteBar, error) {
	out := []d.MinuteBar{}
	cursor := start.Unix() * 1000
	for page := 0; page < 64 && cursor < end.Unix()*1000; page++ {
		raw, _, err := k.Fetcher.Get(ctx, k.BaseURL+"/fapi/v1/klines?symbol="+k.Symbol+"&interval=1m&startTime="+strconv.FormatInt(cursor, 10)+"&limit=1500")
		if err != nil {
			return nil, err
		}
		var rows [][]json.RawMessage
		if json.Unmarshal(raw, &rows) != nil {
			return nil, d.Fail("KLINE_RESPONSE_INVALID", 503)
		}
		if len(rows) == 0 {
			break
		}
		for _, r := range rows {
			if len(r) < 5 {
				return nil, d.Fail("KLINE_RESPONSE_INVALID", 503)
			}
			var open int64
			var high, low, close string
			if json.Unmarshal(r[0], &open) != nil || json.Unmarshal(r[2], &high) != nil || json.Unmarshal(r[3], &low) != nil || json.Unmarshal(r[4], &close) != nil {
				return nil, d.Fail("KLINE_RESPONSE_INVALID", 503)
			}
			h, e1 := strconv.ParseFloat(high, 64)
			l, e2 := strconv.ParseFloat(low, 64)
			c, e3 := strconv.ParseFloat(close, 64)
			closeAt := open/1000 + 60
			if e1 != nil || e2 != nil || e3 != nil || closeAt > end.Unix() {
				continue
			}
			out = append(out, d.MinuteBar{CloseAt: closeAt, High: h, Low: l, Close: c})
			cursor = open + 60000
		}
		if len(rows) < 1500 {
			break
		}
	}
	return out, nil
}
