package mcpclient

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/kaeawc/mcpcheck/internal/mcpmodel"
)

// modernServer is a fake 2026-07-28 server: stateless, versioned per
// request, with server/discover. legacyToo makes it dual-era.
type modernServer struct {
	supported []string
	legacyToo bool
	sloppy    bool // omit resultType / ttlMs / cacheScope
	logs      bool // emit notifications/message unprompted
	legacy    standard

	mu  sync.Mutex
	got []Message
}

func (s *modernServer) handle(m Message) []string {
	s.mu.Lock()
	s.got = append(s.got, m)
	s.mu.Unlock()
	if s.legacyToo && (m.Method == "initialize" || m.Method == "notifications/initialized" || s.legacyStarted()) {
		return s.legacy.handle(m)
	}
	if !m.IsRequest() {
		return nil
	}
	var p struct {
		Meta map[string]any `json:"_meta"`
	}
	_ = json.Unmarshal(m.Params, &p)
	version, _ := p.Meta[metaProtocolVersion].(string)
	if !contains(s.supported, version) {
		return []string{s.unsupported(m, version)}
	}
	return s.modernResult(m)
}

func (s *modernServer) legacyStarted() bool {
	return len(s.legacy.received()) > 0
}

func (s *modernServer) unsupported(m Message, requested string) string {
	data, _ := json.Marshal(map[string]any{"supported": s.supported, "requested": requested})
	out, _ := json.Marshal(Message{JSONRPC: "2.0", ID: m.ID, Error: &RPCError{Code: -32022, Message: "Unsupported protocol version", Data: data}})
	return string(out)
}

func (s *modernServer) modernResult(m Message) []string {
	var res map[string]any
	switch m.Method {
	case "server/discover":
		res = map[string]any{
			"supportedVersions": s.supported,
			"capabilities":      map[string]any{"tools": map[string]any{}},
			"_meta":             map[string]any{metaServerInfo: map[string]any{"name": "modern", "version": "2.0.0"}},
		}
	case "tools/list":
		res = map[string]any{"tools": []any{weatherTool}}
	default:
		return []string{rpcError(m.ID, -32601, "Method not found")}
	}
	if !s.sloppy {
		res["resultType"], res["ttlMs"], res["cacheScope"] = "complete", 60000, "public"
	}
	out := []string{result(m.ID, res)}
	if s.logs {
		out = append([]string{notification("notifications/message")}, out...)
	}
	return out
}

func (s *modernServer) methods() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	var names []string
	for _, m := range s.got {
		names = append(names, m.Method)
	}
	return strings.Join(names, ",")
}

func TestModernStdio(t *testing.T) {
	srv := &modernServer{supported: []string{"2026-07-28"}}
	set, _, err := runStdio(t, 2*time.Second, srv.handle)
	if err != nil {
		t.Fatal(err)
	}
	s := set.Server
	if s.Era != mcpmodel.EraModern || s.ProtocolVersion != "2026-07-28" || s.Name != "modern" || s.Version != "2.0.0" || s.LegacyFallback != "" {
		t.Errorf("server = %+v", s)
	}
	if len(s.Violations) != 0 || len(set.Tools) != 1 {
		t.Errorf("violations %+v, %d tools", s.Violations, len(set.Tools))
	}
	if got := srv.methods(); got != "server/discover,tools/list" {
		t.Errorf("client sent %s", got)
	}
	var p struct {
		Meta map[string]any `json:"_meta"`
	}
	_ = json.Unmarshal(srv.got[1].Params, &p)
	if info, _ := p.Meta[metaClientInfo].(map[string]any); info["name"] != "mcpcheck" || p.Meta[metaClientCapabilities] == nil {
		t.Errorf("tools/list _meta = %v", p.Meta)
	}
}

func TestModernRetriesWithAdvertisedVersion(t *testing.T) {
	srv := &modernServer{supported: []string{"2026-07-28"}}
	rec := NewRecorder(nil)
	set, err := fetchStdioWith(t, srv.handle, rec, Options{ProtocolVersion: "2027-01-01"})
	if err != nil {
		t.Fatal(err)
	}
	if set.Server.ProtocolVersion != "2026-07-28" || srv.methods() != "server/discover,server/discover,tools/list" {
		t.Errorf("version %s after %s", set.Server.ProtocolVersion, srv.methods())
	}
}

func TestDualEraServerThatOnlySupportsLegacyVersions(t *testing.T) {
	srv := &modernServer{supported: []string{"2025-06-18"}, legacyToo: true, legacy: standard{tools: []map[string]any{weatherTool}}}
	set, _, err := runStdio(t, 2*time.Second, srv.handle)
	if err != nil {
		t.Fatal(err)
	}
	s := set.Server
	if s.Era != mcpmodel.EraLegacy || s.OfferedProtocolVersion != "2025-06-18" || !strings.Contains(s.LegacyFallback, "supports only 2025-06-18") {
		t.Errorf("server = %+v", s)
	}
}

