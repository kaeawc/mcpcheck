package mcpclient

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/kaeawc/mcpcheck/internal/mcpmodel"
)

// streamableServer is a fake Streamable HTTP endpoint. respondSSE
// switches responses from application/json to text/event-stream.
type streamableServer struct {
	h          handler
	respondSSE bool
	mu         sync.Mutex
	headers    []http.Header
	methods    []string
}

func (s *streamableServer) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	s.headers = append(s.headers, r.Header.Clone())
	s.methods = append(s.methods, r.Method)
	s.mu.Unlock()
	if r.Method == http.MethodDelete {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	body, _ := io.ReadAll(r.Body)
	var m Message
	if err := json.Unmarshal(body, &m); err != nil {
		http.Error(w, "bad json", http.StatusBadRequest)
		return
	}
	out := s.h(m)
	if m.Method == "initialize" {
		w.Header().Set("Mcp-Session-Id", "sess-1")
	}
	if len(out) == 0 {
		w.WriteHeader(http.StatusAccepted)
		return
	}
	if s.respondSSE {
		w.Header().Set("Content-Type", "text/event-stream")
		for _, line := range out {
			fmt.Fprintf(w, "event: message\ndata: %s\n\n", line)
		}
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_, _ = io.WriteString(w, out[len(out)-1])
}

func fetchHTTP(t *testing.T, url, mode string, header http.Header) (*mcpmodel.ToolSet, error) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	rec := NewRecorder(nil)
	tr, err := DialHTTP(ctx, url, mode, HTTPOptions{Header: header}, rec)
	if err != nil {
		return nil, err
	}
	defer tr.Close()
	return FetchTools(ctx, tr, rec, url, Options{})
}

func TestStreamableHTTP(t *testing.T) {
	for _, sse := range []bool{false, true} {
		t.Run(fmt.Sprintf("sse=%v", sse), func(t *testing.T) {
			std := &standard{tools: []map[string]any{weatherTool}}
			fake := &streamableServer{h: func(m Message) []string {
				if m.Method == "tools/list" && sse {
					return append([]string{notification("notifications/message")}, std.handle(m)...)
				}
				return std.handle(m)
			}, respondSSE: sse}
			srv := httptest.NewServer(fake)
			defer srv.Close()

			set, err := fetchHTTP(t, srv.URL+"/mcp", TransportAuto, http.Header{"Authorization": {"Bearer t0k"}})
			if err != nil {
				t.Fatal(err)
			}
			if len(set.Tools) != 1 || set.Server.Transport != "streamable-http" {
				t.Fatalf("tools=%d transport=%s", len(set.Tools), set.Server.Transport)
			}
			if len(set.Server.Violations) != 0 {
				t.Errorf("violations: %+v", set.Server.Violations)
			}
			fake.mu.Lock()
			defer fake.mu.Unlock()
			if got := strings.Join(fake.methods, ","); got != "POST,POST,POST,POST,DELETE" {
				t.Errorf("requests: %s", got)
			}
			if probe := fake.headers[0]; probe.Get("Mcp-Method") != "server/discover" || probe.Get("MCP-Protocol-Version") != LatestProtocolVersion {
				t.Errorf("probe headers: %v", probe)
			}
			first, last := fake.headers[1], fake.headers[3]
			if first.Get("MCP-Protocol-Version") != "" {
				t.Errorf("initialize must not carry a modern MCP-Protocol-Version: %q", first.Get("MCP-Protocol-Version"))
			}
			if first.Get("Mcp-Session-Id") != "" || last.Get("Mcp-Session-Id") != "sess-1" {
				t.Errorf("session id not echoed: first=%q last=%q", first.Get("Mcp-Session-Id"), last.Get("Mcp-Session-Id"))
			}
			if last.Get("MCP-Protocol-Version") != LatestLegacyProtocolVersion {
				t.Errorf("MCP-Protocol-Version = %q", last.Get("MCP-Protocol-Version"))
			}
			if !strings.Contains(first.Get("Accept"), "text/event-stream") || first.Get("Authorization") != "Bearer t0k" {
				t.Errorf("headers: %v", first)
			}
		})
	}
}

func TestStreamableHTTPAcceptedForRequestIsViolation(t *testing.T) {
	std := &standard{}
	srv := httptest.NewServer(&streamableServer{h: func(m Message) []string {
		if m.Method == "tools/list" {
			return nil
		}
		return std.handle(m)
	}})
	defer srv.Close()
	_, err := fetchHTTP(t, srv.URL, TransportStreamableHTTP, nil)
	if err == nil || !strings.Contains(err.Error(), "tools/list: POST") || !strings.Contains(err.Error(), "202 Accepted") {
		t.Fatalf("err = %v", err)
	}
}

