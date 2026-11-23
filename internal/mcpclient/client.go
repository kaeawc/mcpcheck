package mcpclient

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/kaeawc/mcpcheck/internal/mcpmodel"
)

// Protocol revisions mcpcheck understands, oldest first.
var KnownProtocolVersions = []string{"2024-11-05", "2025-03-26", "2025-06-18", "2025-11-25"}

// LatestProtocolVersion is offered in initialize unless overridden.
var LatestProtocolVersion = KnownProtocolVersions[len(KnownProtocolVersions)-1]

// IsKnownProtocolVersion reports whether v is a published revision.
func IsKnownProtocolVersion(v string) bool {
	for _, k := range KnownProtocolVersions {
		if k == v {
			return true
		}
	}
	return false
}

// maxToolPages bounds tools/list pagination against servers that never
// stop returning a cursor.
const maxToolPages = 1000

// Options configures a live session.
type Options struct {
	// ProtocolVersion is offered in initialize. Defaults to
	// LatestProtocolVersion.
	ProtocolVersion string
	ClientName      string
	ClientVersion   string
}

type client struct {
	t      Transport
	rec    *Recorder
	nextID int64
	server *mcpmodel.Server
	step   string
	recvd  int
}

// FetchTools runs initialize, notifications/initialized, and a paginated
// tools/list over t, and returns the tools along with everything observed
// about the server. The caller owns t and must Close it.
func FetchTools(ctx context.Context, t Transport, rec *Recorder, endpoint string, opts Options) (*mcpmodel.ToolSet, error) {
	if opts.ProtocolVersion == "" {
		opts.ProtocolVersion = LatestProtocolVersion
	}
	c := &client{t: t, rec: rec, server: &mcpmodel.Server{
		Endpoint:               endpoint,
		OfferedProtocolVersion: opts.ProtocolVersion,
	}}
	tools, err := c.run(ctx, opts)
	c.server.Transport = t.Name()
	rec.apply(c.server)
	if err != nil {
		return nil, c.explain(err)
	}
	return &mcpmodel.ToolSet{Tools: tools, Source: endpoint, Server: c.server}, nil
}

func (c *client) run(ctx context.Context, opts Options) ([]mcpmodel.Tool, error) {
	if err := c.initialize(ctx, opts); err != nil {
		return nil, err
	}
	c.step = "notifications/initialized"
	n, err := newNotification("notifications/initialized", nil)
	if err != nil {
		return nil, err
	}
	if err := c.t.Send(ctx, n); err != nil {
		return nil, err
	}
	if !c.server.HasCapability("tools") {
		c.rec.Violation(mcpmodel.ViolationLifecycle,
			"server did not declare the \"tools\" capability in its initialize result; spec-following clients will never call tools/list")
	}
	return c.listTools(ctx)
}

func (c *client) initialize(ctx context.Context, opts Options) error {
	name, version := opts.ClientName, opts.ClientVersion
	if name == "" {
		name, version = "mcpcheck", "dev"
	}
	raw, err := c.call(ctx, "initialize", map[string]any{
		"protocolVersion": opts.ProtocolVersion,
		"capabilities":    map[string]any{},
		"clientInfo":      map[string]any{"name": name, "version": version},
	})
	if err != nil {
		return err
	}
	var res struct {
		ProtocolVersion string         `json:"protocolVersion"`
		Capabilities    map[string]any `json:"capabilities"`
		ServerInfo      struct {
			Name    string `json:"name"`
			Version string `json:"version"`
			Title   string `json:"title"`
		} `json:"serverInfo"`
		Instructions string `json:"instructions"`
	}
	if err := json.Unmarshal(raw, &res); err != nil {
		return fmt.Errorf("parse initialize result: %w", err)
	}
	s := c.server
	s.ProtocolVersion, s.Capabilities, s.Instructions = res.ProtocolVersion, res.Capabilities, res.Instructions
	s.Name, s.Version, s.Title = res.ServerInfo.Name, res.ServerInfo.Version, res.ServerInfo.Title
	c.checkInitialize(opts.ProtocolVersion)
	if va, ok := c.t.(versionAware); ok && IsKnownProtocolVersion(s.ProtocolVersion) {
		va.SetProtocolVersion(s.ProtocolVersion)
	}
	return nil
}