func TestModernResultShapeViolations(t *testing.T) {
	srv := &modernServer{supported: []string{"2026-07-28"}, sloppy: true, logs: true}
	set, _, err := runStdio(t, 2*time.Second, srv.handle)
	if err != nil {
		t.Fatal(err)
	}
	shape := strings.Join(violations(set, mcpmodel.ViolationResultShape), "\n")
	for _, want := range []string{"server/discover result has no resultType", "tools/list result must include a non-negative integer ttlMs", `tools/list result must include cacheScope`} {
		if !strings.Contains(shape, want) {
			t.Errorf("missing %q in:\n%s", want, shape)
		}
	}
	if v := violations(set, mcpmodel.ViolationLifecycle); len(v) != 1 || !strings.Contains(v[0], "logLevel") {
		t.Errorf("lifecycle violations: %q", v)
	}
}

func TestModernPinnedAgainstLegacyServer(t *testing.T) {
	srv := &standard{tools: []map[string]any{weatherTool}}
	_, err := fetchStdioWith(t, srv.handle, NewRecorder(nil), Options{ProtocolVersion: "2026-07-28"})
	if err == nil || !strings.Contains(err.Error(), "only speaks the legacy") {
		t.Fatalf("err = %v", err)
	}
}

func TestLegacyPinnedSkipsProbe(t *testing.T) {
	srv := &standard{tools: []map[string]any{weatherTool}}
	set, err := fetchStdioWith(t, srv.handle, NewRecorder(nil), Options{ProtocolVersion: "2025-03-26"})
	if err != nil {
		t.Fatal(err)
	}
	if got := srv.received()[0].Method; got != "initialize" || set.Server.ProtocolVersion != "2025-03-26" || set.Server.LegacyFallback != "" {
		t.Errorf("first method %s, server %+v", got, set.Server)
	}
}

func TestModernStreamableHTTP(t *testing.T) {
	srv := &modernServer{supported: []string{"2026-07-28"}}
	fake := &streamableServer{h: srv.handle}
	ts := httptest.NewServer(fake)
	defer ts.Close()
	set, err := fetchHTTP(t, ts.URL+"/mcp", TransportAuto, nil)
	if err != nil {
		t.Fatal(err)
	}
	if set.Server.Era != mcpmodel.EraModern || set.Server.Transport != "streamable-http" || len(set.Server.Violations) != 0 {
		t.Errorf("server = %+v", set.Server)
	}
	fake.mu.Lock()
	defer fake.mu.Unlock()
	if got := strings.Join(fake.methods, ","); got != "POST,POST" {
		t.Errorf("requests %s (modern servers have no session to DELETE)", got)
	}
	h := fake.headers[1]
	if h.Get("Mcp-Method") != "tools/list" || h.Get("MCP-Protocol-Version") != "2026-07-28" || h.Get("Mcp-Session-Id") != "" {
		t.Errorf("tools/list headers: %v", h)
	}
}

// TestLegacyHTTPRejectsProbeWith400 mirrors the Python and TS v1 SDKs,
// which reject a session-less request with 400 and a JSON-RPC error that
// is not a modern error: the client must fall back to initialize on the
// same transport rather than to legacy SSE.
func TestLegacyHTTPRejectsProbeWith400(t *testing.T) {
	std := &standard{tools: []map[string]any{weatherTool}}
	inner := &streamableServer{h: std.handle}
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost && r.Header.Get("Mcp-Session-Id") == "" {
			body, _ := io.ReadAll(r.Body)
			if !strings.Contains(string(body), `"initialize"`) {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusBadRequest)
				_, _ = io.WriteString(w, `{"jsonrpc":"2.0","id":"server-error","error":{"code":-32600,"message":"Bad Request: No valid session ID provided"}}`)
				return
			}
			r.Body = io.NopCloser(strings.NewReader(string(body)))
		}
		inner.ServeHTTP(w, r)
	}))
	defer ts.Close()
	set, err := fetchHTTP(t, ts.URL+"/mcp", TransportAuto, nil)
	if err != nil {
		t.Fatal(err)
	}
	s := set.Server
	if s.Era != mcpmodel.EraLegacy || s.Transport != "streamable-http" || !strings.Contains(s.LegacyFallback, "No valid session ID") {
		t.Errorf("server = %+v", s)
	}
}

func fetchStdioWith(t *testing.T, h handler, rec *Recorder, opts Options) (*mcpmodel.ToolSet, error) {
	t.Helper()
	c2sR, c2sW := io.Pipe()
	s2cR, s2cW := io.Pipe()
	go serveLines(c2sR, s2cW, h)
	defer c2sW.Close()
	ctx, cancel := contextWithTimeout(2 * time.Second)
	defer cancel()
	return FetchTools(ctx, NewStdio(c2sW, s2cR, rec), rec, "x", opts)
}
