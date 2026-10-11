package experiments_test

import (
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	d "github.com/AceNanako0721/Factorforge/src/factorforge/applications/soxl_jev/domain"
)

// These public documents were followed from Binance's contract specification
// index and clearing-procedures page. They are reference evidence, not an
// acceptance of product terms or a change to the registered contract multiplier.
func TestCaptureVenueTermsReferences(t *testing.T) {
	lab := os.Getenv("FACTORFORGE_VENUE_TERMS_LAB")
	if lab == "" {
		t.Skip("explicit public product reference capture")
	}
	if !filepath.IsAbs(lab) || filepath.Base(filepath.Dir(lab)) != "runtime" {
		t.Fatal("VENUE_TERMS_PATH")
	}
	if os.Mkdir(lab, 0700) != nil {
		t.Fatal("VENUE_TERMS_LAB_EXISTS")
	}
	frozenRateWrite(t, lab, "started.json", map[string]any{"at": time.Now().UTC(), "maximum_gets": 2, "maximum_response_bytes": 4000000, "timeout_seconds": 20, "retry": false, "signed_calls": 0, "orders": 0})
	sources := []struct{ Name, URL string }{
		{"soxl-specification.pdf", "https://bin.bnbstatic.com/static/cms/cg08ou2ak0tn7mcplvfg/file/dfa33f2039fd1a8b73058c328bce625e8d178d5005a05083233be5d1de10d5eb.pdf"},
		{"clearing-procedures.pdf", "https://bin.bnbstatic.com/static/cms/cg08ou2ak0tn7mcplvfg/file/a86691f40d8155992c622715ce727d608d7f972ebb93ce90fa0106830e8a0a36.pdf"},
	}
	client := &http.Client{Timeout: 20 * time.Second, Transport: &http.Transport{Proxy: nil}, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	receipts := []map[string]any{}
	for _, source := range sources {
		start := time.Now().UTC()
		response, err := client.Get(source.URL)
		if err != nil {
			frozenRateWrite(t, lab, "capture-failed.json", map[string]any{"name": source.Name, "reason": "DELIVERY_UNKNOWN", "receipts": receipts})
			t.Fatal("VENUE_TERMS_DELIVERY_UNKNOWN")
		}
		raw, err := io.ReadAll(io.LimitReader(response.Body, 4000001))
		_ = response.Body.Close()
		receipt := map[string]any{"name": source.Name, "url": source.URL, "http": response.StatusCode, "bytes": len(raw), "sha256": d.ContentDigest(raw), "received_at": time.Now().UTC(), "elapsed_ms": time.Since(start).Milliseconds()}
		receipts = append(receipts, receipt)
		if err != nil || response.StatusCode != 200 || len(raw) > 4000000 || len(raw) < 5 || string(raw[:5]) != "%PDF-" {
			frozenRateWrite(t, lab, "capture-failed.json", map[string]any{"reason": "BODY_OR_STATUS_INVALID", "receipts": receipts})
			t.Fatal("VENUE_TERMS_CAPTURE_REJECTED")
		}
		if err = writeVenueReference(filepath.Join(lab, source.Name), raw); err != nil {
			t.Fatal("VENUE_TERMS_ARTIFACT_WRITE")
		}
		t.Logf("%s: HTTP %d, %d bytes", source.Name, response.StatusCode, len(raw))
	}
	frozenRateWrite(t, lab, "report.json", map[string]any{"receipts": receipts, "production_policy_installed": false, "product_terms_accepted": false, "signed_calls": 0, "orders": 0})
}

// Keep reference capture independent of the discontinued local-model runner.
// Exclusive creation preserves the original evidence if the caller runs again.
func writeVenueReference(path string, raw []byte) error {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return err
	}
	n, written := f.Write(raw)
	closed := f.Close()
	if written != nil {
		return written
	}
	if n != len(raw) {
		return io.ErrShortWrite
	}
	return closed
}

func TestVenueReferenceArtifactIsPrivateAndImmutable(t *testing.T) {
	path := filepath.Join(t.TempDir(), "reference.pdf")
	original := []byte("%PDF-1.7 fixture reference\n")
	if err := writeVenueReference(path, original); err != nil {
		t.Fatal(err)
	}
	if err := writeVenueReference(path, []byte("replacement")); !os.IsExist(err) {
		t.Fatal("original reference could be replaced", err)
	}
	raw, err := os.ReadFile(path)
	if err != nil || string(raw) != string(original) {
		t.Fatal("reference evidence changed", err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if runtime.GOOS != "windows" && info.Mode().Perm() != 0600 {
		t.Fatal("reference evidence is not private")
	}
}
