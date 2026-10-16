package adapters

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"net"
	"net/url"
	"regexp"
	"strings"
	"time"

	d "github.com/AceNanako0721/Factorforge/src/factorforge/applications/console/domain"
	"golang.org/x/net/websocket"
)

// MarketEvent and LiveKline are the domain reference values.
type (
	MarketEvent = d.MarketEvent
	LiveKline   = d.LiveKline
)

// MarketStream subscribes to the venue's public mark price and kline push
// for one instrument. It needs no account credential.
type MarketStream struct {
	URL         string // wss root, for example wss://fstream.binance.com
	Origin      string
	TLS         *tls.Config // nil uses the system roots
	ReadTimeout time.Duration
}

var decimalText = regexp.MustCompile(`^[0-9]{1,20}(\.[0-9]{1,20})?$`)

func positive(s string) bool {
	return decimalText.MatchString(s) && strings.Trim(strings.ReplaceAll(s, ".", ""), "0") != ""
}
func millis(n int64) time.Time { return time.UnixMilli(n).UTC() }

// Parse accepts one combined-stream frame for instrument; ok is false for a
// frame that is malformed, for another symbol or of an unknown kind. Keys are
// read exactly: the venue sends pairs such as "p"/"P" and "l"/"L", which the
// case-insensitive struct decoding of encoding/json would mix up.
func Parse(raw []byte, instrument string) (MarketEvent, bool) {
	var frame struct {
		Data map[string]json.RawMessage `json:"data"`
	}
	if json.Unmarshal(raw, &frame) != nil || frame.Data == nil {
		return MarketEvent{}, false
	}
	var kind, symbol string
	var at int64
	if !field(frame.Data, "e", &kind) || !field(frame.Data, "s", &symbol) || !field(frame.Data, "E", &at) || symbol != instrument || at <= 0 {
		return MarketEvent{}, false
	}
	switch kind {
	case "markPriceUpdate":
		var price string
		if !field(frame.Data, "p", &price) || !positive(price) {
			return MarketEvent{}, false
		}
		return MarketEvent{At: millis(at), Price: price}, true
	case "kline":
		var k map[string]json.RawMessage
		var open, close int64
		var o, h, l, c string
		var final bool
		if !field(frame.Data, "k", &k) || !field(k, "t", &open) || !field(k, "T", &close) || !field(k, "o", &o) || !field(k, "h", &h) || !field(k, "l", &l) || !field(k, "c", &c) || !field(k, "x", &final) {
			return MarketEvent{}, false
		}
		if open <= 0 || close <= open || !positive(o) || !positive(h) || !positive(l) || !positive(c) {
			return MarketEvent{}, false
		}
		return MarketEvent{At: millis(at), Kline: &LiveKline{OpenAt: millis(open), CloseAt: millis(close + 1), Open: o, High: h, Low: l, Close: c, Final: final}}, true
	}
	return MarketEvent{}, false
}
func field(m map[string]json.RawMessage, key string, out any) bool {
	raw, ok := m[key]
	return ok && json.Unmarshal(raw, out) == nil
}

// Run holds one connection until ctx ends, a read fails or maxAge passes, and
// calls emit for every accepted frame. dropped counts rejected frames.
func (s MarketStream) Run(ctx context.Context, instrument, interval string, maxAge time.Duration, emit func(MarketEvent)) (dropped int, err error) {
	symbol := strings.ToLower(instrument)
	cfg, err := websocket.NewConfig(strings.TrimRight(s.URL, "/")+"/market/stream?streams="+url.PathEscape(symbol+"@markPrice@1s")+"/"+url.PathEscape(symbol+"@kline_"+interval), s.Origin)
	if err != nil {
		return 0, err
	}
	cfg.TlsConfig = s.TLS
	cfg.Dialer = &net.Dialer{Timeout: s.ReadTimeout}
	conn, err := cfg.DialContext(ctx)
	if err != nil {
		return 0, err
	}
	defer conn.Close()
	stop := context.AfterFunc(ctx, func() { conn.Close() })
	defer stop()
	deadline := time.Now().Add(maxAge)
	for time.Now().Before(deadline) {
		conn.SetReadDeadline(time.Now().Add(s.ReadTimeout))
		var raw []byte
		if err = websocket.Message.Receive(conn, &raw); err != nil {
			if ctx.Err() != nil {
				return dropped, ctx.Err()
			}
			return dropped, err
		}
		if event, ok := Parse(raw, instrument); ok {
			emit(event)
		} else {
			dropped++
		}
	}
	return dropped, nil
}
