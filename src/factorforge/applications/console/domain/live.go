package domain

import "time"

// MarketEvent is one accepted public market message (S4-019, P4 design 17.1).
// Prices stay decimal strings; nothing here is an accounting fact.
type MarketEvent struct {
	At    time.Time
	Price string     // set for a mark price update
	Kline *LiveKline // set for a kline update
}

// LiveKline is the bar currently forming (or just finished) at the venue.
type LiveKline struct {
	OpenAt, CloseAt        time.Time
	Open, High, Low, Close string
	Final                  bool
}
