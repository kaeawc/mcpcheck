// Python intake for the official SDK (FastMCP in mcp 1.x, MCPServer in
// mcp 2.x, and the low-level Server), the standalone fastmcp package, and
// hand-rolled servers. Declarations are located with regexes and read
// with the shared literal parser.
//
// Recognized patterns:
//
//	@mcp.tool / @mcp.tool() / @mcp.tool("name") / @mcp.tool(name=..., description=...,
//	    title=..., annotations=ToolAnnotations(...) or {...}) on a def or async def
//	mcp.add_tool(func, name=..., description=...)
//	types.Tool(name=..., description=..., inputSchema={...}) (low-level server)
//	{"name": "x", "description": "...", "inputSchema": {...}} (dict literals)
//
// For decorated functions the description is the decorator's
// description= or the cleaned docstring, and the input schema is derived
// from the signature the way FastMCP does: str/int/float/bool/list/dict,
// Optional and X | None, Literal (enum), Annotated[..., Field(...)] and
// Field(...) defaults (description, pattern, bounds), with parameters
// typed Context excluded and parameters without defaults required.
//
// Limitations: Pydantic model and Enum parameters become {} (any), and
// names or descriptions computed at runtime are not recovered.
package scanner

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/kaeawc/mcpcheck/internal/mcpmodel"
)

var (
	// pyToolDecoratorRe matches `@mcp.tool`, `@server.tool(`, `@app.mcp.tool`.
	pyToolDecoratorRe = regexp.MustCompile(`(?m)^[ \t]*@[ \t]*[A-Za-z_][\w.]*\.tool\b`)
	// pyAddToolRe matches `.add_tool(func`.
	pyAddToolRe = regexp.MustCompile(`\.add_tool\(\s*([A-Za-z_]\w*)`)
	// pyToolCallRe matches `Tool(` and `types.Tool(`.
	pyToolCallRe = regexp.MustCompile(`\bTool\s*\(`)
	// pyDefRe matches a def line; group 1 is the function name.
	pyDefRe = regexp.MustCompile(`(?m)^[ \t]*(?:async[ \t]+)?def[ \t]+([A-Za-z_]\w*)[ \t]*\(`)
)

func isPythonSourceFile(path string) bool { return filepath.Ext(path) == ".py" }

// LoadPython scans a .py file or a directory tree for tool registrations.
func LoadPython(path string) (*mcpmodel.ToolSet, error) {
	set := &mcpmodel.ToolSet{Source: path}
	err := walkSources(path, isPythonSourceFile, func(p string) error {
		raw, err := os.ReadFile(p) // #nosec G304 -- scanning user-named sources is the point
		if err != nil {
			return fmt.Errorf("read %s: %w", p, err)
		}
		set.Tools = append(set.Tools, scanPython(p, string(raw))...)
		return nil
	})
	return set, err
}

// scanPython extracts tools from one source file.
func scanPython(path, src string) []mcpmodel.Tool {
	var tools []mcpmodel.Tool
	for _, m := range pyToolDecoratorRe.FindAllStringIndex(src, -1) {
		if t, ok := pyDecoratedTool(src, m[1]); ok {
			t.SourceFile, t.SourceLine = path, lineAt(src, m[0])
			tools = append(tools, t)
		}
	}
	tools = append(tools, pyAddTools(path, src)...)
	tools = append(tools, pyToolCalls(path, src)...)
	return append(tools, objectLiteralTools(path, src, langPython, literalSchema)...)
}

// pyDecoratedTool reads the decorator arguments starting at pos (just
// after `.tool`) and the function they decorate.
func pyDecoratedTool(src string, pos int) (mcpmodel.Tool, bool) {
	p := newLitParser(src, pos, langPython)
	p.wsInline()
	var args []any
	var kwargs map[string]any
	if p.peek() == '(' {
		args, kwargs = p.callArgs()
	}
	def, ok := pyFindDef(src, p.pos)
	if !ok {
		return mcpmodel.Tool{}, false
	}
	if len(args) > 0 {
		if name, ok := asString(args[0]); ok {
			kwargs = withDefault(kwargs, "name", name)
		}
	}
	return pyToolFromDef(src, def, kwargs), true
}

func withDefault(m map[string]any, key string, v any) map[string]any {
	if m == nil {
		m = map[string]any{}
	}
	if _, ok := m[key]; !ok {
		m[key] = v
	}
	return m
}

