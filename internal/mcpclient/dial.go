package mcpclient

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
)

// HTTP transport selections for DialHTTP.
const (
	TransportAuto           = "auto"
	TransportStreamableHTTP = "streamable-http"
	TransportSSE            = "sse"
)

// DialHTTP connects to an MCP server over HTTP. With TransportAuto it
// follows the spec's backwards-compatibility procedure: POST the first
// message as Streamable HTTP, and if the server answers 400, 404, or 405,
// fall back to the legacy HTTP+SSE transport at the same URL. URLs whose
// path ends in /sse go straight to the legacy transport.
func DialHTTP(ctx context.Context, endpoint, mode string, opts HTTPOptions, rec *Recorder) (Transport, error) {
	u, err := url.Parse(endpoint)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return nil, fmt.Errorf("invalid server URL %q: want http(s)://host[:port]/path", endpoint)
	}
	switch mode {
	case TransportSSE:
		return DialLegacySSE(ctx, endpoint, opts, rec)
	case TransportStreamableHTTP:
		return NewStreamableHTTP(ctx, endpoint, opts, rec), nil
	case TransportAuto, "":
		if strings.HasSuffix(strings.TrimRight(u.Path, "/"), "/sse") {
			return DialLegacySSE(ctx, endpoint, opts, rec)
		}
		return &fallback{ctx: ctx, url: endpoint, opts: opts, rec: rec,
			cur: NewStreamableHTTP(ctx, endpoint, opts, rec)}, nil
	}
	return nil, fmt.Errorf("unknown transport %q (want auto, streamable-http, or sse)", mode)
}

// fallback starts as Streamable HTTP and switches to legacy SSE if the
// very first POST is rejected the way pre-2025-03-26 servers reject it.
type fallback struct {
	ctx     context.Context
	url     string
	opts    HTTPOptions
	rec     *Recorder
	cur     Transport
	decided bool
}

func (f *fallback) Name() string { return f.cur.Name() }

func (f *fallback) Send(ctx context.Context, m Message) error {
	err := f.cur.Send(ctx, m)
	if f.decided {
		return err
	}
	f.decided = true
	var se *HTTPStatusError
	if err == nil || !errors.As(err, &se) || !legacyStatus(se.Status) {
		return err
	}
	f.rec.Tracef("POST got HTTP %d; falling back to legacy HTTP+SSE", se.Status)
	legacy, lerr := DialLegacySSE(f.ctx, f.url, f.opts, f.rec)
	if lerr != nil {
		return fmt.Errorf("%w; legacy HTTP+SSE fallback also failed: %w", err, lerr)
	}
	_ = f.cur.Close()
	f.cur = legacy
	return f.cur.Send(ctx, m)
}

func legacyStatus(code int) bool {
	return code == http.StatusBadRequest || code == http.StatusNotFound || code == http.StatusMethodNotAllowed
}

func (f *fallback) Recv(ctx context.Context) (Message, error) { return f.cur.Recv(ctx) }

func (f *fallback) Close() error { return f.cur.Close() }

func (f *fallback) SetProtocolVersion(v string) {
	if va, ok := f.cur.(versionAware); ok {
		va.SetProtocolVersion(v)
	}
}
