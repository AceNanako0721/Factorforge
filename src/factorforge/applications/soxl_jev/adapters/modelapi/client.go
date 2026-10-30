// Package modelapi calls one OpenAI-compatible HTTPS endpoint for a single
// text generation. It has no login, account pool, tool loop or subprocess.
package modelapi

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"path/filepath"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/AceNanako0721/Factorforge/src/factorforge/applications/soxl_jev/config"
	d "github.com/AceNanako0721/Factorforge/src/factorforge/applications/soxl_jev/domain"
)

// systemPrompt is the fixed boundary text. The business instructions are owned
// by P3 and travel inside the user text.
const systemPrompt = "Answer with exactly one JSON object. Do not call tools, do not ask questions and do not add prose outside the JSON."

var requestID = regexp.MustCompile(`^[A-Za-z0-9_-]{1,128}$`)

type Client struct {
	Root           string
	Access         config.ModelAccess
	ModelID        string
	TimeoutSeconds int64
	HTTP           *http.Client
	Now            func() time.Time
}

type message struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type thinking struct {
	Type string `json:"type"`
}

type request struct {
	Model          string    `json:"model"`
	Messages       []message `json:"messages"`
	ResponseFormat struct {
		Type string `json:"type"`
	} `json:"response_format"`
	MaxTokens       int       `json:"max_tokens"`
	Thinking        *thinking `json:"thinking,omitempty"`
	ReasoningEffort string    `json:"reasoning_effort,omitempty"`
	Stream          bool      `json:"stream"`
}

type envelope struct {
	Choices []struct {
		FinishReason string `json:"finish_reason"`
		Message      struct {
			Content          string `json:"content"`
			ReasoningContent string `json:"reasoning_content"`
		} `json:"message"`
	} `json:"choices"`
	Usage struct {
		PromptTokens     int `json:"prompt_tokens"`
		CompletionTokens int `json:"completion_tokens"`
		TotalTokens      int `json:"total_tokens"`
	} `json:"usage"`
}

// Generate sends exactly one request. It never retries and never switches
// model or endpoint.
func (c Client) Generate(parent context.Context, id, input string) (d.SemanticGeneration, error) {
	var result d.SemanticGeneration
	now := c.now()
	if !c.Access.Enabled || !requestID.MatchString(id) || strings.TrimSpace(input) == "" || !utf8.ValidString(input) ||
		len(input) > c.Access.MaxInputBytes || c.Access.MaxOutputBytes <= 0 || c.TimeoutSeconds <= 0 || c.ModelID == "" {
		return result, d.Fail("SEMANTIC_INPUT_INVALID", 422)
	}
	statePath := c.statePath()
	if err := Reserve(statePath, id, c.Access, now); err != nil {
		return result, err
	}
	payload := c.request(input)
	body, err := json.Marshal(payload)
	if err != nil || len(body) > c.Access.MaxInputBytes {
		return result, d.Fail("SEMANTIC_INPUT_INVALID", 422)
	}
	timeout := time.Duration(c.TimeoutSeconds) * time.Second
	ctx, cancel := context.WithTimeout(parent, timeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.Access.Endpoint, bytes.NewReader(body))
	if err != nil {
		return result, d.Fail("MODEL_CONFIGURATION_INVALID", 503)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+c.Access.APIKey)
	client := c.HTTP
	if client == nil {
		client = &http.Client{}
	}
	resp, err := client.Do(req)
	if err != nil {
		return result, d.Fail("DELIVERY_UNKNOWN", 503)
	}
	defer resp.Body.Close()
	raw, readErr := io.ReadAll(io.LimitReader(resp.Body, int64(c.Access.MaxOutputBytes)+1))
	if readErr != nil || len(raw) > c.Access.MaxOutputBytes {
		return result, d.Fail("PROTOCOL_INVALID", 503)
	}
	if resp.StatusCode != http.StatusOK {
		return result, statusError(resp.StatusCode)
	}
	var reply envelope
	if json.Unmarshal(raw, &reply) != nil || len(reply.Choices) == 0 {
		return result, d.Fail("PROTOCOL_INVALID", 503)
	}
	choice := reply.Choices[0]
	switch choice.FinishReason {
	case "stop":
	case "length":
		return result, d.Fail("INCOMPLETE_RESPONSE", 503)
	case "tool_calls", "tool_use":
		return result, d.Fail("UNSUPPORTED_OUTPUT", 503)
	default:
		return result, d.Fail("DELIVERY_UNKNOWN", 503)
	}
	text := strings.TrimSpace(choice.Message.Content)
	if text == "" {
		return result, d.Fail("EMPTY_RESPONSE", 503)
	}
	if len(text) > c.Access.MaxOutputBytes {
		return result, d.Fail("INCOMPLETE_RESPONSE", 503)
	}
	sum := sha256.Sum256([]byte(input))
	return d.SemanticGeneration{
		Provider:    c.Access.Channel,
		Model:       c.ModelID,
		PromptHash:  hex.EncodeToString(sum[:]),
		Text:        text,
		CompletedAt: now,
	}, nil
}

func (c Client) request(input string) request {
	var payload request
	payload.Model = c.ModelID
	payload.Messages = []message{{Role: "system", Content: systemPrompt}, {Role: "user", Content: input}}
	payload.ResponseFormat.Type = "json_object"
	payload.MaxTokens = c.Access.MaxTokens
	payload.Stream = false
	if c.Access.Thinking {
		payload.Thinking = &thinking{Type: "enabled"}
		payload.ReasoningEffort = c.Access.ReasoningEffort
	} else {
		payload.Thinking = &thinking{Type: "disabled"}
	}
	return payload
}

func (c Client) statePath() string {
	if c.Access.StatePath != "" {
		return c.Access.StatePath
	}
	if c.Root == "" {
		return ""
	}
	return filepath.Join(c.Root, "runtime", "model-state.json")
}

func (c Client) now() time.Time {
	if c.Now != nil {
		return c.Now().UTC()
	}
	return time.Now().UTC()
}

func statusError(code int) error {
	switch {
	case code == http.StatusUnauthorized || code == http.StatusForbidden:
		return d.Fail("MODEL_AUTH_FAILED", 503)
	case code == http.StatusTooManyRequests:
		return d.Fail("MODEL_RATE_LIMITED", 503)
	default:
		return d.Fail("MODEL_ACCESS_FAILED", 503)
	}
}
