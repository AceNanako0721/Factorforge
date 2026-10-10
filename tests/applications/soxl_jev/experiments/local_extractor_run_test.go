package experiments_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	d "github.com/AceNanako0721/Factorforge/src/factorforge/applications/soxl_jev/domain"
)

type localDocumentInput struct {
	ID     string   `json:"id"`
	Text   string   `json:"text"`
	Tokens []string `json:"tokens"`
}

type localPromptAsset struct {
	Instructions string          `json:"instructions"`
	Schema       json.RawMessage `json:"schema"`
	Settings     map[string]any  `json:"settings"`
}

type localRequestSeal struct {
	ProtocolHash  string   `json:"protocol_hash"`
	PromptHash    string   `json:"prompt_hash"`
	InputHashes   []string `json:"input_hashes"`
	RequestHashes []string `json:"request_hashes"`
}

// Read only the annotation-free inputs. All four requests are immutable before
// the server starts; no gold file is opened by preparation or delivery.
func TestPrepareFrozenLocalExtractionRequests(t *testing.T) {
	lab := localExtractorLab(t)
	var protocol struct {
		PromptHash string `json:"prompt_hash"`
	}
	financialRead(t, lab, "frozen-protocol.json", &protocol)
	promptRaw, err := os.ReadFile(filepath.Join(lab, "prompt.json"))
	if err != nil || d.ContentDigest(promptRaw) != protocol.PromptHash {
		t.Fatal("LOCAL_PROMPT_CHANGED")
	}
	var prompt localPromptAsset
	if json.Unmarshal(promptRaw, &prompt) != nil || prompt.Instructions == "" || !json.Valid(prompt.Schema) {
		t.Fatal("LOCAL_PROMPT_SHAPE")
	}
	protocolRaw, err := os.ReadFile(filepath.Join(lab, "frozen-protocol.json"))
	if err != nil {
		t.Fatal("LOCAL_PROTOCOL_REQUIRED")
	}
	seal := localRequestSeal{ProtocolHash: d.ContentDigest(protocolRaw), PromptHash: d.ContentDigest(promptRaw)}
	for i := 1; i <= 4; i++ {
		raw, err := os.ReadFile(filepath.Join(lab, fmt.Sprintf("input-%02d.json", i)))
		var input localDocumentInput
		if err != nil || localStrictDecode(raw, &input) != nil || input.ID == "" || len(input.Tokens) == 0 {
			t.Fatal("LOCAL_INPUT_SHAPE")
		}
		numbered := make([]struct {
			ID   int    `json:"id"`
			Text string `json:"text"`
		}, len(input.Tokens))
		for j, token := range input.Tokens {
			numbered[j].ID = j
			numbered[j].Text = token
		}
		user, err := json.Marshal(map[string]any{"document_id": input.ID, "document": input.Text, "numbered_tokens": numbered})
		if err != nil {
			t.Fatal("LOCAL_INPUT_ENCODING")
		}
		request := map[string]any{}
		for k, v := range prompt.Settings {
			request[k] = v
		}
		request["messages"] = []map[string]string{{"role": "system", "content": prompt.Instructions}, {"role": "user", "content": string(user)}}
		request["response_format"] = map[string]any{"type": "json_object", "schema": prompt.Schema}
		requestRaw, err := json.MarshalIndent(request, "", "  ")
		requestRaw = append(requestRaw, '\n')
		if err != nil || len(requestRaw) > 65536 {
			t.Fatal("LOCAL_REQUEST_BOUND")
		}
		localWriteRaw(t, lab, fmt.Sprintf("request-%02d.json", i), requestRaw)
		seal.InputHashes = append(seal.InputHashes, d.ContentDigest(raw))
		seal.RequestHashes = append(seal.RequestHashes, d.ContentDigest(requestRaw))
	}
	frozenRateWrite(t, lab, "requests-seal.json", seal)
	t.Log("four annotation-free requests frozen; zero model calls")
}

type localCallReceipt struct {
	Index         int       `json:"index"`
	StartedAt     time.Time `json:"started_at"`
	ElapsedMillis int64     `json:"elapsed_millis"`
	HTTP          int       `json:"http"`
	RequestHash   string    `json:"request_hash"`
	ResponseHash  string    `json:"response_hash"`
	Error         string    `json:"error,omitempty"`
}

