package binance

import (
	"context"
	"errors"
	"fmt"
	"net"
	"sync"
	"time"
)

// RequestFailure describes a failed venue request for operator logs: the
// admitted path, the HTTP status (0 when no response arrived), the venue's
// numeric error code (0 when it gave none), the kind of failure and how long
// the request took. It never holds parameters, signatures, keys, account or
// order identities, or response bodies.
type RequestFailure struct {
	Path      string
	Cause     string // TIMEOUT, NETWORK, STATUS or BODY
	Status    int
	VenueCode int64
	Elapsed   time.Duration
}

func (f RequestFailure) String() string {
	return fmt.Sprintf("path=%s cause=%s status=%d venue_code=%d elapsed_ms=%d", f.Path, f.Cause, f.Status, f.VenueCode, f.Elapsed.Milliseconds())
}

// failures keeps the outcome of the most recent request that reached the
// network: a failure, or nothing after a success.
type failures struct {
	mu   sync.Mutex
	last *RequestFailure
}

func (l *failures) record(f *RequestFailure) {
	l.mu.Lock()
	l.last = f
	l.mu.Unlock()
}
func (l *failures) String() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.last == nil {
		return ""
	}
	return l.last.String()
}
func networkCause(err error) string {
	var timeout net.Error
	if errors.Is(err, context.DeadlineExceeded) || errors.As(err, &timeout) && timeout.Timeout() {
		return "TIMEOUT"
	}
	return "NETWORK"
}
