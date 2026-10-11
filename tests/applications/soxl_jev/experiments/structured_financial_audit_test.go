package experiments_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	d "github.com/AceNanako0721/Factorforge/src/factorforge/applications/soxl_jev/domain"
)

// The native TS experiment hashed its timestamp string. Go time.Time instead
// strips trailing fractional zeroes. Audit the original hash before creating
// an explicitly separate Go projection; never rewrite original evidence.
type structuredTransportGeneration struct {
	Provider    string `json:"provider"`
	AccountID   int64  `json:"account_id"`
	Model       string `json:"model"`
	PromptHash  string `json:"prompt_hash"`
	Text        string `json:"text"`
	CompletedAt string `json:"completed_at"`
}

func TestStructuredFinancialAuditProjection(t *testing.T) {
	source := os.Getenv("FACTORFORGE_STRUCTURED_FINANCIAL_AUDIT_SOURCE")
	if source == "" {
		t.Skip("explicit native timestamp audit, no model calls")
	}
	target := os.Getenv("FACTORFORGE_STRUCTURED_FINANCIAL_AUDIT_TARGET")
	if !filepath.IsAbs(source) || filepath.Dir(source) != filepath.Dir(target) || filepath.Base(filepath.Dir(source)) != "runtime" {
		t.Fatal("STRUCTURED_AUDIT_PATH")
	}
	if err := os.Mkdir(target, 0700); err != nil {
		t.Fatal("STRUCTURED_AUDIT_TARGET_EXISTS")
	}
	var seal financialExtractionSeal
	var cases []financialExtractionCase
	sr := externalRead(t, filepath.Join(source, "seal.json"), &seal)
	cr := externalRead(t, filepath.Join(source, "cases.json"), &cases)
	if d.ContentDigest(cr) != seal.CasesHash || len(cases) != 12 {
		t.Fatal("STRUCTURED_AUDIT_SOURCE")
	}
	semanticTrialWrite(t, target, "seal.json", seal)
	semanticTrialWrite(t, target, "cases.json", cases)
	audit := []map[string]any{}
	different := 0
	for _, c := range cases {
		var g structuredTransportGeneration
		gr := externalRead(t, filepath.Join(source, c.ID+"-generation.json"), &g)
		var attempt struct {
			At        time.Time `json:"at"`
			InputHash string    `json:"input_hash"`
			SealHash  string    `json:"seal_hash"`
		}
		ar := externalRead(t, filepath.Join(source, c.ID+"-attempt.json"), &attempt)
		var outcome struct {
			Elapsed        int64     `json:"elapsed_ms"`
			Code           string    `json:"code"`
			At             time.Time `json:"at"`
			GenerationHash string    `json:"generation_hash"`
		}
		or := externalRead(t, filepath.Join(source, c.ID+"-outcome.json"), &outcome)
		at, err := time.Parse(time.RFC3339Nano, g.CompletedAt)
		if err != nil || !d.UTC(at) || attempt.At.After(at) || at.After(outcome.At) || outcome.Code != "" || outcome.GenerationHash != d.Digest(g) || attempt.InputHash != d.ContentDigest([]byte(c.Input)) || attempt.SealHash != d.Digest(seal) || g.Provider != seal.Settings.Provider || g.Model != seal.Settings.Model || g.AccountID != 1 || g.PromptHash != seal.PromptHash {
			t.Fatal("STRUCTURED_AUDIT_RECORD_INVALID")
		}
		canonical := d.SemanticGeneration{Provider: g.Provider, AccountID: g.AccountID, Model: g.Model, PromptHash: g.PromptHash, Text: g.Text, CompletedAt: at}
		if d.Digest(canonical) != outcome.GenerationHash {
			different++
		}
		outcome.GenerationHash = d.Digest(canonical)
		semanticTrialWrite(t, target, c.ID+"-generation.json", canonical)
		semanticTrialWrite(t, target, c.ID+"-attempt.json", attempt)
		semanticTrialWrite(t, target, c.ID+"-outcome.json", outcome)
		audit = append(audit, map[string]any{"id": c.ID, "original_generation_bytes_hash": d.ContentDigest(gr), "original_attempt_bytes_hash": d.ContentDigest(ar), "original_outcome_bytes_hash": d.ContentDigest(or), "go_projection_generation_hash": outcome.GenerationHash, "response_hash": d.ContentDigest([]byte(g.Text))})
	}
	semanticTrialWrite(t, target, "audit.json", map[string]any{"at": time.Now().UTC(), "source": filepath.Base(source), "original_seal_bytes_hash": d.ContentDigest(sr), "original_cases_bytes_hash": d.ContentDigest(cr), "method": "VALIDATE_ORIGINAL_STRING_TIMESTAMP_HASH_THEN_SEPARATE_GO_TIME_PROJECTION", "timestamp_hash_differences": different, "model_calls": 0, "cases": audit})
	t.Logf("originals verified; timestamp hash differences=%d; separate audit projection, zero new calls", different)
}

func TestStructuredTimestampHashBoundary(t *testing.T) {
	var raw structuredTransportGeneration
	if json.Unmarshal([]byte(`{"provider":"fixture","account_id":1,"model":"fixture","prompt_hash":"fixture","text":"{}","completed_at":"2026-10-11T00:00:00.240Z"}`), &raw) != nil {
		t.Fatal("fixture")
	}
	at, err := time.Parse(time.RFC3339Nano, raw.CompletedAt)
	if err != nil {
		t.Fatal("time")
	}
	canonical := d.SemanticGeneration{Provider: raw.Provider, AccountID: raw.AccountID, Model: raw.Model, PromptHash: raw.PromptHash, Text: raw.Text, CompletedAt: at}
	if d.Digest(raw) == d.Digest(canonical) || raw.Text != canonical.Text || !at.Equal(canonical.CompletedAt) {
		t.Fatal("timestamp serialization boundary lost")
	}
	raw.CompletedAt = at.Format(time.RFC3339Nano)
	if d.Digest(raw) != d.Digest(canonical) {
		t.Fatal("canonical transport hash mismatch")
	}
}
