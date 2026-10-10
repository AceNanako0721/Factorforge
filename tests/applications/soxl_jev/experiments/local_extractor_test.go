package experiments_test

import (
	"crypto/sha1"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	d "github.com/AceNanako0721/Factorforge/src/factorforge/applications/soxl_jev/domain"
)

const localCorpusRevision = "6a9e8f9fc11ba7db3d8dffe3e5df9e7b062e6441"
const localCorpusBlob = "d6d8917e1d9c0a3ee5ac1a894927d5deb75ec369"

func localExtractorLab(t *testing.T) string {
	t.Helper()
	lab := os.Getenv("FACTORFORGE_LOCAL_EXTRACTOR_LAB")
	if lab == "" {
		t.Skip("explicit isolated local model method experiment")
	}
	if !filepath.IsAbs(lab) || filepath.Base(filepath.Dir(lab)) != "runtime" {
		t.Fatal("LOCAL_EXTRACTOR_PATH")
	}
	return lab
}

func TestFreezeLocalExtractionProtocolBeforeCorpus(t *testing.T) {
	lab := localExtractorLab(t)
	for _, name := range []string{"corpus.jsonl", "corpus-access-started.json", "local-calls-started.json"} {
		if _, err := os.Lstat(filepath.Join(lab, name)); !os.IsNotExist(err) {
			t.Fatal("LOCAL_EXTRACTION_ALREADY_ACCESSED")
		}
	}
	prompt, err := os.ReadFile(filepath.Join(lab, "prompt.json"))
	if err != nil || !json.Valid(prompt) || len(prompt) > 65536 {
		t.Fatal("LOCAL_PROMPT_REQUIRED")
	}
	schema, err := os.ReadFile(filepath.Join(lab, "metadata-label_schema.json"))
	if err != nil || !json.Valid(schema) {
		t.Fatal("LOCAL_LABEL_SCHEMA_REQUIRED")
	}
	frozenRateWrite(t, lab, "frozen-protocol.json", map[string]any{
		"at": time.Now().UTC(), "corpus_revision": localCorpusRevision, "corpus_git_blob": localCorpusBlob,
		"corpus_size": 1280841, "corpus_split": "test", "documents": 4, "selection": "SHA256(id), full text 1000..6500 UTF8 bytes and 100..1000 original tokens; no gold filter",
		"exclude_ids": []string{"doc-1:sentivent_unified_document"}, "prompt_hash": d.ContentDigest(prompt), "label_schema_hash": d.ContentDigest(schema),
		"model_revision": "bc640142c66e1fdd12af0bd68f40445458f3869b", "model_sha256": "7485fe6f11af29433bc51cab58009521f205840f5b4ae3a32fa7f92e8534fdf5",
		"llama_commit": "d81235049384534c167caea52b85a694f6103d14", "llama_release": "b11429", "max_calls": 4, "request_bytes": 65536, "response_bytes": 262144, "max_output_tokens": 4096, "timeout_seconds": 600,
		"runtime":      "loopback only, one slot, 4 CPU threads/6 batch threads, context 16384, no context shift, no tools, ephemeral process",
		"settings":     "temperature 0.7, top_p 0.8, top_k 20, min_p 0, presence_penalty 1.5, seed 20261010, enable_thinking false; exact schema/settings sealed inside prompt.json",
		"metrics":      "exact multiset typed trigger tokens, parent-bound role/kind argument tokens, negation/modality where gold known; no official aggregate score",
		"unknown_gold": "retain in denominator or separately mark unscorable; no silent exclusion", "independent_human_annotations": true, "unseen_to_model": false,
		"gold_accessed": false, "model_calls": 0, "production_installed": false, "orders": 0,
	})
}

