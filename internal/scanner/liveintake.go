// Live-mode intake — runs the MCP handshake against a real server
// subprocess and consumes its tools/list response. Catches tools that
// are registered dynamically (via runtime introspection or plugin
// loading) and so don't appear in any static intake.
//
// Wire format: MCP speaks JSON-RPC 2.0 over stdio, one JSON object per
// line, terminated with a newline. We send `initialize`, the
// `notifications/initialized` notification, and `tools/list`, then
// close stdin to let the server exit cleanly.
//
// The handshake logic is exposed at FetchToolsLive over a plain
// io.Reader / io.Writer pair so tests can drive it with an in-memory
// fake server. LoadLive is the production wrapper that spawns a
// subprocess via os/exec.
package scanner

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"

	"github.com/kaeawc/mcpcheck/internal/mcpmodel"
)

// protocolVersion is what we advertise in the initialize request. The
// MCP spec requires a date-stamped version string; servers negotiate.
const protocolVersion = "2024-11-05"

// clientInfo identifies us to the server. Cosmetic; servers may log it.
var clientInfo = map[string]any{
	"name":    "mcpcheck",
	"version": "0.1.0",
}

type jsonrpcMessage struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method,omitempty"`
	Params  json.RawMessage `json:"params,omitempty"`
	Result  json.RawMessage `json:"result,omitempty"`
	Error   *jsonrpcError   `json:"error,omitempty"`
}

type jsonrpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

// FetchToolsLive performs the MCP handshake (initialize + initialized
// notification + tools/list) by writing requests to stdin and reading
// responses from stdout. Returns the parsed tool set on success.
func FetchToolsLive(ctx context.Context, stdin io.Writer, stdout io.Reader) ([]mcpmodel.Tool, error) {
	dec := bufio.NewReader(stdout)

	// initialize
	if err := writeRequest(stdin, 1, "initialize", map[string]any{
		"protocolVersion": protocolVersion,
		"capabilities":    map[string]any{},
		"clientInfo":      clientInfo,
	}); err != nil {
		return nil, fmt.Errorf("send initialize: %w", err)
	}
	if _, err := readResponse(dec); err != nil {
		return nil, fmt.Errorf("initialize: %w", err)
	}

	// notifications/initialized — no id, no response expected.
	if err := writeNotification(stdin, "notifications/initialized", nil); err != nil {
		return nil, fmt.Errorf("send initialized notification: %w", err)
	}

	// tools/list
	if err := writeRequest(stdin, 2, "tools/list", nil); err != nil {
		return nil, fmt.Errorf("send tools/list: %w", err)
	}
	resp, err := readResponse(dec)
	if err != nil {
		return nil, fmt.Errorf("tools/list: %w", err)
	}

	var result struct {
		Tools []struct {
			Name        string         `json:"name"`
			Description string         `json:"description"`
			InputSchema map[string]any `json:"inputSchema"`
		} `json:"tools"`
	}
	if err := json.Unmarshal(resp.Result, &result); err != nil {
		return nil, fmt.Errorf("parse tools/list result: %w", err)
	}

	tools := make([]mcpmodel.Tool, 0, len(result.Tools))
	for _, t := range result.Tools {
		tools = append(tools, mcpmodel.Tool{
			Name:        t.Name,
			Description: t.Description,
			InputSchema: t.InputSchema,
		})
	}
	return tools, nil
}

// LoadLive spawns cmd, runs the handshake, and shuts the subprocess
// down. cmd[0] is the program; cmd[1:] are args. The subprocess's
// stderr is forwarded to the host process's stderr so server-side
// errors are visible.
func LoadLive(ctx context.Context, cmd []string) (*mcpmodel.ToolSet, error) {
	if len(cmd) == 0 {
		return nil, fmt.Errorf("LoadLive: empty command")
	}
	proc := exec.CommandContext(ctx, cmd[0], cmd[1:]...)
	stdin, err := proc.StdinPipe()
	if err != nil {
		return nil, fmt.Errorf("stdin pipe: %w", err)
	}
	stdout, err := proc.StdoutPipe()
	if err != nil {
		return nil, fmt.Errorf("stdout pipe: %w", err)
	}
	proc.Stderr = os.Stderr

	if err := proc.Start(); err != nil {
		return nil, fmt.Errorf("start %s: %w", cmd[0], err)
	}

	tools, fetchErr := FetchToolsLive(ctx, stdin, stdout)

	// Closing stdin signals the server to exit. We don't surface Wait's
	// error: a non-zero exit on a clean disconnect is common (servers
	// don't always treat EOF as success), and the fetch error is what
	// the caller actually cares about.
	_ = stdin.Close()
	_ = proc.Wait()

	if fetchErr != nil {
		return nil, fetchErr
	}
	return &mcpmodel.ToolSet{Tools: tools, Source: strings.Join(cmd, " ")}, nil
}

func writeRequest(w io.Writer, id int, method string, params any) error {
	idRaw, _ := json.Marshal(id)
	msg := jsonrpcMessage{JSONRPC: "2.0", ID: idRaw, Method: method}
	if params != nil {
		raw, err := json.Marshal(params)
		if err != nil {
			return err
		}
		msg.Params = raw
	}
	return writeMessage(w, msg)
}

func writeNotification(w io.Writer, method string, params any) error {
	msg := jsonrpcMessage{JSONRPC: "2.0", Method: method}
	if params != nil {
		raw, err := json.Marshal(params)
		if err != nil {
			return err
		}
		msg.Params = raw
	}
	return writeMessage(w, msg)
}

func writeMessage(w io.Writer, msg jsonrpcMessage) error {
	raw, err := json.Marshal(msg)
	if err != nil {
		return err
	}
	if _, err := w.Write(append(raw, '\n')); err != nil {
		return err
	}
	return nil
}

func readResponse(r *bufio.Reader) (*jsonrpcMessage, error) {
	line, err := r.ReadBytes('\n')
	if err != nil && len(line) == 0 {
		return nil, err
	}
	var msg jsonrpcMessage
	if err := json.Unmarshal(line, &msg); err != nil {
		return nil, fmt.Errorf("decode response: %w (line: %q)", err, string(line))
	}
	if msg.Error != nil {
		return nil, fmt.Errorf("server returned error %d: %s", msg.Error.Code, msg.Error.Message)
	}
	return &msg, nil
}
