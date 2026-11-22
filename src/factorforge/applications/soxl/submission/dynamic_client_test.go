package submission

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	d "github.com/AceNanako0721/Factorforge/src/factorforge/applications/soxl/domain"
	dto "github.com/AceNanako0721/Factorforge/src/factorforge/strategy/api/dto"
)

const casConflict = `{"code":"AGGREGATE_VERSION_CONFLICT","message":"AGGREGATE_VERSION_CONFLICT","field":null,"correlation_id":null,"retryable":true}`

func conflictServer(t *testing.T, conflicts int) (*HTTPClient, *int) {
	t.Helper()
	posts := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == "GET" && r.URL.Path == "/api/v2/strategy/health":
			_, _ = w.Write([]byte(`{"environment":"SIM","listener":"WORKLOAD","schema_version":"strategy-2.0"}`))
		case r.Method == "GET" && r.URL.Path == "/api/v2/strategy/objects":
			_, _ = w.Write([]byte(`{"snapshot_version":7,"items":[]}`))
		case r.Method == "POST" && r.URL.Path == "/api/v2/strategy/stop-directives":
			posts++
			if posts <= conflicts {
				w.WriteHeader(http.StatusConflict)
				_, _ = w.Write([]byte(casConflict))
				return
			}
			_, _ = w.Write([]byte(`{"directive_id":"dir-1","version":3}`))
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	c, err := NewHTTP(HTTPOptions{BaseURL: srv.URL, Token: "t", Binding: d.Binding{InstanceID: "inst-1", Environment: "SIM"},
		Client: &http.Client{Timeout: 5 * time.Second}, MaxResponseBytes: 1 << 20, MaxPages: 1})
	if err != nil {
		t.Fatal(err)
	}
	return c, &posts
}

func TestCommandRetriesConflictBurst(t *testing.T) {
	old := commandBackoff
	commandBackoff = time.Millisecond
	t.Cleanup(func() { commandBackoff = old })
	c, posts := conflictServer(t, commandAttempts-1)
	if _, err := c.SubmitStopDirective(context.Background(), dto.StopDirective{DirectiveID: "dir-1", Version: 3}); err != nil {
		t.Fatalf("burst of %d conflicts should be absorbed: %v", commandAttempts-1, err)
	}
	if *posts != commandAttempts {
		t.Fatalf("posts=%d want %d", *posts, commandAttempts)
	}
}

func TestCommandGivesUpAfterAttempts(t *testing.T) {
	old := commandBackoff
	commandBackoff = time.Millisecond
	t.Cleanup(func() { commandBackoff = old })
	c, posts := conflictServer(t, commandAttempts)
	_, err := c.SubmitStopDirective(context.Background(), dto.StopDirective{DirectiveID: "dir-1", Version: 3})
	if e, ok := err.(*d.Error); !ok || e.Code != "FRAMEWORK_AGGREGATE_VERSION_CONFLICT" {
		t.Fatalf("err=%v", err)
	}
	if *posts != commandAttempts {
		t.Fatalf("posts=%d want %d", *posts, commandAttempts)
	}
}

func TestCommandPauseJitterBounds(t *testing.T) {
	for attempt := 1; attempt < commandAttempts; attempt++ {
		for i := 0; i < 50; i++ {
			p := commandPause(attempt)
			lo := time.Duration(attempt) * commandBackoff
			if p < lo || p >= lo+commandBackoff {
				t.Fatalf("attempt %d pause %v outside [%v,%v)", attempt, p, lo, lo+commandBackoff)
			}
		}
	}
	var total time.Duration
	for attempt := 1; attempt < commandAttempts; attempt++ {
		total += time.Duration(attempt+1) * commandBackoff
	}
	if total >= time.Minute {
		t.Fatalf("worst-case retry wait %v must stay far inside one 5-minute cycle", total)
	}
}