func TestCaptureFrozenLocalExtractionCorpus(t *testing.T) {
	lab := localExtractorLab(t)
	if _, err := os.Stat(filepath.Join(lab, "frozen-protocol.json")); err != nil {
		t.Fatal("LOCAL_PROTOCOL_REQUIRED")
	}
	frozenRateWrite(t, lab, "corpus-access-started.json", map[string]any{"at": time.Now().UTC(), "maximum_gets": 1, "no_retry": true})
	client := &http.Client{Timeout: 30 * time.Second}
	response, err := client.Get("https://huggingface.co/datasets/GillesJacobs/sentivent/resolve/" + localCorpusRevision + "/data/sentivent_unified_document/test.jsonl")
	if err != nil {
		t.Fatal("LOCAL_CORPUS_DELIVERY_UNKNOWN")
	}
	defer response.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(response.Body, 1280842))
	git := sha1.New()
	fmt.Fprintf(git, "blob %d\x00", len(raw))
	git.Write(raw)
	if err != nil || response.StatusCode != 200 || len(raw) != 1280841 || hex.EncodeToString(git.Sum(nil)) != localCorpusBlob {
		t.Fatal("LOCAL_CORPUS_INTEGRITY")
	}
	f, err := os.OpenFile(filepath.Join(lab, "corpus.jsonl"), os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		t.Fatal("LOCAL_CORPUS_EXISTS")
	}
	_, err = f.Write(raw)
	closed := f.Close()
	if err != nil || closed != nil {
		t.Fatal("LOCAL_CORPUS_WRITE")
	}
	frozenRateWrite(t, lab, "corpus-provenance.json", map[string]any{"at": time.Now().UTC(), "sha256": d.ContentDigest(raw), "git_blob": localCorpusBlob, "bytes": len(raw), "http": response.StatusCode, "model_calls": 0})
}

type localCorpusRow struct {
	ID          string            `json:"id"`
	Split       string            `json:"split"`
	Text        string            `json:"text"`
	Tokens      []string          `json:"tokens"`
	Annotations []json.RawMessage `json:"annotations"`
}

// Selection uses input metadata only. Targets remain in the private labels
// file, never in the local server's request or token candidate list.
func TestPrepareFrozenLocalDocumentInputs(t *testing.T) {
	lab := localExtractorLab(t)
	var protocol struct {
		PromptHash string `json:"prompt_hash"`
	}
	financialRead(t, lab, "frozen-protocol.json", &protocol)
	prompt, err := os.ReadFile(filepath.Join(lab, "prompt.json"))
	if err != nil || d.ContentDigest(prompt) != protocol.PromptHash {
		t.Fatal("LOCAL_PROMPT_CHANGED")
	}
	raw, err := os.ReadFile(filepath.Join(lab, "corpus.jsonl"))
	if err != nil {
		t.Fatal("LOCAL_CORPUS_REQUIRED")
	}
	rows := []localCorpusRow{}
	count := 0
	for _, line := range strings.Split(strings.TrimSpace(string(raw)), "\n") {
		var row localCorpusRow
		if json.Unmarshal([]byte(line), &row) != nil || row.ID == "" || row.Split != "test" || !utf8.ValidString(row.Text) {
			t.Fatal("LOCAL_CORPUS_SHAPE")
		}
		count++
		if row.ID != "doc-1:sentivent_unified_document" && len(row.Text) >= 1000 && len(row.Text) <= 6500 && len(row.Tokens) >= 100 && len(row.Tokens) <= 1000 {
			rows = append(rows, row)
		}
	}
	if count != 30 || len(rows) < 4 {
		t.Fatal("LOCAL_CORPUS_SAMPLE_INSUFFICIENT")
	}
	sort.Slice(rows, func(i, j int) bool { return d.ContentDigest([]byte(rows[i].ID)) < d.ContentDigest([]byte(rows[j].ID)) })
	labels := rows[:4]
	for i, row := range labels {
		frozenRateWrite(t, lab, fmt.Sprintf("input-%02d.json", i+1), map[string]any{"id": row.ID, "text": row.Text, "tokens": row.Tokens})
	}
	frozenRateWrite(t, lab, "labels.json", map[string]any{"rows": labels, "corpus_count": count, "eligible_count": len(rows), "all_selected_documents_consumed_development": true})
	frozenRateWrite(t, lab, "sealed-inputs.json", map[string]any{"at": time.Now().UTC(), "prompt_hash": d.ContentDigest(prompt), "corpus_hash": d.ContentDigest(raw), "selected_ids": []string{labels[0].ID, labels[1].ID, labels[2].ID, labels[3].ID}, "model_calls": 0, "production_installed": false})
	t.Logf("%d full test documents eligible; four SHA-selected; no annotations in input files; zero model calls", len(rows))
}
