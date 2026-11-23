package mcpclient

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/kaeawc/mcpcheck/internal/mcpmodel"
)

// HTTPOptions configures the HTTP transports.
type HTTPOptions struct {
	// Client performs requests. Must not have a Timeout, since SSE
	// streams are long-lived; deadlines come from contexts instead.
	Client *http.Client
	// Header is added to every request (e.g. Authorization).
	Header http.Header
}

// HTTPStatusError is a non-2xx HTTP response.
type HTTPStatusError struct {
	Method, URL string
	Status      int
	Body        string
	Header      http.Header
}

func (e *HTTPStatusError) Error() string {
	msg := fmt.Sprintf("%s %s returned HTTP %d", e.Method, e.URL, e.Status)
	switch e.Status {
	case http.StatusUnauthorized, http.StatusForbidden:
		msg += " — the server requires authorization; pass credentials with --header 'Authorization: Bearer <token>'"
		if wa := e.Header.Get("WWW-Authenticate"); wa != "" {
			msg += " (WWW-Authenticate: " + wa + ")"
		}
	case http.StatusNotAcceptable:
		msg += " — the server rejected the Accept header"
	}
	if body := strings.TrimSpace(e.Body); body != "" {
		msg += ": " + body
	}
	return msg
}

const maxErrorBody = 512

func statusError(req *http.Request, resp *http.Response) *HTTPStatusError {
	body, _ := io.ReadAll(io.LimitReader(resp.Body, maxErrorBody))
	return &HTTPStatusError{
		Method: req.Method, URL: req.URL.String(), Status: resp.StatusCode,
		Body: string(body), Header: resp.Header,
	}
}

func mediaType(h http.Header) string {
	mt, _, err := mime.ParseMediaType(h.Get("Content-Type"))
	if err != nil {
		return strings.ToLower(strings.TrimSpace(h.Get("Content-Type")))
	}
	return mt
}

// sseEvent is one dispatched server-sent event.
type sseEvent struct {
	Event string
	Data  string
	ID    string
}

// readSSE parses a text/event-stream body, calling onEvent for each
// dispatched event until the body ends or onEvent returns false.
func readSSE(r io.Reader, onEvent func(sseEvent) bool) error {
	br := bufio.NewReader(r)
	var p sseParser
	for {
		line, err := br.ReadString('\n')
		line = strings.TrimRight(line, "\r\n")
		if line == "" && err == nil {
			if ev, ok := p.dispatch(); ok && !onEvent(ev) {
				return nil
			}
			continue
		}
		p.field(line)
		if err != nil {
			if ev, ok := p.dispatch(); ok && len(ev.Data) > 0 {
				onEvent(ev)
			}
			if errors.Is(err, io.EOF) {
				return nil
			}
			return err
		}
	}
}

// sseParser accumulates the fields of one event.
type sseParser struct {
	ev   sseEvent
	data []string
}

func (p *sseParser) field(line string) {
	name, value, _ := strings.Cut(line, ":")
	value = strings.TrimPrefix(value, " ")
	switch name {
	case "event":
		p.ev.Event = value
	case "data":
		p.data = append(p.data, value)
	case "id":
		p.ev.ID = value
	}
}

// dispatch returns the accumulated event, if any, and resets.
func (p *sseParser) dispatch() (sseEvent, bool) {
	ev, ok := p.ev, len(p.data) > 0 || p.ev.Event != ""
	ev.Data = strings.Join(p.data, "\n")
	p.ev, p.data = sseEvent{}, nil
	return ev, ok
}

// httpBase holds what both HTTP transports share.
type httpBase struct {
	opts   HTTPOptions
	rec    *Recorder
	in     *inbox
	ctx    context.Context
	cancel context.CancelFunc
	wg     sync.WaitGroup
}

func newHTTPBase(ctx context.Context, opts HTTPOptions, rec *Recorder) httpBase {
	if opts.Client == nil {
		opts.Client = &http.Client{}
	}
	lifetime, cancel := context.WithCancel(ctx)
	return httpBase{opts: opts, rec: rec, in: newInbox(), ctx: lifetime, cancel: cancel}
}

func (b *httpBase) newRequest(method, target string, body []byte) (*http.Request, error) {
	var r io.Reader
	if body != nil {
		r = bytes.NewReader(body)
	}
	req, err := http.NewRequestWithContext(b.ctx, method, target, r)
	if err != nil {
		return nil, err
	}
	for k, vs := range b.opts.Header {
		for _, v := range vs {
			req.Header.Add(k, v)
		}
	}
	return req, nil
}