func TestHTTPUnauthorizedExplainsHeaderFlag(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("WWW-Authenticate", `Bearer resource_metadata="https://x/.well-known/oauth-protected-resource"`)
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer srv.Close()
	_, err := fetchHTTP(t, srv.URL+"/mcp", TransportAuto, nil)
	var se *HTTPStatusError
	if !errors.As(err, &se) || se.Status != 401 {
		t.Fatalf("err = %v", err)
	}
	for _, want := range []string{"--header 'Authorization: Bearer <token>'", "resource_metadata"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error lacks %q: %v", want, err)
		}
	}
}

func TestHTTPJSONRPCErrorBodyOnNon2xx(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		_, _ = io.WriteString(w, `{"jsonrpc":"2.0","id":null,"error":{"code":-32600,"message":"Bad Request: Server not initialized"}}`)
	}))
	defer srv.Close()
	_, err := fetchHTTP(t, srv.URL+"/mcp", TransportStreamableHTTP, nil)
	if err == nil || !strings.Contains(err.Error(), "Server not initialized") {
		t.Fatalf("err = %v", err)
	}
}

// legacyServer is a fake 2024-11-05 HTTP+SSE server. Optionally it
// rejects POSTs to the stream URL so auto mode must fall back.
type legacyServer struct {
	h   handler
	out chan string
}

func newLegacyServer(h handler) *legacyServer {
	return &legacyServer{h: h, out: make(chan string, 16)}
}

func (s *legacyServer) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	switch {
	case r.Method == http.MethodGet && strings.HasSuffix(r.URL.Path, "/sse"), r.Method == http.MethodGet && r.URL.Path == "/":
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprintf(w, "event: endpoint\ndata: /messages?session_id=abc\n\n")
		w.(http.Flusher).Flush()
		for {
			select {
			case line := <-s.out:
				fmt.Fprintf(w, "event: message\ndata: %s\n\n", line)
				w.(http.Flusher).Flush()
			case <-r.Context().Done():
				return
			}
		}
	case r.Method == http.MethodPost && r.URL.Path == "/messages":
		if r.URL.Query().Get("session_id") != "abc" {
			http.Error(w, "unknown session", http.StatusNotFound)
			return
		}
		body, _ := io.ReadAll(r.Body)
		var m Message
		_ = json.Unmarshal(body, &m)
		for _, line := range s.h(m) {
			s.out <- line
		}
		w.WriteHeader(http.StatusAccepted)
	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

func TestLegacySSE(t *testing.T) {
	cases := []struct{ name, path, mode string }{
		{"sse path auto-detected", "/sse", TransportAuto},
		{"explicit sse", "/sse", TransportSSE},
		{"fallback after 405", "/", TransportAuto},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			std := &standard{tools: []map[string]any{weatherTool}}
			srv := httptest.NewServer(newLegacyServer(std.handle))
			defer srv.Close()
			set, err := fetchHTTP(t, srv.URL+tc.path, tc.mode, nil)
			if err != nil {
				t.Fatal(err)
			}
			if len(set.Tools) != 1 || set.Server.Transport != "sse" {
				t.Fatalf("tools=%d transport=%s", len(set.Tools), set.Server.Transport)
			}
		})
	}
}

func TestDialHTTPRejectsBadURL(t *testing.T) {
	_, err := DialHTTP(context.Background(), "localhost:8000", TransportAuto, HTTPOptions{}, NewRecorder(nil))
	if err == nil || !strings.Contains(err.Error(), "http(s)://") {
		t.Fatalf("err = %v", err)
	}
}

func TestReadSSE(t *testing.T) {
	stream := ": comment\r\nevent: endpoint\r\ndata: /a\r\n\r\ndata: line1\ndata: line2\nid: 7\n\ndata: tail"
	var got []sseEvent
	if err := readSSE(strings.NewReader(stream), func(ev sseEvent) bool { got = append(got, ev); return true }); err != nil {
		t.Fatal(err)
	}
	want := []sseEvent{{Event: "endpoint", Data: "/a"}, {Data: "line1\nline2", ID: "7"}, {Data: "tail"}}
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Errorf("got %+v, want %+v", got, want)
	}
}
