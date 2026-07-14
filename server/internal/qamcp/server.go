// Package qamcp is Agora's own QA MCP server — the deterministic test-runner
// tool surface every agent task gets for free (`agora mcp qa`, injected into
// each task's mcp_config by the daemon).
//
// Why: before LLMs, test cases WERE scripts run by a library (jest, vitest,
// playwright) — running them was a plain command with a deterministic exit
// code. This server gives agents that exact contract back as structured MCP
// tools: detect the repo's runner, run the suite (or one case script), write a
// test file into the repo. An agent composing ad-hoc shell + parsing raw
// output is slower and flakier than one tool call returning {exit_code,
// passed, output} — and every result the tool reports traces to a real
// process exit code, never an opinion.
//
// Transport: MCP stdio — newline-delimited JSON-RPC 2.0 over stdin/stdout.
// The server is deliberately dependency-free (no MCP SDK): the protocol
// subset agents need is initialize / tools/list / tools/call / ping.
package qamcp

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"sync"
)

const protocolVersion = "2024-11-05"

// jsonRPCRequest is one incoming newline-delimited JSON-RPC 2.0 message.
type jsonRPCRequest struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"` // absent/null = notification
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

type jsonRPCError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

type jsonRPCResponse struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Result  any             `json:"result,omitempty"`
	Error   *jsonRPCError   `json:"error,omitempty"`
}

// Server hosts the QA tool surface over MCP stdio.
type Server struct {
	version string
	logger  *slog.Logger

	mu  sync.Mutex // serializes writes to out
	out io.Writer
}

// New builds a Server. logger goes to stderr in practice — stdout is the
// protocol channel and must carry nothing but JSON-RPC lines.
func New(version string, logger *slog.Logger) *Server {
	return &Server{version: version, logger: logger}
}

// Serve reads newline-delimited JSON-RPC messages from in and answers on out
// until in closes. Malformed lines are logged and skipped — a broken client
// line must not kill the whole server mid-task.
func (s *Server) Serve(in io.Reader, out io.Writer) error {
	s.out = out
	sc := bufio.NewScanner(in)
	// Case scripts arrive inline as tool arguments — allow large lines (10MB).
	sc.Buffer(make([]byte, 0, 64*1024), 10*1024*1024)
	for sc.Scan() {
		line := sc.Bytes()
		if len(line) == 0 {
			continue
		}
		var req jsonRPCRequest
		if err := json.Unmarshal(line, &req); err != nil {
			s.logger.Warn("qamcp: skipping malformed line", "error", err)
			continue
		}
		s.handle(&req)
	}
	return sc.Err()
}

// handle dispatches one message. Notifications (no id) never get a response.
func (s *Server) handle(req *jsonRPCRequest) {
	isNotification := len(req.ID) == 0 || string(req.ID) == "null"
	switch req.Method {
	case "initialize":
		s.reply(req.ID, map[string]any{
			"protocolVersion": protocolVersion,
			"capabilities":    map[string]any{"tools": map[string]any{}},
			"serverInfo":      map[string]any{"name": "agora-qa", "version": s.version},
		})
	case "notifications/initialized", "notifications/cancelled":
		// Client lifecycle notifications — nothing to do.
	case "ping":
		s.reply(req.ID, map[string]any{})
	case "tools/list":
		s.reply(req.ID, map[string]any{"tools": toolDefinitions()})
	case "tools/call":
		var params struct {
			Name      string          `json:"name"`
			Arguments json.RawMessage `json:"arguments"`
		}
		if err := json.Unmarshal(req.Params, &params); err != nil {
			s.replyError(req.ID, -32602, "invalid tools/call params: "+err.Error())
			return
		}
		result, callErr := dispatchTool(params.Name, params.Arguments, s.logger)
		if callErr != nil {
			// Tool-level failure: MCP wants a RESULT with isError, not a
			// protocol error — the agent should read the failure text.
			s.reply(req.ID, toolResult(fmt.Sprintf(`{"error":%q}`, callErr.Error()), true))
			return
		}
		s.reply(req.ID, toolResult(result, false))
	default:
		if !isNotification {
			s.replyError(req.ID, -32601, "method not found: "+req.Method)
		}
	}
}

// toolResult wraps a JSON payload string in the MCP tools/call result shape.
func toolResult(jsonText string, isError bool) map[string]any {
	return map[string]any{
		"content": []map[string]any{{"type": "text", "text": jsonText}},
		"isError": isError,
	}
}

func (s *Server) reply(id json.RawMessage, result any) {
	if len(id) == 0 || string(id) == "null" {
		return // never answer a notification
	}
	s.write(jsonRPCResponse{JSONRPC: "2.0", ID: id, Result: result})
}

func (s *Server) replyError(id json.RawMessage, code int, msg string) {
	if len(id) == 0 || string(id) == "null" {
		return
	}
	s.write(jsonRPCResponse{JSONRPC: "2.0", ID: id, Error: &jsonRPCError{Code: code, Message: msg}})
}

func (s *Server) write(resp jsonRPCResponse) {
	s.mu.Lock()
	defer s.mu.Unlock()
	b, err := json.Marshal(resp)
	if err != nil {
		s.logger.Error("qamcp: marshal response failed", "error", err)
		return
	}
	b = append(b, '\n')
	if _, err := s.out.Write(b); err != nil {
		s.logger.Error("qamcp: write response failed", "error", err)
	}
}
