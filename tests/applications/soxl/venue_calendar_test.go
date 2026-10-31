package soxl_test

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	d "github.com/AceNanako0721/Factorforge/src/factorforge/applications/soxl/domain"
	"github.com/AceNanako0721/Factorforge/src/factorforge/applications/soxl/operations"
)

func venueRequest(t *testing.T, now time.Time) operations.VenueCalendarRequest {
	t.Helper()
	first := now.Truncate(24 * time.Hour).Add(-24*time.Hour + 13*time.Hour + 30*time.Minute)
	close := first.Add(3*time.Hour + 30*time.Minute)
	second := first.Add(73 * time.Hour)
	third := second.Add(24 * time.Hour)
	session := func(a, b time.Time, kind string) map[string]any {
		return map[string]any{"startTime": a.UnixMilli(), "endTime": b.UnixMilli(), "type": kind, "newField": "allowed upstream extension"}
	}
	product, _ := json.Marshal(map[string]any{"symbols": []any{map[string]any{"symbol": "SOXLUSDT", "contractType": "TRADIFI_PERPETUAL", "underlyingType": "EQUITY", "baseAsset": "SOXL", "quoteAsset": "USDT", "marginAsset": "USDT", "status": "TRADING"}}, "unrelatedField": 1})
	schedule, _ := json.Marshal(map[string]any{"updateTime": now.Add(-time.Minute).UnixMilli(), "marketSchedules": map[string]any{"EQUITY": map[string]any{"sessions": []any{session(first, close, "REGULAR"), session(close, second, "NO_TRADING"), session(second, second.Add(6*time.Hour+30*time.Minute), "REGULAR"), session(second.Add(6*time.Hour+30*time.Minute), third, "OVERNIGHT"), session(third, third.Add(6*time.Hour+30*time.Minute), "REGULAR")}}, "FX": map[string]any{"newField": true}}})
	snapshot := func(path string, raw []byte) operations.VenueSnapshot {
		return operations.VenueSnapshot{URL: "https://demo-fapi.binance.com" + path, Content: string(raw), ContentHash: d.ContentDigest(raw), ReceivedAt: now}
	}
	return operations.VenueCalendarRequest{SchemaVersion: 1, Binding: d.Binding{InstanceID: "fixture-venue", Environment: "SIM"}, Version: "fixture-calendar-registration", ProviderEnvironment: "DEMO", ProductSnapshot: snapshot("/fapi/v1/exchangeInfo", product), ScheduleSnapshot: snapshot("/fapi/v1/tradingSchedule", schedule), Limits: operations.VenueCalendarLimits{MaxInputBytes: 100000, MaxSessions: 10, MaxUpdateAgeSeconds: 3600}}
}

func TestVenueCalendarStrictSnapshotAndDST(t *testing.T) {
	now := time.Date(2026, 10, 31, 12, 0, 0, 0, time.UTC)
	r := venueRequest(t, now)
	a, err := operations.CompileVenueCalendar(r, now)
	if err != nil {
		t.Fatal(err)
	}
	windows, err := a.Calendar.Windows(context.Background())
	if err != nil || len(windows) != 5 || windows[1].Kind != "NON_TRADITIONAL" || windows[1].End.Sub(windows[1].Start) != 69*time.Hour+30*time.Minute {
		t.Fatal("DST/early close weekend split", windows, err)
	}
	if a.Calendar.Sessions[0].OpenLocal != "09:30" || a.Calendar.Sessions[0].CloseLocal != "13:00" || a.Calendar.Sessions[1].OpenLocal != "09:30" {
		t.Fatal("DST local conversion", a.Calendar.Sessions)
	}
	for _, name := range []string{"environment", "live", "hash", "utf8", "future-received", "expired", "future-update", "expired-update", "duplicate", "duplicate-unused", "missing-product", "duplicate-product", "wrong-identity", "unknown-session", "gap", "overlap", "precision", "one-regular", "missing-market", "bytes", "sessions"} {
		t.Run(name, func(t *testing.T) {
			x := venueRequest(t, now)
			var wire map[string]any
			json.Unmarshal([]byte(x.ScheduleSnapshot.Content), &wire)
			rows := wire["marketSchedules"].(map[string]any)["EQUITY"].(map[string]any)["sessions"].([]any)
			switch name {
			case "environment":
				x.ProviderEnvironment = "PUBLIC_MAIN"
			case "live":
				x.Binding.Environment = "LIVE"
			case "hash":
				x.ProductSnapshot.ContentHash = "bad"
			case "utf8":
				x.ProductSnapshot.Content = "\xff"
			case "future-received":
				x.ProductSnapshot.ReceivedAt = now.Add(time.Second)
			case "expired":
				x.ProductSnapshot.ReceivedAt = now.Add(-2 * time.Hour)
			case "future-update":
				wire["updateTime"] = now.Add(time.Second).UnixMilli()
			case "expired-update":
				wire["updateTime"] = now.Add(-2 * time.Hour).UnixMilli()
			case "duplicate":
				x.ProductSnapshot.Content = strings.Replace(x.ProductSnapshot.Content, `"symbols":`, `"symbols":[],"symbols":`, 1)
			case "duplicate-unused":
				x.ProductSnapshot.Content = strings.Replace(x.ProductSnapshot.Content, `"unrelatedField":1`, `"unrelatedField":{"x":1,"x":2}`, 1)
			case "missing-product":
				x.ProductSnapshot.Content = `{"symbols":[]}`
			case "duplicate-product":
				x.ProductSnapshot.Content = strings.Replace(x.ProductSnapshot.Content, `"symbols":[`, `"symbols":[{"symbol":"SOXLUSDT"},`, 1)
			case "wrong-identity":
				x.ProductSnapshot.Content = strings.Replace(x.ProductSnapshot.Content, "EQUITY", "HK_EQUITY", 1)
			case "unknown-session":
				rows[1].(map[string]any)["type"] = "HALT_UNSPECIFIED"
			case "gap":
				rows[1].(map[string]any)["startTime"] = rows[1].(map[string]any)["startTime"].(float64) + 60000
			case "overlap":
				rows[1].(map[string]any)["startTime"] = rows[1].(map[string]any)["startTime"].(float64) - 60000
			case "precision":
				rows[0].(map[string]any)["startTime"] = rows[0].(map[string]any)["startTime"].(float64) + 1
			case "one-regular":
				rows[2].(map[string]any)["type"] = "NO_TRADING"
				rows[4].(map[string]any)["type"] = "NO_TRADING"
			case "missing-market":
				delete(wire["marketSchedules"].(map[string]any), "EQUITY")
			case "bytes":
				x.Limits.MaxInputBytes = 1
			case "sessions":
				x.Limits.MaxSessions = 2
			}
			b, _ := json.Marshal(wire)
			x.ScheduleSnapshot.Content = string(b)
			x.ScheduleSnapshot.ContentHash = d.ContentDigest(b)
			if name != "hash" {
				x.ProductSnapshot.ContentHash = d.ContentDigest([]byte(x.ProductSnapshot.Content))
			}
			if _, err := operations.CompileVenueCalendar(x, now); err == nil {
				t.Fatal("invalid snapshot admitted")
			}
		})
	}
	// A response refresh changes artifact identity but cannot replenish windows.
	r2 := r
	r2.ScheduleSnapshot.Content = strings.Replace(r2.ScheduleSnapshot.Content, `"newField":true`, `"newField":false`, 1)
	r2.ScheduleSnapshot.ContentHash = d.ContentDigest([]byte(r2.ScheduleSnapshot.Content))
	a1, _ := operations.CompileVenueCalendar(r, now)
	a2, err := operations.CompileVenueCalendar(r2, now)
	if err != nil || a1.ArtifactID == a2.ArtifactID || a1.Calendar.Version != a2.Calendar.Version {
		t.Fatal("unrelated refresh churned windows", err)
	}
}

