package mcpclient

import (
	"bufio"
	"context"
	"encoding/json"
	"io"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/kaeawc/mcpcheck/internal/mcpmodel"
)

// handler scripts a fake server: given one client message, it returns
// the raw lines the server writes back (which may be noise or invalid).
type handler func(m Message) []string

func result(id json.RawMessage, v any) string {
	raw, _ := json.Marshal(v)
	out, _ := json.Marshal(Message{JSONRPC: "2.0", ID: id, Result: raw})
	return string(out)
}

func rpcError(id json.RawMessage, code int, msg string) string {
	out, _ := json.Marshal(Message{JSONRPC: "2.0", ID: id, Error: &RPCError{Code: code, Message: msg}})
	return string(out)
}

func notification(method string) string {
	out, _ := json.Marshal(Message{JSONRPC: "2.0", Method: method})
	return string(out)
}

var weatherTool = map[string]any{
	"name":        "get_weather",
	"title":       "Weather",
	"description": "Get the current weather for a city.",
	"inputSchema": map[string]any{
		"type":       "object",
		"properties": map[string]any{"city": map[string]any{"type": "string"}},
		"required":   []any{"city"},
	},
	"outputSchema": map[string]any{"type": "object"},
	"annotations":  map[string]any{"readOnlyHint": true},
}

func initResult(version string) map[string]any {
	return map[string]any{
		"protocolVersion": version,
		"capabilities":    map[string]any{"tools": map[string]any{}},
		"serverInfo":      map[string]any{"name": "fake", "version": "1.2.3"},
	}
}

// standard answers initialize with the offered version and tools/list
// with tools, and records every message it receives.
type standard struct {
	mu    sync.Mutex
	tools []map[string]any
	got   []Message
}

func (s *standard) handle(m Message) []string {
	s.mu.Lock()
	s.got = append(s.got, m)
	s.mu.Unlock()
	switch m.Method {
	case "initialize":
		var p struct {
			ProtocolVersion string `json:"protocolVersion"`
		}
		_ = json.Unmarshal(m.Params, &p)
		return []string{result(m.ID, initResult(p.ProtocolVersion))}
	case "tools/list":
		return []string{result(m.ID, map[string]any{"tools": s.tools})}
	}
	if m.IsRequest() {
		return []string{rpcError(m.ID, -32601, "Method not found")}
	}
	return nil
}

func (s *standard) received() []Message {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]Message(nil), s.got...)
}

// runStdio connects FetchTools to h over in-memory pipes.
func runStdio(t *testing.T, timeout time.Duration, h handler) (*mcpmodel.ToolSet, *Recorder, error) {
	t.Helper()
	c2sR, c2sW := io.Pipe()
	s2cR, s2cW := io.Pipe()
	ctx, cancel := contextWithTimeout(timeout)
	defer cancel()
	go serveLines(c2sR, s2cW, h)
	rec := NewRecorder(nil)
	set, err := FetchTools(ctx, NewStdio(c2sW, s2cR, rec), rec, "fake-server", Options{})
	_ = c2sW.Close()
	return set, rec, err
}

// serveLines runs h as a newline-delimited server. Like real legacy
// SDKs, an unanswered server/discover gets "method not found".
func serveLines(in io.Reader, out *io.PipeWriter, h handler) {
	defer out.Close()
	br := bufio.NewReader(in)
	for {
		line, err := br.ReadBytes('\n')
		if err != nil {
			return
		}
		var m Message
		if json.Unmarshal(line, &m) != nil {
			continue
		}
		replies := h(m)
		if len(replies) == 0 && m.Method == "server/discover" {
			replies = []string{rpcError(m.ID, -32601, "Method not found")}
		}
		for _, r := range replies {
			if _, err := io.WriteString(out, r+"\n"); err != nil {
				return
			}
		}
	}
}

func contextWithTimeout(d time.Duration) (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.Background(), d)
}

func violations(set *mcpmodel.ToolSet, kind mcpmodel.ViolationKind) []string {
	var out []string
	for _, v := range set.Server.Violations {
		if v.Kind == kind {
			out = append(out, v.Message)
		}
	}
	return out
}

// pipeDiscard returns a pipe whose read side is drained in the
// background, standing in for a server stdin nobody reads.
func pipeDiscard() (*io.PipeReader, *io.PipeWriter) {
	r, w := io.Pipe()
	go func() { _, _ = io.Copy(io.Discard, r) }()
	return r, w
}

func itoa(n int) string { return strconv.Itoa(n) }
