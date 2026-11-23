// Live-mode intake — connects to a running MCP server and consumes its
// tools/list response. Catches tools that are registered dynamically
// (runtime introspection, plugin loading) and so don't appear in any
// static intake, and records how the server behaves on the wire.
//
// The protocol work lives in internal/mcpclient; this file maps CLI-level
// options onto a transport.
package scanner

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/kaeawc/mcpcheck/internal/mcpclient"
	"github.com/kaeawc/mcpcheck/internal/mcpmodel"
	"github.com/kaeawc/mcpcheck/internal/proc"
)

// LiveOptions selects and configures the server to connect to. Exactly
// one of Command and URL must be set.
type LiveOptions struct {
	// Command is the argv of a server speaking MCP over stdio.
	Command []string
	// Dir is the working directory for Command.
	Dir string

	// URL is the endpoint of a server speaking MCP over HTTP.
	URL string
	// Transport is mcpclient.TransportAuto (default),
	// TransportStreamableHTTP, or TransportSSE.
	Transport string
	// Header is sent with every HTTP request.
	Header http.Header

	// ProtocolVersion is offered in initialize; empty means latest.
	ProtocolVersion string
	// ClientVersion is reported in clientInfo.
	ClientVersion string

	// Trace, when non-nil, receives a log of every wire message.
	Trace io.Writer
	// ServerStderr, when non-nil, receives the stdio server's stderr as
	// it is written. It is always retained for error reports.
	ServerStderr io.Writer

	Spawner    proc.Spawner
	HTTPClient *http.Client
}

// LoadLive connects to the server described by opts, runs the MCP
// handshake and a paginated tools/list, and disconnects.
func LoadLive(ctx context.Context, opts LiveOptions) (*mcpmodel.ToolSet, error) {
	rec := mcpclient.NewRecorder(opts.Trace)
	t, endpoint, err := dialLive(ctx, opts, rec)
	if err != nil {
		return nil, err
	}
	defer t.Close()
	return mcpclient.FetchTools(ctx, t, rec, endpoint, mcpclient.Options{
		ProtocolVersion: opts.ProtocolVersion,
		ClientName:      "mcpcheck",
		ClientVersion:   opts.ClientVersion,
	})
}

func dialLive(ctx context.Context, opts LiveOptions, rec *mcpclient.Recorder) (mcpclient.Transport, string, error) {
	hasCmd, hasURL := len(opts.Command) > 0, opts.URL != ""
	switch {
	case hasCmd && hasURL:
		return nil, "", errors.New("live intake: set a command or a URL, not both")
	case hasURL:
		t, err := mcpclient.DialHTTP(ctx, opts.URL, opts.Transport,
			mcpclient.HTTPOptions{Client: opts.HTTPClient, Header: opts.Header}, rec)
		return t, opts.URL, err
	case hasCmd:
		spawner := opts.Spawner
		if spawner == nil {
			spawner = proc.OS{}
		}
		cmd := proc.Cmd{Name: opts.Command[0], Args: opts.Command[1:], Dir: opts.Dir}
		t, err := mcpclient.SpawnStdio(ctx, spawner, cmd, rec, opts.ServerStderr)
		return t, strings.Join(opts.Command, " "), err
	}
	return nil, "", fmt.Errorf("live intake: no command or URL given")
}
