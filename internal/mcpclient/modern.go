package mcpclient

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/kaeawc/mcpcheck/internal/mcpmodel"
)

// Modern-era (2026-07-28+) support: there is no handshake; every request
// carries its version and client identity in _meta, and server/discover
// both describes the server and, per the spec's compatibility procedure,
// tells a dual-era client whether to fall back to initialize.

const (
	metaProtocolVersion    = "io.modelcontextprotocol/protocolVersion"
	metaClientInfo         = "io.modelcontextprotocol/clientInfo"
	metaClientCapabilities = "io.modelcontextprotocol/clientCapabilities"
	metaServerInfo         = "io.modelcontextprotocol/serverInfo"
)

// Modern error codes. The pre-renumbering draft codes are accepted for
// UnsupportedProtocolVersion only when the error carries a supported list.
const (
	codeHeaderMismatch             = -32020
	codeMissingClientCapability    = -32021
	codeUnsupportedProtocolVersion = -32022
	codeDraftUnsupportedVersion    = -32004
)

// defaultProbeTimeout bounds the server/discover probe; a legacy server
// may silently ignore an unknown pre-initialize request.
const defaultProbeTimeout = 5 * time.Second

// errLegacyOnly reports a server that doesn't speak the modern protocol
// when the caller pinned a modern version.
var errLegacyOnly = errors.New("the server does not implement server/discover, so it only speaks the legacy (initialize-based) protocol")

func (c *client) withMeta(params map[string]any) map[string]any {
	out := make(map[string]any, len(params)+1)
	for k, v := range params {
		out[k] = v
	}
	out["_meta"] = map[string]any{
		metaProtocolVersion:    c.server.ProtocolVersion,
		metaClientInfo:         c.clientInfo(),
		metaClientCapabilities: map[string]any{},
	}
	return out
}

// tryModern probes with server/discover. It returns modern=true once a
// modern version is established, or modern=false with the legacy version
// to offer in initialize. strict forbids falling back.
func (c *client) tryModern(ctx context.Context, offer string, strict bool) (bool, string, error) {
	if c.t.Name() == "sse" {
		return c.fallBack(strict, "the legacy HTTP+SSE transport predates server/discover", LatestLegacyProtocolVersion)
	}
	for attempt := 0; attempt < 2; attempt++ {
		c.startModern(offer)
		raw, err := c.probe(ctx)
		var next, legacy string
		var done bool
		if err == nil {
			if err := c.parseDiscover(raw); err != nil {
				return false, "", err
			}
			if contains(c.server.SupportedVersions, offer) {
				return true, "", nil
			}
			next, legacy, done, err = c.chooseFromSupported(c.server.SupportedVersions, strict)
		} else {
			next, legacy, done, err = c.classifyProbeError(ctx, err, strict)
		}
		if done {
			return false, legacy, err
		}
		offer = next
	}
	return false, "", fmt.Errorf("server rejected every protocol version it advertised")
}

func (c *client) startModern(version string) {
	c.modern = true
	c.server.Era = mcpmodel.EraModern
	c.server.OfferedProtocolVersion = version
	c.server.ProtocolVersion = version
	c.setTransportVersion(version)
}

func (c *client) probe(ctx context.Context) (json.RawMessage, error) {
	timeout := c.opts.ProbeTimeout
	if timeout <= 0 {
		timeout = defaultProbeTimeout
	}
	pctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	raw, err := c.call(pctx, "server/discover", nil)
	if err != nil && errors.Is(err, context.DeadlineExceeded) && ctx.Err() == nil {
		c.abandoned[normalizeID(json.RawMessage(fmt.Sprint(c.nextID)))] = true
		return nil, fmt.Errorf("no answer to server/discover within %s: %w", timeout, errProbeTimeout)
	}
	return raw, err
}

var errProbeTimeout = errors.New("probe timed out")