// pushData decodes one SSE data payload or JSON body into the inbox.
func (b *httpBase) pushData(data []byte) {
	data = bytes.TrimSpace(data)
	if len(data) == 0 {
		return
	}
	b.rec.Trace("<-", data)
	msgs, err := decodeMessages(data)
	if err != nil {
		b.rec.Violation(mcpmodel.ViolationJSONRPC, "server sent a message that is not valid JSON-RPC: %v", err)
		return
	}
	for _, m := range msgs {
		b.in.push(m)
	}
}

// streamSSE consumes an SSE body in the background, pushing message
// events. onEnd runs when the stream finishes.
func (b *httpBase) streamSSE(body io.ReadCloser, onEvent func(sseEvent) bool, onEnd func(error)) {
	b.wg.Add(1)
	go func() {
		defer b.wg.Done()
		defer body.Close()
		err := readSSE(body, onEvent)
		if onEnd != nil {
			onEnd(err)
		}
	}()
}

func (b *httpBase) messageEvent(ev sseEvent) bool {
	switch ev.Event {
	case "", "message":
		b.pushData([]byte(ev.Data))
	default:
		b.rec.Tracef("ignoring SSE event %q", ev.Event)
	}
	return true
}

// Recv implements Transport.
func (b *httpBase) Recv(ctx context.Context) (Message, error) { return b.in.recv(ctx) }

// StreamableHTTP is the Streamable HTTP transport (protocol 2025-03-26+):
// every client message is a POST whose response is JSON or an SSE stream.
type StreamableHTTP struct {
	httpBase
	url string

	mu              sync.Mutex
	sessionID       string
	protocolVersion string
}

// NewStreamableHTTP returns a Streamable HTTP transport for endpoint.
func NewStreamableHTTP(ctx context.Context, endpoint string, opts HTTPOptions, rec *Recorder) *StreamableHTTP {
	return &StreamableHTTP{httpBase: newHTTPBase(ctx, opts, rec), url: endpoint}
}

// Name implements Transport.
func (t *StreamableHTTP) Name() string { return "streamable-http" }

// SetProtocolVersion implements versionAware.
func (t *StreamableHTTP) SetProtocolVersion(v string) {
	t.mu.Lock()
	t.protocolVersion = v
	t.mu.Unlock()
}

// Send implements Transport.
func (t *StreamableHTTP) Send(_ context.Context, m Message) error {
	raw, err := json.Marshal(m)
	if err != nil {
		return err
	}
	req, err := t.newRequest(http.MethodPost, t.url, raw)
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")
	t.mu.Lock()
	if t.sessionID != "" {
		req.Header.Set("Mcp-Session-Id", t.sessionID)
	}
	if t.protocolVersion != "" {
		req.Header.Set("MCP-Protocol-Version", t.protocolVersion)
	}
	t.mu.Unlock()

	t.rec.Trace("->", raw)
	resp, err := t.opts.Client.Do(req)
	if err != nil {
		return fmt.Errorf("POST %s: %w", t.url, err)
	}
	t.captureSession(m, resp)
	return t.handleResponse(m, req, resp)
}

func (t *StreamableHTTP) captureSession(m Message, resp *http.Response) {
	sid := resp.Header.Get("Mcp-Session-Id")
	if sid == "" {
		return
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.sessionID == "" && m.Method != "initialize" {
		t.rec.Violation(mcpmodel.ViolationTransport,
			"server assigned Mcp-Session-Id on a %s response; session ids must be assigned on the initialize response", m.Method)
	}
	if t.sessionID == "" {
		t.sessionID = sid
	}
}

func (t *StreamableHTTP) handleResponse(m Message, req *http.Request, resp *http.Response) error {
	if resp.StatusCode == http.StatusAccepted {
		_ = resp.Body.Close()
		if m.IsRequest() {
			t.rec.Violation(mcpmodel.ViolationTransport,
				"server answered the %s request with 202 Accepted; requests must get a JSON or SSE response", m.Method)
			return fmt.Errorf("POST %s answered the request with 202 Accepted and no response body, so the response can never arrive", t.url)
		}
		return nil
	}
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		defer resp.Body.Close()
		return t.statusFailure(m, req, resp)
	}
	switch mt := mediaType(resp.Header); mt {
	case "text/event-stream":
		t.streamSSE(resp.Body, t.messageEvent, nil)
		return nil
	case "application/json":
	default:
		if m.IsRequest() {
			t.rec.Violation(mcpmodel.ViolationTransport,
				"server answered %s with Content-Type %q; Streamable HTTP responses must be application/json or text/event-stream", m.Method, mt)
		}
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("read %s response: %w", m.Method, err)
	}
	t.pushData(body)
	return nil
}

