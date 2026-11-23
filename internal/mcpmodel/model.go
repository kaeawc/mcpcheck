package mcpmodel

// Tool is the analyzer's normalized view of a single MCP tool advertisement.
//
// Different intake paths populate it differently: protocol intakes (a
// tools/list response on disk, or a live server) fill the advertised fields
// and keep the original object in Raw; source intakes (Python, TypeScript,
// Go, Rust) fill what they can recover from the registration site plus the
// source coordinates, and leave Raw nil.
type Tool struct {
	Name         string
	Title        string
	Description  string
	InputSchema  map[string]any
	OutputSchema map[string]any
	Annotations  map[string]any

	// Raw is the tool object exactly as the server advertised it. Nil for
	// source intakes, which is how rules tell "the server omitted this
	// field" apart from "the source intake could not recover it".
	Raw map[string]any

	SourceFile string
	SourceLine int
}

// ToolSet is what an intake produces and what the dispatcher consumes.
type ToolSet struct {
	Tools  []Tool
	Source string

	// Server describes the live session the tools came from. Nil for
	// static intakes.
	Server *Server
}

// Server records what a live MCP session observed beyond the tool list:
// negotiated protocol, server identity, and protocol-level problems seen on
// the wire. Rules read it to give feedback on the server's behavior.
type Server struct {
	// Transport is "stdio", "streamable-http", or "sse".
	Transport string `json:"transport,omitempty"`
	// Endpoint is the command line (stdio) or URL (HTTP) that was dialed.
	Endpoint string `json:"endpoint,omitempty"`

	OfferedProtocolVersion string `json:"offeredProtocolVersion,omitempty"`
	ProtocolVersion        string `json:"protocolVersion,omitempty"`

	Name         string         `json:"name,omitempty"`
	Version      string         `json:"version,omitempty"`
	Title        string         `json:"title,omitempty"`
	Instructions string         `json:"instructions,omitempty"`
	Capabilities map[string]any `json:"capabilities,omitempty"`

	// ToolPages is how many tools/list pages the server returned.
	ToolPages int `json:"toolPages,omitempty"`

	// NonProtocolOutput holds stdout lines that were not JSON-RPC
	// messages. On stdio that output corrupts the transport for clients
	// that are less forgiving than mcpcheck.
	NonProtocolOutput []string `json:"nonProtocolOutput,omitempty"`

	Violations []Violation `json:"violations,omitempty"`
}

// ViolationKind groups protocol violations so each kind maps to one rule.
type ViolationKind string

// Violation kinds recorded by the live client.
const (
	ViolationJSONRPC         ViolationKind = "jsonrpc"
	ViolationProtocolVersion ViolationKind = "protocol-version"
	ViolationLifecycle       ViolationKind = "lifecycle"
	ViolationTransport       ViolationKind = "transport"
	ViolationPagination      ViolationKind = "pagination"
)

// Violation is one protocol-level problem observed during a live session.
type Violation struct {
	Kind    ViolationKind `json:"kind"`
	Message string        `json:"message"`
}

// HasCapability reports whether the server declared the named top-level
// capability (e.g. "tools") in its initialize result.
func (s *Server) HasCapability(name string) bool {
	if s == nil || s.Capabilities == nil {
		return false
	}
	_, ok := s.Capabilities[name]
	return ok
}

// ToolFromJSON builds a Tool from a tool object as it appears in a
// tools/list result, keeping the original object in Raw.
func ToolFromJSON(m map[string]any) Tool {
	t := Tool{Raw: m}
	t.Name, _ = m["name"].(string)
	t.Title, _ = m["title"].(string)
	t.Description, _ = m["description"].(string)
	t.InputSchema, _ = m["inputSchema"].(map[string]any)
	t.OutputSchema, _ = m["outputSchema"].(map[string]any)
	t.Annotations, _ = m["annotations"].(map[string]any)
	if t.Title == "" && t.Annotations != nil {
		t.Title, _ = t.Annotations["title"].(string)
	}
	return t
}