// classifyProbeError decides what a failed probe means. It returns a
// modern version to retry with, or done=true with either a legacy
// version to fall back to or a fatal error.
func (c *client) classifyProbeError(ctx context.Context, err error, strict bool) (retry, legacy string, done bool, fatal error) {
	var rpcErr *RPCError
	isRPC := errors.As(err, &rpcErr)
	switch {
	case ctx.Err() != nil, errors.Is(err, ErrServerClosed), isAuthError(err):
		return "", "", true, err
	case isRPC && isUnsupportedVersion(rpcErr):
		return c.chooseFromSupported(supportedFromError(rpcErr), strict)
	case isRPC && isModernError(rpcErr):
		return "", "", true, fmt.Errorf("modern server rejected the request: %w", err)
	}
	_, legacy, fatal = c.fallBack(strict, describeProbeFailure(err), LatestLegacyProtocolVersion)
	return "", legacy, true, fatal
}

// fallBack switches the client to the legacy era.
func (c *client) fallBack(strict bool, reason, version string) (bool, string, error) {
	if strict {
		return false, "", fmt.Errorf("%w (%s)", errLegacyOnly, reason)
	}
	c.modern = false
	c.server.LegacyFallback = reason
	c.server.ProtocolVersion = ""
	c.setTransportVersion("")
	c.rec.Tracef("falling back to initialize: %s", reason)
	return false, version, nil
}

func describeProbeFailure(err error) string {
	var rpcErr *RPCError
	var se *HTTPStatusError
	switch {
	case errors.Is(err, errProbeTimeout):
		return "server/discover got no answer"
	case errors.As(err, &rpcErr):
		return fmt.Sprintf("server/discover failed with error %d (%s)", rpcErr.Code, rpcErr.Message)
	case errors.As(err, &se):
		return fmt.Sprintf("server/discover got HTTP %d", se.Status)
	}
	return "server/discover failed: " + err.Error()
}

func isAuthError(err error) bool {
	var se *HTTPStatusError
	return errors.As(err, &se) && (se.Status == http.StatusUnauthorized || se.Status == http.StatusForbidden)
}

func isModernError(e *RPCError) bool {
	return e.Code == codeHeaderMismatch || e.Code == codeMissingClientCapability || isUnsupportedVersion(e)
}

func isUnsupportedVersion(e *RPCError) bool {
	if e.Code != codeUnsupportedProtocolVersion && e.Code != codeDraftUnsupportedVersion {
		return false
	}
	return len(supportedFromError(e)) > 0 || e.Code == codeUnsupportedProtocolVersion
}

func supportedFromError(e *RPCError) []string {
	var data struct {
		Supported []string `json:"supported"`
	}
	_ = json.Unmarshal(e.Data, &data)
	return data.Supported
}

// chooseFromSupported picks the newest version both sides speak from the
// list in an UnsupportedProtocolVersionError or DiscoverResult.
func (c *client) chooseFromSupported(supported []string, strict bool) (string, string, bool, error) {
	c.server.SupportedVersions = supported
	if len(supported) == 0 {
		_, legacy, err := c.fallBack(strict, "server/discover listed no supported versions", LatestLegacyProtocolVersion)
		return "", legacy, true, err
	}
	sorted := append([]string(nil), supported...)
	sort.Sort(sort.Reverse(sort.StringSlice(sorted)))
	for _, v := range sorted {
		if contains(ModernProtocolVersions, v) && v != c.server.ProtocolVersion {
			return v, "", false, nil
		}
	}
	for _, v := range sorted {
		if contains(LegacyProtocolVersions, v) {
			_, legacy, err := c.fallBack(strict, fmt.Sprintf("the server supports only %s", strings.Join(supported, ", ")), v)
			return "", legacy, true, err
		}
	}
	return "", "", true, fmt.Errorf("server supports protocol versions %v, none of which mcpcheck speaks (%s)",
		supported, strings.Join(KnownProtocolVersions, ", "))
}

