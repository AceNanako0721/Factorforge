package strategy_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"sync"
	"testing"

	"github.com/AceNanako0721/Factorforge/src/factorforge/strategy/adapters"
	sd "github.com/AceNanako0721/Factorforge/src/factorforge/strategy/domain"
)

func TestAuditTargetsPagesAndReadsOnlyNewRows(t *testing.T) {
	var mu sync.Mutex
	audit := []map[string]any{}
	add := func(n int) {
		for i := 0; i < n; i++ {
			action := "MARKET_SNAPSHOT"
			if len(audit)%10 == 0 {
				action = "TARGET_SET"
			}
			audit = append(audit, map[string]any{"sequence": len(audit), "action": action, "request_id": fmt.Sprintf("decision-%d", len(audit))})
		}
	}
	add(1205)
	requested := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		offset, _ := strconv.Atoi(r.URL.Query().Get("cursor"))
		limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
		if r.URL.Path != "/api/v2/trading/audit" || limit <= 0 || limit > adapters.AuditPageLimit {
			http.Error(w, `{"code":"INVALID_REQUEST"}`, 422)
			return
		}
		end, cursor := min(offset+limit, len(audit)), any(nil)
		if end < len(audit) {
			cursor = strconv.Itoa(end)
		}
		requested += end - offset
		json.NewEncoder(w).Encode(map[string]any{"items": audit[offset:end], "cursor": cursor, "snapshot_version": len(audit)})
	}))
	defer server.Close()
	client := &adapters.TradingV2{URL: server.URL, Token: "t", Client: server.Client()}
	query := url.Values{"environment": {"SIM"}, "account_id": {"a"}, "run_id": {"r"}}
	page, err := client.AuditTargets(context.Background(), query)
	if err != nil || len(sd.Rows(page["items"])) != 121 || requested != 1205 || sd.Number(page["snapshot_version"]) != 1205 {
		t.Fatal("first read", err, len(sd.Rows(page["items"])), requested)
	}
	mu.Lock()
	add(15)
	requested = 0
	mu.Unlock()
	page, err = client.AuditTargets(context.Background(), query)
	rows := sd.Rows(page["items"])
	if err != nil || len(rows) != 122 || requested != 15 || rows[121]["request_id"] != "decision-1210" || sd.Number(page["snapshot_version"]) != 1220 {
		t.Fatal("incremental read", err, len(rows), requested)
	}
	for _, row := range rows {
		if row["action"] != "TARGET_SET" {
			t.Fatal("non-target row kept", row)
		}
	}
}