func (c *client) checkInitialize(offered string) {
	s := c.server
	switch {
	case s.ProtocolVersion == "":
		c.rec.Violation(mcpmodel.ViolationProtocolVersion, "initialize result has no protocolVersion")
	case !IsKnownProtocolVersion(s.ProtocolVersion):
		c.rec.Violation(mcpmodel.ViolationProtocolVersion,
			"server answered protocolVersion %q to an offer of %q; published revisions are %s",
			s.ProtocolVersion, offered, strings.Join(KnownProtocolVersions, ", "))
	}
	if s.Capabilities == nil {
		c.rec.Violation(mcpmodel.ViolationLifecycle, "initialize result has no capabilities object")
	}
	if s.Name == "" || s.Version == "" {
		c.rec.Violation(mcpmodel.ViolationLifecycle,
			"initialize result's serverInfo must include name and version (got name=%q version=%q)", s.Name, s.Version)
	}
}

func (c *client) listTools(ctx context.Context) ([]mcpmodel.Tool, error) {
	var tools []mcpmodel.Tool
	seen := map[string]bool{}
	cursor := ""
	for page := 1; page <= maxToolPages; page++ {
		var params any
		if cursor != "" {
			params = map[string]any{"cursor": cursor}
		}
		raw, err := c.call(ctx, "tools/list", params)
		if err != nil {
			return nil, err
		}
		pageTools, next, err := c.parseToolsPage(raw)
		if err != nil {
			return nil, err
		}
		c.server.ToolPages = page
		tools = append(tools, pageTools...)
		if next == "" {
			return tools, nil
		}
		if seen[next] {
			c.rec.Violation(mcpmodel.ViolationPagination,
				"tools/list returned nextCursor %q twice; pagination never terminates", next)
			return tools, nil
		}
		seen[next] = true
		cursor = next
	}
	c.rec.Violation(mcpmodel.ViolationPagination, "tools/list kept returning nextCursor after %d pages", maxToolPages)
	return tools, nil
}

func (c *client) parseToolsPage(raw json.RawMessage) ([]mcpmodel.Tool, string, error) {
	var res struct {
		Tools      []map[string]any `json:"tools"`
		NextCursor any              `json:"nextCursor"`
	}
	if err := json.Unmarshal(raw, &res); err != nil {
		return nil, "", fmt.Errorf("parse tools/list result: %w", err)
	}
	if res.Tools == nil {
		c.rec.Violation(mcpmodel.ViolationJSONRPC, "tools/list result has no \"tools\" array")
	}
	tools := make([]mcpmodel.Tool, 0, len(res.Tools))
	for _, m := range res.Tools {
		tools = append(tools, mcpmodel.ToolFromJSON(m))
	}
	switch next := res.NextCursor.(type) {
	case nil:
		return tools, "", nil
	case string:
		return tools, next, nil
	default:
		c.rec.Violation(mcpmodel.ViolationPagination, "tools/list nextCursor must be a string, got %T", next)
		return tools, "", nil
	}
}

// call sends a request and waits for its response, servicing any
// server-initiated requests and notifications that arrive first.
func (c *client) call(ctx context.Context, method string, params any) (json.RawMessage, error) {
	c.step = method
	c.nextID++
	req, err := newRequest(c.nextID, method, params)
	if err != nil {
		return nil, err
	}
	if err := c.t.Send(ctx, req); err != nil {
		return nil, err
	}
	for {
		m, err := c.t.Recv(ctx)
		if err != nil {
			return nil, err
		}
		c.recvd++
		if res, done, err := c.dispatch(ctx, req, m); done {
			return res, err
		}
	}
}

func (c *client) dispatch(ctx context.Context, req, m Message) (json.RawMessage, bool, error) {
	if m.JSONRPC != "2.0" {
		c.rec.Violation(mcpmodel.ViolationJSONRPC, "server message has jsonrpc=%q; every message must carry \"jsonrpc\": \"2.0\"", m.JSONRPC)
	}
	switch {
	case m.IsRequest():
		c.answer(ctx, m)
	case m.IsNotification():
	case m.IsResponse():
		return c.matchResponse(req, m)
	default:
		c.rec.Violation(mcpmodel.ViolationJSONRPC, "server sent a message that is neither a request, notification, nor response")
	}
	return nil, false, nil
}