func (c *client) parseDiscover(raw json.RawMessage) error {
	var res struct {
		SupportedVersions []string       `json:"supportedVersions"`
		Capabilities      map[string]any `json:"capabilities"`
		Instructions      string         `json:"instructions"`
		Meta              map[string]any `json:"_meta"`
	}
	if err := json.Unmarshal(raw, &res); err != nil {
		return fmt.Errorf("parse server/discover result: %w", err)
	}
	s := c.server
	s.SupportedVersions, s.Capabilities, s.Instructions = res.SupportedVersions, res.Capabilities, res.Instructions
	if info, ok := res.Meta[metaServerInfo].(map[string]any); ok {
		s.Name, _ = info["name"].(string)
		s.Version, _ = info["version"].(string)
		s.Title, _ = info["title"].(string)
	}
	c.checkDiscover()
	c.checkCacheable("server/discover", raw)
	return nil
}

func (c *client) checkDiscover() {
	s := c.server
	switch {
	case len(s.SupportedVersions) == 0:
		c.rec.Violation(mcpmodel.ViolationProtocolVersion, "server/discover result has no supportedVersions")
	case !contains(s.SupportedVersions, s.ProtocolVersion):
		c.rec.Violation(mcpmodel.ViolationProtocolVersion,
			"server answered a %s server/discover request although its supportedVersions %v omit that version; it must return UnsupportedProtocolVersionError (-32022) instead",
			s.ProtocolVersion, s.SupportedVersions)
	}
	if s.Capabilities == nil {
		c.rec.Violation(mcpmodel.ViolationLifecycle, "server/discover result has no capabilities object")
	} else if !s.HasCapability("tools") {
		c.rec.Violation(mcpmodel.ViolationLifecycle,
			"server did not declare the \"tools\" capability in server/discover; spec-following clients will never call tools/list")
	}
	if s.Name == "" || s.Version == "" {
		c.rec.Violation(mcpmodel.ViolationLifecycle,
			"server/discover result should identify the server in _meta[%q] with name and version", metaServerInfo)
	}
}

// checkResultType enforces that modern results say whether they are
// complete; tools/list can't continue on an input_required result.
func (c *client) checkResultType(method string, raw json.RawMessage) (json.RawMessage, error) {
	var res struct {
		ResultType *string `json:"resultType"`
	}
	_ = json.Unmarshal(raw, &res)
	switch {
	case res.ResultType == nil:
		c.rec.Violation(mcpmodel.ViolationResultShape,
			"%s result has no resultType; under protocol %s every result must carry resultType \"complete\" or \"input_required\"", method, c.server.ProtocolVersion)
	case *res.ResultType == "input_required":
		return nil, fmt.Errorf("server answered %s with an input_required result; listing tools must not need client input", method)
	case *res.ResultType != "complete":
		c.rec.Violation(mcpmodel.ViolationResultShape, "%s result has unknown resultType %q", method, *res.ResultType)
	}
	return raw, nil
}

// checkCacheable enforces the CacheableResult fields required on list
// and discover results.
func (c *client) checkCacheable(method string, raw json.RawMessage) {
	var res struct {
		TTL        any `json:"ttlMs"`
		CacheScope any `json:"cacheScope"`
	}
	_ = json.Unmarshal(raw, &res)
	if ttl, ok := res.TTL.(float64); !ok || ttl < 0 {
		c.rec.Violation(mcpmodel.ViolationResultShape, "%s result must include a non-negative integer ttlMs (got %v)", method, res.TTL)
	}
	if scope, _ := res.CacheScope.(string); scope != "public" && scope != "private" {
		c.rec.Violation(mcpmodel.ViolationResultShape, "%s result must include cacheScope \"public\" or \"private\" (got %v)", method, res.CacheScope)
	}
}

func (c *client) checkNotification(m Message) {
	if c.modern && m.Method == "notifications/message" {
		c.rec.Violation(mcpmodel.ViolationLifecycle,
			"server sent notifications/message although the request did not set _meta[\"io.modelcontextprotocol/logLevel\"]")
	}
}
