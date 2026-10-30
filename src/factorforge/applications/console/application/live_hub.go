package application

import (
	"context"
	"sync"
	"time"

	d "github.com/AceNanako0721/Factorforge/src/factorforge/applications/console/domain"
)

// LiveSource is the venue public push (d.MarketStream in production).
type LiveSource interface {
	Run(ctx context.Context, instrument, interval string, maxAge time.Duration, emit func(d.MarketEvent)) (int, error)
}

// LiveSnapshot is the newest reference value of one instrument (S4-019).
// Seq numbers let a reader skip values it already sent.
type LiveSnapshot struct {
	State             string // CONNECTING, LIVE, STALE or UNAVAILABLE
	Code              string
	LastAt            time.Time // receive time of the newest accepted frame
	Mark              *d.MarketEvent
	Kline             *d.LiveKline
	MarkSeq, KlineSeq uint64
}

// LiveHub shares one upstream connection per instrument among all browser
// streams. It never stores, forwards to a lower layer or decides anything.
type LiveHub struct {
	// Venue and Product name the instruments whose public push Source speaks.
	Venue, Product string
	Source         LiveSource
	Interval       string
	Stale          time.Duration
	Idle           time.Duration
	MaxStreams     int
	MaxAge         time.Duration           // connection renewal, below the venue's 24 hours
	Backoff        func(int) time.Duration // reconnect delay by attempt; nil uses 1, 2, 4 … 30 s
	Now            func() time.Time

	mu      sync.Mutex
	feeds   map[string]*liveFeed
	streams int
}
type liveFeed struct {
	snap   LiveSnapshot
	subs   map[*LiveSubscription]struct{}
	cancel context.CancelFunc
	idle   *time.Timer
}

// LiveSubscription delivers change notices; C holds at most one pending
// notice, so a slow reader only skips frames and never blocks the feed.
type LiveSubscription struct {
	C    chan struct{}
	hub  *LiveHub
	key  string
	once sync.Once
}

func (h *LiveHub) now() time.Time {
	if h.Now != nil {
		return h.Now()
	}
	return time.Now()
}
func (h *LiveHub) backoff(attempt int) time.Duration {
	if h.Backoff != nil {
		return h.Backoff(attempt)
	}
	delay := time.Second << min(attempt, 5)
	return min(delay, 30*time.Second)
}

// Subscribe joins (or starts) the feed of instrument.
func (h *LiveHub) Subscribe(instrument string) (*LiveSubscription, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.streams >= h.MaxStreams {
		return nil, d.Fail("LIVE_STREAM_BUDGET", 429)
	}
	if h.feeds == nil {
		h.feeds = map[string]*liveFeed{}
	}
	f := h.feeds[instrument]
	if f == nil {
		ctx, cancel := context.WithCancel(context.Background())
		f = &liveFeed{snap: LiveSnapshot{State: "CONNECTING"}, subs: map[*LiveSubscription]struct{}{}, cancel: cancel}
		h.feeds[instrument] = f
		go h.run(ctx, instrument, f)
		go h.watch(ctx, f)
	}
	if f.idle != nil {
		f.idle.Stop()
		f.idle = nil
	}
	s := &LiveSubscription{C: make(chan struct{}, 1), hub: h, key: instrument}
	f.subs[s] = struct{}{}
	h.streams++
	notify(s)
	return s, nil
}

// Snapshot returns the newest value of the subscribed instrument.
func (s *LiveSubscription) Snapshot() LiveSnapshot {
	s.hub.mu.Lock()
	defer s.hub.mu.Unlock()
	if f := s.hub.feeds[s.key]; f != nil {
		return f.snap
	}
	return LiveSnapshot{State: "UNAVAILABLE", Code: "LIVE_UPSTREAM_UNAVAILABLE"}
}

// Close leaves the feed; the last leaver closes it after Idle.
func (s *LiveSubscription) Close() {
	s.once.Do(func() {
		h := s.hub
		h.mu.Lock()
		defer h.mu.Unlock()
		h.streams--
		f := h.feeds[s.key]
		if f == nil {
			return
		}
		delete(f.subs, s)
		if len(f.subs) == 0 && f.idle == nil {
			f.idle = time.AfterFunc(h.Idle, func() {
				h.mu.Lock()
				defer h.mu.Unlock()
				if h.feeds[s.key] == f && len(f.subs) == 0 {
					f.cancel()
					delete(h.feeds, s.key)
				}
			})
		}
	})
}

func notify(s *LiveSubscription) {
	select {
	case s.C <- struct{}{}:
	default:
	}
}
func (h *LiveHub) broadcast(f *liveFeed) {
	for s := range f.subs {
		notify(s)
	}
}

// run keeps the upstream connected with backoff until the feed is closed.
func (h *LiveHub) run(ctx context.Context, instrument string, f *liveFeed) {
	for attempt := 0; ctx.Err() == nil; {
		received := false
		h.Source.Run(ctx, instrument, h.Interval, h.MaxAge, func(e d.MarketEvent) {
			h.mu.Lock()
			defer h.mu.Unlock()
			received = true
			f.snap.LastAt = h.now()
			if e.Kline != nil {
				k := *e.Kline
				f.snap.Kline = &k
				f.snap.KlineSeq++
			} else {
				m := e
				f.snap.Mark = &m
				f.snap.MarkSeq++
			}
			f.snap.State, f.snap.Code = "LIVE", ""
			h.broadcast(f)
		})
		if ctx.Err() != nil {
			return
		}
		if received {
			attempt = 0
		}
		h.mu.Lock()
		f.snap.State, f.snap.Code = "UNAVAILABLE", "LIVE_UPSTREAM_UNAVAILABLE"
		h.broadcast(f)
		h.mu.Unlock()
		select {
		case <-ctx.Done():
			return
		case <-time.After(h.backoff(attempt)):
		}
		attempt++
	}
}

// watch turns LIVE into STALE when no frame arrived within Stale.
func (h *LiveHub) watch(ctx context.Context, f *liveFeed) {
	tick := time.NewTicker(max(h.Stale/4, 10*time.Millisecond))
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
			h.mu.Lock()
			if f.snap.State == "LIVE" && h.now().Sub(f.snap.LastAt) > h.Stale {
				f.snap.State, f.snap.Code = "STALE", ""
				h.broadcast(f)
			}
			h.mu.Unlock()
		}
	}
}
