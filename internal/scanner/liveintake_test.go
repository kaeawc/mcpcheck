package scanner

import (
	"bufio"
	"context"
	"encoding/json"
	"io"
	"strings"
	"testing"
	"time"
)

// fakeServer drives the server side of an MCP stdio handshake against
// the client's writes, emitting canned responses for initialize and
// tools/list.
type fakeServer struct {
	tools  []map[string]any
	failOn string // "initialize" or "tools/list" to inject a JSON-RPC error
}

func (s *fakeServer) run(ctx context.Context, in io.Reader, out io.Writer) error {
	r := bufio.NewReader(in)
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}
		line, err := r.ReadBytes('\n')
		if err != nil {
			return err // EOF when client closes stdin
		}
		var msg jsonrpcMessage
		if err := json.Unmarshal(line, &msg); err != nil {
			return err
		}
		switch msg.Method {
		case "initialize":
			if s.failOn == "initialize" {
				if err := writeError(out, msg.ID, -32603, "boom"); err != nil {
					return err
				}
				continue
			}
			result := map[string]any{
				"protocolVersion": "2024-11-05",
				"capabilities":    map[string]any{},
				"serverInfo":      map[string]any{"name": "fake", "version": "0"},
			}
			if err := writeResult(out, msg.ID, result); err != nil {
				return err
			}
		case "notifications/initialized":
			// no response expected
		case "tools/list":
			if s.failOn == "tools/list" {
				if err := writeError(out, msg.ID, -32603, "boom"); err != nil {
					return err
				}
				continue
			}
			result := map[string]any{"tools": s.tools}
			if err := writeResult(out, msg.ID, result); err != nil {
				return err
			}
		}
	}
}

func writeResult(out io.Writer, id json.RawMessage, result any) error {
	raw, err := json.Marshal(result)
	if err != nil {
		return err
	}
	msg := jsonrpcMessage{JSONRPC: "2.0", ID: id, Result: raw}
	return writeMessage(out, msg)
}

func writeError(out io.Writer, id json.RawMessage, code int, message string) error {
	msg := jsonrpcMessage{JSONRPC: "2.0", ID: id, Error: &jsonrpcError{Code: code, Message: message}}
	return writeMessage(out, msg)
}

// runWithFakeServer wires the client (FetchToolsLive) to a fakeServer
// over two pipes and returns whatever FetchToolsLive returned, projected
// to a flat liveTool view that drops InputSchema (we just record
// presence) so tests can assert with simple equality.
func runWithFakeServer(t *testing.T, srv *fakeServer) ([]liveTool, error) {
	t.Helper()
	clientToServerR, clientToServerW := io.Pipe()
	serverToClientR, serverToClientW := io.Pipe()

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	serverDone := make(chan error, 1)
	go func() {
		serverDone <- srv.run(ctx, clientToServerR, serverToClientW)
		serverToClientW.Close()
	}()

	tools, err := FetchToolsLive(ctx, clientToServerW, serverToClientR)
	clientToServerW.Close()
	<-serverDone

	out := make([]liveTool, len(tools))
	for i, tool := range tools {
		out[i] = liveTool{Name: tool.Name, Description: tool.Description, HasSchema: tool.InputSchema != nil}
	}
	return out, err
}

type liveTool struct {
	Name        string
	Description string
	HasSchema   bool
}

func TestFetchToolsLive_HappyPath(t *testing.T) {
	srv := &fakeServer{
		tools: []map[string]any{
			{
				"name":        "fetch_user",
				"description": "Fetch a user by id.",
				"inputSchema": map[string]any{"type": "object"},
			},
			{
				"name":        "send_message",
				"description": "Post a message to a channel.",
				"inputSchema": map[string]any{
					"type": "object",
					"properties": map[string]any{
						"body": map[string]any{"type": "string"},
					},
				},
			},
		},
	}
	tools, err := runWithFakeServer(t, srv)
	if err != nil {
		t.Fatalf("FetchToolsLive: %v", err)
	}
	want := []liveTool{
		{Name: "fetch_user", Description: "Fetch a user by id.", HasSchema: true},
		{Name: "send_message", Description: "Post a message to a channel.", HasSchema: true},
	}
	if len(tools) != len(want) {
		t.Fatalf("got %d tools, want %d: %+v", len(tools), len(want), tools)
	}
	for i, w := range want {
		if tools[i] != w {
			t.Errorf("tools[%d] = %+v, want %+v", i, tools[i], w)
		}
	}
}

func TestFetchToolsLive_InitializeError(t *testing.T) {
	srv := &fakeServer{failOn: "initialize"}
	_, err := runWithFakeServer(t, srv)
	if err == nil {
		t.Fatal("expected error when server rejects initialize")
	}
	if !strings.Contains(err.Error(), "initialize") {
		t.Errorf("error should mention initialize phase: %v", err)
	}
}

func TestFetchToolsLive_ToolsListError(t *testing.T) {
	srv := &fakeServer{failOn: "tools/list"}
	_, err := runWithFakeServer(t, srv)
	if err == nil {
		t.Fatal("expected error when server rejects tools/list")
	}
	if !strings.Contains(err.Error(), "tools/list") {
		t.Errorf("error should mention tools/list phase: %v", err)
	}
}

func TestFetchToolsLive_EmptyToolList(t *testing.T) {
	srv := &fakeServer{tools: nil}
	tools, err := runWithFakeServer(t, srv)
	if err != nil {
		t.Fatalf("FetchToolsLive: %v", err)
	}
	if len(tools) != 0 {
		t.Errorf("expected empty list, got %+v", tools)
	}
}