// A disposable loopback process uses the pinned official native binary. No
// service installation, global configuration, Python, external model API or
// downstream framework write occurs in this experiment.
func TestRunFrozenLocalExtractionBatch(t *testing.T) {
	lab := localExtractorLab(t)
	var seal localRequestSeal
	financialRead(t, lab, "requests-seal.json", &seal)
	if len(seal.RequestHashes) != 4 || len(seal.InputHashes) != 4 {
		t.Fatal("LOCAL_SEAL_SHAPE")
	}
	for _, item := range []struct{ Name, Hash string }{{"frozen-protocol.json", seal.ProtocolHash}, {"prompt.json", seal.PromptHash}} {
		raw, err := os.ReadFile(filepath.Join(lab, item.Name))
		if err != nil || d.ContentDigest(raw) != item.Hash {
			t.Fatal("LOCAL_ASSET_CHANGED")
		}
	}
	requests := make([][]byte, 4)
	for i := range requests {
		raw, err := os.ReadFile(filepath.Join(lab, fmt.Sprintf("request-%02d.json", i+1)))
		input, ie := os.ReadFile(filepath.Join(lab, fmt.Sprintf("input-%02d.json", i+1)))
		if err != nil || ie != nil || len(raw) > 65536 || d.ContentDigest(raw) != seal.RequestHashes[i] || d.ContentDigest(input) != seal.InputHashes[i] {
			t.Fatal("LOCAL_REQUEST_CHANGED")
		}
		requests[i] = raw
	}
	for _, asset := range []struct{ Name, Hash string }{{"model.gguf", "7485fe6f11af29433bc51cab58009521f205840f5b4ae3a32fa7f92e8534fdf5"}, {"llama-cpu.tar.gz", "f6d25dde8f51133143d1453da4fd5f73b145127177612a283bf7995957af3392"}} {
		f, err := os.Open(filepath.Join(lab, asset.Name))
		if err != nil {
			t.Fatal("LOCAL_RUNTIME_ASSET_REQUIRED")
		}
		h := sha256.New()
		_, copied := io.Copy(h, f)
		closed := f.Close()
		if copied != nil || closed != nil || hex.EncodeToString(h.Sum(nil)) != asset.Hash {
			t.Fatal("LOCAL_RUNTIME_INTEGRITY")
		}
	}
	evaluationSeal, err := os.ReadFile(filepath.Join(lab, "evaluation-seal.json"))
	if err != nil || !json.Valid(evaluationSeal) {
		t.Fatal("LOCAL_EVALUATION_SEAL_REQUIRED")
	}
	frozenRateWrite(t, lab, "local-calls-started.json", map[string]any{"at": time.Now().UTC(), "requests_sealed": true, "evaluation_seal_hash": d.ContentDigest(evaluationSeal), "maximum_calls": 4, "retry": false, "orders": 0})
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal("LOCAL_PORT")
	}
	port := listener.Addr().(*net.TCPAddr).Port
	if listener.Close() != nil {
		t.Fatal("LOCAL_PORT_RELEASE")
	}
	log, err := os.OpenFile(filepath.Join(lab, "server.log"), os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		t.Fatal("LOCAL_LOG_EXISTS")
	}
	defer log.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Minute)
	defer cancel()
	command := exec.CommandContext(ctx, "nice", "-n", "10", filepath.Join(lab, "llama-cpu", "llama-server"), "--model", filepath.Join(lab, "model.gguf"), "--host", "127.0.0.1", "--port", fmt.Sprint(port), "--alias", "qwen3-4b-q4km-lab", "--threads", "4", "--threads-batch", "6", "--parallel", "1", "--ctx-size", "16384", "--no-context-shift", "--no-webui", "--jinja")
	command.Stdout = log
	command.Stderr = log
	if command.Start() != nil {
		t.Fatal("LOCAL_SERVER_START")
	}
	defer func() { _ = command.Process.Kill(); _ = command.Wait() }()
	base := fmt.Sprintf("http://127.0.0.1:%d", port)
	client := &http.Client{Timeout: 600 * time.Second, Transport: &http.Transport{Proxy: nil}, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	healthClient := &http.Client{Timeout: time.Second, Transport: &http.Transport{Proxy: nil}}
	ready := false
	for start := time.Now(); time.Since(start) < 120*time.Second; {
		r, e := healthClient.Get(base + "/health")
		if e == nil {
			_ = r.Body.Close()
			if r.StatusCode == 200 {
				ready = true
				break
			}
		}
		time.Sleep(time.Second)
	}
	if !ready {
		frozenRateWrite(t, lab, "batch-failed.json", map[string]any{"reason": "LOCAL_SERVER_NOT_READY", "calls": 0})
		t.Fatal("LOCAL_SERVER_NOT_READY")
	}
	receipts := []localCallReceipt{}
	for i, raw := range requests {
		receipt := localCallReceipt{Index: i + 1, StartedAt: time.Now().UTC(), RequestHash: seal.RequestHashes[i]}
		callCtx, stop := context.WithTimeout(ctx, 600*time.Second)
		req, e := http.NewRequestWithContext(callCtx, http.MethodPost, base+"/v1/chat/completions", bytes.NewReader(raw))
		if e != nil {
			stop()
			t.Fatal("LOCAL_REQUEST_BUILD")
		}
		req.Header.Set("Content-Type", "application/json")
		response, e := client.Do(req)
		var body []byte
		if e != nil {
			receipt.Error = "DELIVERY_UNKNOWN"
		} else {
			receipt.HTTP = response.StatusCode
			body, e = io.ReadAll(io.LimitReader(response.Body, 262145))
			_ = response.Body.Close()
			if e != nil {
				receipt.Error = "RESPONSE_INCOMPLETE"
			} else if len(body) > 262144 {
				receipt.Error = "RESPONSE_BOUND"
			} else if receipt.HTTP != 200 {
				receipt.Error = "HTTP_REJECTED"
			}
		}
		stop()
		receipt.ElapsedMillis = time.Since(receipt.StartedAt).Milliseconds()
		receipt.ResponseHash = d.ContentDigest(body)
		localWriteRaw(t, lab, fmt.Sprintf("response-%02d.json", i+1), body)
		frozenRateWrite(t, lab, fmt.Sprintf("receipt-%02d.json", i+1), receipt)
		receipts = append(receipts, receipt)
		t.Logf("local document %d: HTTP %d, %dms, %s; no retry", i+1, receipt.HTTP, receipt.ElapsedMillis, receipt.Error)
	}
	frozenRateWrite(t, lab, "delivery.json", map[string]any{"receipts": receipts, "external_model_requests": 0, "orders": 0, "production_installed": false})
}

