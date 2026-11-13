package soxl_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	d "github.com/AceNanako0721/Factorforge/src/factorforge/applications/soxl/domain"
	"github.com/AceNanako0721/Factorforge/src/factorforge/applications/soxl/submission"
	dto "github.com/AceNanako0721/Factorforge/src/factorforge/strategy/api/dto"
)

// The position manager asks only for the current directives: paging every
// archived one (4000+ after a month of initial-distance refreshes) exceeded
// the page budget, and the effective stop was then missing every round.
func TestStopDirectivesReadsCurrentScopeInOneRequest(t *testing.T) {
	gets := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v2/strategy/health":
			json.NewEncoder(w).Encode(map[string]any{"schema_version": "strategy-2.0", "environment": "SIM", "listener": "WORKLOAD"})
		case "/api/v2/strategy/objects/obj/stop-directives":
			gets++
			if r.URL.Query().Get("scope") != "current" || r.URL.Query().Has("cursor") {
				w.WriteHeader(422)
				return
			}
			json.NewEncoder(w).Encode(map[string]any{"snapshot_version": 9, "cursor": "", "items": []dto.StopDirectiveRecord{{Directive: dto.StopDirective{DirectiveID: "trail-1", Version: 7, Kind: "POSITION_STOP"}}}})
		default:
			w.WriteHeader(404)
		}
	}))
	t.Cleanup(server.Close)
	client, err := submission.NewHTTP(submission.HTTPOptions{BaseURL: server.URL, Token: "fixture-only", Binding: d.Binding{InstanceID: "inst", Environment: "SIM"}, Client: &http.Client{Timeout: time.Second}, MaxResponseBytes: 10000, MaxPages: 1})
	if err != nil {
		t.Fatal(err)
	}
	rows, err := client.StopDirectives(context.Background(), "obj")
	if err != nil || gets != 1 || len(rows) != 1 || rows[0].Directive.DirectiveID != "trail-1" {
		t.Fatal("current directives", err, gets, rows)
	}
}