func writePrivateJSON(t *testing.T, file string, value any) {
	t.Helper()
	raw, e := json.Marshal(value)
	if e != nil || os.MkdirAll(filepath.Dir(file), 0700) != nil || os.WriteFile(file, raw, 0600) != nil {
		t.Fatal("fixture write", e)
	}
}

func TestVenueCalendarPrivateFileBoundary(t *testing.T) {
	wd, _ := os.Getwd()
	root := filepath.Clean(filepath.Join(wd, "..", "..", ".."))
	t.Chdir(root)
	dir, err := os.MkdirTemp(filepath.Join(root, "runtime"), "calendar-assets-test-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	now := time.Now().UTC()
	r := venueRequest(t, now)
	input, output := filepath.Join(dir, "input.json"), filepath.Join(dir, "calendar.json")
	writePrivateJSON(t, input, r)
	if err = operations.CompileVenueCalendarFile(root, input, output, 100000, now); err != nil {
		t.Fatal(err)
	}
	if err = operations.CompileVenueCalendarFile(root, input, output, 100000, now); err == nil {
		t.Fatal("overwrite")
	}
	info, _ := os.Stat(output)
	if info.Mode().Perm() != 0600 {
		t.Fatal("private mode")
	}
	link := filepath.Join(dir, "link.json")
	if os.Symlink(input, link) != nil {
		t.Fatal("symlink fixture")
	}
	if err = operations.CompileVenueCalendarFile(root, link, filepath.Join(dir, "second.json"), 100000, now); err == nil {
		t.Fatal("symlink input read")
	}
	if err = operations.CompileVenueCalendarFile(root, input, filepath.Join(root, "outside.json"), 100000, now); err == nil {
		t.Fatal("output outside runtime")
	}
}

func TestVenueCalendarNativeOfflineCLI(t *testing.T) {
	wd, _ := os.Getwd()
	root := t.TempDir()
	bin := filepath.Join(root, "instance-cli")
	cmd := exec.Command("go", "build", "-o", bin, "./src/factorforge/applications/soxl/entrypoints/instance-cli")
	cmd.Dir = filepath.Clean(filepath.Join(wd, "..", "..", ".."))
	if out, e := cmd.CombinedOutput(); e != nil {
		t.Fatal("native build", e, string(out))
	}
	r := venueRequest(t, time.Now().UTC())
	input, output := filepath.Join(root, "input.json"), filepath.Join(root, "runtime", "calendar.json")
	writePrivateJSON(t, input, r)
	run := func() ([]byte, error) {
		cmd := exec.Command(bin, "--action", "compile-calendar", "--root", root, "--input", input, "--output", output, "--max-bytes", "200000")
		cmd.Dir = root
		cmd.Env = []string{"PATH=/nonexistent"}
		return cmd.CombinedOutput()
	}
	if out, e := run(); e != nil {
		t.Fatal("offline compile acquired dependency", e, string(out))
	}
	raw, e := os.ReadFile(output)
	var artifact operations.VenueCalendarArtifact
	if e != nil || json.Unmarshal(raw, &artifact) != nil || artifact.Calendar.Version == "" {
		t.Fatal("artifact not written", e)
	}
	if out, e := run(); e == nil || !strings.Contains(string(out), "PRIVATE_OUTPUT_EXISTS") {
		t.Fatal("native restart overwrite", e)
	}
}