func localWriteRaw(t *testing.T, lab, name string, raw []byte) {
	t.Helper()
	f, err := os.OpenFile(filepath.Join(lab, name), os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		t.Fatal("LOCAL_ARTIFACT_EXISTS")
	}
	_, written := f.Write(raw)
	closed := f.Close()
	if written != nil || closed != nil {
		t.Fatal("LOCAL_ARTIFACT_WRITE")
	}
}

func localStrictDecode(raw []byte, out any) error {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(out); err != nil {
		return err
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		return fmt.Errorf("LOCAL_TRAILING_JSON")
	}
	return nil
}

// The private prompt cannot accidentally become part of ordinary test output.
func TestLocalInputRejectsGoldFields(t *testing.T) {
	var input localDocumentInput
	if localStrictDecode([]byte(`{"id":"synthetic","text":"example","tokens":["example"],"annotations":[]}`), &input) == nil {
		t.Fatal("gold field accepted")
	}
	if localStrictDecode([]byte(`{"id":"synthetic","text":"example","tokens":["example"]} {}`), &input) == nil {
		t.Fatal("trailing payload accepted")
	}
	if localStrictDecode([]byte(strings.TrimSpace(`{"id":"synthetic","text":"example","tokens":["example"]}`)), &input) != nil {
		t.Fatal("valid isolated input rejected")
	}
}