// pyFindDef returns the def match following a decorator, skipping blank
// lines, comments, and further (possibly multi-line) decorators.
func pyFindDef(src string, pos int) ([]int, bool) {
	for pos < len(src) {
		pos = nextLine(src, pos)
		rest := strings.TrimLeft(src[pos:], " \t")
		switch {
		case rest == "" || strings.HasPrefix(rest, "\n") || strings.HasPrefix(rest, "#"):
			continue
		case strings.HasPrefix(rest, "@"):
			pos = skipDecorator(src, pos)
			continue
		}
		m := pyDefRe.FindStringSubmatchIndex(src[pos:])
		if m == nil || m[0] != 0 {
			return nil, false
		}
		for i := range m {
			m[i] += pos
		}
		return m, true
	}
	return nil, false
}

// nextLine returns the offset just past the next newline at or after pos.
func nextLine(src string, pos int) int {
	if i := strings.IndexByte(src[pos:], '\n'); i >= 0 {
		return pos + i + 1
	}
	return len(src)
}

// skipDecorator advances past a decorator line, including a bracketed
// argument list that spans lines; it returns the start of that last line.
func skipDecorator(src string, pos int) int {
	end := nextLine(src, pos)
	if open := strings.IndexByte(src[pos:end], '('); open >= 0 {
		p := newLitParser(src, pos+open, langPython)
		p.skipBalanced()
		end = p.pos
	}
	return lineStart(src, end)
}

func lineStart(src string, pos int) int {
	return strings.LastIndexByte(src[:pos], '\n') + 1
}

// pyToolFromDef builds a tool from a def match and decorator/add_tool
// keyword arguments.
func pyToolFromDef(src string, def []int, kwargs map[string]any) mcpmodel.Tool {
	funcName := src[def[2]:def[3]]
	params, bodyPos := pyParams(src, def[1]-1)
	t := mcpmodel.Tool{Name: funcName, InputSchema: pySchema(params)}
	if name, ok := asString(kwargs["name"]); ok && name != "" {
		t.Name = name
	}
	t.Title, _ = asString(kwargs["title"])
	t.Description, _ = asString(kwargs["description"])
	if t.Description == "" {
		t.Description = pyDocstring(src, bodyPos)
	}
	if v, ok := kwargs["annotations"]; ok {
		t.Annotations = annotationsValue(v)
	}
	return t
}

// pyAddTools handles mcp.add_tool(func, name=..., description=...).
func pyAddTools(path, src string) []mcpmodel.Tool {
	defs := map[string][]int{}
	for _, m := range pyDefRe.FindAllStringSubmatchIndex(src, -1) {
		defs[src[m[2]:m[3]]] = m
	}
	var tools []mcpmodel.Tool
	for _, m := range pyAddToolRe.FindAllStringSubmatchIndex(src, -1) {
		def, ok := defs[src[m[2]:m[3]]]
		if !ok {
			continue
		}
		_, kwargs := newLitParser(src, m[0]+len(".add_tool"), langPython).callArgs()
		t := pyToolFromDef(src, def, kwargs)
		t.SourceFile, t.SourceLine = path, lineAt(src, m[0])
		tools = append(tools, t)
	}
	return tools
}

// pyToolCalls handles low-level types.Tool(name=..., inputSchema=...).
func pyToolCalls(path, src string) []mcpmodel.Tool {
	var tools []mcpmodel.Tool
	for _, m := range pyToolCallRe.FindAllStringIndex(src, -1) {
		_, kwargs := newLitParser(src, m[1]-1, langPython).callArgs()
		if t, ok := toolFromFields(kwargs, literalSchema); ok {
			t.SourceFile, t.SourceLine = path, lineAt(src, m[0])
			tools = append(tools, t)
		}
	}
	return tools
}

// pyDocstring returns the cleaned docstring of the body at pos, the
// way inspect.cleandoc presents it.
func pyDocstring(src string, pos int) string {
	p := newLitParser(src, pos, langPython)
	p.ws()
	prefixed := isPyStringPrefix(string(p.peek())) && p.isQuote(p.peekNext())
	if !p.isQuote(p.peek()) && !prefixed {
		return ""
	}
	if prefixed {
		p.pos++
	}
	doc, ok := p.stringLit().(string)
	if !ok {
		return ""
	}
	return cleandoc(doc)
}

// cleandoc mirrors Python's inspect.cleandoc.
func cleandoc(doc string) string {
	lines := strings.Split(strings.ReplaceAll(doc, "\t", "        "), "\n")
	indent := -1
	for _, l := range lines[1:] {
		if trimmed := strings.TrimLeft(l, " "); trimmed != "" {
			if n := len(l) - len(trimmed); indent < 0 || n < indent {
				indent = n
			}
		}
	}
	lines[0] = strings.TrimSpace(lines[0])
	for i := 1; i < len(lines); i++ {
		if indent > 0 && len(lines[i]) >= indent {
			lines[i] = lines[i][indent:]
		}
		lines[i] = strings.TrimRight(lines[i], " ")
	}
	return strings.Trim(strings.Join(lines, "\n"), "\n")
}
