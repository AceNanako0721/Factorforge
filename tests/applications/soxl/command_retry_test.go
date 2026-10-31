package soxl_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	d "github.com/AceNanako0721/Factorforge/src/factorforge/applications/soxl/domain"
	"github.com/AceNanako0721/Factorforge/src/factorforge/applications/soxl/submission"
	dto "github.com/AceNanako0721/Factorforge/src/factorforge/strategy/api/dto"
)

// a stop directive meets P2's decision cycle and P1's collector at
// the same 5-minute boundary. Two CAS conflicts in one round, or a P1 snapshot
// that moved while P2 read it, used to fail the whole position-manager round.
func commandServer(t *testing.T, rejections []string) (*submission.HTTPClient, *int) {
	posts := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v2/strategy/health":
			json.NewEncoder(w).Encode(map[string]any{"schema_version": "strategy-2.0", "environment": "SIM", "listener": "WORKLOAD"})
		case "/api/v2/strategy/objects":
			json.NewEncoder(w).Encode(map[string]any{"snapshot_version": 7 + posts, "items": []any{}})
		case "/api/v2/strategy/stop-directives":
			posts++
			if posts <= len(rejections) {
				w.WriteHeader(409)
				fmt.Fprintf(w, `{"code":%q,"message":%q,"field":null,"correlation_id":null,"retryable":false}`, rejections[posts-1], rejections[posts-1])
				return
			}
			json.NewEncoder(w).Encode(map[string]any{"directive_id": "stop-1", "object_id": "obj", "version": 2})
		default:
			w.WriteHeader(404)
		}
	}))
	t.Cleanup(server.Close)
	client, err := submission.NewHTTP(submission.HTTPOptions{BaseURL: server.URL, Token: "fixture-only", Binding: d.Binding{InstanceID: "inst", Environment: "SIM"}, Client: &http.Client{Timeout: time.Second}, MaxResponseBytes: 10000, MaxPages: 2})
	if err != nil {
		t.Fatal(err)
	}
	return client, &posts
}

func TestStopDirectiveRetriesConflictBurst(t *testing.T) {
	for _, tc := range []struct {
		name       string
		rejections []string
	}{
		{"two-cas", []string{"AGGREGATE_VERSION_CONFLICT", "AGGREGATE_VERSION_CONFLICT"}},
		{"trading-snapshot", []string{"TRADING_SNAPSHOT_CONFLICT"}},
		{"mixed", []string{"TRADING_SNAPSHOT_CONFLICT", "AGGREGATE_VERSION_CONFLICT"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			client, posts := commandServer(t, tc.rejections)
			if _, err := client.SubmitStopDirective(context.Background(), dto.StopDirective{DirectiveID: "stop-1", Version: 2}); err != nil || *posts != len(tc.rejections)+1 {
				t.Fatal("conflict burst failed the round", err, *posts)
			}
		})
	}
}

func TestStopDirectiveStopsAfterThreeConflictsAndOtherConflicts(t *testing.T) {
	client, posts := commandServer(t, []string{"AGGREGATE_VERSION_CONFLICT", "AGGREGATE_VERSION_CONFLICT", "AGGREGATE_VERSION_CONFLICT"})
	if _, err := client.SubmitStopDirective(context.Background(), dto.StopDirective{DirectiveID: "stop-1", Version: 2}); providerError(err) != "FRAMEWORK_AGGREGATE_VERSION_CONFLICT" || *posts != 3 {
		t.Fatal("retry not bounded", err, *posts)
	}
	client, posts = commandServer(t, []string{"IDEMPOTENCY_CONFLICT"})
	if _, err := client.SubmitStopDirective(context.Background(), dto.StopDirective{DirectiveID: "stop-1", Version: 2}); providerError(err) != "FRAMEWORK_CONFLICT" || *posts != 1 {
		t.Fatal("identity conflict retried", err, *posts)
	}
}
