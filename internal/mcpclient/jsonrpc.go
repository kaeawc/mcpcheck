// Package mcpclient is a deliberately forgiving MCP client used by live
// mode. It speaks every published protocol revision over stdio,
// Streamable HTTP, and the legacy HTTP+SSE transport, tolerates the
// mistakes real servers make, and records each one so rules can report it
// instead of the session simply failing.
package mcpclient

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"sync"
)

// Message is one JSON-RPC 2.0 message in either direction.
type Message struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method,omitempty"`
	Params  json.RawMessage `json:"params,omitempty"`
	Result  json.RawMessage `json:"result,omitempty"`
	Error   *RPCError       `json:"error,omitempty"`
}

// IsRequest reports whether m is a request (has a method and an id).
func (m *Message) IsRequest() bool { return m.Method != "" && len(m.ID) > 0 }

// IsNotification reports whether m is a notification (method, no id).
func (m *Message) IsNotification() bool { return m.Method != "" && len(m.ID) == 0 }

// IsResponse reports whether m is a response to a request.
func (m *Message) IsResponse() bool { return m.Method == "" && (len(m.ID) > 0 || m.Error != nil) }

// RPCError is a JSON-RPC error object.
type RPCError struct {
	Code    int             `json:"code"`
	Message string          `json:"message"`
	Data    json.RawMessage `json:"data,omitempty"`
}

func (e *RPCError) Error() string {
	name := rpcErrorNames[e.Code]
	if name == "" {
		return fmt.Sprintf("server returned error %d: %s", e.Code, e.Message)
	}
	return fmt.Sprintf("server returned error %d (%s): %s", e.Code, name, e.Message)
}

var rpcErrorNames = map[int]string{
	-32700: "parse error",
	-32600: "invalid request",
	-32601: "method not found",
	-32602: "invalid params",
	-32603: "internal error",
	-32002: "resource not found",
	-32042: "URL elicitation required",
}

// decodeMessages parses one JSON-RPC message or a batch array of them.
func decodeMessages(raw []byte) ([]Message, error) {
	raw = bytes.TrimSpace(raw)
	if len(raw) > 0 && raw[0] == '[' {
		var batch []Message
		if err := json.Unmarshal(raw, &batch); err != nil {
			return nil, err
		}
		return batch, nil
	}
	var m Message
	if err := json.Unmarshal(raw, &m); err != nil {
		return nil, err
	}
	return []Message{m}, nil
}

func newRequest(id int64, method string, params any) (Message, error) {
	m := Message{JSONRPC: "2.0", ID: json.RawMessage(strconv.FormatInt(id, 10)), Method: method}
	return m, m.setParams(params)
}

func newNotification(method string, params any) (Message, error) {
	m := Message{JSONRPC: "2.0", Method: method}
	return m, m.setParams(params)
}

func (m *Message) setParams(params any) error {
	if params == nil {
		return nil
	}
	raw, err := json.Marshal(params)
	if err != nil {
		return fmt.Errorf("encode params for %s: %w", m.Method, err)
	}
	m.Params = raw
	return nil
}

// sameID compares two JSON-RPC ids. exact is false when they only match
// after normalizing number-vs-string (e.g. 1 vs "1"), which the spec
// forbids but some servers do.
func sameID(a, b json.RawMessage) (match, exact bool) {
	if bytes.Equal(bytes.TrimSpace(a), bytes.TrimSpace(b)) {
		return true, true
	}
	return normalizeID(a) == normalizeID(b), false
}

func normalizeID(id json.RawMessage) string {
	var s string
	if json.Unmarshal(id, &s) == nil {
		return s
	}
	var n json.Number
	if json.Unmarshal(id, &n) == nil {
		return n.String()
	}
	return string(id)
}

// inbox is an unbounded queue of incoming messages fed by transport
// reader goroutines. Unbounded so a transport can enqueue from inside
// Send without deadlocking against the reader of the queue.
type inbox struct {
	mu     sync.Mutex
	queue  []Message
	notify chan struct{}
	closed bool
	err    error
}

func newInbox() *inbox { return &inbox{notify: make(chan struct{}, 1)} }

func (b *inbox) push(m Message) {
	b.mu.Lock()
	if !b.closed {
		b.queue = append(b.queue, m)
	}
	b.mu.Unlock()
	b.signal()
}

// close ends the stream. Messages already queued are still delivered;
// after that recv returns err.
func (b *inbox) close(err error) {
	b.mu.Lock()
	if !b.closed {
		b.closed = true
		b.err = err
	}
	b.mu.Unlock()
	b.signal()
}

func (b *inbox) signal() {
	select {
	case b.notify <- struct{}{}:
	default:
	}
}

func (b *inbox) recv(ctx context.Context) (Message, error) {
	for {
		b.mu.Lock()
		if len(b.queue) > 0 {
			m := b.queue[0]
			b.queue = b.queue[1:]
			b.mu.Unlock()
			return m, nil
		}
		if b.closed {
			err := b.err
			b.mu.Unlock()
			return Message{}, err
		}
		b.mu.Unlock()
		select {
		case <-b.notify:
		case <-ctx.Done():
			return Message{}, ctx.Err()
		}
	}
}
