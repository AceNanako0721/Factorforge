// Package mcp serves the operations agent's tools over the Model Context
// Protocol (JSON-RPC 2.0, one message per line on stdio). Only the tools of
// the profile the process was started with exist (S6-009).
package mcp

import (
	"bufio"
	"context"
	"encoding/json"
	"io"
	"strings"
	"sync"
	"unicode/utf8"

	d "github.com/AceNanako0721/Factorforge/src/factorforge/operations/agent/domain"
)

// ProtocolVersion is the MCP revision the server speaks.
const ProtocolVersion = "2025-06-18"

// MaxResultBytes bounds one tool result; a larger result is refused so the
// caller narrows the request.
const MaxResultBytes = 256 * 1024

// Tier of a tool (S6-002).
const (
	Observe = "OBSERVE"
	Record  = "RECORD"
)

// Tool is one registered tool.
type Tool struct {
	Name        string
	Tier        string
	Profiles    []string
	Description string
	Schema      map[string]any
	Call        func(ctx context.Context, args json.RawMessage) (any, error)
}

// Server is one MCP session.
type Server struct {
	Name, Version, Profile string
	tools                  map[string]Tool
	order                  []string
	mu                     sync.Mutex
}

// NewServer keeps only the tools registered for the profile.
func NewServer(name, version, profile string, tools []Tool) *Server {
	s := &Server{Name: name, Version: version, Profile: profile, tools: map[string]Tool{}}
	for _, t := range tools {
		if d.Has(t.Profiles, profile) {
			s.tools[t.Name] = t
			s.order = append(s.order, t.Name)
		}
	}
	return s
}

// Tools lists the names this session offers.
func (s *Server) Tools() []string { return append([]string(nil), s.order...) }

type request struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

type response struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Result  any             `json:"result,omitempty"`
	Error   *rpcError       `json:"error,omitempty"`
}

// Serve reads requests from in and writes responses to out until in ends.
// Requests are handled one at a time.
func (s *Server) Serve(ctx context.Context, in io.Reader, out io.Writer) error {
	sc := bufio.NewScanner(in)
	sc.Buffer(make([]byte, 64*1024), 4<<20)
	w := bufio.NewWriter(out)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" {
			continue
		}
		resp, ok := s.Handle(ctx, []byte(line))
		if !ok {
			continue
		}
		b, _ := json.Marshal(resp)
		w.Write(b)
		w.WriteByte('\n')
		if err := w.Flush(); err != nil {
			return err
		}
	}
	return sc.Err()
}

// Handle answers one message; ok is false for notifications.
func (s *Server) Handle(ctx context.Context, raw []byte) (response, bool) {
	var r request
	if err := json.Unmarshal(raw, &r); err != nil || r.JSONRPC != "2.0" || r.Method == "" {
		return response{JSONRPC: "2.0", ID: json.RawMessage("null"), Error: &rpcError{-32600, "invalid request"}}, true
	}
	if len(r.ID) == 0 {
		return response{}, false // notifications/initialized and the like
	}
	res := response{JSONRPC: "2.0", ID: r.ID}
	switch r.Method {
	case "initialize":
		res.Result = map[string]any{
			"protocolVersion": ProtocolVersion,
			"capabilities":    map[string]any{"tools": map[string]any{"listChanged": false}},
			"serverInfo":      map[string]any{"name": s.Name, "version": s.Version},
			"instructions":    "Factorforge 运维代理（" + s.Profile + " 档）。工具只读取各层公开接口和运维记录；改变记录的工具都会留审计。",
		}
	case "ping":
		res.Result = map[string]any{}
	case "tools/list":
		list := make([]map[string]any, 0, len(s.order))
		for _, n := range s.order {
			t := s.tools[n]
			list = append(list, map[string]any{"name": t.Name, "description": t.Description, "inputSchema": t.Schema,
				"annotations": map[string]any{"readOnlyHint": t.Tier == Observe, "destructiveHint": false, "idempotentHint": t.Tier == Observe, "openWorldHint": false}})
		}
		res.Result = map[string]any{"tools": list}
	case "tools/call":
		var p struct {
			Name      string          `json:"name"`
			Arguments json.RawMessage `json:"arguments"`
		}
		if json.Unmarshal(r.Params, &p) != nil {
			res.Error = &rpcError{-32602, "invalid params"}
			break
		}
		t, ok := s.tools[p.Name]
		if !ok {
			res.Result = toolError("TOOL_NOT_IN_PROFILE")
			break
		}
		if len(p.Arguments) == 0 || string(p.Arguments) == "null" {
			p.Arguments = json.RawMessage("{}")
		}
		s.mu.Lock()
		out, err := t.Call(ctx, p.Arguments)
		s.mu.Unlock()
		if err != nil {
			res.Result = toolError(d.Code(err))
			break
		}
		b, err := json.Marshal(out)
		if err != nil {
			res.Result = toolError("RESULT_INVALID")
			break
		}
		if len(b) > MaxResultBytes {
			res.Result = toolError("RESULT_TOO_LARGE")
			break
		}
		text := string(b)
		if !utf8.ValidString(text) {
			res.Result = toolError("RESULT_INVALID")
			break
		}
		result := map[string]any{"content": []map[string]any{{"type": "text", "text": text}}, "isError": false}
		var obj map[string]any
		if json.Unmarshal(b, &obj) == nil {
			result["structuredContent"] = obj
		}
		res.Result = result
	default:
		res.Error = &rpcError{-32601, "method not found"}
	}
	return res, true
}

func toolError(code string) map[string]any {
	return map[string]any{"content": []map[string]any{{"type": "text", "text": `{"error":"` + code + `"}`}}, "isError": true}
}