// statusFailure turns a non-2xx response into an error, except when the
// body is a JSON-RPC error for this request: then the client gets the
// server's own explanation.
func (t *StreamableHTTP) statusFailure(m Message, req *http.Request, resp *http.Response) error {
	se := statusError(req, resp)
	if msgs, err := decodeMessages([]byte(se.Body)); err == nil && len(msgs) == 1 && msgs[0].Error != nil && m.IsRequest() {
		t.rec.Trace("<-", []byte(se.Body))
		if len(msgs[0].ID) == 0 {
			msgs[0].ID = m.ID
		}
		t.in.push(msgs[0])
		return nil
	}
	if resp.StatusCode == http.StatusNotFound && t.sessionID != "" {
		return fmt.Errorf("%w (the server no longer recognizes session %s)", se, t.sessionID)
	}
	return se
}

// Close implements Transport, terminating the session with DELETE when
// the server issued a session id.
func (t *StreamableHTTP) Close() error {
	t.mu.Lock()
	sid := t.sessionID
	t.mu.Unlock()
	if sid != "" {
		ctx, cancel := context.WithTimeout(context.WithoutCancel(t.ctx), 2*time.Second)
		req, err := http.NewRequestWithContext(ctx, http.MethodDelete, t.url, nil)
		if err == nil {
			req.Header.Set("Mcp-Session-Id", sid)
			if resp, err := t.opts.Client.Do(req); err == nil {
				_ = resp.Body.Close()
			}
		}
		cancel()
	}
	t.cancel()
	t.wg.Wait()
	return nil
}

// LegacySSE is the HTTP+SSE transport from protocol 2024-11-05: a
// long-lived GET stream carries server messages, and the first event
// names the endpoint the client POSTs its messages to.
type LegacySSE struct {
	httpBase
	url      string
	endpoint string
	ready    chan struct{}
	once     sync.Once
}

// DialLegacySSE opens the SSE stream at streamURL and waits until the
// server names its message endpoint.
func DialLegacySSE(ctx context.Context, streamURL string, opts HTTPOptions, rec *Recorder) (*LegacySSE, error) {
	t := &LegacySSE{httpBase: newHTTPBase(ctx, opts, rec), url: streamURL, ready: make(chan struct{})}
	req, err := t.newRequest(http.MethodGet, streamURL, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "text/event-stream")
	rec.Tracef("GET %s (legacy SSE)", streamURL)
	resp, err := t.opts.Client.Do(req)
	if err != nil {
		t.cancel()
		return nil, fmt.Errorf("GET %s: %w", streamURL, err)
	}
	if resp.StatusCode != http.StatusOK {
		defer resp.Body.Close()
		t.cancel()
		return nil, statusError(req, resp)
	}
	if mt := mediaType(resp.Header); mt != "text/event-stream" {
		_ = resp.Body.Close()
		t.cancel()
		return nil, fmt.Errorf("GET %s returned Content-Type %q, not an SSE stream", streamURL, mt)
	}
	t.streamSSE(resp.Body, t.onEvent, func(err error) {
		if err == nil {
			err = ErrServerClosed
		}
		t.in.close(fmt.Errorf("SSE stream ended: %w", err))
	})
	select {
	case <-t.ready:
		return t, nil
	case <-ctx.Done():
		t.cancel()
		return nil, fmt.Errorf("GET %s: server did not send an \"endpoint\" event: %w", streamURL, ctx.Err())
	}
}

func (t *LegacySSE) onEvent(ev sseEvent) bool {
	if ev.Event != "endpoint" {
		return t.messageEvent(ev)
	}
	ref, err := url.Parse(strings.TrimSpace(ev.Data))
	base, berr := url.Parse(t.url)
	if err != nil || berr != nil {
		t.rec.Violation(mcpmodel.ViolationTransport, "server sent an unparseable endpoint event %q", ev.Data)
		return true
	}
	t.once.Do(func() {
		t.endpoint = base.ResolveReference(ref).String()
		t.rec.Tracef("legacy SSE message endpoint: %s", t.endpoint)
		close(t.ready)
	})
	return true
}

// Name implements Transport.
func (t *LegacySSE) Name() string { return "sse" }

// Send implements Transport.
func (t *LegacySSE) Send(_ context.Context, m Message) error {
	raw, err := json.Marshal(m)
	if err != nil {
		return err
	}
	req, err := t.newRequest(http.MethodPost, t.endpoint, raw)
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	t.rec.Trace("->", raw)
	resp, err := t.opts.Client.Do(req)
	if err != nil {
		return fmt.Errorf("POST %s: %w", t.endpoint, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return statusError(req, resp)
	}
	// Responses belong on the SSE stream, but tolerate servers that
	// also answer inline.
	if mediaType(resp.Header) == "application/json" {
		body, _ := io.ReadAll(resp.Body)
		t.pushData(body)
	}
	return nil
}

// Close implements Transport.
func (t *LegacySSE) Close() error {
	t.cancel()
	t.wg.Wait()
	return nil
}