func (c *client) matchResponse(req, m Message) (json.RawMessage, bool, error) {
	match, exact := sameID(req.ID, m.ID)
	if !match {
		if m.Error != nil && (len(m.ID) == 0 || string(m.ID) == "null") {
			return nil, true, m.Error
		}
		c.rec.Violation(mcpmodel.ViolationJSONRPC, "server sent a response with id %s while %s (id %s) was outstanding", m.ID, req.Method, req.ID)
		return nil, false, nil
	}
	if !exact {
		c.rec.Violation(mcpmodel.ViolationJSONRPC, "server answered request id %s with id %s; response ids must match exactly, including type", req.ID, m.ID)
	}
	if m.Error != nil {
		if len(m.Result) > 0 {
			c.rec.Violation(mcpmodel.ViolationJSONRPC, "response to %s has both result and error", req.Method)
		}
		return nil, true, m.Error
	}
	if len(m.Result) == 0 || string(m.Result) == "null" {
		c.rec.Violation(mcpmodel.ViolationJSONRPC, "response to %s has neither result nor error", req.Method)
		return json.RawMessage("{}"), true, nil
	}
	return m.Result, true, nil
}

// answer replies to a server-initiated request. mcpcheck declares no
// client capabilities, so only ping is expected.
func (c *client) answer(ctx context.Context, m Message) {
	reply := Message{JSONRPC: "2.0", ID: m.ID}
	switch m.Method {
	case "ping":
		reply.Result = json.RawMessage("{}")
	default:
		c.rec.Violation(mcpmodel.ViolationLifecycle,
			"server sent a %s request although the client declared no capabilities that allow it", m.Method)
		reply.Error = &RPCError{Code: -32601, Message: "mcpcheck does not support " + m.Method}
	}
	_ = c.t.Send(ctx, reply)
}

// explain decorates a session failure with the step that failed, a hint
// at the likely cause, and transport diagnostics.
func (c *client) explain(err error) error {
	msg := fmt.Sprintf("%s: %v", c.step, err)
	if errors.Is(err, context.DeadlineExceeded) {
		msg = c.step + ": timed out waiting for the server"
	}
	if hint := c.hint(err); hint != "" {
		msg += "\n  hint: " + hint
	}
	if noise := c.server.NonProtocolOutput; len(noise) > 0 {
		msg += "\n  server wrote non-JSON-RPC output to stdout:\n    " + strings.Join(noise, "\n    ")
	}
	if d, ok := c.t.(diagnoser); ok {
		if diag := d.Diagnose(); diag != "" {
			msg += "\n  " + diag
		}
	}
	return &SessionError{Step: c.step, Err: err, msg: msg}
}

func (c *client) hint(err error) string {
	switch {
	case errors.Is(err, context.DeadlineExceeded) && c.recvd == 0 && c.t.Name() == "stdio":
		return "the server never wrote a JSON-RPC message; check that the command starts an MCP server on stdio (not HTTP), and that it doesn't wait for other input — or raise --live-timeout if it is slow to start"
	case errors.Is(err, context.DeadlineExceeded):
		return "no response before the deadline; raise --live-timeout if the server is slow"
	case errors.Is(err, ErrServerClosed) && c.recvd == 0:
		return "the server exited or closed the stream before answering; see the diagnostics below"
	}
	var rpcErr *RPCError
	if errors.As(err, &rpcErr) && c.step == "initialize" && rpcErr.Code == -32602 {
		return fmt.Sprintf("the server may not accept protocol version %s; retry with --protocol-version %s", c.server.OfferedProtocolVersion, KnownProtocolVersions[0])
	}
	return ""
}

// SessionError is returned when a live session fails. Error() includes
// hints and diagnostics; Unwrap exposes the underlying cause.
type SessionError struct {
	Step string
	Err  error
	msg  string
}

func (e *SessionError) Error() string { return e.msg }
func (e *SessionError) Unwrap() error { return e.Err }
