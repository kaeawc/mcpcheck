package mcpclient

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/kaeawc/mcpcheck/internal/mcpmodel"
)

func TestStdioHappyPath(t *testing.T) {
	srv := &standard{tools: []map[string]any{weatherTool}}
	set, _, err := runStdio(t, 2*time.Second, srv.handle)
	if err != nil {
		t.Fatal(err)
	}
	if len(set.Tools) != 1 {
		t.Fatalf("got %d tools", len(set.Tools))
	}
	tool := set.Tools[0]
	if tool.Name != "get_weather" || tool.Title != "Weather" || tool.InputSchema == nil ||
		tool.OutputSchema == nil || tool.Annotations["readOnlyHint"] != true || tool.Raw == nil {
		t.Errorf("tool not fully decoded: %+v", tool)
	}
	s := set.Server
	if s.Transport != "stdio" || s.Name != "fake" || s.Version != "1.2.3" ||
		s.ProtocolVersion != LatestProtocolVersion || s.OfferedProtocolVersion != LatestProtocolVersion {
		t.Errorf("server not recorded: %+v", s)
	}
	if len(s.Violations) != 0 || len(s.NonProtocolOutput) != 0 {
		t.Errorf("unexpected observations: %+v", s)
	}
	methods := []string{}
	for _, m := range srv.received() {
		methods = append(methods, m.Method)
	}
	if got := strings.Join(methods, ","); got != "initialize,notifications/initialized,tools/list" {
		t.Errorf("client sent %s", got)
	}
}

