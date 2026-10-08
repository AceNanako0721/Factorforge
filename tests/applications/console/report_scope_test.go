package console_test

import (
	"bytes"
	"context"
	"encoding/json"
	d "github.com/AceNanako0721/Factorforge/src/factorforge/applications/console/domain"
	"testing"
)

func TestNativeReportDetailsAreFiniteAndBoundToSelectedObject(t *testing.T) {
	w := world(t)
	raw, e := w.Client.Get(context.Background(), w.Selection, "reports", "", nil)
	if e != nil {
		t.Fatal(e)
	}
	var page map[string]any
	if e = json.Unmarshal(raw, &page); e != nil {
		t.Fatal(e)
	}
	items := page["items"].([]any)
	if len(items) != 1 {
		t.Fatal("missing recorded report")
	}
	own := items[0].(map[string]any)
	clone, _ := json.Marshal(own)
	var foreign map[string]any
	json.Unmarshal(clone, &foreign)
	foreign["report_id"] = "foreign-report"
	foreign["report_details"].(map[string]any)["object_id"] = "foreign-object"
	page["items"] = append(items, foreign)
	mixed, _ := json.Marshal(page)
	safe, e := d.ProjectBound("instances", d.Routes["reports"].Path, mixed, false, w.Selection.ObjectID)
	if e != nil || bytes.Contains(safe.Bytes(), []byte("foreign-report")) || !bytes.Contains(safe.Bytes(), []byte(`"unknown_ratio":"0.5"`)) || !bytes.Contains(safe.Bytes(), []byte("INSUFFICIENT_EVIDENCE")) {
		t.Fatal("report scope or projection", e, string(safe.Bytes()))
	}
	own["report_details"].(map[string]any)["prompt"] = "private-canary"
	bad, _ := json.Marshal(page)
	if _, e = d.ProjectBound("instances", d.Routes["reports"].Path, bad, false, w.Selection.ObjectID); e == nil {
		t.Fatal("unknown report field admitted")
	}
	if w.Writes.Load() != 0 {
		t.Fatal("report read wrote lower state")
	}
}
