package rules

import (
	"fmt"
	"regexp"
	"sort"
	"strings"

	"github.com/kaeawc/mcpcheck/internal/mcpmodel"
	v2 "github.com/kaeawc/mcpcheck/internal/v2"
)

// Rules for protocol revision 2026-07-28 (the stateless "modern" era).

// headerTokenRe is RFC 9110 field-name token syntax (1*tchar).
var headerTokenRe = regexp.MustCompile("^[!#$%&'*+\\-.^_`|~0-9A-Za-z]+$")

// headerParamTypes are the JSON Schema types x-mcp-header may annotate.
var headerParamTypes = map[string]bool{"string": true, "integer": true, "boolean": true}

func init() {
	registerViolationRule("server-result-shape-nonconformant", mcpmodel.ViolationResultShape, v2.SevWarning,
		"Under protocol 2026-07-28 results must carry resultType, and list/discover results must carry ttlMs and cacheScope.")
	v2.Register(&v2.Rule{
		ID:          "server-legacy-protocol-only",
		Category:    v2.CatProtocol,
		Severity:    v2.SevInfo,
		Description: "The server speaks only the legacy initialize-based protocol (2025-11-25 and earlier); clients that implement only 2026-07-28 cannot connect to it.",
		Fix:         v2.FixNone,
		ProjectImplementation: func(pc *v2.ProjectContext) {
			s := pc.ToolSet.Server
			if s == nil || s.LegacyFallback == "" {
				return
			}
			if s.Transport == "sse" {
				pc.Report("connected over the deprecated HTTP+SSE transport (2024-11-05), which only carries the legacy protocol; " +
					"serve Streamable HTTP so clients that implement only protocol 2026-07-28 can connect")
				return
			}
			pc.Report(fmt.Sprintf(
				"server only speaks the legacy protocol (%s; negotiated %s via initialize); current SDKs serve both eras — "+
					"TypeScript @modelcontextprotocol/server v2, Python mcp 2.x / fastmcp, rmcp, mcp-go",
				s.LegacyFallback, s.ProtocolVersion))
		},
	})
	v2.Register(&v2.Rule{
		ID:             "tool-mcp-header-invalid",
		Category:       v2.CatProtocol,
		Severity:       v2.SevError,
		Description:    "x-mcp-header annotations in an inputSchema must name a unique, valid header for a statically reachable string, integer, or boolean property; clients drop tools that violate this.",
		Fix:            v2.FixNone,
		Implementation: checkMCPHeaders,
	})
}

func checkMCPHeaders(ctx *v2.Context) {
	schema := ctx.Tool.InputSchema
	if schema == nil {
		return
	}
	reachable := map[string]bool{}
	seen := map[string]string{}
	var problems []string
	walkReachable(schema, "", func(path string, prop map[string]any) {
		h, ok := prop["x-mcp-header"]
		if !ok {
			return
		}
		reachable[path] = true
		problems = append(problems, headerProblems(path, h, prop, seen)...)
	})
	walkAll(schema, "", func(path string, node map[string]any) {
		if _, ok := node["x-mcp-header"]; ok && !reachable[path] && path != "" {
			problems = append(problems, fmt.Sprintf("x-mcp-header at %s is not reachable through properties alone (items, anyOf/oneOf/allOf, if/then/else, and $ref are not allowed)", path))
		}
	})
	sort.Strings(problems)
	for _, p := range problems {
		ctx.Report(p + "; Streamable HTTP clients must exclude this tool from tools/list")
	}
}

func headerProblems(path string, h any, prop map[string]any, seen map[string]string) []string {
	name, ok := h.(string)
	switch {
	case !ok || name == "":
		return []string{fmt.Sprintf("x-mcp-header at %s must be a non-empty string", path)}
	case !headerTokenRe.MatchString(name):
		return []string{fmt.Sprintf("x-mcp-header %q at %s is not a valid HTTP header name token", name, path)}
	}
	var out []string
	if prev, dup := seen[strings.ToLower(name)]; dup {
		out = append(out, fmt.Sprintf("x-mcp-header %q at %s duplicates the one at %s (names are case-insensitive)", name, path, prev))
	}
	seen[strings.ToLower(name)] = path
	if t, _ := prop["type"].(string); !headerParamTypes[t] {
		out = append(out, fmt.Sprintf("x-mcp-header %q at %s annotates a %q property; only string, integer, and boolean are allowed", name, path, prop["type"]))
	}
	return out
}

// walkReachable visits properties reachable from the root through
// "properties" keys only.
func walkReachable(schema map[string]any, path string, visit func(string, map[string]any)) {
	props, _ := schema["properties"].(map[string]any)
	for _, name := range sortedKeys(props) {
		prop, ok := props[name].(map[string]any)
		if !ok {
			continue
		}
		p := path + "/properties/" + name
		visit(p, prop)
		walkReachable(prop, p, visit)
	}
}

// walkAll visits every schema object nested anywhere in schema.
func walkAll(node any, path string, visit func(string, map[string]any)) {
	switch n := node.(type) {
	case map[string]any:
		visit(path, n)
		for _, k := range sortedKeys(n) {
			walkAll(n[k], path+"/"+k, visit)
		}
	case []any:
		for i, e := range n {
			walkAll(e, fmt.Sprintf("%s/%d", path, i), visit)
		}
	}
}

func sortedKeys(m map[string]any) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