func TestStdioPagination(t *testing.T) {
	pages := map[string]string{"": "p2", "p2": "p3", "p3": ""}
	set, _, err := runStdio(t, 2*time.Second, func(m Message) []string {
		if m.Method == "initialize" {
			return []string{result(m.ID, initResult("2025-06-18"))}
		}
		if m.Method != "tools/list" {
			return nil
		}
		var p struct {
			Cursor string `json:"cursor"`
		}
		_ = json.Unmarshal(m.Params, &p)
		res := map[string]any{"tools": []any{map[string]any{"name": "tool_" + p.Cursor}}}
		if next := pages[p.Cursor]; next != "" {
			res["nextCursor"] = next
		}
		return []string{result(m.ID, res)}
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(set.Tools) != 3 || set.Server.ToolPages != 3 {
		t.Fatalf("got %d tools over %d pages", len(set.Tools), set.Server.ToolPages)
	}
	if set.Server.ProtocolVersion != "2025-06-18" {
		t.Errorf("negotiated %q", set.Server.ProtocolVersion)
	}
}

func TestStdioRepeatedCursorStops(t *testing.T) {
	set, _, err := runStdio(t, 2*time.Second, func(m Message) []string {
		if m.Method == "initialize" {
			return []string{result(m.ID, initResult(LatestProtocolVersion))}
		}
		if m.Method == "tools/list" {
			return []string{result(m.ID, map[string]any{"tools": []any{}, "nextCursor": "same"})}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if v := violations(set, mcpmodel.ViolationPagination); len(v) != 1 {
		t.Errorf("pagination violations: %v", v)
	}
}

func TestStdioToleratesNoiseNotificationsAndServerRequests(t *testing.T) {
	var pinged bool
	srv := &standard{tools: []map[string]any{weatherTool}}
	set, _, err := runStdio(t, 2*time.Second, func(m Message) []string {
		switch {
		case m.Method == "initialize":
			return append([]string{
				"Server starting on stdio...",
				notification("notifications/message"),
				`{"jsonrpc":"2.0","id":"s1","method":"ping"}`,
			}, srv.handle(m)...)
		case m.IsResponse() && string(m.ID) == `"s1"`:
			pinged = len(m.Result) > 0
		}
		return srv.handle(m)
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(set.Tools) != 1 {
		t.Fatalf("got %d tools", len(set.Tools))
	}
	if !pinged {
		t.Error("client did not answer the server's ping")
	}
	if got := set.Server.NonProtocolOutput; len(got) != 1 || got[0] != "Server starting on stdio..." {
		t.Errorf("noise = %q", got)
	}
}

func TestStdioRecordsJSONRPCViolations(t *testing.T) {
	set, _, err := runStdio(t, 2*time.Second, func(m Message) []string {
		id := `"` + string(m.ID) + `"`
		switch m.Method {
		case "initialize":
			raw, _ := json.Marshal(initResult(LatestProtocolVersion))
			return []string{`{"jsonrpc":"1.0","id":` + id + `,"result":` + string(raw) + `}`}
		case "tools/list":
			return []string{`{"jsonrpc":"2.0","id":` + id + `,"result":{"tools":[]}}`}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	v := violations(set, mcpmodel.ViolationJSONRPC)
	if len(v) != 3 {
		t.Fatalf("want jsonrpc-version + two id-type violations, got %q", v)
	}
	if !strings.Contains(v[0], `jsonrpc="1.0"`) || !strings.Contains(v[1], "including type") {
		t.Errorf("unexpected messages: %q", v)
	}
}

func TestStdioLifecycleAndVersionViolations(t *testing.T) {
	set, _, err := runStdio(t, 2*time.Second, func(m Message) []string {
		switch m.Method {
		case "initialize":
			return []string{result(m.ID, map[string]any{
				"protocolVersion": "1.0",
				"capabilities":    map[string]any{},
				"serverInfo":      map[string]any{"name": "x"},
			})}
		case "tools/list":
			return []string{result(m.ID, map[string]any{"tools": []any{}})}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if v := violations(set, mcpmodel.ViolationProtocolVersion); len(v) != 1 || !strings.Contains(v[0], `"1.0"`) {
		t.Errorf("version violations: %q", v)
	}
	lifecycle := strings.Join(violations(set, mcpmodel.ViolationLifecycle), "\n")
	if !strings.Contains(lifecycle, "serverInfo") || !strings.Contains(lifecycle, `"tools" capability`) {
		t.Errorf("lifecycle violations: %s", lifecycle)
	}
}

func TestStdioErrorsNameTheFailingStep(t *testing.T) {
	cases := []struct {
		failOn, want string
	}{
		{"initialize", "initialize: server returned error -32602 (invalid params): bad version"},
		{"tools/list", "tools/list: server returned error -32602 (invalid params): bad version"},
	}
	for _, tc := range cases {
		t.Run(tc.failOn, func(t *testing.T) {
			srv := &standard{}
			_, _, err := runStdio(t, 2*time.Second, func(m Message) []string {
				if m.Method == tc.failOn {
					return []string{rpcError(m.ID, -32602, "bad version")}
				}
				return srv.handle(m)
			})
			var se *SessionError
			if !errors.As(err, &se) || se.Step != tc.failOn || !strings.HasPrefix(err.Error(), tc.want) {
				t.Fatalf("err = %v", err)
			}
		})
	}
}

func TestStdioServerClosesBeforeAnswering(t *testing.T) {
	c2sR, c2sW := pipeDiscard()
	defer c2sR.Close()
	rec := NewRecorder(nil)
	tr := NewStdio(c2sW, strings.NewReader("fatal: missing API key\n"), rec)
	_, err := FetchTools(context.Background(), tr, rec, "x", Options{})
	if !errors.Is(err, ErrServerClosed) {
		t.Fatalf("err = %v", err)
	}
	for _, want := range []string{"exited or closed the stream", "fatal: missing API key"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error lacks %q:\n%v", want, err)
		}
	}
}

func TestStdioTimeoutHint(t *testing.T) {
	_, _, err := runStdio(t, 100*time.Millisecond, func(Message) []string { return nil })
	if !errors.Is(err, context.DeadlineExceeded) || !strings.Contains(err.Error(), "never wrote a JSON-RPC message") {
		t.Fatalf("err = %v", err)
	}
}

func TestStdioLSPFramingAndBatches(t *testing.T) {
	c2sR, c2sW := pipeDiscard()
	defer c2sR.Close()
	initRaw := result(json.RawMessage("1"), initResult(LatestProtocolVersion))
	listRaw := result(json.RawMessage("2"), map[string]any{"tools": []any{weatherTool}})
	stream := "Content-Length: " + itoa(len(initRaw)) + "\r\n\r\n" + initRaw +
		"\n[" + listRaw + "," + notification("notifications/message") + "]\n"
	rec := NewRecorder(nil)
	set, err := FetchTools(context.Background(), NewStdio(c2sW, strings.NewReader(stream), rec), rec, "x", Options{})
	if err != nil {
		t.Fatal(err)
	}
	if len(set.Tools) != 1 {
		t.Fatalf("got %d tools", len(set.Tools))
	}
	if len(violations(set, mcpmodel.ViolationTransport)) != 1 || len(violations(set, mcpmodel.ViolationJSONRPC)) != 1 {
		t.Errorf("violations: %+v", set.Server.Violations)
	}
}
