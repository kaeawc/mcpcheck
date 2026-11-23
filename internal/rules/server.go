package rules

import (
	"fmt"

	"github.com/kaeawc/mcpcheck/internal/mcpmodel"
	v2 "github.com/kaeawc/mcpcheck/internal/v2"
)

// Server rules report what the live client observed on the wire. They are
// silent for static intakes, which have no Server.

func init() {
	v2.Register(&v2.Rule{
		ID:          "server-stdout-not-protocol",
		Category:    v2.CatProtocol,
		Severity:    v2.SevError,
		Description: "A stdio server must write only JSON-RPC messages to stdout; logs and banners there corrupt the transport for most clients.",
		Fix:         v2.FixNone,
		ProjectImplementation: func(pc *v2.ProjectContext) {
			s := pc.ToolSet.Server
			if s == nil || s.Transport != "stdio" || len(s.NonProtocolOutput) == 0 {
				return
			}
			pc.Report(fmt.Sprintf(
				"server wrote non-JSON-RPC output to stdout (first line: %q); send logs to stderr instead — "+
					"e.g. print(..., file=sys.stderr) or logging in Python, console.error in Node, log/os.Stderr in Go, eprintln!/tracing to stderr in Rust",
				s.NonProtocolOutput[0]))
		},
	})
	registerViolationRule("server-jsonrpc-malformed", mcpmodel.ViolationJSONRPC, v2.SevError,
		"Server messages must be well-formed JSON-RPC 2.0: correct jsonrpc version, matching response ids, exactly one of result or error.")
	registerViolationRule("server-protocol-version-unsupported", mcpmodel.ViolationProtocolVersion, v2.SevError,
		"The server must negotiate a published MCP protocol version in its initialize result.")
	registerViolationRule("server-lifecycle-nonconformant", mcpmodel.ViolationLifecycle, v2.SevWarning,
		"The initialize exchange must follow the spec: serverInfo with name and version, a capabilities object that declares tools, and no requests the client didn't enable.")
	registerViolationRule("server-transport-nonconformant", mcpmodel.ViolationTransport, v2.SevWarning,
		"The server must follow its transport's framing rules (newline-delimited stdio; Streamable HTTP status codes, content types, and session headers).")
	registerViolationRule("server-tools-pagination-broken", mcpmodel.ViolationPagination, v2.SevError,
		"tools/list pagination must terminate and use string cursors.")
}

// registerViolationRule registers a rule that reports every recorded
// violation of one kind.
func registerViolationRule(id string, kind mcpmodel.ViolationKind, sev v2.Severity, description string) {
	v2.Register(&v2.Rule{
		ID:          id,
		Category:    v2.CatProtocol,
		Severity:    sev,
		Description: description,
		Fix:         v2.FixNone,
		ProjectImplementation: func(pc *v2.ProjectContext) {
			s := pc.ToolSet.Server
			if s == nil {
				return
			}
			for _, v := range s.Violations {
				if v.Kind == kind {
					pc.Report(v.Message)
				}
			}
		},
	})
}
